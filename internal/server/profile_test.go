package server

import (
	"io"
	"net/http"
	"net/http/httptest"
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
