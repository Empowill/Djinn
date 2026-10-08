// Command windowcheck tells whether a Connect server stream reaches the native window of Djinn message by message,
// on the path the window really uses on this system: the Wails internal asset server (wails://) on macOS and
// Linux, the loopback HTTP server with its token on Windows; and whether a unary call with an empty response still
// succeeds there. It opens a window for a few seconds, prints the arrivals and their latency, then PASS or FAIL,
// and exits with 0 or 1. With -bench, the page then times the round trips of the two Connect formats, binary
// Protobuf and JSON, from its own clock (see docs/transport.md).
//
//	go tool task check-window
//	go tool task check-window -- -path http
//	go tool task check-window -- -bench
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
	"github.com/empowill/djinn/internal/server"
)

//go:embed page
var page embed.FS

// result is what the page posts once the stream ends: when it read each value, in milliseconds since the epoch,
// and how many reads of the response body it took.
type result struct {
	Values []struct {
		V int32   `json:"v"`
		T float64 `json:"t"`
	} `json:"values"`
	Reads int          `json:"reads"`
	Error string       `json:"error"`
	Bench *benchResult `json:"bench"`
}

// benchResult is what the page measured with -bench, in milliseconds of its own clock.
type benchResult struct {
	Unary []struct {
		Format string  `json:"format"`
		Size   int     `json:"size"`
		Calls  int     `json:"calls"`
		MS     float64 `json:"ms"`
	} `json:"unary"`
	Burst []struct {
		Format   string  `json:"format"`
		Messages int     `json:"messages"`
		MS       float64 `json:"ms"`
		Reads    int     `json:"reads"`
	} `json:"burst"`
}

func main() {
	os.Exit(run())
}

func run() int {
	native := server.TransportFor(runtime.GOOS)
	path := flag.String("path", string(native), "the path to check: wails (wails://, no port) or http (loopback HTTP with a token)")
	count := flag.Int("count", 100, "number of values in the stream")
	interval := flag.Duration("interval", 20*time.Millisecond, "time between two values")
	timeout := flag.Duration("timeout", 30*time.Second, "give up after this long")
	format := flag.String("format", "binary", "the Connect format of the stream: binary (Protobuf, as the interface) or json")
	benchmark := flag.Bool("bench", false, "then time unary round trips and a burst of messages in both formats")
	flag.Parse()
	transport := server.Transport(*path)
	if transport != server.Wails && transport != server.HTTP || *count < 2 || *interval <= 0 ||
		*format != "binary" && *format != "json" {
		flag.Usage()
		return 2
	}

	fmt.Println("Djinn window check")
	fmt.Printf("  system:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
	what := map[server.Transport]string{
		server.Wails: "wails: the Wails internal asset server (wails://), no port",
		server.HTTP:  "http: the loopback HTTP server, with a token",
	}[transport]
	if transport == native {
		what += ", the path of the window on this system"
	} else {
		what += fmt.Sprintf(", NOT the path of the window on this system (%s)", native)
	}
	fmt.Printf("  path:    %s\n", what)
	fmt.Printf("  stream:  %d values, one every %s, over Connect in %s\n", *count, *interval, *format)
	fmt.Println("Opening a native window for a few seconds...")

	c := &counter{interval: *interval, sent: map[int32]time.Time{}}
	results := make(chan result, 1)
	ui, _ := fs.Sub(page, "page") // "page" is a valid path: fs.Sub cannot fail on it.
	demoPrefix, demoHandler := demov1connect.NewDemoServiceHandler(c)
	var acceptEncoding sync.Map // What the webview asks of a unary call, before Handler hides it.
	h := server.Handler(ui, map[string]http.Handler{
		demoPrefix:               demoHandler,
		"/windowcheck.v1.Bench/": benchService(),
		"/windowcheck/result": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var res result
			if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
				res.Error = "unreadable result: " + err.Error()
			}
			select {
			case results <- res:
			default:
			}
		}),
	})

	h = recordAcceptEncoding(h, &acceptEncoding)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	query := fmt.Sprintf("count=%d&format=%s", *count, *format)
	if *benchmark {
		query += "&bench=1"
	}
	var err error
	switch transport {
	case server.Wails:
		err = showWindow(ctx, "/?"+query, h)
	case server.HTTP:
		err = viaHTTP(ctx, query, h)
	}

	select {
	case res := <-results:
		code := report(res, c.sentTimes(), *count, *interval)
		if res.Bench != nil {
			reportBench(*res.Bench, &acceptEncoding)
		}
		return code
	default:
	}
	switch {
	case err != nil:
		fmt.Println("FAIL: the window did not run:", err)
	case ctx.Err() != nil:
		fmt.Printf("FAIL: no result after %s: the page did not finish reading the stream.\n", *timeout)
	default:
		fmt.Println("FAIL: the window was closed before the end of the stream.")
	}
	return 1
}

