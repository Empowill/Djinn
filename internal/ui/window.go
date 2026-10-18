package ui

import (
	"context"
	"net/http"
)

type windowKey struct{}

// FromWindow marks the calls h receives as the native window's: on macOS and Linux, the window's page reaches the
// server through the Wails asset server, apart from the command line's socket.
func FromWindow(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), windowKey{}, true)))
	})
}

// InWindow tells that a call comes from the window's page: marked by FromWindow, or, over HTTP (Windows, a browser),
// sent by a page, which names its Origin, where the command line names none.
func InWindow(ctx context.Context, header http.Header) bool {
	in, _ := ctx.Value(windowKey{}).(bool)
	return in || header.Get("Origin") != ""
}

// StopBy names who asks Djinn to restart on a newer one, for the log of its stop (decision Q84): an install of a
// build, or a restart, from the window or from the command line (djinn update).
func StopBy(ctx context.Context, header http.Header, install bool) string {
	what, from := "a restart on a newer Djinn", "the command line (djinn update)"
	if install {
		what = "an install"
	}
	if InWindow(ctx, header) {
		from = "the window"
	}
	return what + " from " + from
}
