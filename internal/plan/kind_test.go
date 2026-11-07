package plan

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestKind: a task's one type is its kind as stored, work when it holds none; FillKind gives a task of no kind or of
// work the kind its fields tell (watcher, review, correction), and leaves any other as it is.
func TestKind(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		task       *planv1.Task
		want, fill planv1.TaskKind
	}{
		{"none is work", &planv1.Task{}, planv1.TaskKind_TASK_KIND_WORK, planv1.TaskKind_TASK_KIND_UNSPECIFIED},
		{"work", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_WORK}, planv1.TaskKind_TASK_KIND_WORK,
			planv1.TaskKind_TASK_KIND_WORK},
		{"a question worker is work", &planv1.Task{Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR},
			planv1.TaskKind_TASK_KIND_WORK, planv1.TaskKind_TASK_KIND_UNSPECIFIED},
		{"azima, even watching", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_AZIMA,
			Provider: planv1.Provider_PROVIDER_WATCH}, planv1.TaskKind_TASK_KIND_AZIMA, planv1.TaskKind_TASK_KIND_AZIMA},
		{"an old watcher", &planv1.Task{Provider: planv1.Provider_PROVIDER_WATCH}, planv1.TaskKind_TASK_KIND_WORK,
			planv1.TaskKind_TASK_KIND_WATCHER},
		{"an old review", &planv1.Task{Review: &planv1.TaskReview{}}, planv1.TaskKind_TASK_KIND_WORK,
			planv1.TaskKind_TASK_KIND_REVIEW},
		{"an old correction, stored as work", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_WORK,
			Correction: &planv1.TaskCorrection{}}, planv1.TaskKind_TASK_KIND_WORK, planv1.TaskKind_TASK_KIND_CORRECTION},
		{"a review that corrects", &planv1.Task{Review: &planv1.TaskReview{}, Correction: &planv1.TaskCorrection{}},
			planv1.TaskKind_TASK_KIND_WORK, planv1.TaskKind_TASK_KIND_REVIEW},
		{"a stored correction", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_CORRECTION},
			planv1.TaskKind_TASK_KIND_CORRECTION, planv1.TaskKind_TASK_KIND_CORRECTION},
		{"mockup", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_MOCKUP}, planv1.TaskKind_TASK_KIND_MOCKUP,
			planv1.TaskKind_TASK_KIND_MOCKUP},
	} {
		if got := Kind(tt.task); got != tt.want {
			t.Errorf("Kind, %s: %s; want %s", tt.name, got, tt.want)
		}
		before := tt.task.GetKind()
		if changed := FillKind(tt.task); tt.task.GetKind() != tt.fill || changed != (before != tt.fill) {
			t.Errorf("FillKind, %s: %s, changed %v; want %s", tt.name, tt.task.GetKind(), changed, tt.fill)
		}
		if changed := FillKind(tt.task); changed {
			t.Errorf("FillKind, %s: changed twice", tt.name)
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
