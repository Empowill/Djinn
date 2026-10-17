package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// TestLogged: each call logs its method and duration, at warn past logx.Slow, and a failed one its code and error
// (W511); an internal error logs at error, its cause last; a panic answers 500 (W525). The cases share the global log:
// they run in turn, before the parallel tests of the package.
func TestLogged(t *testing.T) {
	// One call sleeps past logx.Slow, a constant of a second: the warn it logs is what the case checks.
	t.Run("slow and failed calls", func(t *testing.T) {
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
	})

	// A call or a stream that fails with an internal error logs at error once, with its whole message, its cause last.
	t.Run("internal errors log their cause", func(t *testing.T) {
		var log lockedBuffer
		defer logx.Use(logx.New(&log, slog.LevelDebug))()
		long := strings.Repeat("context: ", 40) + "disk full"
		failing := func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return nil, connect.NewError(connect.CodeInternal, errors.New(long))
		}
		watch := func(_ context.Context, _ *connect.Request[emptypb.Empty], s *connect.ServerStream[emptypb.Empty]) error {
			if err := s.Send(&emptypb.Empty{}); err != nil {
				return err
			}
			return connect.NewError(connect.CodeInternal, errors.New("read the store: database is locked"))
		}
		h := server.Handler(ui, nil, map[string]http.Handler{
			"/test.v1.FailService/":  connect.NewUnaryHandler("/test.v1.FailService/Call", failing),
			"/test.v1.WatchService/": connect.NewServerStreamHandler("/test.v1.WatchService/Watch", watch),
		})
		srv := httptest.NewServer(h)
		defer srv.Close()
		call := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+"/test.v1.FailService/Call")
		if _, err := call.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("call: %v", err)
		}
		stream := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+"/test.v1.WatchService/Watch")
		s, err := stream.CallServerStream(context.Background(), connect.NewRequest(&emptypb.Empty{}))
		if err != nil {
			t.Fatal(err)
		}
		for s.Receive() {
		}
		if connect.CodeOf(s.Err()) != connect.CodeInternal {
			t.Fatalf("stream: %v", s.Err())
		}
		s.Close()
		got := log.String()
		for _, want := range []string{
			"level=ERROR msg=call took=",
			"code=internal error=\"" + long + "\"",
			"level=ERROR msg=stream method=/test.v1.WatchService/Watch code=internal error=\"read the store: database is locked\"",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the log lacks %q:\n%s", want, got)
			}
		}
		if n := strings.Count(got, "\n"); n != 2 {
			t.Errorf("%d lines for 2 calls:\n%s", n, got)
		}
	})

	// A handler that panics answers 500, an internal Connect error, logs at error with its stack, and the server goes
	// on serving.
	t.Run("a panic answers 500", func(t *testing.T) {
		var log lockedBuffer
		defer logx.Use(logx.New(&log, slog.LevelDebug))()
		panics := func(_ context.Context, r *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			panic("no task for " + r.Spec().Procedure)
		}
		h := server.Handler(ui, nil, map[string]http.Handler{
			"/test.v1.PanicService/": connect.NewUnaryHandler("/test.v1.PanicService/Call", panics),
		})
		srv := httptest.NewServer(h)
		defer srv.Close()
		for range 2 {
			res, err := http.Post(srv.URL+"/test.v1.PanicService/Call", "application/proto", nil)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), `"code":"internal"`) {
				t.Errorf("status %d, body %s", res.StatusCode, body)
			}
		}
		got := log.String()
		for _, want := range []string{
			"level=ERROR msg=\"panic recovered\" in=call method=/test.v1.PanicService/Call",
			"panic=\"no task for /test.v1.PanicService/Call\"",
			"stack=\"goroutine ",
			"calls_test.go",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the log lacks %q:\n%s", want, got)
			}
		}
		if n := strings.Count(got, "\n"); n != 2 {
			t.Errorf("%d lines for 2 panics:\n%s", n, got)
		}
	})
}
