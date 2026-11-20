package window

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProcRoot is where the processes are read: /proc. Tests replace it.
var ProcRoot = "/proc"

// clockTicks is the kernel's clock tick, the unit of a process's CPU time in /proc: 100 per second on Linux.
const clockTicks = 100

// Process is a process of WebKit, as the log of a dead or silent page names it.
type Process struct {
	PID int
	// Name is the kernel's, cut at 15 characters: WebKitWebProces, WebKitNetworkPr.
	Name string
	// State is the kernel's letter: R running, S sleeping, D waiting on a disk, Z a zombie.
	State string
	// RSS is the memory it holds, in bytes.
	RSS uint64
	// CPU is the CPU time it took since it started.
	CPU time.Duration
	// Threads is what its threads wait on, from /proc/<pid>/task: its main thread's state and wchan, then each state
	// and wchan with its count, the most first: "main S do_epoll_wait, threads 24 (18 S futex_wait_queue, 6 S
	// do_epoll_wait)". Empty when it cannot be read.
	Threads string
}

// WebKitProcesses is the processes of WebKit parent started: its web and network processes.
func WebKitProcesses(parent int) []Process {
	entries, err := os.ReadDir(ProcRoot)
	if err != nil {
		return nil
	}
	var found []Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, ppid, ok := readProcess(pid)
		if ok && ppid == parent && strings.HasPrefix(p.Name, "WebKit") {
			p.Threads = threads(pid)
			found = append(found, p)
		}
	}
	return found
}

// readProcess reads /proc/<pid>/stat: the process and its parent's PID.
func readProcess(pid int) (Process, int, bool) {
	b, err := os.ReadFile(filepath.Join(ProcRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return Process{}, 0, false
	}
	s := string(b)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return Process{}, 0, false
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 22 {
		return Process{}, 0, false
	}
	ppid, _ := strconv.Atoi(fields[1])
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	pages, _ := strconv.ParseUint(fields[21], 10, 64)
	return Process{
		PID: pid, Name: s[open+1 : end], State: fields[0],
		RSS: pages * uint64(os.Getpagesize()),
		CPU: time.Duration(utime+stime) * time.Second / clockTicks,
	}, ppid, true
}

// threads describes what the threads of pid wait on (Process.Threads): a JS thread busy shows R, one waiting on the
// network or a lock shows S with its wchan, a disk D.
func threads(pid int) string {
	dir := filepath.Join(ProcRoot, strconv.Itoa(pid), "task")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	main := ""
	counts := map[string]int{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(b)
		end := strings.LastIndexByte(s, ')')
		fields := strings.Fields(s[end+1:])
		if end < 0 || len(fields) == 0 {
			continue
		}
		wchan, _ := os.ReadFile(filepath.Join(dir, e.Name(), "wchan"))
		what := fields[0]
		if w := strings.TrimSpace(string(wchan)); w != "" {
			what += " " + w
		}
		counts[what]++
		if e.Name() == strconv.Itoa(pid) {
			main = what
		}
	}
	if len(counts) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(counts))
	total := 0
	for k, n := range counts {
		kinds = append(kinds, k)
		total += n
	}
	sort.Slice(kinds, func(i, j int) bool {
		if counts[kinds[i]] != counts[kinds[j]] {
			return counts[kinds[i]] > counts[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%d %s", counts[k], k)
	}
	return fmt.Sprintf("main %s, threads %d (%s)", main, total, strings.Join(parts, ", "))
}

// Describe names p for the log: its PID, name, state, memory, CPU time, and its CPU over sample when before holds it:
// "pid 4242 WebKitWebProces S rss 155 MB cpu 1.2 s, 0 % over 500ms", then what its threads wait on.
func (p Process) Describe(before []Process, sample time.Duration) string {
	text := fmt.Sprintf("pid %d %s %s rss %d MB cpu %.1f s", p.PID, p.Name, p.State, p.RSS>>20, p.CPU.Seconds())
	for _, b := range before {
		if b.PID == p.PID && sample > 0 {
			text += fmt.Sprintf(", %d %% over %s", int(100*(p.CPU-b.CPU)/sample), sample)
		}
	}
	if p.Threads != "" {
		text += ", " + p.Threads
	}
	return text
}
