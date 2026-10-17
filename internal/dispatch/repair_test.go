package dispatch

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestRepairHoldsNewWork (W233, Q44 A): while a correction repairs a wish's integration branch in a project (it is
// planned, runs, or its work waits to be merged), no new worker of the wish starts in that project, its reason naming
// the repair. The correction itself, a review, an urgent task, a question worker, a task Djinn resumes and a watcher
// still start; so do the tasks of another project or another wish. Once the repair's work is in, or the repair is
// over, the hold lifts.
func TestRepairHoldsNewWork(t *testing.T) {
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}}}
	other := &planv1.Wish{Id: "v", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := func(id, code string, edit func(*planv1.Task)) *planv1.Task {
		t := &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: code, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
		if edit != nil {
			edit(t)
		}
		return t
	}
	w118 := task("w118", "W118", func(t *planv1.Task) {
		t.Status = planv1.TaskStatus_TASK_STATUS_DONE
		t.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Branch: "feat/qol",
			CorrectedBy: "w225"}
	})
	w225 := task("w225", "W225", func(t *planv1.Task) {
		t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
		t.Correction = &planv1.TaskCorrection{Attempt: 1, Failure: &planv1.IntegrationFailure{
			State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, TaskIds: []string{"w118"}}}
	})
	work := task("work", "W226", nil)
	// Another correction, planned, would hold the work too: it is only checked to pass.
	next := task("correction", "W227", func(t *planv1.Task) { t.Correction = &planv1.TaskCorrection{Attempt: 1} })
	pass := []*planv1.Task{
		task("review", "W228", func(t *planv1.Task) { t.Review = &planv1.TaskReview{Attempt: 1} }),
		task("urgent", "W229", func(t *planv1.Task) { t.Urgent = true }),
		task("investigator", "W230", func(t *planv1.Task) { t.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR }),
		task("resumed", "W231", func(t *planv1.Task) { t.Status = planv1.TaskStatus_TASK_STATUS_RESUMING }),
		task("watcher", "W232", func(t *planv1.Task) { t.Provider = planv1.Provider_PROVIDER_WATCH }),
		task("project", "W234", func(t *planv1.Task) { t.ProjectId = "q" }),
		task("wish", "W1", func(t *planv1.Task) { t.WishId = "v" }),
	}
	tasks := append([]*planv1.Task{w118, w225, work}, pass...)
	git := map[string]bool{"p": true, "q": true}
	blocker := func() string {
		why, _ := New(tasks, []*planv1.Wish{wish, other}, git, nil).Blocker(work)
		return why
	}
	const conflict = "waits for W225 to settle the conflict in feat/qol"
	if why := blocker(); why != conflict {
		t.Errorf("work while W225 runs: %q; want %q", why, conflict)
	}
	s := New(append(tasks, next), []*planv1.Wish{wish, other}, git, nil)
	for _, p := range append(pass, next) {
		if why, failed := s.Blocker(p); why != "" || failed != "" {
			t.Errorf("%s while W225 runs: %q %q; want it started", p.GetId(), why, failed)
		}
	}

	for _, c := range []struct {
		status planv1.TaskStatus
		state  planv1.IntegrationState
		want   string
	}{
		{planv1.TaskStatus_TASK_STATUS_PENDING, 0, conflict},
		{planv1.TaskStatus_TASK_STATUS_PAUSED, 0, conflict},
		{planv1.TaskStatus_TASK_STATUS_RESUMING, 0, conflict},
		{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_PENDING, conflict},
		{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, conflict},
		{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, ""},
		{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, ""},
		{planv1.TaskStatus_TASK_STATUS_DONE, planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, ""},
		{planv1.TaskStatus_TASK_STATUS_FAILED, 0, ""},
		{planv1.TaskStatus_TASK_STATUS_CANCELLED, 0, ""},
		{planv1.TaskStatus_TASK_STATUS_STOPPED, 0, ""},
	} {
		w225.Status, w225.Integration = c.status, nil
		if c.state != 0 {
			w225.Integration = &planv1.TaskIntegration{State: c.state}
		}
		if why := blocker(); why != c.want {
			t.Errorf("work, W225 %s %s: %q; want %q", c.status, c.state, why, c.want)
		}
	}
	// A review commits the work its correction left not committed: the repair goes on.
	w225.Status = planv1.TaskStatus_TASK_STATUS_DONE
	w225.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, ReviewedBy: "review"}
	if why := blocker(); why != conflict {
		t.Errorf("work while W228 reviews W225's work: %q; want %q", why, conflict)
	}

	// A red merge, or main's merge, says so.
	w225.Status, w225.Integration = planv1.TaskStatus_TASK_STATUS_RUNNING, nil
	w225.Correction.Failure.State = planv1.IntegrationState_INTEGRATION_STATE_RED
	if why := blocker(); why != "waits for W225 to repair the red checks of feat/qol" {
		t.Errorf("work while W225 repairs red checks: %q", why)
	}
	w225.Correction.Failure = &planv1.IntegrationFailure{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, MainSha: "0f2e0240"}
	if why := blocker(); why != "waits for W225 to settle the merge of main into feat/qol" {
		t.Errorf("work while W225 settles main's merge: %q", why)
	}
}

// TestMergeTakesASlot (W233): on a full machine a correction or a review waits for a slot alone, the harness pausing a
// work task for it (Decision.Slot); a work task waits for the machine. The paused workers that resume first keep their
// slots from work, not from a merge.
func TestMergeTakesASlot(t *testing.T) {
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := func(id string, edit func(*planv1.Task)) *planv1.Task {
		t := &planv1.Task{Id: id, WishId: "w", ProjectId: id, Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING, Scheduled: true}
		edit(t)
		return t
	}
	correction := task("correction", func(t *planv1.Task) { t.Correction = &planv1.TaskCorrection{Attempt: 1} })
	review := task("review", func(t *planv1.Task) { t.Review = &planv1.TaskReview{Attempt: 1} })
	work := task("work", func(*planv1.Task) {})
	git := map[string]bool{"correction": true, "review": true, "work": true}
	for _, m := range []*Machine{{Slots: 2, Running: 2, Rule: "test"}, {Slots: 3, Running: 1, Resuming: 2, Rule: "test"}} {
		s := New([]*planv1.Task{correction, review, work}, []*planv1.Wish{wish}, git, m)
		full := m.Resuming == 0
		for _, d := range s.Pass() {
			switch id := d.Task.GetId(); {
			case id == "work" && (d.Slot || !d.Machine):
				t.Errorf("%+v: work %+v; want it waiting for the machine", m, d)
			case id != "work" && full && (!d.Slot || d.Why == ""):
				t.Errorf("%+v: %s %+v; want it waiting for a slot alone", m, id, d)
			case id != "work" && !full && d.Why != "":
				t.Errorf("%+v: %s %+v; want it started before the paused workers", m, id, d)
			}
		}
	}
	// Under pressure, none starts.
	s := New([]*planv1.Task{correction}, []*planv1.Wish{wish}, git, &Machine{Slots: 2, Pressure: "simulated", Rule: "test"})
	if d := s.Pass()[0]; d.Slot || d.Why == "" {
		t.Errorf("a correction under pressure: %+v", d)
	}
}
