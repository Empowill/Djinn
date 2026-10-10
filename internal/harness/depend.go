package harness

// The tasks of a wish form a graph without cycle: what each one waits for, and the azima each one is part of. Depend
// sets a task's dependencies after it was made, so that a plan can be sequenced again as it learns; Group sets its
// azima.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Depend sets the dependencies of the task, and of the others the request names (--also), in place of those they
// had: tasks of their wish, by code or identifier. All or none: a dependency on itself, a task whose worker runs, or a
// graph that would close a cycle refuses them all, naming the cycle. The scheduler then looks again.
func (h *Harness) Depend(ctx context.Context, procedure string, req *planv1.TaskServiceDependRequest) ([]*planv1.Task, error) {
	h.sched.Lock()
	defer h.sched.Unlock()
	var set []*planv1.Task
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		first, err := store.Get[*planv1.Task](ctx, tx, req.GetTaskId())
		if err != nil {
			return err
		}
		tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": first.GetWishId()})
		if err != nil {
			return err
		}
		asked := []*planv1.TaskAfter{{Task: first.GetId(), After: append(slices.Clip(req.GetDependsOn()), req.GetAfter()...)}}
		graph := slices.Clone(tasks) // As it would be once set.
		for _, a := range append(asked, req.GetAlso()...) {
			ids, err := resolveIn(tasks, []string{a.GetTask()}, "task")
			if err != nil {
				return err
			}
			if len(ids) != 1 {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("--also %q names one task", a.GetTask()))
			}
			i := slices.IndexFunc(graph, func(t *planv1.Task) bool { return t.GetId() == ids[0] })
			task := proto.CloneOf(graph[i])
			if slices.ContainsFunc(set, func(t *planv1.Task) bool { return t.GetId() == task.GetId() }) {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s is set twice", task.GetCode()))
			}
			if h.hasRun(task.GetId()) {
				// Its worker writes the task as it goes: what it waits for is set before it starts, or once it ended.
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"task %s runs: set what it waits for once it ended", task.GetCode()))
			}
			if task.DependsOn, err = resolveIn(tasks, a.GetAfter(), "dependency"); err != nil {
				return err
			}
			if slices.Contains(task.GetDependsOn(), task.GetId()) {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("task %s cannot wait for itself", task.GetCode()))
			}
			graph[i], set = task, append(set, task)
		}
		// A cycle of the new graph goes through a task just set: looking from each one finds it.
		for _, task := range set {
			if cycle := closesCycle(task, task.GetDependsOn(), task.GetPartOf(), graph); cycle != "" {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
			}
		}
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		for _, task := range set {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.wake()
	return set, nil
}

// insertBefore makes task, new in its wish, one more dependency of each task names (--blocks): planned tasks of the
// wish, not started yet. A task that has started is refused, saying where it stands, and so is a graph that would
// close a cycle. It returns those tasks changed, for the caller to write with task in one transaction; the caller
// holds h.sched, so that no pass of the scheduler starts one of them in between.
func (h *Harness) insertBefore(ctx context.Context, r store.Reader, task *planv1.Task, names []string) ([]*planv1.Task, error) {
	if len(names) == 0 {
		return nil, nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": task.GetWishId()})
	if err != nil {
		return nil, err
	}
	ids, err := resolveIn(tasks, names, "task")
	if err != nil {
		return nil, err
	}
	// The new task has no code yet: a cycle names it by what it is.
	named := proto.CloneOf(task)
	named.Code = cmp.Or(named.GetCode(), "the new task")
	graph := append(slices.Clone(tasks), named)
	var blocked []*planv1.Task
	for _, id := range ids {
		i := slices.IndexFunc(graph, func(t *planv1.Task) bool { return t.GetId() == id })
		t := proto.CloneOf(graph[i])
		if where := started(t, h.hasRun(id)); where != "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"task %s has started (%s): --blocks puts a new task before a planned one only", t.GetCode(), where))
		}
		t.DependsOn = append(t.DependsOn, task.GetId())
		graph[i], blocked = t, append(blocked, t)
	}
	for _, t := range blocked {
		if cycle := closesCycle(t, t.GetDependsOn(), t.GetPartOf(), graph); cycle != "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the tasks of a wish form no cycle: %s", cycle))
		}
	}
	return blocked, nil
}

// hasRun tells whether a worker of the task runs, paused or not.
func (h *Harness) hasRun(taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[taskID] != nil
}

// started says where a task that has started stands (running, paused, done…), "" for one still planned: pending,
// never started, no worker on it.
func started(t *planv1.Task, running bool) string {
	switch {
	case running:
		return "running"
	case t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING:
		return strings.ToLower(strings.TrimPrefix(t.GetStatus().String(), "TASK_STATUS_"))
	case t.GetStartTime() != nil:
		return "started"
	}
	return ""
}

// block makes task wait before the tasks names says it blocks (insertBefore), in tx, where task is written.
func (h *Harness) block(ctx context.Context, tx *store.Tx, task *planv1.Task, names []string) error {
	blocked, err := h.insertBefore(ctx, tx, task, names)
	if err != nil {
		return err
	}
	for _, t := range blocked {
		if err := tx.Put(t); err != nil {
			return err
		}
	}
	return nil
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
		if azima != "" && task.GetPartOf() != azima {
			if err := reopenAzima(ctx, tx, azima, task.GetCode()+" grouped"); err != nil {
				return err
			}
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
	case tasks[i].GetDraft():
		return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"azima %s is a draft: open it first with djinn task open %s", tasks[i].GetCode(), tasks[i].GetCode()))
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
