package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestLamplighterSilentWorkerResumes(t *testing.T) {
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

	e.follow(t, id)("its worker said start", said("start"))

	// Advance time by 15 minutes so silence limit is exceeded.
	clock.set(clock.now().Add(15 * time.Minute))

	// Trigger scheduler / Lamplighter pass.
	e.h.Wake()

	// Task should be ended by the Lamplighter, marked RESUMING with Continuing=true, and then continuation runs and ends done.
	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE", done.GetStatus())
	}
	if done.GetResumes() != 0 {
		t.Errorf("resumes = %d, want 0: a wake spends no usage-limit resume", done.GetResumes())
	}
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runWoken {
		t.Errorf("runs = %v, want the first woken", runs)
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

func TestLamplighterBusyWorkerNotStopped(t *testing.T) {
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

	e.follow(t, id)("its worker said its usage", saidUsage)

	// Advance time past silence limit.
	clock.set(clock.now().Add(15 * time.Minute))

	// Trigger scheduler / Lamplighter pass.
	e.h.Wake()
	time.Sleep(50 * time.Millisecond)

	// Since process is alive and CPU is increasing, worker should still be RUNNING.
	tk := e.get(t, id)
	if tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("busy worker was stopped: status = %v", tk.GetStatus())
	}
}

func TestLamplighterGatedWorkerNotStopped(t *testing.T) {
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

	e.follow(t, id)("its worker said start", said("start"))

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

func TestLamplighterLostProcessResumesAndStopSucceeds(t *testing.T) {
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
	e.follow(t, task1.GetId())("its worker said its session", saidSession(task1.GetId()))
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
	e.follow(t, task2.GetId())("its worker said its session", saidSession(task2.GetId()))
	// Simulate lost process by deleting from active runs map.
	e.h.mu.Lock()
	delete(e.h.runs, task2.GetId())
	e.h.mu.Unlock()

	// Run a Lamplighter pass.
	e.h.Wake()

	// It should resume and its continuation should run and end done.
	done := e.ended(t, task2.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE", done.GetStatus())
	}
	if done.GetResumes() != 0 {
		t.Errorf("resumes = %d, want 0: a wake spends no usage-limit resume", done.GetResumes())
	}
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runWoken {
		t.Errorf("runs = %v, want the first woken", runs)
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

func TestLamplighterBudgetWallTimeExceeded(t *testing.T) {
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

func TestLamplighterBudgetTurnsExceeded(t *testing.T) {
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
func TestLamplighterBudgetTurnsDefault(t *testing.T) {
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
	e.h.lamplighter(t.Context(), []*planv1.Task{running})
	e.h.mu.Lock()
	stopping, reason := e.h.runs[id].stopping, e.h.runs[id].stoppingReason
	e.h.mu.Unlock()
	if stopping {
		t.Fatalf("stopped at 120 turns: %q", reason)
	}

	turns(TaskMaxTurns + 1)
	e.h.lamplighter(t.Context(), []*planv1.Task{running})
	done := e.ended(t, id)
	if want := fmt.Sprintf("budget exceeded: %d turns (limit %d)", TaskMaxTurns+1, TaskMaxTurns); done.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED || done.GetError() != want {
		t.Errorf("status = %v, error = %q; want STOPPED, %q", done.GetStatus(), done.GetError(), want)
	}
}

func TestLamplighterDeadProcessEnds(t *testing.T) {
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

	e.follow(t, id)("its worker said its usage", saidUsage)

	// Before resumption, set the next run to finish successfully.
	sw.play(successEnv)

	// Advance past silence limit.
	clock.set(clock.now().Add(15 * time.Minute))
	e.h.Wake()

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v, want DONE (after continuation)", done.GetStatus())
	}
	if done.GetResumes() != 0 {
		t.Errorf("resumes = %d, want 0: a wake spends no usage-limit resume", done.GetResumes())
	}
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runWoken {
		t.Errorf("runs = %v, want the first woken", runs)
	}
}

// TestLamplighterWatcherOutlivesBudgets replays Babysit PR #2 (12/10/2026): W1 and W6, the template's watcher, were
// failed as silent for 15 min, and W9 would have been stopped as a runaway past the 2 h budget. A watcher past the
// silence limit, the wall-time budget and the turn budget still runs; the agent worker checked in the same pass
// does not.
func TestLamplighterWatcherOutlivesBudgets(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 12, 11, 13, 0, 0, time.UTC)}
	e := up(t, t.TempDir(), WithClock(clock.now), WithSilenceLimit(100*time.Millisecond),
		WithTaskMaxWallTime(time.Hour), WithTaskMaxTurns(1))
	wishID, _ := e.wish(t, gitRepo(t))

	watcher, err := e.spawnWatch(t, wishID, watchCommand("wait"), false)
	if err != nil {
		t.Fatal(err)
	}
	worker := e.spawn(t, wishID, "text start\nwait")
	running := func(tk *planv1.Task) bool { return tk.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING }
	e.until(t, watcher.GetId(), running)
	e.until(t, worker.GetId(), running)
	e.h.mu.Lock()
	e.h.runs[watcher.GetId()].turns = 2 // Past the turn budget, were it counted.
	e.h.mu.Unlock()

	clock.set(clock.now().Add(3 * time.Hour))
	e.h.Wake()

	// One Lamplighter pass checks both: the worker's end says it ran.
	if got := e.ended(t, worker.GetId()); !strings.Contains(got.GetError(), "budget exceeded") {
		t.Errorf("the agent worker past its budgets: %v", got)
	}

	e.h.mu.Lock()
	r := e.h.runs[watcher.GetId()]
	ended := r == nil || r.lamplighterEnd || r.stopping
	e.h.mu.Unlock()
	if tk := e.get(t, watcher.GetId()); ended || tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Fatalf("silent watcher past its budgets: status = %v, error = %q, ended by the Lamplighter = %v; want RUNNING",
			tk.GetStatus(), tk.GetError(), ended)
	}
}

// TestWatcherStartsAgainWhenItDies: a watcher whose command dies is started again by the Lamplighter, in the same task,
// its state folder as it left it: the lead spawned W6 then W9 by hand, each time losing what the watch had said.
func TestWatcherStartsAgainWhenItDies(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	task, err := e.spawnWatch(t, wishID, watchCommand("keep:runs", "die-before:3", "say:watching", "wait"), false)
	if err != nil {
		t.Fatal(err)
	}
	got := e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == "watching" })
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("watcher = %v", got)
	}
	if b, err := os.ReadFile(filepath.Join(home, "watchers", task.GetId(), "runs")); err != nil || string(b) != "3" {
		t.Errorf("its state folder counts %q runs (%v), want 3: one folder over its runs", b, err)
	}
	var failed []string
	for _, ev := range e.storedEvents(t, task.GetId()) {
		if strings.HasPrefix(ev.GetText(), "the command failed") {
			failed = append(failed, ev.GetText())
		}
	}
	if len(failed) != 2 || !strings.HasPrefix(failed[0], "the command failed (exit code 1, 1 of 5 in a row); it starts again in ") {
		t.Errorf("events %q, want two deaths", failed)
	}
	if tasks, err := store.List[*planv1.Task](t.Context(), e.db, store.Where{"wish_id": wishID}); err != nil || len(tasks) != 1 {
		t.Errorf("tasks = %v, %v: the same task watches on", tasks, err)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_STOPPED {
		t.Errorf("stopped watcher = %v", got)
	}
}

