// Package server builds the single HTTP handler that both the native window and the browser mode serve: the
// Connect services, and the interface for every other path.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

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
// method whose response is empty in binary Protobuf, as LoadState with nothing saved or SaveState, writes neither a
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

// assets serves the files of ui. A path that matches no file gets index.html, so that the interface handles its
// own routes, unless it names a file by its extension: a missing script is a 404, not a page. Any method but GET
// and HEAD is a 404 too: a call to a service this server does not serve reads as unimplemented, not as a page.
func assets(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
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
