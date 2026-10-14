package harness

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func isWorkerLowPriority(h *Harness, taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.runs[taskID]
	if r == nil || r.worker == nil {
		return false
	}
	if fw, ok := r.worker.(*fakeWorker); ok {
		return fw.IsLowPriority()
	}
	return false
}

// TestNotchRuntimePriorityAndPause verifies that lowering the operating load notch:
// 1. Immediately sets low CPU priority on running workers (Q120 A).
// 2. Immediately pauses extra running workers in Q121 order (Q117 reversed).
// 3. Emits a paused status event and a journal entry with method "harness/notch".
func TestNotchRuntimePriorityAndPause(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	lim := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	// Start two workers with standard priority, disjoint write scopes.
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})

	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s1"}})
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	w2 := e.mustSpawn(t, wishID, "Work 2", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	if isWorkerLowPriority(e.h, w1.GetId()) || isWorkerLowPriority(e.h, w2.GetId()) {
		t.Fatal("expected standard priority on running workers under High notch")
	}

	// Lower notch to Minimal, then reduce slots to 1: the other way round, a scheduler pass between the two
	// would pause W2 under High, and its event would name High.
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL})
	lim.set(1, "")

	// 1. Priorities updated at once.
	if !isWorkerLowPriority(e.h, w1.GetId()) {
		t.Errorf("w1 was not set to low priority after lowering notch")
	}
	if !isWorkerLowPriority(e.h, w2.GetId()) {
		t.Errorf("w2 was not set to low priority after lowering notch")
	}

	// 2. Extra worker paused in Q121 order (Q117 reversed):
	// W2 started after W1, so W2 is paused; W1 remains running.
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	t1 := e.get(t, w1.GetId())
	if t1.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("w1 status = %v, want RUNNING", t1.GetStatus())
	}

	if paused := e.h.Paused(); paused != 1 {
		t.Errorf("harness.Paused() = %d, want 1", paused)
	}

	// 3. Verify paused event and journal.
	w2Events := e.storedEvents(t, w2.GetId())
	if !slices.ContainsFunc(w2Events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "paused: operating load lowered to Minimal")
	}) {
		t.Errorf("w2 missing paused event: %v", eventTexts(w2Events))
	}

	cmds, err := store.Commands(t.Context(), e.db, func(c store.Command) bool {
		return c.Actor == actorHarness && c.Method == methodNotch
	})
	if err != nil || len(cmds) == 0 {
		t.Errorf("no journal entries with actor %q and method %q: %v", actorHarness, methodNotch, err)
	}

	// Cleanup.
	e.release(t, w1.GetId())
	e.ended(t, w1.GetId())
}

// TestNotchRuntimeResumeFirst verifies that raising the notch:
// 1. Resumes paused workers first in Q123 order (Q117 order).
// 2. Restores normal priority on resumed workers.
func TestNotchRuntimeResumeFirst(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	lim := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})

	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s1"}})
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	w2 := e.mustSpawn(t, wishID, "Work 2", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// Lower notch to pause W2.
	lim.set(1, "")
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL})
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))

	// Raise notch back to High with 2 slots.
	lim.set(2, "")
	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})

	// W2 must be resumed first and running again.
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if paused := e.h.Paused(); paused != 0 {
		t.Errorf("harness.Paused() = %d, want 0", paused)
	}

	// Priority restored to normal.
	if isWorkerLowPriority(e.h, w2.GetId()) {
		t.Errorf("w2 is still low priority after raising notch to High")
	}

	// Verify resumed event.
	w2Events := e.storedEvents(t, w2.GetId())
	if !slices.ContainsFunc(w2Events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "resumed: a slot is free again")
	}) {
		t.Errorf("w2 missing resumed event: %v", eventTexts(w2Events))
	}

	// Cleanup.
	e.release(t, w1.GetId())
	e.release(t, w2.GetId())
	e.ended(t, w1.GetId())
	e.ended(t, w2.GetId())
}

