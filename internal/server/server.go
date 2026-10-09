// Package server builds the single HTTP handler that both the native window and the browser mode serve: the
// Connect services, and the interface for every other path.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// TilasmPrefix is where the tilasms' files are served, each tilasm's under /tilasm/<id>/ (plan.Tilasms.Files).
const TilasmPrefix = "/tilasm/"

// Handler serves each Connect service at its path prefix, as returned by a generated New…Handler, and the
// interface from ui for every other path.
//
// The services never compress a response: every client of this server is on the same machine, where gzip only
// costs time on both ends (see docs/transport.md). A service that writes nothing still answers 200, as on net/http.
func Handler(ui fs.FS, services map[string]http.Handler) http.Handler {
	mux := http.NewServeMux()
	for prefix, h := range services {
		mux.Handle(prefix, answered(uncompressed(h)))
	}
	mux.Handle("/", assets(ui))
	return mux
}

// uncompressed hides from h the compressions the client accepts, so that a Connect handler answers in the clear.
// connect-go compresses a response whenever the client accepts gzip, and both the command line (connect-go) and
// the browsers (on every fetch) say they do; a request the client compressed itself is still read.
func uncompressed(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "" || r.Header.Get("Connect-Accept-Encoding") != "" {
			r = r.Clone(r.Context())
			r.Header.Del("Accept-Encoding")
			r.Header.Del("Connect-Accept-Encoding")
		}
		h.ServeHTTP(w, r)
	})
}

// answered gives a response that h left unwritten the status 200, as net/http does on its own. A unary Connect
// method whose response is empty in binary Protobuf, as TerminalService.Write or Resize, writes neither a
// header nor a byte; the Wails asset server, behind the native window, answers such a request 501 Not Implemented.
func answered(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aw := &answerWriter{ResponseWriter: w}
		h.ServeHTTP(aw, r)
		if !aw.wrote {
			w.WriteHeader(http.StatusOK)
		}
	})
}

// answerWriter tells whether the handler wrote a header or a byte.
type answerWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *answerWriter) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *answerWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Flush keeps the streams of the services working: connect-go needs an http.Flusher for a server stream.
func (w *answerWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets an http.ResponseController reach the writer underneath.
func (w *answerWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// types are the content types of the interface's files, set here. On Windows Go reads them from the registry, where
// another program may have changed them (.js as text/plain), and a browser refuses a module script that is not
// JavaScript.
var types = map[string]string{
	".css":  "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".json": "application/json",
	".svg":  "image/svg+xml",
}

// assets serves the files of ui. A path that matches no file gets index.html, so that the interface handles its
// own routes, unless it names a file by its extension: a missing script is a 404, not a page. Any method but GET
// and HEAD is a 404 too: a call to a service this server does not serve reads as unimplemented, not as a page.
//
// Each file carries an ETag, the hash of its content: an embedded file has no date, so without it the browser
// fetched the file again on every use, the 3.6 MB Mermaid frame once per diagram. The browser keeps the file and
// asks each time whether it changed (no-cache): the server answers 304 with no body until Djinn is updated.
func assets(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	var tags sync.Map // name → ETag; ui does not change while the server runs
	tag := func(w http.ResponseWriter, name string) {
		t, ok := tags.Load(name)
		if !ok {
			b, err := fs.ReadFile(ui, name)
			if err != nil {
				return
			}
			sum := sha256.Sum256(b)
			t, _ = tags.LoadOrStore(name, `"`+hex.EncodeToString(sum[:16])+`"`)
		}
		w.Header().Set("Etag", t.(string))
		w.Header().Set("Cache-Control", "no-cache")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
			if t, ok := types[path.Ext(name)]; ok {
				w.Header().Set("Content-Type", t)
			}
			tag(w, name)
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		tag(w, "index.html")
		http.ServeFileFS(w, r, ui, "index.html")
	})
}

// NewToken returns a random secret to put in the URL the browser opens.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error.
	return hex.EncodeToString(b)
}

