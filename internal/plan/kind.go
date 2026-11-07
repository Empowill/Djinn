package plan

import planv1 "github.com/empowill/djinn/gen/go/plan/v1"

// Kind is what t is, its one type, which the window and the queries read: its kind when stored, else what its fields
// say, the first that fits from azima down. A task stored before correction, review and watcher were kinds holds none
// of the three, nor does a correction, a review or a watcher Djinn has not yet marked at creation: its fields tell
// them (Task.correction, Task.review, PROVIDER_WATCH). Work for any other, never unspecified.
func Kind(t *planv1.Task) planv1.TaskKind {
	k := t.GetKind()
	switch {
	case k != planv1.TaskKind_TASK_KIND_UNSPECIFIED && k != planv1.TaskKind_TASK_KIND_WORK:
		return k
	case t.GetProvider() == planv1.Provider_PROVIDER_WATCH:
		return planv1.TaskKind_TASK_KIND_WATCHER
	case t.GetReview() != nil:
		return planv1.TaskKind_TASK_KIND_REVIEW
	case t.GetCorrection() != nil:
		return planv1.TaskKind_TASK_KIND_CORRECTION
	}
	return planv1.TaskKind_TASK_KIND_WORK
}

// DjinnKind tells a kind Djinn alone gives a task, which a spawn or a plan never asks: a correction, a review or a
// watcher.
func DjinnKind(k planv1.TaskKind) bool {
	switch k {
	case planv1.TaskKind_TASK_KIND_CORRECTION, planv1.TaskKind_TASK_KIND_REVIEW, planv1.TaskKind_TASK_KIND_WATCHER:
		return true
	}
	return false
}
