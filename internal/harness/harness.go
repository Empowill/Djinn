package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
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
	methodLaunch   = "harness/launch"   // a question's planned task was launched; the request is the question
	methodReceived = "harness/received" // the worker took a message in; the request is the event
	methodHold     = "harness/hold"     // the task's worker was paused or resumed; the request is the task
	methodMeasure  = "harness/measure"  // what the task's worker uses was read; the request is the reading
	methodAzima    = "harness/azima"    // djinn up found an azima stored as work, from before kinds; the request is the task
	methodUngroup  = "harness/ungroup"  // djinn up found a task part of an azima that is gone; the request is the task, part of none
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
	cuts   stopCuts       // what a stop cuts, for the next start (cut.go)

	answering        sync.Mutex    // one answer to an edit question at a time
	launching        sync.Mutex    // one launch of a question's planned task at a time
	questioning      sync.Mutex    // one question worker decided at a time (question.go)
	enlightenWorkers *bool         // enlighten workers start (WithEnlightenWorkers); nil: follows project settings
	enlightenWait    time.Duration // how long an investigator may take to start (WithEnlightenWait); 0: the default

	scopes *machine.Scopes // the systemd scopes the workers run in, one each (WithScopes); nil: none
	// children records the processes Djinn starts, so that none outlives it (orphans.go); nil without a data folder.
	children *children

	beforeStart func() // runs before a worker's process starts (WithBeforeStart); nil: nothing

	clock func() time.Time // nil: the machine's (WithClock)

	measureEvery time.Duration    // how often a worker is read (WithMeasure); 0: never
	measureRead  MeasureFunc      // nil: workers are not measured
	history      *machine.History // nil: the readings are kept in memory only (WithHistory)

	tell       TellFunc      // wakes the lead of a watcher's wish (TellLeads); guarded by mu
	notifyLead NotifyFunc    // queues an event for the lead (NotifyLead), in place of tell; guarded by mu
	watched    WatchedFunc   // reads a watcher's new paragraph (OnWatched); guarded by mu
	tellDelay  time.Duration // delay before worker lines are flushed to the lead (WithTellDelay)
	batcher    *tellBatcher  // batches worker lines to the lead (tell.go)

	// The scheduler (schedule.go).
	capacity   Capacity                    // nil: no limit
	barSlots   func(djinnv1.LoadNotch) int // the workers a notch itself lets run (WithBarSlots); nil: the capacity's slots
	forcing    string                      // the task Start launches now, by hand; guarded by mu
	available  func() uint64               // the memory available; nil: the memory holds no worker back
	total      func() uint64               // the machine's total memory; nil: total RAM is unknown
	policy     atomic.Pointer[machine.Policy]
	tick       time.Duration // a pass at least this often
	sched      sync.Mutex    // one scheduling decision at a time: a spawn, a pass, a planned task stopped
	kick       chan struct{} // wakes the scheduler
	scheduling sync.Once
	loopDone   chan struct{} // closed when the scheduler has stopped; nil until Schedule
	sleepers   atomic.Bool   // a question may sleep: the scheduler's next pass reads them (wakeQuestions)

	// The integration of finished work (integrate.go).
	gates            TakeGate      // nil: commands run without a gate
	commands         RunCommand    // nil: the processes the commands name
	integrateTick    time.Duration // a pass at least this often; 0: a minute
	integrateKick    chan struct{} // wakes the integration
	configKick       chan struct{} // wakes the watch of the configuration (WatchConfig)
	configTick       time.Duration // a look at least this often; 0: plan.ConfigLookEvery
	configWatching   sync.Once
	integrating      sync.Once
	integrateDone    chan struct{}         // closed when the integration has stopped; nil until Integrate
	tested           map[string]tested     // by wish/project; owned by the integration's pass
	integrateMu      sync.Mutex            // held while a batch is integrated, main merged, or a push decided after its checks (push_apart.go)
	installMu        sync.Mutex            // held while a build is installed
	installing       string                // the commit installed now, guarded by mu
	installed        map[string]string     // by projectID: sha of last installed build, guarded by mu
	uninstalledDelay time.Duration         // how long a build waits uninstalled before asking; 0: defaultUninstalledDelay
	pushMu           sync.Mutex            // held while an integration branch is pushed: its push checks use the push worktree
	pushJobsMu       sync.Mutex            // guards pushJobs
	pushJobs         map[string]*pushJob   // by pushKey: the pushes run apart from the pass (push_apart.go)
	pushingMu        sync.Mutex            // guards pushing
	pushing          string                // the push that runs now (pushKey), "" between pushes
	built            func(Built)           // nil: no build is proposed
	mainTested       map[string]mainTested // by mainLine.key; owned by the integration's pass
	pushWaits        map[string]bool       // by wish/project/branch: a push due waits for merges, said (repair.go); owned by the integration's pass
	mainNow          atomic.Bool           // a release was found: the next pass looks at main whatever the cadence
	toldApart        map[string]bool       // by wish and what was told the lead of a branch apart from its remote's (remote_branch.go), guarded by mu
	referenceFetched map[string]time.Time  // by repository, when its reference branch was last fetched (fetchReference), guarded by mu
	mergedLooks      map[string]time.Time  // by repository, when Djinn last fetched main to find azimas merged into it (merged.go), guarded by mu

	// The workers' health (health.go), judged by the scheduler's pass: guarded by sched.
	accountOf AccountFunc                     // nil: no agent's account is read
	accounts  map[planv1.Provider]accountSeen // each agent's account, as last read

	// The network, as Djinn knows it (network.go).
	net network

	// The disks' free space, as Djinn knows it (disk.go).
	disk disk

	// Babysitting what tasks open (babysit.go).
	babysitting sync.WaitGroup // one per look for what a task opened; Close waits for them
	babysitMu   sync.Mutex     // one watcher decided at a time

	// mu guards the runs and the fields marked "guarded by mu". Never send on a channel, run a command or call out (a func
	// given by an option: gates, tell, watched) while holding it: a callee that calls the harness back locks it again
	// (W549's freeze). It logs a holder that keeps it past logx.HeldLong (New).
	mu           logx.Mutex
	closed       bool
	held         func(taskID string) []string // the gates a task holds (HeldGates); nil: none known
	waiting      func(taskID string) []string // the gates a task waits for (HeldGates); nil: none known
	outside      func() (int, int)            // the gates held outside the running workers, and of them the merges' and pushes' (GatesOutside); nil: none
	silenceLimit time.Duration                // silence limit before the Lamplighter checks a worker (lamplighter.go)
	maxWallTime  time.Duration                // ceiling on wall time budget per task (lamplighter.go)
	ifritIdle    time.Duration                // how long the developer may leave Djinn alone before an ifrit goes out (ifrit.go)
	presence     *Presence                    // when the developer last did something in Djinn (presence.go)
	ifritWarned  func(*planv1.Task)           // shows that an ifrit goes out soon (WarnIfrits), guarded by mu; nil: nothing shows
	maxTurns     int                          // ceiling on model turns budget per task (lamplighter.go)
	version      string                       // version of the running binary (WithVersion)
	builtFrom    map[string]string            // by project id: the branch the running binary was built from (compare.go)
	runs         map[string]*run              // by task id
	changed      chan struct{}                // closed at the next change of a task without worker (notifyLocked)
	preempted    map[string]string            // investigator or merge task ID -> paused work task ID, guarded by mu
	rateHits     map[string]int               // task ID -> consecutive per-minute rate limit hits, guarded by mu
	goos         func() string                // runtime.GOOS; nil: runtime.GOOS
	aliasesMu    sync.RWMutex
	aliases      map[string]string // alias -> model ID

	// What the workers use (measure.go), apart from mu: a pump measures its worker, and the machine reads the memory
	// they hold (Resident), without waiting for a pass or a spawn that holds mu. Taken after mu, never before it.
	useMu    sync.Mutex
	resident map[string]uint64 // by task ID: the resident memory of a worker, as last read, watchers left out

	// The serves the ifrits run (serve.go), apart from mu: a serve's start and stop take seconds. Never taken with mu
	// held, nor mu with it.
	servesMu sync.Mutex
	serving  map[string]*ifritServes // by task ID
}

// run is a task at work: its worker, or the workers it runs one after the other when the task starts again.
type run struct {
	id     string
	wish   string             // the task's wish, which never changes
	done   chan struct{}      // closed once the task has its final status
	wake   chan struct{}      // an answer waits in answers
	notes  chan Event         // events from outside the worker (a gate), for the pump to write
	sends  chan *message      // messages for the worker (inbox.go), for the pump to deliver
	timers chan struct{}      // an ifrit's timers to look at (ifritTimers), for the pump
	holds  chan chan struct{} // a pause or a resume for the pump to write; closed once written

	// Guarded by Harness.mu.
	worker             Worker
	code               string // the task's code, once its worker starts (TrackGate)
	pid                int    // process ID of the running worker (0 if none)
	cgroup             string // cgroup of the running worker
	token              string // secret per-run token passed in DJINN_WORKER_TOKEN
	stopping           bool
	stoppingReason     string               // why worker was stopped if not stopped on request (e.g. budget exceeded)
	shelved            bool                 // its wish was paused: the worker stops, the task resumes with the wish (shelve.go)
	paused             bool                 // the worker holds still: it takes no slot
	notchPaused        bool                 // paused by the operating load notch
	slotPaused         bool                 // paused to give its slot to an investigator: resumed first when a slot frees
	netPaused          bool                 // paused while the network is down: resumed once it is back (network.go)
	inTool             bool                 // a tool call of the worker runs: the network does not pause it (network.go)
	forced             bool                 // started or resumed by hand, whatever the slots (Harness.Forced)
	holdWhy            string               // why the worker holds still (preemption or pause reason)
	preemptedWhy       string               // if set, stopping to make room for an investigator
	watcher            bool                 // a watcher runs a command, and takes no slot
	light              bool                 // a question worker reads and runs djinn's commands: it takes no slot
	ifrit              bool                 // an ifrit: its agent's input stays open between turns (ifrit.go)
	listenSince        time.Time            // when the ifrit began to listen; zero while it works: it takes no slot meanwhile
	listened           time.Duration        // how long the ifrit listened before listenSince, over all its runs
	out                string               // why the ifrit goes out (ifritTimers): its input closes, and it ends out
	outAt              time.Time            // when the ifrit goes out unless the developer comes back (ifritTimers)
	ending             bool                 // djinn task done closed the ifrit's input (doneIfrit): it ends done, never out
	final              bool                 // the task is getting its final status: an answer waits for done instead
	answers            []*planv1.Question   // answers to the task's edit question, for the pump to apply
	shown              *planv1.Task         // a copy of task as last written, replaced, never changed: what the scheduler reads of it (W182)
	use                *machinev1.WorkerUse // what its worker uses, at the last reading (measure.go); nil before one; guarded by Harness.useMu
	samples            *workerRing          // recent measurement samples (measure.go); guarded by Harness.useMu
	resources          *planv1.Resources    // the task's resources as its pump last set them, for the readers apart from the pump; guarded by Harness.useMu
	subs               map[chan *planv1.TaskEvent]struct{}
	startTime          time.Time            // when the current run started
	turns              int                  // model turns of the current worker (lamplighter.go), kept on the task by setTurns
	lastActivity       time.Time            // when the worker last produced an event (lamplighter.go)
	lastKind           planv1.TaskEventKind // the kind of that event: after its turn's result, a worker may wait for its background work (health.go)
	lastLamplighterCPU time.Duration        // CPU time used at the Lamplighter's previous check
	resultAt           time.Time            // when the worker's turn ended with its result, said nothing of a new turn since; zero otherwise (silent.go)
	resultNote         bool                 // that turn left a final note
	silentWhy          string               // why Djinn stops the worker, silent after its result (silent.go)
	silentNote         bool                 // it had written its final note: it ends done
	lamplighterEnd     bool                 // the Lamplighter ended this worker
	lamplighterReason  string               // why the Lamplighter ended it ("silent for N min")
	wakePrompt         string               // what the woken worker is told, when not to continue (continueStalled)
	relaunched         bool                 // the lead or the developer relaunched it, stalled (continueStalled): woken whatever its wakes
	publishAt          time.Time            // when the Lamplighter asked the investigation to publish in this run (investigation.go)
	publishWhy         string               // what it reached then: its time limit or its budget
	spent              float64              // what the task spent so far, its run's estimate (Event.Spent) included

	// Owned by whoever writes the task: Spawn, then the pump.
	task           *planv1.Task
	base           *planv1.Usage   // baseline added to a cumulative session result (prior sessions, minus any fork parent)
	spentBase      float64         // what the task had spent before the current run's mid-turn estimate (Event.Spent)
	turnsBefore    int             // the task's model turns before this worker
	restart        bool            // the worker stops to start again, allowed to edit
	unread         []string        // messages the worker took on its input and has said nothing after yet
	unreadWaitTurn bool            // messages in unread were delivered while an agy turn was under way: acknowledged once the next turn speaks
	tools          int             // tool calls of the current worker without a result yet: it is mid-turn (take)
	held           *message        // a message for the end of the worker's turn, written once tools is 0 (turnOver)
	branch         string          // the branch template of the project's settings, for launch; empty: the default
	from           string          // the wish's integration branch in the project, which its worktree starts from; empty: HEAD
	checks         string          // what the project's settings say of the checks Djinn runs, for the worker's first prompt
	pushChecks     []string        // commands of the project's push checks, to note when a worker runs them
	testOne        string          // project's targeted test command template or prefix
	notedPush      bool            // already noted that worker ran push checks
	fallback       string          // why the worker fell back to another provider, for its start event
	failure        string          // the last error the current worker said: it never ends done
	limit          *Limit          // the usage limit the current worker said it hit: it wins over failure
	refused        string          // the last call its permissions refused, as the worker said it (question.go)
	refusals       *denials        // the calls its permissions refused in this run (refusals.go)
	envReplayed    bool            // a tool call/result already triggered replay of env failures
	startHead      string          // HEAD commit when the worker started
	answered       bool            // worker gave its final answer (text after tool calls)
	closingFailed  bool            // closing call failed after final answer and committed work
	requestedModel string          // model as requested before stream events
	nextProvider   planv1.Provider // provider queued by UpdateModels to take effect when this run ends
	nextModel      string          // model queued by UpdateModels to take effect when this run ends
	rebase         *behind         // the rebase its worker was asked on resuming, until its branch took it (uptodate.go)
}

