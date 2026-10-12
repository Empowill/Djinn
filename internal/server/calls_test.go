package server_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/server"
)

// lockedBuffer is a buffer the log and the test share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestCallsAreLogged: each call logs its method and duration, at warn past logx.Slow, and a failed one its code and
// error (W511).
func TestCallsAreLogged(t *testing.T) {
	var log lockedBuffer
	defer logx.Use(logx.New(&log, slog.LevelDebug))()
	slow := func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		time.Sleep(logx.Slow + 50*time.Millisecond)
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	failing := func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no task W9"))
	}
	h := server.Handler(ui, nil, map[string]http.Handler{
		"/test.v1.SlowService/": connect.NewUnaryHandler("/test.v1.SlowService/Call", slow),
		"/test.v1.FailService/": connect.NewUnaryHandler("/test.v1.FailService/Call", failing),
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, s := range []string{"Slow", "Fail"} {
		res, err := http.Post(srv.URL+"/test.v1."+s+"Service/Call", "application/proto", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	got := log.String()
	for _, want := range []string{
		"level=WARN msg=call took=1.",
		"method=/test.v1.SlowService/Call",
		"level=INFO msg=call took=",
		"method=/test.v1.FailService/Call status=404 code=not_found error=\"no task W9\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log lacks %q:\n%s", want, got)
		}
	}
}
