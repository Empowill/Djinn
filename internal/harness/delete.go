package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Delete removes a task no worker of this Djinn ran, with its events: a plan item, or a task imported from another
// Djinn. A task a worker of this Djinn ran (it has a session or a worktree) is the record of that work: it stays. An
// azima goes once no task is part of it: those are named, to be regrouped first (Group), for no task may be part of
// an azima that is gone. A task others wait for (DependsOn, in any wish) goes once they are rewired (Depend --also):
// those are named too. Its code is retired with it (Wish.retired_codes): no task of the wish gets it again.
func (h *Harness) Delete(ctx context.Context, procedure string, req *planv1.TaskServiceDeleteRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	var task *planv1.Task
	logx.Lock(&h.sched, "scheduler") // Serialize deletion with scheduler passes and graph edits (Spawn, Depend, Group).
	defer h.sched.Unlock()
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if task, err = store.Get[*planv1.Task](ctx, tx, id); err != nil {
			return err
		}
		h.mu.Lock()
		running := h.runs[task.GetId()] != nil
		h.mu.Unlock()
		if running || task.GetWorktree() != "" || task.GetSessionId() != "" {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("task %s was run by a worker of this Djinn: it stays, as the record of that work", task.GetCode()))
		}
		if err := keptParts(ctx, tx, task); err != nil {
			return err
		}
		dependents, err := incomingDependents(ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		if len(dependents) > 0 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s still has dependents: %s. Rewire them in one atomic `djinn task depend <task-id> --after ... --also ...` command, then delete it",
				task.GetCode(), strings.Join(dependents, ", ")))
		}
		events, err := store.List[*planv1.TaskEvent](ctx, tx, store.Where{"task_id": task.GetId()})
		if err != nil {
			return err
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		for _, ev := range events {
			if err := tx.Delete(ev); err != nil {
				return err
			}
		}
		if err := retire(ctx, tx, task); err != nil {
			return err
		}
		return tx.Delete(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	return task, nil
}

// incomingDependents returns the codes of every task that still names taskID in DependsOn, across wishes. Imported
// data is checked globally too: a malformed cross-wish edge must not be erased by deleting its target.
func incomingDependents(ctx context.Context, r store.Reader, taskID string) ([]string, error) {
	tasks, err := store.List[*planv1.Task](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	var dependents []string
	for _, task := range tasks {
		if task.GetId() != taskID && slices.Contains(task.GetDependsOn(), taskID) {
			dependents = append(dependents, task.GetCode())
		}
	}
	slices.SortFunc(dependents, plan.CompareCodes)
	return dependents, nil
}

// retire keeps the code of task, being deleted, in its wish, which gives it no more.
func retire(ctx context.Context, tx *store.Tx, task *planv1.Task) error {
	wish, err := store.Get[*planv1.Wish](ctx, tx, task.GetWishId())
	if err != nil || task.GetCode() == "" || slices.Contains(wish.GetRetiredCodes(), task.GetCode()) {
		return err
	}
	wish.RetiredCodes = append(wish.RetiredCodes, task.GetCode())
	return tx.Put(wish)
}

// keptParts refuses to delete azima while tasks are part of it, naming them and how to regroup them.
func keptParts(ctx context.Context, r store.Reader, azima *planv1.Task) error {
	if !plan.IsAzima(azima) {
		return nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": azima.GetWishId()})
	if err != nil {
		return err
	}
	var parts []string
	for _, t := range tasks {
		if t.GetPartOf() == azima.GetId() {
			parts = append(parts, t.GetCode())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	slices.SortFunc(parts, plan.CompareCodes)
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"azima %s still has tasks part of it: %s. Regroup them first (djinn task group <task> --part-of <another azima>, "+
			"or --part-of \"\" for none; a task whose worker runs, once it ended), then delete it",
		azima.GetCode(), strings.Join(parts, ", ")))
}
