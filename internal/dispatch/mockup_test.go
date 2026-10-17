package dispatch

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// mockupHolds are the holds of the other workers' state that hold a planned work task and never a mockup (W268): the
// wish's red push checks, a merge in conflict that a correction settles, red checks that one repairs.
func mockupHolds(now time.Time) map[string]func(*planv1.Wish, []*planv1.Task) []*planv1.Task {
	repair := func(state planv1.IntegrationState) func(*planv1.Wish, []*planv1.Task) []*planv1.Task {
		return func(w *planv1.Wish, tasks []*planv1.Task) []*planv1.Task {
			w.Pushes = nil
			return append(tasks, &planv1.Task{Id: "fix", WishId: "w", ProjectId: "p", Code: "W9", Scheduled: true,
				Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: timestamppb.New(now.Add(-time.Hour)),
				Correction: &planv1.TaskCorrection{Attempt: 1, Failure: &planv1.IntegrationFailure{State: state}}})
		}
	}
	return map[string]func(*planv1.Wish, []*planv1.Task) []*planv1.Task{
		"red branch":     func(_ *planv1.Wish, tasks []*planv1.Task) []*planv1.Task { return tasks },
		"conflict hold":  repair(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT),
		"repair running": repair(planv1.IntegrationState_INTEGRATION_STATE_RED),
	}
}

// TestMockupNotHeld (W268): a mockup changes no code of its project, so no hold of the other workers' state applies
// to it: on a red branch, while a correction settles a merge's conflict, or while one repairs red checks, the work
// waits and the mockup starts, at the clock the pass reads.
func TestMockupNotHeld(t *testing.T) {
	now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
	for name, hold := range mockupHolds(now) {
		t.Run(name, func(t *testing.T) {
			tasks, wish := investigationCase(now,
				func() *planv1.Task { return planned("work", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
				func() *planv1.Task { return planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP) })
			tasks = hold(wish, tasks)
			m := &Machine{Slots: 4, Rule: "test", Running: 2}
			decisions := New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass()
			if got := decisions[0].Task.GetId(); got != "mock" {
				t.Fatalf("first = %s; want the mockup before the older work", got)
			}
			if d := decisions[0]; d.Why != "" || d.Failed != "" {
				t.Errorf("mock: %q %q; want it started", d.Why, d.Failed)
			}
			if d := decisions[1]; !strings.HasPrefix(d.Why, "waits for ") {
				t.Errorf("work: why %q; want it held", d.Why)
			}
		})
	}
}

// TestMockupWriteScope (W268): outside Git, a mockup is held by no write scope another worker takes.
func TestMockupWriteScope(t *testing.T) {
	now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
	wish := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	writer := &planv1.Task{Id: "writer", WishId: "w", ProjectId: "p", Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_PAUSED,
		Scheduled: true, CreateTime: timestamppb.New(now.Add(-time.Hour))}
	work := planned("work", now.Add(-time.Minute), planv1.TaskKind_TASK_KIND_WORK)
	mock := planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP)
	s := New([]*planv1.Task{writer, work, mock}, []*planv1.Wish{wish}, map[string]bool{"p": false}, nil).At(now)
	if why, _ := s.Held(work); !strings.Contains(why, "overlaps") {
		t.Errorf("work: %q; want it held by W1's scope", why)
	}
	if why, failed := s.Held(mock); why != "" || failed != "" {
		t.Errorf("mock: %q %q; want no scope to hold it", why, failed)
	}
}

// TestMockupServedFirst (W268, W213, W234): a mockup takes an investigation's place, after the investigators and
// beside the investigations, within their cap; marked urgent for feedback, it goes before them all, after the urgent
// repair. A task after a done mockup waits for no merge of its work.
func TestMockupServedFirst(t *testing.T) {
	now := time.Date(2026, 10, 17, 14, 2, 0, 0, time.UTC)
	mark := func(task *planv1.Task, feedback bool) *planv1.Task {
		task.Urgent, task.Feedback = true, feedback
		return task
	}
	tasks, wish := investigationCase(now,
		func() *planv1.Task { return planned("work", now.Add(-2*time.Hour), planv1.TaskKind_TASK_KIND_WORK) },
		func() *planv1.Task {
			return planned("study", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_INVESTIGATION)
		},
		func() *planv1.Task { return planned("mock", now, planv1.TaskKind_TASK_KIND_MOCKUP) },
		func() *planv1.Task {
			t := planned("enlighten", now, planv1.TaskKind_TASK_KIND_UNSPECIFIED)
			t.Role = planv1.TaskRole_TASK_ROLE_INVESTIGATOR
			return t
		})
	order := func() string {
		var ids []string
		for _, t := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, nil).At(now).Order() {
			ids = append(ids, t.GetId())
		}
		return strings.Join(ids, ",")
	}
	if got := order(); got != "enlighten,study,mock,work" {
		t.Errorf("order = %s; want the investigator, then the investigation and the mockup by age, then the work", got)
	}
	// One investigation runs: on three slots, the mockup waits for the cap they share.
	m := &Machine{Slots: 3, Rule: "test", Running: 1}
	for _, d := range New(tasks, []*planv1.Wish{wish}, map[string]bool{"p": true}, m).At(now).Pass() {
		if d.Task.GetId() == "mock" && !strings.Contains(d.Why, "the most investigations this machine runs at once, mockups counted") {
			t.Errorf("mock beside the investigation on 3 slots: why %q", d.Why)
		}
	}
	mark(tasks[3], true)
	repair := mark(planned("repair", now, planv1.TaskKind_TASK_KIND_WORK), false)
	tasks = append(tasks, repair)
	if got := order(); got != "repair,mock,enlighten,study,work" {
		t.Errorf("order = %s; want the urgent repair, then the mockup marked for feedback, then the others", got)
	}
	done := planned("done", now.Add(-time.Hour), planv1.TaskKind_TASK_KIND_MOCKUP)
	done.Status, done.Branch = planv1.TaskStatus_TASK_STATUS_DONE, "w3-mock"
	next := planned("next", now, planv1.TaskKind_TASK_KIND_WORK)
	next.DependsOn = []string{"done"}
	free := &planv1.Wish{Id: "w", State: planv1.WishState_WISH_STATE_ACTIVE}
	if why, failed := New([]*planv1.Task{done, next}, []*planv1.Wish{free}, map[string]bool{"p": true}, nil).Blocker(next); why != "" || failed != "" {
		t.Errorf("next: %q %q; want it free once the mockup is done", why, failed)
	}
}