// newRun is the run of task, registered so that a watcher never misses its first events.
// It fails when Djinn is stopping, or when the task already runs.
func (h *Harness) newRun(task *planv1.Task) (*run, error) {
	r := &run{
		id: task.GetId(), wish: task.GetWishId(), task: task, shown: proto.CloneOf(task), base: task.GetUsage(), done: make(chan struct{}), wake: make(chan struct{}, 1),
		notes: make(chan Event, 64), sends: make(chan *message), holds: make(chan chan struct{}), timers: make(chan struct{}, 1),
		subs: map[chan *planv1.TaskEvent]struct{}{}, watcher: watching(task),
		light: questionWorker(task), requestedModel: task.GetModel(),
		token:        store.NewID(),
		lastActivity: h.now(),
	}
	if pt := task.GetInvestigation().GetPublishTime(); pt != nil && pt.IsValid() {
		r.publishAt = pt.AsTime()
		r.publishWhy = task.GetInvestigation().GetPublishWhy()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("djinn is stopping"))
	case h.runs[r.id] != nil:
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s is running", task.GetCode()))
	}
	r.ifrit = dispatch.Ifrit(task)
	// Started by hand (Start), an ifrit or an urgent task (dispatch.Urgent): beyond the slots from its first instant.
	r.forced = h.forcing == r.id || r.ifrit || dispatch.Urgent(task)
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
		tick: 2 * time.Second, kick: make(chan struct{}, 1), changed: make(chan struct{}),
		integrateKick: make(chan struct{}, 1), configKick: make(chan struct{}, 1), tested: map[string]tested{}, mainTested: map[string]mainTested{}, pushWaits: map[string]bool{}, toldApart: map[string]bool{}, referenceFetched: map[string]time.Time{}, mergedLooks: map[string]time.Time{},
		pushJobs:  map[string]*pushJob{},
		installed: map[string]string{}, preempted: map[string]string{}, rateHits: map[string]int{}, aliases: map[string]string{},
		resident: map[string]uint64{},
	}
	h.ctx = gitx.OnCut(ctx, h.cutGit) // The gits a stop cuts are told to the next start (cut.go).
	def := machine.DefaultPolicy()
	h.policy.Store(&def)
	for _, o := range opts {
		o(h)
	}
	h.presence = &Presence{now: h.now, touched: h.pokeIfrits}
	h.batcher = newTellBatcher(h)
	dumps := ""
	if home != "" {
		dumps = logx.Folder(home)
	}
	go h.mu.Watch(ctx, "harness", dumps)
	if home != "" && h.children == nil {
		h.children = newChildren(filepath.Join(home, runDir), Grace)
	}
	return h
}

// defaultUninstalledDelay is how long a build waits uninstalled before Djinn asks to install and restart.
const defaultUninstalledDelay = 5 * time.Minute

// WithUninstalledDelay sets how long an integrated build may stay uninstalled before Djinn asks to install it.
func WithUninstalledDelay(d time.Duration) Option {
	return func(h *Harness) { h.uninstalledDelay = d }
}

// WithVersion sets the version of the running binary.
func WithVersion(v string) Option {
	return func(h *Harness) { h.version = v }
}

// lowPriority reports whether workers should run with low CPU priority under the current policy.
// The caller holds h.sched.
func (h *Harness) lowPriority() bool {
	return h.Policy().LowPriority()
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
	h.waitPushes() // The pushes the integration started apart stop with it.
	if h.batcher != nil {
		h.batcher.Close()
	}
	h.wg.Wait()
	h.babysitting.Wait()
	h.net.wg.Wait()
	h.disk.wg.Wait()
	h.children.close() // What the workers left running stops with them.
	h.writeCuts()
}

// Recover marks as interrupted the tasks a previous djinn up left running: their workers died with it. They keep
// what resuming them needs (provider, session, worktree). Then every interrupted task Djinn may resume waits for the
// scheduler to resume it (queueInterrupted). The lead's restart event names the workers the last djinn up left
// running, and those it stopped as it ended, interrupted since it started (lastStart). First, the azimas stored as
// work before tasks had kinds become azimas (migrateAzimas), and a task part of an azima that is gone becomes part of
// none (ungroupOrphans). Before all, the processes the last djinn up started and left running are stopped
// (children.sweep), so that no task resumes beside its old worker: the restart event names them; an ifrit's serves
// they ran are stopped, their stop commands run (recoverServes).
func (h *Harness) Recover(ctx context.Context) error {
	orphans := h.children.sweep()
	locked := h.unlockWorktrees(ctx) // No git of the last djinn up runs now, and none of this one yet.
	cut := readCuts(h.home)
	h.sweepTemp(ctx)
	h.cleanWarmLeftovers(ctx)
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return err
	}
	if err := h.recoverServes(ctx, tasks); err != nil {
		return err
	}
	if err := h.rebuildTaskRunsFromJournal(ctx, tasks); err != nil {
		return err
	}
	if err := h.migrateAzimas(ctx, tasks); err != nil {
		return err
	}
	if err := h.ungroupOrphans(ctx, tasks); err != nil {
		return err
	}
	integrating := h.recoverIntegrations(ctx, tasks)
	for _, t := range tasks {
		if t.GetModel() != "" && !isModelAlias(t.GetModel()) {
			h.learnAliases(t.GetModel())
		}
		for _, m := range t.GetModels() {
			if m != "" && !isModelAlias(m) {
				h.learnAliases(m)
			}
		}
	}
	lastStart, err := h.lastStart(ctx)
	if err != nil {
		return err
	}
	var interrupted, out []string
	for _, t := range tasks {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
		case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			if dispatch.Ifrit(t) { // Lit as the last djinn up ended: it comes back out, never resumed by itself.
				if err := h.putOut(ctx, t); err != nil {
					return err
				}
				out = append(out, t.GetCode())
				continue
			}
			// Stopped as the last djinn up ended (end): its worker ran, resumed by Djinn or not.
			if !lastStart.IsZero() && t.GetEndTime().AsTime().After(lastStart) && !watching(t) {
				interrupted = append(interrupted, t.GetCode())
			}
			continue
		default:
			continue
		}
		// A task no worker ever started (planned, or imported with a wish) waits for one: nothing to interrupt.
		if t.GetStartTime() == nil {
			continue
		}
		if dispatch.Ifrit(t) { // Lit as the last djinn up crashed: it comes back out.
			if err := h.putOut(ctx, t); err != nil {
				return err
			}
			out = append(out, t.GetCode())
			continue
		}
		if !watching(t) { // A watcher is no worker: the restart line counts it apart.
			interrupted = append(interrupted, t.GetCode())
		}
		nowPb := timestamppb.New(h.now())
		t.Status, t.Error, t.EndTime = planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "djinn up ended while the worker ran", nowPb
		if len(t.Runs) > 0 {
			last := t.Runs[len(t.Runs)-1]
			if last.EndTime == nil {
				last.EndTime = nowPb
				last.Reason = "interrupted"
			}
		}
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodRecover, t); err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			return putEvents(ctx, tx, h.newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "interrupted: " + t.GetError()}))
		})
		if err != nil {
			return fmt.Errorf("recover task %s: %w", t.GetCode(), err)
		}
	}
	resumed, watchers, err := h.queueInterrupted(ctx, tasks)
	if err != nil {
		return err
	}
	// The lead hears of the restart from its queue, whether its watch died with the last djinn up or not.
	restart := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART,
		Text: restartLine(h.version, interrupted, resumed, watchers, out, orphans, cutPart(cut, integrating),
			lockedPart(locked))}
	if err := plan.QueueLeadEvent(ctx, h.store, restart, h.now()); err != nil {
		return fmt.Errorf("tell the lead of the restart: %w", err)
	}
	return h.recoverAzimas(ctx, tasks)
}

// lastStart is when the last djinn up started: the time of its restart event, the latest one. Zero when none is left
// (the first start, or the lead acknowledged it long ago and it was dropped).
func (h *Harness) lastStart(ctx context.Context) (time.Time, error) {
	events, err := store.List[*planv1.LeadEvent](ctx, h.store, nil)
	if err != nil {
		return time.Time{}, err
	}
	var last time.Time
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART && e.GetCreateTime().AsTime().After(last) {
			last = e.GetCreateTime().AsTime()
		}
	}
	return last, nil
}

// restartLine is what the lead's restart event says: the version Djinn started on, the workers the last djinn up
// left running and Djinn interrupted (or that it stopped, and Djinn resumes), those of them it resumes by itself,
// apart the watchers it resumes: a watcher is no worker, the ifrits it left lit, which come back out (putOut), the
// processes the last djinn up left running that Djinn stopped, by their labels (orphansPart), and the parts more,
// each "" for none: what the stop cut (cutPart), what it left in the person's checkouts (lockedPart).
func restartLine(version string, interrupted, resumed, watchers, out, orphans []string, more ...string) string {
	if version == "" {
		version = "unknown"
	}
	line := "Djinn: restarted on version " + version
	for _, c := range resumed {
		if !slices.Contains(interrupted, c) {
			interrupted = append(interrupted, c) // Stopped with the last djinn up: interrupted all the same.
		}
	}
	var parts []string
	if len(interrupted) > 0 {
		part := fmt.Sprintf("%d %s interrupted (%s)", len(interrupted), plural(len(interrupted), "worker"),
			strings.Join(interrupted, ", "))
		var left []string
		for _, c := range interrupted {
			if !slices.Contains(resumed, c) {
				left = append(left, c)
			}
		}
		if len(resumed) > 0 {
			part += fmt.Sprintf(", resumed by Djinn: %s", strings.Join(resumed, ", "))
		}
		if len(left) > 0 {
			part += fmt.Sprintf("; not resumed, the move is yours: %s", strings.Join(left, ", "))
		}
		parts = append(parts, part)
	}
	if len(watchers) > 0 {
		parts = append(parts, fmt.Sprintf("%d %s resumed: %s", len(watchers), plural(len(watchers), "watcher"),
			strings.Join(watchers, ", ")))
	}
	switch len(out) {
	case 0:
	case 1:
		parts = append(parts, out[0]+" (ifrit) is out: a message lights it again")
	default:
		parts = append(parts, strings.Join(out, ", ")+" (ifrits) are out: a message lights each again")
	}
	if part := orphansPart(orphans); part != "" {
		parts = append(parts, part)
	}
	for _, part := range more {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return line + ": no worker was running."
	}
	return line + ": " + strings.Join(parts, "; ") + "."
}

// plural is word, with an s but for one.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// rebuildTaskRunsFromJournal reconstructs FirstStartTime and Runs on tasks that ran before runs were recorded,
// from the events Djinn already journals.
func (h *Harness) rebuildTaskRunsFromJournal(ctx context.Context, tasks []*planv1.Task) error {
	var needRebuild []*planv1.Task
	needMap := make(map[string]*planv1.Task)
	for _, t := range tasks {
		if t.GetStartTime() != nil && len(t.GetRuns()) == 0 {
			needRebuild = append(needRebuild, t)
			needMap[t.GetId()] = t
		}
	}
	if len(needRebuild) == 0 {
		return nil
	}

	cmds, err := store.CommandsOf(ctx, h.store, "harness/", nil)
	if err != nil {
		return err
	}

	taskCmds := make(map[string][]store.Command)
	for _, c := range cmds {
		switch c.Method {
		case methodStart, methodHold, methodEnd, methodRecover:
			snap := &planv1.Task{}
			if err := proto.Unmarshal(c.Request, snap); err == nil && snap.GetId() != "" {
				if _, ok := needMap[snap.GetId()]; ok {
					taskCmds[snap.GetId()] = append(taskCmds[snap.GetId()], c)
				}
			}
		}
	}

	for _, t := range needRebuild {
		cs := taskCmds[t.GetId()]
		if len(cs) == 0 {
			if t.FirstStartTime == nil {
				t.FirstStartTime = t.StartTime
			}
			t.Runs = []*planv1.TaskRun{{
				StartTime: t.StartTime,
				EndTime:   t.EndTime,
				Reason:    runReason(t, nil),
			}}
		} else {
			var runs []*planv1.TaskRun
			var firstStart *timestamppb.Timestamp
			for _, c := range cs {
				snap := &planv1.Task{}
				if err := proto.Unmarshal(c.Request, snap); err != nil {
					continue
				}
				cAt := timestamppb.New(c.At)
				switch c.Method {
				case methodStart:
					startAt := snap.GetStartTime()
					if startAt == nil {
						startAt = cAt
					}
					if firstStart == nil {
						firstStart = startAt
					}
					if len(runs) > 0 && runs[len(runs)-1].EndTime == nil {
						runs[len(runs)-1].EndTime = startAt
						runs[len(runs)-1].Reason = "restart"
					}
					runs = append(runs, &planv1.TaskRun{
						StartTime: startAt,
					})
				case methodHold:
					if snap.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED {
						if len(runs) > 0 && runs[len(runs)-1].EndTime == nil {
							endAt := cAt
							if snap.GetEndTime() != nil {
								endAt = snap.GetEndTime()
							}
							runs[len(runs)-1].EndTime = endAt
							runs[len(runs)-1].Reason = "pause"
						}
					} else if snap.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
						if len(runs) == 0 || runs[len(runs)-1].EndTime != nil {
							startAt := snap.GetStartTime()
							if startAt == nil {
								startAt = cAt
							}
							if firstStart == nil {
								firstStart = startAt
							}
							runs = append(runs, &planv1.TaskRun{
								StartTime: startAt,
							})
						}
					}
				case methodEnd, methodRecover:
					if len(runs) > 0 && runs[len(runs)-1].EndTime == nil {
						endAt := snap.GetEndTime()
						if endAt == nil {
							endAt = cAt
						}
						reason := runReason(snap, nil)
						if c.Method == methodRecover {
							reason = "interrupted"
						}
						runs[len(runs)-1].EndTime = endAt
						runs[len(runs)-1].Reason = reason
					}
				}
			}
			if firstStart == nil {
				firstStart = t.StartTime
			}
			t.FirstStartTime = firstStart
			if t.EndTime != nil && len(runs) > 0 && runs[len(runs)-1].EndTime == nil {
				runs[len(runs)-1].EndTime = t.EndTime
				runs[len(runs)-1].Reason = runReason(t, nil)
			}
			t.Runs = runs
		}
		if err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodRecover, t); err != nil {
				return err
			}
			return tx.Put(t)
		}); err != nil {
			return err
		}
	}
	return nil
}

