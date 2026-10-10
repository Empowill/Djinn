package harness

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Done marks a task done by hand: a task no worker runs now, planned, waiting, cut short, failed, stopped or
// imported. The task records who closed it, when and why, apart from a worker's own done. A running or paused task
// is refused (stop it first), and so is a task done already. A task waiting on it may start.
func (h *Harness) Done(ctx context.Context, procedure string, req *planv1.TaskServiceDoneRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	// No answer starts the worker again, and no pass of the scheduler starts a planned task, while it closes.
	h.answering.Lock()
	defer h.answering.Unlock()
	h.sched.Lock()
	defer h.sched.Unlock()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	switch s := task.GetStatus(); {
	case s == planv1.TaskStatus_TASK_STATUS_DONE:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is done already", task.GetCode()))
	case running || s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("task %s is %s: stop it first (djinn task stop)", task.GetCode(), short(s)))
	}
	if plan.IsAzima(task) {
		tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": task.GetWishId()})
		if err != nil {
			return nil, plan.Status(err)
		}
		if plan.HasUnfinishedParts(task.GetId(), tasks) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("azima %s has parts still to finish", task.GetCode()))
		}
	}
	by := req.GetBy()
	if by == planv1.Closer_CLOSER_PLAN_FILE {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("only djinn plan sync closes a task for its plan file"))
	}
	if by == planv1.Closer_CLOSER_UNSPECIFIED {
		by = planv1.Closer_CLOSER_LEAD
	}
	now := timestamppb.Now()
	text := fmt.Sprintf("marked done by the %s, was %s", plan.CloserWord(by), short(task.GetStatus()))
	if e := task.GetError(); e != "" {
		text += " (" + e + ")"
	}
	if n := req.GetNote(); n != "" {
		text += ": " + n
	}
	task.Status, task.Error, task.WaitReason = planv1.TaskStatus_TASK_STATUS_DONE, "", ""
	if task.GetEndTime() == nil {
		task.EndTime = now
	}
	task.Closed = &planv1.Closure{Actor: by, CreateTime: now, Note: req.GetNote()}
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
		return tx.Put(newEvent(id, seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake() // A task waiting on it may start.
	return task, nil
}
