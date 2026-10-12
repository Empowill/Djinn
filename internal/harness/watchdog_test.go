package harness

import (
	"context"
	"fmt"
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

func TestWatchdogBudgetWallTimeExceeded(t *testing.T) {
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
		WithTaskMaxWallTime(100*time.Millisecond),
		WithTellDelay(10*time.Millisecond),
	)
	e.h.TellLeads(tell)
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.spawn(t, wishID, "wait")
	id := task.GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	// Advance clock past wall time budget.
	clock.set(clock.now().Add(3 * time.Hour))
	e.h.Wake()

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Fatalf("status = %v, want STOPPED", done.GetStatus())
	}
	if !strings.Contains(done.GetError(), "budget exceeded") {
		t.Errorf("error = %q, want budget exceeded", done.GetError())
	}

	e.h.batcher.Flush(t.Context())
	toldMu.Lock()
	defer toldMu.Unlock()
	found := false
	for _, line := range told {
		if strings.Contains(line, "budget exceeded") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("lead not told of budget failure, got told: %v", told)
	}
}

func TestWatchdogBudgetTurnsExceeded(t *testing.T) {
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
		WithTaskMaxTurns(1),
		WithTellDelay(10*time.Millisecond),
	)
	e.h.TellLeads(tell)
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.spawn(t, wishID, "wait")
	id := task.GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	// Inject extra turn into run.
	e.h.mu.Lock()
	r := e.h.runs[id]
	if r != nil {
		r.turns = 5
	}
	e.h.mu.Unlock()

	e.h.Wake()

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Fatalf("status = %v, want STOPPED", done.GetStatus())
	}
	if !strings.Contains(done.GetError(), "budget exceeded") {
		t.Errorf("error = %q, want budget exceeded", done.GetError())
	}

	e.h.batcher.Flush(t.Context())
	toldMu.Lock()
	defer toldMu.Unlock()
	found := false
	for _, line := range told {
		if strings.Contains(line, "budget exceeded") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("lead not told of budget failure, got told: %v", told)
	}
}

// A worker at 120 turns, as real Opus tasks take (W442, W453), goes on under the default budget; past it, it stops.
func TestWatchdogBudgetTurnsDefault(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now))
	wishID, _ := e.wish(t, gitRepo(t))

	task := e.spawn(t, wishID, "wait")
	id := task.GetId()
	running := e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	turns := func(n int) {
		e.h.mu.Lock()
		e.h.runs[id].turns = n
		e.h.mu.Unlock()
	}
	turns(120)
	e.h.watchdog(t.Context(), []*planv1.Task{running})
	e.h.mu.Lock()
	stopping, reason := e.h.runs[id].stopping, e.h.runs[id].stoppingReason
	e.h.mu.Unlock()
	if stopping {
		t.Fatalf("stopped at 120 turns: %q", reason)
	}

	turns(TaskMaxTurns + 1)
	e.h.watchdog(t.Context(), []*planv1.Task{running})
	done := e.ended(t, id)
	if want := fmt.Sprintf("budget exceeded: %d turns (limit %d)", TaskMaxTurns+1, TaskMaxTurns); done.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || done.GetError() != want {
		t.Errorf("status = %v, error = %q; want STOPPED, %q", done.GetStatus(), done.GetError(), want)
	}
}

func TestWatchdogDeadProcessEnds(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	measureFunc := func(pid int, cgroup string) (machine.Group, error) {
		return machine.Group{Processes: 0}, nil
	}

	waitEnv, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	successEnv, _, _ := fake{provider: "claude", fixture: "success"}.env(t)
	providers, sw := claudePlaying(waitEnv)

	e := upWith(t, t.TempDir(), providers,
		WithClock(clock.now),
		WithSilenceLimit(100*time.Millisecond),
		WithMeasure(10*time.Millisecond, measureFunc),
	)
	wishID, _ := e.wish(t, gitRepo(t))

	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Dead Worker", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	e.until(t, id, func(tk *planv1.Task) bool {
		return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && tk.GetSessionId() != ""
	})

	// Before resumption, set the next run to finish successfully.
	sw.play(successEnv)

	// Advance past silence limit.
	clock.set(clock.now().Add(15 * time.Minute))
	e.h.Wake()

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE (after continuation)", done.GetStatus())
	}
	if done.GetResumes() != 1 {
		t.Errorf("resumes = %d, want 1", done.GetResumes())
	}
}

// The incident of 12/10/2026: the watchdog failed the babysit's watcher, "silent for 15 min", while its command printed
// only when its pull request changed. A watcher silent past the limit runs on; an agent worker as silent is ended.
func TestWatchdogSilentWatcherRunsOn(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 12, 11, 13, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithSilenceLimit(100*time.Millisecond))
	wishID, _ := e.wish(t, gitRepo(t))

	watcher, err := e.spawnWatch(t, wishID, watchCommand("wait"), true)
	if err != nil {
		t.Fatal(err)
	}
	worker := e.spawn(t, wishID, "text start\nwait")
	running := func(tk *planv1.Task) bool { return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING }
	e.until(t, watcher.GetId(), running)
	e.until(t, worker.GetId(), running)

	clock.set(clock.now().Add(15 * time.Minute))
	e.h.Wake()

	// One watchdog pass checks both: the worker's end says it ran.
	e.ended(t, worker.GetId())
	silenced := false
	for _, ev := range e.watch(t.Context(), t, worker.GetId(), 0) {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "silent for 15 min") {
			silenced = true
		}
	}
	if !silenced {
		t.Errorf("the silent agent worker was not ended by the watchdog")
	}

	e.h.mu.Lock()
	r := e.h.runs[watcher.GetId()]
	ended := r == nil || r.watchdogEnd
	e.h.mu.Unlock()
	if tk := e.get(t, watcher.GetId()); ended || tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("silent watcher: status = %v, error = %q, ended by the watchdog = %v; want RUNNING", tk.GetStatus(), tk.GetError(), ended)
	}
}
