package machine

import (
	"fmt"
	"os"
	"runtime"
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
	if _, err := ReadWorker(os.Getpid()); (err != nil) != (NotMeasured != "") {
		t.Errorf("ReadWorker: %v, NotMeasured %q", err, NotMeasured)
	}
	if (NotMeasured == "") != (runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		t.Errorf("on %s, NotMeasured is %q", runtime.GOOS, NotMeasured)
	}
}
