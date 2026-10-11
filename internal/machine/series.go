package machine

import (
	"bufio"
	"bytes"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
)

const maxMachineSamples = 1440 // 120 minutes at 5s intervals

// SeriesRecorder records machine and Djinn process consumption samples over time.
type SeriesRecorder struct {
	m          *Monitor
	readWorker func(pid int, cgroup string) (Group, error)
	fsys       fs.FS

	mu             sync.Mutex
	buf            []*machinev1.MachinePoint
	head           int
	full           bool
	lastTotalTicks uint64
	lastIdleTicks  uint64
	lastDjinnCPU   time.Duration
	lastDjinnAt    time.Time
}

// NewSeriesRecorder returns a recorder for machine metrics.
func NewSeriesRecorder(m *Monitor, readWorker func(pid int, cgroup string) (Group, error)) *SeriesRecorder {
	return &SeriesRecorder{
		m:          m,
		readWorker: readWorker,
		fsys:       os.DirFS("/"),
		buf:        make([]*machinev1.MachinePoint, maxMachineSamples),
	}
}

// SetFS overrides the filesystem for testing /proc.
func (r *SeriesRecorder) SetFS(fsys fs.FS) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fsys = fsys
}

// AddPoint appends a pre-built machine point (used in tests).
func (r *SeriesRecorder) AddPoint(p *machinev1.MachinePoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = p
	r.head++
	if r.head >= len(r.buf) {
		r.head = 0
		r.full = true
	}
}

// Sample collects one measurement point from the machine and Djinn process.
func (r *SeriesRecorder) Sample() {
	now := time.Now()
	var snap Snapshot
	if r.m != nil {
		snap = r.m.Snapshot()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Machine CPU % from /proc/stat if available.
	var machineCPU float64
	if r.fsys != nil {
		if data, err := fs.ReadFile(r.fsys, "proc/stat"); err == nil {
			total, idle := parseProcStat(data)
			if r.lastTotalTicks > 0 && total > r.lastTotalTicks {
				dTotal := total - r.lastTotalTicks
				dIdle := uint64(0)
				if idle >= r.lastIdleTicks {
					dIdle = idle - r.lastIdleTicks
				}
				if dTotal >= dIdle {
					machineCPU = math.Round(float64(dTotal-dIdle)/float64(dTotal)*1000) / 10
				}
			}
			r.lastTotalTicks = total
			r.lastIdleTicks = idle
		}
	}
	if machineCPU == 0 && snap.CPU != nil && snap.CPU.Some > 0 {
		machineCPU = snap.CPU.Some
	}

	// 2. Djinn process CPU % and memory.
	var djinnCPU float64
	var djinnMem uint64
	if r.readWorker != nil && NotMeasured == "" {
		if g, err := r.readWorker(os.Getpid(), ""); err == nil {
			djinnMem = g.Memory
			if !r.lastDjinnAt.IsZero() {
				d := now.Sub(r.lastDjinnAt)
				if d > 0 && g.CPU >= r.lastDjinnCPU {
					djinnCPU = math.Round(float64(g.CPU-r.lastDjinnCPU)/float64(d)*1000) / 10
				}
			}
			r.lastDjinnCPU = g.CPU
			r.lastDjinnAt = now
		}
	}

	// 3. Machine memory used.
	var memUsed uint64
	if snap.MemoryTotal >= snap.MemoryAvailable {
		memUsed = snap.MemoryTotal - snap.MemoryAvailable
	}

	point := &machinev1.MachinePoint{
		Time:             timestamppb.New(now),
		CpuPercent:       machineCPU,
		DjinnCpuPercent:  djinnCPU,
		MemoryBytes:      memUsed,
		DjinnMemoryBytes: djinnMem,
	}

	r.buf[r.head] = point
	r.head++
	if r.head >= len(r.buf) {
		r.head = 0
		r.full = true
	}
}

// Points returns all recorded machine points since cutoff.
func (r *SeriesRecorder) Points(cutoff time.Time) []*machinev1.MachinePoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	var out []*machinev1.MachinePoint
	if !r.full {
		for i := 0; i < r.head; i++ {
			if r.buf[i] != nil && (cutoff.IsZero() || !r.buf[i].GetTime().AsTime().Before(cutoff)) {
				out = append(out, r.buf[i])
			}
		}
		return out
	}
	for i := r.head; i < len(r.buf); i++ {
		if r.buf[i] != nil && (cutoff.IsZero() || !r.buf[i].GetTime().AsTime().Before(cutoff)) {
			out = append(out, r.buf[i])
		}
	}
	for i := 0; i < r.head; i++ {
		if r.buf[i] != nil && (cutoff.IsZero() || !r.buf[i].GetTime().AsTime().Before(cutoff)) {
			out = append(out, r.buf[i])
		}
	}
	return out
}

func parseProcStat(data []byte) (total, idle uint64) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line[4:])
			for i, f := range fields {
				val, err := strconv.ParseUint(f, 10, 64)
				if err != nil {
					continue
				}
				total += val
				if i == 3 || i == 4 { // idle (index 3) and iowait (index 4)
					idle += val
				}
			}
			return total, idle
		}
	}
	return 0, 0
}
