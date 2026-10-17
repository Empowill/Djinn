package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
	"github.com/empowill/djinn/internal/demo"
	"github.com/empowill/djinn/internal/server"
)

const token = "s3cret"

var ui = fstest.MapFS{
	"index.html":       {Data: []byte("<title>index</title>")},
	"assets/app.js":    {Data: []byte("console.log(1)")},
	"assets/style.css": {Data: []byte("body{}")},
}

func handler() http.Handler {
	prefix, h := demov1connect.NewDemoServiceHandler(demo.Service{})
	return server.Handler(ui, nil, map[string]http.Handler{prefix: h})
}

// get serves a GET on h and returns the status and the body.
func get(t *testing.T, h http.Handler, target string, header http.Header) (*httptest.ResponseRecorder, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, rec.Body.String()
}

// protoClient returns an HTTP client to network and address that speaks HTTP/1.1, or h2c only (prior knowledge).
func protoClient(network, address string, h2c bool) *http.Client {
	var d net.Dialer
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialContext(ctx, network, address) },
	}
	if h2c {
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetUnencryptedHTTP2(true)
	}
	return &http.Client{Transport: transport}
}

// echoHandler serves bench.v1.Echo/Echo, a unary call, and bench.v1.Echo/Chat, a bidirectional stream, which
// return what they receive.
func echoHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/bench.v1.Echo/Echo", connect.NewUnaryHandler("/bench.v1.Echo/Echo",
		func(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			return connect.NewResponse(req.Msg), nil
		}))
	mux.Handle("/bench.v1.Echo/Chat", connect.NewBidiStreamHandler("/bench.v1.Echo/Chat",
		func(_ context.Context, s *connect.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
			for {
				msg, err := s.Receive()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := s.Send(msg); err != nil {
					return err
				}
			}
		}))
	demoPrefix, demo := demov1connect.NewDemoServiceHandler(countTo{})
	return server.Handler(fstest.MapFS{}, nil, map[string]http.Handler{"/bench.v1.Echo/": mux, demoPrefix: demo})
}

// countTo sends 1, 2, … up to the request at once: demo.Service waits between two values.
type countTo struct{}

func (countTo) Count(
	_ context.Context, req *connect.Request[demov1.CountRequest], s *connect.ServerStream[demov1.CountResponse],
) error {
	for v := int32(1); v <= req.Msg.GetUpTo(); v++ {
		if err := s.Send(&demov1.CountResponse{Value: v}); err != nil {
			return err
		}
	}
	return nil
}

// strictWriter records only what a handler really writes, unlike net/http and httptest, which answer 200 on their
// own. The Wails asset server behind the native window answers 501 to a request left with no status.
type strictWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *strictWriter) Header() http.Header { return w.header }
func (w *strictWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *strictWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(b)
}

