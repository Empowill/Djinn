package store

import (
	"testing"
	"time"
)

// TestSamples: samples are read back by series, after a time; past Fine they are rolled up by the minute, keeping
// the highest values; past Keep they go, and beyond Max the oldest go first.
func TestSamples(t *testing.T) {
	ctx := t.Context()
	s := open(t)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var in []Sample
	// Three hours of readings every 5 s of the machine, and a worker's last minute.
	for at := now.Add(-3 * time.Hour); !at.After(now); at = at.Add(5 * time.Second) {
		cpu := 10.0
		if at.Equal(now.Add(-90*time.Minute + 10*time.Second)) {
			cpu = 95 // A peak, an hour and a half ago.
		}
		in = append(in, Sample{Series: "machine", At: at, Seconds: 5, CPU: cpu, Memory: 1 << 30, DjinnMemory: 1 << 20})
	}
	for at := now.Add(-time.Minute); !at.After(now); at = at.Add(5 * time.Second) {
		in = append(in, Sample{Series: "task:w1", At: at, Seconds: 5, CPU: 50, Memory: 300 << 20})
	}
	if err := s.AddSamples(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Samples(ctx, "task:w1", now.Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0].At.UnixMilli() != now.Add(-25*time.Second).UnixMilli() || got[0].Memory != 300<<20 {
		t.Errorf("worker samples after 30 s ago = %d from %v, want 6 from 25 s ago", len(got), got)
	}

	if err := s.PruneSamples(ctx, now, Retention{Fine: time.Hour, Keep: 2 * time.Hour, Max: 100000}); err != nil {
		t.Fatal(err)
	}
	all, err := s.Samples(ctx, "machine", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Two hours kept: one of minutes rolled up, then an hour of readings (12 a minute, and the one at now).
	minutes, fine, peak := 0, 0, 0.0
	for _, x := range all {
		if x.At.Before(now.Add(-2 * time.Hour)) {
			t.Fatalf("a sample older than Keep: %v", x)
		}
		if x.Seconds == 60 {
			minutes++
			peak = max(peak, x.CPU)
		} else {
			fine++
		}
	}
	if minutes != 60 || fine != 12*60+1 || peak != 95 {
		t.Errorf("after pruning: %d minutes (want 60), %d readings (want 721), peak %v (want 95)", minutes, fine, peak)
	}

	// The cap: the oldest go first.
	if err := s.PruneSamples(ctx, now, Retention{Fine: time.Hour, Keep: 2 * time.Hour, Max: 100}); err != nil {
		t.Fatal(err)
	}
	all, _ = s.Samples(ctx, "machine", time.Time{})
	worker, _ := s.Samples(ctx, "task:w1", time.Time{})
	if len(all)+len(worker) != 100 || len(worker) != 13 || !all[len(all)-1].At.Equal(now) {
		t.Errorf("capped to 100: %d machine samples, %d of the worker", len(all), len(worker))
	}
}