// Guard lets through only the requests that carry token and, when the browser names one, come from origin
// (scheme://host:port, the address the server listens on).
//
// The browser opens origin/?token=…: that first request sets a cookie and redirects to the same URL without
// the token. Later requests carry the cookie; a program sends "Authorization: Bearer <token>" instead.
func Guard(next http.Handler, token, origin string) http.Handler {
	u, err := url.Parse(origin)
	if err != nil {
		panic(err)
	}
	// Cookies are not isolated by port: name it after the port so two instances do not overwrite each other.
	cookie := "djinn_token_" + u.Port()
	valid := func(s string) bool { return subtle.ConstantTimeCompare([]byte(s), []byte(token)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A tilasm's frame has an opaque origin: the browser sends the cookie for the frame's page, never for the files
		// it loads (Origin null, Sec-Fetch-Site cross-site). Its page is sent to an address with a key of its own, from
		// which the files load.
		if id, key, rest, ok := keyedTilasm(r.URL.Path); ok {
			if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
				subtle.ConstantTimeCompare([]byte(key), []byte(FrameKey(token, id))) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path, r.URL.RawPath = TilasmPrefix+id+"/"+rest, ""
			next.ServeHTTP(w, r)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != origin {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if q := r.URL.Query(); r.Method == http.MethodGet && q.Has("token") {
			if !valid(q.Get("token")) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: cookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
			q.Del("token")
			clean := *r.URL
			clean.RawQuery = q.Encode()
			http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
			return
		}
		if c, err := r.Cookie(cookie); err == nil && valid(c.Value) {
			if id, rest, ok := tilasmPage(r); ok {
				to := TilasmPrefix + id + "/" + keyMark + FrameKey(token, id) + "/" + rest
				if r.URL.RawQuery != "" {
					to += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, to, http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && valid(bearer) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// keyMark starts the segment of a tilasm's address that holds its key: /tilasm/<id>/@<key>/<file>.
const keyMark = "@"

// FrameKey is the key of a tilasm's files for the server of token: it opens that tilasm only, while that server runs.
func FrameKey(token, id string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("tilasm " + strings.ToLower(id)))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// keyedTilasm reads an address /tilasm/<id>/@<key>/<rest>.
func keyedTilasm(p string) (id, key, rest string, ok bool) {
	id, after, ok := strings.Cut(strings.TrimPrefix(p, TilasmPrefix), "/")
	if !ok || !strings.HasPrefix(p, TilasmPrefix) || !strings.HasPrefix(after, keyMark) {
		return "", "", "", false
	}
	key, rest, _ = strings.Cut(after[len(keyMark):], "/")
	return id, key, rest, true
}

// tilasmPage tells a browser opening a tilasm's page, in a frame or alone, at /tilasm/<id>/<rest>: it gets the address
// with the key. A program, with its bearer token, reads the files where they are.
func tilasmPage(r *http.Request) (id, rest string, ok bool) {
	if r.Method != http.MethodGet || r.Header.Get("Sec-Fetch-Mode") != "navigate" ||
		!strings.HasPrefix(r.URL.Path, TilasmPrefix) {
		return "", "", false
	}
	id, rest, ok = strings.Cut(strings.TrimPrefix(r.URL.Path, TilasmPrefix), "/")
	return id, rest, ok && id != ""
}

// Serve serves h on ln until ctx is done, then shuts down. Requests share ctx, so open streams end with it
// instead of holding the shutdown.
//
// A Unix socket speaks HTTP/1.1, which the command line uses, and HTTP/2 without TLS (h2c, prior knowledge), which
// a bidirectional Connect stream needs. A TCP listener, which a browser reaches, speaks HTTP/1.1 only: no browser
// speaks h2c. See docs/transport.md.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := newServer(ctx, ln, h)
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-done
}

// newServer returns the HTTP server of Serve, apart for the benchmarks.
func newServer(ctx context.Context, ln net.Listener, h http.Handler) *http.Server {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	if ln.Addr().Network() == "unix" {
		srv.Protocols = new(http.Protocols)
		srv.Protocols.SetHTTP1(true)
		srv.Protocols.SetUnencryptedHTTP2(true)
	}
	return srv
}
