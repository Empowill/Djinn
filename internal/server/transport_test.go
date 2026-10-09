package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
	"github.com/empowill/djinn/internal/server"
)

func TestTransportFor(t *testing.T) {
	for goos, want := range map[string]server.Transport{
		"windows": server.HTTP,
		"darwin":  server.Wails,
		"linux":   server.Wails,
		"freebsd": server.Wails,
	} {
		if got := server.TransportFor(goos); got != want {
			t.Errorf("TransportFor(%q) = %q, want %q", goos, got, want)
		}
	}
}

// shortDir returns a fresh directory with a short path: a Unix socket path is limited to about 100 bytes, and
// t.TempDir is longer than that on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// ownerOnly checks that path is readable and writable by its owner only. Windows has no such mode bits.
func ownerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s: mode %o, want 600", path, perm)
	}
}

func TestAddrFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home") // Created by WriteAddr.
	if _, err := server.ReadAddr(home); err == nil || !strings.Contains(err.Error(), "djinn up") {
		t.Fatalf("no file: %v, want an error that says to run djinn up", err)
	}

	const addr = "http://127.0.0.1:4000/?token=s3cret"
	path := filepath.Join(home, server.AddrFile)
	// A file left with loose permissions is replaced, not reused.
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	remove, err := server.WriteAddr(home, addr)
	if err != nil {
		t.Fatal(err)
	}
	ownerOnly(t, path)
	if got, err := server.ReadAddr(home); err != nil || got != addr {
		t.Fatalf("ReadAddr = %q, %v, want %q", got, err, addr)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("after remove: %v, want no file", err)
	}

	// A server that started since owns the file: the first one leaves it.
	remove, err = server.WriteAddr(home, addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.WriteAddr(home, "unix:///elsewhere/djinn.sock"); err != nil {
		t.Fatal(err)
	}
	remove()
	if got, err := server.ReadAddr(home); err != nil || got != "unix:///elsewhere/djinn.sock" {
		t.Fatalf("after the first server stops: %q, %v, want the second address", got, err)
	}
}

func TestListenUnix(t *testing.T) {
	path := filepath.Join(shortDir(t), "djinn.sock")
	ln, err := server.ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	ownerOnly(t, path)
	if _, err := server.ListenUnix(path); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second listener: %v, want already running", err)
	}
	ln.Close()

	// A socket file nobody listens on, as a crash leaves it, is replaced.
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stale socket should remain: %v", err)
	}
	ln, err = server.ListenUnix(path)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	ln.Close()

	long := filepath.Join(shortDir(t), strings.Repeat("d", 100), "djinn.sock")
	if _, err := server.ListenUnix(long); err == nil || !strings.Contains(err.Error(), "DJINN_HOME") {
		t.Fatalf("long path: %v, want a hint to shorten DJINN_HOME", err)
	}
}

// protoClient returns an HTTP client to network and address that speaks HTTP/1.1, or h2c only (prior knowledge).
func protoClient(network, address string, h2c bool) *http.Client {
	var d net.Dialer
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialContext(ctx, network, address) },
	}
	if h2c {
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetUnencryptedHTTP2(true)
	}
	return &http.Client{Transport: transport}
}

// echoHandler serves bench.v1.Echo/Echo, a unary call, and bench.v1.Echo/Chat, a bidirectional stream, which
// return what they receive.
func echoHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/bench.v1.Echo/Echo", connect.NewUnaryHandler("/bench.v1.Echo/Echo",
		func(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			return connect.NewResponse(req.Msg), nil
		}))
	mux.Handle("/bench.v1.Echo/Chat", connect.NewBidiStreamHandler("/bench.v1.Echo/Chat",
		func(_ context.Context, s *connect.BidiStream[wrapperspb.StringValue, wrapperspb.StringValue]) error {
			for {
				msg, err := s.Receive()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := s.Send(msg); err != nil {
					return err
				}
			}
		}))
	demoPrefix, demo := demov1connect.NewDemoServiceHandler(countTo{})
	return server.Handler(fstest.MapFS{}, nil, map[string]http.Handler{"/bench.v1.Echo/": mux, demoPrefix: demo})
}

// countTo sends 1, 2, … up to the request at once: demo.Service waits between two values.
type countTo struct{}

func (countTo) Count(
	_ context.Context, req *connect.Request[demov1.CountRequest], s *connect.ServerStream[demov1.CountResponse],
) error {
	for v := int32(1); v <= req.Msg.GetUpTo(); v++ {
		if err := s.Send(&demov1.CountResponse{Value: v}); err != nil {
			return err
		}
	}
	return nil
}