func TestHandler(t *testing.T) {
	t.Parallel()
	docs := fstest.MapFS{
		"index.html": {Data: []byte("<title>docs</title>")},
		"site.css":   {Data: []byte("main{}")},
		"site.js":    {Data: []byte("window.site = {};")},
	}
	prefix, demoHandler := demov1connect.NewDemoServiceHandler(demo.Service{})
	withDocs := server.Handler(ui, docs, map[string]http.Handler{prefix: demoHandler})

	// The interface's files, and the documentation site at /docs/ beside them.
	for _, tc := range []struct {
		name              string
		h                 http.Handler
		path              string
		status            int
		body, contentType string
	}{
		{"index", handler(), "/", http.StatusOK, "<title>index</title>", "text/html"},
		{"script", handler(), "/assets/app.js", http.StatusOK, "console.log(1)", "text/javascript"},
		{"style", handler(), "/assets/style.css", http.StatusOK, "body{}", "text/css"},
		{"route of the interface", handler(), "/missions/42", http.StatusOK, "<title>index</title>", "text/html"},
		{"no directory listing", handler(), "/assets/", http.StatusOK, "<title>index</title>", "text/html"},
		{"missing file is not a page", handler(), "/assets/missing.js", http.StatusNotFound, "", ""},
		{"docs index", withDocs, "/docs/", http.StatusOK, "<title>docs</title>", "text/html"},
		{"docs style", withDocs, "/docs/site.css", http.StatusOK, "main{}", "text/css"},
		{"docs script", withDocs, "/docs/site.js", http.StatusOK, "window.site = {};", "text/javascript"},
		{"docs missing file", withDocs, "/docs/missing.js", http.StatusNotFound, "", ""},
		{"docs without its slash", withDocs, "/docs", http.StatusTemporaryRedirect, "", ""},
		{"interface beside the docs", withDocs, "/", http.StatusOK, "<title>index</title>", "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec, body := get(t, tc.h, tc.path, nil)
			if rec.Code != tc.status {
				t.Errorf("%s: status %d, want %d", tc.path, rec.Code, tc.status)
			}
			if tc.body != "" && body != tc.body {
				t.Errorf("%s: body %q, want %q", tc.path, body, tc.body)
			}
			if !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.contentType) {
				t.Errorf("%s: content type %q, want %q", tc.path, rec.Header().Get("Content-Type"), tc.contentType)
			}
		})
	}

	// The browser keeps a file and gets it again only when it changed: an embedded file has no date, and the Mermaid
	// frame, 3.6 MB, loads once per diagram.
	t.Run("files are cached by their etag", func(t *testing.T) {
		t.Parallel()
		h := handler()
		rec, _ := get(t, h, "/assets/app.js", nil)
		tag := rec.Header().Get("Etag")
		if !strings.HasPrefix(tag, `"`) || len(tag) < 10 {
			t.Fatalf("ETag %q, want a quoted hash", tag)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control %q, want no-cache", got)
		}
		rec, body := get(t, h, "/assets/app.js", http.Header{"If-None-Match": {tag}})
		if rec.Code != http.StatusNotModified || body != "" {
			t.Errorf("with its ETag: status %d, body %q, want 304 and no body", rec.Code, body)
		}
		if other, _ := get(t, h, "/assets/style.css", nil); other.Header().Get("Etag") == tag {
			t.Error("two files share an ETag")
		}
		if page, _ := get(t, h, "/missions/42", http.Header{"If-None-Match": {tag}}); page.Code != http.StatusOK ||
			page.Header().Get("Etag") == "" {
			t.Errorf("a route of the interface: status %d, ETag %q, want 200 and index.html's own ETag",
				page.Code, page.Header().Get("Etag"))
		}
	})

	// A call to a service the server does not serve reads as unimplemented.
	t.Run("unknown service is not found", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(handler())
		defer srv.Close()
		res, err := http.Post(srv.URL+"/plan.v1.QuestionService/List", "application/proto", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d, want 404", res.StatusCode)
		}
	})

	// A unary method whose response is empty in binary Protobuf (TerminalService.Write, Resize) still gets its 200:
	// connect-go writes nothing for it.
	t.Run("empty response is answered", func(t *testing.T) {
		t.Parallel()
		const procedure = "/test.v1.EmptyService/Call"
		h := server.Handler(ui, nil, map[string]http.Handler{
			"/test.v1.EmptyService/": connect.NewUnaryHandler(procedure,
				func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
					return connect.NewResponse(&emptypb.Empty{}), nil
				}),
		})
		req := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/proto")
		w := &strictWriter{header: http.Header{}}
		h.ServeHTTP(w, req)
		if w.status != http.StatusOK || w.body.Len() != 0 {
			t.Fatalf("status %d, body %q; want 200 and no body", w.status, w.body.String())
		}
	})

	// The services answer in the clear although connect-go clients, like the command line, and browsers ask for gzip.
	t.Run("services answer uncompressed", func(t *testing.T) {
		t.Parallel()
		socket := filepath.Join(shortDir(t), server.SocketFile)
		ln, err := server.ListenUnix(socket)
		if err != nil {
			t.Fatal(err)
		}
		go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.
		hc := protoClient("unix", socket, false)

		// connect-go with its defaults, as the command line calls: it accepts gzip.
		var sent, encoding string
		spy := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				res, err := next(ctx, req)
				sent = req.Header().Get("Accept-Encoding")
				if res != nil {
					encoding = res.Header().Get("Content-Encoding")
				}
				return res, err
			}
		}))
		echo := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](hc, "http://djinn/bench.v1.Echo/Echo", spy)
		big := strings.Repeat("compressible ", 1000)
		res, err := echo.CallUnary(t.Context(), connect.NewRequest(wrapperspb.String(big)))
		if err != nil || res.Msg.GetValue() != big {
			t.Fatalf("echo: %v", err)
		}
		if !strings.Contains(sent, "gzip") {
			t.Fatalf("the client sent Accept-Encoding %q: the test no longer asks for gzip", sent)
		}
		if encoding != "" {
			t.Errorf("unary response: Content-Encoding %q, want none", encoding)
		}

		// A browser asks with Accept-Encoding on every request.
		req, _ := http.NewRequest(http.MethodPost, "http://djinn/bench.v1.Echo/Echo", strings.NewReader(`"`+big+`"`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		raw, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw.Body.Close()
		if raw.StatusCode != http.StatusOK || raw.Header.Get("Content-Encoding") != "" {
			t.Errorf("browser-like unary: status %d, Content-Encoding %q, want 200 and none",
				raw.StatusCode, raw.Header.Get("Content-Encoding"))
		}

		// A stream: connect-go asks with Connect-Accept-Encoding.
		stream, err := demov1connect.NewDemoServiceClient(hc, "http://djinn").
			Count(t.Context(), connect.NewRequest(&demov1.CountRequest{UpTo: 3}))
		if err != nil {
			t.Fatal(err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatal(err)
		}
		if e := stream.ResponseHeader().Get("Connect-Content-Encoding"); e != "" {
			t.Errorf("stream: Connect-Content-Encoding %q, want none", e)
		}
		stream.Close()
	})
}

