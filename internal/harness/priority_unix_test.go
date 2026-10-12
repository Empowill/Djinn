//go:build !windows

package harness

import (
	"context"
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

	"connectrpc.com/connect"
	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
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

// TestWorkerPriorityUnix verifies that workers run with nice 10 under minimal and light
// operating load notches, and at Djinn's own nice level under normal notches (decision Q68).
// It is a system test that must run under DJINN_TEST_SYSTEM_ONLY=1 (omits testx.Portable).
func TestWorkerPriorityUnix(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	for _, tc := range []struct {
		name        string
		lowPriority bool
	}{
		{name: "normal priority", lowPriority: false},
		{name: "low priority", lowPriority: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{
				TaskID:      "task-priority-" + tc.name,
				Dir:         t.TempDir(),
				Prompt:      "hello",
				Env:         env,
				LowPriority: tc.lowPriority,
			}
			w, err := Claude{Command: os.Args[0], Grace: time.Second}.Start(t.Context(), spec)
			if err != nil {
				t.Fatalf("start worker: %v", err)
			}
			defer w.Stop()

			proc, ok := w.(Process)
			if !ok {
				t.Fatalf("worker does not implement Process")
			}
			pid := proc.PID()
			if pid <= 0 {
				t.Fatalf("invalid worker PID %d", pid)
			}

			want := wantNice(t, tc.lowPriority)
			waitNice(t, "worker", pid, want)

			if runtime.GOOS == "linux" {
				procNice, err := readProcStatNice(pid)
				if err != nil {
					t.Fatalf("read /proc/%d/stat: %v", pid, err)
				}
				if procNice != want {
					t.Errorf("/proc/%d/stat nice = %d, want %d", pid, procNice, want)
				}
			}
		})
	}
}

// priorityHookProvider records the PID of every started worker.
type priorityHookProvider struct {
	Provider
	mu      sync.Mutex
	started map[string]int
}

func newPriorityHookProvider(base Provider) *priorityHookProvider {
	return &priorityHookProvider{Provider: base, started: make(map[string]int)}
}

func (p *priorityHookProvider) Start(ctx context.Context, spec Spec) (Worker, error) {
	w, err := p.Provider.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if proc, ok := w.(Process); ok {
		p.mu.Lock()
		p.started[spec.TaskID] = proc.PID()
		p.mu.Unlock()
	}
	return w, nil
}

func (p *priorityHookProvider) pidOf(taskID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started[taskID]
}

// TestWorkerPriorityPolicyChangeUnix verifies that changing the operating load notch
// dynamically updates the priority for workers launched subsequently (decision Q68).
func TestWorkerPriorityPolicyChangeUnix(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	baseProvider := envProvider{Claude{Command: os.Args[0], Grace: time.Second}, env}
	hook := newPriorityHookProvider(baseProvider)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = hook

	// Start harness with normal operating load (MEDIUM notch).
	initialPolicy := machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	e := upWith(t, t.TempDir(), providers, WithPolicy(initialPolicy))
	wishID, _ := e.wish(t, gitRepo(t))

	spawnAndCheck := func(title string, low bool) int {
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "do work", Provider: planv1.Provider_PROVIDER_CLAUDE,
		}))
		if err != nil {
			t.Fatalf("spawn %q: %v", title, err)
		}
		id := res.Msg.GetTask().GetId()
		waitFor(t, title+" PID", func() bool { return hook.pidOf(id) > 0 })
		pid := hook.pidOf(id)

		waitNice(t, title, pid, wantNice(t, low))

		// Stop the task to free worker slots.
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatalf("stop %q: %v", title, err)
		}
		return pid
	}

	// Each notch's priority is machine.TestLowPriority's: here, a change of notch reaches the next worker, both ways.
	// Three workers, not one per notch: each costs a worktree and a start of the test binary, slow on Windows.
	// 1. Initial MEDIUM notch: low priority (nice 10).
	spawnAndCheck("Worker 1 Medium", true)

	// 2. Change policy to HIGH notch: standard priority (Djinn's own nice level).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH))
	spawnAndCheck("Worker 2 High", false)

	// 3. Change policy to MINIMAL notch: low priority (nice 10) again.
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL))
	spawnAndCheck("Worker 3 Minimal", true)
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
	tests := []string{"TestWorkerPriorityUnix", "TestWorkerPriorityPolicyChangeUnix",
		"TestWorkerChildPriorityInheritanceUnix", "TestProcessUnixSetPriorityTree"}
	cmd := exec.CommandContext(t.Context(), nice, "-n", "5", os.Args[0],
		"-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the priority tests fail under nice -n 5: %v\n%s", err, out)
	}
	for _, name := range []string{"TestWorkerPriorityUnix/normal_priority", "TestWorkerPriorityPolicyChangeUnix"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Errorf("%s did not pass under nice -n 5:\n%s", name, out)
		}
	}
}
