package harness

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
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

// WithBarSlots gives the most workers a notch itself lets run, before the memory's share (machine.Policy.BarSlots):
// lowering the notch pauses the workers above it, never a dip of the memory available. It is asked of the harness's
// own notch, so that a pass between the monitor's change and SetPolicy weighs the notch it names. Without it, the
// capacity's slots.
func WithBarSlots(n func(djinnv1.LoadNotch) int) Option { return func(h *Harness) { h.barSlots = n } }

// WithMemory holds a new worker until the memory available (0 when unknown) holds the typical peak of a worker of
// its provider, as p weighs it (machine.Policy.WorkerRoom). djinn up gives the machine's; without it, the memory
// holds no worker back. It needs WithCapacity.
func WithMemory(available func() uint64, p machine.Policy) Option {
	return func(h *Harness) {
		h.available = available
		h.policy.Store(&p)
	}
}

// WithPolicy sets the policy Djinn runs with (operating load notch, limits).
func WithPolicy(p machine.Policy) Option {
	return func(h *Harness) { h.policy.Store(&p) }
}

// WithTotal gives the machine's total RAM in bytes (0 when unknown) for peak forecast committable limits.
func WithTotal(total func() uint64) Option {
	return func(h *Harness) { h.total = total }
}

// SetPolicy updates the scheduling policy and wakes the scheduler for the next pass.
func (h *Harness) SetPolicy(p machine.Policy) {
	h.updatePriorities(p.LowPriority())
	logx.Lock(&h.sched, "scheduler")
	h.policy.Store(&p)
	if !p.Auto && h.capacity != nil {
		h.enforceNotchCapacity(h.ctx, p)
	}
	h.sched.Unlock()
	h.wake()
}

// Policy returns the current scheduling policy.
func (h *Harness) Policy() machine.Policy {
	if p := h.policy.Load(); p != nil {
		return *p
	}
	return machine.Policy{}
}

// WithScopes runs each worker in a systemd scope of its own, which stopping, pausing and measuring it take whole, its
// CPU and memory capped as scopes says (djinn up --worker-cpu, --worker-memory; machine.ProbeScopes).
func WithScopes(scopes *machine.Scopes) Option { return func(h *Harness) { h.scopes = scopes } }

// scope gives each process of the worker name (a task's code) a scope of its own; nil without scopes.
func (h *Harness) scope(name string) func() machine.Scope {
	if h.scopes == nil {
		return nil
	}
	return func() machine.Scope { return h.scopes.New(name) }
}

// scopeFor gives each process of task t a scope of its own with its memory ceiling (Policy.WorkerMemory and/or
// Policy.WorkerMemoryGuard); nil without scopes.
func (h *Harness) scopeFor(t *planv1.Task) func() machine.Scope {
	if h.scopes == nil {
		return nil
	}
	if t == nil {
		return h.scope("")
	}
	p := h.Policy()

	if !h.scopes.MemoryController && h.scopes.Memory == 0 {
		return func() machine.Scope { return h.scopes.New(t.GetCode()) }
	}

	var peak uint64
	var measured int
	if p.WorkerMemoryGuard > 0 && h.store != nil {
		ctx := h.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if tasks, err := store.List[*planv1.Task](ctx, h.store, nil); err == nil {
			peaks := dispatch.ProviderPeaks(tasks)
			peak, measured = p.Typical(peaks[dispatch.Provider(t)])
		}
	}
	ceiling := p.WorkerMemoryCeiling(peak, measured)
	if ceiling == 0 && p.WorkerMemoryGuard <= 0 && h.scopes.Memory > 0 {
		ceiling = h.scopes.Memory
	}
	return func() machine.Scope { return h.scopes.NewWithMemory(t.GetCode(), ceiling) }
}

// WithBeforeStart runs f before each worker's process starts: djinn up brings its PATH up to date there
// (machine.ExtendPath), so that a worker finds the agent the window's panel found.
func WithBeforeStart(f func()) Option { return func(h *Harness) { h.beforeStart = f } }

