package machine

import (
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestSeriesRecorder_Points: the points recorded since a cutoff, in order, the oldest dropped once the ring is full.
func TestSeriesRecorder_Points(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cases := []struct {
		name   string
		added  int
		cutoff time.Time
		want   []float64 // the CPU of the first and the last point
		n      int
	}{
		{name: "empty"},
		{name: "all", added: 5, want: []float64{0, 40}, n: 5},
		{name: "since a cutoff", added: 5, cutoff: now.Add(2 * time.Minute), want: []float64{20, 40}, n: 3},
		// Two points past the ring: the two oldest dropped.
		{name: "full", added: maxMachineSamples + 2, want: []float64{20, float64(maxMachineSamples+1) * 10}, n: maxMachineSamples},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := NewSeriesRecorder(nil, nil)
			for i := range c.added {
				rec.AddPoint(&machinev1.MachinePoint{
					Time:            timestamppb.New(now.Add(time.Duration(i) * time.Minute)),
					CpuPercent:      float64(i * 10),
					MemoryBytes:     uint64(i * 100),
					DjinnCpuPercent: float64(i),
				})
			}
			pts := rec.Points(c.cutoff)
			if len(pts) != c.n {
				t.Fatalf("%d points, want %d", len(pts), c.n)
			}
			if c.n > 0 && (pts[0].GetCpuPercent() != c.want[0] || pts[c.n-1].GetCpuPercent() != c.want[1]) {
				t.Errorf("first %v, last %v; want %v", pts[0], pts[c.n-1], c.want)
			}
		})
	}
}

