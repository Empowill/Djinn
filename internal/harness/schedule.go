package harness

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// What the scheduler journals, under the actor harness.
const (
	methodSchedule = "harness/schedule" // a planned task is ready: its access is decided; the request is the task
	methodWait     = "harness/wait"     // a planned task waits, for a new reason; the request is the task
)

// Capacity says how many workers may run at once now and the rule that gave it, and why the machine is under
// pressure ("" when it is not). djinn up gives the machine's (internal/machine); without one, nothing limits.
type Capacity func() (slots int, rule, pressure string)

// Option sets how the harness schedules its tasks.
type Option func(*Harness)

// WithCapacity limits the workers that run at once, and stops new ones while the machine is under pressure.
func WithCapacity(c Capacity) Option { return func(h *Harness) { h.capacity = c } }

// WithTick sets how often the scheduler looks at the planned tasks again without being woken: the pressure of the
// machine falls without telling anyone.
func WithTick(d time.Duration) Option { return func(h *Harness) { h.tick = d } }

// Schedule starts the scheduler: a planned task starts as soon as its dependencies are done, its write scopes are
// free and a slot is free. djinn up calls it once the harness has recovered; Close stops it.
func (h *Harness) Schedule() {
	h.scheduling.Do(func() {
		h.loopDone = make(chan struct{})
		go h.loop()
	})
}

func (h *Harness) loop() {
	defer close(h.loopDone)
	tick := time.NewTicker(h.tick)
	defer tick.Stop()
	for {
		h.schedule(h.ctx)
		select {
		case <-h.ctx.Done():
			return
		case <-h.kick:
		case <-tick.C:
		}
	}
}

// wake asks the scheduler for a pass: a slot, a scope or a dependency may have freed.
func (h *Harness) wake() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// notifyLocked tells the watchers of a task without worker that something changed: a planned task waited for a
// new reason, started, or ended. The caller holds h.mu.
func (h *Harness) notifyLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

func (h *Harness) notify() {
	h.mu.Lock()
	h.notifyLocked()
	h.mu.Unlock()
}

// generation is a channel closed at the next change of a task without worker.
func (h *Harness) generation() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

// Running is the number of workers that run now.
func (h *Harness) Running() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runs)
}

// planned tells whether Djinn starts the task by itself, once it is ready.
func planned(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING && t.GetScheduled() && t.GetStartTime() == nil
}

// schedule makes one pass over the planned tasks, the first wish first: each one starts, waits with its reason,
// or fails when a dependency failed.
func (h *Harness) schedule(ctx context.Context) {
	h.sched.Lock()
	defer h.sched.Unlock()
	defer h.refreshWarm(ctx) // Once the planned tasks have taken their slots.
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("djinn: schedule: %v", err)
		}
		return
	}
	var waiting []*planv1.Task
	for _, t := range tasks {
		if planned(t) {
			waiting = append(waiting, t)
		}
	}
	if len(waiting) == 0 {
		return
	}
	waiting = h.order(ctx, waiting)
	s := newSituation(ctx, h.store, tasks)
	for _, t := range waiting {
		if h.ctx.Err() != nil {
			return
		}
		why, failed := h.blocker(s, t)
		switch {
		case failed != "":
			h.failPlanned(ctx, t, failed)
		case why != "":
			h.setWaiting(ctx, t, why)
		default:
			if err := h.launchPlanned(ctx, t); err != nil {
				log.Printf("djinn: task %s: %v", t.GetCode(), err)
			}
			s.started[t.GetId()] = true
		}
	}
}

// order sorts tasks by the rank of their wish (plan.ActiveWishes), then the oldest first. The tasks of a wish that
// is not active come last; blocker keeps them waiting.
func (h *Harness) order(ctx context.Context, tasks []*planv1.Task) []*planv1.Task {
	pos := h.wishRanks(ctx)
	out := slices.Clone(tasks)
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		return cmp.Or(cmp.Compare(pos(a.GetWishId()), pos(b.GetWishId())),
			a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), strings.Compare(a.GetId(), b.GetId()))
	})
	return out
}

// wishRanks gives the position of a wish among the active ones, by rank; a wish that is not active comes last.
func (h *Harness) wishRanks(ctx context.Context) func(wishID string) int {
	active, err := plan.ActiveWishes(ctx, h.store)
	if err != nil && ctx.Err() == nil {
		log.Printf("djinn: rank the wishes: %v", err)
	}
	pos := make(map[string]int, len(active))
	for i, w := range active {
		pos[w.GetId()] = i
	}
	return func(id string) int {
		if p, ok := pos[id]; ok {
			return p
		}
		return len(pos)
	}
}

