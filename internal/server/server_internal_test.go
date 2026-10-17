package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownServer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		// A held request outlives the graceful deadline: its connection is closed, and the shutdown succeeds.
		{"force closes after its deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, nil},
		// A cancelled shutdown is not a deadline: it says so and leaves the connections.
		{"keeps a cancelled context", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			hold := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(started)
				<-hold
			}))
			defer srv.Close()
			defer close(hold)

			response := make(chan error, 1)
			go func() {
				res, err := srv.Client().Get(srv.URL)
				if res != nil {
					res.Body.Close()
				}
				response <- err
			}()
			<-started
			ctx, cancel := tc.ctx()
			defer cancel()
			err := shutdownServer(ctx, srv.Config)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("shutdownServer = %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				_ = srv.Config.Close()
			}
			select {
			case <-response:
			case <-time.After(time.Second):
				t.Fatal("the held request did not finish after its connection closed")
			}
		})
	}
}
