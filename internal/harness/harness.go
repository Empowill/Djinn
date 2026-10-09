package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Who changed a task, as the journal records it: the user's commands are journaled as received; what the
// harness and the workers bring is journaled under these names.
const (
	actorLocal   = "local"
	actorHarness = "harness"
	actorWorker  = "worker"

	methodStart    = "harness/start"    // the task's worker starts; the request is the task
	methodEvent    = "harness/event"    // the worker said something; the request is the event
	methodEnd      = "harness/end"      // the worker ended; the request is the task
	methodRecover  = "harness/recover"  // djinn up found a task whose worker it had lost; the request is the task
	methodAnswer   = "harness/answer"   // the task took the answer to its edit question; the request is the task
	methodReceived = "harness/received" // the worker took a message in; the request is the event
	methodHold     = "harness/hold"     // the task's worker was paused or resumed; the request is the task
	methodMeasure  = "harness/measure"  // what the task's worker uses was read; the request is the reading
	methodAzima    = "harness/azima"    // djinn up found an azima stored as work, from before kinds; the request is the task
)

// maxText is the most of an event's text, and of its raw line, that is kept.
const maxText = 64 << 10

// Providers are the providers Djinn knows, by the name a task gives.
func Providers() map[planv1.Provider]Provider {
	return map[planv1.Provider]Provider{
		planv1.Provider_PROVIDER_CLAUDE:      Claude{},
		planv1.Provider_PROVIDER_FAKE:        Fake{},
		planv1.Provider_PROVIDER_CODEX:       Codex{},
		planv1.Provider_PROVIDER_ANTIGRAVITY: Antigravity{},
		planv1.Provider_PROVIDER_WATCH:       Watch{},
	}
}

// Harness runs the tasks' workers and records what they say. Close it before the store.
type Harness struct {
	store     *store.Store
	home      string
	providers map[planv1.Provider]Provider

	ctx    context.Context // cancelled by Close: every worker stops
	cancel context.CancelFunc
	wg     sync.WaitGroup // one per run

	answering sync.Mutex // one answer to an edit question at a time

	scopes *machine.Scopes // the systemd scopes the workers run in, one each (WithScopes); nil: none

	clock func() time.Time // nil: the machine's (WithClock)

	measureEvery time.Duration // how often a worker is read (WithMeasure); 0: never
	measureRead  MeasureFunc   // nil: workers are not measured

	tell    TellFunc    // wakes the lead of a watcher's wish (TellLeads); guarded by mu
	watched WatchedFunc // reads a watcher's new paragraph (OnWatched); guarded by mu

	// The scheduler (schedule.go).
	capacity   Capacity      // nil: no limit
	available  func() uint64 // the memory available; nil: the memory holds no worker back
	policy     machine.Policy
	tick       time.Duration // a pass at least this often
	sched      sync.Mutex    // one scheduling decision at a time: a spawn, a pass, a planned task stopped
	kick       chan struct{} // wakes the scheduler
	scheduling sync.Once
	loopDone   chan struct{} // closed when the scheduler has stopped; nil until Schedule

	// The integration of finished work (integrate.go).
	gates         TakeGate      // nil: commands run without a gate
	commands      RunCommand    // nil: the processes the commands name
	integrateTick time.Duration // a pass at least this often; 0: a minute
	integrateKick chan struct{} // wakes the integration
	integrating   sync.Once
	integrateDone chan struct{}     // closed when the integration has stopped; nil until Integrate
	tested        map[string]tested // by wish/project; owned by the integration's pass

	// Warm workers (warm.go), guarded by sched.
	warmOn bool
	warm   map[string]*warm // by wish/project

	mu      sync.Mutex
	closed  bool
	held    func(taskID string) []string // the gates a task holds (HeldGates); nil: none known
	waiting func(taskID string) []string // the gates a task waits for (HeldGates); nil: none known
	outside func() int                   // the gates held outside the running workers (GatesOutside); nil: none
	runs    map[string]*run              // by task id
	changed chan struct{}                // closed at the next change of a task without worker (notifyLocked)
}

// run is a task at work: its worker, or the workers it runs one after the other when the task starts again.
type run struct {
	id    string
	wish  string             // the task's wish, which never changes
	done  chan struct{}      // closed once the task has its final status
	wake  chan struct{}      // an answer waits in answers
	notes chan Event         // events from outside the worker (a gate), for the pump to write
	sends chan *message      // messages for the worker (inbox.go), for the pump to deliver
	holds chan chan struct{} // a pause or a resume for the pump to write; closed once written

	// Guarded by Harness.mu.
	worker   Worker
	stopping bool
	shelved  bool                 // its wish was paused: the worker stops, the task resumes with the wish (shelve.go)
	paused   bool                 // the worker holds still: it takes no slot
	watcher  bool                 // a watcher runs a command, and takes no slot
	final    bool                 // the task is getting its final status: an answer waits for done instead
	answers  []*planv1.Question   // answers to the task's edit question, for the pump to apply
	use      *machinev1.WorkerUse // what its worker uses, at the last reading (measure.go); nil before one
	subs     map[chan *planv1.TaskEvent]struct{}

	// Owned by whoever writes the task: Spawn, then the pump.
	task    *planv1.Task
	seq     int64         // last event written
	base    *planv1.Usage // what the task had spent before this worker
	restart bool          // the worker stops to start again, allowed to edit
	unread  []string      // messages the worker took on its input and has said nothing after yet
	warm    *warm         // the warm worker the task takes, until launch
	branch  string        // the branch template of the project's settings, for launch; empty: the default
	failure string        // the last error the current worker said: it never ends done
	limit   *Limit        // the usage limit the current worker said it hit: it wins over failure
}