func runReason(t *planv1.Task, r *run) string {
	if r != nil && r.preemptedWhy != "" {
		return "preemption"
	}
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		if strings.HasPrefix(t.GetWaitReason(), whyWishPaused) {
			return "pause"
		}
		if strings.Contains(strings.ToLower(t.GetWaitReason()), "preempt") {
			return "preemption"
		}
		if networkWait(t) {
			return "network"
		}
		return "limit"
	case planv1.TaskStatus_TASK_STATUS_PAUSED:
		return "pause"
	case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		return "interrupted"
	case planv1.TaskStatus_TASK_STATUS_STOPPED:
		return "stopped"
	case planv1.TaskStatus_TASK_STATUS_FAILED:
		return "failed"
	case planv1.TaskStatus_TASK_STATUS_DONE:
		return "done"
	case planv1.TaskStatus_TASK_STATUS_WAITING:
		return "waiting"
	default:
		return "done"
	}
}

// cleanWarmLeftovers removes leftover worktrees and branches (djinn-warm-…) from older versions of Djinn that
// supported warm workers: when a previous djinn up died before a task took them, their worktrees remained in Djinn's
// data folder. Kept so older worktrees and branches do not linger after upgrading.
func (h *Harness) cleanWarmLeftovers(ctx context.Context) {
	const warmBranchPrefix = "djinn-warm-"
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return
	}
	root := filepath.Join(h.home, "projects")
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	root += string(filepath.Separator)
	for _, p := range projects {
		if !p.GetGit() || p.GetDirectory() == "" {
			continue
		}
		if _, err := os.Stat(p.GetDirectory()); err != nil {
			continue
		}
		out, err := git(ctx, p.GetDirectory(), "worktree", "list", "--porcelain")
		if err != nil {
			continue
		}
		var path string
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "branch refs/heads/"+warmBranchPrefix) && strings.HasPrefix(path+string(filepath.Separator), root):
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				if err := removeWorktree(ctx, p.GetDirectory(), path, true); err != nil {
					logx.L().Warn("remove a leftover warm worktree", "project", p.GetId(), "path", path, "err", err)
				}
				if _, err := git(ctx, p.GetDirectory(), "branch", "-D", branch); err != nil {
					logx.L().Warn("remove a leftover warm branch", "project", p.GetId(), "branch", branch, "err", err)
				}
			}
		}
	}
}

// Spawn creates a task and starts its worker: in a Git project, in a new worktree on its own branch. procedure
// is the method the request came by, for the journal. A task that cannot start now (a dependency not done, its
// write scope taken, no slot free, the machine under pressure), or planned for later, is created pending with the
// reason, and the scheduler starts it as soon as it can. What the worker may do is decided when it starts
// (prepare).
// A task that repairs the wish's red push checks becomes their correction, or is refused while another one repairs
// them (pushrepair.go).
func (h *Harness) Spawn(ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
	task, err := h.spawn(ctx, procedure, req, nil)
	if err == nil && task.GetCorrection().GetAdopted() {
		h.adoptRepair(ctx, task)
	}
	return task, err
}

// spawn is Spawn, of a task Djinn spawns by itself when made is set, with what made says it is for: a correction
// worker (Task.correction), whose worktree starts on the failed merge, a review worker (Task.review), which works in
// the worktree and on the branch of the task it reviews (made's), a question worker (Task.role and question), which
// reads in its project's folder, or nothing more (an empty task). Without made, the spawn comes from outside Djinn: a
// repair of the red push checks is claimed (claimRepair).
func (h *Harness) spawn(
	ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest, made *planv1.Task,
) (*planv1.Task, error) {
	if req.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
		return h.spawnAzima(ctx, procedure, req)
	}
	if req.GetDraft() || req.GetDescription() != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only an azima can be a draft or have a description"))
	}
	scopes, err := cleanScopes(req.GetWriteScopes())
	if err != nil {
		return nil, err
	}
	task := &planv1.Task{
		Id: store.NewID(), WishId: req.GetWishId(), Title: req.GetTitle(), Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		CreateTime: timestamppb.New(h.now()), Model: req.GetModel(), MaxBudgetUsd: req.GetMaxBudgetUsd(),
		WriteScopes: scopes, Scheduled: true, Correction: made.GetCorrection(), Review: made.GetReview(), Role: made.GetRole(),
		Question: made.GetQuestion(), Babysit: made.GetBabysit(),
	}
	if made.GetId() != "" {
		task.Id = made.GetId()
	}
	if made.GetReview() != nil {
		task.Branch, task.Worktree = made.GetBranch(), made.GetWorktree()
	}
	logx.Lock(&h.sched, "scheduler")
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
	if req, err = babysitSpawn(ctx, h.home, req, project, task); err != nil {
		return nil, err
	}
	prompt := cmp.Or(req.GetPrompt(), req.GetTitle())
	// The Summoning Circle (settings.json) and the project's settings fill what the request leaves out
	// (docs/team-settings.md). A watcher runs a command: none applies to it.
	var settings plan.Settings
	var settingsErr error
	defaultProvider := planv1.Provider_PROVIDER_UNSPECIFIED
	defaultModel := ""
	if req.GetProvider() != planv1.Provider_PROVIDER_WATCH {
		if settings, settingsErr = plan.LoadSettings(h.home, project); settingsErr != nil {
			settings = plan.ResolveSettings(nil, nil)
		}
		defaultProvider, defaultModel = settings.Provider, settings.Model
		if cp, cm, ok := plan.ResolveWorkerSeat(h.home, workerKindOf(req, task)); ok {
			defaultProvider, defaultModel = cp, cm
		}
	}
	kind := cmp.Or(req.GetProvider(), defaultProvider, planv1.Provider_PROVIDER_CLAUDE)
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
	// A question worker's model and budget are the project's question workers', in its request.
	if kind != planv1.Provider_PROVIDER_WATCH && !questionWorker(task) {
		if task.Model == "" && kind == defaultProvider {
			task.Model = defaultModel
		}
		if task.MaxBudgetUsd == 0 {
			task.MaxBudgetUsd = settings.MaxBudgetUSD
		}
		if task.Model != "" {
			norm, err := plan.ValidateTaskModel(kind, task.Model)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
			task.Model = norm
		}
	}
	if len(req.GetTilasm()) > 0 {
		// The tilasms open the first prompt as they are now: a resumed worker reads the same.
		if kind == planv1.Provider_PROVIDER_WATCH {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--tilasm is for an agent: a watcher runs a command"))
		}
		material, err := plan.TilasmContext(ctx, h.store, h.home, wish.GetId(), req.GetTilasm())
		if err != nil {
			return nil, plan.Status(err)
		}
		prompt += "\n\n" + material
	}
	if kind == planv1.Provider_PROVIDER_WATCH {
		if project == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a watcher runs its command in a project: the wish has none"))
		}
		task.Restart = req.GetRestart()
		if req.GetUrgent() || req.GetFeedback() {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--urgent is for an agent: a watcher takes no slot"))
		}
	} else if req.GetRestart() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("--restart is for a watcher (--provider watch)"))
	}
	if err := ifritSpawn(req, kind, project, task); err != nil {
		return nil, err
	}
	if err := investigationSpawn(req, kind, project, task); err != nil {
		return nil, err
	}
	if made == nil {
		if task.Question, err = mockupQuestion(ctx, h.store, req); err != nil {
			return nil, err
		}
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
	if err := h.refuseAzimaDependencies(ctx, h.store, wish, append(slices.Clone(blocked), task)...); err != nil {
		return nil, plan.Status(err)
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return nil, plan.Status(err)
	}
	if req.GetUrgent() || req.GetFeedback() {
		task.Urgent, task.Feedback = true, req.GetFeedback()
		if err := refuseSecondUrgent(tasks, task); err != nil {
			return nil, err
		}
	}
	if made == nil {
		if err := h.claimRepair(req, wish, project, task, tasks); err != nil {
			return nil, err
		}
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
	var machine, room, slot bool
	var hold planv1.TaskHold
	for _, d := range sit.Pass() {
		if d.Task == task {
			why, failed, machine, room, slot, hold = d.Why, d.Failed, d.Machine, d.Room, d.Slot, d.Hold
		}
	}
	var preemptedCode string
	switch {
	case dispatch.ServedFirst(task):
		if why != "" && isMemoryShort(why) {
			if code, err := h.interruptPausedForMemory(ctx, task); err == nil && code != "" {
				preemptedCode = code
				if fresh, err3 := store.List[*planv1.Task](ctx, h.store, nil); err3 == nil {
					tasks = fresh
				}
				if sit2, err2 := h.situation(ctx, append(tasks, task)); err2 == nil {
					for _, d := range sit2.Pass() {
						if d.Task == task {
							why, failed, machine, room, hold = d.Why, d.Failed, d.Machine, d.Room, d.Hold
						}
					}
				}
			}
			if why != "" && isMemoryShort(why) {
				if code, err := h.preemptForMemory(ctx, task); err == nil && code != "" {
					preemptedCode = code
					if fresh, err3 := store.List[*planv1.Task](ctx, h.store, nil); err3 == nil {
						tasks = fresh
					}
					if sit2, err2 := h.situation(ctx, append(tasks, task)); err2 == nil {
						for _, d := range sit2.Pass() {
							if d.Task == task {
								why, failed, machine, room, hold = d.Why, d.Failed, d.Machine, d.Room, d.Hold
							}
						}
					}
				}
			}
		} else if slot {
			// It takes the slot of the work task it pauses, never one more: none to pause, it waits for a slot.
			if code, err := h.preemptForSlot(ctx, task); err == nil && code != "" {
				preemptedCode, why, machine, room, hold = code, "", false, false, 0
			}
		}
	case slot && plan.IsMerge(task):
		// A merge or a repair takes the slot of the work task it pauses, as an investigator does (W233).
		if code, err := h.preemptForSlot(ctx, task); err == nil && code != "" {
			preemptedCode, why, machine, room, hold = code, "", false, false, 0
		}
	case why != "" && isMemoryShort(why):
		if code, err := h.interruptPausedForMemory(ctx, task); err == nil && code != "" {
			preemptedCode = code
			if fresh, err3 := store.List[*planv1.Task](ctx, h.store, nil); err3 == nil {
				tasks = fresh
			}
			if sit2, err2 := h.situation(ctx, append(tasks, task)); err2 == nil {
				for _, d := range sit2.Pass() {
					if d.Task == task {
						why, failed, machine, room, hold = d.Why, d.Failed, d.Machine, d.Room, d.Hold
					}
				}
			}
		}
	}
	switch {
	case failed != "":
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the task would never start: %s", failed))
	case settingsErr != nil:
		why, machine, room, hold = fmt.Sprintf("settings unreadable: %v", settingsErr), false, false, 0
	case req.GetLater() && why == "":
		why = "planned: it starts at the scheduler's next pass"
	}
	var overlaps []spawnOverlap
	if made == nil {
		for _, b := range blocked {
			if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == b.GetId() }); i >= 0 {
				tasks[i] = b
			}
		}
		overlaps = h.spawnOverlaps(ctx, wish, project, task, parent, cmp.Or(req.GetPrompt(), req.GetTitle()), tasks)
	}
	if why != "" {
		task.MachineWait, task.RoomWait, task.Hold = machine, room, hold
		return h.plan(ctx, procedure, req, task, parent, prompt, why, overlaps)
	}

	actor := spawner(task) // before a babysit spawned by hand knows its request
	if task.GetBabysit() != nil && task.GetBabysit().GetNumber() == 0 {
		// A babysit spawned by hand that starts now watches what is open now.
		if prompt, err = h.resolveBabysit(ctx, wish, project, task, prompt); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("--babysit: %w", err))
		}
	}
	r, err := h.newRun(task)
	if err != nil {
		return nil, err
	}
	r.branch, r.from = settings.Branch, h.workerStartBranch(ctx, wish, project, settings, task)
	r.setChecks(settings)
	if questionWorker(task) {
		p, _, fb := questionProvider(settings)
		if task.GetProvider() == p {
			r.fallback = fb
		}
	}
	var prep prepared
	prompted := h.newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})
	var preEv, overlapEv *planv1.TaskEvent
	if preemptedCode != "" {
		preEv = h.newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "preempted " + preemptedCode + " to make room"})
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, procedure, req); err != nil {
			return err
		}
		if task.Code, err = nextCode(ctx, tx, wish.GetId()); err != nil {
			return err
		}
		if len(overlaps) > 0 {
			overlapEv = h.newEvent(task.GetId(), Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: formatSpawnOverlaps(task.GetCode(), overlaps),
			})
		}
		if task.PartOf != "" {
			if err := reopenAzima(ctx, tx, task.PartOf, task, "spawned"); err != nil {
				return err
			}
		}
		if parent != nil {
			if err := closeParent(ctx, tx, parent, task.GetCode()); err != nil {
				return err
			}
		}
		if prep, err = prepare(ctx, tx, h.home, task, wish, project); err != nil {
			return err
		}
		if err := h.block(ctx, tx, task, req.GetBlocks()); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		if err := putEvents(ctx, tx, prompted); err != nil {
			return err
		}
		if preEv != nil {
			if err := putEvents(ctx, tx, preEv); err != nil {
				return err
			}
		}
		if overlapEv != nil {
			return putEvents(ctx, tx, overlapEv)
		}
		return nil
	})
	if err != nil {
		h.forget(r)
		return nil, plan.Status(err)
	}
	h.publish(r, prompted)
	if preEv != nil {
		h.publish(r, preEv)
	}
	if overlapEv != nil {
		h.publish(r, overlapEv)
	}
	if len(blocked) > 0 {
		h.wake() // The tasks it blocks say what they wait for now.
	}
	return h.launch(ctx, r, provider, project, prep, prompt)
}