func TestNewToken(t *testing.T) {
	t.Parallel()
	a, b := server.NewToken(), server.NewToken()
	if len(a) != 64 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("NewToken = %q, want 64 hexadecimal digits", a)
	}
	if a == b {
		t.Error("two tokens are the same")
	}
}

func TestGuard(t *testing.T) {
	t.Parallel()
	const origin = "http://127.0.0.1:4000"
	bearer := http.Header{"Authorization": {"Bearer " + token}}

	// What is refused before any cookie is set.
	for _, tc := range []struct {
		name   string
		target string
		header http.Header
		status int
	}{
		{"no token", "/", nil, http.StatusUnauthorized},
		{"wrong token", "/?token=wrong", nil, http.StatusUnauthorized},
		{"wrong bearer", "/", http.Header{"Authorization": {"Bearer wrong"}}, http.StatusUnauthorized},
		{"cookie of another port", "/", http.Header{"Cookie": {"djinn_token_4001=" + token}}, http.StatusUnauthorized},
		{"another port", "/", http.Header{"Origin": {"http://127.0.0.1:4001"}, "Authorization": bearer["Authorization"]},
			http.StatusForbidden},
		{"another host", "/", http.Header{"Origin": {"http://localhost:4000"}, "Authorization": bearer["Authorization"]},
			http.StatusForbidden},
		{"another site", "/", http.Header{"Origin": {"https://example.com"}, "Authorization": bearer["Authorization"]},
			http.StatusForbidden},
		{"null origin", "/", http.Header{"Origin": {"null"}, "Authorization": bearer["Authorization"]},
			http.StatusForbidden},
		// A page elsewhere cannot plant the cookie.
		{"foreign origin with the url token", "/?token=" + token, http.Header{"Origin": {"https://example.com"}},
			http.StatusForbidden},
		{"own origin", "/", http.Header{"Origin": {origin}, "Authorization": bearer["Authorization"]}, http.StatusOK},
		{"bearer", "/assets/app.js", bearer, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if rec, _ := get(t, server.Guard(handler(), token, origin), tc.target, tc.header); rec.Code != tc.status {
				t.Errorf("status %d, want %d", rec.Code, tc.status)
			}
		})
	}

	t.Run("url token sets the cookie", func(t *testing.T) {
		t.Parallel()
		h := server.Guard(handler(), token, origin)
		// The URL token sets the cookie and redirects to the same URL without it.
		rec, _ := get(t, h, "/missions?token="+token+"&tab=plan", nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/missions?tab=plan" {
			t.Fatalf("URL token: status %d, location %q", rec.Code, rec.Header().Get("Location"))
		}
		// A path that starts with two slashes stays on this site.
		if rec, _ := get(t, h, "//example.com/x?token="+token, nil); rec.Header().Get("Location") != "/example.com/x" {
			t.Fatalf("URL token on //example.com/x: location %q, want /example.com/x", rec.Header().Get("Location"))
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "djinn_token_4000" || !cookies[0].HttpOnly ||
			cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatalf("URL token: cookies %v", cookies)
		}
		if rec, body := get(t, h, "/", http.Header{"Cookie": {cookies[0].String()}}); rec.Code != http.StatusOK ||
			body != "<title>index</title>" {
			t.Fatalf("cookie: status %d, body %q", rec.Code, body)
		}
	})

	// A tilasm's frame has an opaque origin, so the browser sends the cookie for its page only. That page goes to an
	// address with the tilasm's key, from which its files load, with no cookie and Origin null.
	t.Run("tilasm frame", func(t *testing.T) {
		t.Parallel()
		const id = "01a1223a-ae45-728f-8c37-c005eee91edb"
		var served []string
		files := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = append(served, r.URL.Path) })
		h := server.Guard(files, token, origin)
		key := server.FrameKey(token, id)
		cookie := "djinn_token_4000=" + token

		// The page, opened in the frame with the cookie: sent to the address with the key.
		rec, _ := get(t, h, "/tilasm/"+id+"/docs/?x=1", http.Header{"Cookie": {cookie}, "Sec-Fetch-Mode": {"navigate"}})
		if want := "/tilasm/" + id + "/@" + key + "/docs/?x=1"; rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Fatalf("page: %d to %q, want %q", rec.Code, rec.Header().Get("Location"), want)
		}
		// Its files, with the key: no cookie, Origin null for a module script; served at their own path.
		rec, _ = get(t, h, "/tilasm/"+id+"/@"+key+"/js/app.mjs", http.Header{"Origin": {"null"}})
		if rec.Code != http.StatusOK || len(served) != 1 || served[0] != "/tilasm/"+id+"/js/app.mjs" {
			t.Fatalf("file with the key: %d, served %v", rec.Code, served)
		}
		// The address djinn tilasm get gives a browser or an agent, its page at the root of the key.
		rec, _ = get(t, h, strings.TrimPrefix(server.TilasmAddress(origin, token, id), origin), nil)
		if rec.Code != http.StatusOK || served[len(served)-1] != "/tilasm/"+id+"/" {
			t.Fatalf("the address given: %d, served %v", rec.Code, served)
		}
		// Another tilasm's key, a wrong key, or a write: refused.
		for _, target := range []string{
			"/tilasm/01a1223a-ae45-728f-8c37-c005eee91ede/@" + key + "/index.html",
			"/tilasm/" + id + "/@" + strings.Repeat("0", 32) + "/index.html",
			"/tilasm/" + id + "/@/index.html",
		} {
			if rec, _ := get(t, h, target, nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s: %d, want 401", target, rec.Code)
			}
		}
		req := httptest.NewRequest(http.MethodPost, "/tilasm/"+id+"/@"+key+"/index.html", nil)
		rec = httptest.NewRecorder()
		if h.ServeHTTP(rec, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST with the key: %d, want 401", rec.Code)
		}
		// Without the key nor a credential, nothing; a program with its bearer token reads the files where they are.
		if rec, _ := get(t, h, "/tilasm/"+id+"/index.html", http.Header{"Origin": {"null"}}); rec.Code != http.StatusForbidden {
			t.Errorf("no key, Origin null: %d, want 403", rec.Code)
		}
		if rec, _ := get(t, h, "/tilasm/"+id+"/index.html", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("no key, no credential: %d, want 401", rec.Code)
		}
		if rec, _ := get(t, h, "/tilasm/"+id+"/index.html", bearer); rec.Code != http.StatusOK ||
			served[len(served)-1] != "/tilasm/"+id+"/index.html" {
			t.Errorf("bearer: %d, served %v", rec.Code, served)
		}
		// The bridge, the same for every tilasm, loads in the frame with neither the cookie nor a key; it is only read.
		if rec, _ := get(t, h, server.TilasmBridge, http.Header{"Origin": {"null"}}); rec.Code != http.StatusOK ||
			served[len(served)-1] != server.TilasmBridge {
			t.Errorf("the bridge: %d, served %v", rec.Code, served)
		}
		req = httptest.NewRequest(http.MethodPost, server.TilasmBridge, nil)
		rec = httptest.NewRecorder()
		if h.ServeHTTP(rec, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST to the bridge: %d, want 401", rec.Code)
		}
	})

	// The count runs through a real HTTP server and the guard, as the browser mode does.
	t.Run("stream through a real server", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewUnstartedServer(nil)
		srv.Config.Handler = server.Guard(handler(), token, "http://"+srv.Listener.Addr().String())
		srv.Start()
		defer srv.Close()

		client := demov1connect.NewDemoServiceClient(&http.Client{Transport: withBearer{srv.Client().Transport}}, srv.URL)
		stream, err := client.Count(t.Context(), connect.NewRequest(&demov1.CountRequest{UpTo: 3}))
		if err != nil {
			t.Fatal(err)
		}
		var got []int32
		for stream.Receive() {
			got = append(got, stream.Msg().GetValue())
		}
		if err := stream.Err(); err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || got[0] != 1 || got[2] != 3 {
			t.Fatalf("got %v, want [1 2 3]", got)
		}

		// Without the token, the stream is refused.
		anonymous := demov1connect.NewDemoServiceClient(srv.Client(), srv.URL)
		stream, err = anonymous.Count(t.Context(), connect.NewRequest(&demov1.CountRequest{UpTo: 3}))
		if err == nil {
			for stream.Receive() {
				t.Fatal("received a value without the token")
			}
			err = stream.Err()
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("without token: %v, want unauthenticated", err)
		}
	})
}

