package main

import (
	"testing"
	"time"
)

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

func TestAnalyze(t *testing.T) {
	const count, interval = 100, 20 * time.Millisecond

	streamed, sent := arrivals(count, 0.8, func(i int) float64 { return float64(i) * 20 })
	a := analyze(streamed, sent, count, interval)
	if !a.pass || a.reads != count || a.medianGap != 20 || a.latency(0.5) < 0.79 || a.latency(0.5) > 0.81 {
		t.Errorf("streamed: pass %v, median gap %.1f, median latency %.2f", a.pass, a.medianGap, a.latency(0.5))
	}

	// Two values read together now and then still stream.
	paired, sent := arrivals(count, 1, func(i int) float64 { return float64(i/2) * 40 })
	if a := analyze(paired, sent, count, interval); !a.pass {
		t.Errorf("paired: fail, %d reads, span %.0f", a.reads, a.span)
	}

	// A buffered response: everything at the end, latency up to the whole stream.
	burst, sent := arrivals(count, 0, func(i int) float64 { return 1980 + float64(i)*0.01 })
	for v := range sent {
		sent[v] = sent[v].Add(-time.Duration(count-int(v)) * interval)
	}
	a = analyze(burst, sent, count, interval)
	if a.pass || a.latency(1) < 1900 {
		t.Errorf("burst: pass %v, max latency %.1f", a.pass, a.latency(1))
	}

	missing, sent := arrivals(count-1, 1, func(i int) float64 { return float64(i) * 20 })
	if analyze(missing, sent, count, interval).pass {
		t.Error("missing value: pass")
	}
	streamed.Error = "boom"
	if analyze(streamed, sent, count, interval).pass {
		t.Error("page error: pass")
	}
}

func TestQuantile(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for q, want := range map[float64]float64{0.5: 5, 0.9: 9, 1: 10, 0: 1} {
		if got := quantile(xs, q); got != want {
			t.Errorf("quantile(%v) = %v, want %v", q, got, want)
		}
	}
	if quantile(nil, 0.5) != 0 {
		t.Error("empty: not 0")
	}
}