// spawner is who spawns task, as the journal records it: Djinn for a correction, a review or a question worker, or the
// watcher of a request a task opened; the developer's side otherwise, a repair Djinn took as a correction included.
func spawner(task *planv1.Task) string {
	if (task.GetCorrection() != nil && !task.GetCorrection().GetAdopted()) || task.GetReview() != nil || questionWorker(task) || task.GetBabysit().GetUrl() != "" {
		return actorHarness
	}
	return actorLocal
}

// after is what a spawn says comes before its task: --after, and --depends-on, its former name.
func after(req *planv1.TaskServiceSpawnRequest) []string {
	return append(slices.Clip(req.GetDependsOn()), req.GetAfter()...)
}

// plan creates a task that waits, with its prompt and why it waits, closes the parent it continues, if any, and makes
// the tasks it blocks (--blocks) wait for it; the scheduler starts it.
func (h *Harness) plan(
	ctx context.Context, procedure string, req *planv1.TaskServiceSpawnRequest, task, parent *planv1.Task, prompt, why string,
	overlaps []spawnOverlap,
) (*planv1.Task, error) {
	task.WaitReason = why
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(spawner(task), procedure, req); err != nil {
			return err
		}
		var err error
		if task.Code, err = nextCode(ctx, tx, task.GetWishId()); err != nil {
			return err
		}
		if task.PartOf != "" {
			if err := reopenAzima(ctx, tx, task.PartOf, task, "spawned"); err != nil {
				return err
			}
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
		if err := putEvents(ctx, tx, h.newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})); err != nil {
			return err
		}
		if err := putEvents(ctx, tx, h.newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "waiting: " + why})); err != nil {
			return err
		}
		if len(overlaps) > 0 {
			return putEvents(ctx, tx, h.newEvent(task.GetId(), Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: formatSpawnOverlaps(task.GetCode(), overlaps),
			}))
		}
		return nil
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	logx.L().Info("task held", "task", task.GetCode(), "hold", task.GetHold().String(), "why", why)
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
func prepare(
	ctx context.Context, tx *store.Tx, home string, task *planv1.Task, wish *planv1.Wish, project *planv1.Project,
) (prepared, error) {
	var p prepared
	var err error
	if questionWorker(task) {
		// It reads and runs djinn's commands, wherever it runs: no edit question to ask.
		task.Access = planv1.TaskAccess_TASK_ACCESS_DJINN
		return p, nil
	}
	if task.Access, p.declared, err = decideAccess(home, project, task.GetProvider(), plan.AllowanceOf(wish, project.GetId())); err != nil {
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

const workerHandRule = "To try a command by hand, start a Djinn of your own with its own data folder; never create a wish, a question or a task in the person's Djinn to test."

// workerCheckRule (Q246): what an agent can check, it checks; the developer runs no command to verify a worker's work.
const workerCheckRule = "Never ask the developer for a check, a measure, a log to read, a CI result to look at or a command to run: do it yourself when you can; otherwise say in your final note what remains to verify, and the lead spawns a task for it or lets a watcher report it (a CI result comes from the PR's babysit watcher)."
const workerMoveRule = "A question to the developer is only for a decision or a move only a person can make (grant a right, push a tag, open or merge a PR, start a release, check on a Mac or Windows Djinn does not have, install): ask it with `djinn question ask` before you end. " + plan.QuestionShape

// workerCommitRule (W360): a worker that edits a worktree commits its work itself; Djinn commits nothing for it, and
// starts a review worker on what it leaves (docs/team-settings.md#integration). Review and correction workers are
// told how to commit in their own prompts.
const workerCommitRule = "Commit your work before you end, with a message in the repository's style (`git log` shows it), and leave nothing not committed: Djinn commits nothing for you, and a review worker judges what you leave."

// workerWaitRule (W80): a worker that ends its turn on a run still going ends its task, and Djinn removes the worktree
// the run writes to.
const workerWaitRule = "Never end a turn while a long run you started is going (a benchmark, a build, a background command, a monitor): your task ends with your turn, and Djinn removes its worktree. Wait for it in the foreground, in commands under 9 minutes each (`sleep`, then read its output), until it ends; then report what it gave."

// workerDoubtRule (W260, decision Q50): a worker in real doubt asks a blocking question and ends its turn, its task
// continued with the decision; otherwise it decides, and says so. One blocking question at a time, plan.BlockingCap in
// all, plan.FreeCap that do not block: the questions service refuses more.
const workerDoubtRule = "Ask a blocking question (`djinn question ask --wait`) only in real doubt: two valid designs " +
	"whose consequences the developer will see (the interface, a behaviour, data, a cost), where a wrong pick throws " +
	"away the work that follows. Decide alone on naming, internals, what a test settles, and what a decision of the " +
	"wish settles already: read the wish's questions first (`djinn question list`). Before you ask with --wait, commit " +
	"all the work that does not depend on the answer; then end your turn, saying what waits for it: Djinn frees your " +
	"slot, keeps your worktree and session, and continues your task with \"Decision Qnn: ...\" once answered. A " +
	"choice you made that the developer may want to revisit is a question without --wait: go on with your pick. One " +
	"blocking question at a time, two per task, and three that do not block; past them, decide, and say it in your " +
	"final note."

// workerReadRule (W405): locate symbols with rg -n or grep -n before reading a file, and pass StartLine/EndLine (<= 200
// lines) instead of paging 800 lines at a time so agy stays below its ~120k compaction threshold.
const workerReadRule = "Locate symbols and lines with `rg -n` or `grep -n` before reading a file; never page through a " +
	"file with `view_file` 800 lines at a time. Pass `StartLine` and `EndLine` (`<= 200` lines) around the match so the " +
	"context stays below compaction."
const workerRules = workerHandRule + " " + workerCheckRule + " " + workerWaitRule + " " + workerMoveRule + " " +
	workerDoubtRule + " " + workerReadRule

// investigationRules are an investigation's or a mockup's: a worker's, but for the blocking question, as it ends with
// its question.
const investigationRules = workerHandRule + " " + workerCheckRule + " " + workerWaitRule + " " + workerMoveRule + " " +
	workerReadRule

// ifritRule (T51, Q11) is how an ifrit works: a turn per request, then it listens; Djinn runs what lasts, and keeps it
// alive. It replaces workerWaitRule, whose wait in the foreground would hold its turn open.
const ifritRule = "You are an ifrit: Djinn keeps you lit between the developer's requests, which the Djinn relays to " +
	"you as messages. After each request: make the change, say in three lines at most what changed and how to see it " +
	"(its URL), then end your turn; Djinn keeps you alive, and the next message starts your next turn. Run every " +
	"durable process (a dev server, a stack) with `djinn serve run <name> \"<command>\"` (`--port 5173=front`, " +
	"`--gate <gate>`, `--stop \"<command>\"`), never with `&` or `nohup`: Djinn shows its ports as links, and stops it " +
	"when you go out or end. Never wait in a loop or sleep. Run no check and no test suite after a change: only the " +
	"tests a request names, when it asks."
const ifritRules = workerHandRule + " " + workerCheckRule + " " + ifritRule + " " + workerMoveRule + " " + workerReadRule

// briefed is the first prompt of the run's task as its worker reads it, at launch and on a resume that gives it again:
// a worker that edits a worktree learns which checks its work meets (run.checks), to run the commit checks before it
// ends, to commit its work (workerCommitRule), to run what it can check itself, and to ask only a decision or a move
// only the developer can make. An ifrit
// learns its own rules instead (ifritRule), whatever folder it works in.
func briefed(r *run, prompt string, readOnly bool) string {
	rules := workerRules
	if r.ifrit {
		rules = ifritRules
	}
	switch {
	case dispatch.Investigation(r.task):
		rules = investigationRule(r.task) + " " + investigationRules
	case dispatch.Mockup(r.task):
		rules = mockupRule(r.task) + " " + investigationRules
	}
	edits := r.task.GetBranch() != "" && !readOnly
	if r.ifrit && !edits && r.task.GetProvider() != planv1.Provider_PROVIDER_FAKE {
		return prompt + "\n\n" + rules
	}
	if edits && !r.ifrit && !dispatch.Investigation(r.task) && !dispatch.Mockup(r.task) && r.task.GetReview() == nil &&
		r.task.GetCorrection() == nil {
		rules = workerCommitRule + " " + rules
	}
	if edits {
		if r.checks != "" {
			return prompt + "\n\n" + r.checks + " " + rules
		}
		if r.task.GetProvider() != planv1.Provider_PROVIDER_FAKE {
			return prompt + "\n\n" + rules
		}
	}
	return prompt
}

// launch starts the worker of the run's task, now written with its access: in its worktree, its project's folder,
// or an empty folder of its own. A failure ends the task, and is returned.
func (h *Harness) launch(
	ctx context.Context, r *run, provider Provider, project *planv1.Project, prep prepared, prompt string,
) (*planv1.Task, error) {
	task := r.task
	readOnly, perms := accessSpec(task.GetAccess(), prep.declared)
	perms = withCommit(task, perms)
	var skills []Skill
	var skillsDir string
	if !r.watcher && !r.light { // A command reads no skill, nor does a question worker.
		skills, skillsDir = h.summon(ctx, r, project)
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
	// The integration branch the worktree starts from, and its commit then, for the start event.
	fromText := ""
	if project.GetGit() && !r.watcher && !r.light { // They write nothing: they run in the project's folder.
		if task.GetReview() == nil { // A review worker works in the worktree, on the branch, of the task it reviews.
			task.Branch = branchName(r.branch, task.GetCode(), task.GetTitle(), task.GetId())
			task.Worktree = worktreeDir(h.home, project.GetId(), task.GetId())
		}
		var err error
		var sha, from string
		switch f := task.GetCorrection().GetFailure(); {
		case task.GetReview() != nil:
			dir, err = reviewWorktree(ctx, project.GetDirectory(), task.GetWorktree())
		case f != nil:
			dir, err = correctionWorktree(ctx, project.GetDirectory(), task.GetWorktree(), task.GetBranch(), f)
		default:
			if sha, from, err = h.startPoint(ctx, project, r.from); err == nil {
				if depWhy := h.dependenciesUnmergedIn(ctx, project.GetDirectory(), task, sha, cmp.Or(r.from, from)); depWhy != "" {
					logx.L().Info("worktree not created: a dependency is not merged", "task", task.GetCode(), "why", depWhy)
					_ = os.RemoveAll(skillsDir)
					task.Branch, task.Worktree = "", ""
					h.forgetWithoutWake(r)
					h.setWaiting(ctx, task, depWhy, false, false, planv1.TaskHold_TASK_HOLD_DEPENDENCY)
					return store.Get[*planv1.Task](ctx, h.store, task.GetId())
				}
				dir, err = addWorktreeFrom(ctx, project.GetDirectory(), task.GetWorktree(), task.GetBranch(), sha)
			}
		}
		if from != "" {
			fromText = ", from " + from + " at " + short8(sha)
		}
		if err != nil {
			task.Branch, task.Worktree = "", ""
			h.finish(r, Result{ExitCode: -1, Err: fmt.Errorf("create the worktree: %w", err)})
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: create the worktree: %w", task.GetCode(), err))
		}
	}
	where := "in " + dir
	if task.GetBranch() != "" {
		where += ", on branch " + task.GetBranch() + fromText
	}
	model := task.GetModel()
	if foreignModel(task.GetProvider(), model) && !r.watcher {
		model = DefaultModel(task.GetProvider())
		task.Model = model
	}
	spec := Spec{
		TaskID: task.GetId(), Dir: dir, ReadOnly: readOnly, Permissions: perms, Prompt: prompt, Model: model,
		MaxBudgetUSD: providerBudget(task), Resume: task.GetForkSession(), Fork: task.GetForkSession() != "",
		Skills: skills, SkillsDir: skillsDir, Restart: task.GetRestart(), ReadDirs: plan.TilasmDirs(h.home, prompt),
	}
	var err error
	if spec.WriteDirs, err = h.questionWriteDirs(task); err != nil {
		h.finish(r, Result{ExitCode: -1, Err: err})
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("task %s: %w", task.GetCode(), err))
	}
	if task.GetForkOf() != "" {
		where += ", forked from " + forkText(task.GetForkOf())
	}
	spec.Prompt = briefed(r, spec.Prompt, readOnly)
	text := "started " + short(task.GetProvider()) + " " + where + ", " + accessText(task, prep.question) +
		skillsText(spec.Skills)
	if r.watcher {
		command, perms, how, err := h.watchPermissions(ctx, task, project, spec.Permissions, spec.Prompt)
		if err != nil {
			h.finish(r, Result{ExitCode: -1, Err: err})
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("task %s: %w", task.GetCode(), err))
		}
		spec.Prompt, spec.Permissions = command, perms
		text = "started watch " + where + watchText(task) + how
	}
	if r.light {
		how := ""
		if r.fallback != "" {
			how = " (" + r.fallback + ")"
		}
		text = "started " + short(task.GetProvider()) + how + " " + where + ": " + questionStart(task) + "; " +
			accessText(task, nil)
	}
	err = h.start(r, provider, spec, text)
	if err != nil && r.light && questionWorker(task) && task.GetProvider() != planv1.Provider_PROVIDER_CLAUDE {
		if claudeProvider, ok := h.providers[planv1.Provider_PROVIDER_CLAUDE]; ok {
			origProvider := task.GetProvider()
			task.Provider = planv1.Provider_PROVIDER_CLAUDE
			task.Model = plan.DefaultQuestionModel
			spec.Model = plan.DefaultQuestionModel
			if errors.Is(err, ErrReadOnly) {
				r.fallback = fmt.Sprintf("%s cannot run read-only: %s", short(origProvider), short(planv1.Provider_PROVIDER_CLAUDE))
			} else {
				r.fallback = fmt.Sprintf("%s cannot start: %s", short(origProvider), short(planv1.Provider_PROVIDER_CLAUDE))
			}
			text = "started " + short(task.GetProvider()) + " (" + r.fallback + ") " + where + ": " + questionStart(task) + "; " +
				accessText(task, nil)
			err = h.start(r, claudeProvider, spec, text)
		}
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

// start marks the run's task running, says so with text, and how long and for what it waited before (waited), and
// starts its worker with spec. On an error the task is left to the caller to end.
func (h *Harness) start(r *run, provider Provider, spec Spec, text string) error {
	t := r.task
	now := h.now()
	nowPb := timestamppb.New(now)
	if t.FirstStartTime == nil {
		t.FirstStartTime = nowPb
	}
	t.Status, t.StartTime, t.EndTime, t.ExitCode, t.Error = planv1.TaskStatus_TASK_STATUS_RUNNING, nowPb, nil, 0, ""
	t.WaitReason, t.MachineWait, t.RoomWait, t.Hold, t.ResumeAfter = "", false, false, 0, nil
	t.LastActivityTime = nowPb
	h.mu.Lock()
	r.startTime = now
	r.lastActivity = now
	r.resultAt, r.silentWhy, r.silentNote = time.Time{}, "", false
	r.code = t.GetCode()
	r.listenSince, r.listened = time.Time{}, t.GetIfrit().GetListened().AsDuration()
	h.mu.Unlock()
	if r.ifrit {
		setIfrit(t, planv1.IfritState_IFRIT_STATE_WORKING, r.listened)
		spec.Listen = true
		since := h.presence.Last()
		if since.Before(now) {
			since = now // Lit at the developer's word: they were there.
		}
		outAt := since.Add(h.IfritIdle(t))
		h.mu.Lock()
		r.outAt = outAt
		h.mu.Unlock()
	}
	r.base = h.usageBase(t, spec)
	r.spentBase = t.GetUsage().GetCostUsd()
	r.turnsBefore = int(t.GetTurns().GetTotal())
	h.setTurns(r, func(int) int { return 1 })
	if len(t.Runs) > 0 && t.Runs[len(t.Runs)-1].EndTime == nil {
		t.Runs[len(t.Runs)-1].EndTime = nowPb
		if t.Runs[len(t.Runs)-1].Reason == "" {
			t.Runs[len(t.Runs)-1].Reason = "restart"
		}
	}
	t.Runs = append(t.Runs, &planv1.TaskRun{
		StartTime: nowPb,
	})
	r.failure, r.limit = "", nil
	r.answered, r.closingFailed = false, false
	if r.startHead == "" {
		wt := t.GetWorktree()
		if wt == "" {
			wt = spec.Dir
		}
		if wt != "" {
			if t.GetBranch() != "" && t.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
				r.startHead = branchStartCommit(h.ctx, wt, t.GetBranch())
			}
			if r.startHead == "" {
				if head, err := git(h.ctx, wt, "rev-parse", "HEAD"); err == nil {
					r.startHead = strings.TrimSpace(head)
				}
			}
		}
	}
	fresh(t)
	if foreignModel(t.GetProvider(), spec.Model) {
		spec.Model = DefaultModel(t.GetProvider())
	}
	if (t.GetModel() == "" || foreignModel(t.GetProvider(), t.GetModel())) && !r.watcher {
		t.Model = DefaultModel(t.GetProvider())
	}
	text += h.waited(h.ctx, t.GetId(), now)
	h.write(r, actorHarness, methodStart, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	if r.ifrit {
		h.relightServes(r, spec.Dir) // Before its agent: the message it was lit for may be about them.
	}
	// A worker that calls djinn knows its task; the Git guard keeps it from pushing.
	guard, err := gitGuard(h.home, spec.Dir)
	if err != nil {
		return fmt.Errorf("git guard: %w", err)
	}
	spec.Env = append([]string{
		"DJINN_TASK_ID=" + t.GetId(),
		"DJINN_WISH_ID=" + t.GetWishId(),
		"DJINN_WORKER_TOKEN=" + r.token,
	}, guard...)
	if r.watcher {
		// Its state folder, DJINN_HOME/watchers/<task>, outlives each run of its command (watcherLife); made before
		// it starts, as a lead's command expects when it names a file there (W7 of wish Dew).
		if err := os.MkdirAll(watcherDir(h.home, t.GetId()), 0o700); err != nil {
			return fmt.Errorf("make the watcher's folder: %w", err)
		}
		spec.Env = append(spec.Env, "DJINN_HOME="+h.home)
		spec.LastLine = t.GetLastLine()
	}
	spec.Scope = h.scopeFor(t)
	kind := "worker"
	if r.watcher {
		kind = "watcher"
	}
	spec.Track = h.children.tracker(t.GetCode(), kind)
	spec.LowPriority = h.lowPriority()
	spec.StartHead = r.startHead
	spec.Branch = t.GetBranch()
	spec.IsWorkDone = func() bool {
		wt := t.GetWorktree()
		if wt == "" {
			wt = spec.Dir
		}
		return h.taskWorkDone(r, t, wt)
	}
	h.prepare()
	w, err := provider.Start(h.ctx, spec)
	if err != nil {
		return err
	}
	h.mu.Lock()
	r.worker = w
	h.useMu.Lock()
	r.use = nil
	delete(h.resident, r.id)
	h.useMu.Unlock()
	if proc, ok := w.(Process); ok {
		r.pid = proc.PID()
		r.cgroup = proc.Cgroup()
	}
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
	case planv1.TaskAccess_TASK_ACCESS_DJINN:
		return "reading, and running only djinn's commands: a question worker, it takes no slot"
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

// nextCode is the code of a new task of the wish: W1, W2…, after the highest one ever given (nextNumber).
func nextCode(ctx context.Context, r store.Reader, wishID string) (string, error) {
	return nextNumber(ctx, r, wishID, "W")
}

// nextNumber is the code of a new task of the wish wishID lettered letter, W for work and T for an azima: the number
// after the highest one its tasks have or its deleted tasks had (Wish.retired_codes), so that a code is never given
// twice, deleted or not. The unique index guards the tasks' codes anyway.
func nextNumber(ctx context.Context, r store.Reader, wishID, letter string) (string, error) {
	return plan.NextTaskNumber(ctx, r, wishID, letter)
}

// pump records the worker's events until the task ends for good: a worker stopped to start again, allowed to
// edit, is followed by the next one.
func (h *Harness) pump(r *run) {
	defer logx.Crash("pump", "task", r.id, "wish", r.wish) // It only ever runs as a goroutine.
	for !h.settle(r, h.drain(r)) {
	}
}

// drain records the worker's events, and applies the answers to the task's edit question, until the worker ends.
func (h *Harness) drain(r *run) Result {
	events := r.worker.Events()
	h.useMu.Lock()
	r.resources = r.task.GetResources()
	h.useMu.Unlock()
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
				h.rebased(r)
				r.unread, r.unreadWaitTurn, r.held, r.tools = nil, false, nil, 0 // A worker started again never had them.
				h.mu.Lock()
				r.inTool = false
				h.mu.Unlock()
				return r.worker.Wait()
			}
			h.record(r, ev)
			if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT {
				h.rebased(r)
			}
		case ev := <-r.notes:
			h.note(r, ev)
		case m := <-r.sends:
			m.reply <- h.take(r, m)
		case written := <-r.holds:
			h.writeHold(r)
			close(written)
		case <-r.timers:
			h.ifritTimers(r)
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
	if ev.Model != "" {
		if r.requestedModel != "" && isModelAlias(r.requestedModel) {
			h.recordAlias(r.requestedModel, ev.Model)
		}
		h.learnAliases(ev.Model)
		if len(r.task.Models) == 0 || r.task.Models[len(r.task.Models)-1] != ev.Model {
			r.task.Models = append(r.task.Models, ev.Model)
			changed = true
		}
		if ev.Model != r.task.GetModel() {
			r.task.Model, changed = ev.Model, true
		}
	}
	if ev.SessionID != "" && ev.SessionID != r.task.GetSessionId() {
		r.task.SessionId, changed = ev.SessionID, true
	}
	if ev.Usage != nil {
		r.task.Usage, changed = sum(r.base, ev.Usage), true
		h.mu.Lock()
		r.spent = r.task.Usage.GetCostUsd()
		h.mu.Unlock()
	}
	if ev.Turns > 0 {
		h.setTurns(r, func(n int) int { return max(n, ev.Turns) })
	}
	if ev.Spent > 0 {
		h.mu.Lock()
		r.spent = r.spentBase + ev.Spent
		h.mu.Unlock()
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_USAGE {
		var uTurns int
		if _, err := fmt.Sscanf(ev.Text, "%d turns", &uTurns); err == nil && uTurns > 0 {
			h.setTurns(r, func(n int) int { return max(n, uTurns) })
		}
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
		h.setTurns(r, func(n int) int { return n + 1 })
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR &&
		(r.task.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY || r.task.GetProvider() == planv1.Provider_PROVIDER_FAKE) &&
		(r.closingFailed || (r.answered && r.tools == 0 && h.taskWorkDone(r, r.task, r.task.GetWorktree()))) {
		ev.Kind = planv1.TaskEventKind_TASK_EVENT_KIND_STATUS
		ev.Text = "closing call failed: " + ev.Text
		ev.Limit = nil
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_ERROR {
		r.failure = ev.Text
		h.netSign(r.task.GetProvider(), ev.Text, true) // Its agent cannot reach its provider: the network may be down.
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && strings.TrimSpace(ev.Text) != "" && !strings.HasSuffix(ev.Text, " [cut]") {
		r.answered = true
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL {
		r.answered = false
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.HasPrefix(ev.Text, "continued: ") {
		r.answered = false
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.HasPrefix(ev.Text, "closing call failed: ") {
		r.closingFailed = true
		logx.L().Info("closing call failed after final answer", "task", r.task.GetCode(), "why", strings.TrimPrefix(ev.Text, "closing call failed: "))
	}
	if ev.Limit != nil {
		r.limit = ev.Limit
	}
	if what, ok := strings.CutPrefix(ev.Text, permissionDenied); ok && ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS {
		r.refused = what
		r.refuse(what)
	}
	if w := ev.Watched; w != nil && w.First != "" {
		line := clipRunes(w.First, watchLineMax)
		if line != r.task.GetLastLine() || r.task.GetLastLineTime() == nil {
			r.task.LastLineTime = timestamppb.New(h.now()) // The same state said again keeps its time.
		}
		r.task.LastLine, changed = line, true
	}
	if ev.Ifrit != planv1.IfritState_IFRIT_STATE_UNSPECIFIED && r.ifrit {
		h.ifritTurn(r, ev.Ifrit)
		changed = true
	}
	var task proto.Message
	if changed {
		task = r.task
	}
	h.acknowledge(r, ev)
	h.write(r, actorWorker, methodEvent, task, ev)
	switch ev.Kind {
	case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
		r.tools++
	case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT:
		r.tools = max(r.tools-1, 0)
		h.turnOver(r)
	case planv1.TaskEventKind_TASK_EVENT_KIND_USAGE: // A turn's result says its usage: a call cut short has no result.
		r.tools = 0
		r.unreadWaitTurn = false
		h.turnOver(r)
		h.resulted(r)
	}
	if inTool := r.tools > 0; inTool != r.inTool { // Read by the scheduler's pass under mu (pauseForNetwork).
		h.mu.Lock()
		r.inTool = inTool
		h.mu.Unlock()
	}
	if ev.Ifrit == planv1.IfritState_IFRIT_STATE_LISTENING && r.ifrit {
		h.ifritTimers(r) // Its turn ended: past its out time, it goes out now.
	}
	if ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL && !r.notedPush {
		if r.ranPushCheck(ev.Text) || r.ranPushCheck(ev.Raw) {
			r.notedPush = true
			h.write(r, actorHarness, methodEvent, nil, Event{
				Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: "ran the push checks: Djinn runs them before the push",
			})
		}
	}
	if ev.Watched != nil {
		h.wakeLead(r, ev.Watched, ev.Text)
	}
	if !r.envReplayed && (ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL || ev.Kind == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT) {
		r.envReplayed = true
		if err := h.replayEnvFailures(context.Background(), r.task.GetProvider()); err != nil {
			logx.L().Error("start again the tasks failed for their environment", "task", r.id, "err", err)
		}
	}
}

// setChecks records what the project's settings say of checks Djinn runs, and its push checks to watch for.
func (r *run) setChecks(settings plan.Settings) {
	r.checks = settings.ChecksBrief()
	if r.ifrit {
		r.checks = settings.IfritChecksBrief()
	}
	r.testOne = settings.TestOne
	var pushes []string
	commitNames := make(map[string]bool)
	for _, c := range settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_COMMIT) {
		commitNames[strings.ToLower(c.GetName())] = true
	}
	for _, c := range settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_PUSH) {
		if commitNames[strings.ToLower(c.GetName())] {
			continue
		}
		if cmd := strings.TrimSpace(c.GetCommand()); cmd != "" {
			pushes = append(pushes, cmd)
		}
		if gateCmd := strings.TrimSpace(plan.GateCommand(c)); gateCmd != "" {
			pushes = append(pushes, gateCmd)
		}
		if name := strings.TrimSpace(c.GetName()); name != "" {
			pushes = append(pushes, "djinn gate run "+name)
		}
	}
	r.pushChecks = pushes
}

func (r *run) ranPushCheck(text string) bool {
	if text == "" || len(r.pushChecks) == 0 {
		return false
	}
	if r.testOne != "" {
		prefix, _, _ := strings.Cut(r.testOne, "{")
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && matchesCommand(text, prefix) {
			return false
		}
	}
	for _, cmd := range r.pushChecks {
		if matchesCommand(text, cmd) {
			return true
		}
	}
	return false
}

func matchesCommand(text, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	offset := 0
	for {
		idx := strings.Index(text[offset:], cmd)
		if idx < 0 {
			return false
		}
		i := offset + idx
		beforeOK := i == 0 || !isWordChar(text[i-1])
		end := i + len(cmd)
		afterOK := end >= len(text) || !isWordChar(text[end])
		if beforeOK && afterOK {
			return true
		}
		offset = i + len(cmd)
		if offset >= len(text) {
			return false
		}
	}
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '/'
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

// taskWorkDone reports whether task t has completed its deliverables: for a ChangesNoCode task (investigation or
// question worker), a published tilasm, question or plan block; for a code task, a clean worktree ahead of startHead.
func (h *Harness) taskWorkDone(r *run, t *planv1.Task, wt string) bool {
	if dispatch.ChangesNoCode(t) && changesNoCodeDone(h.ctx, h.store, t) {
		return true
	}
	if wt == "" {
		return false
	}
	return agyWorkDone(h.ctx, wt, r.startHead, t.GetBranch())
}

func (h *Harness) incRateHits(taskID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rateHits[taskID]++
	return h.rateHits[taskID]
}

func (h *Harness) hasRateHits(taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rateHits[taskID] > 0
}

func (h *Harness) clearRateHits(taskID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rateHits, taskID)
}

// agyDoneAfterWork checks whether an antigravity worker already gave its final answer and committed its work
// (worktree clean, branch ahead with commits made in this run, or published its ChangesNoCode deliverables) and only
// then hit an error.
func (h *Harness) agyDoneAfterWork(r *run, res Result) bool {
	if r.task.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY && r.task.GetProvider() != planv1.Provider_PROVIDER_FAKE {
		return false
	}
	if r.closingFailed {
		return true
	}
	if res.Err == nil && res.ExitCode == 0 && r.failure == "" {
		return false
	}
	if !r.answered {
		return false
	}
	return h.taskWorkDone(r, r.task, r.task.GetWorktree())
}

// end gives the task its final status, then lets its watchers go. A task whose worker read while it asks whether
// it may edit waits for the answer. A task is done only when its worker ended without an error, whatever its
// provider: an error it reported, an exit code other than 0, or an error it said on the way, even when its
// process then exited 0, fails the task with the reason. But a failure its provider's usage limit caused is no
// failure: the task waits for the limit to reset, and resumes.
func (h *Harness) end(r *run, res Result) {
	if r.ifrit {
		h.endServes(r) // Before the notes: what its serves said before they stopped is stale now.
	}
	for flushed := false; !flushed; {
		select {
		case ev := <-r.notes:
			h.note(r, ev)
		default:
			flushed = true
		}
	}
	h.mu.Lock()
	stopping, stoppingReason, shelved, preemptedWhy, lamplighterEnd, lamplighterReason := r.stopping, r.stoppingReason, r.shelved, r.preemptedWhy, r.lamplighterEnd, r.lamplighterReason
	silentWhy, silentNote := r.silentWhy, r.silentNote
	relaunched := r.relaunched
	out := r.out
	nextProvider, nextModel := r.nextProvider, r.nextModel
	h.mu.Unlock()
	var streamIdleReason string
	if isStreamIdle(res.Err) {
		streamIdleReason = res.Err.Error()
	} else if isStreamIdleString(r.failure) {
		streamIdleReason = r.failure
	}
	streamIdleEnd := streamIdleReason != ""
	t := r.task
	if t.GetModel() == "" && !r.watcher {
		if len(t.Models) > 0 {
			t.Model = t.Models[len(t.Models)-1]
		} else {
			t.Model = DefaultModel(t.GetProvider())
		}
	}
	if t.GetModel() != "" && !r.watcher {
		if len(t.Models) == 0 {
			t.Models = []string{t.GetModel()}
		} else if t.Models[len(t.Models)-1] != t.GetModel() {
			t.Models = append(t.Models, t.GetModel())
		}
		h.learnAliases(t.GetModel())
	}
	if r.watcher {
		t.Model = ""
		t.Models = nil
	}
	now := h.now()
	nowPb := timestamppb.New(now)
	t.EndTime, t.ExitCode = nowPb, int32(res.ExitCode)
	if r.ifrit {
		h.mu.Lock()
		listened := r.listenedBy(now)
		h.mu.Unlock()
		state := planv1.IfritState_IFRIT_STATE_UNSPECIFIED
		if out != "" && !stopping {
			state = planv1.IfritState_IFRIT_STATE_OUT
		}
		setIfrit(t, state, listened)
		t.Ifrit.OutTime, t.Ifrit.WarnTime = nil, nil
	}
	// Under useMu, as measure sets them. A copy: the reading is shared with what the
	// workers use (Uses, RunsSeries), which never changes it.
	h.useMu.Lock()
	if t.Resources != nil {
		t.Resources = proto.CloneOf(t.Resources)
	}
	if r.samples != nil {
		if pct := r.samples.MemoryPercentiles(); pct.p95 > 0 {
			if t.Resources == nil {
				t.Resources = &planv1.Resources{}
			}
			pct.set(t.Resources)
		}
	}
	if t.Resources != nil && t.Resources.P95MemoryBytes == 0 && t.Resources.PeakMemoryBytes > 0 {
		t.Resources.P95MemoryBytes = t.Resources.PeakMemoryBytes
	}
	h.useMu.Unlock()
	switch {
	case preemptedWhy != "" && t.GetStartTime() == nil:
		t.Status, t.Error, t.EndTime, t.ExitCode, t.WaitReason = planv1.TaskStatus_TASK_STATUS_PENDING, "", nil, 0, preemptedWhy
	case preemptedWhy != "":
		t.Status, t.Error, t.WaitReason, t.ResumeAfter = planv1.TaskStatus_TASK_STATUS_RESUMING, "", preemptedWhy, nil
	case stopping:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_STOPPED, cmp.Or(stoppingReason, "stopped on request")
	case out != "":
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_STOPPED, out
	case silentWhy != "":
		endedSilent(t, silentWhy, silentNote)
	case streamIdleEnd && !lamplighterEnd && h.netHolds():
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, streamIdleReason // The network cut it: networkCut.
	case lamplighterEnd || streamIdleEnd:
		why := streamIdleReason
		if lamplighterEnd {
			why = lamplighterReason
		}
		if canWake(t) || (relaunched && plan.CanResume(t.GetProvider())) {
			markWoken(t, why, nowPb)
			logx.L().Info("lamplighter wakes a task", "task", t.GetCode(), "why", why)
		} else {
			t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, notWoken(t, why)
			logx.L().Info("lamplighter fails a task", "task", t.GetCode(), "why", why)
		}
	case shelved:
		shelve(t)
	case h.ctx.Err() != nil && t.GetScheduled() && t.GetStartTime() == nil:
		// Stopped before its worker started (its worktree being made): nothing to interrupt, nothing to resume. It
		// waits for a worker again, as planned; interrupted without a worktree, it would never resume.
		t.Status, t.Error, t.EndTime, t.ExitCode, t.WaitReason = planv1.TaskStatus_TASK_STATUS_PENDING, "", nil, 0, whyStoppedFirst
	case h.ctx.Err() != nil:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_INTERRUPTED, "djinn up stopped while the worker ran"
	case h.agyDoneAfterWork(r, res):
		t.Status, t.Error, t.ExitCode = planv1.TaskStatus_TASK_STATUS_DONE, "", 0
		why := r.failure
		if why == "" && res.Err != nil {
			why = res.Err.Error()
		}
		if why == "" && res.ExitCode != 0 {
			why = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		if why == "" {
			why = "model error"
		}
		res.Err, res.ExitCode, r.failure, r.limit = nil, 0, "", nil
		if !r.closingFailed {
			r.closingFailed = true
			logx.L().Info("closing call failed after final answer", "task", t.GetCode(), "why", why)
			h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "closing call failed: " + why})
		}
	case res.Err != nil:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, res.Err.Error()
	case r.failure != "":
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, r.failure
	case res.ExitCode != 0:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, fmt.Sprintf("exit code %d", res.ExitCode)
	case t.GetAccess() == planv1.TaskAccess_TASK_ACCESS_ASKING:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_WAITING, "waiting for the answer to its edit question"
	case r.light && r.refused != "" && questionWorker(t) && !h.questionDid(t):
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_FAILED, refusedError(r.refused)
	default:
		t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_DONE, ""
	}
	// A worker that asked a blocking question ends its turn, not its task: the task waits on the question, its slot
	// free; answered while it ran without the decision reaching it, the task continues with it at once.
	var decision *planv1.Question
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !r.light && !r.watcher {
		if q := h.waitOn(context.Background(), t); q != nil {
			waitOnQuestion(t, q)
			if q.GetAnswer() != nil {
				decision = q
			}
		}
	}
	if !r.watcher {
		t.Refused = r.refusals
	}
	integrate, left := h.pendIntegration(t)
	if dispatch.ChangesNoCode(t) {
		h.unpausePreempted(t) // The work task it paused for its slot resumes first.
	}
	why := t.GetError()
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED {
		h.limited(r, t)
		h.networkCut(r, t, streamIdleEnd)
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED {
		if cause, isEnv := classifyFailure(r, t, res); isEnv {
			t.EnvCause = cause
			t.EnvBuild = h.version
		}
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
		h.clearRateHits(t.GetId())
		if err := h.replayEnvFailures(context.Background(), t.GetProvider()); err != nil {
			logx.L().Error("start again the tasks failed for their environment", "task", t.GetCode(), "err", err)
		}
		if err := h.unblockDependencyFailed(context.Background(), t.GetCode()); err != nil {
			logx.L().Error("unblock the tasks a dependency failed", "task", t.GetCode(), "err", err)
		}
	}
	reason := runReason(t, r)
	if silentWhy != "" && t.GetContinuing() {
		reason = runSilent
	}
	if len(t.Runs) > 0 {
		last := t.Runs[len(t.Runs)-1]
		if last.EndTime == nil {
			last.EndTime = nowPb
			last.Reason = reason
		}
	}
	text := short(t.GetStatus())
	switch {
	case t.GetWaitQuestionId() != "" && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING:
		text = "waiting: " + t.GetError() + "; its slot is free, and the answer continues it with the decision"
	case preemptedWhy != "":
		text = "resuming: " + preemptedWhy
	case shelved:
		text = "resuming: " + whyWishPaused
	case silentWhy != "":
		text = silentText(t, silentWhy)
	case lamplighterEnd && t.GetContinuing():
		text = "resuming: " + lamplighterReason + "; continued"
	case streamIdleEnd && t.GetContinuing():
		text = "resuming: " + streamIdleReason + "; continued"
	case ifritOut(t):
		text = t.GetError() // It says out, and why.
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING:
		text = "waiting: " + whyStoppedFirst
	case networkWait(t):
		text = "resuming: " + t.GetWaitReason() + " (" + why + "); Djinn resumes it once the network is back"
	case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && t.GetResumeAfter() != nil:
		text = "waiting for the limit: " + t.GetWaitReason() + " (" + why + "); Djinn resumes it then"
	case t.GetError() != "":
		text += ": " + t.GetError()
	}
	if (lamplighterEnd || streamIdleEnd) && t.GetContinuing() {
		h.mu.Lock()
		prompt := cmp.Or(r.wakePrompt, agyContinuePrompt)
		h.mu.Unlock()
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: prompt})
	}
	if silentWhy != "" && t.GetContinuing() {
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, Text: finishPrompt})
	}
	if nextProvider != planv1.Provider_PROVIDER_UNSPECIFIED {
		currentProvider := t.GetProvider()
		if currentProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
			currentProvider = planv1.Provider_PROVIDER_CLAUDE
		}
		if nextProvider != currentProvider {
			t.PriorProvider = currentProvider
			t.SessionId = ""
		}
		t.Provider = nextProvider
		t.Model = nextModel
	}
	h.write(r, actorHarness, methodEnd, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	if integrate {
		text := "integration: pending, to be merged into the integration branch"
		if t.GetIntegration().GetReason() != "" {
			text = "integration: waiting: " + t.GetIntegration().GetReason()
		}
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	}
	if left != "" && left != nothingToMerge { // Work that brings nothing: its end's line says it (tellDone).
		h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "integration: none: " + left})
	}
	switch {
	case watching(t) && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED:
		h.tellWatcherFailed(t)
	case ifritOut(t):
		h.tellOut(t)
	case silentWhy != "" && t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED:
		if !tellsDone(t) || !toldDoneAtEnd(t, integrate) { // Else its done line says it.
			h.tellSilent(t, silentWhy, integrate) // Its one line: done, or continued once.
		}
		h.babysitOpened(proto.CloneOf(t))
	case iterates(t) && !relaunched:
		h.mockupEnded(t) // Its one line, as an investigator's: the version it put, the question it revised or not.
	case !r.light && !watching(t) && !relaunched: // Relaunched, its lead or the developer asked for it.
		h.workerEnded(t)
		h.babysitOpened(proto.CloneOf(t))
	}
	if tellsDone(t) && toldDoneAtEnd(t, integrate) && !iterates(t) {
		h.tellDone(context.Background(), t, notIntegrated(left), silentWhy)
	}
	if t.GetCorrection() != nil || t.GetReview() != nil {
		h.unpausePreempted(t) // The work task it paused for its slot resumes once that slot is free.
	}
	if r.light {
		h.questionEnded(t) // The lead learns what it did, before the task's watchers let go, as a watcher's lead does.
	}
	// The links to the summoned skills go with the worker; a worker started again makes them anew.
	_ = os.RemoveAll(skillsDir(h.home, r.id))
	h.forget(r)
	if integrate || t.GetCorrection() != nil || t.GetReview() != nil {
		h.kickIntegrate()
	}
	if decision != nil {
		h.decideLater(t, decision)
	}
}

// classifyFailure determines whether a worker's failure was caused by the provider's environment or by its work.
func classifyFailure(r *run, t *planv1.Task, res Result) (cause string, isEnv bool) {
	allErr := t.GetError()
	if res.Err != nil {
		allErr += " " + res.Err.Error()
	}
	if r != nil {
		if r.failure != "" {
			allErr += " " + r.failure
		}
		if r.refused != "" {
			allErr += " " + r.refused
		}
	}
	lower := strings.ToLower(allErr)
	if res.ExitCode == 137 || strings.Contains(lower, "signal: killed") || strings.Contains(lower, "killed by the machine") {
		return "killed by the machine", true
	}
	if errors.Is(res.Err, exec.ErrNotFound) || strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "cli missing") || strings.Contains(lower, "cli outdated") {
		return "cli missing or outdated", true
	}
	if (strings.Contains(lower, "headless") && (strings.Contains(lower, "permission") ||
		strings.Contains(lower, "cannot prompt") || strings.Contains(lower, "cannot ask") ||
		strings.Contains(lower, "cannot run commands"))) ||
		strings.Contains(lower, "cannot run commands headless") ||
		strings.Contains(lower, "permission refused headless") {
		return "permission refused headless", true
	}
	if errors.Is(res.Err, ErrReadOnly) || strings.Contains(lower, "cannot run without editing yet") ||
		strings.Contains(lower, "cannot run a read-only task yet") || strings.Contains(lower, "provider could not start") ||
		strings.Contains(lower, "could not start") {
		return "provider could not start", true
	}
	if (strings.Contains(lower, "without finishing") && strings.Contains(lower, "resumed")) ||
		(t.GetResumes() >= maxResumes && ((r != nil && r.limit != nil) || strings.Contains(lower, "session limit"))) {
		return "session limit beyond retries", true
	}
	return "", false
}

// workerEnded tells the lead of t's wish what went wrong if the work worker t failed or was cut short.
func (h *Harness) workerEnded(t *planv1.Task) {
	if t.GetEnvCause() != "" && t.GetEnvReplays() < 2 {
		return
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED ||
		(t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && (t.GetResumeAfter() != nil || t.GetContinuing())) ||
		(t.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED && !strings.HasPrefix(t.GetError(), "stopped on request")) {
		if h.flaggedAtEnd(t) {
			return // The scheduler's next pass flags it, in one line with the command that relaunches it (health.go).
		}
		h.tellWorkerFailed(t)
	}
}

// forget removes the run once its task is written for good, and ends its watchers' live feed.
func (h *Harness) forget(r *run) {
	h.forgetWithoutWake(r)
	h.wake() // A slot, a scope or a dependency may have freed.
}

// forgetWithoutWake removes a run that never started a worker without waking the scheduler again.
func (h *Harness) forgetWithoutWake(r *run) {
	h.mu.Lock()
	delete(h.runs, r.id)
	h.useMu.Lock()
	delete(h.resident, r.id)
	h.useMu.Unlock()
	for ch := range r.subs {
		close(ch)
	}
	r.subs = nil
	h.notifyLocked()
	h.mu.Unlock()
	close(r.done)
	h.wg.Done()
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
		logx.L().Error("record an event", "task", r.id, "err", err)
	}
}

// writeAs is write, journaling req; nil journals the event.
func (h *Harness) writeAs(r *run, actor, method string, req, task proto.Message, ev Event) (*planv1.TaskEvent, error) {
	te := h.newEvent(r.id, ev)
	if req == nil {
		req = te
	}
	h.mu.Lock()
	nextProvider, nextModel := r.nextProvider, r.nextModel
	h.mu.Unlock()
	err := h.store.Tx(context.Background(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, method, req); err != nil {
			return err
		}
		if task != nil {
			putTask := task
			if pt, ok := task.(*planv1.Task); ok {
				carry(tx, pt)
				if method != methodEnd && nextProvider != planv1.Provider_PROVIDER_UNSPECIFIED {
					cp := proto.CloneOf(pt)
					currentProvider := pt.GetProvider()
					if currentProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
						currentProvider = planv1.Provider_PROVIDER_CLAUDE
					}
					if nextProvider != currentProvider {
						cp.PriorProvider = currentProvider
						cp.SessionId = ""
					}
					cp.Provider = nextProvider
					cp.Model = nextModel
					putTask = cp
				}
			}
			if err := tx.Put(putTask); err != nil {
				return err
			}
		}
		if method == methodStart || (method == methodHold && ev.Text == "resumed") {
			part := r.task
			if pt, ok := task.(*planv1.Task); ok && pt != nil {
				part = pt
			}
			if partOf := part.GetPartOf(); partOf != "" {
				if err := reopenAzima(context.Background(), tx, partOf, part, "started"); err != nil {
					return err
				}
			}
		}
		return putEvents(context.Background(), tx, te)
	})
	if err != nil {
		return nil, err
	}
	// The scheduler reads a copy: the pump changes its task without a lock (W182).
	var shown *planv1.Task
	if pt, ok := task.(*planv1.Task); ok && pt.GetId() == r.id {
		shown = proto.CloneOf(pt)
	}
	h.mu.Lock()
	r.lastActivity, r.lastKind = h.now(), te.GetKind()
	r.said(te.GetKind())
	if r.task != nil {
		r.task.LastActivityTime = timestamppb.New(r.lastActivity)
	}
	if shown != nil {
		r.shown = shown
	}
	h.mu.Unlock()
	h.publish(r, te)
	return te, nil
}

