package harness

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
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

// WithHistory keeps each reading of a worker in the store's series too, which outlive a restart of Djinn.
func WithHistory(history *machine.History) Option {
	return func(h *Harness) { h.history = history }
}

// rewrite is how long a task keeps a reading on the store while its worker's use barely moves: past it, the next
// reading is written anyway.
const rewrite = time.Minute

// minCPUInterval is the least time between two writes caused solely by CPU movements.
const minCPUInterval = 30 * time.Second

// minCPUMoved is the minimum CPU percentage point change required after minCPUInterval to justify a write.
const minCPUMoved = 20.0

// meter follows the readings of one worker.
type meter struct {
	proc    Process
	pid     int
	cpu     time.Duration // CPU time at the reading before
	at      time.Time     // when it was read
	written *planv1.Resources
	turns   *planv1.Turns // the task's turns, as last written
}

// newMeter follows the run's current worker; nil when it is not measured: no measure given, or a worker without a
// process of its own.
func (h *Harness) newMeter(r *run) *meter {
	p, ok := r.worker.(Process)
	if !ok || h.measureRead == nil || h.measureEvery <= 0 {
		return nil
	}
	m := &meter{proc: p, pid: p.PID(), at: time.Now(), written: r.task.GetResources(), turns: r.task.GetTurns()}
	// Initial CPU baseline: CPU time before the meter starts is not the task's.
	if m.pid != 0 {
		if g, err := h.measureRead(m.pid, p.Cgroup()); err == nil {
			m.cpu = g.CPU
		}
	}
	return m
}

// measure reads what the run's worker uses, keeps it on the task, and writes the task when the reading moved
// enough to show (worth), or its turns grew (setTurns). Runs in the pump, which owns the task.
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
	point := &machinev1.WorkerPoint{
		Time:        timestamppb.New(now),
		CpuPercent:  cpu,
		MemoryBytes: g.Memory,
	}
	h.mu.Lock()
	if r.samples == nil {
		r.samples = newWorkerRing(maxWorkerSamples)
	}
	r.samples.Add(point)
	pct := r.samples.MemoryPercentiles()
	h.mu.Unlock()
	if h.history != nil {
		h.history.Worker(r.task.GetId(), point)
	}

	t := r.task
	res := &planv1.Resources{
		CpuPercent: cpu, MemoryBytes: g.Memory, Processes: int32(g.Processes),
		PeakCpuPercent:  max(t.GetResources().GetPeakCpuPercent(), cpu),
		PeakMemoryBytes: max(t.GetResources().GetPeakMemoryBytes(), g.Memory, g.Peak),
		ReadTime:        timestamppb.New(now),
	}
	pct.set(res)
	t.Resources = res // Replaced at each reading, never changed: Uses shares it.
	use := &machinev1.WorkerUse{
		TaskId: t.GetId(), WishId: t.GetWishId(), Code: t.GetCode(), Title: t.GetTitle(), Resources: res,
	}
	h.mu.Lock()
	r.use = use
	h.mu.Unlock()
	// The turns the worker took since the last write go with the reading: an event writes the task seldom.
	if !worth(m.written, res) && t.GetTurns() == m.turns {
		return
	}
	err = h.store.Tx(context.Background(), func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodMeasure, res); err != nil {
			return err
		}
		carry(tx, t)
		return tx.Put(t)
	})
	if err != nil {
		logx.L().Error("record what a worker uses", "task", r.id, "err", err)
		return
	}
	m.written, m.turns = res, t.GetTurns()
}

// worth says whether a reading moved enough from the one written to be written: a process more or less,
// 5% of memory, a new peak as large, 20 points of CPU after 30 s, or a minute gone.
func worth(was, now *planv1.Resources) bool {
	if was == nil || was.GetReadTime() == nil {
		return true
	}
	elapsed := now.GetReadTime().AsTime().Sub(was.GetReadTime().AsTime())
	if elapsed >= rewrite {
		return true
	}
	if was.GetProcesses() != now.GetProcesses() {
		return true
	}
	ratio := func(a, b uint64) bool { return math.Abs(float64(a)-float64(b)) >= 0.05*float64(max(a, 1)) }
	if ratio(was.GetMemoryBytes(), now.GetMemoryBytes()) ||
		ratio(was.GetPeakMemoryBytes(), now.GetPeakMemoryBytes()) ||
		ratio(was.GetP95MemoryBytes(), now.GetP95MemoryBytes()) ||
		ratio(was.GetP50MemoryBytes(), now.GetP50MemoryBytes()) {
		return true
	}
	if elapsed >= minCPUInterval {
		cpuMoved := func(a, b float64) bool { return math.Abs(a-b) >= minCPUMoved }
		if cpuMoved(was.GetCpuPercent(), now.GetCpuPercent()) || cpuMoved(was.GetPeakCpuPercent(), now.GetPeakCpuPercent()) {
			return true
		}
	}
	return false
}

// fresh clears what the task's last worker was using, keeping its peaks and percentiles, as a new worker starts.
func fresh(t *planv1.Task) {
	if res := t.GetResources(); res != nil {
		t.Resources = &planv1.Resources{
			PeakCpuPercent:  res.GetPeakCpuPercent(),
			PeakMemoryBytes: res.GetPeakMemoryBytes(),
			P95MemoryBytes:  res.GetP95MemoryBytes(),
			P50MemoryBytes:  res.GetP50MemoryBytes(),
			P60MemoryBytes:  res.GetP60MemoryBytes(),
			P70MemoryBytes:  res.GetP70MemoryBytes(),
			P80MemoryBytes:  res.GetP80MemoryBytes(),
		}
	}
}

