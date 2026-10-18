package plan

import (
	"slices"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestMergeCountsWithItsOriginal: a correction of a correction (W422 of W419 of W401) is shown and counted with W401,
// the original task it serves in its azima: the azima counts one part, running while the correction runs.
func TestMergeCountsWithItsOriginal(t *testing.T) {
	t.Parallel()
	conflict := planv1.IntegrationState_INTEGRATION_STATE_CONFLICT
	w401 := &planv1.Task{Id: "w401", Code: "W401", PartOf: "t1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		Integration: &planv1.TaskIntegration{State: conflict}}
	w419 := &planv1.Task{Id: "w419", Code: "W419", PartOf: "t1", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		Correction: &planv1.TaskCorrection{Failure: &planv1.IntegrationFailure{TaskIds: []string{"w401"}}}}
	w422 := &planv1.Task{Id: "w422", Code: "W422", PartOf: "t1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Correction: &planv1.TaskCorrection{Failure: &planv1.IntegrationFailure{TaskIds: []string{"w419", "w401"}}}}
	push := &planv1.Task{Id: "w430", Code: "W430", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Correction: &planv1.TaskCorrection{Failure: &planv1.IntegrationFailure{}}}
	t1 := &planv1.Task{Id: "t1", Code: "T1", Kind: planv1.TaskKind_TASK_KIND_AZIMA}
	tasks := []*planv1.Task{t1, w401, w419, w422, push}
	byID := map[string]*planv1.Task{}
	for _, x := range tasks {
		byID[x.GetId()] = x
	}
	if got := MergeServes(w422, byID); !slices.Equal(got, []string{"w401"}) {
		t.Errorf("W422 serves %v; want [w401]", got)
	}
	for _, c := range []struct {
		task *planv1.Task
		want string
	}{{w401, ""}, {w419, "w401"}, {w422, "w401"}, {push, ""}} {
		if got := MergeHost(c.task, byID); got != c.want {
			t.Errorf("%s sits under %q; want %q", c.task.GetCode(), got, c.want)
		}
	}
	FillAzimas(tasks)
	if a := t1.GetAzima(); a.GetParts() != 1 || a.GetPartsRunning() != 1 || a.GetPartsDone() != 0 ||
		a.GetState() != planv1.AzimaState_AZIMA_STATE_IN_PROGRESS {
		t.Errorf("T1: %v; want one part, running, in progress", a)
	}
}
