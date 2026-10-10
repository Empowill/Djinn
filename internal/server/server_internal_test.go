package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownServerForceClosesAfterDeadline(t *testing.T) {
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
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := shutdownServer(ctx, srv.Config); err != nil {
		t.Fatalf("shutdownServer: %v", err)
	}
	select {
	case <-response:
	case <-time.After(time.Second):
		t.Fatal("the held request did not finish after its connection closed")
	}
}

func TestShutdownServerPreservesCanceledContext(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := shutdownServer(ctx, srv.Config)
	_ = srv.Config.Close()
	<-response
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdownServer = %v, want context canceled", err)
	}
}