// newRun is the run of task, its next event after seq, registered so that a watcher never misses its first events.
// It fails when Djinn is stopping, or when the task already runs.
func (h *Harness) newRun(task *planv1.Task, seq int64) (*run, error) {
	r := &run{
		id: task.GetId(), wish: task.GetWishId(), task: task, seq: seq, base: task.GetUsage(), done: make(chan struct{}), wake: make(chan struct{}, 1),
		notes: make(chan Event, 64), sends: make(chan *message), holds: make(chan chan struct{}),
		subs: map[chan *planv1.TaskEvent]struct{}{}, watcher: watching(task),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("djinn is stopping"))
	case h.runs[r.id] != nil:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is running", task.GetCode()))
	}
	h.runs[r.id] = r
	h.wg.Add(1)
	h.notifyLocked() // A watcher of the planned task follows its worker now.
	return r, nil
}

// New returns a harness on the store s. Worktrees go under home, Djinn's data folder. Without options, nothing limits
// the workers and the wishes are served by rank; Schedule starts the planned tasks.
func New(s *store.Store, home string, providers map[planv1.Provider]Provider, opts ...Option) *Harness {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Harness{
		store: s, home: home, providers: providers, ctx: ctx, cancel: cancel, runs: map[string]*run{},
		tick: 2 * time.Second, kick: make(chan struct{}, 1), changed: make(chan struct{}), warm: map[string]*warm{},
		integrateKick: make(chan struct{}, 1), tested: map[string]tested{},
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Close stops the scheduler and every worker, waits for them to end, and records their tasks as interrupted.
func (h *Harness) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.cancel()
	h.scheduling.Do(func() {}) // No scheduler starts from now on.
	if h.loopDone != nil {
		<-h.loopDone
	}
	h.integrating.Do(func() {}) // Nor any integration.
	if h.integrateDone != nil {
		<-h.integrateDone
	}
	h.closeWarm()
	h.wg.Wait()
}

// Recover marks as interrupted the tasks a previous djinn up left running: their workers died with it. They keep
// what resuming them needs (provider, session, worktree). Then every interrupted task Djinn may resume waits for the
// scheduler to resume it (queueInterrupted). First, the azimas stored as work before tasks had kinds become azimas
// (migrateAzimas).
func (h *Harness) Recover(ctx context.Context) error {
	h.cleanWarmLeftovers(ctx)
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return err
	}
	if err := h.migrateAzimas(ctx, tasks); err != nil {
		return err
	}
	h.recoverIntegrations(ctx, tasks)
	for _, t := range tasks {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
		default:
			continue
		}
		// A task no worker ever started (planned, or imported with a wish) waits for one: nothing to interrupt.
		if t.GetStartTime() == nil {
			continue
		}
		t.Status, t.Error, t.EndTime = planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "djinn up ended while the worker ran", timestamppb.Now()
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodRecover, t); err != nil {
				return err
			}
			seq, err := lastSeq(ctx, tx, t.GetId())
			if err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			return tx.Put(newEvent(t.GetId(), seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "interrupted: " + t.GetError()}))
		})
		if err != nil {
			return fmt.Errorf("recover task %s: %w", t.GetCode(), err)
		}
	}
	return h.queueInterrupted(ctx, tasks)
}

