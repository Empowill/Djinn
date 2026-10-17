package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestProfiles: the profiles answer where djinn up --pprof mounts them; without, the path is the interface's.
func TestProfiles(t *testing.T) {
	t.Parallel()
	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	get := func(h http.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ProfilePath+"goroutine?debug=1", nil))
		b, _ := io.ReadAll(rec.Result().Body)
		return string(b)
	}
	for _, tc := range []struct {
		name     string
		services map[string]http.Handler
		want     func(string) bool
	}{
		{"mounted", map[string]http.Handler{ProfilePath: Profiles()},
			func(body string) bool { return strings.Contains(body, "goroutine profile") }},
		{"not mounted", nil, func(body string) bool { return body == "<!doctype html>" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if body := get(Handler(ui, nil, tc.services)); !tc.want(body) {
				t.Errorf("%.80q, want the goroutine profile when mounted, the interface otherwise", body)
			}
		})
	}
}

// TestDump: a POST to DumpPath writes a dump to the folder and answers its path (W511); a GET is refused.
func TestDump(t *testing.T) {
	t.Parallel()
	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	dir := t.TempDir()
	h := Handler(ui, nil, map[string]http.Handler{DumpPath: Dump(dir)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, DumpPath, nil))
	path := strings.TrimSpace(rec.Body.String())
	if rec.Code != http.StatusOK || filepath.Dir(path) != dir {
		t.Fatalf("status %d, body %q", rec.Code, path)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "asked by djinn debug dump") {
		t.Errorf("dump %s: %v\n%.300s", path, err, b)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DumpPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rec.Code)
	}
}
