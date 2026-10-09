package harness

import (
	"context"
	"errors"
	"fmt"
	"log"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Answered takes the answer to a question: when it is a task's edit question, a yes starts the task's worker again,
// allowed to edit the project's files and to run no command; a no leaves it reading only. Only the first answer
// counts. The plan services call it once the answer is stored.
func (h *Harness) Answered(ctx context.Context, q *planv1.Question) {
	if q.GetAnswer() == nil {
		return
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"edit_question_id": q.GetId()})
	if err != nil {
		log.Printf("djinn: question %s: find its task: %v", q.GetCode(), err)
		return
	}
	for _, t := range tasks {
		if err := h.answer(t.GetId(), q); err != nil {
			log.Printf("djinn: task %s: answer %s: %v", t.GetCode(), q.GetCode(), err)
		}
	}
}

// answer gives the answer q to the task id: to its run, which applies it, or to the task itself when no worker runs
// for it.
func (h *Harness) answer(id string, q *planv1.Question) error {
	h.answering.Lock()
	defer h.answering.Unlock()
	for {
		h.mu.Lock()
		r := h.runs[id]
		if r != nil && !r.final {
			r.answers = append(r.answers, q)
			select {
			case r.wake <- struct{}{}:
			default:
			}
			h.mu.Unlock()
			return nil
		}
		h.mu.Unlock()
		if r == nil {
			break
		}
		<-r.done // The run is getting its final status: the answer goes to the task once it has.
	}

	task, err := store.Get[*planv1.Task](context.Background(), h.store, id)
	if err != nil || task.GetAccess() != planv1.TaskAccess_TASK_ACCESS_ASKING {
		return err
	}
	seq, err := lastSeq(context.Background(), h.store, id)
	if err != nil {
		return err
	}
	if !yes(q) || task.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED || task.GetClosed() != nil {
		// Nothing starts: the answer is recorded with the task, a task waiting for it is done.
		r := &run{id: id, task: task, seq: seq}
		h.applyAnswer(r, q)
		return nil
	}
	r, err := h.newRun(task, seq)
	if err != nil {
		return err
	}
	h.applyAnswer(r, q)
	if err := h.again(r); err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return nil
	}
	go h.pump(r)
	return nil
}

// yes tells whether q's answer allows editing: the first option of an edit question.
func yes(q *planv1.Question) bool { return q.GetAnswer().GetChoice() == planv1.Choice_CHOICE_A }

// applyAnswer records the answer q in the run's task, still asking, and says whether its worker should start
// again, allowed to edit. The caller owns the run's task. A run without a watcher list (a task without worker)
// writes without publishing.
func (h *Harness) applyAnswer(r *run, q *planv1.Question) bool {
	t := r.task
	if t.GetAccess() != planv1.TaskAccess_TASK_ACCESS_ASKING {
		return false
	}
	text := "edit refused (" + q.GetCode() + "): the worker only reads"
	t.Access = planv1.TaskAccess_TASK_ACCESS_EDIT_REFUSED
	start := false
	switch {
	case yes(q) && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED:
		t.Access = planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED
		text = "edit granted (" + q.GetCode() + "): the task was stopped on request, and stays stopped"
	case yes(q) && t.GetClosed() != nil:
		t.Access = planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED
		text = "edit granted (" + q.GetCode() + "): the task was marked done by hand, and stays done"
	case yes(q):
		t.Access, start = planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED, true
		text = "edit granted (" + q.GetCode() + "): the worker starts again, allowed to edit the project's files"
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING:
		t.Status, t.Error, t.EndTime = planv1.TaskStatus_TASK_STATUS_DONE, "", timestamppb.Now()
	}
	h.write(r, actorHarness, methodAnswer, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	return start
}

// again starts the run's task's worker again, after a yes to its edit question: in the project's folder, allowed
// to edit, resuming the agent's session when it has one, with what remains of the task's budget. The caller owns
// the run's task.
func (h *Harness) again(r *run) error {
	t := r.task
	provider, ok := h.providers[t.GetProvider()]
	if !ok {
		return fmt.Errorf("provider %s is not available", t.GetProvider())
	}
	if t.GetWorktree() != "" {
		return errors.New("a task in a worktree is not started again")
	}
	project, err := store.Get[*planv1.Project](context.Background(), h.store, t.GetProjectId())
	if err != nil {
		return err
	}
	budget := t.GetMaxBudgetUsd()
	if budget > 0 {
		if budget -= t.GetUsage().GetCostUsd(); budget <= 0 {
			return fmt.Errorf("its budget of $%.2f is spent", t.GetMaxBudgetUsd())
		}
	}
	prompt, err := firstPrompt(h.store, t.GetId())
	if err != nil {
		return err
	}
	readOnly, perms := accessSpec(t.GetAccess(), nil)
	r.base = t.GetUsage()
	spec := Spec{
		TaskID: t.GetId(), Dir: project.GetDirectory(), ReadOnly: readOnly, Permissions: perms, Model: t.GetModel(),
		MaxBudgetUSD: budget, Resume: t.GetSessionId(),
		Prompt: "The developer now allows you to edit the files of this project, and to run no command. " +
			"Carry on with your task, as it was given:\n" + prompt,
	}
	spec.Skills, spec.SkillsDir = h.summon(context.Background(), r, project)
	return h.start(r, provider, spec, "started "+short(t.GetProvider())+" again in "+project.GetDirectory()+", "+
		accessText(t, nil)+skillsText(spec.Skills))
}

// firstPrompt is what the task's worker was first asked: its first event.
func firstPrompt(s *store.Store, taskID string) (string, error) {
	events, err := store.List[*planv1.TaskEvent](context.Background(), s, store.Where{"task_id": taskID})
	if err != nil {
		return "", err
	}
	for _, ev := range events {
		if ev.GetSeq() == 1 && ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
			return ev.GetText(), nil
		}
	}
	return "", errors.New("its prompt is lost")
}