// TestWatcherGivesUp: a watcher whose command dies at each start fails at the fifth, and its lead is told why once,
// with the command's last error lines.
func TestWatcherGivesUp(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithTick(time.Hour))
	lead := &told{}
	e.h.TellLeads(lead.tell)
	wishID, _ := e.wish(t, gitRepo(t))

	task, err := e.spawnWatch(t, wishID, watchCommand("keep:runs", "err:gh: not logged in", "exit:4"), true)
	if err != nil {
		t.Fatal(err)
	}
	got := e.ended(t, task.GetId())
	want := "its command died 5 times in a row; the last time: exit code 4; its error output: gh: not logged in"
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || got.GetError() != want {
		t.Errorf("watcher = %v, want failed: %s", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(home, "watchers", task.GetId(), "runs")); err != nil || string(b) != "5" {
		t.Errorf("%q runs (%v), want 5", b, err)
	}
	var failures []string
	for _, l := range lead.all() {
		if strings.Contains(l, "failed") || strings.Contains(l, "has ended") {
			failures = append(failures, l)
		}
	}
	if line := wishID + ": Djinn: W1's watcher failed: " + want; len(failures) != 1 || failures[0] != line {
		t.Errorf("the lead was told %q, want once %q", failures, line)
	}
}

// TestLamplighterLostWatcherStartsAgain: a watcher whose task runs without a run (a lost process) starts again, in the
// same task, instead of failing: plan.CanResume is false for a watcher, which has no session to resume.
func TestLamplighterLostWatcherStartsAgain(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	e := up(t, home, WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))

	task, err := e.spawnWatch(t, wishID, watchCommand("keep:runs", "say:watching", "wait"), false)
	if err != nil {
		t.Fatal(err)
	}
	e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetLastLine() == "watching" })
	// Its process is lost: stopped, then the store says it runs, and no run holds it.
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	lost := e.get(t, task.GetId())
	lost.Status, lost.Error, lost.EndTime = planv1.TaskStatus_TASK_STATUS_RUNNING, "", nil
	putTaskInDB(t, e.db, lost)
	e.h.Wake()

	runs := filepath.Join(home, "watchers", task.GetId(), "runs")
	waitFor(t, "the lost watcher's second run", func() bool {
		b, _ := os.ReadFile(runs)
		return string(b) == "2"
	})
	got := e.until(t, task.GetId(), func(t *planv1.Task) bool { return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING })
	if got.GetError() != "" {
		t.Errorf("lost watcher = %v", got)
	}
	e.untilText(t, task.GetId(), "resumed after its process was lost: started watch in ")
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
}

