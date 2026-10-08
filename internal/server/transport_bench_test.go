package server

// Benchmarks of the local transport, run with `go tool task bench`. They compare, over a real Unix socket, the
// variants docs/transport.md weighs: HTTP/1.1 or HTTP/2 without TLS (h2c), gzip or not, the binary or the JSON
// codec. Each serves through an http.Server like Serve, and calls like the command line, with connect-go.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	echoPath  = "/bench.v1.Bench/Echo"
	burstPath = "/bench.v1.Bench/Burst"
)

// statePayload returns a JSON document of about size bytes shaped like the saved workspace (the stateJson of
// ui.v1.SaveStateRequest): steps with French text, quotes and new lines, the characters JSON escapes.
func statePayload(size int) string {
	var b strings.Builder
	b.WriteString(`{"version":3,"steps":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"step-%05d","title":"Étape %d : vérifier le \"flux\" du serveur","status":"done",`+
			`"body":"L'agent a lu internal/server/server.go\npuis a lancé les tests : 42 passés.","at":%d}`,
			i, i, 1760000000000+i)
	}
	b.WriteString(`]}`)
	return b.String()
}

// benchHandler serves Echo, a unary call that returns its request, and Burst, a server stream of n messages of
// the given payload, through Handler. With gzip, the services keep connect-go's default: they compress when the
// client asks.
func benchHandler(gzip bool, payload string) http.Handler {
	echo := connect.NewUnaryHandler(echoPath,
		func(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			return connect.NewResponse(req.Msg), nil
		})
	msg := wrapperspb.String(payload)
	burst := connect.NewServerStreamHandler(burstPath,
		func(_ context.Context, req *connect.Request[wrapperspb.UInt32Value], s *connect.ServerStream[wrapperspb.StringValue]) error {
			for range req.Msg.GetValue() {
				if err := s.Send(msg); err != nil {
					return err
				}
			}
			return nil
		})
	mux := http.NewServeMux()
	mux.Handle(echoPath, echo)
	mux.Handle(burstPath, burst)
	if gzip {
		return mux
	}
	return Handler(fstest.MapFS{}, map[string]http.Handler{"/bench.v1.Bench/": mux})
}

// variant is one way to carry the calls over the socket.
type variant struct {
	name string
	h2c  bool // HTTP/2 without TLS; HTTP/1.1 otherwise
	gzip bool // the server compresses when asked, as connect-go does by default
}

var variants = []variant{
	{"http1-gzip", false, true}, // before: connect-go's defaults over HTTP/1.1
	{"http1", false, false},
	{"h2c-gzip", true, true},
	{"h2c", true, false}, // after
}

// listen serves h on a fresh Unix socket and returns its path.
func listen(b *testing.B, h http.Handler, h2c bool) string {
	dir, err := os.MkdirTemp("", "djb")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, SocketFile)
	ln, err := ListenUnix(socket)
	if err != nil {
		b.Fatal(err)
	}
	// HTTP/1.1 and h2c, as Serve does on a Unix socket.
	srv := newServer(context.Background(), ln, h)
	go srv.Serve(ln) //nolint:errcheck // Closed with the benchmark.
	b.Cleanup(func() { srv.Close() })
	return socket
}

// client returns an HTTP client to socket, over HTTP/1.1 like the command line, or over h2c.
func client(socket string, h2c bool) *http.Client {
	var d net.Dialer
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialContext(ctx, "unix", socket) },
	}
	if h2c {
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetUnencryptedHTTP2(true)
	}
	return &http.Client{Transport: transport}
}

// BenchmarkUnary measures one call with a request and a response of the given size, on a kept connection: an
// agent calling the server again and again, or the window saving its state.
func BenchmarkUnary(b *testing.B) {
	for _, size := range []int{1 << 10, 32 << 10, 512 << 10} {
		payload := statePayload(size)
		for _, v := range variants {
			for _, codec := range []string{"proto", "json"} {
				if codec == "json" && v.name != "h2c" {
					continue // The codec costs the same on every variant.
				}
				b.Run(fmt.Sprintf("%dKiB/%s/%s", size>>10, v.name, codec), func(b *testing.B) {
					socket := listen(b, benchHandler(v.gzip, ""), v.h2c)
					var opts []connect.ClientOption
					if codec == "json" {
						opts = append(opts, connect.WithProtoJSON())
					}
					c := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
						client(socket, v.h2c), "http://djinn"+echoPath, opts...)
					req := connect.NewRequest(wrapperspb.String(payload))
					b.SetBytes(int64(2 * len(payload)))
					b.ReportAllocs()
					for b.Loop() {
						res, err := c.CallUnary(b.Context(), req)
						if err != nil {
							b.Fatal(err)
						}
						if len(res.Msg.GetValue()) != len(payload) {
							b.Fatal("short echo")
						}
					}
				})
			}
		}
	}
}

// BenchmarkColdCall measures what a command line does: a new connection, then one small call.
func BenchmarkColdCall(b *testing.B) {
	payload := statePayload(256)
	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			socket := listen(b, benchHandler(v.gzip, ""), v.h2c)
			req := connect.NewRequest(wrapperspb.String(payload))
			b.ReportAllocs()
			for b.Loop() {
				hc := client(socket, v.h2c)
				c := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](hc, "http://djinn"+echoPath)
				if _, err := c.CallUnary(b.Context(), req); err != nil {
					b.Fatal(err)
				}
				hc.CloseIdleConnections()
			}
		})
	}
}

// BenchmarkStream measures a server stream of 1000 messages of 256 bytes, an agent's events: the time per
// message, from the server's Send to the client's Receive.
func BenchmarkStream(b *testing.B) {
	const n = 1000
	payload := statePayload(256)
	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			socket := listen(b, benchHandler(v.gzip, payload), v.h2c)
			c := connect.NewClient[wrapperspb.UInt32Value, wrapperspb.StringValue](
				client(socket, v.h2c), "http://djinn"+burstPath)
			b.SetBytes(int64(n * len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				s, err := c.CallServerStream(b.Context(), connect.NewRequest(wrapperspb.UInt32(n)))
				if err != nil {
					b.Fatal(err)
				}
				got := 0
				for s.Receive() {
					got++
				}
				if err := s.Err(); err != nil || got != n {
					b.Fatalf("received %d of %d: %v", got, n, err)
				}
				s.Close()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/msg")
		})
	}
}