// viaHTTP serves h on the loopback behind the guard, as djinn up does on Windows, and opens the window on it.
func viaHTTP(ctx context.Context, query string, h http.Handler) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	origin := "http://" + ln.Addr().String()
	token := server.NewToken()
	ctx, stop := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, ln, server.Guard(h, token, origin)) }()
	err = showWindow(ctx, origin+"/?token="+token+"&"+query, nil)
	stop()
	return errors.Join(err, <-served)
}

// benchService serves windowcheck.v1.Bench, which only exists here and needs no proto: Echo returns its request,
// a google.protobuf.StringValue; Burst streams as many StringValue of 256 bytes as its UInt32Value request says.
func benchService() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/windowcheck.v1.Bench/Echo", connect.NewUnaryHandler("/windowcheck.v1.Bench/Echo",
		func(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			return connect.NewResponse(req.Msg), nil
		}))
	msg := wrapperspb.String(strings.Repeat("djinn ", 43)[:256])
	mux.Handle("/windowcheck.v1.Bench/Burst", connect.NewServerStreamHandler("/windowcheck.v1.Bench/Burst",
		func(_ context.Context, req *connect.Request[wrapperspb.UInt32Value], s *connect.ServerStream[wrapperspb.StringValue]) error {
			for range req.Msg.GetValue() {
				if err := s.Send(msg); err != nil {
					return err
				}
			}
			return nil
		}))
	return mux
}

// recordAcceptEncoding notes the Accept-Encoding of the unary calls to Bench.Echo, by format.
func recordAcceptEncoding(h http.Handler, seen *sync.Map) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/windowcheck.v1.Bench/Echo" {
			seen.LoadOrStore(r.Header.Get("Content-Type"), r.Header.Get("Accept-Encoding"))
		}
		h.ServeHTTP(w, r)
	})
}

// reportBench prints what the page timed: the mean of each run of unary round trips, and each burst.
func reportBench(b benchResult, acceptEncoding *sync.Map) {
	fmt.Println()
	fmt.Println("Benchmark, timed by the page (one clock), the server answering in the clear:")
	acceptEncoding.Range(func(k, v any) bool {
		fmt.Printf("  the webview sends Accept-Encoding %q with %s\n", v, k)
		return true
	})
	fmt.Println("  unary echo of a saved workspace, mean round trip:")
	for _, u := range b.Unary {
		fmt.Printf("    %4d KiB  %-5s  %7.3f ms  (%d calls)\n", u.Size>>10, u.Format, u.MS/float64(u.Calls), u.Calls)
	}
	fmt.Println("  server stream of 256-byte messages:")
	for _, s := range b.Burst {
		fmt.Printf("    %d messages  %-5s  %7.1f ms  %5.1f µs/message  %d reads\n",
			s.Messages, s.Format, s.MS, s.MS*1000/float64(s.Messages), s.Reads)
	}
}

// counter implements demo.v1.DemoService like internal/demo, with its own interval, and records when it sends
// each value.
type counter struct {
	interval time.Duration
	mu       sync.Mutex
	sent     map[int32]time.Time
}

func (c *counter) Count(
	ctx context.Context, req *connect.Request[demov1.CountRequest], stream *connect.ServerStream[demov1.CountResponse],
) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for v := int32(1); v <= req.Msg.GetUpTo(); v++ {
		if v > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		c.mu.Lock()
		c.sent[v] = time.Now()
		c.mu.Unlock()
		if err := stream.Send(&demov1.CountResponse{Value: v}); err != nil {
			return err
		}
	}
	return nil
}