// carry copies onto the run's copy of its task, before it is written, what is stored on the task from outside the
// run while it runs: the comments on its diff, the developer's marks, its title and urgent mark (Update, W234), the
// babysit Djinn found it does (adoptBabysit), and the health the scheduler judged it by.
func carry(tx *store.Tx, t *planv1.Task) {
	if stored, err := store.Get[*planv1.Task](context.Background(), tx, t.GetId()); err == nil {
		t.Comments = stored.GetComments()
		t.Marks = stored.GetMarks()
		t.Title = stored.GetTitle()
		t.Urgent, t.Feedback = stored.GetUrgent(), stored.GetFeedback()
		// The health the scheduler judged this run by (health.go): one judged before it started is the last run's.
		t.Health = nil
		if health := stored.GetHealth(); health != nil && t.GetStartTime() != nil &&
			!health.GetSince().AsTime().Before(t.GetStartTime().AsTime()) {
			t.Health = health
		}
		if t.GetBabysit().GetUrl() == "" && stored.GetBabysit().GetUrl() != "" {
			done := t.GetBabysit().GetDoneTime()
			t.Babysit = proto.CloneOf(stored.GetBabysit())
			if done != nil {
				t.Babysit.DoneTime = done
			}
		}
	}
}

// newEvent builds an event to store, its text and raw line cut to maxText; putEvents gives it its position.
func (h *Harness) newEvent(taskID string, ev Event) *planv1.TaskEvent {
	return newEventAt(taskID, ev, h.now())
}

