package window

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ProcRoot is where the processes are read: /proc. Tests replace it.
var ProcRoot = "/proc"

// clockTicks is the kernel's clock tick, the unit of a process's CPU time in /proc: 100 per second on Linux.
const clockTicks = 100

// Process is a process of WebKit, as the log of a silent page names it.
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

// Describe names p for the log: its PID, name, state, memory, CPU time, and its CPU over sample when before holds it:
// "pid 4242 WebKitWebProces S rss 155 MB cpu 1.2 s, 0 % over 500ms".
func (p Process) Describe(before []Process, sample time.Duration) string {
	text := fmt.Sprintf("pid %d %s %s rss %d MB cpu %.1f s", p.PID, p.Name, p.State, p.RSS>>20, p.CPU.Seconds())
	for _, b := range before {
		if b.PID == p.PID && sample > 0 {
			text += fmt.Sprintf(", %d %% over %s", int(100*(p.CPU-b.CPU)/sample), sample)
		}
	}
	return text
}
