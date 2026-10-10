package harness

import (
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, task.GetId(), 0)
	if got := e.get(t, task.GetId()).GetResources(); got.GetPeakMemoryBytes() != 400<<20 {
		t.Errorf("stopped: resources = %v, want its peak kept", got)
	}
	if uses := e.h.Uses(); len(uses) != 0 {
		t.Errorf("uses once stopped = %v", uses)
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
		ReadTime: timestamppb.New(at)}
	same := func(change func(r *planv1.Resources)) *planv1.Resources {
		r := &planv1.Resources{CpuPercent: 41, MemoryBytes: 1020, Processes: 3, PeakCpuPercent: 80, PeakMemoryBytes: 2000,
			ReadTime: timestamppb.New(at.Add(5 * time.Second))}
		if change != nil {
			change(r)
		}
		return r
	}
	for name, c := range map[string]struct {
		was, now *planv1.Resources
		want     bool
	}{
		"first":         {nil, same(nil), true},
		"barely moved":  {was, same(nil), false},
		"a process":     {was, same(func(r *planv1.Resources) { r.Processes = 4 }), true},
		"CPU":           {was, same(func(r *planv1.Resources) { r.CpuPercent = 46 }), true},
		"memory":        {was, same(func(r *planv1.Resources) { r.MemoryBytes = 1060 }), true},
		"peak memory":   {was, same(func(r *planv1.Resources) { r.PeakMemoryBytes = 2200 }), true},
		"a minute gone": {was, same(func(r *planv1.Resources) { r.ReadTime = timestamppb.New(at.Add(time.Minute)) }), true},
	} {
		if got := worth(c.was, c.now); got != c.want {
			t.Errorf("%s: worth = %v, want %v", name, got, c.want)
		}
	}
}
