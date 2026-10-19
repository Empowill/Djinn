package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStopBy covers StopBy, and InWindow and FromWindow under it: an install or a restart, from the window (marked,
// or a page's Origin over HTTP) or from the command line.
func TestStopBy(t *testing.T) {
	marked := func() context.Context {
		var ctx context.Context
		FromWindow(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })).
			ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ui.v1.UiService/Update", nil))
		return ctx
	}
	page := http.Header{"Origin": {"http://127.0.0.1:4321"}}
	for _, c := range []struct {
		name    string
		ctx     context.Context
		header  http.Header
		install bool
		want    string
	}{
		{"the window's install, through Wails", marked(), http.Header{}, true, "an install from the window"},
		{"the window's restart, over HTTP", t.Context(), page, false, "a restart on a newer Djinn from the window"},
		{"djinn update", t.Context(), http.Header{}, false, "a restart on a newer Djinn from the command line (djinn update)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := StopBy(c.ctx, c.header, c.install); got != c.want {
				t.Errorf("StopBy = %q; want %q", got, c.want)
			}
		})
	}
}
