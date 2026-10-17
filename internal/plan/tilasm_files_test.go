package plan

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// fetch reads an address of the server without following a redirect: its status, headers and body.
func fetch(t *testing.T, method, url string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}

func TestTilasmFilesServeTheLatestVersionWithTheirPolicy(t *testing.T) {
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{Wish: wish, Path: folder(t, map[string]string{
		"index.html": page("Model", "v1"), "js/app.mjs": "export const v = 1;", "img/a.svg": "<svg/>",
		"docs/index.html": "<p>docs</p>",
	})}).GetTilasm()
	base := c.url + "/tilasm/" + tilasm.GetId()

	res, body := fetch(t, http.MethodGet, base+"/")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<p>v1</p>") ||
		res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("GET /tilasm/<id>/ = %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	// The policy: its scripts and its own files, no network, no access to Djinn.
	policy := res.Header.Get("Content-Security-Policy")
	origin := strings.TrimSuffix(c.url, "/")
	if policy != tilasmPolicy(strings.TrimPrefix(origin, "http://")) {
		t.Fatalf("policy = %q, want TilasmPolicy for %s", policy, origin)
	}
	directives := map[string]string{}
	for _, d := range strings.Split(policy, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(d), " ")
		directives[name] = value
	}
	for name, want := range map[string]string{
		// A fetch, an XMLHttpRequest, a WebSocket or an EventSource to the outside, or to Djinn, is refused.
		"connect-src": "'none'",
		// Any other load from elsewhere too: only 'self' and inline sources are named below.
		"default-src": "'none'",
		"form-action": "'none'",
		// An opaque origin, even opened alone: never Djinn's, never its cookie nor its storage.
		"sandbox":         "allow-scripts",
		"frame-ancestors": "'self' " + origin,
	} {
		if directives[name] != want {
			t.Errorf("%s = %q, want %q", name, directives[name], want)
		}
	}
	for name, value := range directives {
		// The server's own origin is named beside 'self', for WebKit (tilasmPolicy): no other.
		if value = strings.ReplaceAll(value, "'self' "+origin, "'self'"); strings.Contains(value, "http") || strings.Contains(value, "*") || strings.Contains(value, "allow-same-origin") {
			t.Errorf("%s %s opens more than the tilasm's own files", name, value)
		}
	}
	if !strings.Contains(directives["script-src"], "'self'") || !strings.Contains(directives["script-src"], "'unsafe-inline'") {
		t.Errorf("script-src = %q: the tilasm's own scripts must run", directives["script-src"])
	}

	res, body = fetch(t, http.MethodGet, base+"/js/app.mjs")
	if res.StatusCode != http.StatusOK || body != "export const v = 1;" ||
		res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" || res.Header.Get("Content-Security-Policy") != policy {
		t.Errorf("GET app.mjs = %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	if res, body = fetch(t, http.MethodGet, base+"/docs/"); !strings.Contains(body, "docs") {
		t.Errorf("a folder serves its index.html: %d %q", res.StatusCode, body)
	}
	if res, _ = fetch(t, http.MethodGet, base); res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/tilasm/"+tilasm.GetId()+"/" {
		t.Errorf("GET without the slash = %d to %q, want a redirect to the folder", res.StatusCode, res.Header.Get("Location"))
	}
	// Nothing else: a file it lacks, a folder without index.html, a path that climbs out.
	for _, p := range []string{"/missing.js", "/js", "/../../tilasms", "/%2e%2e/%2e%2e/tilasms"} {
		if res, body := fetch(t, http.MethodGet, base+p); res.StatusCode == http.StatusOK {
			t.Errorf("GET %s = %d %q, want nothing", p, res.StatusCode, body)
		}
	}
	if res, _ := fetch(t, http.MethodGet, c.url+"/tilasm/01a1223a-ae45-728f-8c37-c005eee91edb/"); res.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown tilasm = %d, want 404", res.StatusCode)
	}
	if res, _ := fetch(t, http.MethodPost, base+"/"); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", res.StatusCode)
	}

	// A new version replaces what the address shows; the address stays. The browser's copy of the earlier one, even
	// put within the same second, is not taken for it: its tag names its version.
	if res, _ := fetch(t, http.MethodGet, base+"/"); res.Header.Get("Etag") != `"v1"` {
		t.Errorf("ETag = %q, want the version", res.Header.Get("Etag"))
	}
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Wish: wish, Code: "L01", Path: folder(t, map[string]string{"index.html": page("Model", "v2")})})
	if res, body := fetch(t, http.MethodGet, base+"/"); !strings.Contains(body, "<p>v2</p>") || res.Header.Get("Etag") != `"v2"` {
		t.Errorf("after a new version, the page = %q, ETag %q", body, res.Header.Get("Etag"))
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", `"v1"`)
	req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusOK {
		t.Errorf("the browser's copy of v1: %v %v, want the new version", res.StatusCode, err)
	} else {
		res.Body.Close()
	}
	if res, _ := fetch(t, http.MethodGet, base+"/js/app.mjs"); res.StatusCode != http.StatusNotFound {
		t.Errorf("a file of the earlier version only = %d, want 404", res.StatusCode)
	}
}

