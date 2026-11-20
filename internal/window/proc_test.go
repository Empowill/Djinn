package window

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestWebKitProcesses covers WebKitProcesses and Describe: the WebKit processes a parent started, with their state,
// memory and CPU, and their CPU over a sample.
func TestWebKitProcesses(t *testing.T) {
	root := t.TempDir()
	stat := func(pid, ppid int, name string, utime, rssPages int) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 4194560 100 0 0 0 %d 50 0 0 20 0 30 0 1000 600000000 %d 18446744073709551615\n",
			pid, name, ppid, utime, rssPages)
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	page := os.Getpagesize()
	stat(4242, 100, "WebKitWebProces", 70, (155<<20)/page)
	stat(4243, 100, "WebKitNetworkPr", 10, (40<<20)/page)
	stat(4244, 100, "claude", 900, 1000)
	stat(4245, 99, "WebKitWebProces", 0, 1)
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func(old string) func() { return func() { ProcRoot = old } }(ProcRoot))
	ProcRoot = root

	got := WebKitProcesses(100)
	if len(got) != 2 || got[0].PID != 4242 || got[0].Name != "WebKitWebProces" || got[0].State != "S" ||
		got[0].RSS>>20 != 155 || got[0].CPU != 1200*time.Millisecond || got[1].Name != "WebKitNetworkPr" {
		t.Fatalf("processes = %+v", got)
	}
	before := []Process{{PID: 4242, CPU: time.Second}}
	if d := got[0].Describe(before, 500*time.Millisecond); d != "pid 4242 WebKitWebProces S rss 155 MB cpu 1.2 s, 40 % over 500ms" {
		t.Errorf("Describe = %q", d)
	}
	if d := got[1].Describe(before, 500*time.Millisecond); d != "pid 4243 WebKitNetworkPr S rss 40 MB cpu 0.6 s" {
		t.Errorf("Describe without a first read = %q", d)
	}
}

// TestThreads covers what a WebKit process's threads wait on: its main thread first, then each state and wchan by
// count, in the process's description.
func TestThreads(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func(old string) func() { return func() { ProcRoot = old } }(ProcRoot))
	ProcRoot = root
	write := func(path, text string) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("4242/stat", "4242 (WebKitWebProces) S 100 1 1 0 -1 4194560 100 0 0 0 70 50 0 0 20 0 30 0 1000 600000000 10 18446744073709551615\n")
	for tid, state := range map[int]string{4242: "S", 4243: "S", 4244: "S", 4245: "R"} {
		write(fmt.Sprintf("4242/task/%d/stat", tid), fmt.Sprintf("%d (JS thread) %s 100 1 1\n", tid, state))
	}
	write("4242/task/4242/wchan", "do_epoll_wait")
	write("4242/task/4243/wchan", "futex_wait_queue")
	write("4242/task/4244/wchan", "futex_wait_queue")
	write("4242/task/4245/wchan", "0")

	got := WebKitProcesses(100)
	if len(got) != 1 {
		t.Fatalf("processes = %+v", got)
	}
	want := "main S do_epoll_wait, threads 4 (2 S futex_wait_queue, 1 R 0, 1 S do_epoll_wait)"
	if got[0].Threads != want {
		t.Errorf("Threads = %q; want %q", got[0].Threads, want)
	}
	if d := got[0].Describe(nil, 0); d != "pid 4242 WebKitWebProces S rss 0 MB cpu 1.2 s, "+want {
		t.Errorf("Describe = %q", d)
	}
}