func newEventAt(taskID string, ev Event, at time.Time) *planv1.TaskEvent {
	return &planv1.TaskEvent{
		Id: store.NewID(), TaskId: taskID, Kind: ev.Kind, Text: clip(ev.Text), Usage: ev.Usage,
		Raw: clip(ev.Raw), CreateTime: timestamppb.New(at),
	}
}

func newEvent(taskID string, ev Event) *planv1.TaskEvent {
	return newEventAt(taskID, ev, time.Now())
}

// putEvents writes the events in tx, each at the position after its task's last event. It is the one place that
// numbers a task's events: the store has one connection, so a transaction reads the last position no other writer
// can take before it commits. A run that counted its events apart gave one position to two events when a gate's note
// or an integration step was written beside it, and the second was lost (W534). It counts a gate event in its
// task's gate wait (plan.CountGate).
func putEvents(ctx context.Context, tx *store.Tx, events ...*planv1.TaskEvent) error {
	for _, e := range events {
		seq, err := lastSeq(ctx, tx, e.GetTaskId())
		if err != nil {
			return err
		}
		e.Seq = seq + 1
		if err := plan.CountGate(ctx, tx, e); err != nil {
			return err
		}
		if err := tx.Put(e); err != nil {
			return err
		}
	}
	return nil
}

