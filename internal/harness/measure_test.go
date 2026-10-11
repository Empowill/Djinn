package harness

import (
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

// measuredClaude runs a fake claude that waits until it is stopped, measured every 20 ms with read.
func measuredClaude(t *testing.T, read MeasureFunc) (*env, *planv1.Task) {
	t.Helper()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}
	e := upWith(t, t.TempDir(), providers, WithMeasure(20*time.Millisecond, read))
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Measure a worker", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return e, res.Msg.GetTask()
}

// TestMeasurePeaks: the task keeps the latest reading of its worker and the peaks; the machine lists the worker
// while it runs; once it is stopped, the task keeps its peaks.
func TestMeasurePeaks(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	var mu sync.Mutex
	readings := []machine.Group{
		{Processes: 1, CPU: 0, Memory: 100 << 20},
		{Processes: 3, CPU: time.Second, Memory: 300 << 20, Peak: 400 << 20}, // a cgroup's peak, between two readings
		{Processes: 2, CPU: time.Second, Memory: 200 << 20},
	}
	read := func(pid int, _ string) (machine.Group, error) {
		mu.Lock()
		defer mu.Unlock()
		g := readings[0]
		if len(readings) > 1 {
			readings = readings[1:]
		}
		return g, nil
	}
	e, task := measuredClaude(t, read)
	var got *planv1.Resources
	waitFor(t, "the last reading", func() bool {
		got = e.get(t, task.GetId()).GetResources()
		return got.GetProcesses() == 2
	})
	if got.GetMemoryBytes() != 200<<20 || got.GetPeakMemoryBytes() != 400<<20 || got.GetCpuPercent() != 0 ||
		got.GetPeakCpuPercent() <= 0 || got.GetReadTime() == nil {
		t.Errorf("resources = %v, want 200 MiB now, 400 MiB at most, no CPU now and some before", got)
	}
	uses := e.h.Uses()
	if len(uses) != 1 || uses[0].GetCode() != task.GetCode() || uses[0].GetResources().GetMemoryBytes() != 200<<20 {
		t.Errorf("uses = %v, want %s at 200 MiB", uses, task.GetCode())
	}
	pol := machine.Policy{WorkerPeak: 1 * machine.GiB, WorkerMargin: 512 * machine.MiB}
	engaged, actual, err := e.h.WorkerMemory(t.Context(), pol)
	if err != nil {
		t.Fatalf("WorkerMemory: %v", err)
	}
	if wantEngaged := uint64(1*machine.GiB + 512*machine.MiB); engaged != wantEngaged {
		t.Errorf("engaged = %d, want %d", engaged, wantEngaged)
	}
	if wantActual := uint64(200 << 20); actual != wantActual {
		t.Errorf("actual = %d, want %d", actual, wantActual)
	}
	if got := e.h.Resident(); got != 200<<20 {
		t.Errorf("Resident = %d, want 200 MiB", got)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, task.GetId(), 0)
	if got := e.get(t, task.GetId()).GetResources(); got.GetPeakMemoryBytes() != 400<<20 {
		t.Errorf("stopped: resources = %v, want its peak kept", got)
	}
	// Most readings were 200 MiB: the series' median, which Overclock engages a worker at.
	if got := e.get(t, task.GetId()).GetResources(); got.GetP50MemoryBytes() != 200<<20 ||
		got.GetP80MemoryBytes() < got.GetP50MemoryBytes() || got.GetP95MemoryBytes() < got.GetP80MemoryBytes() {
		t.Errorf("stopped: resources = %v, want its percentiles from p50 at 200 MiB", got)
	}
	if got := e.h.Resident(); got != 0 {
		t.Errorf("Resident once stopped = %d", got)
	}
	if uses := e.h.Uses(); len(uses) != 0 {
		t.Errorf("uses once stopped = %v", uses)
	}
	engagedAfter, actualAfter, err := e.h.WorkerMemory(t.Context(), pol)
	if err != nil {
		t.Fatalf("WorkerMemory after stop: %v", err)
	}
	if engagedAfter != 0 || actualAfter != 0 {
		t.Errorf("after stop: engaged = %d, actual = %d, want 0", engagedAfter, actualAfter)
	}
}

// TestNotMeasured: the fake agent runs in Djinn's process, and a harness without a measure reads no worker.
func TestNotMeasured(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	read := func(int, string) (machine.Group, error) {
		t.Error("an in-process worker read")
		return machine.Group{}, nil
	}
	e := up(t, t.TempDir(), WithMeasure(time.Millisecond, read))
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "sleep 50ms\ntext done")
	e.watch(t.Context(), t, task.GetId(), 0)
	if got := e.get(t, task.GetId()); got.GetResources() != nil {
		t.Errorf("resources of the fake agent: %v", got.GetResources())
	}
}

