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

// workerProc is a simulated /proc: Djinn (50), a worker leading group 100, a tool it started in a group of its own
// (102) and that tool's child, and another program (200).
func workerProc() fstest.MapFS {
	return fstest.MapFS{
		"proc/50/stat":  stat(50, "djinn", 1, 50, [4]int{900, 100, 0, 0}, 10000),
		"proc/100/stat": stat(100, "claude", 50, 100, [4]int{150, 50, 100, 0}, 1000),
		"proc/101/stat": stat(101, "node (worker) x", 100, 100, [4]int{20, 10, 0, 0}, 300),
		"proc/102/stat": stat(102, "bash", 101, 102, [4]int{1, 1, 0, 0}, 100),
		"proc/103/stat": stat(103, "go", 102, 102, [4]int{300, 98, 0, 0}, 2000),
		"proc/200/stat": stat(200, "other", 1, 200, [4]int{5000, 0, 0, 0}, 9000),
	}
}

// TestReadGroup reads a worker from a simulated /proc: its process group, a tool it started in a group of its own
// and that tool's child, but neither Djinn above it nor another program.
func TestReadGroup(t *testing.T) {
	t.Parallel()
	page := uint64(os.Getpagesize())
	fsys := workerProc()
	fsys["proc/self/stat"] = stat(200, "other", 1, 200, [4]int{5000, 0, 0, 0}, 9000)
	fsys["proc/300/cmdline"] = &fstest.MapFile{Data: []byte("ended")} // A process that ended while /proc was read.
	fsys["proc/meminfo"] = &fstest.MapFile{Data: []byte(meminfo)}
	broken := workerProc()
	broken["proc/104/stat"] = &fstest.MapFile{Data: []byte("104 (broken")}
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		pid     int
		want    Group
		wantErr bool
	}{
		{name: "worker", fsys: fsys, pid: 100, want: Group{
			Processes: 4, CPU: (300 + 30 + 2 + 398) * 10 * time.Millisecond, Memory: (1000 + 300 + 100 + 2000) * page,
		}},
		{name: "gone", fsys: fsys, pid: 999},
		// A stat line without its fields.
		{name: "broken stat", fsys: broken, pid: 100, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, err := ReadGroup(c.fsys, c.pid)
			if (err != nil) != c.wantErr || !c.wantErr && g != c.want {
				t.Errorf("ReadGroup(%d) = %+v, %v; want %+v, error %v", c.pid, g, err, c.want, c.wantErr)
			}
		})
	}
}

// TestGroupPIDs finds all process IDs belonging to a process group and its children.
func TestGroupPIDs(t *testing.T) {
	t.Parallel()
	procs, err := readProcs(workerProc())
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

// TestWorkerPIDs: a worker's processes are its cgroup's when it has one, else its tree's; without a process, none.
func TestWorkerPIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		pid    int
		cgroup func(t *testing.T) string
		want   []int
	}{
		{name: "cgroup", pid: 1, cgroup: cgroupDir, want: []int{12, 34}},
		{name: "no process", pid: 0, cgroup: func(*testing.T) string { return "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, err := WorkerPIDs(c.pid, c.cgroup(t)); err != nil || !slices.Equal(got, c.want) {
				t.Errorf("WorkerPIDs = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

// TestParsePS reads ps as macOS and Linux print it.
func TestParsePS(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		out     string
		want    Group
		wantErr bool
	}{
		{name: "group", out: `    1     0     1  12000   1:02.50
  500     1   500   8000   0:01.00
  501   500   500   2048   0:00.25
  502   501   502   1024 1-00:00:01
  600     1   600   4096   9:59.99
`, want: Group{Processes: 3, CPU: 86401*time.Second + 1250*time.Millisecond, Memory: (8000 + 2048 + 1024) << 10}},
		{name: "a line without its CPU time", out: "500 1 500 8000\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, err := ParsePS([]byte(c.out), 500)
			if (err != nil) != c.wantErr || !c.wantErr && g != c.want {
				t.Errorf("ParsePS = %+v, %v; want %+v, error %v", g, err, c.want, c.wantErr)
			}
		})
	}
}

// TestParseCPUTime reads the CPU times ps prints, from seconds to days.
func TestParseCPUTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "0:00.03", want: 30 * time.Millisecond},
		{in: "12:34.5", want: 754500 * time.Millisecond},
		{in: "01:02:03", want: 3723 * time.Second},
		{in: "2-01:00:00", want: 49 * time.Hour},
		{in: "12", wantErr: true},
		{in: "a:b", wantErr: true},
		{in: "1:2:3:4", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got, err := parseCPUTime(c.in); (err != nil) != c.wantErr || got != c.want {
				t.Errorf("parseCPUTime(%q) = %v, %v; want %v, error %v", c.in, got, err, c.want, c.wantErr)
			}
		})
	}
}

