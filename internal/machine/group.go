package machine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Group is what a worker uses at one time: its process group, and every process it started, even one that left the
// group (a tool an agent starts in a session of its own).
type Group struct {
	// Processes running.
	Processes int
	// CPU time, user and system, of the processes running and of the children they waited for.
	CPU time.Duration
	// Resident memory, summed over the processes: a page they share counts once per process. Read from a cgroup,
	// what the kernel charges it (ReadCgroup).
	Memory uint64
	// Peak is the most memory the worker's cgroup held since it started; 0 without a cgroup.
	Peak uint64
}

// proc is one process, as a listing gives it.
type proc struct {
	pid, ppid, pgrp int
	cpu             time.Duration // its own and that of the children it waited for
	own             time.Duration // its own alone
	memory          uint64
}

// clockTicks is the unit of the CPU times of /proc/<pid>/stat: USER_HZ, 100 on every Linux Djinn runs on.
const clockTicks = 100

// ReadGroup reads, from a Linux /proc, what the worker whose process leads group pid uses; fsys is the root of the
// file system. No process left: a zero Group.
func ReadGroup(fsys fs.FS, pid int) (Group, error) {
	procs, err := readProcs(fsys)
	return sumGroup(procs, pid), err
}

// readProcs lists the processes of a Linux /proc. A process that ends while it is read is left out.
func readProcs(fsys fs.FS) ([]proc, error) {
	entries, err := fs.ReadDir(fsys, "proc")
	if err != nil {
		return nil, err
	}
	page := uint64(os.Getpagesize())
	var procs []proc
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := fs.ReadFile(fsys, "proc/"+e.Name()+"/stat")
		if err != nil {
			continue // Ended meanwhile.
		}
		p, err := parseStat(b, page)
		if err != nil {
			return nil, fmt.Errorf("proc/%s/stat: %w", e.Name(), err)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// parseStat reads a /proc/<pid>/stat line. The command name, between parentheses, may hold spaces and parentheses:
// the fields that follow start after the last ")".
func parseStat(b []byte, page uint64) (proc, error) {
	s := string(b)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return proc{}, errors.New("no command name")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return proc{}, err
	}
	// From the state on: state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime
	// cstime priority nice threads itrealvalue starttime vsize rss.
	f := strings.Fields(s[end+1:])
	if len(f) < 22 {
		return proc{}, fmt.Errorf("%d fields", len(f))
	}
	n := func(i int) (int64, error) { return strconv.ParseInt(f[i], 10, 64) }
	p := proc{pid: pid}
	var ppid, pgrp, rss int64
	if ppid, err = n(1); err != nil {
		return proc{}, err
	}
	if pgrp, err = n(2); err != nil {
		return proc{}, err
	}
	var ticks, own int64
	for _, i := range []int{11, 12, 13, 14} {
		t, err := n(i)
		if err != nil {
			return proc{}, err
		}
		ticks += t
		if i < 13 {
			own += t
		}
	}
	if rss, err = n(21); err != nil {
		return proc{}, err
	}
	p.ppid, p.pgrp = int(ppid), int(pgrp)
	p.cpu = time.Duration(ticks) * time.Second / clockTicks
	p.own = time.Duration(own) * time.Second / clockTicks
	if rss > 0 {
		p.memory = uint64(rss) * page
	}
	return p, nil
}

// PSArgs are the arguments of ps that ParsePS reads: every process, its parent, its group, its resident memory in
// KiB and its CPU time.
var PSArgs = []string{"-A", "-o", "pid=,ppid=,pgid=,rss=,time="}

// ParsePS reads, from the output of ps with PSArgs, what the worker whose process leads group pid uses.
func ParsePS(out []byte, pid int) (Group, error) {
	procs, err := parsePS(out)
	return sumGroup(procs, pid), err
}

// parsePS lists the processes of the output of ps with PSArgs.
func parsePS(out []byte) ([]proc, error) {
	var procs []proc
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 5 {
			return nil, fmt.Errorf("ps: %q", sc.Text())
		}
		var n [4]int
		for i := range n {
			v, err := strconv.Atoi(f[i])
			if err != nil {
				return nil, fmt.Errorf("ps: %q: %w", sc.Text(), err)
			}
			n[i] = v
		}
		cpu, err := parseCPUTime(f[4])
		if err != nil {
			return nil, fmt.Errorf("ps: %q: %w", sc.Text(), err)
		}
		procs = append(procs, proc{pid: n[0], ppid: n[1], pgrp: n[2], memory: uint64(max(n[3], 0)) << 10, cpu: cpu, own: cpu})
	}
	return procs, sc.Err()
}

