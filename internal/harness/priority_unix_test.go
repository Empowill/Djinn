//go:build !windows

package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// processNice reads the nice level of pid.
// On Linux, the raw getpriority syscall returns 20 - nice.
// On Darwin, libc's getpriority directly returns the nice value (-20 to 19).
func processNice(pid int) (int, error) {
	prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "linux" {
		return 20 - prio, nil
	}
	return prio, nil
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
// operating load notches, and nice 0 under normal notches (decision Q68).
// It is a system test that must run under DJINN_TEST_SYSTEM_ONLY=1 (omits testx.Portable).
func TestWorkerPriorityUnix(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)

	for _, tc := range []struct {
		name        string
		lowPriority bool
		wantNice    int
	}{
		{name: "normal priority", lowPriority: false, wantNice: 0},
		{name: "low priority", lowPriority: true, wantNice: 10},
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

			nice, err := processNice(pid)
			if err != nil {
				t.Fatalf("read nice of PID %d: %v", pid, err)
			}
			if nice != tc.wantNice {
				t.Errorf("worker PID %d nice = %d, want %d", pid, nice, tc.wantNice)
			}

			if runtime.GOOS == "linux" {
				procNice, err := readProcStatNice(pid)
				if err != nil {
					t.Fatalf("read /proc/%d/stat: %v", pid, err)
				}
				if procNice != tc.wantNice {
					t.Errorf("/proc/%d/stat nice = %d, want %d", pid, procNice, tc.wantNice)
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

	spawnAndCheck := func(title string, wantNice int) int {
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: title, Prompt: "do work", Provider: planv1.Provider_PROVIDER_CLAUDE,
		}))
		if err != nil {
			t.Fatalf("spawn %q: %v", title, err)
		}
		id := res.Msg.GetTask().GetId()
		waitFor(t, title+" PID", func() bool { return hook.pidOf(id) > 0 })
		pid := hook.pidOf(id)

		nice, err := processNice(pid)
		if err != nil {
			t.Fatalf("read nice for %q (PID %d): %v", title, pid, err)
		}
		if nice != wantNice {
			t.Errorf("%q (PID %d) nice = %d, want %d", title, pid, nice, wantNice)
		}

		// Stop the task to free worker slots.
		if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: id})); err != nil {
			t.Fatalf("stop %q: %v", title, err)
		}
		return pid
	}

	// 1. Initial MEDIUM notch: low priority (nice 10).
	spawnAndCheck("Worker 1 Medium", 10)

	// 2. Change policy to MINIMAL notch: low priority (nice 10).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL))
	spawnAndCheck("Worker 2 Minimal", 10)

	// 3. Change policy to HIGH notch: standard priority (nice 0).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_HIGH))
	spawnAndCheck("Worker 3 High", 0)

	// 4. Change policy to MAX notch: standard priority (nice 0).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MAX))
	spawnAndCheck("Worker 4 Max", 0)

	// 5. Change policy to OVERCLOCK notch: standard priority (nice 0).
	e.h.SetPolicy(machine.NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK))
	spawnAndCheck("Worker 5 Overclock", 0)
}

// TestWorkerChildPriorityInheritanceUnix verifies that child commands executed by a low-priority
// worker automatically inherit the low priority (nice 10).
func TestWorkerChildPriorityInheritanceUnix(t *testing.T) {
	t.Parallel()
	// Create a parent process with lowPriority: true that starts a child process.
	// The parent script prints the child's PID and waits.
	cmd := exec.Command("sh", "-c", `read dummy; sh -c 'sleep 10' & echo $!; wait`)
	ownGroup(cmd, true)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd.Stdout = w

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inW.Close()
	cmd.Stdin = inR

	if err := cmd.Start(); err != nil {
		w.Close()
		t.Fatalf("start parent: %v", err)
	}
	applyPriority(cmd, true)
	w.Close()

	// Signal parent to proceed now that priority has been applied.
	if _, err := inW.Write([]byte("go\n")); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("signal parent: %v", err)
	}
	_ = inW.Close()

	// Read child PID from stdout.
	var childPidStr string
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("read child PID: %v", err)
	}
	childPidStr = strings.TrimSpace(string(buf[:n]))
	childPID, err := strconv.Atoi(childPidStr)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("parse child PID %q: %v", childPidStr, err)
	}

	parentPID := cmd.Process.Pid
	parentNice, err := processNice(parentPID)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("read parent nice: %v", err)
	}
	if parentNice != 10 {
		t.Errorf("parent PID %d nice = %d, want 10", parentPID, parentNice)
	}

	childNice, err := processNice(childPID)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("read child nice: %v", err)
	}
	if childNice != 10 {
		t.Errorf("child PID %d nice = %d, want 10", childPID, childNice)
	}

	// Clean up processes.
	_ = syscall.Kill(childPID, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