// Spawn creates a task and starts its worker: in a Git project, in a new worktree on its own branch. procedure
// is the method the request came by, for the journal. A task that cannot start now (a dependency not done, its
// write scope taken, no slot free, the machine under pressure), or planned for later, is created pending with the
// reason, and the scheduler starts it as soon as it can. What the worker may do is decided when it starts
// (prepare).
func (h *Harness) Spawn(ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
	if req.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
		return h.spawnAzima(ctx, procedure, req)
	}
	scopes, err := cleanScopes(req.GetWriteScopes())
	if err != nil {
		return nil, err
	}
	task := &planv1.Task{
		Id: store.NewID(), WishId: req.GetWishId(), Title: req.GetTitle(), Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		CreateTime: timestamppb.Now(), Model: req.GetModel(), MaxBudgetUsd: req.GetMaxBudgetUsd(),
		WriteScopes: scopes, Scheduled: true,
	}
	prompt := cmp.Or(req.GetPrompt(), req.GetTitle())

	h.sched.Lock()
	defer h.sched.Unlock()
	// What the task needs, read before it exists, tells whether it starts in this call.
	wish, err := store.Get[*planv1.Wish](ctx, h.store, req.GetWishId())
	if err != nil {
		return nil, plan.Status(err)
	}
	project, err := pickProject(ctx, h.store, wish, req.GetProjectId())
	if err != nil {
		return nil, plan.Status(err)
	}
	task.ProjectId = project.GetId()
	// The project's settings fill what the request leaves out (docs/team-settings.md). A watcher runs a command:
	// none applies to it.
	var settings plan.Settings
	if req.GetProvider() != planv1.Provider_PROVIDER_WATCH {
		if settings, err = plan.LoadSettings(h.home, project); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s: %w", project.GetName(), err))
		}
	}
	kind := cmp.Or(req.GetProvider(), settings.Provider, planv1.Provider_PROVIDER_CLAUDE)
	src, err := forkSource(ctx, h.store, wish, req)
	if err != nil {
		return nil, plan.Status(err)
	}
	if src.session != "" {
		task.ForkSession, task.ForkOf, kind = src.session, src.of, src.provider
	}
	// A fork of a task cut short, failed or stopped continues it: the parent is closed with the fork, in its
	// transaction. A fork of a task that runs or is done leaves it as it is.
	var parent *planv1.Task
	if src.task != nil && forkCloses(src.task) {
		h.mu.Lock()
		if h.runs[src.task.GetId()] == nil {
			parent = src.task
		}
		h.mu.Unlock()
	}
	task.Provider = kind
	provider, ok := h.providers[kind]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider %s is not available", kind))
	}
	if kind != planv1.Provider_PROVIDER_WATCH {
		if task.Model == "" && kind == settings.Provider {
			task.Model = settings.Model
		}
		if task.MaxBudgetUsd == 0 {
			task.MaxBudgetUsd = settings.MaxBudgetUSD
		}
	}
	if kind == planv1.Provider_PROVIDER_WATCH {
		if project == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a watcher runs its command in a project: the wish has none"))
		}
		task.Restart = req.GetRestart()
	} else if req.GetRestart() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--restart is for a watcher (--provider watch)"))
	}
	if task.DependsOn, err = resolveDeps(ctx, h.store, wish.GetId(), after(req)); err != nil {
		return nil, plan.Status(err)
	}
	if task.PartOf, err = resolveAzima(ctx, h.store, wish.GetId(), req.GetPartOf()); err != nil {
		return nil, plan.Status(err)
	}
	if ref := req.GetDecision(); ref != "" {
		if task.Decision, err = plan.Decision(ctx, h.store, wish.GetId(), ref); err != nil {
			return nil, plan.Status(err)
		}
	}
	// What it blocks is checked now, the decision reads them waiting for it, and they are written with it.
	blocked, err := h.insertBefore(ctx, h.store, task, req.GetBlocks())
	if err != nil {
		return nil, plan.Status(err)
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return nil, plan.Status(err)
	}
	for _, b := range blocked {
		tasks[slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == b.GetId() })] = b
	}
	// It takes its turn in the scheduler's pass: the planned tasks before it, the ones Djinn resumes first, take the
	// free slots before it does.
	sit, err := h.situation(ctx, append(tasks, task))
	if err != nil {
		return nil, plan.Status(err)
	}
	var why, failed string
	for _, d := range sit.Pass() {
		if d.Task == task {
			why, failed = d.Why, d.Failed
		}
	}
	switch {
	case failed != "":
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the task would never start: %s", failed))
	case req.GetLater() && why == "":
		why = "planned: it starts at the scheduler's next pass"
	}
	if why != "" {
		return h.plan(ctx, procedure, req, task, parent, prompt, why)
	}

	// A warm worker of the wish in the project, when the task asks for nothing it was not started with.
	wk := h.claimWarm(ctx, wish, project, task)
	if wk != nil {
		task.Id = wk.id
	}
	r, err := h.newRun(task, 1)
	if err != nil {
		if wk != nil {
			h.dropWarm(wk, "the task did not start")
		}
		return nil, err
	}
	r.warm, r.branch = wk, settings.Branch
	var prep prepared
	prompted := newEvent(task.GetId(), r.seq, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		if task.Code, err = nextCode(ctx, tx, wish.GetId()); err != nil {
			return err
		}
		if parent != nil {
			if err := closeParent(ctx, tx, parent, task.GetCode()); err != nil {
				return err
			}
		}
		if prep, err = prepare(ctx, tx, task, wish, project); err != nil {
			return err
		}
		if err := h.block(ctx, tx, task, req.GetBlocks()); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return tx.Put(prompted)
	})
	if err != nil {
		if wk != nil {
			h.dropWarm(wk, "the task did not start")
		}
		h.forget(r)
		return nil, plan.Status(err)
	}
	h.publish(r, prompted)
	if len(blocked) > 0 {
		h.wake() // The tasks it blocks say what they wait for now.
	}
	return h.launch(ctx, r, provider, project, prep, prompt)
}

// after is what a spawn says comes before its task: --after, and --depends-on, its former name.
func after(req *planv1.TaskServiceSpawnRequest) []string {
	return append(slices.Clip(req.GetDependsOn()), req.GetAfter()...)
}

// plan creates a task that waits, with its prompt and why it waits, closes the parent it continues, if any, and makes
// the tasks it blocks (--blocks) wait for it; the scheduler starts it.
func (h *Harness) plan(
	ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest, task, parent *planv1.Task, prompt, why string,
) (*planv1.Task, error) {
	task.WaitReason = why
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		var err error
		if task.Code, err = nextCode(ctx, tx, task.GetWishId()); err != nil {
			return err
		}
		if parent != nil {
			if err := closeParent(ctx, tx, parent, task.GetCode()); err != nil {
				return err
			}
		}
		if err := h.block(ctx, tx, task, req.GetBlocks()); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		if err := tx.Put(newEvent(task.GetId(), 1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})); err != nil {
			return err
		}
		return tx.Put(newEvent(task.GetId(), 2, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "waiting: " + why}))
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	h.wake()
	return task, nil
}

// prepared is what deciding a task's access gives its worker: the permissions the project declares, and the edit
// question the task asks.
type prepared struct {
	declared *djinnv1.Permissions
	question *planv1.Question
}

