package harness

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestLiveTask: the lists of what runs read every task not done nor cancelled, the azimas, and the done work still on
// its way into its branch (TaskServiceListRequest.live, W219); not the work merged nor the cancelled.
func TestLiveTask(t *testing.T) {
	t.Parallel()
	done := func(state planv1.IntegrationState) *planv1.Task {
		return &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_DONE, Integration: &planv1.TaskIntegration{State: state}}
	}
	for _, c := range []struct {
		name string
		task *planv1.Task
		want bool
	}{
		{"running", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_RUNNING}, true},
		{"planned", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_PENDING}, true},
		{"paused", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_PAUSED}, true},
		{"stopped", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_STOPPED}, true},
		{"cut short", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED}, true},
		{"resuming", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_RESUMING}, true},
		{"failed", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_FAILED}, true},
		{"waiting for you", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_WAITING}, true},
		{"azima done", &planv1.Task{Kind: planv1.TaskKind_TASK_KIND_AZIMA, Status: planv1.TaskStatus_TASK_STATUS_DONE}, true},
		{"merging", done(planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING), true},
		{"waits to merge", done(planv1.IntegrationState_INTEGRATION_STATE_PENDING), true},
		{"merge red", done(planv1.IntegrationState_INTEGRATION_STATE_RED), true},
		{"merge in conflict", done(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT), true},
		{"left not committed", done(planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED), true},
		{"merged", done(planv1.IntegrationState_INTEGRATION_STATE_COMMITTED), false},
		{"done, nothing to merge", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_DONE}, false},
		{"done with refused calls", &planv1.Task{
			Status:  planv1.TaskStatus_TASK_STATUS_DONE,
			Refused: &planv1.TaskRefusals{Count: 2, Names: []string{"Bash"}},
		}, true},
		{"done with refused calls, closed", &planv1.Task{
			Status:  planv1.TaskStatus_TASK_STATUS_DONE,
			Refused: &planv1.TaskRefusals{Count: 2, Names: []string{"Bash"}},
			Closed:  &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, Note: "acknowledged"},
		}, false},
		{"cancelled", &planv1.Task{Status: planv1.TaskStatus_TASK_STATUS_CANCELLED}, false},
	} {
		if got := liveTask(c.task); got != c.want {
			t.Errorf("%s: live = %v, want %v", c.name, got, c.want)
		}
	}
}
