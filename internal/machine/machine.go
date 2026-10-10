// Package machine discovers what the machine has (cores, memory, disk, GPUs) and how loaded it is (load, pressure),
// and turns it into the number of workers Djinn runs at once, whether a new worker or a gate may start now, and
// whether a local model can run.
package machine

import (
	"cmp"
	"fmt"
	"runtime"
	"slices"
	"strings"
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
	Disk        *Disk // the data folder's; nil when unknown
	GPUs        []GPU
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
	// WorkerMemory, when above 0, is the most memory a worker may hold, in bytes, where it runs in a systemd scope
	// of its own (Scopes, djinn up --worker-memory): past it, the kernel reclaims, then kills a process of the worker.
	// 0, the default: no ceiling.
	WorkerMemory uint64
	// The machine is under pressure when, over the last 10 seconds, tasks waited for the CPU at least CPUPressure
	// percent of the time, or for memory at least MemoryPressure percent (PSI "some").
	CPUPressure    float64
	MemoryPressure float64
	// Without PSI: when the load average per core reaches LoadPerCore, or the memory available falls below
	// MemoryFree of the total. On macOS, the system's own memory pressure level at warning or above.
	LoadPerCore float64
	MemoryFree  float64
	// A gate goes to a command measured in its project only when the memory available, less the peaks of the
	// commands holding a gate, holds its highest peak plus CommandMargin; a command never measured goes as it comes.
	// Djinn's recommendation: the developer may change it.
	CommandMargin uint64
	// Another worker starts only when the memory available, less what the workers running may still take, holds the
	// typical peak of a worker of its provider plus WorkerMargin. The typical peak is the median of the peaks of the
	// latest WorkerPeaks finished workers of that provider; WorkerPeak while none is measured. A running worker may
	// still take the typical peak of its provider less what it uses now; one not read yet, all of it.
	WorkerPeak   uint64
	WorkerPeaks  int
	WorkerMargin uint64
	// A local model runs on an NVIDIA or AMD GPU, its driver loaded, with ModelGPUMemory of its own; on Apple
	// Silicon with ModelMemory of unified memory; else on the CPU, slowly, with ModelMemory. Its weights need
	// ModelDisk free on the disk of the data folder.
	ModelGPUMemory uint64
	ModelMemory    uint64
	ModelDisk      uint64
}

