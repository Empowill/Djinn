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

// TestProfilesOnlyWhenMounted: the profiles answer where djinn up --pprof mounts them; without, the path is the
// interface's.
func TestProfilesOnlyWhenMounted(t *testing.T) {
	ui := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	get := func(h http.Handler) string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ProfilePath+"goroutine?debug=1", nil))
		b, _ := io.ReadAll(rec.Result().Body)
		return string(b)
	}
	if body := get(Handler(ui, nil, map[string]http.Handler{ProfilePath: Profiles()})); !strings.Contains(body, "goroutine profile") {
		t.Errorf("mounted: %.80q, want the goroutine profile", body)
	}
	if body := get(Handler(ui, nil, nil)); body != "<!doctype html>" {
		t.Errorf("not mounted: %.80q, want the interface", body)
	}
}

// TestDumpWritesTheGoroutines: a POST to DumpPath writes a dump to the folder and answers its path (W511).
func TestDumpWritesTheGoroutines(t *testing.T) {
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
