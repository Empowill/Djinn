package harness

import (
	"context"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Cancel cancels a task: stops its worker if running or paused, records who cancelled it,
// removes its worktree while keeping the branch, and drops its work from integration.
func (h *Harness) Cancel(ctx context.Context, procedure string, req *planv1.TaskServiceCancelRequest) (*planv1.Task, error) {
	return nil, nil
}
