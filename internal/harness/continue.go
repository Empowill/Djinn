package harness

// A task is continued in place, never copied: djinn task continue gives a task no worker runs a new turn of its own
// session, in its own worktree, as the same task. A fork of a task cut short continues it too, as another task: the
// parent is closed, and points to its fork.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Continue plans again a task no worker runs, done, failed, stopped or cut short: its next worker resumes its own
// session, in its own worktree, on the prompt. It starts in this call when the scheduler would start it; otherwise
// it waits, with the reason, and the scheduler starts it. Refused while the task runs or is planned, when it
// continues in a fork, when its worktree is gone, or when its agent cannot resume a session.
func (h *Harness) Continue(ctx context.Context, procedure string, req *planv1.TaskServiceContinueRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	// No answer starts the worker again, and no pass of the scheduler decides, while it is planned again.
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
	if err := h.continuable(ctx, task, running); err != nil {
		return nil, err
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return nil, plan.Status(err)
	}
	by := req.GetBy()
	if by == planv1.Closer_CLOSER_UNSPECIFIED {
		by = planv1.Closer_CLOSER_LEAD
	}
	text := fmt.Sprintf("continued by the %s, was %s", plan.CloserWord(by), short(task.GetStatus()))
	if e := task.GetError(); e != "" {
		text += " (" + e + ")"
	}
	if first := firstLine(req.GetPrompt()); first != "" {
		text += ": " + first
	}
	t := proto.CloneOf(task)
	// Its own resumes count again from none: a person asked for this turn.
	t.Status, t.Error, t.ResumeAfter, t.Resumes, t.Closed, t.Continuing = planv1.TaskStatus_TASK_STATUS_RESUMING, "", nil, 0, nil, true
	t.WaitReason = ""
	sit, err := h.situation(ctx, replaced(tasks, t))
	if err != nil {
		return nil, plan.Status(err)
	}
	why, failed := sit.Blocker(t)
	if failed != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s would never start: %s", t.GetCode(), failed))
	}
	t.WaitReason = why
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		seq, err := lastSeq(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		if err := tx.Put(t); err != nil {
			return err
		}
		events := []Event{
			{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text},
			{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: req.GetPrompt()},
		}
		if why != "" {
			events = append(events, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "waiting: " + why})
		}
		for i, ev := range events {
			if err := tx.Put(newEvent(id, seq+1+int64(i), ev)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	if why == "" {
		if err := h.relaunch(ctx, t); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: %w", t.GetCode(), err))
		}
	}
	h.wake()
	got, err := store.Get[*planv1.Task](ctx, h.store, id)
	return got, plan.Status(err)
}

// continuable says why a task cannot be continued, or nil when it can: no worker runs it, it ran on this machine and
// does not continue in a fork, its session can be resumed, and in Git its worktree is there. The caller holds h.sched.
func (h *Harness) continuable(ctx context.Context, t *planv1.Task, running bool) error {
	refuse := func(format string, args ...any) error {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s %s", t.GetCode(), fmt.Sprintf(format, args...)))
	}
	switch s := t.GetStatus(); {
	case running || s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED:
		return refuse("runs: send it a message instead (djinn task send)")
	case s == planv1.TaskStatus_TASK_STATUS_WAITING:
		return refuse("waits for the answer to its edit question")
	case planned(t) || s == planv1.TaskStatus_TASK_STATUS_PENDING:
		return refuse("has not started: it starts by itself, or djinn task stop drops it")
	case t.GetClosed().GetContinuedIn() != "":
		return refuse("continues in %s: continue %s instead", t.GetClosed().GetContinuedIn(), t.GetClosed().GetContinuedIn())
	case !t.GetScheduled():
		return refuse("was not run by this Djinn: spawn a task for it")
	}
	if t.GetProvider() == planv1.Provider_PROVIDER_WATCH {
		return refuse("is a watcher, which has no session: spawn it again")
	}
	if _, ok := h.providers[t.GetProvider()]; !ok {
		return refuse("runs %s, which is not available", short(t.GetProvider()))
	}
	providerChanged := t.GetPriorProvider() != planv1.Provider_PROVIDER_UNSPECIFIED && t.GetPriorProvider() != t.GetProvider()
	if t.GetSessionId() == "" && !providerChanged {
		return refuse("has no session to resume: its worker never said one")
	}
	if b := t.GetMaxBudgetUsd(); b > 0 && t.GetUsage().GetCostUsd() >= b {
		return refuse("has spent its budget of $%.2f", b)
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return plan.Status(err)
	}
	if wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return refuse("belongs to a granted wish")
	}
	if t.GetProjectId() == "" {
		return nil
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil {
		return plan.Status(err)
	}
	if project.GetGit() && !questionWorker(t) { // A question worker reads in the project's folder.
		if t.GetWorktree() == "" {
			return refuse("has no worktree any more: fork it to start from its context (djinn task spawn --fork %s)", t.GetCode())
		}
		if _, err := os.Stat(t.GetWorktree()); err != nil {
			return refuse("has lost its worktree %s: fork it to start from its context (djinn task spawn --fork %s)",
				t.GetWorktree(), t.GetCode())
		}
	}
	return nil
}

