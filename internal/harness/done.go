package harness

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Done marks a task done by hand: a task no worker runs now, planned, waiting, cut short, failed, stopped or
// imported. The task records who closed it, when and why, apart from a worker's own done. A running or paused task
// is refused (stop it first), and so is a task done already, but one done with problems (plan.DoneWithProblems):
// marked done, it is closed as seen, nothing else changes. A task waiting on it may start. A lit ifrit is ended
// instead: its input closes, and its work integrates once its agent ends (doneIfrit).
func (h *Harness) Done(ctx context.Context, procedure string, req *planv1.TaskServiceDoneRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	// No answer starts the worker again, and no pass of the scheduler starts a planned task, while it closes.
	h.answering.Lock()
	defer h.answering.Unlock()
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	switch s := task.GetStatus(); {
	case plan.DoneWithProblems(task) && !req.GetDrop():
		return h.doneSeen(ctx, procedure, req, task)
	case s == planv1.TaskStatus_TASK_STATUS_DONE:
		if !req.GetDrop() || task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			(task.GetClosed() != nil && task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is done already", task.GetCode()))
		}
		if task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is integrating: refused while it merges", task.GetCode()))
		}
	case s == planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is cancelled already", task.GetCode()))
	case running && s == planv1.TaskStatus_TASK_STATUS_RUNNING && dispatch.Ifrit(task):
		return h.doneIfrit(ctx, procedure, req, task)
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
	var unmerged bool
	if task.GetBranch() != "" {
		wish, _ := store.Get[*planv1.Wish](ctx, h.store, task.GetWishId())
		project, _ := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
		if project != nil && project.GetGit() {
			intBranch, _ := h.integrationBranch(ctx, wish, project)
			unmerged = branchHoldsUnmergedCommits(ctx, project.GetDirectory(), task.GetBranch(), intBranch)
		}
	}
	if unmerged || (task.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && req.GetDrop()) {
		if req.GetDrop() {
			if req.GetNote() == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("task %s has unmerged commits: --note must say why they are dropped", task.GetCode()))
			}
			task.Integration = nil
		} else {
			task.Integration = pending(task.GetBranch(), "")
		}
	}
	task.Status, task.Error, task.WaitReason = planv1.TaskStatus_TASK_STATUS_DONE, "", ""
	if task.GetEndTime() == nil {
		task.EndTime = now
	}
	task.Closed = &planv1.Closure{Actor: by, CreateTime: now, Note: req.GetNote()}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		if err := putEvents(ctx, tx, newEvent(id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})); err != nil {
			return err
		}
		if task.GetIntegration() == nil {
			if err := h.clearCorrectionLinks(ctx, tx, task.GetWishId(), task.GetId()); err != nil {
				return err
			}
		}
		if task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			return putEvents(ctx, tx, newEvent(id, Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: "integration: pending, to be merged into the integration branch",
			}))
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	if task.GetIntegration() == nil || task.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING {
		h.kickIntegrate()
	}
	_ = h.unblockDependencyFailed(ctx, task.GetCode())
	h.notify()
	h.wake() // A task waiting on it may start.
	return task, nil
}

// doneSeen closes a task done with problems as seen by the closer of req: its status, its work and its integration
// stay as they are; Djinn may propose to grant its wish again.
func (h *Harness) doneSeen(ctx context.Context, procedure string, req *planv1.TaskServiceDoneRequest, task *planv1.Task) (*planv1.Task, error) {
	by := req.GetBy()
	if by == planv1.Closer_CLOSER_PLAN_FILE {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("only djinn plan sync closes a task for its plan file"))
	}
	if by == planv1.Closer_CLOSER_UNSPECIFIED {
		by = planv1.Closer_CLOSER_LEAD
	}
	text := fmt.Sprintf("marked done by the %s, was done with %d refused calls", plan.CloserWord(by), task.GetRefused().GetCount())
	if n := req.GetNote(); n != "" {
		text += ": " + n
	}
	task.Closed = &planv1.Closure{Actor: by, CreateTime: timestamppb.Now(), Note: req.GetNote()}
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return putEvents(ctx, tx, newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return task, nil
}
