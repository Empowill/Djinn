package harness

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Delete removes a task no worker of this Djinn ran, with its events: a plan item, or a task imported from another
// Djinn. A task a worker of this Djinn ran (it has a session or a worktree) is the record of that work: it stays.
func (h *Harness) Delete(ctx context.Context, procedure string, req *planv1.TaskServiceDeleteRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	if running || task.GetWorktree() != "" || task.GetSessionId() != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("task %s was run by a worker of this Djinn: it stays, as the record of that work", task.GetCode()))
	}
	events, err := store.List[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": id})
	if err != nil {
		return nil, plan.Status(err)
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		for _, ev := range events {
			if err := tx.Delete(ev); err != nil {
				return err
			}
		}
		return tx.Delete(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	return task, nil
}