// prepare runs WithBeforeStart's function, if any.
func (h *Harness) prepare() {
	if h.beforeStart != nil {
		h.beforeStart()
	}
}

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
	defer logx.Crash("schedule")
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

// Running is the number of workers that run now, each in a slot: every agent, a question worker's included (W577:
// the load bar's count is the agents that run). A paused worker does not count: it takes no slot; nor does a
// watcher, which sleeps until its command prints.
func (h *Harness) Running() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.runs {
		if r.slotted() {
			n++
		}
	}
	return n
}

// Forced is the number of the workers running (Running) that the developer started or resumed by hand, whatever the
// slots (djinn task start, djinn task resume): the ones the load bar shows beyond what it lets run.
func (h *Harness) Forced() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.runs {
		if r.slotted() && r.forced {
			n++
		}
	}
	return n
}

// slotted tells whether the run's worker takes a slot now: it runs, is not a watcher, and is no ifrit that listens
// between two turns. The caller holds h.mu.
func (r *run) slotted() bool { return !r.paused && !r.watcher && r.listenSince.IsZero() }

// Works tells whether a worker of the task runs now and takes a slot: what it runs, a gate's command included, is in
// that slot. A paused worker or a watcher takes none.
func (h *Harness) Works(taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.runs[taskID]
	return ok && r.slotted()
}

// planned tells whether Djinn starts the task by itself, once it is ready.
func planned(t *planv1.Task) bool { return dispatch.Planned(t) }

