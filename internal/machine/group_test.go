package machine

import (
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"testing"
	"testing/fstest"
	"time"
)

// stat is a /proc/<pid>/stat line: its parent, its group, its CPU ticks (utime stime cutime cstime) and its
// resident pages.
func stat(pid int, comm string, ppid, pgrp int, ticks [4]int, rss int) *fstest.MapFile {
	line := fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194304 100 0 0 0 %d %d %d %d 20 0 4 0 12345 1000000 %d "+
		"18446744073709551615\n", pid, comm, ppid, pgrp, pgrp, ticks[0], ticks[1], ticks[2], ticks[3], rss)
	return &fstest.MapFile{Data: []byte(line)}
}

// TestReadGroup reads a worker from a simulated /proc: its process group, a tool it started in a group of its own
// and that tool's child, but neither Djinn above it nor another program.
func TestReadGroup(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/50/stat":     stat(50, "djinn", 1, 50, [4]int{900, 100, 0, 0}, 10000),
		"proc/100/stat":    stat(100, "claude", 50, 100, [4]int{150, 50, 100, 0}, 1000),
		"proc/101/stat":    stat(101, "node (worker) x", 100, 100, [4]int{20, 10, 0, 0}, 300),
		"proc/102/stat":    stat(102, "bash", 101, 102, [4]int{1, 1, 0, 0}, 100),
		"proc/103/stat":    stat(103, "go", 102, 102, [4]int{300, 98, 0, 0}, 2000),
		"proc/200/stat":    stat(200, "other", 1, 200, [4]int{5000, 0, 0, 0}, 9000),
		"proc/self/stat":   stat(200, "other", 1, 200, [4]int{5000, 0, 0, 0}, 9000),
		"proc/300/cmdline": {Data: []byte("ended")}, // A process that ended while /proc was read.
		"proc/meminfo":     {Data: []byte(meminfo)},
	}
	g, err := ReadGroup(fsys, 100)
	if err != nil {
		t.Fatal(err)
	}
	page := uint64(os.Getpagesize())
	want := Group{
		Processes: 4, CPU: (300 + 30 + 2 + 398) * 10 * time.Millisecond, Memory: (1000 + 300 + 100 + 2000) * page,
	}
	if g != want {
		t.Errorf("group 100: %+v, want %+v", g, want)
	}
	if g, err := ReadGroup(fsys, 999); err != nil || g != (Group{}) {
		t.Errorf("a worker gone: %+v, %v", g, err)
	}
	fsys["proc/104/stat"] = &fstest.MapFile{Data: []byte("104 (broken")}
	if _, err := ReadGroup(fsys, 100); err == nil {
		t.Error("a stat line without its fields read")
	}
}

// TestGroupPIDs finds all process IDs belonging to a process group and its children.
func TestGroupPIDs(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/50/stat":  stat(50, "djinn", 1, 50, [4]int{900, 100, 0, 0}, 10000),
		"proc/100/stat": stat(100, "claude", 50, 100, [4]int{150, 50, 100, 0}, 1000),
		"proc/101/stat": stat(101, "node", 100, 100, [4]int{20, 10, 0, 0}, 300),
		"proc/102/stat": stat(102, "bash", 101, 102, [4]int{1, 1, 0, 0}, 100),
		"proc/103/stat": stat(103, "go", 102, 102, [4]int{300, 98, 0, 0}, 2000),
		"proc/200/stat": stat(200, "other", 1, 200, [4]int{5000, 0, 0, 0}, 9000),
	}
	procs, err := readProcs(fsys)
	if err != nil {
		t.Fatal(err)
	}
	pids := GroupPIDs(procs, 100)
	slices.Sort(pids)
	want := []int{100, 101, 102, 103}
	if !slices.Equal(pids, want) {
		t.Errorf("GroupPIDs(100) = %v, want %v", pids, want)
	}
}