// withBearer is an HTTP client that sends the token, as a program would.
type withBearer struct{ base http.RoundTripper }

func (b withBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return b.base.RoundTrip(r)
}

// TestFrameKey: the key follows the token and the tilasm, case ignored.
func TestFrameKey(t *testing.T) {
	t.Parallel()
	const id = "01a1223a-ae45-728f-8c37-c005eee91edb"
	key := server.FrameKey(token, id)
	for _, tc := range []struct {
		name, token, id string
		same            bool
	}{
		{"same token and tilasm", token, id, true},
		{"upper case tilasm", token, strings.ToUpper(id), true},
		{"another token", "other", id, false},
		{"another tilasm", token, "01a1223a-ae45-728f-8c37-c005eee91ede", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := server.FrameKey(tc.token, tc.id); (got == key) != tc.same {
				t.Errorf("FrameKey(%q, %q) = %q, the same as %q: %v, want %v", tc.token, tc.id, got, key, !tc.same, tc.same)
			}
		})
	}
}

// TestTilasmAddress: the address djinn tilasm get gives a browser or an agent, with the tilasm's key, whatever the
// case of its id.
func TestTilasmAddress(t *testing.T) {
	t.Parallel()
	const origin = "http://127.0.0.1:4000"
	const id = "01a1223a-ae45-728f-8c37-c005eee91edb"
	want := origin + "/tilasm/" + id + "/@" + server.FrameKey(token, id) + "/"
	for _, tc := range []struct{ name, id string }{{"lower case", id}, {"upper case", strings.ToUpper(id)}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := server.TilasmAddress(origin, token, tc.id); got != want {
				t.Errorf("TilasmAddress = %q, want %q", got, want)
			}
		})
	}
}

