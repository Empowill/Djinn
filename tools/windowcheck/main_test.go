package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	demov1 "github.com/empowill/djinn/gen/go/demo/v1"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
)

// bearer adds the token of the loopback server to every request, as the window's cookie does.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// streamInterval is the interval of the streams TestRun asks for.
const streamInterval = 10 * time.Millisecond

// fakePage stands for the page of the window: it reads the stream the query asks for, as page/index.html does, times the
// two formats with bench=1, and posts what it read. Its mode says how it ends instead: "closed" returns at once,
// "broken" fails to open, "stuck" waits for the deadline, "garbled" posts what cannot be read.
func fakePage(mode string) show {
	return func(ctx context.Context, address string, assets http.Handler) error {
		switch mode {
		case "closed":
			return nil
		case "broken":
			return errors.New("no display")
		case "stuck":
			<-ctx.Done()
			return nil
		}
		u, err := url.Parse(address)
		if err != nil {
			return err
		}
		base := u.Scheme + "://" + u.Host
		if assets != nil {
			srv := httptest.NewServer(assets)
			defer srv.Close()
			base = srv.URL
		}
		client := &http.Client{Transport: bearer{u.Query().Get("token")}}
		post := func(body string) error {
			res, err := client.Post(base+"/windowcheck/result", "application/json", strings.NewReader(body))
			if err != nil {
				return err
			}
			return res.Body.Close()
		}
		if mode == "garbled" {
			return post("{")
		}
		var opts []connect.ClientOption
		if u.Query().Get("format") == "json" {
			opts = append(opts, connect.WithProtoJSON())
		}
		count, _ := strconv.Atoi(u.Query().Get("count"))
		stream, err := demov1connect.NewDemoServiceClient(client, base, opts...).
			Count(ctx, connect.NewRequest(&demov1.CountRequest{UpTo: int32(count)}))
		if err != nil {
			return err
		}
		// The page stamps each value one interval after the previous one, as a page that reads a stream value by
		// value: on a loaded machine the real reads of a 10 ms stream bunch up, and the verdict on real arrivals is
		// TestReport's and TestAnalyze's, not this one's.
		var values []string
		first := float64(time.Now().UnixMicro()) / 1000
		for stream.Receive() {
			at := first + float64(len(values))*ms(streamInterval)
			values = append(values, `{"v":`+strconv.Itoa(int(stream.Msg().GetValue()))+`,"t":`+strconv.FormatFloat(at, 'f', 3, 64)+`}`)
		}
		if err := stream.Err(); err != nil {
			return err
		}
		bench := ""
		if u.Query().Get("bench") == "1" {
			for _, o := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
				echo := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](client, base+"/windowcheck.v1.Bench/Echo", o...)
				if _, err := echo.CallUnary(ctx, connect.NewRequest(wrapperspb.String("saved"))); err != nil {
					return err
				}
				burst := connect.NewClient[wrapperspb.UInt32Value, wrapperspb.StringValue](client, base+"/windowcheck.v1.Bench/Burst", o...)
				s, err := burst.CallServerStream(ctx, connect.NewRequest(wrapperspb.UInt32(3)))
				if err != nil {
					return err
				}
				n := 0
				for s.Receive() {
					n++
				}
				if err := s.Err(); err != nil || n != 3 {
					return errors.Join(err, errors.New("burst of "+strconv.Itoa(n)))
				}
			}
			bench = `,"bench":{"unary":[{"format":"binary","size":2048,"calls":4,"ms":2}],` +
				`"burst":[{"format":"json","messages":3,"ms":1.5,"reads":2}]}`
		}
		return post(`{"values":[` + strings.Join(values, ",") + `],"reads":` + strconv.Itoa(len(values)) + bench + `}`)
	}
}