// schedule makes one pass over the planned tasks, the first wish first: each one starts, waits with its reason,
// or fails when a dependency failed. The rules are internal/dispatch's.
func (h *Harness) schedule(ctx context.Context) {
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	if ctx.Err() != nil {
		return
	}
	start := time.Now()
	logx.L().Debug("scheduler pass begins")
	defer func() { logx.Took(slog.LevelDebug, "scheduler pass ended", start) }()
	p := h.Policy()
	if !p.Auto && h.capacity != nil {
		h.enforceNotchCapacity(ctx, p)
	}
	h.resumeNotchPaused(ctx)

	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		if ctx.Err() == nil {
			logx.L().Error("schedule failed", "loop", "schedule", "err", err)
		}
		return
	}
	if h.lamplighter(ctx, tasks) {
		if reloaded, err := store.List[*planv1.Task](ctx, h.store, nil); err == nil {
			tasks = reloaded
		}
	}
	if !slices.ContainsFunc(tasks, planned) {
		return
	}
	s, err := h.situation(ctx, tasks)
	if err != nil {
		if ctx.Err() == nil {
			logx.L().Error("schedule failed", "loop", "schedule", "err", err)
		}
		return
	}
	for _, d := range s.Pass() {
		if h.ctx.Err() != nil {
			return
		}
		if d.Commit {
			h.kickIntegrate() // A task another waits for is committed at once.
		}
		switch t := d.Task; {
		case d.Failed != "":
			h.failPlanned(ctx, t, d.Failed)
		case d.Slot && t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR:
			// An investigator takes the slot of the work task it pauses, never one more: none to pause, it waits.
			if code, err := h.preemptForSlot(ctx, t); err == nil && code != "" {
				if err := h.launchPlannedWithPreemption(ctx, t, code); err != nil {
					logx.L().Error("start a planned task", "loop", "schedule", "task", t.GetCode(), "err", err)
				}
				continue
			}
			h.setWaiting(ctx, t, d.Why, d.Machine)
		case d.Why != "":
			if isMemoryShort(d.Why) {
				if code, err := h.interruptPausedForMemory(ctx, t); err == nil && code != "" {
					if err := h.launchPlannedWithPreemption(ctx, t, code); err != nil {
						logx.L().Error("start a planned task", "loop", "schedule", "task", t.GetCode(), "err", err)
					}
					continue
				}
			}
			if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR && isMemoryShort(d.Why) {
				if code, err := h.preemptForMemory(ctx, t); err == nil && code != "" {
					if err := h.launchPlannedWithPreemption(ctx, t, code); err != nil {
						logx.L().Error("start a planned task", "loop", "schedule", "task", t.GetCode(), "err", err)
					}
					continue
				}
			}
			h.setWaiting(ctx, t, d.Why, d.Machine)
		default:
			if err := h.launchPlanned(ctx, t); err != nil {
				logx.L().Error("start a planned task", "loop", "schedule", "task", t.GetCode(), "err", err)
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
	projectGit := make(map[string]bool, len(projects))
	projectDirs := make(map[string]string, len(projects))
	for _, p := range projects {
		projectGit[p.GetId()] = p.GetGit()
		projectDirs[p.GetId()] = p.GetDirectory()
	}
	var m *dispatch.Machine
	if h.capacity != nil {
		m = &dispatch.Machine{Running: h.Running()}
		m.Slots, m.Rule, m.Pressure = h.capacity()
		h.mu.Lock()
		outside := h.outside
		for _, r := range h.runs {
			if r.paused && (r.notchPaused || r.slotPaused) && !r.stopping && !r.final {
				m.Resuming++
			}
		}
		h.mu.Unlock()
		if outside != nil {
			// Djinn serves the gates: one slot is kept for the merges' and pushes' (Q26).
			m.Gates, m.Merges = outside()
			m.Keep = true
		}
		if h.available != nil {
			m.Available, m.Policy = h.available(), h.Policy()
		}
		if h.total != nil {
			m.Total = h.total()
		}
	}
	type branchState struct {
		head      string
		noCommits bool
	}
	branchCache := make(map[[2]string]branchState)
	branchHead := func(projectID, branch string) (string, bool) {
		if branch == "" {
			return "", false
		}
		key := [2]string{projectID, branch}
		if st, ok := branchCache[key]; ok {
			return st.head, st.noCommits
		}
		dir := projectDirs[projectID]
		if dir == "" || !projectGit[projectID] {
			return "", false
		}
		head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
		if err != nil || head == "" {
			if h, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", branch+"^{commit}"); err == nil && h != "" {
				head = h
			} else if branch == plan.CheckedOutBranch(ctx, dir) || branch == "HEAD" {
				head, _ = git(ctx, dir, "rev-parse", "HEAD")
			}
		}
		noCommits := head == "" || branchStartCommit(ctx, dir, branch) == head
		branchCache[key] = branchState{head: head, noCommits: noCommits}
		return head, noCommits
	}
	isAncestor := func(projectID, a, b string) bool {
		if a == "" || b == "" {
			return false
		}
		dir := projectDirs[projectID]
		if dir == "" || !projectGit[projectID] {
			return false
		}
		_, err := git(ctx, dir, "merge-base", "--is-ancestor", a, b)
		return err == nil
	}
	sit := dispatch.New(tasks, wishes, projectGit, m).At(h.now())
	sit.WithAncestry(branchHead, isAncestor)
	sit.WithRunning(func(taskID string) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.runs[taskID] != nil
	})
	return sit, nil
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

// HeldGates tells the harness which gates a task holds, and which it waits for, by name: a worker holding or
// waiting for one is not paused. djinn up gives the gates' (gate.Gates.Held and Waiting), once they exist. They run
// under h.mu: they must never call the harness back.
func (h *Harness) HeldGates(held, waiting func(taskID string) []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held, h.waiting = held, waiting
}

// GatesOutside tells the harness how many gates are held outside its running workers, by a person's terminal, a
// lead, a script or Djinn's own merges and pushes, and how many of them are those merges' and pushes': each takes a
// slot, so no new worker starts on it while it is held. Once told, the scheduler keeps one slot for the merges and
// pushes on a machine of more than one (dispatch.Machine.Keep). djinn up gives the gates' (gate.Gates.Outside). n
// calls the harness back (Works): it runs without h.mu (W549).
func (h *Harness) GatesOutside(n func() (outside, merges int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outside = n
}

// setWaiting records why a planned task waits, and whether for the machine alone, when the reason changed, with an
// event.
func (h *Harness) setWaiting(ctx context.Context, t *planv1.Task, why string, machine bool) {
	why = grantedWhy(t, shelvedWhy(t, preemptedWaitReason(t, why)))
	if t.GetWaitReason() == why && t.GetMachineWait() == machine {
		return
	}
	t = proto.CloneOf(t)
	t.WaitReason, t.MachineWait = why, machine
	h.writeAlone(ctx, actorHarness, methodWait, t, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "waiting: " + why})
}

// failPlanned ends a planned task that never will start.
func (h *Harness) failPlanned(ctx context.Context, t *planv1.Task, why string) {
	t = proto.CloneOf(t)
	t.Status, t.Error, t.WaitReason, t.MachineWait, t.EndTime = planv1.TaskStatus_TASK_STATUS_FAILED, why, "", false, timestamppb.Now()
	h.writeAlone(ctx, actorHarness, methodEnd, t, t.GetId(), t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "failed: " + why})
	h.wake() // Its own dependents fail in turn.
}