// prepare decides what the task's worker may do (decideAccess), in tx: in a folder outside Git without any agent
// configuration, the worker starts read-only, and the task asks the developer whether it may edit the project's
// files.
func prepare(ctx context.Context, tx *store.Tx, task *planv1.Task, wish *planv1.Wish, project *planv1.Project) (prepared, error) {
	var p prepared
	var err error
	if task.Access, p.declared, err = decideAccess(project, task.GetProvider(), plan.AllowanceOf(wish, project.GetId())); err != nil {
		return p, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s: %w", project.GetName(), err))
	}
	if task.GetAccess() == planv1.TaskAccess_TASK_ACCESS_ASKING && watching(task) {
		return p, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"a watcher runs a command, and %s lets no worker run one: it is outside Git, without agent configuration; "+
				"list the command in its %s", project.GetName(), filepath.ToSlash(PermissionsFile)))
	}
	if task.GetAccess() == planv1.TaskAccess_TASK_ACCESS_ASKING {
		p.question = editQuestion(task, project)
		if err := plan.Ask(ctx, tx, p.question); err != nil {
			return p, err
		}
		task.EditQuestionId = p.question.GetId()
	}
	return p, nil
}

// launch starts the worker of the run's task, now written with its access: in its worktree, its project's folder,
// or an empty folder of its own. A failure ends the task, and is returned.
func (h *Harness) launch(
	ctx context.Context, r *run, provider Provider, project *planv1.Project, prep prepared, prompt string,
) (*planv1.Task, error) {
	task := r.task
	readOnly, perms := accessSpec(task.GetAccess(), prep.declared)
	wk := r.warm
	r.warm = nil
	if wk != nil && !wk.fits(readOnly, perms) {
		// Decided otherwise in the meantime: its worktree is the task's, so it goes first.
		h.stopWarm(wk)
		wk = nil
	}
	var skills []Skill
	var skillsDir string
	if !r.watcher { // A command reads no skill.
		skills, skillsDir = h.summon(ctx, r, project)
	}
	if wk != nil && len(skills) > 0 {
		// A warm worker started without the project's summoned skills: the task starts cold, with them.
		h.stopWarm(wk)
		wk = nil
	}
	dir := project.GetDirectory()
	if project == nil {
		// Outside any project the worker only reads, in an empty folder of its own.
		dir = scratchDir(h.home, task.GetId())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			h.finish(r, Result{ExitCode: -1, Err: fmt.Errorf("create the task's folder: %w", err)})
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("task %s: create its folder: %w", task.GetCode(), err))
		}
	}
	if project.GetGit() && !r.watcher { // A watcher writes nothing: it runs in the project's folder.
		task.Branch = branchName(r.branch, task.GetCode(), task.GetTitle(), task.GetId())
		task.Worktree = worktreeDir(h.home, project.GetId(), task.GetId())
		var err error
		if wk != nil {
			dir = wk.spec.Dir
		} else {
			dir, err = addWorktree(ctx, project.GetDirectory(), task.GetWorktree(), task.GetBranch())
		}
		if err != nil {
			task.Branch, task.Worktree = "", ""
			h.finish(r, Result{ExitCode: -1, Err: fmt.Errorf("create the worktree: %w", err)})
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: create the worktree: %w", task.GetCode(), err))
		}
	}
	where := "in " + dir
	if task.GetBranch() != "" {
		where += ", on branch " + task.GetBranch()
	}
	spec := Spec{
		TaskID: task.GetId(), Dir: dir, ReadOnly: readOnly, Permissions: perms, Prompt: prompt, Model: task.GetModel(),
		MaxBudgetUSD: task.GetMaxBudgetUsd(), Resume: task.GetForkSession(), Fork: task.GetForkSession() != "",
		Skills: skills, SkillsDir: skillsDir, Restart: task.GetRestart(),
	}
	if task.GetForkOf() != "" {
		where += ", forked from " + forkText(task.GetForkOf())
	}
	text := "started " + short(task.GetProvider()) + " " + where + ", " + accessText(task, prep.question) +
		skillsText(spec.Skills)
	if r.watcher {
		text = "started watch " + where + watchText(task)
	}
	var err error
	if wk != nil {
		err = h.adoptWarm(ctx, r, provider, wk, spec, text)
	} else {
		err = h.start(r, provider, spec, text)
	}
	switch {
	case err == nil:
	case errors.Is(err, ErrReadOnly) && task.GetAccess() == planv1.TaskAccess_TASK_ACCESS_ASKING:
		// The agent cannot be kept from writing: it waits for the developer's answer instead of reading first.
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: err.Error()})
		h.finish(r, Result{})
		waiting, err := store.Get[*planv1.Task](ctx, h.store, task.GetId())
		return waiting, plan.Status(err)
	default:
		h.finish(r, Result{ExitCode: -1, Err: err})
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: %w", task.GetCode(), err))
	}
	started := proto.CloneOf(task)
	go h.pump(r)
	return started, nil
}

