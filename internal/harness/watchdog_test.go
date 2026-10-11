package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

func TestWatchdogSilentWorkerResumes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	var told []string
	var toldMu sync.Mutex
	tell := func(ctx context.Context, wishID, line string) error {
		toldMu.Lock()
		defer toldMu.Unlock()
		told = append(told, line)
		return nil
	}

	e := up(t, t.TempDir(),
		WithClock(clock.now),
		WithSilenceLimit(100*time.Millisecond),
		WithTellDelay(10*time.Millisecond),
	)
	e.h.TellLeads(tell)
	wishID, _ := e.wish(t, gitRepo(t))

	// Spawn a worker that writes one event then waits silently.
	task := e.spawn(t, wishID, "text start\nwait")
	id := task.GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	// Advance time by 15 minutes so silence limit is exceeded.
	clock.set(clock.now().Add(15 * time.Minute))

	// Trigger scheduler / watchdog pass.
	e.h.Wake()

	// Task should be ended by watchdog, marked RESUMING with Continuing=true, and then continuation runs and ends done.
	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE", done.GetStatus())
	}
	if done.GetResumes() != 1 {
		t.Errorf("resumes = %d, want 1", done.GetResumes())
	}

	events := e.watch(t.Context(), t, id, 0)
	hasResumed := false
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "silent for 15 min") {
			hasResumed = true
			break
		}
	}
	if !hasResumed {
		t.Errorf("events lack silence resumption event: %v", events)
	}

	// Verify the lead was told.
	e.h.batcher.Flush(t.Context())
	toldMu.Lock()
	defer toldMu.Unlock()
	found := false
	for _, line := range told {
		if strings.Contains(line, "silent for 15 min") && strings.Contains(line, "A continuation scheduled.") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("lead not notified properly of continuation, got told: %v", told)
	}
}

func TestWatchdogBusyWorkerNotStopped(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	cpuNano := uint64(100)
	var cpuMu sync.Mutex
	measureFunc := func(pid int, cgroup string) (machine.Group, error) {
		cpuMu.Lock()
		defer cpuMu.Unlock()
		cpuNano += 50 // increasing CPU
		return machine.Group{
			Processes: 1,
			CPU:       time.Duration(cpuNano) * time.Millisecond,
		}, nil
	}

	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}

	e := upWith(t, t.TempDir(), providers,
		WithClock(clock.now),
		WithSilenceLimit(100*time.Millisecond),
		WithMeasure(10*time.Millisecond, measureFunc),
	)
	wishID, _ := e.wish(t, gitRepo(t))

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Busy Worker", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	// Advance time past silence limit.
	clock.set(clock.now().Add(15 * time.Minute))

	// Trigger scheduler / watchdog pass.
	e.h.Wake()
	time.Sleep(50 * time.Millisecond)

	// Since process is alive and CPU is increasing, worker should still be RUNNING.
	tk := e.get(t, id)
	if tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("busy worker was stopped: status = %v", tk.GetStatus())
	}
}

func TestWatchdogGatedWorkerNotStopped(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(),
		WithClock(clock.now),
		WithSilenceLimit(100*time.Millisecond),
	)
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.spawn(t, wishID, "text start\nwait")
	id := task.GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	// Worker holds a gate.
	e.h.HeldGates(func(taskID string) []string {
		if taskID == id {
			return []string{"build-gate"}
		}
		return nil
	}, nil)

	// Advance time past silence limit.
	clock.set(clock.now().Add(15 * time.Minute))
	e.h.Wake()
	time.Sleep(50 * time.Millisecond)

	// Should still be RUNNING because of held gate.
	tk := e.get(t, id)
	if tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("gated worker was stopped: status = %v", tk.GetStatus())
	}
}

func TestWatchdogLostProcessResumesAndStopSucceeds(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	var told []string
	var toldMu sync.Mutex
	tell := func(ctx context.Context, wishID, line string) error {
		toldMu.Lock()
		defer toldMu.Unlock()
		told = append(told, line)
		return nil
	}

	e := up(t, t.TempDir(),
		WithClock(clock.now),
		WithTellDelay(10*time.Millisecond),
	)
	e.h.TellLeads(tell)
	wishID, _ := e.wish(t, gitRepo(t))

	task1 := e.spawn(t, wishID, "wait")
	e.until(t, task1.GetId(), func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})
	// Simulate lost process by deleting from active runs map.
	e.h.mu.Lock()
	delete(e.h.runs, task1.GetId())
	e.h.mu.Unlock()

	// First test: Stop on a lost process should succeed and not return "not running: running".
	res, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task1.GetId()}))
	if err != nil {
		t.Fatalf("Stop on lost process failed: %v", err)
	}
	if res.Msg.GetTask().GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Fatalf("stopped status = %v, want STOPPED", res.Msg.GetTask().GetStatus())
	}

	task2 := e.spawn(t, wishID, "wait")
	e.until(t, task2.GetId(), func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})
	// Simulate lost process by deleting from active runs map.
	e.h.mu.Lock()
	delete(e.h.runs, task2.GetId())
	e.h.mu.Unlock()

	// Run watchdog pass.
	e.h.Wake()

	// It should resume and its continuation should run and end done.
	done := e.ended(t, task2.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE", done.GetStatus())
	}
	if done.GetResumes() != 1 {
		t.Errorf("resumes = %d, want 1", done.GetResumes())
	}

	events := e.watch(t.Context(), t, task2.GetId(), 0)
	hasResumed := false
	for _, ev := range events {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "resuming: lost process") {
			hasResumed = true
			break
		}
	}
	if !hasResumed {
		t.Errorf("events lack lost process resumption event: %v", events)
	}

	// Flush and verify tell was sent.
	e.h.batcher.Flush(t.Context())
	toldMu.Lock()
	defer toldMu.Unlock()
	found := false
	for _, line := range told {
		if strings.Contains(line, task2.GetCode()) && strings.Contains(line, "lost process") && strings.Contains(line, "A continuation scheduled.") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("lead not told of lost process continuation: %v", told)
	}
}
