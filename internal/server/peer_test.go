package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/empowill/djinn/internal/server"
)

func TestPeerPIDFromContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"none", context.Background(), 0},
		{"set", server.WithPeerPID(context.Background(), 42), 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := server.PeerPIDFromContext(tc.ctx); got != tc.want {
				t.Errorf("PeerPIDFromContext = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWorkerContext: attach gets the peer's PID and the worker's token, and the request goes on with its context.
func TestWorkerContext(t *testing.T) {
	t.Parallel()
	type key struct{}
	for _, tc := range []struct {
		name   string
		attach bool
		want   any
	}{
		{"attached", true, "42 w-token"},
		{"no attach", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var attach func(context.Context, int, string) context.Context
			if tc.attach {
				attach = func(ctx context.Context, pid int, token string) context.Context {
					if pid != 42 || token != "w-token" {
						return ctx
					}
					return context.WithValue(ctx, key{}, "42 w-token")
				}
			}
			var got any
			h := server.WorkerContext(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.Context().Value(key{})
			}), attach)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(server.WithPeerPID(req.Context(), 42))
			req.Header.Set("X-Djinn-Worker-Token", "w-token")
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("the request carries %v, want %v", got, tc.want)
			}
		})
	}
}
