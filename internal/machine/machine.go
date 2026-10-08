// Package machine discovers what the machine has (cores, memory) and how loaded it is (load, pressure), and turns
// it into the number of workers Djinn runs at once, and whether a new worker or a gate may start now.
package machine

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// GiB is a gibibyte.
const GiB = 1 << 30

// Pressure is how much of the last 10 seconds tasks waited for a resource, in percent: Linux's pressure stall
// information.
type Pressure struct {
	Some float64 // at least one task waited
	Full float64 // every task waited
}

// Snapshot is the machine as read at one time. What the system does not give stays unknown.
type Snapshot struct {
	OS, Arch        string
	Cores           int
	MemoryTotal     uint64 // bytes; 0 when unknown
	MemoryAvailable uint64 // bytes available to new programs
	Load1           float64
	LoadKnown       bool
	CPU, Memory     *Pressure // nil without PSI
	// MemoryLevel is macOS's memory pressure level (kern.memorystatus_vm_pressure_level): 1 normal, 2 warning,
	// 4 critical; 0 when unknown.
	MemoryLevel int
	Time        time.Time
}

// Policy turns a snapshot into a number of workers and a pressure verdict. Every threshold is a field: the
// defaults are DefaultPolicy.
type Policy struct {
	// Workers, when above 0, is the number of workers, whatever the machine (djinn up --workers).
	Workers int
	// One worker per CoresPerWorker cores, and per MemoryPerWorker bytes beyond MemoryReserve; the smaller wins,
	// between 1 and MaxWorkers.
	CoresPerWorker  int
	MemoryPerWorker uint64
	MemoryReserve   uint64
	MaxWorkers      int
	// The machine is under pressure when, over the last 10 seconds, tasks waited for the CPU at least CPUPressure
	// percent of the time, or for memory at least MemoryPressure percent (PSI "some").
	CPUPressure    float64
	MemoryPressure float64
	// Without PSI: when the load average per core reaches LoadPerCore, or the memory available falls below
	// MemoryFree of the total. On macOS, the system's own memory pressure level at warning or above.
	LoadPerCore float64
	MemoryFree  float64
}

// DefaultPolicy is the policy Djinn runs with. A worker is mostly an agent waiting for its model; the heavy commands
// it runs (builds, tests, code generation) go through gates. Two cores and 2 GiB a worker leaves room for them.
func DefaultPolicy() Policy {
	return Policy{
		CoresPerWorker: 2, MemoryPerWorker: 2 * GiB, MemoryReserve: 2 * GiB, MaxWorkers: 16,
		CPUPressure: 50, MemoryPressure: 10, LoadPerCore: 2, MemoryFree: 0.10,
	}
}

// Slots is the most workers the machine runs at once, and the rule that gave it.
func (p Policy) Slots(s Snapshot) (int, string) {
	if p.Workers > 0 {
		return p.Workers, fmt.Sprintf("%d, set by djinn up --workers", p.Workers)
	}
	cores := max(s.Cores, 1)
	byCores := max(cores/max(p.CoresPerWorker, 1), 1)
	n, rule := byCores, fmt.Sprintf("one per %d cores of %d", p.CoresPerWorker, cores)
	if s.MemoryTotal > 0 && p.MemoryPerWorker > 0 {
		spare := uint64(0)
		if s.MemoryTotal > p.MemoryReserve {
			spare = s.MemoryTotal - p.MemoryReserve
		}
		byMemory := max(int(spare/p.MemoryPerWorker), 1)
		memRule := fmt.Sprintf("one per %s of memory beyond %s, of %s", size(p.MemoryPerWorker), size(p.MemoryReserve),
			size(s.MemoryTotal))
		switch {
		case byMemory < byCores:
			n, rule = byMemory, memRule
		case byMemory == byCores:
			rule += ", and " + memRule
		}
	}
	if p.MaxWorkers > 0 && n > p.MaxWorkers {
		return p.MaxWorkers, fmt.Sprintf("%d, the most Djinn runs (%s gives more)", p.MaxWorkers, rule)
	}
	return n, fmt.Sprintf("%d: %s", n, rule)
}

// Pressure says why the machine is under pressure, or "" when it is not. Under pressure, no worker starts and no
// gate is granted; what runs goes on.
func (p Policy) Pressure(s Snapshot) string {
	switch {
	case s.CPU != nil && s.CPU.Some >= p.CPUPressure:
		return fmt.Sprintf("tasks waited for the CPU %.0f%% of the last 10 s (%.0f%% at most)", s.CPU.Some, p.CPUPressure)
	case s.Memory != nil && s.Memory.Some >= p.MemoryPressure:
		return fmt.Sprintf("tasks waited for memory %.0f%% of the last 10 s (%.0f%% at most)", s.Memory.Some, p.MemoryPressure)
	case s.MemoryLevel >= 2:
		return "the system reports memory pressure"
	case s.CPU == nil && s.LoadKnown && s.Cores > 0 && s.Load1/float64(s.Cores) >= p.LoadPerCore:
		return fmt.Sprintf("the load is %.1f on %d cores (%.1f a core at most)", s.Load1, s.Cores, p.LoadPerCore)
	case s.Memory == nil && s.MemoryLevel == 0 && s.MemoryTotal > 0 &&
		float64(s.MemoryAvailable) < p.MemoryFree*float64(s.MemoryTotal):
		return fmt.Sprintf("%s of memory available of %s (%.0f%% at least)", size(s.MemoryAvailable), size(s.MemoryTotal),
			p.MemoryFree*100)
	}
	return ""
}

// size writes bytes in GiB, or MiB below one.
func size(b uint64) string {
	if b < GiB {
		return fmt.Sprintf("%d MiB", b>>20)
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/GiB)
}

// Monitor reads the machine at most once a second, for the scheduler and the gates.
type Monitor struct {
	policy Policy
	read   func() (Snapshot, error)
	every  time.Duration

	mu   sync.Mutex
	last Snapshot
	at   time.Time
}

// NewMonitor is a monitor of this machine with the policy p. read, when not nil, replaces reading the machine: the
// tests simulate it.
func NewMonitor(p Policy, read func() (Snapshot, error)) *Monitor {
	if read == nil {
		read = Read
	}
	return &Monitor{policy: p, read: read, every: time.Second}
}

// Snapshot is the machine, read at most a second ago. A reading that fails keeps what is known: the cores.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.at.IsZero() && time.Since(m.at) < m.every {
		return m.last
	}
	s, err := m.read()
	if err != nil {
		s = Snapshot{Cores: runtime.NumCPU()}
	}
	s.OS, s.Arch = runtime.GOOS, runtime.GOARCH
	if s.Cores == 0 {
		s.Cores = runtime.NumCPU()
	}
	if s.Time.IsZero() {
		s.Time = time.Now()
	}
	m.last, m.at = s, time.Now()
	return s
}

// Capacity is the most workers the machine runs at once and its rule, and why it is under pressure ("" when it is
// not).
func (m *Monitor) Capacity() (slots int, rule, pressure string) {
	s := m.Snapshot()
	slots, rule = m.policy.Slots(s)
	return slots, rule, m.policy.Pressure(s)
}

// Pressure says why the machine is under pressure now, or "".
func (m *Monitor) Pressure() string { return m.policy.Pressure(m.Snapshot()) }

// Policy is the monitor's policy.
func (m *Monitor) Policy() Policy { return m.policy }
