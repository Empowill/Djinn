package dispatch

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// TestUrgentPassesTheQueue (W133): an urgent task goes before every older waiting task, a correction and one that
// unblocks others included, and takes a slot on a full machine, under pressure or short of memory, as djinn task
// start does; the tasks after it keep waiting.
func TestUrgentPassesTheQueue(t *testing.T) {
	now := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	task := func(id string, age time.Duration) *planv1.Task {
		return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, CreateTime: timestamppb.New(now.Add(-age))}
	}
	running := task("running", 3*time.Hour)
	running.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
	old, blocker, correction := task("old", 2*time.Hour), task("blocker", time.Hour), task("correction", 30*time.Minute)
	correction.Correction = &planv1.TaskCorrection{Attempt: 1}
	child := task("child", 10*time.Minute)
	child.DependsOn = []string{"blocker"}
	urgent := task("urgent", 0)
	urgent.Urgent = true
	tasks := []*planv1.Task{running, old, blocker, correction, child, urgent}
	git := map[string]bool{"p": true}

	for name, m := range map[string]*Machine{
		"full":           {Slots: 1, Rule: "test", Running: 1},
		"under pressure": {Slots: 4, Rule: "test", Running: 1, Pressure: "memory at 95%"},
		"short of memory": {Slots: 4, Rule: "test", Running: 1, Available: machine.GiB / 4, Total: 8 * machine.GiB,
			Policy: machine.DefaultPolicy()},
	} {
		t.Run(name, func(t *testing.T) {
			decisions := New(tasks, []*planv1.Wish{wish}, git, m).At(now).Pass()
			if got := decisions[0].Task.GetId(); got != "urgent" {
				t.Errorf("first = %s, want the urgent task before the older ones", got)
			}
			for _, d := range decisions {
				switch id := d.Task.GetId(); {
				case id == "urgent" && (d.Why != "" || d.Failed != ""):
					t.Errorf("urgent waits: %q %q", d.Why, d.Failed)
				case id != "urgent" && d.Why == "":
					t.Errorf("%s starts; want it waiting behind the urgent one", id)
				}
			}
			if why, failed := New(tasks, []*planv1.Wish{wish}, git, m).At(now).Blocker(urgent); why != "" || failed != "" {
				t.Errorf("Blocker(urgent) = %q, %q; want none", why, failed)
			}
		})
	}

	// Done, it is history: the order and the slots are the others' again.
	done := task("urgent", 0)
	done.Urgent, done.Status = true, planv1.TaskStatus_TASK_STATUS_DONE
	if Urgent(done) || UrgentOf([]*planv1.Task{done}, "w", "", false) != nil {
		t.Error("a done task counts as urgent")
	}
	if UrgentOf(tasks, "w", "", false) != urgent || UrgentOf(tasks, "w", "urgent", false) != nil || UrgentOf(tasks, "other", "", false) != nil {
		t.Error("UrgentOf does not find the wish's urgent task alone")
	}
}

// TestUrgentStartsOnARedBranch (W133): once the push checks' corrections are spent (the push held, no correction
// working), every planned task of the wish waits for the branch to be green again, except the urgent task the lead
// marks to repair it: nothing else could ever make it green.
func TestUrgentStartsOnARedBranch(t *testing.T) {
	wish := &planv1.Wish{
		Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
		Pushes: []*planv1.WishPush{{
			ProjectId: "p", Held: "at 0f2e0240, test: exit status 1; left to the lead", FailedSha: "0f2e02401234", Attempts: 2,
			Failure: &planv1.IntegrationFailure{Reason: "test: exit status 1", Base: "0f2e02401234"},
		}},
	}
	work := &planv1.Task{Id: "work", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
	repair := &planv1.Task{Id: "repair", WishId: "w", ProjectId: "p", Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Scheduled: true, Urgent: true}
	git := map[string]bool{"p": true}
	sit := New([]*planv1.Task{work, repair}, []*planv1.Wish{wish}, git, &Machine{Slots: 4, Rule: "test"})
	if why, _ := sit.Blocker(work); why != "waits for feat/qol to be green again (push checks red at 0f2e0240: test)" {
		t.Errorf("work: %q", why)
	}
	if why, failed := sit.Blocker(repair); why != "" || failed != "" {
		t.Errorf("urgent repair: %q %q; want it started on the red branch", why, failed)
	}
}

// TestUrgentRepairAndFeedback (W213): a repair and a task for the developer's quick feedback may be urgent at once in a
// wish, one of each class; both pass the queue, the repair first, and both start on a full machine and a red branch.
func TestUrgentRepairAndFeedback(t *testing.T) {
	wish := &planv1.Wish{
		Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
		Pushes:              []*planv1.WishPush{{ProjectId: "p", Held: "at 0f2e0240, test: exit status 1", FailedSha: "0f2e02401234"}},
	}
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	task := func(id string, age time.Duration) *planv1.Task {
		return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
			Scheduled: true, CreateTime: timestamppb.New(now.Add(-age))}
	}
	work, mockup, repair := task("work", time.Hour), task("mockup", 30*time.Minute), task("repair", 0)
	mockup.Urgent, mockup.Feedback = true, true
	repair.Urgent = true
	tasks := []*planv1.Task{work, mockup, repair}
	decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: 1, Rule: "test", Running: 1}).At(now).Pass()
	var order []string
	for _, d := range decisions {
		order = append(order, d.Task.GetId())
		if urgent := d.Task.GetId() != "work"; urgent != (d.Why == "") {
			t.Errorf("%s: why %q", d.Task.GetId(), d.Why)
		}
	}
	if order[0] != "repair" || order[1] != "mockup" {
		t.Errorf("order = %v; want the repair, then the feedback task, before the work", order)
	}
	if !Feedback(mockup) || Feedback(repair) {
		t.Error("Feedback does not tell the feedback task from the repair")
	}
	if UrgentOf(tasks, "w", "", true) != mockup || UrgentOf(tasks, "w", "", false) != repair ||
		UrgentOf(tasks, "w", "mockup", true) != nil {
		t.Error("UrgentOf does not find each class's urgent task")
	}
}