// writeAlone records an event of the task id, outside its run when a worker runs for it, journaled with req (the event when nil),
// and the task when not nil. Its watchers read it from the store.
func (h *Harness) writeAlone(ctx context.Context, actor, method string, req proto.Message, id string, task *planv1.Task, ev Event) {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		te := h.newEvent(id, ev)
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
			if method == methodResume && task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && task.GetPartOf() != "" {
				if err := reopenAzima(ctx, tx, task.GetPartOf(), task.GetCode()+" started"); err != nil {
					return err
				}
			}
		}
		return putEvents(ctx, tx, te)
	})
	if err != nil {
		logx.L().Error("record an event", "task", id, "err", err)
	}
	h.notify()
}

// launchPlanned starts the worker of a planned task that is ready: its access is decided now, with the wish's
// grants and the project's configuration as they are, and it may ask its edit question.
func (h *Harness) launchPlanned(ctx context.Context, t *planv1.Task) error {
	return h.launchPlannedWithPreemption(ctx, t, "")
}

// launchPlannedWithPreemption starts the worker of a planned task, recording an event if a task was preempted to make room.
func (h *Harness) launchPlannedWithPreemption(ctx context.Context, t *planv1.Task, preemptedCode string) error {
	if dispatch.Resuming(t) {
		return h.relaunch(ctx, t, preemptedCode)
	}
	provider, ok := h.providers[t.GetProvider()]
	if !ok {
		h.failPlanned(ctx, t, fmt.Sprintf("provider %s is not available", t.GetProvider()))
		return nil
	}
	prompt, err := lastPrompt(ctx, h.store, t.GetId())
	if err != nil {
		h.failPlanned(ctx, t, err.Error())
		return nil
	}
	var project *planv1.Project
	if t.GetProjectId() != "" {
		p, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
		if err != nil {
			h.failPlanned(ctx, t, err.Error())
			return nil
		}
		project = p
	}
	var settings plan.Settings
	if project != nil && project.GetGit() && t.GetProvider() != planv1.Provider_PROVIDER_WATCH {
		var err error
		if settings, err = plan.LoadSettings(h.home, project); err != nil {
			h.setWaiting(ctx, t, fmt.Sprintf("settings unreadable: %v", err), false)
			return nil
		}
		if !questionWorker(t) && t.GetReview() == nil && t.GetCorrection().GetFailure() == nil && len(t.GetDependsOn()) > 0 {
			if wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId()); err == nil {
				from := h.workerStartBranch(ctx, wish, project, settings, t)
				if sha, realFrom, err := startPoint(ctx, project.GetDirectory(), from); err == nil {
					if depWhy := h.dependenciesUnmergedIn(ctx, project.GetDirectory(), t, sha, realFrom); depWhy != "" {
						h.setWaiting(ctx, t, depWhy, false)
						return nil
					}
				}
			}
		}
	}
	// A babysit spawned by hand watches what the tasks it comes after opened: its command, filled, is its prompt
	// from now on.
	var prompted *planv1.TaskEvent
	if t.GetBabysit() != nil && t.GetBabysit().GetNumber() == 0 && project != nil {
		filled, why := h.babysitPrompt(ctx, project, t, prompt)
		if why != "" {
			h.failPlanned(ctx, t, why)
			t = proto.CloneOf(t)
			t.Error = why
			h.tellWatcherFailed(t)
			return nil
		}
		prompt = filled
		prompted = h.newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})
	}
	t = proto.CloneOf(t)
	t.WaitReason, t.MachineWait = "", false
	r, err := h.newRun(t)
	if err != nil {
		return err
	}
	var preEv *planv1.TaskEvent
	if preemptedCode != "" {
		preEv = h.newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "preempted " + preemptedCode + " to make room"})
	}
	var wish *planv1.Wish
	var prep prepared
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, t.GetWishId()); err != nil {
			return err
		}
		if prep, err = prepare(ctx, tx, t, wish, project); err != nil {
			return err
		}
		if err := tx.Journal(actorHarness, methodSchedule, t); err != nil {
			return err
		}
		if err := tx.Put(t); err != nil {
			return err
		}
		if prompted != nil {
			if err := putEvents(ctx, tx, prompted); err != nil {
				return err
			}
		}
		if preEv != nil {
			return putEvents(ctx, tx, preEv)
		}
		return nil
	})
	if err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return err
	}
	if prompted != nil {
		h.publish(r, prompted)
	}
	if project != nil && project.GetGit() && t.GetProvider() != planv1.Provider_PROVIDER_WATCH {
		// Its branch follows the project's settings as they are now, like its access.
		r.branch, r.from = settings.Branch, h.workerStartBranch(h.ctx, wish, project, settings, t)
		r.setChecks(settings)
		if questionWorker(t) {
			p, _, fb := questionProvider(settings)
			if t.GetProvider() == p {
				r.fallback = fb
			}
		}
	}
	_, err = h.launch(h.ctx, r, provider, project, prep, prompt)
	return err
}