// start marks the run's task running, says so with text, and starts its worker with spec. On an error the task is
// left to the caller to end.
func (h *Harness) start(r *run, provider Provider, spec Spec, text string) error {
	t := r.task
	t.Status, t.StartTime, t.EndTime, t.ExitCode, t.Error = planv1.TaskStatus_TASK_STATUS_RUNNING, timestamppb.Now(), nil, 0, ""
	r.failure, r.limit = "", nil
	fresh(t)
	h.write(r, actorHarness, methodStart, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	// A worker that calls djinn knows its task.
	spec.Env = []string{"DJINN_TASK_ID=" + t.GetId(), "DJINN_WISH_ID=" + t.GetWishId()}
	spec.Scope = h.scope(t.GetCode())
	w, err := provider.Start(h.ctx, spec)
	if err != nil {
		return err
	}
	h.mu.Lock()
	r.worker, r.use = w, nil
	if r.stopping || r.shelved {
		w.Stop()
	}
	h.mu.Unlock()
	return nil
}

// accessText says, in the task's start event, where its worker's rights come from.
func accessText(t *planv1.Task, question *planv1.Question) string {
	switch t.GetAccess() {
	case planv1.TaskAccess_TASK_ACCESS_READ_ONLY:
		return "read-only: outside any project"
	case planv1.TaskAccess_TASK_ACCESS_AGENTS:
		return "with the project's " + filepath.ToSlash(PermissionsFile)
	case planv1.TaskAccess_TASK_ACCESS_ASKING:
		return "read-only until " + question.GetCode() + " is answered: the folder is outside Git and has no agent configuration"
	case planv1.TaskAccess_TASK_ACCESS_EDIT_GRANTED:
		return "allowed to edit the project's files, and to run no command"
	case planv1.TaskAccess_TASK_ACCESS_WISH_EDIT:
		return "allowed to edit by the wish"
	case planv1.TaskAccess_TASK_ACCESS_WISH_AUTO:
		return "in auto mode by the wish"
	}
	return "with the agent's own configuration of the project"
}

// editQuestion is the question a task asks when its project is a folder outside Git without any agent
// configuration: may its worker edit the project's files?
func editQuestion(t *planv1.Task, project *planv1.Project) *planv1.Question {
	return &planv1.Question{
		Id: store.NewID(), WishId: t.GetWishId(), CreateTime: timestamppb.Now(), Icon: "🔓",
		Text: fmt.Sprintf("May the worker of task %s change the files of %s? It reads only until you answer: %s is outside Git, "+
			"and has no agent configuration (no %s, AGENTS.md nor configuration file of its agent).",
			t.GetCode(), project.GetName(), project.GetDirectory(), filepath.ToSlash(PermissionsFile)),
		Options: []string{
			"Yes: it starts again, allowed to edit the project's files, and to run no command",
			"No: it only reads",
		},
	}
}

// pickProject is the project a task of wish works in: id, or the wish's only project; none for a wish without
// any project.
func pickProject(ctx context.Context, tx store.Reader, wish *planv1.Wish, id string) (*planv1.Project, error) {
	ids := wish.GetProjectIds()
	if id == "" {
		if len(ids) == 0 {
			return nil, nil
		}
		if len(ids) != 1 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"the wish has %d projects: name one with --project-id", len(ids)))
		}
		id = ids[0]
	}
	project, err := store.Get[*planv1.Project](ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 && !slices.ContainsFunc(ids, func(s string) bool { return s == project.GetId() }) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"project %s is not one of the wish's projects", project.GetName()))
	}
	return project, nil
}

// nextCode is the code of a new task of the wish: W1, W2…, after the highest one. Tasks are never deleted, so a
// code is never given twice; the unique index guards it anyway.
func nextCode(ctx context.Context, tx *store.Tx, wishID string) (string, error) {
	tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wishID})
	if err != nil {
		return "", err
	}
	last := 0
	for _, t := range tasks {
		var n int
		if _, err := fmt.Sscanf(t.GetCode(), "W%d", &n); err == nil && n > last {
			last = n
		}
	}
	return fmt.Sprintf("W%d", last+1), nil
}

// pump records the worker's events until the task ends for good: a worker stopped to start again, allowed to
// edit, is followed by the next one.
func (h *Harness) pump(r *run) {
	for !h.settle(r, h.drain(r)) {
	}
}

// drain records the worker's events, and applies the answers to the task's edit question, until the worker ends.
func (h *Harness) drain(r *run) Result {
	events := r.worker.Events()
	var measuring <-chan time.Time
	m := h.newMeter(r)
	if m != nil {
		tick := time.NewTicker(h.measureEvery)
		defer tick.Stop()
		measuring = tick.C
	}
	for {
		select {
		case <-measuring:
			h.measure(r, m)
		case ev, ok := <-events:
			if !ok {
				r.unread = nil // A worker started again never had them.
				return r.worker.Wait()
			}
			h.record(r, ev)
		case ev := <-r.notes:
			h.write(r, actorHarness, methodEvent, nil, ev)
		case m := <-r.sends:
			m.reply <- h.deliver(r, m)
		case written := <-r.holds:
			h.writeHold(r)
			close(written)
		case <-r.wake:
			for _, q := range h.takeAnswers(r) {
				if h.applyAnswer(r, q) && !r.restart {
					r.restart = true
					r.worker.Stop()
				}
			}
		}
	}
}

// record writes an event of the worker, with the task when the event changes it.
func (h *Harness) record(r *run, ev Event) {
	changed := false
	if ev.SessionID != "" && ev.SessionID != r.task.GetSessionId() {
		r.task.SessionId, changed = ev.SessionID, true
	}
	if ev.Usage != nil {
		r.task.Usage, changed = sum(r.base, ev.Usage), true
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
		r.failure = ev.Text
	}
	if ev.Limit != nil {
		r.limit = ev.Limit
	}
	if w := ev.Watched; w != nil && w.First != "" {
		r.task.LastLine, changed = clipRunes(w.First, watchLineMax), true
	}
	var task proto.Message
	if changed {
		task = r.task
	}
	h.acknowledge(r, ev)
	h.write(r, actorWorker, methodEvent, task, ev)
	if ev.Watched != nil {
		h.wakeLead(r, ev.Watched, ev.Text)
	}
}

// takeAnswers takes the answers waiting for the run.
func (h *Harness) takeAnswers(r *run) []*planv1.Question {
	h.mu.Lock()
	defer h.mu.Unlock()
	answers := r.answers
	r.answers = nil
	return answers
}