// TestParsePS reads ps as macOS and Linux print it.
func TestParsePS(t *testing.T) {
	out := `    1     0     1  12000   1:02.50
  500     1   500   8000   0:01.00
  501   500   500   2048   0:00.25
  502   501   502   1024 1-00:00:01
  600     1   600   4096   9:59.99
`
	g, err := ParsePS([]byte(out), 500)
	if err != nil {
		t.Fatal(err)
	}
	want := Group{Processes: 3, CPU: 86401*time.Second + 1250*time.Millisecond, Memory: (8000 + 2048 + 1024) << 10}
	if g != want {
		t.Errorf("group 500: %+v, want %+v", g, want)
	}
	if _, err := ParsePS([]byte("500 1 500 8000\n"), 500); err == nil {
		t.Error("a line without its CPU time read")
	}
	for in, want := range map[string]time.Duration{
		"0:00.03":    30 * time.Millisecond,
		"12:34.5":    754500 * time.Millisecond,
		"01:02:03":   3723 * time.Second,
		"2-01:00:00": 49 * time.Hour,
	} {
		if got, err := parseCPUTime(in); err != nil || got != want {
			t.Errorf("parseCPUTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"12", "a:b", "1:2:3:4"} {
		if _, err := parseCPUTime(in); err == nil {
			t.Errorf("parseCPUTime(%q) read", in)
		}
	}
}

// TestNotMeasured says, where workers are not measured, why.
func TestNotMeasured(t *testing.T) {
	if _, err := ReadWorker(os.Getpid(), ""); (err != nil) != (NotMeasured != "") {
		t.Errorf("ReadWorker: %v, NotMeasured %q", err, NotMeasured)
	}
	if (NotMeasured == "") != (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		t.Errorf("on %s, NotMeasured is %q", runtime.GOOS, NotMeasured)
	}
}

// Djinn's work is read from the cgroups that hold it alone: every djinn-*.scope of the slice (its workers' and its
// commands', an ended process's time kept), its own cgroup when nothing else runs in it; and, one by one, the
// processes of its work out of them: what a worker started that moved to a scope of its own. The developer's
// browser is not Djinn's (W347).
func TestWork(t *testing.T) {
	const slice = "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice"
	cg := func(fsys fstest.MapFS, unit, procs string, usec int) {
		dir := slice[1:] + "/" + unit
		fsys[dir+"/cgroup.procs"] = &fstest.MapFile{Data: []byte(procs)}
		fsys[dir+"/cpu.stat"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("usage_usec %d\nuser_usec 0\n", usec))}
	}
	fsys := fstest.MapFS{"proc/100/cgroup": {Data: []byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-djinn-90.scope\n")}}
	// The launcher, djinn up and its window; 700 a process of its window's whose parent ended, adopted by systemd; 800
	// started after the listing.
	cg(fsys, "app-gnome-djinn-90.scope", "90\n100\n110\n700\n800\n", 9_000_000)
	cg(fsys, "djinn-W1-0a1b2c3d.scope", "200\n300\n", 20_000_000)            // a worker and its loop, adopted by systemd
	cg(fsys, "djinn-gate-test-1a2b3c4d.scope", "500\n", 50_000_000)          // a push check: its ended test binaries too
	cg(fsys, "app-org.chromium.Chromium-301.scope", "301\n302\n", 7_000_000) // the browser the worker's e2e started
	cg(fsys, "app-firefox-400.scope", "400\n", 11_000_000)                   // the developer's browser
	procs := []proc{
		{pid: 90, ppid: 1, pgrp: 90, own: time.Second, memory: 1},
		{pid: 100, ppid: 90, pgrp: 100, own: time.Second, memory: 2},
		{pid: 110, ppid: 100, pgrp: 100, own: time.Second, memory: 4},
		{pid: 200, ppid: 100, pgrp: 200, own: 2 * time.Second, memory: 8},
		{pid: 300, ppid: 8, pgrp: 300, own: 3 * time.Second, memory: 16},
		{pid: 301, ppid: 300, pgrp: 300, own: 5 * time.Second, memory: 32},
		{pid: 302, ppid: 301, pgrp: 300, own: 7 * time.Second, memory: 64},
		{pid: 400, ppid: 8, pgrp: 400, own: 11 * time.Second, memory: 128},
		{pid: 500, ppid: 100, pgrp: 500, own: 13 * time.Second, memory: 256},
		{pid: 700, ppid: 8, pgrp: 110, own: 19 * time.Second, memory: 1024},
	}
	w := work(fsys, procs, 100, slice, []string{slice + "/djinn-W1-0a1b2c3d.scope"})
	want := map[string]time.Duration{
		slice + "/app-gnome-djinn-90.scope":       9 * time.Second,
		slice + "/djinn-W1-0a1b2c3d.scope":        20 * time.Second,
		slice + "/djinn-gate-test-1a2b3c4d.scope": 50 * time.Second,
	}
	if !maps.Equal(w.Cgroups, want) {
		t.Errorf("cgroups %v, want %v", w.Cgroups, want)
	}
	if want := map[int]time.Duration{301: 5 * time.Second, 302: 7 * time.Second}; !maps.Equal(w.Procs, want) {
		t.Errorf("processes %v, want %v", w.Procs, want)
	}
	if w.Memory != 2+4+8+16+32+64+256 {
		t.Errorf("memory %d, want all but the launcher's and the developer's browser", w.Memory)
	}

	// A terminal's cgroup, shared with the developer's editor: Djinn's processes there are read one by one.
	cg(fsys, "app-gnome-djinn-90.scope", "90\n100\n110\n600\n", 9_000_000)
	procs = append(procs, proc{pid: 600, ppid: 90, pgrp: 600, own: 17 * time.Second, memory: 512})
	w = work(fsys, procs, 100, slice, nil)
	if _, ok := w.Cgroups[slice+"/app-gnome-djinn-90.scope"]; ok || len(w.Cgroups) != 2 {
		t.Errorf("cgroups %v, want the two scopes alone", w.Cgroups)
	}
	if w.Procs[100] != time.Second || w.Procs[110] != time.Second || len(w.Procs) != 4 {
		t.Errorf("processes %v, want djinn up, its window, and the browser's two", w.Procs)
	}

	// No cgroup at all (macOS): its tree, process by process.
	w = work(fstest.MapFS{}, procs, 100, "", nil)
	if len(w.Cgroups) != 0 || len(w.Procs) != 4 || w.Procs[500] != 13*time.Second {
		t.Errorf("without cgroups: %v %v, want djinn up, its window, the worker and the check", w.Cgroups, w.Procs)
	}
}
