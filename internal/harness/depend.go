package harness

// The tasks of a wish form a graph without cycle: what each one waits for, and the azima each one is part of. Depend
// sets a task's dependencies after it was made, so that a plan can be sequenced again as it learns; Group sets its
// azima.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Depend sets the dependencies of the task, in place of those it had: tasks of its wish, by code or identifier. A
// dependency on itself, or one that would close a cycle, is refused, naming the cycle. The scheduler then looks again.
func (h *Harness) Depend(ctx context.Context, procedure string, req *planv1.TaskServiceDependRequest) (*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	h.mu.Lock()
	running := h.runs[req.GetTaskId()] != nil
	h.mu.Unlock()
	var task *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if task, err = store.Get[*planv1.Task](ctx, tx, req.GetTaskId()); err != nil {
			return err
		}
		if running {
			// Its worker writes the task as it goes: what it waits for is set before it starts, or once it ended.
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s runs: set what it waits for once it ended", task.GetCode()))
		}
		deps, err := resolveDeps(ctx, tx, task.GetWishId(), req.GetDependsOn())
		if err != nil {
			return err
		}
		if slices.Contains(deps, task.GetId()) {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s cannot wait for itself", task.GetCode()))
		}
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": task.GetWishId()})
		if err != nil {
			return err
		}
		if cycle := closesCycle(task, deps, task.GetPartOf(), tasks); cycle != "" {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		task.DependsOn = deps
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.wake()
	return task, nil
}

// Group sets the azima the task is part of, in place of the one it had: an azima of its wish, by code or
// identifier, or none. One that would close a cycle with what the tasks depend on is refused, naming it. A grouping,
// never a wait: the scheduler looks at what the task depends on only.
func (h *Harness) Group(ctx context.Context, procedure string, req *planv1.TaskServiceGroupRequest) (*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	h.mu.Lock()
	running := h.runs[req.GetTaskId()] != nil
	h.mu.Unlock()
	var task *planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if task, err = store.Get[*planv1.Task](ctx, tx, req.GetTaskId()); err != nil {
			return err
		}
		if running {
			// Its worker writes the task as it goes.
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s runs: set its azima once it ended", task.GetCode()))
		}
		azima, err := resolveAzima(ctx, tx, task.GetWishId(), req.GetPartOf())
		if err != nil {
			return err
		}
		if azima == task.GetId() {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("azima %s cannot be part of itself", task.GetCode()))
		}
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": task.GetWishId()})
		if err != nil {
			return err
		}
		if cycle := closesCycle(task, task.GetDependsOn(), azima, tasks); cycle != "" {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		task.PartOf = azima
		return tx.Put(task)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return task, nil
}

// resolveAzima turns what names an azima, its code (T07, any case) or identifier, into the identifier of an azima of
// the wish; "" names none.
func resolveAzima(ctx context.Context, r store.Reader, wishID, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == name || strings.EqualFold(t.GetCode(), name) })
	switch {
	case i < 0:
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("azima %s is not a task of the wish", name))
	case !plan.IsAzima(tasks[i]):
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s is work, not an azima: a task is part of an azima", tasks[i].GetCode()))
	}
	return tasks[i].GetId(), nil
}

// closesCycle tells whether task, waiting for deps and part of azima, would reach itself through tasks, following what
// each task depends on and the azima it is part of, and then says the cycle in codes ("W1 → W3 → W1"); "" when it
// would not.
func closesCycle(task *planv1.Task, deps []string, azima string, tasks []*planv1.Task) string {
	byID := map[string]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	edges := func(id string) []string {
		if id == task.GetId() {
			if azima != "" {
				return append(slices.Clip(deps), azima)
			}
			return deps
		}
		t := byID[id]
		if p := t.GetPartOf(); p != "" {
			return append(slices.Clip(t.GetDependsOn()), p)
		}
		return t.GetDependsOn()
	}
	// A path from one of the task's edges back to task closes a cycle.
	seen := map[string]bool{}
	var path []string
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == task.GetId() {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		path = append(path, id)
		for _, next := range edges(id) {
			if walk(next) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	for _, d := range edges(task.GetId()) {
		path = path[:0]
		if walk(d) {
			codes := []string{task.GetCode()}
			for _, id := range path {
				codes = append(codes, byID[id].GetCode())
			}
			return strings.Join(append(codes, task.GetCode()), " → ")
		}
	}
	return ""
}