// TestRun opens the window through show, on either path, and tells from what the page posts whether the stream
// arrived value by value; a window that fails, closes early or never answers is a failure, a wrong flag a usage error.
func TestRun(t *testing.T) {
	t.Parallel()
	stream := []string{"-count", "4", "-interval", streamInterval.String(), "-timeout", "10s"}
	cases := []struct {
		name string
		args []string
		mode string
		code int
		want []string
	}{
		{"the wails path streams", append([]string{"-path", "wails"}, stream...), "stream", 0,
			[]string{"path:    wails", "Received:   4 of 4 values", "PASS"}},
		{"the http path streams in json", append([]string{"-path", "http", "-format", "json"}, stream...), "stream", 0,
			[]string{"path:    http", "over Connect in json", "PASS"}},
		{"the bench times both formats", append([]string{"-path", "http", "-bench"}, stream...), "stream", 0,
			[]string{"PASS", "Benchmark, timed by the page", "Accept-Encoding", "2 KiB  binary", "3 messages  json"}},
		{"a garbled result fails", stream, "garbled", 1, []string{"unreadable result", "FAIL: the page met an error"}},
		{"a window that does not run fails", stream, "broken", 1, []string{"FAIL: the window did not run: no display"}},
		{"a window closed early fails", stream, "closed", 1, []string{"FAIL: the window was closed before the end"}},
		{"a page that never posts times out", []string{"-timeout", "20ms"}, "stuck", 1, []string{"FAIL: no result after 20ms"}},
		{"a single value is a usage error", []string{"-count", "1"}, "closed", 2, nil},
		{"an unknown path is a usage error", []string{"-path", "ftp"}, "closed", 2, nil},
		{"an unknown flag is a usage error", []string{"-nope"}, "closed", 2, nil},
		{"help is no error", []string{"-h"}, "closed", 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if code := run(c.args, &out, fakePage(c.mode)); code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out.String())
			}
			for _, w := range c.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("no %q in:\n%s", w, out.String())
				}
			}
		})
	}
}

// arrivals builds the result of count values, the i-th read at start + at(i) ms, each sent latency ms earlier.
func arrivals(count int, latency float64, at func(i int) float64) (result, map[int32]time.Time) {
	start := float64(time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC).UnixMicro()) / 1000
	var res result
	sent := map[int32]time.Time{}
	for i := range count {
		t := start + at(i)
		res.Values = append(res.Values, struct {
			V int32   `json:"v"`
			T float64 `json:"t"`
		}{V: int32(i + 1), T: t})
		sent[int32(i+1)] = time.UnixMicro(int64((t - latency) * 1000))
	}
	return res, sent
}

// buffered is a response read all at the end, each value sent interval after the previous one.
func buffered(count int, interval time.Duration) (result, map[int32]time.Time) {
	res, sent := arrivals(count, 0, func(i int) float64 { return 1980 + float64(i)*0.01 })
	for v := range sent {
		sent[v] = sent[v].Add(-time.Duration(count-int(v)) * interval)
	}
	return res, sent
}

// TestReport prints the arrivals and passes only the values that came one by one, saying why it fails otherwise.
func TestReport(t *testing.T) {
	t.Parallel()
	const count, interval = 10, 20 * time.Millisecond
	streamed, sent := arrivals(count, 0.8, func(i int) float64 { return float64(i) * 20 })
	burst, burstSent := buffered(count, interval)
	missing, missingSent := arrivals(count-1, 1, func(i int) float64 { return float64(i) * 20 })
	failed := streamed
	failed.Error = "boom"
	cases := []struct {
		name string
		res  result
		sent map[int32]time.Time
		code int
		want []string
	}{
		{"streamed", streamed, sent, 0, []string{"Received:   10 of 10", "Latency:    median 0.8 ms", "PASS"}},
		{"buffered", burst, burstSent, 1, []string{"Gaps in ms: ", "FAIL: the values arrived in a burst"}},
		{"a value missing", missing, missingSent, 1, []string{"FAIL: 9 of 10 values arrived."}},
		{"an error in the page", failed, sent, 1, []string{"Error in the page: boom", "FAIL: the page met an error"}},
		{"nothing", result{}, nil, 1, []string{"Received:   0 of 10", "FAIL: 0 of 10"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if code := report(&out, c.res, c.sent, count, interval); code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out.String())
			}
			for _, w := range c.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("no %q in:\n%s", w, out.String())
				}
			}
		})
	}
}

