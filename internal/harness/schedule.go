package harness

import (
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
	"github.com/empowill/djinn/internal/dispatch"
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

// WithPrefix runs every worker under prefix, its own command appended: djinn up --worker-cpu gives a systemd scope
// that caps the worker's CPU (machine.CPULimit).
func WithPrefix(prefix []string) Option { return func(h *Harness) { h.prefix = prefix } }

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

// Running is the number of workers that run now. A paused worker does not count: it takes no slot.
func (h *Harness) Running() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.runs {
		if !r.paused {
			n++
		}
	}
	return n
}

// planned tells whether Djinn starts the task by itself, once it is ready.
func planned(t *planv1.Task) bool { return dispatch.Planned(t) }

// schedule makes one pass over the planned tasks, the first wish first: each one starts, waits with its reason,
// or fails when a dependency failed. The rules are internal/dispatch's.
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
	if !slices.ContainsFunc(tasks, planned) {
		return
	}
	s, err := h.situation(ctx, tasks)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("djinn: schedule: %v", err)
		}
		return
	}
	for _, d := range s.Pass() {
		if h.ctx.Err() != nil {
			return
		}
		switch t := d.Task; {
		case d.Failed != "":
			h.failPlanned(ctx, t, d.Failed)
		case d.Why != "":
			h.setWaiting(ctx, t, d.Why)
		default:
			if err := h.launchPlanned(ctx, t); err != nil {
				log.Printf("djinn: task %s: %v", t.GetCode(), err)
			}
		}
	}
}

// situation is what a scheduling decision reads: the tasks, every wish, which projects are in Git, and what the
// machine allows now. The caller holds h.sched.
func (h *Harness) situation(ctx context.Context, tasks []*planv1.Task) (*dispatch.Situation, error) {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	git := make(map[string]bool, len(projects))
	for _, p := range projects {
		git[p.GetId()] = p.GetGit()
	}
	var m *dispatch.Machine
	if h.capacity != nil {
		m = &dispatch.Machine{Running: h.Running()}
		m.Slots, m.Rule, m.Pressure = h.capacity()
	}
	return dispatch.New(tasks, wishes, git, m).At(h.now()), nil
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
	if dispatch.Resuming(t) {
		return h.relaunch(ctx, t)
	}
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
