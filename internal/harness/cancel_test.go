package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestCancelRunning: cancel a running task stops its worker, removes its worktree,
// keeps its branch, and never queues its work for merging.
func TestCancelRunning(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	task := in.mustSpawn(t, in.wishID, "Running Task", "wait", nil)
	id := task.GetId()

	waitFor(t, "task running", func() bool {
		return in.get(t, id).GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})

	cur := in.get(t, id)
	wt := cur.GetWorktree()
	branch := cur.GetBranch()
	if wt == "" || branch == "" {
		t.Fatalf("want worktree and branch, got wt=%q branch=%q", wt, branch)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree does not exist: %v", err)
	}
	if out := in.git(t, in.repo, "branch", "--list", branch); out == "" {
		t.Fatalf("branch %s does not exist", branch)
	}

	res, err := in.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
		TaskId: id,
		Note:   "no longer needed",
		By:     planv1.Closer_CLOSER_LEAD,
	}))
	if err != nil {
		t.Fatalf("cancel running task: %v", err)
	}

	got := res.Msg.GetTask()
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_CANCELLED {
		t.Errorf("status = %v; want CANCELLED", got.GetStatus())
	}
	if c := got.GetCancelled(); c == nil || c.GetActor() != planv1.Closer_CLOSER_LEAD || c.GetNote() != "no longer needed" || c.GetCreateTime() == nil {
		t.Errorf("cancelled info = %v", c)
	}

	// Worker is stopped.
	if in.h.running(id) {
		t.Errorf("worker still running after cancel")
	}

	// Worktree is gone.
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree was not removed: %v", err)
	}
	if stored := in.get(t, id); stored.GetWorktree() != "" {
		t.Errorf("task worktree field not cleared: %q", stored.GetWorktree())
	}

	// Branch is kept.
	if out := in.git(t, in.repo, "branch", "--list", branch); out == "" {
		t.Errorf("branch %s was removed", branch)
	}

	// Never queues for merging and not in queue.
	if stored := in.get(t, id); stored.GetIntegration() != nil {
		t.Errorf("integration not nil: %v", stored.GetIntegration())
	}
	in.pass(t, time.Second)
	if stored := in.get(t, id); stored.GetIntegration() != nil {
		t.Errorf("integration queued after pass: %v", stored.GetIntegration())
	}
	if stored := in.get(t, id); stored.GetStatus() != planv1.TaskStatus_TASK_STATUS_CANCELLED {
		t.Errorf("status changed after pass: %v", stored.GetStatus())
	}
}

// TestCancelReleasesDependent: cancelling a dependency releases its dependents,
// and puts an event on each dependent naming the cancelled dependency.
func TestCancelReleasesDependent(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, nativeFolder(t))

	a := e.mustSpawn(t, wishID, "Task A", "wait", nil)
	b := e.mustSpawn(t, wishID, "Task B", "text hello from B", &planv1.TaskServiceSpawnRequest{
		DependsOn: []string{a.GetCode()},
	})

	waitFor(t, "A running", func() bool {
		return e.get(t, a.GetId()).GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING
	})
	bTask := e.get(t, b.GetId())
	if bTask.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
		t.Fatalf("B status = %v; want PENDING", bTask.GetStatus())
	}

	_, err := e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
		TaskId: a.GetId(),
		Note:   "abandoned A",
	}))
	if err != nil {
		t.Fatalf("cancel A: %v", err)
	}

	// Check event on B naming cancelled dependency.
	wantText := fmt.Sprintf("dependency %s was cancelled", a.GetCode())
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": b.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ev := range events {
		if ev.GetText() == wantText {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("event %q not found on B; got: %v", wantText, events)
	}

	// B unblocks and finishes done.
	waitFor(t, "B done", func() bool {
		return e.get(t, b.GetId()).GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE
	})
}

// TestCancelCorrectionReleasingHeldPush: cancelling a push correction worker releases the held push, so, the branch
// fixed by hand meanwhile, push checks run and push proceeds when green.
func TestCancelCorrectionReleasingHeldPush(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.checking(t, "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
		"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
	in.correctWith(func(dir, prompt string) string {
		return "wait\n" + prompt
	})
	in.testCode = 1
	bare := in.remote(t)
	old := in.tip(t)
	azima := in.azima(t, in.wishID, "Letters")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })

	in.pass(t, 0)
	if got := in.remoteTip(t, bare); got != old {
		t.Fatalf("pushed red: origin at %s", got)
	}
	state := in.pushState(t)
	if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
		t.Fatalf("the push %v; want held and corrected by a worker", state)
	}
	w2ID := state.GetCorrectedBy()
	in.until(t, w2ID, isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	// Cancel the correction worker via task service.
	res, err := in.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
		TaskId: w2ID,
		Note:   "cancelled correction",
	}))
	if err != nil {
		t.Fatalf("cancel correction worker: %v", err)
	}
	if res.Msg.GetTask().GetStatus() != planv1.TaskStatus_TASK_STATUS_CANCELLED {
		t.Errorf("cancelled task status: %v", res.Msg.GetTask().GetStatus())
	}

	if got := in.pushState(t).GetCorrectedBy(); got != "" {
		t.Errorf("after cancelling correction worker, push corrected_by = %q; want empty", got)
	}

	in.mu.Lock()
	in.testCode = 0
	in.mu.Unlock()
	in.handCommit(t) // The developer fixed it, and cancelled the correction.

	in.pass(t, time.Second)
	tip := in.tip(t)
	if got := in.remoteTip(t, bare); got != tip {
		t.Fatalf("push checks passed, not pushed: origin at %s, want %s", got, tip)
	}
	if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
		t.Errorf("after push succeeded, push state: %v", state)
	}
}