// replaced is tasks with t in place of the task of its identifier.
func replaced(tasks []*planv1.Task, t *planv1.Task) []*planv1.Task {
	out := slices.Clone(tasks)
	if i := slices.IndexFunc(out, func(o *planv1.Task) bool { return o.GetId() == t.GetId() }); i >= 0 {
		out[i] = t
	}
	return out
}

// firstLine is the first line of a prompt that says something, cut for an event.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return clipRunes(line, 200)
		}
	}
	return ""
}

// lastPrompt is the task's last prompt: what djinn task continue asked last. Its events are read from the last back,
// as far as that prompt (lastSeq).
func lastPrompt(ctx context.Context, s *store.Store, taskID string) (string, error) {
	var last *planv1.TaskEvent
	err := store.Latest(ctx, s, store.Where{"task_id": taskID}, func(ev *planv1.TaskEvent) bool {
		if ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			last = ev
		}
		return last == nil
	})
	if err != nil {
		return "", err
	}
	if last == nil {
		return "", errors.New("its prompt is lost")
	}
	return last.GetText(), nil
}

// forkCloses tells whether a fork of the task continues it: the task was cut short, failed or stopped, and no worker
// runs it. A fork of a task that runs, or is done, starts another task from its context, and leaves it as it is.
func forkCloses(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_RESUMING,
		planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
		return true
	}
	return false
}

// closeParent closes, in tx, the task the fork continues: done, closed by the lead, continued in the fork, so that
// Djinn no longer resumes it and the window shows it finished, with the link. A task that depends on it waits for the
// fork. The caller holds h.sched, and gives the parent as forkSource read it.
func closeParent(ctx context.Context, tx *store.Tx, parent *planv1.Task, fork string) error {
	parent, err := store.Get[*planv1.Task](ctx, tx, parent.GetId())
	if err != nil || !forkCloses(parent) {
		return err
	}
	now := timestamppb.Now()
	text := fmt.Sprintf("closed by the %s, was %s", plan.CloserWord(planv1.Closer_CLOSER_LEAD), short(parent.GetStatus()))
	if e := parent.GetError(); e != "" {
		text += " (" + e + ")"
	}
	note := "continued in " + fork
	text += ": " + note
	parent.Status, parent.Error, parent.WaitReason, parent.ResumeAfter = planv1.TaskStatus_TASK_STATUS_DONE, "", "", nil
	if parent.GetEndTime() == nil {
		parent.EndTime = now
	}
	parent.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_LEAD, CreateTime: now, Note: note, ContinuedIn: fork}
	seq, err := lastSeq(ctx, tx, parent.GetId())
	if err != nil {
		return err
	}
	if err := tx.Put(parent); err != nil {
		return err
	}
	return tx.Put(newEvent(parent.GetId(), seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
}
