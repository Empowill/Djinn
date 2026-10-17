package harness

import (
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// settleReadme makes the fake agent of the test's correction workers settle W2's conflict in README.md, then wait to
// be let go: the conflict stays under repair until the test says.
func (in *integration) settleReadme(t *testing.T) {
	in.correctWith(func(dir, prompt string) string {
		if !strings.Contains(prompt, "could not integrate the work of W2") {
			return ""
		}
		writeFile(t, dir, "README.md", "# One and Two\n")
		commitAll(t, dir, "Settle the readme") // Which concludes the merge.
		return "wait\ntext settled"
	})
}

// TestConflictHoldsNewWork (W233, Q44 A): while a merge of the wish is in conflict and its correction repairs it, no
// new worker of the wish starts, its wait reason naming the repair; the correction itself and an urgent task start.
// Once the correction's work is in, the work held starts, from the repaired branch.
func TestConflictHoldsNewWork(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.settleReadme(t)
	in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
	in.pass(t, 0) // W1 goes in; W2 conflicts with it, and its correction starts.
	got, _ := in.integration(t, w2)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetCorrectedBy() == "" {
		t.Fatalf("W2's integration %v; want a conflict, corrected", got)
	}
	fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	planned := in.mustSpawn(t, in.wishID, "Planned", "text planned", nil)
	want := "waits for " + fix.GetCode() + " to settle the conflict in " + in.branch
	if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
		t.Fatalf("new work while %s settles the conflict: %v; want it pending, %q", fix.GetCode(), got, want)
	}
	urgent := in.mustSpawn(t, in.wishID, "Urgent", "text urgent", &planv1.TaskServiceSpawnRequest{Urgent: true})
	if got := in.ended(t, urgent.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("the urgent task while %s settles the conflict: %v; want it run", fix.GetCode(), got)
	}
	if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
		t.Errorf("new work after the urgent task: %v; want it still held", got)
	}

	// The correction ends done: its work waits to be merged, the branch still under repair.
	in.release(t, fix.GetId())
	in.ended(t, fix.GetId())
	if got := in.get(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || got.GetWaitReason() != want {
		t.Errorf("new work before the repair is in: %v; want it still held", got)
	}
	// Its work in, the conflict is settled: the work held starts.
	in.pass(t, time.Second)
	if got := in.states(t, w2, fix); got != "W2 COMMITTED, "+fix.GetCode()+" COMMITTED" {
		t.Fatalf("after the repair: %s", got)
	}
	if got := in.ended(t, planned.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("the work held, once the conflict is settled: %v", got)
	}
}

// TestMergeTakesTheSlotOfWork (W233): on a full machine, the correction of a conflict takes the slot of the running
// work task it pauses, at once, instead of waiting for it; that task resumes once the correction ends.
func TestMergeTakesTheSlotOfWork(t *testing.T) {
	testx.Portable(t)
	lim := &limit{slots: 1}
	in := integrating(t, WithCapacity(lim.capacity))
	in.settleReadme(t)
	in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
	work := in.mustSpawn(t, in.wishID, "Work", "wait", nil) // It takes the one slot.
	in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))

	in.pass(t, 0) // W1 goes in; W2 conflicts with it, and its correction starts on the full machine.
	got, _ := in.integration(t, w2)
	if got.GetCorrectedBy() == "" {
		t.Fatalf("W2's integration %v; want it corrected", got)
	}
	fix := in.until(t, got.GetCorrectedBy(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_PAUSED))
	if !slices.ContainsFunc(in.storedEvents(t, fix.GetId()), func(ev *planv1.TaskEvent) bool {
		return ev.GetText() == "preempted "+work.GetCode()+" to make room"
	}) {
		t.Errorf("%s's events lack the preemption of %s", fix.GetCode(), work.GetCode())
	}

	in.release(t, fix.GetId())
	in.ended(t, fix.GetId())
	in.until(t, work.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	in.release(t, work.GetId())
	in.ended(t, work.GetId())
}
