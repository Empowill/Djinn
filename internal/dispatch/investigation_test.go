package dispatch

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// investigationCase is a wish whose integration branch's push checks are red, a work task running, and planned tasks
// as made.
func investigationCase(now time.Time, made ...func() *planv1.Task) ([]*planv1.Task, *planv1.Wish) {
	wish := &planv1.Wish{
		Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE,
		IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: "p", Branch: "feat/qol"}},
		Pushes:              []*planv1.WishPush{{ProjectId: "p", Held: "at 0f2e0240, test: exit status 1", FailedSha: "0f2e02401234"}},
	}
	running := &planv1.Task{Id: "running", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Scheduled: true, CreateTime: timestamppb.New(now.Add(-3 * time.Hour))}
	tasks := []*planv1.Task{running}
	for _, m := range made {
		tasks = append(tasks, m())
	}
	return tasks, wish
}

func planned(id string, at time.Time, kind planv1.TaskKind) *planv1.Task {
	return &planv1.Task{Id: id, WishId: "w", ProjectId: "p", Code: id, Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Scheduled: true, CreateTime: timestamppb.New(at), Kind: kind}
}

// TestInvestigationServedFirst (W213): on a full machine and while the integration branch's push checks are red, an
// investigation goes before the older planned work and waits for a slot alone (Decision.Slot: the harness pauses a
// work task for it, as for an investigator), while the work waits for the branch to be green again.
func TestInvestigationServedFirst(t *testing.T) {
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	tasks, wish := investigationCase(now,
		func() *planv1.Task { return planned("work", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
		func() *planv1.Task { return planned("study", now, planv1.TaskKind_TASK_KIND_INVESTIGATION) })
	m := &Machine{Slots: 1, Rule: "test", Running: 1, Resuming: 1}
	decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass()
	if got := decisions[0].Task.GetId(); got != "study" {
		t.Fatalf("first = %s; want the investigation before the older work", got)
	}
	if d := decisions[0]; !d.Slot || !strings.Contains(d.Why, "the most this machine holds") {
		t.Errorf("study: slot %v, why %q; want it waiting for a slot alone, a work task's to take", d.Slot, d.Why)
	}
	if d := decisions[1]; !strings.HasPrefix(d.Why, "waits for feat/qol to be green again") {
		t.Errorf("work: why %q; want it held by the red push checks", d.Why)
	}
	// A free slot: it starts at once, on the red branch.
	m = &Machine{Slots: 4, Rule: "test", Running: 1}
	for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass() {
		if d.Task.GetId() == "study" && (d.Why != "" || d.Failed != "") {
			t.Errorf("study waits on a free machine and a red branch: %q %q", d.Why, d.Failed)
		}
	}
}

// TestInvestigationCap (W213): at most one investigation runs at once on a machine of three slots or fewer, two above;
// the next one waits for the machine, saying so, whatever the free slots.
func TestInvestigationCap(t *testing.T) {
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	if InvestigationCap(1) != 1 || InvestigationCap(3) != 1 || InvestigationCap(4) != 2 || InvestigationCap(16) != 2 {
		t.Errorf("InvestigationCap = %d %d %d %d", InvestigationCap(1), InvestigationCap(3), InvestigationCap(4), InvestigationCap(16))
	}
	study := func(id string, age time.Duration) func() *planv1.Task {
		return func() *planv1.Task { return planned(id, now.Add(-age), planv1.TaskKind_TASK_KIND_INVESTIGATION) }
	}
	tasks, wish := investigationCase(now, study("a", 3*time.Minute), study("b", 2*time.Minute), study("c", time.Minute))
	for _, c := range []struct {
		slots   int
		started []string
	}{{3, []string{"a"}}, {8, []string{"a", "b"}}} {
		var started []string
		for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: c.slots, Rule: "test", Running: 1}).At(now).Pass() {
			switch {
			case d.Why == "":
				started = append(started, d.Task.GetId())
			case !d.Machine || !strings.Contains(d.Why, "the most investigations this machine runs at once"):
				t.Errorf("%d slots, %s: why %q, machine %v", c.slots, d.Task.GetId(), d.Why, d.Machine)
			}
		}
		if strings.Join(started, ",") != strings.Join(c.started, ",") {
			t.Errorf("%d slots: started %v, want %v", c.slots, started, c.started)
		}
	}
	// One running counts: the next waits on a machine of three slots.
	tasks[1].Status = planv1.TaskStatus_TASK_STATUS_RUNNING
	for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, &Machine{Slots: 3, Rule: "test", Running: 2}).At(now).Pass() {
		if d.Why == "" {
			t.Errorf("%s starts beside a running investigation on 3 slots", d.Task.GetId())
		}
	}
}

// TestInvestigationDependency (W213): a task after a done investigation does not wait for its work to reach the
// integration branch: it never will.
func TestInvestigationDependency(t *testing.T) {
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	study := planned("study", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_INVESTIGATION)
	study.Status, study.Branch = planv1.TaskStatus_TASK_STATUS_DONE, "w2-study"
	next := planned("next", now, planv1.TaskKind_TASK_KIND_WORK)
	next.DependsOn = []string{"study"}
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	if why, failed := New([]*planv1.Task{study, next}, []*planv1.Wish{wish}, map[string]bool{"p": true}, nil).Blocker(next); why != "" || failed != "" {
		t.Errorf("next: %q %q; want it free once the investigation is done", why, failed)
	}
}