// settle follows the end of a worker: it starts the task's worker again when a yes to its edit question asked for
// it, and says false; otherwise it gives the task its final status, lets its watchers go, and says true. No answer
// reaches the run once its status is final: the answer waits until the run is gone.
func (h *Harness) settle(r *run, res Result) bool {
	for {
		h.mu.Lock()
		answers := r.answers
		r.answers = nil
		if len(answers) > 0 {
			h.mu.Unlock()
			for _, q := range answers {
				if h.applyAnswer(r, q) {
					r.restart = true
				}
			}
			continue
		}
		restart := r.restart && !r.stopping && !r.shelved && h.ctx.Err() == nil
		r.restart, r.paused = false, false // A worker started again starts unpaused.
		if !restart {
			r.final = true
		}
		h.mu.Unlock()
		if !restart {
			break
		}
		err := h.again(r)
		if err == nil {
			return false
		}
		res = Result{ExitCode: -1, Err: err}
	}
	h.end(r, res)
	return true
}

// finish settles a run whose worker did not start, or that waits without a worker; the pump follows the worker
// that a yes to the edit question started meanwhile.
func (h *Harness) finish(r *run, res Result) {
	if !h.settle(r, res) {
		go h.pump(r)
	}
}

// end gives the task its final status, then lets its watchers go. A task whose worker read while it asks whether
// it may edit waits for the answer. A task is done only when its worker ended without an error, whatever its
// provider: an error it reported, an exit code other than 0, or an error it said on the way, even when its
// process then exited 0, fails the task with the reason. But a failure its provider's usage limit caused is no
// failure: the task waits for the limit to reset, and resumes.
func (h *Harness) end(r *run, res Result) {
	for flushed := false; !flushed; {
		select {
		case ev := <-r.notes:
			h.write(r, actorHarness, methodEvent, nil, ev)
		default:
			flushed = true
		}
	}
	h.mu.Lock()
	stopping, shelved := r.stopping, r.shelved
	h.mu.Unlock()
	t := r.task
	t.EndTime, t.ExitCode = timestamppb.Now(), int32(res.ExitCode)
	switch {
	case stopping:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_STOPPED, "stopped on request"
	case shelved:
		shelve(t)
	case h.ctx.Err() != nil:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "djinn up stopped while the worker ran"
	case res.Err != nil:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, res.Err.Error()
	case res.ExitCode != 0:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, fmt.Sprintf("exit code %d", res.ExitCode)
	case r.failure != "":
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, r.failure
	case t.GetAccess() == planv1.TaskAccess_TASK_ACCESS_ASKING:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_WAITING, "waiting for the answer to its edit question"
	default:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_DONE, ""
	}
	integrate := h.pendIntegration(t)
	why := t.GetError()
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED {
		h.limited(r, t)
	}
	text := short(t.GetStatus())
	switch {
	case shelved:
		text = "resuming: " + whyWishPaused
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING:
		text = "waiting for the limit: " + t.GetWaitReason() + " (" + why + "); Djinn resumes it then"
	case t.GetError() != "":
		text += ": " + t.GetError()
	}
	h.write(r, actorHarness, methodEnd, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	if integrate {
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "integration: pending, waiting for its batch"})
		h.kickIntegrate()
	}
	// The links to the summoned skills go with the worker; a worker started again makes them anew.
	_ = os.RemoveAll(skillsDir(h.home, r.id))
	h.forget(r)
}

// forget removes the run once its task is written for good, and ends its watchers' live feed.
func (h *Harness) forget(r *run) {
	h.mu.Lock()
	delete(h.runs, r.id)
	for ch := range r.subs {
		close(ch)
	}
	r.subs = nil
	h.notifyLocked()
	h.mu.Unlock()
	close(r.done)
	h.wg.Done()
	h.wake() // A slot, a scope or a dependency may have freed.
}

// write records an event of the run, and the task when it is not nil, in one transaction journaled under
// actor and method, then hands the event to the watchers. The store is written even while Djinn stops.
func (h *Harness) write(r *run, actor, method string, task proto.Message, ev Event) {
	var req proto.Message
	if task != nil && method != methodEvent {
		req = task
	}
	if _, err := h.writeAs(r, actor, method, req, task, ev); err != nil {
		// The worker goes on: losing one event is better than losing the worker.
		log.Printf("djinn: task %s: record an event: %v", r.id, err)
	}
}