// Ranks gives the position of each task's wish among the active wishes, the first served first; the task of a wish
// that is not active, or unknown, is not in the map. The gates serve their waiters in this order.
func (h *Harness) Ranks(ctx context.Context, taskIDs []string) map[string]int {
	active, err := plan.ActiveWishes(ctx, h.store)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, id := range taskIDs {
		t, err := store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil {
			continue
		}
		if i := slices.IndexFunc(active, func(w *planv1.Wish) bool { return w.GetId() == t.GetWishId() }); i >= 0 {
			out[id] = i
		}
	}
	return out
}

// Describe names a task for those who wait behind it: its code and title.
func (h *Harness) Describe(ctx context.Context, taskID string) (string, error) {
	t, err := store.Get[*planv1.Task](ctx, h.store, taskID)
	if err != nil {
		return "", plan.Status(err)
	}
	return t.GetCode() + " (" + t.GetTitle() + ")", nil
}

// situation is what a scheduling pass knows of the tasks: every task by id, the projects, and the tasks it started.
type situation struct {
	ctx      context.Context
	store    *store.Store
	byID     map[string]*planv1.Task
	tasks    []*planv1.Task
	projects map[string]*planv1.Project
	started  map[string]bool
}

func newSituation(ctx context.Context, s *store.Store, tasks []*planv1.Task) *situation {
	byID := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	return &situation{ctx: ctx, store: s, byID: byID, tasks: tasks, projects: map[string]*planv1.Project{}, started: map[string]bool{}}
}

// git tells whether the project is in Git, where worktrees separate the writers.
func (s *situation) git(projectID string) bool {
	p, ok := s.projects[projectID]
	if !ok {
		p, _ = store.Get[*planv1.Project](s.ctx, s.store, projectID)
		s.projects[projectID] = p
	}
	return p.GetGit()
}

// writing tells whether a task writes in its project now: its worker runs, it was just started, or it waits for
// the answer to its edit question and may start again any time.
func (s *situation) writing(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING:
		return true
	}
	return s.started[t.GetId()]
}

// blocker says why the task cannot start now (why), or why it never will (failed); both empty when it can start.
// Its wish must be active (not paused nor granted); then come the dependencies, the write scopes, and the machine.
func (h *Harness) blocker(s *situation, t *planv1.Task) (why, failed string) {
	if wish, err := store.Get[*planv1.Wish](s.ctx, s.store, t.GetWishId()); err == nil && !plan.Active(wish) {
		return "its wish is " + strings.ToLower(strings.TrimPrefix(wish.GetState().String(), "WISH_STATE_")), ""
	}
	for _, id := range t.GetDependsOn() {
		d, ok := s.byID[id]
		if !ok {
			return "", "its dependency " + id + " is gone"
		}
		switch d.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE:
			continue
		case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			return "", fmt.Sprintf("its dependency %s ended %s", d.GetCode(), short(d.GetStatus()))
		}
		if why == "" {
			why = fmt.Sprintf("waits for %s (%s)", d.GetCode(), short(d.GetStatus()))
		}
	}
	if why != "" {
		return why, ""
	}
	if id := t.GetProjectId(); id != "" && !s.git(id) {
		for _, o := range s.tasks {
			if o.GetId() == t.GetId() || o.GetProjectId() != id || !s.writing(o) {
				continue
			}
			if overlap(t.GetWriteScopes(), o.GetWriteScopes()) {
				return fmt.Sprintf("%s writes %s, which overlaps %s", o.GetCode(), scopeText(o.GetWriteScopes()),
					scopeText(t.GetWriteScopes())), ""
			}
		}
	}
	return h.full(), ""
}

// full says why no worker may start now, or "" when one may.
func (h *Harness) full() string {
	if h.capacity == nil {
		return ""
	}
	slots, rule, pressure := h.capacity()
	if pressure != "" {
		return "the machine is under pressure: " + pressure
	}
	if running := h.Running(); running >= slots {
		if running == 1 {
			return "1 worker runs, the most this machine holds (" + rule + ")"
		}
		return fmt.Sprintf("%d workers run, the most this machine holds (%s)", running, rule)
	}
	return ""
}

// setWaiting records why a planned task waits, when the reason changed, with an event.
func (h *Harness) setWaiting(ctx context.Context, t *planv1.Task, why string) {
	if t.GetWaitReason() == why {
		return
	}
	t = proto.CloneOf(t)
	t.WaitReason = why
	h.writeAlone(ctx, actorHarness, methodWait, t, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "waiting: " + why})
}

