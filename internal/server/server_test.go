package server_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

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
	return server.Handler(ui, map[string]http.Handler{prefix: h})
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

func TestAssets(t *testing.T) {
	h := handler()
	for _, tc := range []struct {
		path              string
		status            int
		body, contentType string
	}{
		{"/", http.StatusOK, "<title>index</title>", "text/html"},
		{"/assets/app.js", http.StatusOK, "console.log(1)", "text/javascript"},
		{"/assets/style.css", http.StatusOK, "body{}", "text/css"},
		{"/missions/42", http.StatusOK, "<title>index</title>", "text/html"}, // a route of the interface
		{"/assets/", http.StatusOK, "<title>index</title>", "text/html"},     // no directory listing
		{"/assets/missing.js", http.StatusNotFound, "", ""},                  // a missing file is not a page
	} {
		rec, body := get(t, h, tc.path, nil)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d", tc.path, rec.Code, tc.status)
		}
		if tc.body != "" && body != tc.body {
			t.Errorf("%s: body %q, want %q", tc.path, body, tc.body)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.contentType) {
			t.Errorf("%s: content type %q, want %q", tc.path, rec.Header().Get("Content-Type"), tc.contentType)
		}
	}
}

// TestAssetsCached checks that the browser keeps a file and gets it again only when it changed: an embedded file
// has no date, and the Mermaid frame, 3.6 MB, loads once per diagram.
func TestAssetsCached(t *testing.T) {
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
}

// TestUnknownService checks that a call to a service the server does not serve reads as unimplemented.
func TestUnknownService(t *testing.T) {
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

// TestEmptyResponseIsAnswered checks that a unary method whose response is empty in binary Protobuf
// (TerminalService.Write, Resize) still gets its 200: connect-go writes nothing for it.
func TestEmptyResponseIsAnswered(t *testing.T) {
	const procedure = "/test.v1.EmptyService/Call"
	h := server.Handler(ui, map[string]http.Handler{
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
}

func TestGuardToken(t *testing.T) {
	const origin = "http://127.0.0.1:4000"
	h := server.Guard(handler(), token, origin)

	if rec, _ := get(t, h, "/", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", rec.Code)
	}
	if rec, _ := get(t, h, "/?token=wrong", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d, want 401", rec.Code)
	}
	if rec, _ := get(t, h, "/", http.Header{"Authorization": {"Bearer wrong"}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: status %d, want 401", rec.Code)
	}
	if rec, _ := get(t, h, "/", http.Header{"Cookie": {"djinn_token_4001=" + token}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie of another port: status %d, want 401", rec.Code)
	}

	// The URL token sets the cookie and redirects to the same URL without it.
	rec, _ := get(t, h, "/missions?token="+token+"&tab=plan", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/missions?tab=plan" {
		t.Fatalf("URL token: status %d, location %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "djinn_token_4000" || !cookies[0].HttpOnly ||
		cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("URL token: cookies %v", cookies)
	}

	cookie := http.Header{"Cookie": {cookies[0].String()}}
	if rec, body := get(t, h, "/", cookie); rec.Code != http.StatusOK || body != "<title>index</title>" {
		t.Fatalf("cookie: status %d, body %q", rec.Code, body)
	}
	if rec, _ := get(t, h, "/assets/app.js", http.Header{"Authorization": {"Bearer " + token}}); rec.Code != http.StatusOK {
		t.Fatalf("bearer: status %d, want 200", rec.Code)
	}
}

func TestGuardOrigin(t *testing.T) {
	const origin = "http://127.0.0.1:4000"
	h := server.Guard(handler(), token, origin)
	auth := http.Header{"Authorization": {"Bearer " + token}}

	for _, o := range []string{"http://127.0.0.1:4001", "http://localhost:4000", "https://example.com", "null"} {
		header := http.Header{"Origin": {o}, "Authorization": auth["Authorization"]}
		if rec, _ := get(t, h, "/", header); rec.Code != http.StatusForbidden {
			t.Errorf("origin %s: status %d, want 403", o, rec.Code)
		}
	}
	// A foreign origin is refused even with the URL token: a page elsewhere cannot plant the cookie.
	if rec, _ := get(t, h, "/?token="+token, http.Header{"Origin": {"https://example.com"}}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign origin with URL token: status %d, want 403", rec.Code)
	}
	header := http.Header{"Origin": {origin}, "Authorization": auth["Authorization"]}
	if rec, _ := get(t, h, "/", header); rec.Code != http.StatusOK {
		t.Errorf("own origin: status %d, want 200", rec.Code)
	}
}

// TestGuardTilasmFrame: a tilasm's frame has an opaque origin, so the browser sends the cookie for its page only. That
// page goes to an address with the tilasm's key, from which its files load, with no cookie and Origin null.
func TestGuardTilasmFrame(t *testing.T) {
	const origin = "http://127.0.0.1:4000"
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
	if rec, _ := get(t, h, "/tilasm/"+id+"/index.html", http.Header{"Authorization": {"Bearer " + token}}); rec.Code != http.StatusOK ||
		served[len(served)-1] != "/tilasm/"+id+"/index.html" {
		t.Errorf("bearer: %d, served %v", rec.Code, served)
	}
	if server.FrameKey(token, id) != server.FrameKey(token, strings.ToUpper(id)) || server.FrameKey("other", id) == key {
		t.Error("the key follows the token and the tilasm, case ignored")
	}
}

// TestStream runs the count through a real HTTP server and the guard, as the browser mode does.
func TestStream(t *testing.T) {
	srv := httptest.NewUnstartedServer(nil)
	origin := "http://" + srv.Listener.Addr().String()
	srv.Config.Handler = server.Guard(handler(), token, origin)
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
}

// TestServeStopsWithOpenStream checks that cancelling the context (SIGINT) ends Serve promptly, even while a
// stream is open.
func TestServeStopsWithOpenStream(t *testing.T) {
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
}

// withBearer is an HTTP client that sends the token, as a program would.
type withBearer struct{ base http.RoundTripper }

func (b withBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return b.base.RoundTrip(r)
}