// writeAs is write, journaling req; nil journals the event.
func (h *Harness) writeAs(r *run, actor, method string, req, task proto.Message, ev Event) (*planv1.TaskEvent, error) {
	te := newEvent(r.id, r.seq+1, ev)
	if req == nil {
		req = te
	}
	err := h.store.Tx(context.Background(), func(tx *store.Tx) error {
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
		return nil, err
	}
	r.seq++
	h.publish(r, te)
	return te, nil
}

// newEvent builds a stored event, its text and raw line cut to maxText.
func newEvent(taskID string, seq int64, ev Event) *planv1.TaskEvent {
	return &planv1.TaskEvent{
		Id: store.NewID(), TaskId: taskID, Seq: seq, Kind: ev.Kind, Text: clip(ev.Text), Usage: ev.Usage,
		Raw: clip(ev.Raw), CreateTime: timestamppb.Now(),
	}
}

func clip(s string) string {
	if len(s) <= maxText {
		return s
	}
	return s[:maxText] + fmt.Sprintf("… (%d bytes cut)", len(s)-maxText)
}

// sum adds what a run spent to what the task had spent before it.
func sum(base, run *planv1.Usage) *planv1.Usage {
	return &planv1.Usage{
		InputTokens:      base.GetInputTokens() + run.GetInputTokens(),
		OutputTokens:     base.GetOutputTokens() + run.GetOutputTokens(),
		CacheReadTokens:  base.GetCacheReadTokens() + run.GetCacheReadTokens(),
		CacheWriteTokens: base.GetCacheWriteTokens() + run.GetCacheWriteTokens(),
		CostUsd:          base.GetCostUsd() + run.GetCostUsd(),
	}
}

// lastSeq is the position of the task's last event.
func lastSeq(ctx context.Context, r store.Reader, taskID string) (int64, error) {
	events, err := store.List[*planv1.TaskEvent](ctx, r, store.Where{"task_id": taskID})
	if err != nil {
		return 0, err
	}
	var last int64
	for _, e := range events {
		last = max(last, e.GetSeq())
	}
	return last, nil
}

// Stop asks the task's worker to stop, and waits until it has, or ctx ends.
func (h *Harness) Stop(ctx context.Context, procedure string, req *planv1.TaskServiceStopRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	r := h.runs[id]
	if r == nil {
		h.mu.Unlock()
		if planned(task) {
			return h.stopPlanned(ctx, procedure, req)
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is not running: %s", task.GetCode(), short(task.GetStatus())))
	}
	r.stopping = true
	if r.worker != nil {
		r.worker.Stop()
	}
	h.mu.Unlock()
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return tx.Journal(actorLocal, procedure, req) }); err != nil {
		return nil, plan.Status(err)
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	task, err = store.Get[*planv1.Task](ctx, h.store, id)
	return task, plan.Status(err)
}

// stopPlanned stops a planned task before it starts. When the scheduler started it meanwhile, its worker stops.
func (h *Harness) stopPlanned(ctx context.Context, procedure string, req *planv1.TaskServiceStopRequest) (*planv1.Task, error) {
	h.sched.Lock()
	task, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		h.sched.Unlock()
		return nil, plan.Status(err)
	}
	if !planned(task) {
		h.sched.Unlock()
		return h.Stop(ctx, procedure, req)
	}
	why := "stopped on request before it started"
	if dispatch.Resuming(task) {
		why = "stopped on request while it waited to resume"
	}
	task.Status, task.Error, task.WaitReason, task.ResumeAfter, task.EndTime = planv1.TaskStatus_TASK_STATUS_STOPPED, why, "", nil, timestamppb.Now()
	h.writeAlone(ctx, actorLocal, procedure, req, task.GetId(), task, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "stopped: " + task.GetError()})
	h.sched.Unlock()
	h.wake() // Its dependents fail in turn.
	return store.Get[*planv1.Task](ctx, h.store, task.GetId())
}

// Pause holds the task's worker where it is, without killing it, until Resume: the task is paused, and its slot
// of the machine is free meanwhile. A worker whose provider cannot pause, or Windows, refuses.
// A worker that holds a gate is not paused: the gate would stay held, frozen, for every other worker. Nor is one
// that waits for a gate: it would get the gate in its turn, and hold it frozen.
func (h *Harness) Pause(ctx context.Context, procedure string, req *planv1.TaskServicePauseRequest) (*planv1.Task, error) {
	return h.hold(ctx, procedure, req, req.GetTaskId(), true)
}

// Resume lets a paused worker go on. It does not wait for a slot: the developer asked for it.
func (h *Harness) Resume(ctx context.Context, procedure string, req *planv1.TaskServiceResumeRequest) (*planv1.Task, error) {
	return h.hold(ctx, procedure, req, req.GetTaskId(), false)
}

// hold pauses or resumes the task's worker, then waits until its pump has written the task so, or the task ended.
func (h *Harness) hold(ctx context.Context, procedure string, req proto.Message, id string, pause bool) (*planv1.Task, error) {
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	r := h.runs[id]
	if r == nil || r.worker == nil || r.stopping || r.final {
		h.mu.Unlock()
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is not running: %s", task.GetCode(), short(task.GetStatus())))
	}
	if pause {
		for _, g := range []struct {
			names func(taskID string) []string
			verb  string
		}{{h.held, "holds"}, {h.waiting, "waits for"}} {
			if g.names == nil {
				continue
			}
			if names := g.names(id); len(names) > 0 {
				h.mu.Unlock()
				gate := "gate"
				if len(names) > 1 {
					gate = "gates"
				}
				return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s %s the %s %s: wait or stop it",
					task.GetCode(), g.verb, gate, strings.Join(names, ", ")))
			}
		}
	}
	if r.paused == pause {
		h.mu.Unlock()
		if pause {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is paused already", task.GetCode()))
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is not paused", task.GetCode()))
	}
	p, ok := r.worker.(Pauser)
	if !ok {
		h.mu.Unlock()
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("task %s: a %s worker cannot be paused", task.GetCode(), short(task.GetProvider())))
	}
	if pause {
		err = p.Pause()
	} else {
		err = p.Resume()
	}
	if err != nil {
		h.mu.Unlock()
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: %w", task.GetCode(), err))
	}
	r.paused = pause
	h.mu.Unlock()
	if pause {
		h.wake() // Its slot is free.
	}
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return tx.Journal(actorLocal, procedure, req) }); err != nil {
		return nil, plan.Status(err)
	}
	written := make(chan struct{})
	select {
	case r.holds <- written:
		select {
		case <-written:
		case <-r.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-r.done: // The worker ended meanwhile: the task has its final status.
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	task, err = store.Get[*planv1.Task](ctx, h.store, id)
	return task, plan.Status(err)
}

// writeHold writes the task paused or running again, as its worker is now. The pump calls it.
func (h *Harness) writeHold(r *run) {
	h.mu.Lock()
	paused := r.paused
	h.mu.Unlock()
	t := r.task
	switch {
	case paused && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING:
		t.Status = planv1.TaskStatus_TASK_STATUS_PAUSED
		h.write(r, actorHarness, methodHold, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
			Text: "paused: the worker holds still, and frees its slot"})
	case !paused && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED:
		t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
		h.write(r, actorHarness, methodHold, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "resumed"})
	}
}