func clip(s string) string {
	if len(s) <= maxText {
		return s
	}
	return s[:maxText] + fmt.Sprintf("… (%d bytes cut)", len(s)-maxText)
}

// sum adds what a run's session spent to the baseline before that session, clamping at zero when a forked session
// reports less than its parent baseline.
func sum(base, run *planv1.Usage) *planv1.Usage {
	return &planv1.Usage{
		InputTokens:      max(0, base.GetInputTokens()+run.GetInputTokens()),
		OutputTokens:     max(0, base.GetOutputTokens()+run.GetOutputTokens()),
		CacheReadTokens:  max(0, base.GetCacheReadTokens()+run.GetCacheReadTokens()),
		CacheWriteTokens: max(0, base.GetCacheWriteTokens()+run.GetCacheWriteTokens()),
		CostUsd:          max(0, base.GetCostUsd()+run.GetCostUsd()),
		ThinkingTokens:   max(0, base.GetThinkingTokens()+run.GetThinkingTokens()),
	}
}

func subUsage(a, b *planv1.Usage) *planv1.Usage {
	if a == nil && b == nil {
		return nil
	}
	return &planv1.Usage{
		InputTokens:      a.GetInputTokens() - b.GetInputTokens(),
		OutputTokens:     a.GetOutputTokens() - b.GetOutputTokens(),
		CacheReadTokens:  a.GetCacheReadTokens() - b.GetCacheReadTokens(),
		CacheWriteTokens: a.GetCacheWriteTokens() - b.GetCacheWriteTokens(),
		CostUsd:          a.GetCostUsd() - b.GetCostUsd(),
		ThinkingTokens:   a.GetThinkingTokens() - b.GetThinkingTokens(),
	}
}

// usageBase returns the baseline to add to a worker's cumulative session result event.
// Each result event from Claude, Antigravity, or Codex reports the cumulative total for the active session.
// Therefore:
//   - Starting a new session (spec.Resume == "") carries over t.GetUsage() from any prior session.
//   - Forking a session (spec.Fork) subtracts the parent session's cumulative baseline.
//   - Resuming the same session (spec.Resume != "" && !spec.Fork) does not add t.GetUsage() from the same session
//     a second time; it only keeps prior sessions' usage (when the task switched sessions earlier) or the negative
//     fork baseline (when the task is still on its forked session).
func (h *Harness) usageBase(t *planv1.Task, spec Spec) *planv1.Usage {
	if spec.Resume == "" {
		return proto.CloneOf(t.GetUsage())
	}
	if spec.Fork {
		return subUsage(t.GetUsage(), h.forkUsage(t, spec.Resume))
	}
	last, switched := h.sessionUsage(t.GetId(), spec.Resume)
	if !switched {
		if t.GetForkSession() != "" {
			return subUsage(nil, h.forkUsage(t, t.GetForkSession()))
		}
		return nil
	}
	if last != nil {
		return subUsage(t.GetUsage(), last)
	}
	return proto.CloneOf(t.GetUsage())
}

// forkUsage returns the cumulative session usage of the parent session that t forked from.
func (h *Harness) forkUsage(t *planv1.Task, sessionID string) *planv1.Usage {
	if h.store == nil {
		return nil
	}
	if t.GetForkOf() == forkLead && sessionID != "" {
		if batches, err := store.List[*planv1.LeadBatch](h.ctx, h.store, nil); err == nil {
			var found *planv1.Usage
			for _, b := range batches {
				if b.GetSessionId() == sessionID && b.GetUsage() != nil {
					found = proto.CloneOf(b.GetUsage())
				}
			}
			if found != nil {
				return found
			}
		}
	}
	var where store.Where
	if t.GetWishId() != "" {
		where = store.Where{"wish_id": t.GetWishId()}
	}
	tasks, err := store.List[*planv1.Task](h.ctx, h.store, where)
	if err != nil {
		return nil
	}
	for _, other := range tasks {
		if other.GetId() == t.GetId() {
			continue
		}
		if (t.GetForkOf() != "" && other.GetCode() == t.GetForkOf()) || (sessionID != "" && other.GetSessionId() == sessionID) {
			if u, _ := h.sessionUsage(other.GetId(), sessionID); u != nil {
				return u
			}
			if other.GetUsage() != nil {
				return proto.CloneOf(other.GetUsage())
			}
		}
	}
	return nil
}

