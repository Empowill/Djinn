package server

import (
	"context"
	"net/http"
)

type peerPIDKey struct{}

// WithPeerPID returns a context carrying the peer PID.
func WithPeerPID(ctx context.Context, pid int) context.Context {
	return context.WithValue(ctx, peerPIDKey{}, pid)
}

// PeerPIDFromContext returns the peer PID from ctx, or 0 if none.
func PeerPIDFromContext(ctx context.Context) int {
	if pid, ok := ctx.Value(peerPIDKey{}).(int); ok {
		return pid
	}
	return 0
}

// WorkerContext wraps next so that any worker making a request is identified and its identity
// is placed in the request context.
func WorkerContext(next http.Handler, attach func(ctx context.Context, peerPID int, token string) context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		peerPID := PeerPIDFromContext(ctx)
		token := r.Header.Get("X-Djinn-Worker-Token")
		if attach != nil {
			ctx = attach(ctx, peerPID, token)
			r = r.Clone(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
