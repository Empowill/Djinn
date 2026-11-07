package plan

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestKind: a task's one type is its kind when stored, else what its fields say (watcher, review, correction), work
// for any other.
func TestKind(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		task *planv1.Task
		want planv1.TaskKind
	}{
		{"none is work", &planv1.Task{}, planv1.TaskKind_TASK_KIND_WORK},
		{"work", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_WORK}, planv1.TaskKind_TASK_KIND_WORK},
		{"a question worker is work", &planv1.Task{Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR},
			planv1.TaskKind_TASK_KIND_WORK},
		{"azima, even watching", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_AZIMA,
			Provider: planv1.Provider_PROVIDER_WATCH}, planv1.TaskKind_TASK_KIND_AZIMA},
		{"an old watcher", &planv1.Task{Provider: planv1.Provider_PROVIDER_WATCH}, planv1.TaskKind_TASK_KIND_WATCHER},
		{"an old review", &planv1.Task{Review: &planv1.TaskReview{}}, planv1.TaskKind_TASK_KIND_REVIEW},
		{"an old correction", &planv1.Task{Correction: &planv1.TaskCorrection{}},
			planv1.TaskKind_TASK_KIND_CORRECTION},
		{"a review that corrects", &planv1.Task{Review: &planv1.TaskReview{}, Correction: &planv1.TaskCorrection{}},
			planv1.TaskKind_TASK_KIND_REVIEW},
		{"a stored correction", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_CORRECTION},
			planv1.TaskKind_TASK_KIND_CORRECTION},
		{"mockup", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_MOCKUP}, planv1.TaskKind_TASK_KIND_MOCKUP},
	} {
		if got := Kind(tt.task); got != tt.want {
			t.Errorf("%s: %s; want %s", tt.name, got, tt.want)
		}
	}
	for k, djinn := range map[planv1.TaskKind]bool{
		planv1.TaskKind_TASK_KIND_REVIEW: true, planv1.TaskKind_TASK_KIND_WATCHER: true,
		planv1.TaskKind_TASK_KIND_CORRECTION: true, planv1.TaskKind_TASK_KIND_IFRIT: false,
	} {
		if DjinnKind(k) != djinn {
			t.Errorf("DjinnKind(%s) = %v", k, !djinn)
		}
	}
}
