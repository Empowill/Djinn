package plan

import planv1 "github.com/empowill/djinn/gen/go/plan/v1"

// Kind is what t is, its one type, which the window and the queries read: its kind as stored, work when it holds
// none (work, as every task was before kinds). Djinn stores a correction's, a review's and a watcher's at creation,
// and djinn up fills those of the tasks stored before they were kinds (FillKind): never unspecified.
func Kind(t *planv1.Task) planv1.TaskKind {
	if k := t.GetKind(); k != planv1.TaskKind_TASK_KIND_UNSPECIFIED {
		return k
	}
	return planv1.TaskKind_TASK_KIND_WORK
}

// FillKind gives t, a task of no kind or of work, the kind Djinn alone gives and its fields tell: a watcher
// (PROVIDER_WATCH), a review (Task.review, even one that corrects) or a correction (Task.correction). It tells whether
// t changed; a task of any other kind, or plain work, stays as it is.
func FillKind(t *planv1.Task) bool {
	if k := t.GetKind(); k != planv1.TaskKind_TASK_KIND_UNSPECIFIED && k != planv1.TaskKind_TASK_KIND_WORK {
		return false
	}
	var k planv1.TaskKind
	switch {
	case t.GetProvider() == planv1.Provider_PROVIDER_WATCH:
		k = planv1.TaskKind_TASK_KIND_WATCHER
	case t.GetReview() != nil:
		k = planv1.TaskKind_TASK_KIND_REVIEW
	case t.GetCorrection() != nil:
		k = planv1.TaskKind_TASK_KIND_CORRECTION
	default:
		return false
	}
	t.Kind = k
	return true
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
