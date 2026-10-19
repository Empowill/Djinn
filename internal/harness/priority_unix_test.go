//go:build !windows

package harness

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// wantNice is the nice level a worker must run at: the test process's own at standard priority, as Djinn's own is a
// worker's; nice 10 at low priority, or the test process's own when it runs lower already. A CI runner does not run
// at nice 0: macOS's ran the tests at -10, and nice -n 5 go test runs them at 5.
func wantNice(t *testing.T, low bool) int {
	t.Helper()
	own, err := niceOf(0)
	if err != nil {
		t.Fatalf("read the test process's nice: %v", err)
	}
	if low {
		return max(own, 10)
	}
	return own
}

// waitNice waits for pid to run at want, and fails with the level it runs at: a low-priority worker starts under nice,
// which sets its level a moment after Djinn started it.
func waitNice(t *testing.T, what string, pid, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		nice, err := niceOf(pid)
		if err != nil {
			t.Fatalf("read the nice of %s (PID %d): %v", what, pid, err)
		}
		if nice == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s (PID %d) nice = %d, want %d", what, pid, nice, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readProcStatNice reads the nice value directly from /proc/<pid>/stat on Linux.
func readProcStatNice(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx == -1 {
		return 0, fmt.Errorf("invalid stat format")
	}
	fields := strings.Fields(s[idx+1:])
	// In /proc/[pid]/stat, field 19 is nice. Fields after ")" start with field 3 (state = fields[0]).
	// So field 19 is fields[19 - 3] = fields[16].
	if len(fields) <= 16 {
		return 0, fmt.Errorf("stat fields too short")
	}
	return strconv.Atoi(fields[16])
}

func checkWorkerPriority(t *testing.T, what string, pid int, low bool) {
	t.Helper()
	want := wantNice(t, low)
	waitNice(t, what, pid, want)
	if runtime.GOOS == "linux" && what == "worker" {
		procNice, err := readProcStatNice(pid)
		if err != nil {
			t.Fatalf("read /proc/%d/stat: %v", pid, err)
		}
		if procNice != want {
			t.Errorf("/proc/%d/stat nice = %d, want %d", pid, procNice, want)
		}
	}
}

// TestWorkerChildPriorityInheritanceUnix verifies that a low-priority worker, and the commands it runs, start at the low
// nice level (nice 10, or Djinn's own when lower) with no setpriority from Djinn once it started: on macOS, PR #2's
// CI saw a low-priority sh, which execs bash at once, run on at nice -10 with its child, Djinn's late setpriority lost
// to the exec. The worker starts under nice instead (lowCommand). It sets the hook every worker reaches: no t.Parallel.
func TestWorkerChildPriorityInheritanceUnix(t *testing.T) {
	orig := setPriorityFn
	t.Cleanup(func() { setPriorityFn = orig })
	var mu sync.Mutex
	var late []int
	setPriorityFn = func(_, who, _ int) error {
		mu.Lock()
		defer mu.Unlock()
		late = append(late, who)
		return nil
	}

	// The worker prints its child's PID, then waits for it.
	p, err := startProcess(t.TempDir(), "sh", []string{"-c", "sleep 10 & echo $!; wait"}, nil, nil, time.Second, true, nil)
	if err != nil {
		t.Fatalf("start the worker: %v", err)
	}
	t.Cleanup(func() {
		p.Stop()
		<-p.done
	})
	var first line
	select {
	case first = <-p.lines:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker printed no child PID")
	}
	childPID, err := strconv.Atoi(first.text)
	if err != nil {
		t.Fatalf("parse the child PID %q: %v", first.text, err)
	}
	mu.Lock()
	if slices.Contains(late, p.cmd.Process.Pid) {
		t.Errorf("Djinn set the worker's priority once it started: a worker must start at its level")
	}
	mu.Unlock()

	want := wantNice(t, true)
	for _, pr := range []struct {
		name string
		pid  int
	}{{"worker", p.cmd.Process.Pid}, {"child", childPID}} {
		nice, err := niceOf(pr.pid)
		if err != nil {
			t.Fatalf("read the %s's nice: %v", pr.name, err)
		}
		if nice != want {
			t.Errorf("%s PID %d nice = %d, want %d", pr.name, pr.pid, nice, want)
		}
	}
}

// TestWorkerPriorityUnderNice runs the priority tests again from a test process started under nice -n 5: the CI
// runners do not run the tests at nice 0 (macOS's at -10), and a worker at standard priority runs at Djinn's own
// nice level, which an unprivileged process cannot go below (PR #2's CI).
func TestWorkerPriorityUnderNice(t *testing.T) {
	t.Parallel()
	nice, err := exec.LookPath("nice")
	if err != nil {
		t.Skipf("no nice command to start the test process under: %v", err)
	}
	tests := []string{"TestWorkerPriority", "TestWorkerPriorityPolicyChange",
		"TestWorkerChildPriorityInheritanceUnix", "TestProcessUnixSetPriorityTree"}
	cmd := exec.CommandContext(t.Context(), nice, "-n", "5", os.Args[0],
		"-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the priority tests fail under nice -n 5: %v\n%s", err, out)
	}
	for _, name := range []string{"TestWorkerPriority/normal_priority", "TestWorkerPriorityPolicyChange"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Errorf("%s did not pass under nice -n 5:\n%s", name, out)
		}
	}
}
