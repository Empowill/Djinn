package harness

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
)

// TestPreemptForSlot: when all slots are taken, starting an investigator pauses the running work task
// of lowest priority to make room. When the investigator ends, that task resumes.
func TestPreemptForSlot(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	lim := &limit{slots: 1}
	e := up(t, t.TempDir(), WithEnlightenWorkers(), WithCapacity(lim.capacity), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	// Work task takes the one slot.
	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", nil)
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	q := e.asker(wishID)
	asked := q.ask(t, "Which database?")
	q.enlighten(t, asked.GetCode(), "Investigate PostgreSQL vs SQLite")

	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("expected 1 investigator, got %d", len(investigators))
	}
	inv := investigators[0]

	// W1 must be paused to make room for the investigator.
	pausedW1 := e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if pausedW1 == nil {
		t.Fatal("w1 was not paused")
	}

	// Investigator is running.
	runningInv := e.until(t, inv.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if runningInv == nil {
		t.Fatal("investigator did not start running")
	}

	// Verify events on both tasks.
	w1Events := e.storedEvents(t, w1.GetId())
	if !slices.ContainsFunc(w1Events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "paused: made room for Q01: enlighten")
	}) {
		t.Errorf("w1 missing paused event: %v", w1Events)
	}

	invEvents := e.storedEvents(t, inv.GetId())
	if !slices.ContainsFunc(invEvents, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "preempted "+w1.GetCode()+" to make room")
	}) {
		t.Errorf("inv missing preempted event: %v", invEvents)
	}

	// Release the investigator and wait for it to end.
	e.release(t, inv.GetId())
	e.ended(t, inv.GetId())

	// When investigator ends, W1 is unpaused and resumes running.
	resumedW1 := e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if resumedW1 == nil {
		t.Fatal("w1 did not resume running after investigator ended")
	}

	// Release W1 so it ends cleanly.
	e.release(t, w1.GetId())
	e.ended(t, w1.GetId())
}

// TestPreemptForMemory: when memory is short, an investigator interrupts the lowest running work task
// so it resumes later in its session, without consuming resumes.
func TestPreemptForMemory(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	var mu sync.Mutex
	free := uint64(2 * machine.GiB)
	available := func() uint64 {
		mu.Lock()
		defer mu.Unlock()
		return free
	}

	e := up(t, t.TempDir(), WithEnlightenWorkers(), WithCapacity((&limit{slots: 4}).capacity),
		WithMemory(available, machine.DefaultPolicy()), WithTick(20*time.Millisecond))
	wishID, _ := e.wish(t, fakeProject(t, ""))

	// Work task starts and takes 1 GiB peak forecast + 512 MiB margin = 1.5 GiB.
	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", nil)
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// An investigator is triggered. With 2 GiB free, another 1.5 GiB cannot fit, so memory is short.
	q := e.asker(wishID)
	asked := q.ask(t, "Which cache?")
	q.enlighten(t, asked.GetCode(), "Investigate Redis vs Memcached")

	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("expected 1 investigator, got %d", len(investigators))
	}
	inv := investigators[0]

	// W1 must be interrupted to RESUMING with reason "made room for Q01: enlighten".
	resumingW1 := e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RESUMING))
	if resumingW1 == nil {
		t.Fatal("w1 was not interrupted to RESUMING")
	}
	if !strings.HasPrefix(resumingW1.GetWaitReason(), "made room for Q01: enlighten") {
		t.Errorf("w1 wait reason = %q, want 'made room for Q01: enlighten'", resumingW1.GetWaitReason())
	}
	if resumingW1.GetResumes() != 0 {
		t.Errorf("w1 resumes = %d, want 0", resumingW1.GetResumes())
	}

	// Investigator starts running.
	runningInv := e.until(t, inv.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	if runningInv == nil {
		t.Fatal("investigator did not start running")
	}

	invEvents := e.storedEvents(t, inv.GetId())
	if !slices.ContainsFunc(invEvents, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS &&
			strings.Contains(ev.GetText(), "preempted "+w1.GetCode()+" to make room")
	}) {
		t.Errorf("inv missing preempted event: %v", invEvents)
	}

	// Increase memory to allow W1 to restart once investigator finishes.
	mu.Lock()
	free = 5 * machine.GiB
	mu.Unlock()

	// Release investigator and wait for it to finish.
	e.release(t, inv.GetId())
	e.ended(t, inv.GetId())

	// Scheduler relaunches W1 without spending a resume attempt.
	// Told why (preemptLine), the fake worker finishes, writing TASK_STATUS_DONE.
	restartedW1 := e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
	if restartedW1 == nil {
		t.Fatal("w1 did not finish after resuming")
	}
	if restartedW1.GetResumes() != 0 {
		t.Errorf("w1 resumes = %d, want 0 (none spent for preemption)", restartedW1.GetResumes())
	}

	w1Events := e.storedEvents(t, w1.GetId())
	for _, want := range []string{"resuming: made room for Q01: enlighten", "resumed " + byPreempt + ": started fake", preemptLine} {
		if !slices.ContainsFunc(w1Events, func(ev *planv1.TaskEvent) bool {
			return strings.Contains(ev.GetText(), want)
		}) {
			t.Errorf("w1 missing event %q:\n%v", want, eventTexts(w1Events))
		}
	}
}

