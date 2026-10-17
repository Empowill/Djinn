package machine

import (
	"bufio"
	"bytes"
	"io/fs"
	"math"
	"os"
	"runtime"
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
	readSelf       func() (Group, error) // Djinn's own process alone; nil: not read
	lastSelfCPU    time.Duration
	lastSelfAt     time.Time
	readStrays     func() (map[int]time.Duration, uint64, error) // its workers' processes out of its tree; nil: none
	lastStrays     map[int]time.Duration
	cpuRead        bool     // the machine's CPU was read from /proc/stat: the points' CpuPercent is the machine's
	memoryTotal    uint64   // the machine's memory, as the last sample read it
	history        *History // nil: the samples are kept in memory only
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
	first := false // The first reading of /proc/stat: no CPU % yet.
	if r.fsys != nil {
		if data, err := fs.ReadFile(r.fsys, "proc/stat"); err == nil {
			total, idle := parseProcStat(data)
			first = r.lastTotalTicks == 0
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
			r.cpuRead = total > 0
		}
	}
	if machineCPU == 0 && snap.CPU != nil && snap.CPU.Some > 0 {
		machineCPU = snap.CPU.Some
	}

	// 2. Djinn process CPU % and memory: its tree, and its workers' processes adopted out of it (ReadStrays).
	var djinnCPU float64
	var djinnMem uint64
	if r.readWorker != nil {
		if g, err := r.readWorker(os.Getpid(), ""); err == nil {
			djinnMem = g.Memory
			var used time.Duration
			if g.CPU >= r.lastDjinnCPU {
				used = g.CPU - r.lastDjinnCPU
			}
			if r.readStrays != nil {
				if strays, memory, err := r.readStrays(); err == nil {
					djinnMem += memory
					// Each stray from its last reading: one that ended, or was just adopted, counts from the next.
					for pid, cpu := range strays {
						if was, ok := r.lastStrays[pid]; ok && cpu > was {
							used += cpu - was
						}
					}
					r.lastStrays = strays
				}
			}
			if !r.lastDjinnAt.IsZero() {
				if d := now.Sub(r.lastDjinnAt); d > 0 {
					djinnCPU = math.Round(float64(used)/float64(d)*1000) / 10
				}
			}
			r.lastDjinnCPU = g.CPU
			r.lastDjinnAt = now
		}
	}

	// 2b. Djinn's own process alone, without what it started.
	var selfCPU float64
	var selfMem uint64
	if r.readSelf != nil {
		if g, err := r.readSelf(); err == nil {
			selfMem = g.Memory
			if !r.lastSelfAt.IsZero() {
				if d := now.Sub(r.lastSelfAt); d > 0 && g.CPU >= r.lastSelfCPU {
					selfCPU = math.Round(float64(g.CPU-r.lastSelfCPU)/float64(d)*1000) / 10
				}
			}
			r.lastSelfCPU, r.lastSelfAt = g.CPU, now
		}
	}
	// Djinn's CPU is read in percent of one core: as a share of the machine, like the machine's own.
	cores := snap.Cores
	if cores <= 0 {
		cores = runtime.NumCPU()
	}
	djinnCPU = math.Round(djinnCPU/float64(cores)*10) / 10
	selfCPU = math.Round(selfCPU/float64(cores)*10) / 10

	// 3. Machine memory used.
	r.memoryTotal = snap.MemoryTotal
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
		SelfCpuPercent:   selfCPU,
		SelfMemoryBytes:  selfMem,
	}

	r.buf[r.head] = point
	r.head++
	if r.head >= len(r.buf) {
		r.head = 0
		r.full = true
	}
	if r.history != nil && !first {
		r.history.Machine(point)
	}
}

// ReadSelf reads Djinn's own process alone with read: ReadSelf in djinn up.
func (r *SeriesRecorder) ReadSelf(read func() (Group, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readSelf = read
}

// ReadStrays reads, with read, the processes of Djinn's running workers adopted out of its tree (ReadStrays in djinn
// up): the Djinn line counts them.
func (r *SeriesRecorder) ReadStrays(read func() (map[int]time.Duration, uint64, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readStrays = read
}

// Keep keeps each sample in h too, the store's series that outlive a restart.
func (r *SeriesRecorder) Keep(h *History) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = h
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

// Outside is the load outside Djinn over the points measured after since: the machine's CPU and memory beyond the
// Djinn line, averaged; false when no point came since. It reads what the samples hold, nothing of the machine: the
// auto mode's beat costs a walk of the last points.
func (r *SeriesRecorder) Outside(since time.Time) (Outside, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o := Outside{CPUKnown: r.cpuRead}
	var n, measured int // the points, and those with Djinn's memory read
	for i := 1; i <= len(r.buf); i++ {
		j := r.head - i
		if j < 0 {
			if !r.full {
				break
			}
			j += len(r.buf)
		}
		p := r.buf[j]
		if p == nil || !p.GetTime().AsTime().After(since) {
			break
		}
		n++
		o.CPU += max(0, p.GetCpuPercent()-p.GetDjinnCpuPercent())
		if r.memoryTotal > 0 && p.GetDjinnMemoryBytes() > 0 {
			measured++
			if p.GetMemoryBytes() > p.GetDjinnMemoryBytes() {
				o.Memory += float64(p.GetMemoryBytes()-p.GetDjinnMemoryBytes()) / float64(r.memoryTotal) * 100
			}
		}
	}
	if n == 0 {
		return Outside{}, false
	}
	o.CPU /= float64(n)
	if measured > 0 {
		o.Memory /= float64(measured)
		o.MemoryKnown = true
	}
	return o, true
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
