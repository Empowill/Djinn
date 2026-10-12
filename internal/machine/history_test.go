package machine

import (
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestHistory: the series read back what is written and what still waits to be; a Djinn started again on the same
// store reads them still; a range longer than MaxSeriesPoints readings is read by buckets that keep their peaks.
func TestHistory(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), store.File)
	db, err := store.Open(ctx, path, plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHistory(db)
	now := time.Now().Truncate(time.Second)
	start := now.Add(-3 * time.Hour)
	n := 0
	for at := start; !at.After(now); at = at.Add(5 * time.Second) {
		cpu := 20.0
		if n == 1000 {
			cpu = 97 // One reading's peak.
		}
		h.Machine(&machinev1.MachinePoint{Time: timestamppb.New(at), CpuPercent: cpu, MemoryBytes: 4 << 30})
		n++
	}
	h.Worker("w1", &machinev1.WorkerPoint{Time: timestamppb.New(now.Add(-10 * time.Second)), CpuPercent: 80})
	h.Flush(ctx)
	h.Worker("w1", &machinev1.WorkerPoint{Time: timestamppb.New(now.Add(-5 * time.Second)), CpuPercent: 90})

	// Written and waiting, in order.
	if pts := h.WorkerPoints(ctx, "w1", now.Add(-time.Minute), now); len(pts) != 2 || pts[1].GetCpuPercent() != 90 {
		t.Errorf("worker points = %v, want the written one then the waiting one", pts)
	}
	// Since a point: those after it alone.
	if pts := h.WorkerPoints(ctx, "w1", now.Add(-10*time.Second), now); len(pts) != 1 {
		t.Errorf("worker points since the first = %d, want 1", len(pts))
	}
	// Ten minutes: every reading.
	if pts := h.MachinePoints(ctx, now.Add(-10*time.Minute), now); len(pts) != 120 {
		t.Errorf("10 minutes = %d points, want 120", len(pts))
	}
	// Three hours: 2161 readings thinned to buckets, the peak kept.
	pts := h.MachinePoints(ctx, start.Add(-time.Second), now)
	peak := 0.0
	for _, p := range pts {
		peak = max(peak, p.GetCpuPercent())
	}
	if len(pts) > MaxSeriesPoints || len(pts) < MaxSeriesPoints/2 || peak != 97 {
		t.Errorf("3 hours = %d points (want at most %d), peak %v (want 97)", len(pts), MaxSeriesPoints, peak)
	}

	// Djinn stops, flushing what waits, and starts again on the same store.
	h.Flush(ctx)
	db.Close()
	db, err = store.Open(ctx, path, plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	again := NewHistory(db)
	if pts := again.WorkerPoints(ctx, "w1", now.Add(-time.Minute), now); len(pts) != 2 {
		t.Errorf("after a restart, worker points = %d, want 2", len(pts))
	}
	if pts := again.MachinePoints(ctx, now.Add(-10*time.Minute), now); len(pts) != 120 {
		t.Errorf("after a restart, 10 minutes = %d points, want 120", len(pts))
	}
}
