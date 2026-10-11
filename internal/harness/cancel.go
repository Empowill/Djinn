package harness

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Cancel cancels a task: stops its worker if running or paused, records who cancelled it,
// removes its worktree while keeping the branch, and drops its work from integration.
// Refused on a task done or cancelled already, and on an azima.
func (h *Harness) Cancel(ctx context.Context, procedure string, req *planv1.TaskServiceCancelRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	if plan.IsAzima(task) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is an azima: cannot cancel an azima", task.GetCode()))
	}
	switch s := task.GetStatus(); {
	case s == planv1.TaskStatus_TASK_STATUS_DONE:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is done already", task.GetCode()))
	case s == planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is cancelled already", task.GetCode()))
	}

	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	wasStatus := task.GetStatus()
	wasError := task.GetError()
	if running || wasStatus == planv1.TaskStatus_TASK_STATUS_RUNNING || wasStatus == planv1.TaskStatus_TASK_STATUS_PAUSED {
		if _, err := h.Stop(ctx, procedure, &planv1.TaskServiceStopRequest{TaskId: id}); err != nil {
			return nil, err
		}
		wasError = ""
		task, err = store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil {
			return nil, plan.Status(err)
		}
	}

	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()

	task, err = store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	switch s := task.GetStatus(); {
	case s == planv1.TaskStatus_TASK_STATUS_DONE:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is done already", task.GetCode()))
	case s == planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is cancelled already", task.GetCode()))
	}

	if task.GetWorktree() != "" {
		project, _ := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
		if project != nil && project.GetDirectory() != "" {
			_ = removeWorktree(ctx, project.GetDirectory(), task.GetWorktree(), true)
		}
		task.Worktree = ""
	}

	by := req.GetBy()
	if by == planv1.Closer_CLOSER_UNSPECIFIED {
		by = planv1.Closer_CLOSER_LEAD
	}
	now := timestamppb.Now()
	text := fmt.Sprintf("cancelled by the %s, was %s", plan.CloserWord(by), short(wasStatus))
	if wasError != "" {
		text += " (" + wasError + ")"
	}
	if n := req.GetNote(); n != "" {
		text += ": " + n
	}

	task.Status = planv1.TaskStatus_TASK_STATUS_CANCELLED
	task.Error = ""
	task.WaitReason = ""
	task.ResumeAfter = nil
	task.Integration = nil
	if task.GetEndTime() == nil {
		task.EndTime = now
	}
	task.Cancelled = &planv1.Cancellation{
		Actor:      by,
		CreateTime: now,
		Note:       req.GetNote(),
	}

	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		seq, err := lastSeq(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		if err := tx.Put(newEvent(id, seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})); err != nil {
			return err
		}
		if err := h.clearCorrectionLinks(ctx, tx, task.GetWishId(), task.GetId()); err != nil {
			return err
		}
		dependents, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": task.GetWishId()})
		if err != nil {
			return err
		}
		for _, dep := range dependents {
			if !slices.Contains(dep.GetDependsOn(), task.GetId()) {
				continue
			}
			depSeq, err := lastSeq(ctx, tx, dep.GetId())
			if err != nil {
				return err
			}
			code := task.GetCode()
			if code == "" {
				code = task.GetId()
			}
			if err := tx.Put(newEvent(dep.GetId(), depSeq+1, Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("dependency %s was cancelled", code),
			})); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}

	h.kickIntegrate()
	_ = h.unblockDependencyFailed(ctx, task.GetCode())
	h.notify()
	h.wake()
	return task, nil
}