// failPlanned ends a planned task that never will start.
func (h *Harness) failPlanned(ctx context.Context, t *planv1.Task, why string) {
	t = proto.CloneOf(t)
	t.Status, t.Error, t.WaitReason, t.EndTime = planv1.TaskStatus_TASK_STATUS_FAILED, why, "", timestamppb.Now()
	h.writeAlone(ctx, actorHarness, methodEnd, t, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "failed: " + why})
	h.wake() // Its own dependents fail in turn.
}

// writeAlone records an event of the task id, which no worker runs for, journaled with req (the event when nil),
// and the task when not nil. Its watchers read it from the store.
func (h *Harness) writeAlone(ctx context.Context, actor, method string, req proto.Message, id string, task *planv1.Task, ev Event) {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		seq, err := lastSeq(ctx, tx, id)
		if err != nil {
			return err
		}
		te := newEvent(id, seq+1, ev)
		if req == nil {
			req = te
		}
		if err := tx.Journal(actor, method, req); err != nil {
			return err
		}
		if task != nil {
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return tx.Put(te)
	})
	if err != nil {
		log.Printf("djinn: task %s: record an event: %v", id, err)
	}
	h.notify()
}

// launchPlanned starts the worker of a planned task that is ready: its access is decided now, with the wish's
// grants and the project's configuration as they are, and it may ask its edit question.
func (h *Harness) launchPlanned(ctx context.Context, t *planv1.Task) error {
	t = proto.CloneOf(t)
	t.WaitReason = ""
	provider, ok := h.providers[t.GetProvider()]
	if !ok {
		h.failPlanned(ctx, t, fmt.Sprintf("provider %s is not available", t.GetProvider()))
		return nil
	}
	prompt, err := firstPrompt(h.store, t.GetId())
	if err != nil {
		h.failPlanned(ctx, t, err.Error())
		return nil
	}
	seq, err := lastSeq(ctx, h.store, t.GetId())
	if err != nil {
		return err
	}
	r, err := h.newRun(t, seq)
	if err != nil {
		return err
	}
	var project *planv1.Project
	var prep prepared
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, t.GetWishId())
		if err != nil {
			return err
		}
		if t.GetProjectId() != "" {
			if project, err = store.Get[*planv1.Project](ctx, tx, t.GetProjectId()); err != nil {
				return err
			}
		}
		if prep, err = prepare(ctx, tx, t, wish, project); err != nil {
			return err
		}
		if err := tx.Journal(actorHarness, methodSchedule, t); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return err
	}
	_, err = h.launch(h.ctx, r, provider, project, prep, prompt)
	return err
}

// resolveDeps turns what a spawn names as dependencies, codes (W1, any case) or identifiers of tasks of the same
// wish, into task identifiers, without repeats.
func resolveDeps(ctx context.Context, r store.Reader, wishID string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range names {
		i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
			return t.GetId() == name || strings.EqualFold(t.GetCode(), name)
		})
		if i < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("dependency %s is not a task of the wish", name))
		}
		if id := tasks[i].GetId(); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// scopeRoot is a scope that names the whole folder.
var scopeRoot = regexp.MustCompile(`^\.?/?$`)

// cleanScopes checks the write scopes of a spawn: paths in the project's folder, slash-separated, cleaned and
// sorted, without repeats. A scope naming the whole folder leaves none: the whole folder.
func cleanScopes(scopes []string) ([]string, error) {
	var out []string
	for _, s := range scopes {
		s = filepath.ToSlash(strings.TrimSpace(s))
		if filepath.IsAbs(s) || path.IsAbs(s) || filepath.VolumeName(s) != "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("write scope %q: a path in the project's folder, not an absolute one", s))
		}
		s = path.Clean(s)
		if s == ".." || strings.HasPrefix(s, "../") {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("write scope %q leaves the project's folder", s))
		}
		if scopeRoot.MatchString(s) {
			return nil, nil
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out, nil
}

// overlap tells whether two sets of write scopes share a path: one names the other, or a folder holding it. No
// scope is the whole folder. Case is ignored, as on macOS and Windows.
func overlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			x, y := strings.ToLower(x), strings.ToLower(y)
			if x == y || strings.HasPrefix(y, x+"/") || strings.HasPrefix(x, y+"/") {
				return true
			}
		}
	}
	return false
}

// scopeText writes write scopes for a person.
func scopeText(scopes []string) string {
	if len(scopes) == 0 {
		return "the whole folder"
	}
	return strings.Join(scopes, ", ")
}