// parseCPUTime reads a CPU time as ps prints it: [[dd-]hh:]mm:ss[.ss]; macOS gives mm:ss.ss, Linux [dd-]hh:mm:ss.
func parseCPUTime(s string) (time.Duration, error) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		var err error
		if days, err = strconv.Atoi(d); err != nil {
			return 0, err
		}
		s = rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("CPU time %q", s)
	}
	secs, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	total := float64(days) * 86400
	for i, unit := range []float64{60, 3600}[:len(parts)-1] {
		v, err := strconv.Atoi(parts[len(parts)-2-i])
		if err != nil {
			return 0, err
		}
		total += float64(v) * unit
	}
	return time.Duration((total + secs) * float64(time.Second)), nil
}

// sumGroup sums the processes of group pid and their descendants.
func sumGroup(procs []proc, pid int) Group {
	parent := make(map[int]int, len(procs))
	in := map[int]bool{}
	for _, p := range procs {
		parent[p.pid] = p.ppid
		if p.pgrp == pid || p.pid == pid {
			in[p.pid] = true
		}
	}
	// member says whether p descends from a process of the group, remembering the answer along the way.
	var member func(p int, depth int) bool
	member = func(p int, depth int) bool {
		if v, ok := in[p]; ok {
			return v
		}
		pp, ok := parent[p]
		v := ok && pp > 1 && pp != p && depth < 64 && member(pp, depth+1)
		in[p] = v
		return v
	}
	var g Group
	for _, p := range procs {
		if member(p.pid, 0) {
			g.Processes++
			g.CPU += p.cpu
			g.Memory += p.memory
		}
	}
	return g
}