func TestServe(t *testing.T) {
	t.Parallel()

	// Cancelling the context (SIGINT) ends Serve promptly, even while a stream is open.
	t.Run("stops with an open stream", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		served := make(chan error, 1)
		go func() { served <- server.Serve(ctx, ln, handler()) }()

		client := demov1connect.NewDemoServiceClient(http.DefaultClient, "http://"+ln.Addr().String())
		stream, err := client.Count(t.Context(), connect.NewRequest(&demov1.CountRequest{UpTo: 1_000_000}))
		if err != nil {
			t.Fatal(err)
		}
		if !stream.Receive() {
			t.Fatalf("no first value: %v", stream.Err())
		}

		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Fatalf("Serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Serve did not stop with an open stream")
		}
		for stream.Receive() {
		}
		_ = stream.Close()

		if _, err := http.Get("http://" + ln.Addr().String()); err == nil {
			t.Fatal("server still answers after shutdown")
		}
	})

	// The Unix socket speaks HTTP/1.1, which the command line uses, and h2c, which carries a bidirectional stream
	// message by message.
	t.Run("unix socket speaks h2c", func(t *testing.T) {
		t.Parallel()
		socket := filepath.Join(shortDir(t), server.SocketFile)
		ln, err := server.ListenUnix(socket)
		if err != nil {
			t.Fatal(err)
		}
		go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.

		for _, h2c := range []bool{false, true} {
			res, err := protoClient("unix", socket, h2c).Get("http://djinn/")
			if err != nil {
				t.Fatalf("h2c %v: %v", h2c, err)
			}
			res.Body.Close()
			if want := map[bool]int{false: 1, true: 2}[h2c]; res.ProtoMajor != want {
				t.Errorf("h2c %v: HTTP/%d, want HTTP/%d", h2c, res.ProtoMajor, want)
			}
		}

		// Each answer comes back before the next message is sent: the stream goes both ways at once. A transport that
		// waited for the end of the request would block here, until the deadline.
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		chat := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
			protoClient("unix", socket, true), "http://djinn/bench.v1.Echo/Chat").CallBidiStream(ctx)
		for _, word := range []string{"one", "two", "three"} {
			if err := chat.Send(wrapperspb.String(word)); err != nil {
				t.Fatalf("send %s: %v", word, err)
			}
			got, err := chat.Receive()
			if err != nil {
				t.Fatalf("receive %s: %v", word, err)
			}
			if got.GetValue() != word {
				t.Fatalf("got %q, want %q", got.GetValue(), word)
			}
		}
		if err := chat.CloseRequest(); err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Receive(); !errors.Is(err, io.EOF) {
			t.Fatalf("end of the chat: %v, want EOF", err)
		}
		if err := chat.CloseResponse(); err != nil {
			t.Fatal(err)
		}
	})

	// The loopback server, which browsers reach, speaks HTTP/1.1 only.
	t.Run("loopback stays http1", func(t *testing.T) {
		t.Parallel()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.

		res, err := protoClient("tcp", ln.Addr().String(), false).Get("http://djinn/")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.ProtoMajor != 1 {
			t.Errorf("HTTP/%d, want HTTP/1.1", res.ProtoMajor)
		}
		if res, err := protoClient("tcp", ln.Addr().String(), true).Get("http://djinn/"); err == nil {
			res.Body.Close()
			t.Fatalf("h2c over TCP answered HTTP/%d, want a refusal", res.ProtoMajor)
		}
	})
}