func (c *counter) sentTimes() map[int32]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent
}

// report prints the arrivals and returns the exit code: 0 when the values arrived one by one.
func report(res result, sent map[int32]time.Time, count int, interval time.Duration) int {
	a := analyze(res, sent, count, interval)
	fmt.Println()
	if res.Error != "" {
		fmt.Println("Error in the page:", res.Error)
	}
	fmt.Printf("Received:   %d of %d values in %.2f s (about %.2f s if streamed, near 0 if buffered)\n",
		a.received, count, a.span/1000, a.expected/1000)
	if a.received > 1 {
		fmt.Printf("Arrivals:   %d separate reads, median gap %.1f ms (one read per value and %s if streamed)\n",
			a.reads, a.medianGap, interval)
		fmt.Printf("Body reads: %d for %d values\n", res.Reads, a.received)
	}
	if len(a.latencies) > 0 {
		fmt.Printf("Latency:    median %.1f ms, p90 %.1f ms, max %.1f ms, from the Go send to the page read\n",
			a.latency(0.5), a.latency(0.9), a.latency(1))
		fmt.Println("            (both clocks are this machine's: Go's wall clock, and the page's")
		fmt.Println("            performance.timeOrigin + performance.now(); they may differ by about a")
		fmt.Println("            millisecond, so a latency under 1 ms, even negative, reads as no delay)")
	}
	fmt.Println()
	if a.pass {
		fmt.Println("PASS: the values arrived one by one: a stream reaches the window on this path.")
		return 0
	}
	if a.received > 1 && res.Error == "" {
		fmt.Print("Gaps in ms: ")
		for _, g := range a.gaps {
			fmt.Printf("%.1f ", g)
		}
		fmt.Println()
	}
	switch {
	case res.Error != "":
		fmt.Println("FAIL: the page met an error (above).")
	case a.received < count:
		fmt.Printf("FAIL: %d of %d values arrived.\n", a.received, count)
	default:
		fmt.Println("FAIL: the values arrived in a burst: the window gets the response only once it is complete, so")
		fmt.Println("a stream does not reach it on this path.")
	}
	return 1
}

// analysis is what the arrivals say.
type analysis struct {
	received       int
	gaps           []float64 // between two arrivals, in milliseconds
	medianGap      float64
	reads          int       // groups of values read together, apart from the next by a quarter of the interval
	span, expected float64   // first to last arrival, measured and if streamed, in milliseconds
	latencies      []float64 // from send to read, sorted, in milliseconds
	pass           bool
}

// analyze passes a stream whose values all arrived, spread over at least half the time they took to send, in at
// least half as many separate reads as values: a webview may read two values together now and then. A buffered
// response arrives all at once: one read, a span near 0.
func analyze(res result, sent map[int32]time.Time, count int, interval time.Duration) analysis {
	a := analysis{received: len(res.Values), expected: float64(count-1) * ms(interval)}
	for i, v := range res.Values {
		if i > 0 {
			a.gaps = append(a.gaps, v.T-res.Values[i-1].T)
		}
		if at, ok := sent[v.V]; ok {
			a.latencies = append(a.latencies, v.T-float64(at.UnixMicro())/1000)
		}
	}
	if a.received > 1 {
		a.span = res.Values[a.received-1].T - res.Values[0].T
		a.medianGap = quantile(sorted(a.gaps), 0.5)
		a.reads = 1
		for _, g := range a.gaps {
			if g >= ms(interval)/4 {
				a.reads++
			}
		}
	}
	a.latencies = sorted(a.latencies)
	a.pass = res.Error == "" && a.received == count && a.span >= a.expected/2 && 2*a.reads >= count
	return a
}

func (a analysis) latency(q float64) float64 { return quantile(a.latencies, q) }

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func sorted(xs []float64) []float64 {
	out := slices.Clone(xs)
	slices.Sort(out)
	return out
}

// quantile is the nearest-rank quantile q of sorted xs: 0.5 the median, 1 the max.
func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	i := int(math.Ceil(q * float64(len(xs))))
	return xs[max(i, 1)-1]
}
