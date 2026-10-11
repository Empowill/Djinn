package harness

import (
	"cmp"
	"context"
	"log"
	"math"
	"slices"
	"strings"
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
	// Initial CPU baseline: CPU time before the meter starts is not the task's.
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
	point := &machinev1.WorkerPoint{
		Time:        timestamppb.New(now),
		CpuPercent:  cpu,
		MemoryBytes: g.Memory,
	}
	h.mu.Lock()
	r.use = use
	if r.samples == nil {
		r.samples = newWorkerRing(maxWorkerSamples)
	}
	r.samples.Add(point)
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

// WorkerForecast returns the typical forecast peak memory per worker across providers in use,
// or p.WorkerForecast() if none is measured.
func (h *Harness) WorkerForecast() uint64 {
	p := h.Policy()
	if h.store == nil {
		return p.WorkerForecast()
	}
	ctx := context.Background()
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil || len(tasks) == 0 {
		return p.WorkerForecast()
	}
	return dispatch.TypicalForecast(tasks, p)
}

const maxWorkerSamples = 1440 // 120 minutes at 5s intervals

type workerRing struct {
	buf  []*machinev1.WorkerPoint
	head int
	full bool
}

func newWorkerRing(capacity int) *workerRing {
	return &workerRing{buf: make([]*machinev1.WorkerPoint, capacity)}
}

func (w *workerRing) Add(p *machinev1.WorkerPoint) {
	w.buf[w.head] = p
	w.head++
	if w.head >= len(w.buf) {
		w.head = 0
		w.full = true
	}
}

func (w *workerRing) Points(cutoff time.Time) []*machinev1.WorkerPoint {
	if len(w.buf) == 0 {
		return nil
	}
	var out []*machinev1.WorkerPoint
	if !w.full {
		for i := 0; i < w.head; i++ {
			if w.buf[i] != nil && (cutoff.IsZero() || !w.buf[i].GetTime().AsTime().Before(cutoff)) {
				out = append(out, w.buf[i])
			}
		}
		return out
	}
	for i := w.head; i < len(w.buf); i++ {
		if w.buf[i] != nil && (cutoff.IsZero() || !w.buf[i].GetTime().AsTime().Before(cutoff)) {
			out = append(out, w.buf[i])
		}
	}
	for i := 0; i < w.head; i++ {
		if w.buf[i] != nil && (cutoff.IsZero() || !w.buf[i].GetTime().AsTime().Before(cutoff)) {
			out = append(out, w.buf[i])
		}
	}
	return out
}

// RunsSeries returns the measurement series for all running workers since cutoff,
// associating each worker with gate intervals matching its task ID.
func (h *Harness) RunsSeries(cutoff time.Time, gateIntervals []*machinev1.GateInterval) []*machinev1.WorkerSeries {
	h.mu.Lock()
	defer h.mu.Unlock()

	intervalsByTask := make(map[string][]*machinev1.GateInterval)
	for _, iv := range gateIntervals {
		if iv.GetTaskId() != "" {
			intervalsByTask[iv.GetTaskId()] = append(intervalsByTask[iv.GetTaskId()], iv)
		}
	}

	var out []*machinev1.WorkerSeries
	for _, r := range h.runs {
		if r.paused || r.light {
			continue
		}
		t := r.task
		if t == nil {
			continue
		}
		var pts []*machinev1.WorkerPoint
		if r.samples != nil {
			pts = r.samples.Points(cutoff)
		}
		var peakMem uint64
		var peakCPU float64
		if t.GetResources() != nil {
			peakMem = t.GetResources().GetPeakMemoryBytes()
			peakCPU = t.GetResources().GetPeakCpuPercent()
		}
		for _, p := range pts {
			if p.GetMemoryBytes() > peakMem {
				peakMem = p.GetMemoryBytes()
			}
			if p.GetCpuPercent() > peakCPU {
				peakCPU = p.GetCpuPercent()
			}
		}

		ws := &machinev1.WorkerSeries{
			TaskId:          t.GetId(),
			WishId:          t.GetWishId(),
			Code:            t.GetCode(),
			Title:           t.GetTitle(),
			Provider:        t.GetProvider(),
			Model:           t.GetModel(),
			Points:          pts,
			Gates:           intervalsByTask[t.GetId()],
			PeakMemoryBytes: peakMem,
			PeakCpuPercent:  peakCPU,
		}
		out = append(out, ws)
	}
	slices.SortFunc(out, func(a, b *machinev1.WorkerSeries) int {
		return strings.Compare(a.GetCode(), b.GetCode())
	})
	return out
}

// AddWorkerSample appends a sample point for taskID (useful in tests).
func (h *Harness) AddWorkerSample(taskID string, p *machinev1.WorkerPoint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.runs[taskID]; ok {
		if r.samples == nil {
			r.samples = newWorkerRing(maxWorkerSamples)
		}
		r.samples.Add(p)
	}
}