// Start starts a planned task that waits for the machine alone, now: the developer asked for it, whatever the slots,
// the pressure and the memory. Its worker takes a slot all the same: the next tasks wait for it, and the load bar
// counts it among the workers started by hand (Forced). A task held by anything else (its wish, a dependency, a push hold, its provider's usage
// limit, a write scope) is refused, with why, and so is one that waits for a task going first: Start frees the
// machine, never the priority order. The scheduler's pass decides, as it would now.
func (h *Harness) Start(ctx context.Context, procedure string, req *planv1.TaskServiceStartRequest) (*planv1.Task, error) {
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if !planned(task) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is not planned: %s", task.GetCode(), short(task.GetStatus())))
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return nil, plan.Status(err)
	}
	s, err := h.situation(ctx, tasks)
	if err != nil {
		return nil, plan.Status(err)
	}
	for _, d := range s.Pass() {
		if d.Task.GetId() == task.GetId() && (d.Failed != "" || d.Why != "" && !d.Machine) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s waits for more than the machine: %s", task.GetCode(), cmp.Or(d.Failed, d.Why)))
		}
	}
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return tx.Journal(actorLocal, procedure, req) }); err != nil {
		return nil, plan.Status(err)
	}
	// It takes a slot whatever the others: the load bar shows it beyond what it lets run (Forced).
	h.mu.Lock()
	h.forcing = task.GetId()
	h.mu.Unlock()
	err = h.launchPlanned(ctx, task)
	h.mu.Lock()
	h.forcing = ""
	h.mu.Unlock()
	if err != nil {
		return nil, plan.Status(err)
	}
	task, err = store.Get[*planv1.Task](ctx, h.store, task.GetId())
	return task, plan.Status(err)
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
	return resolveIn(tasks, names, "dependency")
}

// resolveIn turns names, codes (W1, any case) or identifiers of tasks among tasks, repeated or separated by commas or
// spaces (--after W1,W2; "after: T08 T17"), into task identifiers, without repeats. A name no task has is refused:
// what says what it named ("dependency W9 is not a task of the wish").
func resolveIn(tasks []*planv1.Task, names []string, what string) ([]string, error) {
	var out []string
	for _, list := range names {
		for _, name := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
				return t.GetId() == name || strings.EqualFold(t.GetCode(), name)
			})
			if i < 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s %s is not a task of the wish", what, name))
			}
			if id := tasks[i].GetId(); !slices.Contains(out, id) {
				out = append(out, id)
			}
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