// Each HTML page ends with the bridge's tag as Djinn serves it, and nothing else changes: the tilasm's other files, its
// export and what djinn tilasm get reads are its files byte for byte, and the policy is the same for the page, its
// files and the bridge.
func TestTilasmFilesEndEachPageWithTheBridge(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	files := map[string]string{
		"index.html": page("Model", "v1"), "notes/more.htm": "<p>more</p>", "app.js": "console.log(1);",
		"img/a.svg": "<svg/>", "bridge.js": "// the tilasm's own", "data.json": `{"a":1}`,
	}
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{Wish: wish, Path: folder(t, files)}).GetTilasm()
	base := c.url + "/tilasm/" + tilasm.GetId()
	origin := strings.TrimSuffix(c.url, "/")
	policy := tilasmPolicy(strings.TrimPrefix(origin, "http://"))
	if !strings.HasSuffix(bridgeTag, "<script src=\"/tilasm/bridge.js\"></script>\n") {
		t.Fatalf("bridgeTag = %q", bridgeTag)
	}

	for address, want := range map[string]string{
		"/":               files["index.html"] + bridgeTag,
		"/index.html":     files["index.html"] + bridgeTag,
		"/notes/more.htm": files["notes/more.htm"] + bridgeTag,
		"/notes/":         "",
		"/app.js":         files["app.js"],
		"/img/a.svg":      files["img/a.svg"],
		"/bridge.js":      files["bridge.js"],
		"/data.json":      files["data.json"],
	} {
		res, body := fetch(t, http.MethodGet, base+address)
		if want == "" {
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", address, res.StatusCode)
			}
			continue
		}
		if res.StatusCode != http.StatusOK || body != want || res.Header.Get("Content-Security-Policy") != policy {
			t.Errorf("GET %s = %d %q, policy %q; want %q", address, res.StatusCode, body, res.Header.Get("Content-Security-Policy"), want)
		}
	}
	// A request for part of a page counts the tag in.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/notes/more.htm", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=5-")
	if res, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(body) != (files["notes/more.htm"] + bridgeTag)[5:] {
			t.Errorf("a range of the page = %q", body)
		}
	}

	// The bridge itself, under the tilasms' prefix, before any identifier; the same policy.
	res, body := fetch(t, http.MethodGet, c.url+"/tilasm/bridge.js")
	if res.StatusCode != http.StatusOK || body != string(tilasmBridge) ||
		res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" ||
		res.Header.Get("Content-Security-Policy") != policy || res.Header.Get("Etag") != bridgeETag {
		t.Fatalf("GET /tilasm/bridge.js = %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Etag"))
	}
	if !strings.HasPrefix(body, "// The bridge between") || !strings.Contains(body, `"use strict"`) {
		t.Errorf("the bridge served = %.80q", body)
	}
	// ASCII only: a tilasm's page that names no charset reads the script as windows-1252.
	for i, c := range tilasmBridge {
		if c >= 0x80 {
			t.Fatalf("the bridge holds a byte past ASCII at %d: %q", i, tilasmBridge[max(0, i-20):i+5])
		}
	}

	// Its files as they are: the export, and the text and files djinn tilasm get reads.
	exported, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{
		Tilasm: ref("L01"), File: filepath.Join(t.TempDir(), "model.zip"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	zip := zipped(t, exported.Msg.GetFile())
	for name, content := range files {
		if zip[name] != content {
			t.Errorf("the export's %s = %q, want %q", name, zip[name], content)
		}
	}
	read, err := c.tilasms.Get(ctx, connect.NewRequest(&planv1.TilasmServiceGetRequest{Tilasm: ref("L01"), Wish: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read.Msg.GetText(), "bridge") || strings.Contains(read.Msg.GetText(), "script") {
		t.Errorf("djinn tilasm get reads the bridge: %q", read.Msg.GetText())
	}
	for name, content := range files {
		if b, err := os.ReadFile(filepath.Join(read.Msg.GetDirectory(), filepath.FromSlash(name))); err != nil || string(b) != content {
			t.Errorf("the stored %s = %q %v, want %q", name, b, err, content)
		}
	}
}

// The policy is the one before the bridge: it already lets a tilasm load a script from the server's origin.
func TestTilasmPolicyUnchangedByTheBridge(t *testing.T) {
	const want = "default-src 'none'; script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
		"media-src 'self' data: blob:; worker-src 'self' blob:; frame-src 'self'; connect-src 'none'; " +
		"object-src 'none'; base-uri 'self'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts"
	if TilasmPolicy != want {
		t.Errorf("TilasmPolicy = %q, want %q", TilasmPolicy, want)
	}
}

// WebKit reads 'self' in a sandboxed frame as its opaque origin, which matches nothing: the policy names the server's
// origin too, or a tilasm loads none of its files, its scripts, fonts and pictures refused.
func TestTilasmPolicyNamesTheServersOrigin(t *testing.T) {
	for host, want := range map[string]string{
		// The window's Wails asset server on macOS and Linux.
		"localhost": "'self' wails://localhost",
		// The loopback server, as a browser or the window on Windows reaches it.
		"127.0.0.1:41234": "'self' http://127.0.0.1:41234",
		"localhost:8080":  "'self' http://localhost:8080",
		// A host a policy would read as more: only 'self'.
		"evil.example:1; script-src *": "'self'",
		"a b:1":                        "'self'",
		"127.0.0.1:80x":                "'self'",
		"127.0.0.1:":                   "'self'",
		"[::1]:5":                      "'self'",
		"example.com":                  "'self'",
		"":                             "'self'",
	} {
		policy := tilasmPolicy(host)
		if policy != strings.ReplaceAll(TilasmPolicy, "'self'", want) {
			t.Errorf("tilasmPolicy(%q) = %q, want 'self' as %q", host, policy, want)
		}
		if !strings.Contains(policy, "script-src "+want+" 'unsafe-inline'") || !strings.Contains(policy, "font-src "+want+" data:") {
			t.Errorf("tilasmPolicy(%q): its scripts and fonts load from %q only, got %q", host, want, policy)
		}
	}
}

func TestTilasmPutData(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	// A folder dropped on the tab: its files, by their paths.
	res, err := c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{Wish: wish, Name: "Data model", Files: []*planv1.TilasmFile{
		{Path: "index.html", Content: []byte("<p>no title</p>")}, {Path: "css/a.css", Content: []byte("p{}")},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTilasm(); got.GetCode() != "L01" || got.GetTitle() != "Data model" || got.GetAuthor() != "developer" ||
		got.GetVersions()[0].GetFiles() != 2 || res.Msg.GetImported() {
		t.Fatalf("a dropped folder = %v", res.Msg)
	}
	_, err = c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{Wish: wish, Files: []*planv1.TilasmFile{
		{Path: "../index.html", Content: []byte("<p>out</p>")},
	}}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("a file out of the folder: %v, want invalid_argument", err)
	}

	// A .zip of a folder: a new tilasm, titled by its page.
	data, err := os.ReadFile(zipOf(t, map[string]string{"site/index.html": page("Flows", "how it moves")}))
	if err != nil {
		t.Fatal(err)
	}
	res, err = c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{Wish: wish, Name: "site.zip", Files: []*planv1.TilasmFile{{Path: "site.zip", Content: data}}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTilasm(); got.GetCode() != "L02" || got.GetTitle() != "Flows" || res.Msg.GetImported() {
		t.Fatalf("a dropped .zip = %v", res.Msg)
	}

	// A tilasm's export: imported, as a new version of the same tilasm.
	file := t.TempDir() + "/l01.zip"
	if _, err := c.tilasms.Export(ctx, connect.NewRequest(&planv1.TilasmServiceExportRequest{Tilasm: ref("L01"), File: file})); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	res, err = c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{Wish: wish, Name: "l01.zip", Files: []*planv1.TilasmFile{{Path: "l01.zip", Content: data}}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTilasm(); got.GetCode() != "L01" || len(got.GetVersions()) != 2 || !res.Msg.GetImported() {
		t.Fatalf("a dropped export = %v", res.Msg)
	}

	// A single .html file dropped or chosen: stored as index.html, titled by its <title> or its file name.
	res, err = c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{
		Wish:  wish,
		Name:  "overview.html",
		Files: []*planv1.TilasmFile{{Path: "overview.html", Content: []byte(page("Overview", "One file."))}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTilasm(); got.GetCode() != "L03" || got.GetTitle() != "Overview" || got.GetVersions()[0].GetFiles() != 1 || res.Msg.GetImported() {
		t.Fatalf("a dropped .html with title = %v", res.Msg)
	}
	if got := getTilasm(t, c, "L03", 0); len(got.GetFiles()) != 1 || got.GetFiles()[0] != "index.html" || !strings.Contains(got.GetText(), "One file.") {
		t.Fatalf("L03 files = %v, text = %q", got.GetFiles(), got.GetText())
	}

	res, err = c.tilasms.PutData(ctx, connect.NewRequest(&planv1.TilasmServicePutDataRequest{
		Wish:  wish,
		Name:  "draft-notes.htm",
		Files: []*planv1.TilasmFile{{Path: "draft-notes.htm", Content: []byte("<p>Untagged title</p>")}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetTilasm(); got.GetCode() != "L04" || got.GetTitle() != "draft-notes" || got.GetVersions()[0].GetFiles() != 1 {
		t.Fatalf("a dropped .htm without title = %v", res.Msg)
	}
}
