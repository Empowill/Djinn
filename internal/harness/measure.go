package harness

import (
	"cmp"
	"context"
	"log"
	"math"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
)

// MeasureFunc reads what the worker whose process leads group pid uses, from its cgroup when it runs in a scope:
// machine.ReadWorker.
type MeasureFunc func(pid int, cgroup string) (machine.Group, error)

// WithMeasure reads what each worker uses every interval, with read, and keeps on its task the latest reading and
// the peaks. djinn up gives machine.ReadWorker, where the system lets it measure workers (machine.NotMeasured).
func WithMeasure(every time.Duration, read MeasureFunc) Option {
	return func(h *Harness) { h.measureEvery, h.measureRead = every, read }
}

// rewrite is how long a task keeps a reading on the store while its worker's use barely moves: past it, the next
// reading is written anyway.
const rewrite = time.Minute

// meter follows the readings of one worker.
type meter struct {
	proc    Process
	pid     int
	cpu     time.Duration // CPU time at the reading before
	at      time.Time     // when it was read
	written *planv1.Resources
}

// newMeter follows the run's current worker; nil when it is not measured: no measure given, or a worker without a
// process of its own.
func (h *Harness) newMeter(r *run) *meter {
	p, ok := r.worker.(Process)
	if !ok || h.measureRead == nil || h.measureEvery <= 0 {
		return nil
	}
	m := &meter{proc: p, pid: p.PID(), at: time.Now(), written: r.task.GetResources()}
	// A warm worker ran before its task took it: its CPU time so far is not the task's.
	if m.pid != 0 {
		if g, err := h.measureRead(m.pid, p.Cgroup()); err == nil {
			m.cpu = g.CPU
		}
	}
	return m
}

// measure reads what the run's worker uses, keeps it on the task, and writes the task when the reading moved
// enough to show (worth). Runs in the pump, which owns the task.
func (h *Harness) measure(r *run, m *meter) {
	pid := m.proc.PID()
	if pid == 0 {
		return // A watcher between two runs of its command.
	}
	g, err := h.measureRead(pid, m.proc.Cgroup())
	now := time.Now()
	if err != nil || g.Processes == 0 {
		return
	}
	if pid != m.pid {
		m.pid, m.cpu = pid, 0 // A watcher's next command.
	}
	var cpu float64
	if d := now.Sub(m.at); d > 0 && g.CPU >= m.cpu {
		cpu = math.Round(float64(g.CPU-m.cpu)/float64(d)*1000) / 10
	}
	m.cpu, m.at = g.CPU, now
	t := r.task
	res := &planv1.Resources{
		CpuPercent: cpu, MemoryBytes: g.Memory, Processes: int32(g.Processes),
		PeakCpuPercent:  max(t.GetResources().GetPeakCpuPercent(), cpu),
		PeakMemoryBytes: max(t.GetResources().GetPeakMemoryBytes(), g.Memory, g.Peak),
		ReadTime:        timestamppb.New(now),
	}
	t.Resources = res // Replaced at each reading, never changed: Uses shares it.
	use := &machinev1.WorkerUse{
		TaskId: t.GetId(), WishId: t.GetWishId(), Code: t.GetCode(), Title: t.GetTitle(), Resources: res,
	}
	h.mu.Lock()
	r.use = use
	h.mu.Unlock()
	if !worth(m.written, res) {
		return
	}
	err = h.store.Tx(context.Background(), func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodMeasure, res); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		log.Printf("djinn: task %s: record what its worker uses: %v", r.id, err)
		return
	}
	m.written = res
}

// worth says whether a reading moved enough from the one written to be written: a process more or less, 5 points
// of CPU, 5% of memory, a new peak as large, or a minute gone.
func worth(was, now *planv1.Resources) bool {
	if was == nil || was.GetReadTime() == nil {
		return true
	}
	moved := func(a, b float64) bool { return math.Abs(a-b) >= 5 }
	ratio := func(a, b uint64) bool { return math.Abs(float64(a)-float64(b)) >= 0.05*float64(max(a, 1)) }
	return was.GetProcesses() != now.GetProcesses() ||
		moved(was.GetCpuPercent(), now.GetCpuPercent()) || moved(was.GetPeakCpuPercent(), now.GetPeakCpuPercent()) ||
		ratio(was.GetMemoryBytes(), now.GetMemoryBytes()) || ratio(was.GetPeakMemoryBytes(), now.GetPeakMemoryBytes()) ||
		now.GetReadTime().AsTime().Sub(was.GetReadTime().AsTime()) >= rewrite
}

// fresh clears what the task's last worker was using, keeping its peaks, as a new worker starts.
func fresh(t *planv1.Task) {
	if res := t.GetResources(); res != nil {
		t.Resources = &planv1.Resources{PeakCpuPercent: res.GetPeakCpuPercent(), PeakMemoryBytes: res.GetPeakMemoryBytes()}
	}
}

// Uses lists the workers running now that were read, with what each uses, the busiest first: the most CPU, then
// the most memory.
func (h *Harness) Uses() []*machinev1.WorkerUse {
	h.mu.Lock()
	var out []*machinev1.WorkerUse
	for _, r := range h.runs {
		if r.use != nil {
			out = append(out, r.use)
		}
	}
	h.mu.Unlock()
	slices.SortFunc(out, func(a, b *machinev1.WorkerUse) int {
		return cmp.Or(
			cmp.Compare(b.GetResources().GetCpuPercent(), a.GetResources().GetCpuPercent()),
			cmp.Compare(b.GetResources().GetMemoryBytes(), a.GetResources().GetMemoryBytes()),
			cmp.Compare(a.GetCode(), b.GetCode()),
		)
	})
	return out
}

// WorkerMemory calculates engaged memory (sum of peak forecasts) and actual worker resident memory
// across running workers.
func (h *Harness) WorkerMemory(ctx context.Context, p machine.Policy) (engaged, actual uint64, err error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return 0, 0, err
	}
	// Overlay live resource readings from in-flight runs.
	h.mu.Lock()
	for _, t := range tasks {
		if r, ok := h.runs[t.GetId()]; ok {
			if r.use != nil && r.use.GetResources() != nil {
				t.Resources = r.use.GetResources()
			} else if r.task != nil && r.task.GetResources() != nil {
				t.Resources = r.task.GetResources()
			}
		}
	}
	h.mu.Unlock()
	engaged, actual = dispatch.WorkerMemory(tasks, p)
	return engaged, actual, nil
}