// TestUnixSocketSpeaksH2C checks the protocols of the Unix socket: HTTP/1.1, which the command line uses, and h2c,
// which carries a bidirectional stream message by message.
func TestUnixSocketSpeaksH2C(t *testing.T) {
	socket := filepath.Join(shortDir(t), server.SocketFile)
	ln, err := server.ListenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.

	for _, h2c := range []bool{false, true} {
		res, err := protoClient("unix", socket, h2c).Get("http://djinn/")
		if err != nil {
			t.Fatalf("h2c %v: %v", h2c, err)
		}
		res.Body.Close()
		if want := map[bool]int{false: 1, true: 2}[h2c]; res.ProtoMajor != want {
			t.Errorf("h2c %v: HTTP/%d, want HTTP/%d", h2c, res.ProtoMajor, want)
		}
	}

	// Each answer comes back before the next message is sent: the stream goes both ways at once. A transport that
	// waited for the end of the request would block here, until the deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	chat := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
		protoClient("unix", socket, true), "http://djinn/bench.v1.Echo/Chat").CallBidiStream(ctx)
	for _, word := range []string{"one", "two", "three"} {
		if err := chat.Send(wrapperspb.String(word)); err != nil {
			t.Fatalf("send %s: %v", word, err)
		}
		got, err := chat.Receive()
		if err != nil {
			t.Fatalf("receive %s: %v", word, err)
		}
		if got.GetValue() != word {
			t.Fatalf("got %q, want %q", got.GetValue(), word)
		}
	}
	if err := chat.CloseRequest(); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of the chat: %v, want EOF", err)
	}
	chat.CloseResponse()
}

// TestLoopbackStaysHTTP1 checks that the loopback server, which browsers reach, speaks HTTP/1.1 only.
func TestLoopbackStaysHTTP1(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.

	res, err := protoClient("tcp", ln.Addr().String(), false).Get("http://djinn/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.ProtoMajor != 1 {
		t.Errorf("HTTP/%d, want HTTP/1.1", res.ProtoMajor)
	}
	if res, err := protoClient("tcp", ln.Addr().String(), true).Get("http://djinn/"); err == nil {
		res.Body.Close()
		t.Fatalf("h2c over TCP answered HTTP/%d, want a refusal", res.ProtoMajor)
	}
}

// TestNoCompression checks that the services answer in the clear although connect-go clients, like the command
// line, and browsers ask for gzip.
func TestNoCompression(t *testing.T) {
	socket := filepath.Join(shortDir(t), server.SocketFile)
	ln, err := server.ListenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(t.Context(), ln, echoHandler()) //nolint:errcheck // Stops with the test.
	hc := protoClient("unix", socket, false)

	// connect-go with its defaults, as the command line calls: it accepts gzip.
	var sent, encoding string
	spy := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			sent = req.Header().Get("Accept-Encoding")
			if res != nil {
				encoding = res.Header().Get("Content-Encoding")
			}
			return res, err
		}
	}))
	echo := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](hc, "http://djinn/bench.v1.Echo/Echo", spy)
	big := strings.Repeat("compressible ", 1000)
	res, err := echo.CallUnary(t.Context(), connect.NewRequest(wrapperspb.String(big)))
	if err != nil || res.Msg.GetValue() != big {
		t.Fatalf("echo: %v", err)
	}
	if !strings.Contains(sent, "gzip") {
		t.Fatalf("the client sent Accept-Encoding %q: the test no longer asks for gzip", sent)
	}
	if encoding != "" {
		t.Errorf("unary response: Content-Encoding %q, want none", encoding)
	}

	// A browser asks with Accept-Encoding on every request.
	req, _ := http.NewRequest(http.MethodPost, "http://djinn/bench.v1.Echo/Echo", strings.NewReader(`"`+big+`"`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	raw, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw.Body.Close()
	if raw.StatusCode != http.StatusOK || raw.Header.Get("Content-Encoding") != "" {
		t.Errorf("browser-like unary: status %d, Content-Encoding %q, want 200 and none",
			raw.StatusCode, raw.Header.Get("Content-Encoding"))
	}

	// A stream: connect-go asks with Connect-Accept-Encoding.
	stream, err := demov1connect.NewDemoServiceClient(hc, "http://djinn").
		Count(t.Context(), connect.NewRequest(&demov1.CountRequest{UpTo: 3}))
	if err != nil {
		t.Fatal(err)
	}
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if e := stream.ResponseHeader().Get("Connect-Content-Encoding"); e != "" {
		t.Errorf("stream: Connect-Content-Encoding %q, want none", e)
	}
	stream.Close()
}