// Note adds an event that comes from outside the worker, such as a gate taken or given back, to the task's
// events: through its run when its worker runs, directly otherwise.
func (h *Harness) Note(taskID string, ev Event) {
	h.mu.Lock()
	if r := h.runs[taskID]; r != nil && !r.final {
		select {
		case r.notes <- ev:
			h.mu.Unlock()
			return
		default: // The pump is far behind: the event goes alone.
		}
	}
	h.mu.Unlock()
	h.writeAlone(context.Background(), actorHarness, methodEvent, nil, taskID, nil, ev)
}

// Watch sends the task's events after the position after, as they come, until the task has ended and all its
// events are sent, or ctx ends.
func (h *Harness) Watch(ctx context.Context, taskID string, after int64, send func(*planv1.TaskEvent) error) error {
	if _, err := store.Get[*planv1.Task](ctx, h.store, taskID); err != nil {
		return plan.Status(err)
	}
	emit := func(ev *planv1.TaskEvent) error {
		if ev.GetSeq() <= after {
			return nil
		}
		after = ev.GetSeq()
		return send(ev)
	}
	for {
		// Subscribe, then read what is stored: an event written in between comes both ways, and only once out.
		changed := h.generation()
		ch := h.subscribe(taskID)
		events, err := store.List[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": taskID})
		if err != nil {
			h.unsubscribe(taskID, ch)
			return plan.Status(err)
		}
		slices.SortFunc(events, func(a, b *planv1.TaskEvent) int { return cmp.Compare(a.GetSeq(), b.GetSeq()) })
		for _, ev := range events {
			if err := emit(ev); err != nil {
				h.unsubscribe(taskID, ch)
				return err
			}
		}
		if ch == nil {
			task, err := store.Get[*planv1.Task](ctx, h.store, taskID)
			if s := task.GetStatus(); err == nil && (s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_PAUSED) {
				// Its worker started since it was subscribed to: its run is there, follow it.
				select {
				case <-changed:
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					return nil
				}
				continue
			}
			if err != nil || !planned(task) {
				if ctx.Err() != nil {
					return nil
				}
				return plan.Status(err) // The task has ended: the store holds all its events.
			}
			// A planned task: follow it until its worker starts, or it ends without one.
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return nil
			}
		}
	live:
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					break live // The task ended, or this watcher fell behind: read the store again.
				}
				if err := emit(ev); err != nil {
					h.unsubscribe(taskID, ch)
					return err
				}
			case <-ctx.Done():
				h.unsubscribe(taskID, ch)
				return nil
			}
		}
	}
}

// subscribe returns a channel of the task's next events, or nil when no worker runs for it.
func (h *Harness) subscribe(taskID string) chan *planv1.TaskEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.runs[taskID]
	if r == nil {
		return nil
	}
	ch := make(chan *planv1.TaskEvent, 256)
	r.subs[ch] = struct{}{}
	return ch
}

func (h *Harness) unsubscribe(taskID string, ch chan *planv1.TaskEvent) {
	if ch == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.runs[taskID]; r != nil {
		if _, ok := r.subs[ch]; ok {
			delete(r.subs, ch)
			close(ch)
		}
	}
}

// publish hands an event to the run's watchers. A watcher too slow to keep up is let go: it reads the store again.
func (h *Harness) publish(r *run, ev *planv1.TaskEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
			delete(r.subs, ch)
			close(ch)
		}
	}
}

// Clean removes the worktree of a task that has ended. Its branch stays.
func (h *Harness) Clean(ctx context.Context, procedure string, req *planv1.TaskServiceCleanRequest) (*planv1.Task, error) {
	id := req.GetTaskId()
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	h.mu.Lock()
	running := h.runs[id] != nil
	h.mu.Unlock()
	switch {
	case running:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is running: stop it first", task.GetCode()))
	case task.GetWorktree() == "":
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s has no worktree", task.GetCode()))
	}
	project, err := store.Get[*planv1.Project](ctx, h.store, task.GetProjectId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if err := removeWorktree(ctx, project.GetDirectory(), task.GetWorktree(), req.GetForce()); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: %w", task.GetCode(), err))
	}
	text := "worktree removed"
	if task.GetBranch() != "" {
		text += ", branch " + task.GetBranch() + " kept"
	}
	task.Worktree = ""
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, procedure, req); err != nil {
			return err
		}
		seq, err := lastSeq(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return tx.Put(newEvent(id, seq+1, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	return task, nil
}

// short is an enum value as the command line writes it: TASK_STATUS_DONE is done, PROVIDER_FAKE is fake.
func short(e fmt.Stringer) string {
	s := strings.TrimPrefix(strings.TrimPrefix(e.String(), "TASK_STATUS_"), "PROVIDER_")
	return strings.ToLower(s)
}
