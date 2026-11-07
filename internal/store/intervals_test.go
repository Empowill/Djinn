package store

import (
	"testing"
	"time"
)

// TestIntervals: gate intervals are read back after from, the first released first, and pruned by their release.
func TestIntervals(t *testing.T) {
	t.Parallel()
	s := open(t)
	ctx := t.Context()
	now := time.UnixMilli(time.Now().UnixMilli())
	for i, ago := range []time.Duration{8 * 24 * time.Hour, 2 * time.Hour, time.Minute} {
		if err := s.AddInterval(ctx, Interval{Released: now.Add(-ago), Data: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Intervals(ctx, now.Add(-3*time.Hour))
	if err != nil || len(got) != 2 || got[0].Data[0] != 1 || got[1].Data[0] != 2 || !got[1].Released.Equal(now.Add(-time.Minute)) {
		t.Fatalf("intervals of the last 3 hours = %v, %v", got, err)
	}
	if err := s.PruneIntervals(ctx, now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Intervals(ctx, time.Time{}); err != nil || len(got) != 2 {
		t.Errorf("after the prune = %v, %v", got, err)
	}
}
