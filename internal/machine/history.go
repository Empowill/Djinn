package machine

import (
	"context"
	"log"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/internal/store"
)

// HistoryRetention is how long the series are kept in the store: the readings of every 5 s for a day, then the
// highest of each minute for a week; 400,000 samples at most (some 30 MB), the oldest going first.
var HistoryRetention = store.Retention{Fine: 24 * time.Hour, Keep: 7 * 24 * time.Hour, Max: 400_000}

// MaxSeriesPoints is the most points a series is sent with: a longer range is read by buckets, each its highest
// values, so that a peak stays readable.
const MaxSeriesPoints = 1500

// pruneEvery is how often the store's samples are rolled up and pruned.
const pruneEvery = 10 * time.Minute

const machineSeries = "machine"

func taskSeries(taskID string) string { return "task:" + taskID }

// History keeps the series of the machine and of each worker in the store, so that they outlive a restart of Djinn:
// the readings wait in memory, then Flush writes them, every 30 s in djinn up; reading merges both.
type History struct {
	db *store.Store

	mu        sync.Mutex
	pending   []store.Sample
	lastPrune time.Time
}

// NewHistory keeps the series in db.
func NewHistory(db *store.Store) *History { return &History{db: db} }

// Machine records a reading of the machine.
func (h *History) Machine(p *machinev1.MachinePoint) {
	h.add(store.Sample{
		Series: machineSeries, At: p.GetTime().AsTime(), Seconds: 5, CPU: p.GetCpuPercent(), Memory: p.GetMemoryBytes(),
		DjinnCPU: p.GetDjinnCpuPercent(), DjinnMemory: p.GetDjinnMemoryBytes(),
	})
}

// Worker records a reading of the worker of a task.
func (h *History) Worker(taskID string, p *machinev1.WorkerPoint) {
	h.add(store.Sample{
		Series: taskSeries(taskID), At: p.GetTime().AsTime(), Seconds: 5, CPU: p.GetCpuPercent(),
		Memory: p.GetMemoryBytes(),
	})
}

func (h *History) add(x store.Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending = append(h.pending, x)
}

// Flush writes the readings waiting, and rolls up and prunes the store's samples every 10 minutes
// (HistoryRetention). Readings it could not write wait for the next flush, up to an hour of them.
func (h *History) Flush(ctx context.Context) {
	h.mu.Lock()
	batch := h.pending
	h.pending = nil
	prune := time.Since(h.lastPrune) >= pruneEvery
	if prune {
		h.lastPrune = time.Now()
	}
	h.mu.Unlock()
	if err := h.db.AddSamples(ctx, batch); err != nil {
		log.Printf("djinn: keep the machine's series: %v", err)
		h.mu.Lock()
		h.pending = append(batch, h.pending...)
		if over := len(h.pending) - 720*8; over > 0 {
			h.pending = h.pending[over:]
		}
		h.mu.Unlock()
		return
	}
	if prune {
		if err := h.db.PruneSamples(ctx, time.Now(), HistoryRetention); err != nil {
			log.Printf("djinn: prune the machine's series: %v", err)
		}
	}
}

// read is the samples of series after from: the store's, then those waiting to be written.
func (h *History) read(ctx context.Context, series string, from time.Time) []store.Sample {
	out, err := h.db.Samples(ctx, series, from)
	if err != nil {
		log.Printf("djinn: read the machine's series: %v", err)
	}
	last := from
	if len(out) > 0 {
		last = out[len(out)-1].At
	}
	h.mu.Lock()
	for _, x := range h.pending {
		if x.Series == series && x.At.After(last) {
			out = append(out, x)
		}
	}
	h.mu.Unlock()
	return out
}

// thin keeps at most MaxSeriesPoints of samples spread over [from, to]: one per bucket of time, its highest values,
// at the time of the bucket's last sample.
func thin(samples []store.Sample, from, to time.Time) []store.Sample {
	if len(samples) <= MaxSeriesPoints {
		return samples
	}
	bucket := to.Sub(from)/(MaxSeriesPoints-1) + 1
	if bucket < 5*time.Second {
		bucket = 5 * time.Second
	}
	var out []store.Sample
	var key int64 = -1
	for _, x := range samples {
		k := min(int64(x.At.Sub(from)/bucket), MaxSeriesPoints-1)
		if k != key || len(out) == 0 {
			out = append(out, x)
			key = k
			continue
		}
		b := &out[len(out)-1]
		b.At = x.At
		b.CPU, b.Memory = max(b.CPU, x.CPU), max(b.Memory, x.Memory)
		b.DjinnCPU, b.DjinnMemory = max(b.DjinnCPU, x.DjinnCPU), max(b.DjinnMemory, x.DjinnMemory)
	}
	return out
}

// MachinePoints is the machine's series after from, until to.
func (h *History) MachinePoints(ctx context.Context, from, to time.Time) []*machinev1.MachinePoint {
	samples := thin(h.read(ctx, machineSeries, from), from, to)
	out := make([]*machinev1.MachinePoint, len(samples))
	for i, x := range samples {
		out[i] = &machinev1.MachinePoint{
			Time: timestamppb.New(x.At), CpuPercent: x.CPU, MemoryBytes: x.Memory, DjinnCpuPercent: x.DjinnCPU,
			DjinnMemoryBytes: x.DjinnMemory,
		}
	}
	return out
}

// WorkerPoints is the series of a task's worker after from, until to.
func (h *History) WorkerPoints(ctx context.Context, taskID string, from, to time.Time) []*machinev1.WorkerPoint {
	samples := thin(h.read(ctx, taskSeries(taskID), from), from, to)
	out := make([]*machinev1.WorkerPoint, len(samples))
	for i, x := range samples {
		out[i] = &machinev1.WorkerPoint{Time: timestamppb.New(x.At), CpuPercent: x.CPU, MemoryBytes: x.Memory}
	}
	return out
}