// sessionUsage returns the latest raw USAGE event emitted by taskID in its current session, and whether taskID
// switched from an earlier session to the current one.
func (h *Harness) sessionUsage(taskID, sessionID string) (last *planv1.Usage, switched bool) {
	if h.store == nil || taskID == "" {
		return nil, false
	}
	_ = store.Latest(h.ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		switch e.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_USAGE:
			if last == nil && e.GetUsage() != nil {
				last = proto.CloneOf(e.GetUsage())
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_STATUS:
			text := e.GetText()
			if strings.Contains(text, "from its first prompt") {
				switched = true
				return false
			}
			if sessionID != "" && strings.HasPrefix(text, "session ") {
				sid := strings.TrimPrefix(text, "session ")
				if i := strings.IndexByte(sid, ','); i >= 0 {
					sid = sid[:i]
				}
				sid = strings.TrimSpace(sid)
				if sid != "" && sid != sessionID {
					switched = true
					return false
				}
			}
		}
		return true
	})
	return last, switched
}

// lastSeq is the position of the task's last event. A task's events are written in the order of their positions, each
// one after the last (an import writes them in that order too): the last written holds it. It reads that one alone,
// where a task may hold thousands, a tool's output in each.
func lastSeq(ctx context.Context, r store.Reader, taskID string) (int64, error) {
	var last int64
	err := store.Latest(ctx, r, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		last = e.GetSeq()
		return false
	})
	return last, err
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
		if task.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING {
			return h.stopLost(ctx, procedure, req, task)
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
	logx.Lock(&h.sched, "scheduler")
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
	task.Status, task.Error, task.WaitReason, task.MachineWait, task.RoomWait, task.Hold, task.ResumeAfter, task.EndTime = planv1.TaskStatus_TASK_STATUS_STOPPED, why, "", false, false, 0,
		nil, timestamppb.Now()
	h.writeAlone(ctx, actorLocal, procedure, req, task.GetId(), task, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "stopped: " + task.GetError()})
	h.sched.Unlock()
	h.wake() // Its dependents fail in turn.
	return store.Get[*planv1.Task](ctx, h.store, task.GetId())
}

// stopLost stops a task whose status is RUNNING but has no worker running in harness (a lost process).
func (h *Harness) stopLost(ctx context.Context, procedure string, req *planv1.TaskServiceStopRequest, task *planv1.Task) (*planv1.Task, error) {
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	latest, err := store.Get[*planv1.Task](ctx, h.store, req.GetTaskId())
	if err != nil {
		return nil, plan.Status(err)
	}
	if latest.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		return latest, nil
	}
	why := "stopped on request: worker was lost"
	latest.Status, latest.Error, latest.WaitReason, latest.ResumeAfter, latest.EndTime = planv1.TaskStatus_TASK_STATUS_STOPPED, why, "", nil, timestamppb.Now()
	h.writeAlone(ctx, actorLocal, procedure, req, latest.GetId(), latest, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "stopped: " + latest.GetError()})
	h.wake()
	return store.Get[*planv1.Task](ctx, h.store, latest.GetId())
}

// Pause holds the task's worker where it is, without killing it, until Resume: the task is paused, and its slot
// of the machine is free meanwhile. A worker whose provider cannot pause, or Windows, refuses.
// A worker that holds a gate is not paused: the gate would stay held, frozen, for every other worker. Nor is one
// that waits for a gate: it would get the gate in its turn, and hold it frozen.
func (h *Harness) Pause(ctx context.Context, procedure string, req *planv1.TaskServicePauseRequest) (*planv1.Task, error) {
	return h.hold(ctx, procedure, req, req.GetTaskId(), true)
}

// Resume lets a paused worker go on. It does not wait for a slot: the developer asked for it, and the load bar shows
// it beyond what it lets run (Forced).
func (h *Harness) Resume(ctx context.Context, procedure string, req *planv1.TaskServiceResumeRequest) (*planv1.Task, error) {
	return h.hold(ctx, procedure, req, req.GetTaskId(), false)
}

// hold pauses or resumes the task's worker, then waits until its pump has written the task so, or the task ended.
func (h *Harness) hold(ctx context.Context, procedure string, req proto.Message, id string, pause bool, why ...string) (*planv1.Task, error) {
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
	byHarness := procedure == methodNotch || procedure == methodPreempt || procedure == methodNetwork
	if pause {
		r.notchPaused = (procedure == methodNotch)
		r.slotPaused = (procedure == methodPreempt)
		r.netPaused = (procedure == methodNetwork)
	} else {
		// Resumed by hand, it takes a slot whatever the others: the load bar shows it (Forced).
		r.notchPaused, r.slotPaused, r.netPaused, r.forced = false, false, false, !byHarness
	}
	if len(why) > 0 {
		r.holdWhy = why[0]
	}
	h.mu.Unlock()
	if pause {
		h.wake() // Its slot is free.
	}
	actor := actorLocal
	if byHarness {
		actor = actorHarness
	}
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return tx.Journal(actor, procedure, req) }); err != nil {
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
	holdWhy := r.holdWhy
	r.holdWhy = ""
	h.mu.Unlock()
	t := r.task
	switch {
	case paused && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING:
		t.Status = planv1.TaskStatus_TASK_STATUS_PAUSED
		nowPb := timestamppb.New(h.now())
		reason := "pause"
		if strings.Contains(strings.ToLower(holdWhy), "preempt") {
			reason = "preemption"
		}
		if len(t.Runs) > 0 {
			last := t.Runs[len(t.Runs)-1]
			if last.EndTime == nil {
				last.EndTime = nowPb
				last.Reason = reason
			}
		}
		text := "paused: the worker holds still, and frees its slot"
		if holdWhy != "" {
			text = "paused: " + holdWhy
		}
		h.write(r, actorHarness, methodHold, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
	case !paused && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED:
		t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
		nowPb := timestamppb.New(h.now())
		var pausedFor time.Duration
		if len(t.Runs) > 0 && t.Runs[len(t.Runs)-1].GetEndTime() != nil {
			pausedFor = h.now().Sub(t.Runs[len(t.Runs)-1].GetEndTime().AsTime())
		}
		t.StartTime = nowPb
		if t.FirstStartTime == nil {
			t.FirstStartTime = nowPb
		}
		if len(t.Runs) > 0 && t.Runs[len(t.Runs)-1].EndTime == nil {
			t.Runs[len(t.Runs)-1].EndTime = nowPb
			if t.Runs[len(t.Runs)-1].Reason == "" {
				t.Runs[len(t.Runs)-1].Reason = "restart"
			}
		}
		t.Runs = append(t.Runs, &planv1.TaskRun{
			StartTime: nowPb,
		})
		text := "resumed"
		if holdWhy != "" {
			text = "resumed: " + holdWhy
		}
		h.write(r, actorHarness, methodHold, t, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
		h.rebaseAfterPause(r, pausedFor)
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

// lastEventsFrom is the position after which the task's last n stored events start: 0 when it has n or fewer.
func (h *Harness) lastEventsFrom(ctx context.Context, taskID string, n int) (int64, error) {
	var from int64
	seen := 0
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		seen++
		if seen > n {
			from = e.GetSeq()
			return false
		}
		return true
	})
	if err != nil {
		return 0, plan.Status(err)
	}
	return from, nil
}

// Stored sends the task's events stored after the position after, then returns: it follows no task, running or not.
func (h *Harness) Stored(ctx context.Context, taskID string, after int64, send func(*planv1.TaskEvent) error) error {
	if _, err := store.Get[*planv1.Task](ctx, h.store, taskID); err != nil {
		return plan.Status(err)
	}
	events, err := store.ListAfter[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": taskID},
		store.After{Field: "seq", From: after})
	if err != nil {
		return plan.Status(err)
	}
	for _, ev := range events {
		if err := send(ev); err != nil {
			return err
		}
	}
	return nil
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
		// Those past after only: a watcher let go, or following a task that starts, reads again what it lacks, not
		// the thousands of events of a long task (W504).
		events, err := store.ListAfter[*planv1.TaskEvent](ctx, h.store, store.Where{"task_id": taskID},
			store.After{Field: "seq", From: after})
		if err != nil {
			h.unsubscribe(taskID, ch)
			return plan.Status(err)
		}
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
					return nil //nolint:nilerr // Watching ends with its context: not an error.
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
		if err := tx.Put(task); err != nil {
			return err
		}
		return putEvents(ctx, tx, h.newEvent(id, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
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

// DefaultModel is the model a provider runs when no model was requested; empty when it has none.
func DefaultModel(p planv1.Provider) string {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_UNSPECIFIED:
		return "claude-sonnet-5-5"
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		return "gemini-3.8-flash-high"
	case planv1.Provider_PROVIDER_CODEX:
		return "gpt-5.5"
	default:
		return ""
	}
}

// workerKindOf returns the Summoning Circle worker kind for a task being spawned or inspected.
func workerKindOf(req *planv1.TaskServiceSpawnRequest, t *planv1.Task) planv1.WorkerKind {
	if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		return planv1.WorkerKind_WORKER_KIND_QUESTION
	}
	if t.GetReview() != nil {
		return planv1.WorkerKind_WORKER_KIND_REVIEW
	}
	if t.GetCorrection() != nil {
		return planv1.WorkerKind_WORKER_KIND_CORRECTION
	}
	k := t.GetKind()
	if req != nil && k == planv1.TaskKind_TASK_KIND_UNSPECIFIED {
		k = req.GetKind()
	}
	switch k {
	case planv1.TaskKind_TASK_KIND_INVESTIGATION:
		return planv1.WorkerKind_WORKER_KIND_INVESTIGATION
	case planv1.TaskKind_TASK_KIND_MOCKUP:
		return planv1.WorkerKind_WORKER_KIND_MOCKUP
	case planv1.TaskKind_TASK_KIND_IFRIT:
		return planv1.WorkerKind_WORKER_KIND_IFRIT
	default:
		return planv1.WorkerKind_WORKER_KIND_WORK
	}
}

func isClaudeModel(m string) bool      { return plan.IsClaudeModel(m) }
func isAntigravityModel(m string) bool { return plan.IsAntigravityModel(m) }
func isCodexModel(m string) bool       { return plan.IsCodexModel(m) }

// foreignModel reports whether model is a model identifier belonging to a provider other than p.
// Unknown or custom models are not considered foreign.
func foreignModel(p planv1.Provider, model string) bool {
	if model == "" || p == planv1.Provider_PROVIDER_FAKE {
		return false
	}
	if p == planv1.Provider_PROVIDER_WATCH {
		return true
	}
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_UNSPECIFIED:
		return isAntigravityModel(model) || isCodexModel(model)
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		return isClaudeModel(model) || isCodexModel(model)
	case planv1.Provider_PROVIDER_CODEX:
		return isClaudeModel(model) || isAntigravityModel(model)
	default:
		return false
	}
}

func isModelAlias(model string) bool {
	if model == "" {
		return false
	}
	for _, r := range model {
		if r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

func normalizeAlias(alias string) string {
	a := strings.ToLower(strings.TrimSpace(alias))
	if idx := strings.Index(a, "/"); idx != -1 {
		a = a[idx+1:]
	}
	a = strings.TrimPrefix(a, "claude-")
	return a
}

func (h *Harness) recordAlias(alias, model string) {
	if alias == "" || model == "" {
		return
	}
	model = strings.TrimSpace(model)
	h.aliasesMu.Lock()
	defer h.aliasesMu.Unlock()
	if h.aliases == nil {
		h.aliases = make(map[string]string)
	}
	h.aliases[normalizeAlias(alias)] = model
	h.aliases[strings.ToLower(strings.TrimSpace(alias))] = model
}

func (h *Harness) resolveAlias(alias string) (string, bool) {
	if alias == "" {
		return "", false
	}
	h.aliasesMu.RLock()
	defer h.aliasesMu.RUnlock()
	if h.aliases == nil {
		return "", false
	}
	norm := normalizeAlias(alias)
	if m, ok := h.aliases[norm]; ok {
		return m, true
	}
	m, ok := h.aliases[strings.ToLower(strings.TrimSpace(alias))]
	return m, ok
}

func (h *Harness) learnAliases(model string) {
	if model == "" || isModelAlias(model) {
		return
	}
	m := strings.ToLower(model)
	for _, tier := range []string{"sonnet", "opus", "haiku"} {
		if strings.Contains(m, tier) {
			h.recordAlias(tier, model)
			h.recordAlias("claude-"+tier, model)
		}
	}
}

// Caller returns the WorkerCaller if the peerPID or token matches any currently running worker.
func (h *Harness) Caller(peerPID int, token string) *plan.WorkerCaller {
	if peerPID <= 0 && token == "" {
		return nil
	}
	type cand struct {
		id     string
		wish   string
		pid    int
		cgroup string
		token  string
	}
	h.mu.Lock()
	cands := make([]cand, 0, len(h.runs))
	for _, r := range h.runs {
		if r == nil || r.task == nil {
			continue
		}
		cands = append(cands, cand{
			id:     r.id,
			wish:   r.wish,
			pid:    r.pid,
			cgroup: r.cgroup,
			token:  r.token,
		})
	}
	h.mu.Unlock()

	for _, c := range cands {
		if token != "" && c.token != "" && c.token == token {
			return &plan.WorkerCaller{TaskID: c.id, WishID: c.wish}
		}
		if peerPID > 0 && c.pid > 0 && isDescendant(peerPID, c.pid, c.cgroup) {
			return &plan.WorkerCaller{TaskID: c.id, WishID: c.wish}
		}
	}
	return nil
}
