package machine

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ErrNoScope is why workers do not run in a systemd scope of their own here: ProbeScopes wraps it, and Djinn runs
// them in their process group.
var ErrNoScope = errors.New("no systemd scope per worker")

// ErrNoCPULimit is why the CPU of a worker cannot be capped here: a note of ProbeScopes starts with it.
var ErrNoCPULimit = errors.New("CPU limits per worker are off")

// ErrNoMemoryLimit is why the memory of a worker cannot be capped here: a note of ProbeScopes starts with it.
var ErrNoMemoryLimit = errors.New("memory limits per worker are off")

// Scopes start each worker in a systemd user scope of its own, without root: a cgroup that holds every process the
// worker starts, even one that leaves its process group, so that stopping, pausing and measuring it take the whole
// tree. ProbeScopes makes them.
type Scopes struct {
	// CPU caps each worker at this percent of one core (CPUQuota, djinn up --worker-cpu); 0: uncapped.
	CPU int
	// Memory caps each worker's memory at this many bytes (MemoryMax, Policy.WorkerMemory); 0: no ceiling.
	Memory uint64
	// slice is the cgroup, under /sys/fs/cgroup, that holds the scopes: the probe's parent.
	slice string
}

// Scope is the scope of one process a worker starts: its unit, the command it runs under, its cgroup.
type Scope struct {
	// Unit names it: djinn-<task code>-<uuid8>.scope.
	Unit string
	// Prefix runs a command in it, the command appended. The scope keeps the process: same PID, same process group,
	// same streams.
	Prefix []string
	// Cgroup is its folder, under /sys/fs/cgroup; it exists from the time systemd-run makes the scope until its last
	// process ends. Empty when unknown.
	Cgroup string
}

// cgroupRoot is where Linux mounts the cgroup tree (v2).
const cgroupRoot = "/sys/fs/cgroup"

// New is the scope of a process of the worker name (a task's code): a unit of its own each time, since systemd
// keeps the name of an ended scope a little while.
func (s *Scopes) New(name string) Scope {
	unit := scopeUnit(name, uuid.NewString()[:8])
	sc := Scope{Unit: unit, Prefix: s.prefix(unit)}
	if s.slice != "" {
		sc.Cgroup = path.Join(cgroupRoot, s.slice, unit)
	}
	return sc
}

// scopeUnit names the scope of a worker: djinn-<name>-<uuid8>.scope, the name kept to what a unit name holds.
func scopeUnit(name, uuid8 string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, name)
	return "djinn-" + name + "-" + uuid8 + ".scope"
}

// prefix is the command that runs a process in the scope unit, with the caps asked.
func (s *Scopes) prefix(unit string) []string {
	p := []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + unit}
	if s.CPU > 0 {
		p = append(p, "-p", "CPUQuota="+strconv.Itoa(s.CPU)+"%")
	}
	if s.Memory > 0 {
		p = append(p, "-p", "MemoryMax="+strconv.FormatUint(s.Memory, 10))
	}
	return append(p, "--")
}

// probe prints, from a scope started as a worker's would be, its cgroup, then its cpu.max and its memory.max, or
// "none" for a file that does not exist: without the controller, systemd accepts the cap and silently holds nothing.
const probe = `d="$(sed -n 's/^0:://p' /proc/self/cgroup)"; echo "$d"; ` +
	`cat "/sys/fs/cgroup$d/cpu.max" 2>/dev/null || echo none; cat "/sys/fs/cgroup$d/memory.max" 2>/dev/null || echo none`

// readProbe reads what the probe printed into s: the slice that holds the scopes, and the caps that hold, the others
// set to 0. Each note says why a cap does not hold.
func readProbe(out string, s *Scopes) (notes []string, err error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "/") || !strings.HasSuffix(lines[0], ".scope") {
		return nil, fmt.Errorf("%w: the probe scope printed %q", ErrNoScope, strings.TrimSpace(out))
	}
	s.slice = path.Dir(strings.TrimPrefix(lines[0], "/"))
	if s.CPU > 0 {
		if err := checkQuota(lines[1], s.CPU); err != nil {
			notes, s.CPU = append(notes, err.Error()), 0
		}
	}
	if s.Memory > 0 {
		if err := checkMemoryMax(lines[2], s.Memory); err != nil {
			notes, s.Memory = append(notes, err.Error()), 0
		}
	}
	return notes, nil
}

// delegate is what an administrator does, once, for systemd to give a user the controllers.
const delegate = "an administrator adds Delegate=cpu cpuset io memory pids to user@.service, once"

// checkQuota checks what a scope reads in its cpu.max ("<quota> <period>", in microseconds) against percent of a
// core.
func checkQuota(cpuMax string, percent int) error {
	cpuMax = strings.TrimSpace(cpuMax)
	if cpuMax == "none" {
		return fmt.Errorf("%w: systemd does not give the cpu controller to your user (%s)", ErrNoCPULimit, delegate)
	}
	f := strings.Fields(cpuMax)
	if len(f) != 2 {
		return fmt.Errorf("%w: the scope's cpu.max reads %q", ErrNoCPULimit, cpuMax)
	}
	quota, err1 := strconv.Atoi(f[0])
	period, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || period <= 0 {
		return fmt.Errorf("%w: the scope's CPU is not capped (cpu.max reads %q)", ErrNoCPULimit, cpuMax)
	}
	// systemd rounds the quota to the millisecond of a period of 100 ms.
	if want := percent * period / 100; quota < want-1000 || quota > want+1000 {
		return fmt.Errorf("%w: the scope's quota is %d µs every %d µs, not %d%%", ErrNoCPULimit, quota, period, percent)
	}
	return nil
}

// checkMemoryMax checks what a scope reads in its memory.max (bytes, or "max") against the ceiling asked. The kernel
// rounds it down to a page.
func checkMemoryMax(memoryMax string, ceiling uint64) error {
	memoryMax = strings.TrimSpace(memoryMax)
	if memoryMax == "none" {
		return fmt.Errorf("%w: systemd does not give the memory controller to your user (%s)", ErrNoMemoryLimit, delegate)
	}
	got, err := strconv.ParseUint(memoryMax, 10, 64)
	if err != nil || got > ceiling || got+1<<16 < ceiling {
		return fmt.Errorf("%w: the scope's memory.max reads %q, not %d", ErrNoMemoryLimit, memoryMax, ceiling)
	}
	return nil
}
