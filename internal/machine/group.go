package machine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	// Resident memory, summed over the processes: a page they share counts once per process.
	Memory uint64
}

// proc is one process, as a listing gives it.
type proc struct {
	pid, ppid, pgrp int
	cpu             time.Duration
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
	var ticks int64
	for _, i := range []int{11, 12, 13, 14} {
		t, err := n(i)
		if err != nil {
			return proc{}, err
		}
		ticks += t
	}
	if rss, err = n(21); err != nil {
		return proc{}, err
	}
	p.ppid, p.pgrp = int(ppid), int(pgrp)
	p.cpu = time.Duration(ticks) * time.Second / clockTicks
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
		procs = append(procs, proc{pid: n[0], ppid: n[1], pgrp: n[2], memory: uint64(max(n[3], 0)) << 10, cpu: cpu})
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

// listing is the last list of the processes, shared by the workers' readings.
var listing struct {
	mu    sync.Mutex
	at    time.Time
	procs []proc
	err   error
}

// ReadWorker reads what the worker whose process leads group pid uses. The processes are listed at most once a
// second, for every worker. Where workers are not measured (NotMeasured), it fails.
func ReadWorker(pid int) (Group, error) {
	if NotMeasured != "" {
		return Group{}, errors.New(NotMeasured)
	}
	listing.mu.Lock()
	defer listing.mu.Unlock()
	if now := time.Now(); now.Sub(listing.at) >= time.Second {
		listing.procs, listing.err = listProcs()
		listing.at = now
	}
	return sumGroup(listing.procs, pid), listing.err
}