// TestReportBench prints the mean round trip of each run of unary calls and each burst, after what the webview
// asked for.
func TestReportBench(t *testing.T) {
	t.Parallel()
	var b benchResult
	b.Unary = append(b.Unary, struct {
		Format string  `json:"format"`
		Size   int     `json:"size"`
		Calls  int     `json:"calls"`
		MS     float64 `json:"ms"`
	}{"json", 4096, 10, 5})
	b.Burst = append(b.Burst, struct {
		Format   string  `json:"format"`
		Messages int     `json:"messages"`
		MS       float64 `json:"ms"`
		Reads    int     `json:"reads"`
	}{"binary", 1000, 20, 7})
	var seen sync.Map
	seen.Store("application/proto", "gzip")
	var out bytes.Buffer
	reportBench(&out, b, &seen)
	for _, w := range []string{
		`Accept-Encoding "gzip" with application/proto`, "4 KiB  json     0.500 ms  (10 calls)", "1000 messages  binary",
		"20.0 µs/message  7 reads",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("no %q in:\n%s", w, out.String())
		}
	}
}

// TestAnalyze passes a stream whose values arrived one by one, even two at a time now and then; never a buffered
// response, a missing value or an error in the page.
func TestAnalyze(t *testing.T) {
	t.Parallel()
	const count, interval = 100, 20 * time.Millisecond
	t.Run("streamed", func(t *testing.T) {
		t.Parallel()
		res, sent := arrivals(count, 0.8, func(i int) float64 { return float64(i) * 20 })
		a := analyze(res, sent, count, interval)
		if !a.pass || a.reads != count || a.medianGap != 20 || a.latency(0.5) < 0.79 || a.latency(0.5) > 0.81 {
			t.Errorf("pass %v, median gap %.1f, median latency %.2f", a.pass, a.medianGap, a.latency(0.5))
		}
	})
	// Two values read together now and then still stream.
	t.Run("paired", func(t *testing.T) {
		t.Parallel()
		res, sent := arrivals(count, 1, func(i int) float64 { return float64(i/2) * 40 })
		if a := analyze(res, sent, count, interval); !a.pass {
			t.Errorf("fail, %d reads, span %.0f", a.reads, a.span)
		}
	})
	// A buffered response: everything at the end, latency up to the whole stream.
	t.Run("buffered", func(t *testing.T) {
		t.Parallel()
		res, sent := buffered(count, interval)
		if a := analyze(res, sent, count, interval); a.pass || a.latency(1) < 1900 {
			t.Errorf("pass %v, max latency %.1f", a.pass, a.latency(1))
		}
	})
	t.Run("a value missing", func(t *testing.T) {
		t.Parallel()
		res, sent := arrivals(count-1, 1, func(i int) float64 { return float64(i) * 20 })
		if analyze(res, sent, count, interval).pass {
			t.Error("pass")
		}
	})
	t.Run("an error in the page", func(t *testing.T) {
		t.Parallel()
		res, sent := arrivals(count, 1, func(i int) float64 { return float64(i) * 20 })
		res.Error = "boom"
		if analyze(res, sent, count, interval).pass {
			t.Error("pass")
		}
	})
}

// TestQuantile is the nearest-rank quantile; 0 for no values.
func TestQuantile(t *testing.T) {
	t.Parallel()
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, c := range []struct {
		name string
		xs   []float64
		q    float64
		want float64
	}{
		{"median", xs, 0.5, 5},
		{"p90", xs, 0.9, 9},
		{"max", xs, 1, 10},
		{"min", xs, 0, 1},
		{"empty", nil, 0.5, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := quantile(c.xs, c.q); got != c.want {
				t.Errorf("quantile(%v) = %v, want %v", c.q, got, c.want)
			}
		})
	}
}