// DefaultPolicy is the policy Djinn runs with. A worker is mostly an agent waiting for its model; the heavy commands
// it runs (builds, tests, code generation) go through gates. Two cores and 2 GiB a worker leaves room for them.
func DefaultPolicy() Policy {
	return Policy{
		CoresPerWorker: 2, MemoryPerWorker: 2 * GiB, MemoryReserve: 2 * GiB, MaxWorkers: 16,
		CPUPressure: 50, MemoryPressure: 10, LoadPerCore: 2, MemoryFree: 0.10, CommandMargin: 512 << 20,
		WorkerPeak: GiB, WorkerPeaks: 10, WorkerMargin: 512 << 20,
		ModelGPUMemory: 6 * GiB, ModelMemory: 16 * GiB, ModelDisk: 10 * GiB,
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

// Room says why the machine cannot hold the command yet, its highest measured peak memory being peak, or "" when it
// can: the memory available, less held (the peaks of the commands holding a gate, which may not have reached them
// yet), holds the peak plus CommandMargin. A command never measured (peak 0), or a machine whose memory is unknown,
// has room.
func (p Policy) Room(s Snapshot, command string, peak, held uint64) string {
	free := s.MemoryAvailable - min(held, s.MemoryAvailable)
	if peak == 0 || s.MemoryTotal == 0 || free >= peak+p.CommandMargin {
		return ""
	}
	why := fmt.Sprintf("%s peaks at %s, %s free", command, size(peak), size(s.MemoryAvailable))
	if held > 0 {
		why += fmt.Sprintf(", %s of it for the commands holding a gate", size(min(held, s.MemoryAvailable)))
	}
	return why
}

// Typical is the typical peak memory of a worker, and how many measured peaks gave it: the median of the first
// WorkerPeaks of peaks, the latest first; WorkerPeak and 0 when none is measured. With an even count, the mean of the
// two in the middle.
func (p Policy) Typical(peaks []uint64) (uint64, int) {
	var measured []uint64
	for _, v := range peaks {
		if v > 0 && (p.WorkerPeaks <= 0 || len(measured) < p.WorkerPeaks) {
			measured = append(measured, v)
		}
	}
	n := len(measured)
	if n == 0 {
		return p.WorkerPeak, 0
	}
	slices.Sort(measured)
	if n%2 == 1 {
		return measured[n/2], n
	}
	return measured[n/2-1]/2 + measured[n/2]/2, n
}

// WorkerRoom says why the machine cannot hold another worker of provider yet, or "" when it can: the memory
// available, less held (what the workers running may still take), holds peak, the typical peak of a worker of
// provider measured over measured workers (Typical), plus WorkerMargin. A machine whose memory is unknown (available
// 0) has room.
func (p Policy) WorkerRoom(available, held uint64, provider string, peak uint64, measured int) string {
	free := available - min(held, available)
	if available == 0 || free >= peak+p.WorkerMargin {
		return ""
	}
	how := "none measured yet"
	switch {
	case measured == 1:
		how = "the one measured"
	case measured > 1:
		how = fmt.Sprintf("the median of the last %d measured", measured)
	}
	why := fmt.Sprintf("a %s worker peaks at %s (%s), %s free", provider, size(peak), how, size(available))
	if held > 0 {
		why += fmt.Sprintf(", %s of it for the workers running", size(min(held, available)))
	}
	return why + fmt.Sprintf(", %s kept", size(p.WorkerMargin))
}

// LocalModel says whether a local open-weight model can run on the machine, and why: dispatch asks one only where it
// can (T07). The first usable GPU decides; without one, the memory.
func (p Policy) LocalModel(s Snapshot) (bool, string) {
	if s.Disk != nil && s.Disk.Available < p.ModelDisk {
		return false, fmt.Sprintf("%s free on the disk of %s, %s at least for a model's weights", size(s.Disk.Available),
			s.Disk.Path, size(p.ModelDisk))
	}
	var unusable []string
	for _, g := range s.GPUs {
		ok, why := p.usable(g, s.MemoryTotal)
		if ok {
			return true, why
		}
		unusable = append(unusable, why)
	}
	noGPU := "no GPU found"
	if len(unusable) > 0 {
		noGPU = strings.Join(unusable, "; ")
	}
	switch {
	case s.MemoryTotal >= p.ModelMemory:
		return true, fmt.Sprintf("on the CPU, slowly, with %s of memory: %s", size(s.MemoryTotal), noGPU)
	case s.MemoryTotal == 0:
		return false, noGPU + ", and the memory is unknown"
	}
	return false, fmt.Sprintf("%s, and %s of memory (%s at least without a GPU)", noGPU, size(s.MemoryTotal),
		size(p.ModelMemory))
}

// usable says whether a local model runs on the GPU g, the machine having memory bytes, and why.
func (p Policy) usable(g GPU, memory uint64) (bool, string) {
	switch {
	case g.Unified:
		m := cmp.Or(g.Memory, memory)
		if m < p.ModelMemory {
			return false, fmt.Sprintf("the %s has %s of unified memory, %s at least", g.Name, size(m), size(p.ModelMemory))
		}
		return true, fmt.Sprintf("on the %s, through %s, with %s of unified memory", g.Name, g.Driver, size(m))
	case g.Vendor != "nvidia" && g.Vendor != "amd":
		return false, fmt.Sprintf("the %s GPU %s is not counted: only NVIDIA, AMD and Apple ones are", g.Vendor, g.Name)
	case g.Vendor == "nvidia" && g.Driver != "nvidia", g.Vendor == "amd" && g.Driver != "amdgpu":
		return false, fmt.Sprintf("the %s has no driver a model runs on (%s)", g.Name, cmp.Or(g.Driver, "none"))
	case g.Memory == 0:
		return false, fmt.Sprintf("the memory of the %s is unknown", g.Name)
	case g.Memory < p.ModelGPUMemory:
		return false, fmt.Sprintf("the %s has %s of its own memory, %s at least", g.Name, size(g.Memory),
			size(p.ModelGPUMemory))
	}
	driver := g.Driver
	if g.DriverVersion != "" {
		driver += " " + g.DriverVersion
	}
	return true, fmt.Sprintf("on the %s, with %s of its own memory, driver %s", g.Name, size(g.Memory), driver)
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

// Available is the memory available to new programs now, in bytes; 0 when unknown.
func (m *Monitor) Available() uint64 {
	s := m.Snapshot()
	if s.MemoryTotal == 0 {
		return 0
	}
	return s.MemoryAvailable
}

// Pressure says why the machine is under pressure now, or "".
func (m *Monitor) Pressure() string { return m.policy.Pressure(m.Snapshot()) }

// Policy is the monitor's policy.
func (m *Monitor) Policy() Policy { return m.policy }