// TestPreemptExclusions: gate holders, correction workers, and review workers are never preempted.
func TestPreemptExclusions(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	e := up(t, t.TempDir(), WithEnlightenWorkers())
	wishID, _ := e.wish(t, fakeProject(t, ""))

	task := e.mustSpawn(t, wishID, "Work", "wait", nil)
	e.until(t, task.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// 1. Task holding a gate is never preempted.
	e.h.HeldGates(func(taskID string) []string {
		if taskID == task.GetId() {
			return []string{"build-gate"}
		}
		return nil
	}, nil)

	cand, _ := e.h.lowestWorkTask(t.Context(), true)
	if cand != nil {
		t.Errorf("gate holder was chosen for preemption: %v", cand.GetCode())
	}

	// 2. Task waiting for a gate is never preempted.
	e.h.HeldGates(nil, func(taskID string) []string {
		if taskID == task.GetId() {
			return []string{"build-gate"}
		}
		return nil
	})

	cand, _ = e.h.lowestWorkTask(t.Context(), true)
	if cand != nil {
		t.Errorf("gate waiter was chosen for preemption: %v", cand.GetCode())
	}

	// Clear held gates.
	e.h.HeldGates(nil, nil)

	// Now it would be chosen.
	cand, _ = e.h.lowestWorkTask(t.Context(), true)
	if cand == nil || cand.GetId() != task.GetId() {
		t.Fatalf("expected task to be chosen once gate cleared, got %v", cand)
	}

	// 3. Correction worker is never preempted.
	e.h.mu.Lock()
	e.h.runs[task.GetId()].task.Correction = &planv1.TaskCorrection{Attempt: 1}
	e.h.mu.Unlock()

	cand, _ = e.h.lowestWorkTask(t.Context(), true)
	if cand != nil {
		t.Errorf("correction worker was chosen for preemption: %v", cand.GetCode())
	}

	// 4. Review worker is never preempted.
	e.h.mu.Lock()
	e.h.runs[task.GetId()].task.Correction = nil
	e.h.runs[task.GetId()].task.Review = &planv1.TaskReview{Attempt: 1}
	e.h.mu.Unlock()

	cand, _ = e.h.lowestWorkTask(t.Context(), true)
	if cand != nil {
		t.Errorf("review worker was chosen for preemption: %v", cand.GetCode())
	}

	e.release(t, task.GetId())
	e.ended(t, task.GetId())
}

// TestPreemptWindowsNeverPauses: Windows refuses pausing, so slot preemption returns nil.
func TestPreemptWindowsNeverPauses(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	e := up(t, t.TempDir(), WithEnlightenWorkers())
	wishID, _ := e.wish(t, fakeProject(t, ""))

	w1 := e.mustSpawn(t, wishID, "Work 1", "wait", nil)
	e.until(t, w1.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// Simulate Windows
	e.h.goos = func() string { return "windows" }

	// forSlot = true (slot preemption via pause) must return nil on Windows.
	cand, _ := e.h.lowestWorkTask(t.Context(), true)
	if cand != nil {
		t.Errorf("Windows allowed slot preemption pause: %v", cand.GetCode())
	}

	// forSlot = false (memory preemption via interrupt) is allowed on Windows.
	candMem, _ := e.h.lowestWorkTask(t.Context(), false)
	if candMem == nil || candMem.GetId() != w1.GetId() {
		t.Errorf("Windows should allow memory preemption interrupt: %v", candMem)
	}

	e.release(t, w1.GetId())
	e.ended(t, w1.GetId())
}