// TestCancelPendingAndRefusals: cancelling a pending task records cancellation and leaves it cancelled; cancel is
// refused on a task done, cancelled already, or on an azima; djinn task done is refused on a cancelled task; and an
// azima is done without its cancelled parts.
func TestCancelPendingAndRefusals(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	lim := &limit{slots: 0}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity))
	wishID, _ := e.wish(t, nativeFolder(t))

	t.Run("pending", func(t *testing.T) {
		task := e.mustSpawn(t, wishID, "Pending Task", "text hello", &planv1.TaskServiceSpawnRequest{Later: true})
		if task.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			t.Fatalf("want pending, got %v", task.GetStatus())
		}
		res, err := e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: task.GetId(),
			Note:   "cancelled while pending",
			By:     planv1.Closer_CLOSER_DEVELOPER,
		}))
		if err != nil {
			t.Fatalf("cancel pending task: %v", err)
		}
		got := e.get(t, task.GetId())
		if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_CANCELLED {
			t.Fatalf("status = %v, want CANCELLED", got.GetStatus())
		}
		if c := got.GetCancelled(); c == nil || c.GetActor() != planv1.Closer_CLOSER_DEVELOPER || c.GetNote() != "cancelled while pending" {
			t.Errorf("cancelled = %v", c)
		}
		if res.Msg.GetTask().GetCancelled() == nil {
			t.Errorf("response task has no cancelled info")
		}
		e.h.schedule(t.Context())
		if got := e.get(t, task.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_CANCELLED {
			t.Errorf("status after wake = %v, want CANCELLED", got.GetStatus())
		}
	})

	t.Run("refusals", func(t *testing.T) {
		lim.set(1, "")
		azima := e.azima(t, wishID, "An Azima")
		_, err := e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: azima.GetId(),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "cannot cancel an azima") {
			t.Errorf("cancel azima error: %v", err)
		}

		doneTask := e.ended(t, e.mustSpawn(t, wishID, "Done Task", "text ok", nil).GetId())
		_, err = e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: doneTask.GetId(),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "done already") {
			t.Errorf("cancel done error: %v", err)
		}

		toCancel := e.ended(t, e.mustSpawn(t, wishID, "To Cancel", "fail broke", nil).GetId())
		_, err = e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: toCancel.GetId(),
		}))
		if err != nil {
			t.Fatalf("first cancel: %v", err)
		}
		_, err = e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: toCancel.GetId(),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "cancelled already") {
			t.Errorf("cancel cancelled error: %v", err)
		}

		_, err = e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{
			TaskId: toCancel.GetId(),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "cancelled already") {
			t.Errorf("done on cancelled error: %v", err)
		}
	})

	t.Run("azima_done_without_cancelled_parts", func(t *testing.T) {
		lim.set(0, "")
		azima := e.azima(t, wishID, "Feature Goal")
		p1 := e.mustSpawn(t, wishID, "Part 1", "text part 1", &planv1.TaskServiceSpawnRequest{Later: true, PartOf: azima.GetCode()})
		p2 := e.mustSpawn(t, wishID, "Part 2", "text part 2", &planv1.TaskServiceSpawnRequest{Later: true, PartOf: azima.GetCode()})

		if _, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: p1.GetId()})); err != nil {
			t.Fatal(err)
		}
		_, err := e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{
			TaskId: azima.GetId(),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "has parts still to finish") {
			t.Fatalf("marking azima done with unfinished part: %v", err)
		}
		_, err = e.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{
			TaskId: p2.GetId(),
			Note:   "not needed",
		}))
		if err != nil {
			t.Fatalf("cancel p2: %v", err)
		}
		_, err = e.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{
			TaskId: azima.GetId(),
		}))
		if err != nil {
			t.Fatalf("marking azima done with cancelled part: %v", err)
		}
		if got := e.get(t, azima.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("azima status = %v, want DONE", got.GetStatus())
		}
	})
}