// TestNotMeasured says, where workers are not measured, why.
func TestNotMeasured(t *testing.T) {
	if _, err := ReadWorker(os.Getpid(), ""); (err != nil) != (NotMeasured != "") {
		t.Errorf("ReadWorker: %v, NotMeasured %q", err, NotMeasured)
	}
	if _, err := ReadWork(os.Getpid(), "", nil); (err != nil) != (NotMeasured != "") {
		t.Errorf("ReadWork: %v, NotMeasured %q", err, NotMeasured)
	}
	if (NotMeasured == "") != (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		t.Errorf("on %s, NotMeasured is %q", runtime.GOOS, NotMeasured)
	}
}

// workSlice is the slice of the scopes in the fixtures of TestWork.
const workSlice = "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice"

// workCgroup adds to fsys a cgroup of workSlice: its processes and its CPU time.
func workCgroup(fsys fstest.MapFS, unit, procs string, usec int) {
	dir := workSlice[1:] + "/" + unit
	fsys[dir+"/cgroup.procs"] = &fstest.MapFile{Data: []byte(procs)}
	fsys[dir+"/cpu.stat"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("usage_usec %d\nuser_usec 0\n", usec))}
}

// workFixture is djinn up (100) started by a launcher (90) in a cgroup of its own, with its window (110), a worker in
// its scope, a push check, the browser the worker's e2e started and the developer's browser.
func workFixture() (fstest.MapFS, []proc) {
	fsys := fstest.MapFS{"proc/100/cgroup": {Data: []byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome-djinn-90.scope\n")}}
	// The launcher, djinn up and its window; 700 a process of its window's whose parent ended, adopted by systemd; 800
	// started after the listing.
	workCgroup(fsys, "app-gnome-djinn-90.scope", "90\n100\n110\n700\n800\n", 9_000_000)
	workCgroup(fsys, "djinn-W1-0a1b2c3d.scope", "200\n300\n", 20_000_000)            // a worker and its loop, adopted by systemd
	workCgroup(fsys, "djinn-gate-test-1a2b3c4d.scope", "500\n", 50_000_000)          // a push check: its ended test binaries too
	workCgroup(fsys, "app-org.chromium.Chromium-301.scope", "301\n302\n", 7_000_000) // the browser the worker's e2e started
	workCgroup(fsys, "app-firefox-400.scope", "400\n", 11_000_000)                   // the developer's browser
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
	return fsys, procs
}

// Djinn's work is read from the cgroups that hold it alone: every djinn-*.scope of the slice (its workers' and its
// commands', an ended process's time kept), its own cgroup when nothing else runs in it; and, one by one, the
// processes of its work out of them: what a worker started that moved to a scope of its own. The developer's
// browser is not Djinn's (W347).
func TestWork(t *testing.T) {
	t.Parallel()
	const slice = workSlice
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"own cgroup alone", func(t *testing.T) {
			fsys, procs := workFixture()
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
		}},
		// A terminal's cgroup, shared with the developer's editor: Djinn's processes there are read one by one.
		{"shared cgroup", func(t *testing.T) {
			fsys, procs := workFixture()
			workCgroup(fsys, "app-gnome-djinn-90.scope", "90\n100\n110\n600\n", 9_000_000)
			procs = append(procs, proc{pid: 600, ppid: 90, pgrp: 600, own: 17 * time.Second, memory: 512})
			w := work(fsys, procs, 100, slice, nil)
			if _, ok := w.Cgroups[slice+"/app-gnome-djinn-90.scope"]; ok || len(w.Cgroups) != 2 {
				t.Errorf("cgroups %v, want the two scopes alone", w.Cgroups)
			}
			if w.Procs[100] != time.Second || w.Procs[110] != time.Second || len(w.Procs) != 4 {
				t.Errorf("processes %v, want djinn up, its window, and the browser's two", w.Procs)
			}
		}},
		// No cgroup at all (macOS): its tree, process by process.
		{"no cgroup", func(t *testing.T) {
			_, procs := workFixture()
			procs = append(procs, proc{pid: 600, ppid: 90, pgrp: 600, own: 17 * time.Second, memory: 512})
			w := work(fstest.MapFS{}, procs, 100, "", nil)
			if len(w.Cgroups) != 0 || len(w.Procs) != 4 || w.Procs[500] != 13*time.Second {
				t.Errorf("without cgroups: %v %v, want djinn up, its window, the worker and the check", w.Cgroups, w.Procs)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}
