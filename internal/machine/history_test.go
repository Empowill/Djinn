package machine

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// historyRig is a History holding hours of machine readings until now, one of them a peak at 97 % when they span
// three hours, and a worker's reading; a second reading of the worker waits to be written.
type historyRig struct {
	h          *History
	db         *store.Store
	now, start time.Time
}

// fillHistory opens the store at path ("" in memory) and fills it with the readings of a historyRig from start to now.
func fillHistory(ctx context.Context, path string, span time.Duration) (*historyRig, error) {
	db, err := store.Open(ctx, path, plan.Entities()...)
	if err != nil {
		return nil, err
	}
	r := &historyRig{db: db, h: NewHistory(db)}
	r.now = time.Now().Truncate(time.Second)
	r.start = r.now.Add(-span)
	n := 0
	for at := r.start; !at.After(r.now); at = at.Add(5 * time.Second) {
		cpu := 20.0
		if n == 1000 {
			cpu = 97 // One reading's peak.
		}
		r.h.Machine(&machinev1.MachinePoint{Time: timestamppb.New(at), CpuPercent: cpu, MemoryBytes: 4 << 30})
		n++
	}
	r.h.Worker("w1", &machinev1.WorkerPoint{Time: timestamppb.New(r.now.Add(-10 * time.Second)), CpuPercent: 80})
	r.h.Flush(ctx)
	r.h.Worker("w1", &machinev1.WorkerPoint{Time: timestamppb.New(r.now.Add(-5 * time.Second)), CpuPercent: 90})
	return r, nil
}

// threeHours is a historyRig of three hours in memory, shared by the tests that only read it: a store is slow to open
// under -race (0.8 s), and its 2161 readings slow to write.
var threeHours = sync.OnceValues(func() (*historyRig, error) {
	return fillHistory(context.Background(), "", 3*time.Hour)
})

// sharedHistory is threeHours, the test failing when it could not be made.
func sharedHistory(t *testing.T) *historyRig {
	t.Helper()
	r, err := threeHours()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestHistory_WorkerPoints: a worker's series reads back what is written and what still waits to be, in order.
func TestHistory_WorkerPoints(t *testing.T) {
	t.Parallel()
	r := sharedHistory(t)
	cases := []struct {
		name string
		from time.Time
		want []float64 // the CPU of each point
	}{
		{"written and waiting", r.now.Add(-time.Minute), []float64{80, 90}},
		// Since a point: those after it alone.
		{"since a point", r.now.Add(-10 * time.Second), []float64{90}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pts := r.h.WorkerPoints(t.Context(), "w1", c.from, r.now)
			got := make([]float64, len(pts))
			for i, p := range pts {
				got[i] = p.GetCpuPercent()
			}
			if len(got) != len(c.want) || len(got) > 0 && got[len(got)-1] != c.want[len(c.want)-1] {
				t.Errorf("worker points = %v, want %v", got, c.want)
			}
		})
	}
}

// TestHistory_MachinePoints: the machine's series reads every reading over a short range; a range longer than
// MaxSeriesPoints readings is read by buckets that keep their peaks.
func TestHistory_MachinePoints(t *testing.T) {
	t.Parallel()
	r := sharedHistory(t)
	cases := []struct {
		name     string
		from     time.Time
		min, max int
		peak     float64
	}{
		// Ten minutes: every reading.
		{"ten minutes", r.now.Add(-10 * time.Minute), 120, 120, 20},
		// Three hours: 2161 readings thinned to buckets, the peak kept.
		{"three hours", r.start.Add(-time.Second), MaxSeriesPoints / 2, MaxSeriesPoints, 97},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pts := r.h.MachinePoints(t.Context(), c.from, r.now)
			peak := 0.0
			for _, p := range pts {
				peak = max(peak, p.GetCpuPercent())
			}
			if len(pts) < c.min || len(pts) > c.max || peak != c.peak {
				t.Errorf("%d points, peak %v; want %d to %d, peak %v", len(pts), peak, c.min, c.max, c.peak)
			}
		})
	}
}

// TestHistory_Flush: what a flush writes outlives a restart; readings the store could not write wait for the next
// flush, and are read meanwhile. Its cases run in order on one store file: the first stops Djinn, whose closed store
// the second writes to.
func TestHistory_Flush(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), store.File)
	r, err := fillHistory(t.Context(), path, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Close() })
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		// Djinn stops, flushing what waits, and starts again on the same store.
		{"kept across a restart", func(t *testing.T) {
			r.h.Flush(t.Context())
			r.db.Close()
			db, err := store.Open(t.Context(), path, plan.Entities()...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			again := NewHistory(db)
			if pts := again.WorkerPoints(t.Context(), "w1", r.now.Add(-time.Minute), r.now); len(pts) != 2 {
				t.Errorf("after a restart, worker points = %d, want 2", len(pts))
			}
			if pts := again.MachinePoints(t.Context(), r.now.Add(-10*time.Minute), r.now); len(pts) != 120 {
				t.Errorf("after a restart, 10 minutes = %d points, want 120", len(pts))
			}
		}},
		// The store closed, it fails every write.
		{"store failing", func(t *testing.T) {
			h := NewHistory(r.db)
			h.Worker("w2", &machinev1.WorkerPoint{Time: timestamppb.New(r.now.Add(-5 * time.Second)), CpuPercent: 90})
			h.Flush(t.Context())
			if pts := h.WorkerPoints(t.Context(), "w2", r.now.Add(-time.Minute), r.now); len(pts) != 1 || pts[0].GetCpuPercent() != 90 {
				t.Errorf("worker points after a failed flush = %v, want the reading still waiting", pts)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}