// TestSeriesRecorder_Sample: each sample reads the machine's CPU and memory, Djinn's line (its work, or its tree) and
// Djinn's own process. The cases sleep between samples, 200 ms at most: the CPU shares are read over real time.
func TestSeriesRecorder_Sample(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		// The machine's CPU from /proc/stat, between two samples; Djinn's memory from its tree.
		{"proc stat", func(t *testing.T) {
			fsys := fstest.MapFS{
				"proc/stat": &fstest.MapFile{
					Data: []byte("cpu  100 20 50 830 0 0 0 0 0 0\n"),
				},
			}
			rec := NewSeriesRecorder(nil, func(pid int, cgroup string) (Group, error) {
				return Group{CPU: 100 * time.Millisecond, Memory: 10 << 20}, nil
			})
			rec.SetFS(fsys)

			// First sample sets baseline
			rec.Sample()

			// Update proc stat to simulate 100 ticks elapsed, 50 busy (50% CPU)
			fsys["proc/stat"] = &fstest.MapFile{
				Data: []byte("cpu  140 20 60 880 0 0 0 0 0 0\n"), // total 1100 (+100), idle 880 (+50), busy 50
			}

			rec.Sample()

			pts := rec.Points(time.Time{})
			if len(pts) != 2 {
				t.Fatalf("expected 2 points, got %d", len(pts))
			}
			last := pts[1]
			if last.GetCpuPercent() != 50 {
				t.Errorf("expected 50%% CPU, got %v", last.GetCpuPercent())
			}
			if last.GetDjinnMemoryBytes() != 10<<20 {
				t.Errorf("expected 10 MiB djinn memory, got %v", last.GetDjinnMemoryBytes())
			}
		}},
		// Djinn's own process is read apart from what it started, and Djinn's CPU, read in percent of
		// one core, is drawn as a share of the machine: 400% of one core on 8 cores is 50%.
		{"self", func(t *testing.T) {
			m := NewMonitor(Policy{}, func() (Snapshot, error) { return Snapshot{Cores: 8, MemoryTotal: 16 << 30}, nil })
			var group, self time.Duration
			rec := NewSeriesRecorder(m, func(int, string) (Group, error) {
				return Group{CPU: group, Memory: 3 << 30}, nil
			})
			rec.SetFS(nil)
			rec.ReadSelf(func() (Group, error) { return Group{CPU: self, Memory: 140 << 20}, nil })
			rec.Sample()
			time.Sleep(50 * time.Millisecond)
			// In 50 ms, the group spent 200 ms of CPU (400% of a core), Djinn itself 25 ms (50% of a core).
			group, self = 200*time.Millisecond, 25*time.Millisecond
			rec.Sample()
			last := rec.Points(time.Time{})[1]
			// At least 50 ms passed: at most 50% of the machine, never the 400% of one core; Djinn itself an eighth of
			// it.
			djinn, own := last.GetDjinnCpuPercent(), last.GetSelfCpuPercent()
			if djinn <= 0 || djinn > 50.1 {
				t.Errorf("Djinn and what it started = %v%% of the machine, want at most 50%%", djinn)
			}
			if ratio := own / djinn; ratio < 0.1 || ratio > 0.15 {
				t.Errorf("Djinn itself = %v%%, %v of the whole, want an eighth", own, ratio)
			}
			if last.GetSelfMemoryBytes() != 140<<20 || last.GetDjinnMemoryBytes() != 3<<30 {
				t.Errorf("memory: itself %d, with what it started %d", last.GetSelfMemoryBytes(), last.GetDjinnMemoryBytes())
			}
		}},
		// The Djinn line counts what its work's counters grew by: a scope or a process that ends never takes back what
		// it used, so the line never falls to 0 % when a test run ends (W347). A djinn-*.scope made since the last
		// reading counts all it used; a process first read counts from its next reading.
		{"work", func(t *testing.T) {
			fsys := fstest.MapFS{"proc/stat": &fstest.MapFile{Data: []byte("cpu  100 0 0 900 0 0 0 0 0 0\n")}}
			monitor := NewMonitor(DefaultPolicy(), func() (Snapshot, error) { return Snapshot{Cores: 1, MemoryTotal: 100}, nil })
			rec := NewSeriesRecorder(monitor, func(int, string) (Group, error) { t.Error("the tree read beside the work"); return Group{}, nil })
			rec.SetFS(fsys)
			const worker, check = "/a/djinn-W1-1.scope", "/a/djinn-gate-test-2.scope"
			w := Work{
				Cgroups: map[string]time.Duration{"/a/app-djinn.scope": time.Second, worker: time.Hour},
				Procs:   map[int]time.Duration{7: 10 * time.Second},
				Memory:  15,
			}
			rec.ReadWork(func() (Work, error) { return w, nil })
			var before time.Time // just before the last sample: the recorder reads its time after it
			sample := func(next Work) *machinev1.MachinePoint {
				t.Helper()
				time.Sleep(100 * time.Millisecond)
				w = next
				before = time.Now()
				rec.Sample()
				pts := rec.Points(time.Time{})
				return pts[len(pts)-1]
			}
			rec.Sample()
			start := time.Now()

			// The worker's scope ends (its test run with it), a check's scope starts: 20 ms of Djinn's own, 30 ms of
			// the check's, 10 ms of process 7; process 8, adopted meanwhile, an hour before.
			p := sample(Work{
				Cgroups: map[string]time.Duration{"/a/app-djinn.scope": time.Second + 20*time.Millisecond, check: 30 * time.Millisecond},
				Procs:   map[int]time.Duration{7: 10*time.Second + 10*time.Millisecond, 8: time.Hour},
				Memory:  20,
			})
			// The recorder's interval began before start and ended after before: at least before - start.
			if got, most := p.GetDjinnCpuPercent(), float64(60*time.Millisecond)/float64(before.Sub(start))*100; got <= 0 || got > most+0.1 {
				t.Errorf("Djinn CPU %v %%, want 60 ms of its work, at most %.1f %%: never the ended scope's hour", got, most)
			}
			if p.GetDjinnMemoryBytes() != 20 {
				t.Errorf("Djinn memory %d, want 20", p.GetDjinnMemoryBytes())
			}

			// Process 8 counts from its second reading; process 7 ended.
			p = sample(Work{
				Cgroups: map[string]time.Duration{"/a/app-djinn.scope": time.Second + 20*time.Millisecond, check: 30 * time.Millisecond},
				Procs:   map[int]time.Duration{8: time.Hour + 40*time.Millisecond},
			})
			if got := p.GetDjinnCpuPercent(); got <= 0 || got > 40.1 {
				t.Errorf("Djinn CPU %v %%, want process 8's 40 ms over 100 ms or more", got)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// TestSeriesRecorder_Keep: kept, each sample after the first goes to the store's history too.
func TestSeriesRecorder_Keep(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := NewHistory(db)
	fsys := fstest.MapFS{"proc/stat": &fstest.MapFile{Data: []byte("cpu  100 20 50 830 0 0 0 0 0 0\n")}}
	rec := NewSeriesRecorder(nil, nil)
	rec.SetFS(fsys)
	rec.Keep(h)
	start := time.Now().Add(-time.Second)
	rec.Sample()
	fsys["proc/stat"] = &fstest.MapFile{Data: []byte("cpu  140 20 60 880 0 0 0 0 0 0\n")}
	rec.Sample()
	if pts := h.MachinePoints(t.Context(), start, time.Now().Add(time.Second)); len(pts) != 1 || pts[0].GetCpuPercent() != 50 {
		t.Errorf("history points %v, want the second sample's", pts)
	}
}