// TestNotchRuntimeMemoryInterrupt verifies that a paused worker is interrupted
// only when memory is short for a higher-priority worker that must start (Q122 A),
// entering RESUMING without spending resumes, and resumes in its session when memory frees.
func TestNotchRuntimeMemoryInterrupt(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	var mu sync.Mutex
	free := uint64(5 * machine.GiB)
	available := func() uint64 {
		mu.Lock()
		defer mu.Unlock()
		return free
	}

	lim := &limit{slots: 2}
	e := up(t, t.TempDir(), WithEnlightenWorkers(), WithCapacity(lim.capacity),
		WithMemory(available, machine.DefaultPolicy()), WithTick(5*time.Millisecond))

	repo := fakeProject(t, "")
	wish1, projectID := e.wish(t, repo)

	// Make a second wish (lower priority / rank 2).
	w2Res, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Wish Two", ProjectIds: []string{projectID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish2 := w2Res.Msg.GetWish().GetId()

	// Spawn task in Wish 1 and task in Wish 2.
	w1Task := e.mustSpawn(t, wish1, "Wish 1 Task", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"w1"}})
	e.until(t, w1Task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	w2Task := e.mustSpawn(t, wish2, "Wish 2 Task", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"w2"}})
	e.until(t, w2Task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// Lower notch to Minimal (1 slot) so Wish 2 task is paused (extra worker in lower priority wish).
	lim.set(1, "")
	minPol := machine.DefaultPolicy()
	minPol.Notch = djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL
	e.h.SetPolicy(minPol)
	e.until(t, w2Task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if e.h.Paused() != 1 {
		t.Fatalf("expected 1 paused worker, got %d", e.h.Paused())
	}

	// Now constrict available memory so memory is short for a new task.
	// Standard worker forecast is 1 GiB + 512 MiB margin = 1.5 GiB.
	// With W1 (1.5 GiB) running and W2 (1.5 GiB) paused, engaged memory is 3.0 GiB.
	// With 3.5 GiB free, another 1.5 GiB cannot fit (3.0 + 1.5 = 4.5 > 3.5 GiB), so Djinn interrupts paused W2.
	// Once W2 is interrupted, only W1 (1.5 GiB) is engaged, leaving 2.0 GiB >= 1.5 GiB for the investigator.
	mu.Lock()
	free = uint64(3500 * machine.MiB)
	mu.Unlock()

	// An investigator is triggered in Wish 1 (must start).
	q := e.asker(wish1)
	asked := q.ask(t, "Which database?")
	q.enlighten(t, asked.GetCode(), "Investigate PostgreSQL vs SQLite")

	investigators := e.roles(t, wish1, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("expected 1 investigator, got %d", len(investigators))
	}
	inv := investigators[0]

	// Wish 2 paused task must be interrupted to RESUMING with wait reason "made room for Q01: enlighten".
	// W1 is running and untouched; W2 was paused and is interrupted for memory (Q122 A).
	resumingW2 := e.until(t, w2Task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	if resumingW2 == nil {
		t.Fatal("wish 2 task was not interrupted to RESUMING")
	}
	if !strings.HasPrefix(resumingW2.GetWaitReason(), "made room for Q01: enlighten") {
		t.Errorf("wait reason = %q, want prefix 'made room for Q01: enlighten'", resumingW2.GetWaitReason())
	}
	if resumingW2.GetResumes() != 0 {
		t.Errorf("resumes = %d, want 0", resumingW2.GetResumes())
	}

	// The investigator runs in the room made for it, memory still short. Its fake worker ends within
	// milliseconds: polling for RUNNING may only ever see DONE, so wait for its end.
	if doneInv := e.ended(t, inv.GetId()); doneInv.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("investigator = %v, want DONE", doneInv)
	}

	// Free memory and restore High notch so the tasks finish.
	mu.Lock()
	free = 5 * machine.GiB
	mu.Unlock()
	lim.set(2, "")
	e.h.SetPolicy(machine.DefaultPolicy())

	// Wish 2 task resumes and finishes with 0 resumes spent.
	doneW2 := e.until(t, w2Task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if doneW2 == nil {
		t.Fatal("wish 2 task did not finish after resuming")
	}
	if doneW2.GetResumes() != 0 {
		t.Errorf("resumes = %d, want 0", doneW2.GetResumes())
	}

	// Cleanup.
	e.release(t, w1Task.GetId())
	e.ended(t, w1Task.GetId())
}

// TestNotchRuntimeAutoNeverPauses verifies Q124 (C):
// in Auto mode, lower the priority and stop starting workers, never pause running workers.
func TestNotchRuntimeAutoNeverPauses(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	lim := &limit{slots: 2}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	e.h.SetPolicy(machine.Policy{Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH})

	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s1"}})
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	w2 := e.mustSpawn(t, wishID, "Work 2", "wait", &planv1.TaskServiceSpawnRequest{WriteScopes: []string{"s2"}})
	e.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// Switch to Auto mode with Minimal notch and reduce capacity slots to 1.
	lim.set(1, "")
	e.h.SetPolicy(machine.Policy{
		Auto:  true,
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL,
	})

	// Wait a moment for any potential scheduler passes.
	time.Sleep(50 * time.Millisecond)

	// Both workers must remain RUNNING (never paused in Auto mode).
	t1 := e.get(t, w1.GetId())
	t2 := e.get(t, w2.GetId())
	if t1.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("w1 status = %v, want RUNNING", t1.GetStatus())
	}
	if t2.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Errorf("w2 status = %v, want RUNNING", t2.GetStatus())
	}
	if paused := e.h.Paused(); paused != 0 {
		t.Errorf("harness.Paused() = %d in Auto mode, want 0", paused)
	}

	// Priority is lowered even though no pause occurred.
	if !isWorkerLowPriority(e.h, w1.GetId()) || !isWorkerLowPriority(e.h, w2.GetId()) {
		t.Errorf("expected low priority on running workers in Auto mode under Minimal notch")
	}

	// Cleanup.
	e.release(t, w1.GetId())
	e.release(t, w2.GetId())
	e.ended(t, w1.GetId())
	e.ended(t, w2.GetId())
}