// GroupPIDs returns the list of process IDs belonging to the process group pid or its descendants.
func GroupPIDs(procs []proc, pid int) []int {
	parent := make(map[int]int, len(procs))
	in := map[int]bool{}
	for _, p := range procs {
		parent[p.pid] = p.ppid
		if p.pgrp == pid || p.pid == pid {
			in[p.pid] = true
		}
	}
	var member func(p int, depth int) bool
	member = func(p int, depth int) bool {
		if v, ok := in[p]; ok {
			return v
		}
		pp, ok := parent[p]
		v := ok && pp > 1 && pp != p && depth < 64 && member(pp, depth+1)
		in[p] = v
		return v
	}
	var pids []int
	for _, p := range procs {
		if member(p.pid, 0) {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

// WorkerPIDs returns the list of process IDs belonging to a worker (from its cgroup if available, or process tree).
func WorkerPIDs(pid int, cgroup string) ([]int, error) {
	if cgroup != "" {
		if pids, err := CgroupPIDs(cgroup); err == nil && len(pids) > 0 {
			return pids, nil
		}
	}
	if pid <= 0 {
		return nil, nil
	}
	procs, err := listProcs()
	if err != nil {
		return []int{pid}, err
	}
	pids := GroupPIDs(procs, pid)
	if len(pids) == 0 {
		return []int{pid}, nil
	}
	return pids, nil
}

// listing is the last list of the processes, shared by the workers' readings.
var listing struct {
	mu    sync.Mutex
	at    time.Time
	procs []proc
	err   error
}

// ReadWorker reads what the worker whose process leads group pid uses: from its cgroup when it runs in one
// (Scope.Cgroup, ReadCgroup), else from its processes, listed at most once a second for every worker. Where workers
// are not measured (NotMeasured), it fails. A worker not listed yet, or listed at its exec, before it touched a page,
// reads as a zero Group: no reading.
func ReadWorker(pid int, cgroup string) (Group, error) {
	if NotMeasured != "" {
		return Group{}, errors.New(NotMeasured)
	}
	if cgroup != "" {
		// Not yet made, or without the memory controller: read as without a scope.
		if g, err := ReadCgroup(os.DirFS("/"), cgroup); err == nil && g.Processes > 0 {
			return g, nil
		}
	}
	listing.mu.Lock()
	defer listing.mu.Unlock()
	if now := time.Now(); now.Sub(listing.at) >= time.Second {
		listing.procs, listing.err = listProcs()
		listing.at = now
	}
	g := sumGroup(listing.procs, pid)
	if g.Memory == 0 {
		// Linux shows no resident memory for a process that just exec'd: it counts the pages per CPU and folds them
		// only past a batch. Kept, that reading showed the worker at 0 bytes until the next listing (PR #3's CI).
		return Group{}, listing.err
	}
	return g, listing.err
}

// Work is what Djinn's work used, read at once (ReadWork): counters that only grow while what they count lives, so
// that a process or a scope that ends never takes back what it used (W347: when a test run ended, the sum of Djinn's
// processes fell under its last reading, the Djinn line read 0 % and the next one too little).
type Work struct {
	// Cgroups is the CPU time of each cgroup that holds Djinn's work alone, by folder: its workers' and its own
	// commands' scopes (djinn-*.scope), and its own cgroup when nothing else runs in it. A cgroup counts the
	// processes it held that ended too: a test binary that exits leaves its time behind.
	Cgroups map[string]time.Duration
	// Procs is the CPU time of each process of Djinn's work in none of those cgroups, its own alone: its tree where
	// its cgroup is shared, and what its work started that moved to a cgroup of its own (a headless browser, W253).
	Procs map[int]time.Duration
	// Memory is the resident memory of Djinn's tree, of the processes in its scopes and of what they started.
	Memory uint64
}

// ReadWork reads what Djinn's work uses, pid being Djinn's process: the cgroups (v2) that hold it alone, every
// djinn-*.scope under slice (Scopes.Slice: any Djinn's workers and commands, an end-to-end test's too) and in cgroups
// (the scopes of its running workers, Scope.Cgroup), and the processes of its work out of them; from the same listing
// as ReadWorker. Without cgroups, its tree's processes alone.
func ReadWork(pid int, slice string, cgroups []string) (Work, error) {
	if NotMeasured != "" {
		return Work{}, errors.New(NotMeasured)
	}
	listing.mu.Lock()
	defer listing.mu.Unlock()
	if now := time.Now(); now.Sub(listing.at) >= time.Second {
		listing.procs, listing.err = listProcs()
		listing.at = now
	}
	return work(os.DirFS("/"), listing.procs, pid, slice, cgroups), listing.err
}

// work reads, from the file system fsys and the processes procs, what the work of Djinn's process pid uses: ReadWork.
func work(fsys fs.FS, procs []proc, pid int, slice string, cgroups []string) Work {
	w := Work{Cgroups: map[string]time.Duration{}, Procs: map[int]time.Duration{}}
	dirs := slices.Clone(cgroups)
	if slice != "" {
		entries, _ := fs.ReadDir(fsys, strings.TrimPrefix(slice, "/")) // No slice: no scope.
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "djinn-") && strings.HasSuffix(e.Name(), ".scope") {
				dirs = append(dirs, path.Join(slice, e.Name()))
			}
		}
	}
	counted := map[int]bool{} // the processes a cgroup counts
	scoped := map[int]bool{}  // those in Djinn's scopes, whose children are Djinn's wherever they go
	for _, dir := range dirs {
		if _, ok := w.Cgroups[dir]; ok {
			continue
		}
		pids, cpu, err := cgroupCPU(fsys, dir)
		if err != nil {
			continue // Not made yet, or ended meanwhile.
		}
		w.Cgroups[dir] = cpu
		for _, p := range pids {
			counted[p], scoped[p] = true, true
		}
	}
	tree := map[int]bool{}
	for _, p := range GroupPIDs(procs, pid) {
		tree[p] = true
	}
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.pid] = p.ppid
	}
	if own := ownCgroup(fsys, pid); own != "" {
		if _, ok := w.Cgroups[own]; !ok {
			if pids, cpu, err := cgroupCPU(fsys, own); err == nil && alone(pids, tree, parent, pid) {
				w.Cgroups[own] = cpu
				for _, p := range pids {
					counted[p] = true
				}
			}
		}
	}
	// Djinn's work: its tree, and what is in its scopes or descends from them, out of its tree.
	known := map[int]bool{}
	var mine func(p, depth int) bool
	mine = func(p, depth int) bool {
		if v, ok := known[p]; ok {
			return v
		}
		pp, ok := parent[p]
		v := tree[p] || scoped[p] || ok && pp > 1 && pp != p && depth < 64 && mine(pp, depth+1)
		known[p] = v
		return v
	}
	for _, p := range procs {
		if !mine(p.pid, 0) {
			continue
		}
		w.Memory += p.memory
		if !counted[p.pid] {
			w.Procs[p.pid] = p.own
		}
	}
	return w
}

// alone says whether the processes pids of Djinn's cgroup are all Djinn's: none descends, within the cgroup, from what
// started Djinn there (the shell of a terminal tab, which ran the developer's editor too). A process in its tree, one
// started after the listing, and one whose parent ended (adopted out of the cgroup, by systemd) are Djinn's: the
// cgroup reads as shared only while a program beside Djinn runs in it, and Djinn's processes are then counted one by
// one.
func alone(pids []int, tree map[int]bool, parent map[int]int, pid int) bool {
	in := make(map[int]bool, len(pids))
	for _, p := range pids {
		in[p] = true
	}
	above := map[int]bool{} // what started Djinn, in its cgroup
	for p, depth := parent[pid], 0; in[p] && depth < 64; p, depth = parent[p], depth+1 {
		above[p] = true
	}
	for _, p := range pids {
		if tree[p] || above[p] {
			continue
		}
		for q, depth := p, 0; in[q] && !tree[q] && depth < 64; q, depth = parent[q], depth+1 {
			if above[q] {
				return false
			}
		}
	}
	return len(pids) > 0
}
