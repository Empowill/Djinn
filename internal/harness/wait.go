package harness

// A worker's question comes back to it (W260, decision Q50): one asked with --wait puts its task WAITING once the
// worker's turn ends (end, waitOn), its slot free, its worktree, branch, session and write scopes kept; the answer, the
// developer's or the lead's, continues it with the decision (Continue), through the scheduler: it starts once a slot
// is free, like any task Djinn resumes. A question answered while its worker runs, blocking or not, is sent to the
// worker between two turns (task send); one that does not block, answered after its worker ended, goes to the lead as
// any answer.

import (
	"context"
	"errors"
	"fmt"
	"slices"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// methodDecision is how the journal records a decision Djinn gives the worker that asked its question: sent to it
// while it runs (the request is the message, as djinn task send's), or continuing its task (as djinn task continue's).
const methodDecision = "harness/decision"

// waitText is the error of a task waiting on its blocking question: what the window and the events say.
func waitText(q *planv1.Question) string { return "waiting for the answer to " + q.GetCode() }

// waitOn is the blocking question the worker of t asked that its task waits on once its turn ends: the latest one
// still open, or answered while the worker ran without its decision reaching it (decisionTaken), which continues the
// task at once (decided). nil when there is none: the task ends as it would.
func (h *Harness) waitOn(ctx context.Context, t *planv1.Task) *planv1.Question {
	if t.GetWishId() == "" {
		return nil
	}
	asked, err := store.List[*planv1.Question](ctx, h.store, store.Where{"task_id": t.GetId()})
	if err != nil {
		logx.L().Error("read the questions a worker asked", "task", t.GetCode(), "err", err)
		return nil
	}
	for _, q := range slices.Backward(asked) {
		if !q.GetWait() || q.GetWithdrawal() != nil {
			continue
		}
		if q.GetAnswer() == nil || !h.decisionTaken(ctx, t.GetId(), q.GetCode()) {
			return q
		}
		return nil // The worker took in the latest decision: it went on.
	}
	return nil
}

// decisionTaken tells whether the worker of the task taskID took in the decision of the question code: it received
// the message that sent it, or a turn of it was continued with it.
func (h *Harness) decisionTaken(ctx context.Context, taskID, code string) bool {
	taken := false
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(ev *planv1.TaskEvent) bool {
		switch ev.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_RECEIVED, planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT:
			taken = plan.IsDecision(ev.GetText(), code)
		}
		return !taken
	})
	if err != nil {
		logx.L().Error("read whether a worker took a decision", "task", taskID, "question", code, "err", err)
	}
	return taken
}

// waitOnQuestion puts t, whose worker ended its turn, WAITING on its blocking question q. The caller is end, which owns the
// run's task.
func waitOnQuestion(t *planv1.Task, q *planv1.Question) {
	t.Status, t.Error, t.WaitQuestionId = planv1.TaskStatus_TASK_STATUS_WAITING, waitText(q), q.GetId()
}

// answerAsker gives the decision of q to the worker of the task that asked it: sent between two turns while it runs;
// its task continued with it when it waits on q; nothing when it ended otherwise, the lead's to act on (the answer's
// event). It says what Djinn did, for the lead.
func (h *Harness) answerAsker(ctx context.Context, q *planv1.Question) (string, bool) {
	id := q.GetTaskId()
	if id == "" || q.GetAnswer() == nil || q.GetDjinnMove() != nil {
		return "", false
	}
	h.mu.Lock()
	r := h.runs[id]
	live := r != nil && r.worker != nil && !r.final && !r.stopping && r.out == "" && !r.watcher
	h.mu.Unlock()
	text := plan.DecisionPrompt(q)
	if live {
		_, err := h.sendTo(ctx, r, r.task.GetCode(), &message{
			procedure: methodDecision, req: &planv1.TaskServiceSendRequest{TaskId: id, Text: text},
			reply: make(chan sent, 1), actor: actorHarness, betweenTurns: true,
		})
		if err == nil {
			return "Djinn sends the decision to " + h.taskCode(ctx, id) + ", which runs, between two of its turns", true
		}
		logx.L().Info("a decision finds its worker ending", "task", id, "question", q.GetCode(), "err", err)
	}
	if r != nil {
		select {
		case <-r.done: // Its end is being written: it waits on q, or ended without it.
		case <-ctx.Done():
			return "", false
		}
	}
	return h.decided(ctx, q)
}

// decided continues the task that waits on q, answered, with its decision: through the scheduler, once a slot is free.
// It says what Djinn did, for the lead; false when no task waits on q.
func (h *Harness) decided(ctx context.Context, q *planv1.Question) (string, bool) {
	task, err := store.Get[*planv1.Task](ctx, h.store, q.GetTaskId())
	if err != nil || task.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING || task.GetWaitQuestionId() != q.GetId() {
		return "", false
	}
	text := plan.DecisionPrompt(q)
	by := q.GetAnswer().GetActor()
	if by != planv1.Closer_CLOSER_LEAD {
		by = planv1.Closer_CLOSER_DEVELOPER
	}
	got, err := h.Continue(ctx, methodDecision, &planv1.TaskServiceContinueRequest{TaskId: task.GetId(), Prompt: &text, By: by})
	if err != nil {
		logx.L().Error("continue a task with its decision", "task", task.GetCode(), "question", q.GetCode(), "err", err)
		return fmt.Sprintf("Djinn could not continue %s with the decision (%v): djinn task continue %s with it",
			task.GetCode(), err, task.GetCode()), true
	}
	if got.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
		return "Djinn continues " + task.GetCode() + " with the decision", true
	}
	return fmt.Sprintf("Djinn continues %s with the decision once it may start (%s)", task.GetCode(), got.GetWaitReason()), true
}

// decideLater continues the task t with the decision of q, answered before its worker's turn ended without taking it
// in: once the run of t is gone. end calls it.
func (h *Harness) decideLater(t *planv1.Task, q *planv1.Question) {
	h.wg.Add(1)
	go func() {
		defer logx.Crash("decision", "task", t.GetCode())
		defer h.wg.Done()
		cur, err := store.Get[*planv1.Question](h.ctx, h.store, q.GetId())
		if err != nil {
			return
		}
		if did, ok := h.decided(h.ctx, cur); ok {
			logx.L().Info("a decision continues its task", "task", t.GetCode(), "question", q.GetCode(), "did", did)
		}
	}()
}

// ResumeDecisions continues each task that waits on a blocking question answered already: djinn up stopped between
// storing the answer and continuing the task. djinn up calls it once it serves; it works in the background, and Close
// waits for it.
func (h *Harness) ResumeDecisions() {
	h.wg.Add(1)
	go func() {
		defer logx.Crash("decision")
		defer h.wg.Done()
		if err := h.resumeDecisions(h.ctx); err != nil && !errors.Is(err, context.Canceled) {
			logx.L().Error("continue the tasks whose blocking question was answered", "err", err)
		}
	}()
}

// resumeDecisions is ResumeDecisions, done now.
func (h *Harness) resumeDecisions(ctx context.Context) error {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING || t.GetWaitQuestionId() == "" {
			continue
		}
		q, err := store.Get[*planv1.Question](ctx, h.store, t.GetWaitQuestionId())
		if err != nil || q.GetAnswer() == nil {
			continue
		}
		if did, ok := h.decided(ctx, q); ok {
			logx.L().Info("a decision continues its task after a restart", "task", t.GetCode(), "question", q.GetCode(), "did", did)
		}
	}
	return nil
}