// TestWatcherLife: a command that dies starts again after a backoff of a second, doubling up to five minutes; a run
// that lived a minute starts the backoff and the count over; the fifth death in a row ends the watcher, but for an
// inbox source. Exit 0 starts it again only when its task restarts it.
func TestWatcherLife(t *testing.T) {
	t.Parallel()
	dead := Result{ExitCode: 1}
	l := newWatcherLife(watchBackoff, false)
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		if wait, again := l.ended(dead, true, 0); !again || wait != want {
			t.Fatalf("death %d: wait %s, again %v; want %s", i+1, wait, again, want)
		}
	}
	if wait, again := l.ended(dead, true, watchLived); !again || wait != time.Second {
		t.Errorf("after a run that lived: wait %s, again %v; want a second", wait, again)
	}
	for i := 2; i <= watchTries; i++ {
		_, again := l.ended(Result{Err: errors.New("sh not found in PATH")}, true, 0)
		if again != (i < watchTries) {
			t.Fatalf("death %d in a row: again %v", i, again)
		}
	}

	l = newWatcherLife(watchBackoff, true)
	var wait time.Duration
	for range 20 {
		wait, _ = l.ended(dead, true, 0)
	}
	if wait != watchMaxBackoff {
		t.Errorf("an inbox source after 20 deaths waits %s, want %s", wait, watchMaxBackoff)
	}
	if wait, again := l.ended(Result{}, true, 0); !again || wait != 0 {
		t.Errorf("exit 0, restarting: wait %s, again %v", wait, again)
	}
	if _, again := l.ended(Result{}, false, 0); again {
		t.Errorf("exit 0 without restart starts again")
	}
	if wait, _ := l.ended(dead, true, 0); wait != time.Second {
		t.Errorf("after an exit 0, a death waits %s, want a second", wait)
	}
}

// TestLamplighterWakesAfterUsageLimitResumes replays W485's check of the Lamplighter: wakes and usage-limit resumes
// spent one count (Task.resumes), so a worker resumed three times on its usage limits failed at its first silence or its
// first lost process, although its agent could resume it. Now its wakes are counted apart: it is woken.
func TestLamplighterWakesAfterUsageLimitResumes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	limited := &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Resumes: maxResumes,
		Runs: []*planv1.TaskRun{{Reason: "limit", EndTime: timestamppb.Now()}, {Reason: "limit", EndTime: timestamppb.Now()},
			{Reason: "limit", EndTime: timestamppb.Now()}, {}}}
	if !canWake(limited) {
		t.Errorf("a silent worker resumed %d times on its limits is not woken", maxResumes)
	}

	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.spawn(t, wishID, "wait")
	id := task.GetId()
	running := e.follow(t, id)("its worker said its session", saidSession(id))
	if running.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || running.GetSessionId() == "" {
		t.Fatalf("its worker said its session, stored %v", running)
	}
	running.Resumes = maxResumes
	putTaskInDB(t, e.db, running)
	e.h.mu.Lock()
	e.h.runs[id].task.Resumes = maxResumes
	delete(e.h.runs, id)
	e.h.mu.Unlock()
	e.h.Wake()

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("status = %v (%s), want DONE: the lost worker is woken", done.GetStatus(), done.GetError())
	}
	if runs := done.GetRuns(); len(runs) != 2 || runs[0].GetReason() != runWoken {
		t.Errorf("runs = %v, want the first woken", runs)
	}
}

// follow watches the events of the task as they are written, and gives what waits for the next one ok says: the
// task as stored when it was written, since an event and the task it changes are written in one transaction. A test
// that changes a worker's task or the clock waits so for what its worker says, never for its status alone: read when
// the task runs, before its worker said its session, the task put back erases the session (W547, macOS); the clock
// moved before the worker said all it says, its last words make it active again, and it is never silent.
func (e *env) follow(t *testing.T, id string) func(what string, ok func(*planv1.TaskEvent) bool) *planv1.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	s, err := e.tasks.Watch(ctx, connect.NewRequest(&planv1.TaskServiceWatchRequest{TaskId: id}))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		s.Close()
	})
	return func(what string, ok func(*planv1.TaskEvent) bool) *planv1.Task {
		t.Helper()
		for s.Receive() {
			if ok(s.Msg().GetEvent()) {
				return e.get(t, id)
			}
		}
		t.Fatalf("task %s: its stream ended before %s: %v", id, what, s.Err())
		return nil
	}
}

