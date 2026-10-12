//go:build windows

package harness

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/sys/windows"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// processPriorityClass returns the priority class of pid on Windows.
func processPriorityClass(pid int) (uint32, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	return windows.GetPriorityClass(handle)
}

// wantClass is the priority class a worker must run at: the test process's own at standard priority, as Djinn's own
// is a worker's; BELOW_NORMAL_PRIORITY_CLASS at low priority, or the test process's own when it runs lower already
// (IDLE_PRIORITY_CLASS). A CI runner does not run at NORMAL_PRIORITY_CLASS: Windows's ran the tests at BELOW_NORMAL.
func wantClass(t *testing.T, low bool) uint32 {
	t.Helper()
	own, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatalf("read the test process's priority class: %v", err)
	}
	if low && own != windows.IDLE_PRIORITY_CLASS {
		return windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	return own
}

// TestWorkerPriorityWindows verifies that workers run with BELOW_NORMAL_PRIORITY_CLASS under
// minimal and light operating load notches, and at Djinn's own priority class under normal notches (decision Q68).
// It is a system test that must run under DJINN_TEST_SYSTEM_ONLY=1 (omits testx.Portable).
func TestWorkerPriorityWindows(t *testing.T) {
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

			want := wantClass(t, tc.lowPriority)
			cls, err := processPriorityClass(pid)
			if err != nil {
				t.Fatalf("read priority class of PID %d: %v", pid, err)
			}
			if cls != want {
				t.Errorf("worker PID %d priority class = 0x%08x, want 0x%08x", pid, cls, want)
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

// TestWorkerPriorityPolicyChangeWindows verifies that changing the operating load notch
// dynamically updates the priority for workers launched subsequently on Windows (decision Q68).
func TestWorkerPriorityPolicyChangeWindows(t *testing.T) {
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

		want := wantClass(t, low)
		cls, err := processPriorityClass(pid)
		if err != nil {
			t.Fatalf("read priority class for %q (PID %d): %v", title, pid, err)
		}
		if cls != want {
			t.Errorf("%q (PID %d) priority class = 0x%08x, want 0x%08x", title, pid, cls, want)
		}

		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatalf("stop %q: %v", title, err)
		}
		return pid
	}

	// 1. Initial MEDIUM notch: low priority class.
	spawnAndCheck("Worker 1 Medium", true)

	// 2. Change policy to MINIMAL notch: low priority class.
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL))
	spawnAndCheck("Worker 2 Minimal", true)

	// 3. Change policy to HIGH notch: standard priority (Djinn's own class).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH))
	spawnAndCheck("Worker 3 High", false)

	// 4. Change policy to MAX notch: standard priority (Djinn's own class).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX))
	spawnAndCheck("Worker 4 Max", false)

	// 5. Change policy to OVERCLOCK notch: standard priority (Djinn's own class).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK))
	spawnAndCheck("Worker 5 Overclock", false)
}

// TestWorkerPriorityUnderBelowNormal runs the priority tests again from a test process started at
// BELOW_NORMAL_PRIORITY_CLASS: the Windows CI runner ran the tests there, and a worker at standard priority runs at
// Djinn's own class (PR #2's CI). The Unix twin starts it under nice -n 5.
func TestWorkerPriorityUnderBelowNormal(t *testing.T) {
	t.Parallel()
	tests := []string{"TestWorkerPriorityWindows", "TestWorkerPriorityPolicyChangeWindows"}
	cmd := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.BELOW_NORMAL_PRIORITY_CLASS}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the priority tests fail at BELOW_NORMAL_PRIORITY_CLASS: %v\n%s", err, out)
	}
	for _, name := range []string{"TestWorkerPriorityWindows/normal_priority", "TestWorkerPriorityPolicyChangeWindows"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Errorf("%s did not pass at BELOW_NORMAL_PRIORITY_CLASS:\n%s", name, out)
		}
	}
}