// TestWorth: a reading is written when it moved enough to show, or a minute after the last one written.
func TestWorth(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	was := &planv1.Resources{CpuPercent: 40, MemoryBytes: 1000, Processes: 3, PeakCpuPercent: 80, PeakMemoryBytes: 2000,
		P95MemoryBytes: 1900, ReadTime: timestamppb.New(at)}
	same := func(change func(r *planv1.Resources)) *planv1.Resources {
		r := &planv1.Resources{CpuPercent: 41, MemoryBytes: 1020, Processes: 3, PeakCpuPercent: 80, PeakMemoryBytes: 2000,
			P95MemoryBytes: 1900, ReadTime: timestamppb.New(at.Add(5 * time.Second))}
		if change != nil {
			change(r)
		}
		return r
	}
	for name, c := range map[string]struct {
		was, now *planv1.Resources
		want     bool
	}{
		"first":                   {nil, same(nil), true},
		"barely moved":            {was, same(nil), false},
		"a process":               {was, same(func(r *planv1.Resources) { r.Processes = 4 }), true},
		"CPU at 5s":               {was, same(func(r *planv1.Resources) { r.CpuPercent = 46 }), false},
		"large CPU at 5s":         {was, same(func(r *planv1.Resources) { r.CpuPercent = 90 }), false},
		"memory":                  {was, same(func(r *planv1.Resources) { r.MemoryBytes = 1060 }), true},
		"peak memory":             {was, same(func(r *planv1.Resources) { r.PeakMemoryBytes = 2200 }), true},
		"p95 memory":              {was, same(func(r *planv1.Resources) { r.P95MemoryBytes = 2100 }), true},
		"p50 memory":              {was, same(func(r *planv1.Resources) { r.P50MemoryBytes = 2100 }), true},
		"CPU barely moved at 30s": {was, same(func(r *planv1.Resources) { r.ReadTime = timestamppb.New(at.Add(30 * time.Second)); r.CpuPercent = 50 }), false},
		"CPU moved at 30s":        {was, same(func(r *planv1.Resources) { r.ReadTime = timestamppb.New(at.Add(30 * time.Second)); r.CpuPercent = 65 }), true},
		"peak CPU moved at 30s": {was, same(func(r *planv1.Resources) {
			r.ReadTime = timestamppb.New(at.Add(30 * time.Second))
			r.PeakCpuPercent = 110
		}), true},
		"a minute gone": {was, same(func(r *planv1.Resources) { r.ReadTime = timestamppb.New(at.Add(time.Minute)) }), true},
	} {
		if got := worth(c.was, c.now); got != c.want {
			t.Errorf("%s: worth = %v, want %v", name, got, c.want)
		}
	}
}

func TestP95(t *testing.T) {
	t.Parallel()
	if got := P95(nil); got != 0 {
		t.Errorf("P95(nil): got %d, want 0", got)
	}
	if got := P95([]uint64{100}); got != 100 {
		t.Errorf("P95([100]): got %d, want 100", got)
	}

	// 20 items: 100, 200, ..., 2000
	var vals []uint64
	for i := 1; i <= 20; i++ {
		vals = append(vals, uint64(i*100))
	}
	// idx = 0.95 * 19 = 18.05; low = 18 (1900), high = 19 (2000); 1900*0.95 + 2000*0.05 = 1905
	if got := P95(vals); got != 1905 {
		t.Errorf("P95(20 items): got %d, want 1905", got)
	}
}

func TestWorkerRingP95(t *testing.T) {
	t.Parallel()
	ring := newWorkerRing(5)
	if got := ring.P95Memory(); got != 0 {
		t.Errorf("empty ring P95: got %d, want 0", got)
	}
	for i := 1; i <= 5; i++ {
		ring.Add(&machinev1.WorkerPoint{MemoryBytes: uint64(i * 100)})
	}
	// 5 items: 100, 200, 300, 400, 500
	// idx = 0.95 * 4 = 3.8; low = 3 (400), high = 4 (500); 400*0.2 + 500*0.8 = 480
	if got := ring.P95Memory(); got != 480 {
		t.Errorf("full ring P95: got %d, want 480", got)
	}
	// Add one more to wrap around: replaces 100 with 600.
	ring.Add(&machinev1.WorkerPoint{MemoryBytes: 600})
	// Now elements: 200, 300, 400, 500, 600
	// idx = 3.8; low = 3 (500), high = 4 (600); 500*0.2 + 600*0.8 = 580
	if got := ring.P95Memory(); got != 580 {
		t.Errorf("wrapped ring P95: got %d, want 580", got)
	}
}

// TestWorkerRingPercentiles: the percentiles the notches engage a worker at, from one series.
func TestWorkerRingPercentiles(t *testing.T) {
	t.Parallel()
	ring := newWorkerRing(20)
	if got := ring.MemoryPercentiles(); got != (memoryPercentiles{}) {
		t.Errorf("empty ring: %+v", got)
	}
	// 11 readings, 100 to 1100, and one read as 0, left out.
	ring.Add(&machinev1.WorkerPoint{})
	for i := 11; i >= 1; i-- {
		ring.Add(&machinev1.WorkerPoint{MemoryBytes: uint64(i * 100)})
	}
	want := memoryPercentiles{p50: 600, p60: 700, p70: 800, p80: 900, p95: 1050}
	if got := ring.MemoryPercentiles(); got != want {
		t.Errorf("percentiles = %+v, want %+v", got, want)
	}
	res := &planv1.Resources{}
	want.set(res)
	if res.GetP50MemoryBytes() != 600 || res.GetP60MemoryBytes() != 700 || res.GetP70MemoryBytes() != 800 ||
		res.GetP80MemoryBytes() != 900 || res.GetP95MemoryBytes() != 1050 {
		t.Errorf("set: %v", res)
	}
}