// saidSession is the event where the fake worker of the task says its session.
func saidSession(id string) func(*planv1.TaskEvent) bool {
	return func(ev *planv1.TaskEvent) bool { return strings.HasPrefix(ev.GetText(), "fake session "+id) }
}

// said is the text event where a fake worker says text.
func said(text string) func(*planv1.TaskEvent) bool {
	return func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && ev.GetText() == text
	}
}

// saidUsage is the usage event of a claude turn: the last line of its fixture.
func saidUsage(ev *planv1.TaskEvent) bool {
	return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_USAGE
}

// TestLamplighterGivesUpAfterWakes: a worker silent run after run is woken maxWakes times in a row; silent once more,
// its task fails saying so, and its lead is told once that it failed.
func TestLamplighterGivesUpAfterWakes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}
	dead := func(pid int, cgroup string) (machine.Group, error) { return machine.Group{Processes: 0}, nil }
	var told []string
	var toldMu sync.Mutex
	tell := func(ctx context.Context, wishID, line string) error {
		toldMu.Lock()
		defer toldMu.Unlock()
		told = append(told, line)
		return nil
	}
	waitEnv, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	providers, _ := claudePlaying(waitEnv)
	e := upWith(t, t.TempDir(), providers,
		WithClock(clock.now),
		WithSilenceLimit(100*time.Millisecond),
		WithMeasure(10*time.Millisecond, dead),
		WithTellDelay(10*time.Millisecond),
	)
	e.h.TellLeads(tell)
	wishID, _ := e.wish(t, gitRepo(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Silent worker", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()

	next := e.follow(t, id)
	for run := 1; run <= maxWakes+1; run++ {
		// Each run plays the whole fixture, its usage last, then waits: silent from then on.
		tk := next("its run said its usage", saidUsage)
		if tk.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || len(tk.GetRuns()) != run || tk.GetSessionId() == "" {
			t.Fatalf("run %d said its usage, stored %v", run, tk)
		}
		clock.set(clock.now().Add(15 * time.Minute))
		e.h.Wake()
	}

	done := e.ended(t, id)
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED after %d wakes", done.GetStatus(), maxWakes)
	}
	want := fmt.Sprintf("silent for 15 min; the Lamplighter woke it %d times in a row and gives up", maxWakes)
	if !strings.Contains(done.GetError(), want) {
		t.Errorf("error = %q, want %q", done.GetError(), want)
	}
	if runs := done.GetRuns(); len(runs) != maxWakes+1 || wakes(&planv1.Task{Runs: runs[:maxWakes]}) != maxWakes ||
		runs[maxWakes].GetReason() != "failed" {
		t.Errorf("runs = %v, want %d woken, then one failed", runs, maxWakes)
	}

	e.h.batcher.Flush(t.Context())
	toldMu.Lock()
	defer toldMu.Unlock()
	if n := strings.Count(strings.Join(told, "\n"), "The move is yours."); n != 1 {
		t.Errorf("lead told of the failure %d times, want once: %v", n, told)
	}
}

// TestWakes counts the woken runs in a row, the last ones: a run that ended otherwise ends the row.
func TestWakes(t *testing.T) {
	t.Parallel()
	runs := func(reasons ...string) *planv1.Task {
		task := &planv1.Task{}
		for _, r := range reasons {
			task.Runs = append(task.Runs, &planv1.TaskRun{Reason: r, EndTime: timestamppb.Now()})
		}
		return task
	}
	running := func(task *planv1.Task) *planv1.Task {
		task.Runs = append(task.Runs, &planv1.TaskRun{})
		return task
	}
	for _, c := range []struct {
		task *planv1.Task
		want int
	}{
		{runs(), 0},
		{runs(runWoken, runWoken, ""), 0},
		{runs(runWoken, "limit", runWoken, runWoken), 2},
		{runs(runWoken, runWoken, runWoken), 3},
		{running(runs(runWoken, runWoken)), 2},
	} {
		if got := wakes(c.task); got != c.want {
			t.Errorf("wakes(%v) = %d, want %d", c.task.GetRuns(), got, c.want)
		}
	}
	if canWake(&planv1.Task{Provider: planv1.Provider_PROVIDER_WATCH}) {
		t.Error("a watcher's task is woken as a worker; its command is started again instead")
	}
}