// Uses lists the workers running now that were read, with what each uses, the busiest first: the most CPU, then
// the most memory.
func (h *Harness) Uses() []*machinev1.WorkerUse {
	h.mu.Lock()
	var out []*machinev1.WorkerUse
	for _, r := range h.runs {
		if r.use != nil {
			u := proto.CloneOf(r.use)
			u.Paused = r.paused
			out = append(out, u)
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
	// Overlay live resource readings from in-flight runs, excluding lost processes.
	h.mu.Lock()
	activeTasks := make([]*planv1.Task, 0, len(tasks))
	for _, t := range tasks {
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
			r, ok := h.runs[t.GetId()]
			if !ok {
				continue
			}
			if r.use != nil && r.use.GetResources() != nil {
				t.Resources = r.use.GetResources()
			} else if r.task != nil && r.task.GetResources() != nil {
				t.Resources = r.task.GetResources()
			}
		}
		activeTasks = append(activeTasks, t)
	}
	h.mu.Unlock()
	engaged, actual = dispatch.WorkerMemory(activeTasks, p, h.now())
	return engaged, actual, nil
}

// Resident returns the resident memory of the workers running or paused, as last read, watchers left out: what the
// memory available already leaves out, which the memory Djinn may commit counts back in (machine.Monitor.SetWorkerMemory).
func (h *Harness) Resident() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sum uint64
	for _, r := range h.runs {
		if !r.watcher && r.use != nil {
			sum += r.use.GetResources().GetMemoryBytes()
		}
	}
	return sum
}

// WorkerForecast returns the typical forecast memory per worker across providers in use, at the notch's percentile,
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

// P95 calculates the 95th percentile of uint64 values using linear interpolation.
func P95(vals []uint64) uint64 {
	sorted := slices.Clone(vals)
	slices.Sort(sorted)
	return percentile(sorted, 95)
}

// percentile is the q-th percentile of sorted values, with linear interpolation; 0 without values.
func percentile(sorted []uint64, q int) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := float64(q) / 100 * float64(len(sorted)-1)
	low := int(math.Floor(idx))
	high := int(math.Ceil(idx))
	if low == high {
		return sorted[low]
	}
	weight := idx - float64(low)
	return uint64(math.Round(float64(sorted[low])*(1-weight) + float64(sorted[high])*weight))
}

// memoryPercentiles is a worker's resident memory at the percentiles the operating load notches engage it at
// (machine.Policy.Percentile).
type memoryPercentiles struct{ p50, p60, p70, p80, p95 uint64 }

// set writes them on res.
func (m memoryPercentiles) set(res *planv1.Resources) {
	res.P50MemoryBytes, res.P60MemoryBytes, res.P70MemoryBytes = m.p50, m.p60, m.p70
	res.P80MemoryBytes, res.P95MemoryBytes = m.p80, m.p95
}

// memory is the resident memory of the recorded samples, sorted; samples read as 0 left out.
func (w *workerRing) memory() []uint64 {
	if w == nil || len(w.buf) == 0 {
		return nil
	}
	var vals []uint64
	limit := len(w.buf)
	if !w.full {
		limit = w.head
	}
	for i := 0; i < limit; i++ {
		if p := w.buf[i]; p != nil && p.GetMemoryBytes() > 0 {
			vals = append(vals, p.GetMemoryBytes())
		}
	}
	slices.Sort(vals)
	return vals
}

// P95Memory returns the 95th percentile resident memory across recorded samples.
func (w *workerRing) P95Memory() uint64 {
	return percentile(w.memory(), 95)
}

// MemoryPercentiles returns the 50th, 60th, 70th, 80th and 95th percentiles of resident memory across recorded
// samples, from one sort.
func (w *workerRing) MemoryPercentiles() memoryPercentiles {
	vals := w.memory()
	return memoryPercentiles{
		p50: percentile(vals, 50), p60: percentile(vals, 60), p70: percentile(vals, 70), p80: percentile(vals, 80),
		p95: percentile(vals, 95),
	}
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
		if r.light {
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
		var p95Mem uint64
		if t.GetResources() != nil {
			peakMem = t.GetResources().GetPeakMemoryBytes()
			peakCPU = t.GetResources().GetPeakCpuPercent()
			p95Mem = t.GetResources().GetP95MemoryBytes()
		}
		for _, p := range pts {
			if p.GetMemoryBytes() > peakMem {
				peakMem = p.GetMemoryBytes()
			}
			if p.GetCpuPercent() > peakCPU {
				peakCPU = p.GetCpuPercent()
			}
		}
		if r.samples != nil {
			if sP95 := r.samples.P95Memory(); sP95 > 0 {
				p95Mem = sP95
			}
		}
		if p95Mem == 0 {
			p95Mem = peakMem
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
			P95MemoryBytes:  p95Mem,
			Paused:          r.paused,
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

// WorkerP95Memory returns the 95th percentile memory for a task's run samples.
func (h *Harness) WorkerP95Memory(taskID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.runs[taskID]; ok && r.samples != nil {
		return r.samples.P95Memory()
	}
	return 0
}
