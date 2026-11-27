// Package gate grants the gates: named resources (code generation, the local stack, an end-to-end run, a paid model
// run), granted only while the machine is not under pressure and has the memory the command peaked at. It also admits
// each command a worker's Bash runs that neither reads nor edits (Request.Shared, djinn gate hook): shared with the
// other commands, it waits only for a machine out of pressure and the memory its measured peak needs. The cost of a
// command, not its gate's name, says how it passes (Q17 B): a light command under a gate's name shares it, as many at
// once as the machine holds; a command measured heavy (Heavy) holds its gate alone and goes one at a time, whatever its
// gate: it also waits for the heavy command another task runs. A lit ifrit, or a request that names no command, holds
// its gate alone too. Djinn's own requests to merge a task's
// work, settle a merge or check a push go first, the first come first; then the others, the first wish of the rank
// first (Q26), then the work of its first azima (Task.rank). A holder keeps its gate until it gives it back, its
// connection ends, its process ends or the timeout it asked for passes: none is taken back for a request that goes
// first, nor for holding long (G5 of L04); one held past 3 times its usual hold, or an hour unmeasured, is told to the
// lead (Tasks.HoldsLong).
// A lit ifrit's gate has no timeout: it is held until the ifrit goes out or ends, at its out time at the latest (T51),
// and goes to no one first for it. A gate held outside a running worker takes a worker's slot.
package gate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
)

// Tasks is what the gates need from the tasks: to name a holder, to rank waiters by their wish and their azima, to tell a task's
// events, whether a task's worker runs, which holds its gates in its own slot, and whether a task is a lit ifrit, which
// holds its gates until it goes out or ends. The harness is one.
type Tasks interface {
	Describe(ctx context.Context, taskID string) (string, error)
	Ranks(ctx context.Context, taskIDs []string) map[string]int
	// Places gives the position of each task's azima among the azimas of its wish (Task.rank), from 0; a task part of
	// no azima, or unknown, is not in the map.
	Places(ctx context.Context, taskIDs []string) map[string]int
	Note(taskID string, ev harness.Event)
	Works(taskID string) bool
	// HoldsLong tells the lead of the task's wish that its holder holds a gate long, line saying which and how long:
	// nothing is taken back. It must not call the gates back.
	HoldsLong(taskID, line string)
	// Ifrit tells whether the task is an ifrit lit now, and when it goes out unless the developer comes back (zero:
	// not known yet); once it no longer is, why says why: "went out: …", "ended". A task that is no ifrit is not lit,
	// and has no why. It must not call the gates back.
	Ifrit(ctx context.Context, taskID string) (lit bool, out time.Time, why string)
}

// Machine is what the gates need from the machine: its pressure, its memory, and the policy that judges them. The
// monitor is one.
type Machine interface {
	Snapshot() machine.Snapshot
	Policy() machine.Policy
}

// Costs gives what a command was measured to cost, in its project: the task's, else the one holding dir; zero when
// never measured. Ask counts a run of the command asked under the gate, whether it then runs to its end or not.
// Record counts a run Djinn measured itself: an admitted command, which no djinn gate run waits for.
// machine.Peaks is one.
type Costs interface {
	Cost(ctx context.Context, taskID, dir, command string) machine.Cost
	Ask(ctx context.Context, gate, taskID, dir, command string)
	Record(ctx context.Context, run *machinev1.CommandServiceRecordRequest)
}

// Log keeps the intervals of the gates: Add one released, Since those released after cutoff, the first released
// first. machine.Intervals keeps them in the store, for three months; without one, the last 2,000 stay in memory.
type Log interface {
	Add(ctx context.Context, iv *machinev1.GateInterval)
	Since(ctx context.Context, cutoff time.Time) []*machinev1.GateInterval
}

// memoryLog is the last 2,000 intervals, in memory: the Log of gates given none.
type memoryLog struct {
	mu  sync.Mutex
	ivs []*machinev1.GateInterval
}

func (m *memoryLog) Add(_ context.Context, iv *machinev1.GateInterval) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ivs = append(m.ivs, iv)
	if len(m.ivs) > 2000 {
		m.ivs = m.ivs[len(m.ivs)-2000:]
	}
}

func (m *memoryLog) Since(_ context.Context, cutoff time.Time) []*machinev1.GateInterval {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*machinev1.GateInterval
	for _, iv := range m.ivs {
		if iv.GetReleaseTime() == nil || iv.GetReleaseTime().AsTime().After(cutoff) {
			out = append(out, iv)
		}
	}
	return out
}

// Read reads what the process pid uses, with every process it started: machine.ReadWorker.
type Read func(pid int) (machine.Group, error)

// A hold that asked for no timeout is never taken back while its holder lives: past LongTimes its usual hold, the
// 95th percentile of its command's measured holds, but never before MinLongHold, or past UnmeasuredLong when its
// command was never measured, its task's lead is told (Tasks.HoldsLong).
const (
	LongTimes      = 3
	MinLongHold    = 10 * time.Minute
	UnmeasuredLong = time.Hour
)

// MemoryWait is the longest a command waits for the memory its peak needs: past it, it is admitted all the same, and
// its worker is told why. A peak learned wrong, or a machine that never frees enough, never holds a worker for good
// (W139). On macOS, it bounds the wait of a heavy command at warning memory pressure (Policy.Warned) the same way.
const MemoryWait = 5 * time.Minute

// Commands is the name the admissions of the workers' commands go by (Request.Shared): in Held, Waiting, a task's
// events and the intervals of the gates.
const Commands = harness.AdmissionGate

// A command is heavy when it was measured past DefaultHeavyMemory of peak memory or DefaultHeavyCPU of CPU time,
// unless Heavy says otherwise (the window's settings).
const (
	DefaultHeavyMemory = 2 << 30
	DefaultHeavyCPU    = time.Minute
)

// Gates grants the gates. Its zero value is not usable: New makes one.
type Gates struct {
	machine Machine                             // nil: never under pressure, its memory unknown
	costs   Costs                               // nil: no command measured
	tasks   Tasks                               // nil: holders outside any task only
	tick    time.Duration                       // how often a waiter looks at the machine again, and a holder's process is looked at
	findBy  time.Duration                       // how long an admission looks for its command's process (Request.Find)
	every   time.Duration                       // how often a holder's process is read (Measure)
	read    Read                                // nil: holders not measured
	freed   func()                              // called when a gate is given or taken back (Freed)
	heavy   machine.Cost                        // a command measured past either is heavy (Heavy)
	minLong time.Duration                       // MinLongHold; tests shorten it
	noLong  time.Duration                       // UnmeasuredLong; tests shorten it
	memWait time.Duration                       // MemoryWait; tests shorten it
	granted func() time.Time                    // the time of a grant; nil: now (tests fix it)
	track   func(pid int, taskID string) func() // told of a worker's command holding a gate (Track); nil: none

	projectResolver func(taskID, dir string) string
	declared        func(taskID, dir string) Declared // the gates a project declares (Declare); nil: none
	log             Log                               // the intervals of the gates (Keep)

	mu     sync.Mutex
	held   map[string]*waiter // the gates held alone, by name
	shared map[*waiter]bool   // the gates held shared: the admissions, and the light commands under a gate's name
	queue  []*waiter          // in arrival order
	seq    int
}

// waiter is a holder of a gate, or one that waits for it.
type waiter struct {
	name, taskID, who, what string
	dir, project            string
	peak                    uint64                            // the command's highest measured peak memory; 0 when never measured
	shared                  bool                              // held with others: an admission, or a light command under a gate's name
	admission               bool                              // an admission (Request.Shared): it goes by Commands, its process found (Find)
	heavy                   bool                              // the command was measured heavy: it goes one at a time (Heavy)
	exclusive               bool                              // held alone under a gate its project declares: it waits for those that share it too
	label                   string                            // how a reason names the declared gate it is for, "stack (dolmen, .agents/djinn.yaml)"; "" for another
	find                    func(skip func(pid int) bool) int // finds the admitted command's process (Request.Find)
	lost                    bool                              // no process of its command was found; guarded by Gates.mu
	pid                     int                               // the holder's process; 0 when unknown
	first                   bool                              // Djinn's request for a merge or a push: it goes before the others (Request.First)
	ifrit                   bool                              // taken for a lit ifrit: held until it goes out or ends, with no timeout unless asked
	timeout                 time.Duration                     // the timeout it asked for (Request.Timeout); 0: none
	long                    time.Duration                     // how long it holds before its task's lead is told (LongTimes); 0: never
	usual                   time.Duration                     // its command's usual hold, the 95th percentile measured; 0: never measured
	timed                   bool                              // the holder asked for its timeout (Request.Timeout)
	seq                     int
	asked                   time.Time               // when the gate was requested
	since, until            time.Time               // when it was granted, and when it is taken back at the latest; guarded by Gates.mu (since is set once, at the grant, before granted closes: keep and give read it bare)
	out                     time.Time               // when the ifrit holding it goes out unless the developer comes back; zero: not known
	granted                 chan struct{}           // closed when the gate is granted
	changed                 chan struct{}           // the cause to wait changed
	done                    chan struct{}           // closed when the gate is given or taken back
	why, cause              string                  // guarded by Gates.mu
	stints                  []stint                 // each reason it waited for, in turn (Waited); guarded by Gates.mu
	waitCause               machinev1.GateWaitCause // what it waited for last; guarded by Gates.mu
	unmeasured              bool                    // its command had no measured cost of its own when it asked
	memory                  time.Time               // when it first waited for the memory; guarded by Gates.mu
	forced                  string                  // why it was granted past MemoryWait, "" when it was not; guarded by Gates.mu
	back                    string                  // why Djinn took the gate back; guarded by Gates.mu
	leaving                 bool                    // given or taken back, its task being told; guarded by Gates.mu
	use                     *planv1.Resources
	cpu                     time.Duration // the holder's CPU time at the last reading
	read                    time.Time     // when it was last read
	reads                   int           // how many times it was read
}

// New returns the gates of machine m (nil: never under pressure, its memory unknown), the commands' measured costs
// being costs (nil: none measured), the holders' tasks being tasks (nil: none known).
func New(m Machine, costs Costs, tasks Tasks) *Gates {
	return &Gates{
		machine: m, costs: costs, tasks: tasks, tick: time.Second, findBy: 3 * time.Second, held: map[string]*waiter{},
		shared: map[*waiter]bool{}, minLong: MinLongHold, noLong: UnmeasuredLong, memWait: MemoryWait,
		log:   &memoryLog{},
		heavy: machine.Cost{Peak: DefaultHeavyMemory, CPU: DefaultHeavyCPU},
	}
}

// Heavy sets when a command is heavy: measured past memory bytes of peak memory, or cpu of CPU time; 0 keeps the
// default of either. djinn up reads them in the window's settings.
func (g *Gates) Heavy(memory uint64, cpu time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if memory > 0 {
		g.heavy.Peak = memory
	}
	if cpu > 0 {
		g.heavy.CPU = cpu
	}
}

// heavyCost says whether a command that cost c is heavy. The caller holds g.mu.
func (g *Gates) heavyCost(c machine.Cost) bool {
	return c.Peak >= g.heavy.Peak || c.CPU >= g.heavy.CPU && c.CPU > 0
}

// Keep keeps the intervals of the gates in log: djinn up gives machine.Intervals, which keeps them in the store.
func (g *Gates) Keep(log Log) { g.log = log }

// Measure reads, every interval, what each holder whose process is known uses, with read: djinn up gives
// machine.ReadWorker, where the system lets it measure (machine.NotMeasured).
func (g *Gates) Measure(every time.Duration, read Read) { g.every, g.read = every, read }

// Freed calls f each time a gate is given or taken back: a slot may have freed. djinn up wakes the scheduler.
func (g *Gates) Freed(f func()) { g.freed = f }

// Track calls f with the process of each worker's command that holds a gate, djinn gate run, and its task, as it
// takes it; what f returns is called once the gate is given back. djinn up records them: none outlives it (W514).
func (g *Gates) Track(f func(pid int, taskID string) func()) { g.track = f }

// tracked tells the tracker of the holder pid of the task taskID; what it returns is called once the gate is given back.
func (g *Gates) tracked(pid int, taskID string) func() {
	if g.track == nil || pid <= 0 || taskID == "" {
		return func() {}
	}
	return g.track(pid, taskID)
}

// ProjectResolver sets the function that resolves a task and directory to a project name.
func (g *Gates) ProjectResolver(fn func(taskID, dir string) string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.projectResolver = fn
}

// Request is what a holder asks a gate for.
type Request struct {
	Name string
	// TaskID, when not empty, is the task the gate is taken for: its events say when it waits, takes and gives the
	// gate back.
	TaskID string
	// What says what the holder runs, in the folder Dir: a command measured in its project waits until the machine
	// has the memory it peaked at.
	What, Dir string
	// PID is the holder's process, 0 when unknown: it is measured while it holds the gate, and the gate goes back
	// once it ends.
	PID int
	// Timeout is how long the gate may be held: Djinn takes it back after that. 0: none, the gate held as long as its
	// holder lives, its task's lead told when it holds long (LongTimes); a lit ifrit holds its gates until it goes out
	// or ends.
	Timeout time.Duration
	// First tells that Djinn takes the gate to merge a task's work into its branch (its wish's or its azima's), to
	// settle such a merge, or to check a push: it blocks what comes after the task, so it goes before every other
	// request, the first come first, and never waits for one that came after it (Q26).
	First bool
	// Shared asks an admission rather than a gate, for the command a worker's Bash runs (djinn gate hook): Name is
	// left out, the admission goes by Commands. It waits only for a machine out of pressure and the memory its
	// measured peak needs, in the gates' order, and is held by as many as it admits; a command measured heavy waits
	// for the heavy command of another task. It needs PID or Find: the admission is held until that process ends.
	// A gate's name asked for a light command (What, not measured heavy) is shared the same way, without being asked:
	// it waits only for one that holds its name alone (a heavy command, a lit ifrit) and a merge that goes first.
	// A command a pattern of its project's gates names goes by that gate instead, name@project, alone or shared as
	// the pattern says (Declare).
	Shared bool
	// Beside tells that the command only reads what its gate guards (djinn gate run --beside): it shares the gate,
	// heavy or not, unless an alone pattern of its project names it. Without it, a command no pattern names holds a
	// declared gate alone.
	Beside bool
	// Find, when PID is 0, finds the process of the admitted command, leaving out those skip says, once it starts:
	// 0 while it is not found. The admission is given back when no process is found within a few seconds: its
	// command ran too short to be found, or did not run.
	Find func(skip func(pid int) bool) int
}

// stint is a reason a waiter waited for, from when: the reason without the time its holder had held, which a
// waiter told once goes stale as it waits on (W174).
type stint struct {
	text string
	kind machinev1.GateWaitCause
	from time.Time
}

// Hold is a gate held.
type Hold struct {
	g    *Gates
	w    *waiter
	once sync.Once
}

// Give gives the gate back; calling it again, or once Djinn took it back, does nothing.
func (h *Hold) Give() { h.once.Do(func() { h.g.give(h.w, "") }) }

// Done is closed once the gate is no longer held: given back, or taken back (TakenBack says why).
func (h *Hold) Done() <-chan struct{} { return h.w.done }

// Waited says what the holder waited for before the gate was granted, each reason in turn with how long it waited
// for it, its gate's label first: "stack (dolmen, .agents/djinn.yaml): behind W2: docker build ., which waits to hold
// it alone, shared by W1: task recette (10s), then held by W2: docker build . (11s)". "" when it did not wait.
func (h *Hold) Waited() string {
	h.g.mu.Lock()
	defer h.g.mu.Unlock()
	return h.w.waited()
}

// waited is Hold.Waited. The caller holds g.mu.
func (w *waiter) waited() string {
	if len(w.stints) == 0 && w.forced == "" {
		return ""
	}
	parts := make([]string, 0, len(w.stints))
	for i, s := range w.stints {
		end := w.since
		if i+1 < len(w.stints) {
			end = w.stints[i+1].from
		}
		if i == len(w.stints)-1 && w.forced != "" && s.kind == machinev1.GateWaitCause_GATE_WAIT_CAUSE_MEMORY {
			break // forced says how long it waited for the memory, and why.
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", s.text, max(end.Sub(s.from), 0).Round(time.Second)))
	}
	if w.forced != "" {
		parts = append(parts, w.forced)
	}
	if n := len(parts); n > maxStints {
		parts = slices.Concat(parts[:2], []string{fmt.Sprintf("%d more", n-4)}, parts[n-2:])
	}
	text := strings.Join(parts, ", then ")
	if w.label != "" {
		text = w.label + ": " + text
	}
	return text
}

// maxStints is how many reasons Waited names at most: past them, the first two and the last two, and how many between.
const maxStints = 5

// TakenBack says why Djinn took the gate back before its holder gave it: its process ended, it held it past its
// timeout, or its ifrit went out or ended; "" when it did not.
func (h *Hold) TakenBack() string {
	h.g.mu.Lock()
	defer h.g.mu.Unlock()
	return h.w.back
}

// Name is a gate's name as Djinn keeps it: names ignore case.
func Name(s string) string { return strings.ToLower(s) }

// Take waits until the gate is granted, and returns it held. waiting is called each time the reason to wait
// changes. Take fails when ctx ends first.
func (g *Gates) Take(ctx context.Context, r Request, waiting func(why string)) (*Hold, error) {
	name, taskID, what := Name(r.Name), r.TaskID, r.What
	if r.Shared {
		if r.PID <= 0 && r.Find == nil {
			return nil, errors.New("an admission needs the process of its command")
		}
		name = Commands
	}
	timeout := max(r.Timeout, 0)
	who := "a holder outside any task"
	if taskID != "" {
		if g.tasks == nil {
			return nil, errors.New("no task is known here")
		}
		var err error
		if who, err = g.tasks.Describe(ctx, taskID); err != nil {
			return nil, err
		}
	}
	// A lit ifrit's own gates, not Djinn's merges of its work: held until it goes out or ends.
	var ifrit bool
	var out time.Time
	if taskID != "" && !r.First && !r.Shared {
		ifrit, out, _ = g.tasks.Ifrit(ctx, taskID)
	}
	var cost machine.Cost
	if what != "" {
		who += ": " + what
		if g.costs != nil {
			cost = g.costs.Cost(ctx, taskID, r.Dir, what)
		}
	}
	g.mu.Lock()
	g.seq++
	heavy := g.heavyCost(cost)
	// A light command shares its gate's name (Q17 B), and so does one said beside (djinn gate run --beside): only a
	// heavy one, a lit ifrit's server, or a holder that says not what it runs holds it alone.
	shared := r.Shared || (what != "" && !heavy || r.Beside) && !ifrit
	g.mu.Unlock()
	// A gate the project declares: name@project, alone or shared as its patterns say, alone when none names the command
	// and it is not said beside; an ifrit's serve holds it alone.
	full, alone, label, declared := g.declaredGate(Request{
		TaskID: taskID, What: what, Dir: r.Dir, First: r.First, Shared: r.Shared, Beside: r.Beside,
	}, name)
	if declared {
		name, shared = full, !alone && !ifrit
	}
	if what != "" && g.costs != nil {
		g.costs.Ask(ctx, name, taskID, r.Dir, what)
	}
	g.mu.Lock()
	var proj string
	if g.projectResolver != nil {
		proj = g.projectResolver(taskID, r.Dir)
	}
	long, usual := g.noLong, time.Duration(0)
	if cost.Held > 0 && cost.Head == "" {
		long, usual = max(LongTimes*cost.Held, g.minLong), cost.Held
	}
	if timeout > 0 || ifrit {
		long = 0 // Taken back at its timeout, or once its ifrit goes out.
	}
	w := &waiter{
		name: name, taskID: taskID, who: who, what: what, dir: r.Dir, project: proj, peak: cost.Peak, pid: r.PID, first: r.First,
		shared: shared, admission: r.Shared, heavy: heavy, exclusive: declared && !shared, label: label, find: r.Find, unmeasured: what != "" && (cost.Peak == 0 && cost.CPU == 0 || cost.Head != ""),
		waitCause: machinev1.GateWaitCause_GATE_WAIT_CAUSE_NONE,
		ifrit:     ifrit, out: out, timeout: timeout, long: long, usual: usual, timed: timeout > 0, seq: g.seq,
		asked:   time.Now(),
		granted: make(chan struct{}), changed: make(chan struct{}, 1), done: make(chan struct{}),
	}
	g.queue = append(g.queue, w)
	g.mu.Unlock()
	g.note(w, "asked")

	tick := time.NewTicker(g.tick)
	defer tick.Stop()
	said := ""
	for {
		g.pass(ctx)
		select {
		case <-w.granted:
			g.mu.Lock()
			forced := w.forced
			g.mu.Unlock()
			if forced != "" {
				g.note(w, forced)
				if waiting != nil {
					waiting(forced)
				}
			}
			g.note(w, "taken")
			go g.keep(context.WithoutCancel(ctx), w) // It follows the hold, not the call that asked for it.
			return &Hold{g: g, w: w}, nil
		case <-w.changed:
			g.mu.Lock()
			why := w.why
			g.mu.Unlock()
			if why != said && why != "" {
				said = why
				g.note(w, "waiting: "+why)
				if waiting != nil {
					waiting(why)
				}
			}
		case <-tick.C:
		case <-ctx.Done():
			g.mu.Lock()
			if g.held[name] == w {
				// Granted as the caller left: give it back at once.
				delete(g.held, name)
			}
			delete(g.shared, w) // Likewise for one held shared.
			g.queue = slices.DeleteFunc(g.queue, func(o *waiter) bool { return o == w })
			g.mu.Unlock()
			g.pass(context.WithoutCancel(ctx))
			return nil, ctx.Err()
		}
	}
}

// give gives the gate back, or takes it back saying why, and grants it to the next waiter. Its task is told before
// the gate frees: no waiter, whose own pass grants it, takes it before then.
func (g *Gates) give(w *waiter, why string) {
	g.mu.Lock()
	if !g.holds(w) || w.leaving {
		g.mu.Unlock()
		return // Given or taken back already.
	}
	w.leaving = true
	g.mu.Unlock()
	switch {
	case why != "":
		g.note(w, "taken back: "+why)
	case w.lost:
		g.note(w, "given back: its command ran too short to be found, or did not run")
	default:
		g.note(w, "given back after "+time.Since(w.since).Round(time.Second).String())
	}
	outside := g.outside(w.taskID)
	g.mu.Lock()
	if g.held[w.name] == w {
		delete(g.held, w.name)
	}
	delete(g.shared, w)
	w.back = why
	close(w.done)
	run := g.measured(w, why)
	var iv *machinev1.GateInterval
	if !w.since.IsZero() {
		iv = g.interval(w, outside)
		iv.ReleaseTime = timestamppb.Now()
	}
	g.mu.Unlock()
	if iv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		g.log.Add(ctx, iv)
		cancel()
	}
	if run != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		g.costs.Record(ctx, run)
		cancel()
	}
	g.pass(context.Background())
	if g.freed != nil {
		g.freed()
	}
}

// holds says whether w holds its gate or its admission now. The caller holds g.mu.
func (g *Gates) holds(w *waiter) bool {
	if w.shared {
		return g.shared[w]
	}
	return g.held[w.name] == w
}

// Shown says whether the interval iv goes in the series of the gates unless all the admissions are asked for: every
// gate, shared or held alone, and an admission only when it waited a second or more or its command is heavy; the many
// that pass at once would hide the gates from the Hourglass's frieze. Every interval is kept all the same (Log).
func Shown(iv *machinev1.GateInterval) bool {
	return iv.GetMode() != machinev1.GateMode_GATE_MODE_SHARED || iv.GetGate() != Commands || iv.GetHeavy() ||
		iv.GetGrantTime().AsTime().Sub(iv.GetAskedTime().AsTime()) >= time.Second
}

// interval is the hold w as the intervals of the gates keep it, not released yet; outside says whether it was held
// outside a running worker. The caller holds g.mu.
func (g *Gates) interval(w *waiter, outside bool) *machinev1.GateInterval {
	project := w.project
	if project == "" && g.projectResolver != nil {
		project = g.projectResolver(w.taskID, w.dir)
	}
	mode := machinev1.GateMode_GATE_MODE_EXCLUSIVE
	if w.shared {
		mode = machinev1.GateMode_GATE_MODE_SHARED
	}
	return &machinev1.GateInterval{
		Gate: w.name, Command: w.what, Project: project, TaskId: w.taskID,
		AskedTime: timestamppb.New(w.asked), GrantTime: timestamppb.New(w.since),
		// What it waited for, each holder in turn with its own time, as djinn gate hook reads it: not the last reason as
		// the waiter was told it, its holder's time frozen when the cause changed ("held by W2 …, for 0s", W175).
		Mode: mode, Heavy: w.heavy, WaitCause: w.waitCause, WaitReason: w.waited(),
		PeakMemoryBytes: w.use.GetPeakMemoryBytes(), Unmeasured: w.unmeasured, Outside: outside && (!w.shared || w.heavy),
	}
}

// measured is the run of an admission given back as its command ended, as Djinn read it, to count in what the
// command costs; nil when there is none to count: a gate, shared or not (djinn gate run counts its own), an admission taken back, or
// one whose command ended before a reading after the first (its shell is read each second for its first readings,
// readEvery, then every few seconds: a command under a second goes unmeasured). Its peak is the highest reading, not
// the kernel's: the shell is the agent's child, not Djinn's, and no wait4 gives Djinn its rusage. The caller holds
// g.mu.
func (g *Gates) measured(w *waiter, why string) *machinev1.CommandServiceRecordRequest {
	if !w.admission || why != "" || g.costs == nil || w.what == "" || w.reads < 2 || w.since.IsZero() {
		return nil
	}
	held := time.Since(w.since)
	return &machinev1.CommandServiceRecordRequest{
		TaskId: w.taskID, Directory: w.dir, Command: w.what, CpuSeconds: w.cpu.Seconds(), Seconds: held.Seconds(),
		PeakMemoryBytes: w.use.GetPeakMemoryBytes(), ExitCode: -1, WaitSeconds: w.since.Sub(w.asked).Seconds(),
		HeldSeconds: held.Seconds(), Gate: w.name,
	}
}

// keep follows a holder while it holds its gate: it reads what its process uses, and takes the gate back once that
// process ends or the timeout it asked for passes. A gate is never taken back from a holder that lives for holding it
// long: past w.long its task's lead is told, once. A lit ifrit's gate has no timeout of its own: it follows the
// ifrit's out time, pushed back by each action of the developer, and is taken back once the ifrit goes out or ends.
func (g *Gates) keep(ctx context.Context, w *waiter) {
	defer logx.Crash("gate keep", "gate", w.name, "task", w.taskID)
	tick := time.NewTicker(g.tick)
	defer tick.Stop()
	var limit, long <-chan time.Time
	if w.timed {
		timer := time.NewTimer(time.Until(w.since.Add(w.timeout)))
		defer timer.Stop()
		limit = timer.C
	}
	if w.long > 0 {
		timer := time.NewTimer(time.Until(w.since.Add(w.long)))
		defer timer.Stop()
		long = timer.C
	}
	g.measure(w)
	if w.pid == 0 && w.find != nil && !g.found(w) {
		return
	}
	for {
		select {
		case <-w.done:
			return
		case <-limit:
			g.give(w, "held past its timeout of "+w.timeout.String())
			return
		case <-long:
			g.holdsLong(w)
		case <-tick.C:
			if w.pid != 0 && !alive(w.pid) {
				if w.admission {
					g.give(w, "") // Its command ended: an admission lasts that long.
				} else {
					g.give(w, fmt.Sprintf("its process %d ended", w.pid))
				}
				return
			}
			if w.ifrit {
				lit, out, why := g.tasks.Ifrit(ctx, w.taskID)
				if !lit {
					g.give(w, "its ifrit "+cmp.Or(why, "ended"))
					return
				}
				g.mu.Lock()
				w.out = out
				w.until = w.latest()
				g.mu.Unlock()
			}
			if time.Since(w.read) >= g.readEvery(w) {
				g.measure(w)
			}
		}
	}
}

// fastReads is how many first readings of a holder come each tick rather than each Measure interval: a command of a
// few seconds is read twice or more, which its cost needs (measured); one that lasts reads every interval after.
const fastReads = 5

// readEvery is how often the holder w is read now: each tick for its first fastReads readings, then each interval.
func (g *Gates) readEvery(w *waiter) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w.reads < fastReads {
		return min(g.every, g.tick)
	}
	return g.every
}

// holdsLong tells w's task, and its lead, that w holds its gate long; a holder outside any task is logged. Nothing is
// taken back.
func (g *Gates) holdsLong(w *waiter) {
	held := rounded(time.Since(w.since))
	usual := "its command never measured"
	if w.usual > 0 {
		usual = fmt.Sprintf("past %d times its usual hold of %s", LongTimes, rounded(w.usual))
	}
	g.note(w, fmt.Sprintf("held for %s, %s: nothing is taken back", held, usual))
	if w.taskID == "" || g.tasks == nil {
		logx.L().Warn("a gate held long", "gate", w.name, "holder", w.who, "held", held)
		return
	}
	what := "gate " + w.name
	if w.admission {
		what = "its admitted command"
	}
	line := fmt.Sprintf("Djinn: %s has held %s for %s, %s; nothing is taken back: see whether it hangs.",
		w.who, what, held, usual)
	g.tasks.HoldsLong(w.taskID, line)
}

// rounded is d to the second, to the millisecond under a second.
func rounded(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(time.Second)
}

// found looks for the process of the admitted command w for findBy, often: a command of a second is found before it
// ends. It says whether it found it; when it did not, the admission is given back.
func (g *Gates) found(w *waiter) bool {
	every := min(g.tick, 100*time.Millisecond)
	tick := time.NewTicker(every)
	defer tick.Stop()
	deadline := time.Now().Add(g.findBy)
	for {
		pid := w.find(func(pid int) bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			for h := range g.shared {
				if h.pid == pid {
					return true // Another admission's command.
				}
			}
			for _, h := range g.held {
				if h.pid == pid {
					return true // One that holds a declared gate alone.
				}
			}
			return false
		})
		if pid > 0 {
			g.mu.Lock()
			w.pid = pid
			g.mu.Unlock()
			g.measure(w)
			return true
		}
		if time.Now().After(deadline) {
			g.mu.Lock()
			w.lost = true
			g.mu.Unlock()
			g.give(w, "")
			return false
		}
		select {
		case <-w.done:
			return false
		case <-tick.C:
		}
	}
}

// latest is when the holder gives its gate back at the latest: the end of the timeout it asked for, zero when none;
// for a lit ifrit, its out time, or the timeout it asked for if sooner, zero when neither is known. The caller holds
// g.mu, or w is not granted yet.
func (w *waiter) latest() time.Time {
	var end time.Time
	if w.timed {
		end = w.since.Add(w.timeout)
	}
	if !w.ifrit {
		return end
	}
	if w.timed && (w.out.IsZero() || end.Before(w.out)) {
		return end
	}
	return w.out
}

// measure reads what the holder's process uses, and keeps it with its peaks.
func (g *Gates) measure(w *waiter) {
	if g.read == nil || w.pid == 0 {
		return
	}
	got, err := g.read(w.pid)
	now := time.Now()
	if err != nil || got.Processes == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var cpu float64
	if d := now.Sub(w.read); !w.read.IsZero() && d > 0 && got.CPU >= w.cpu {
		cpu = math.Round(float64(got.CPU-w.cpu)/float64(d)*1000) / 10
	}
	w.cpu, w.read = got.CPU, now
	w.reads++
	w.use = &planv1.Resources{
		CpuPercent: cpu, MemoryBytes: got.Memory, Processes: int32(got.Processes),
		PeakCpuPercent: max(w.use.GetPeakCpuPercent(), cpu), PeakMemoryBytes: max(w.use.GetPeakMemoryBytes(), got.Memory),
		ReadTime: timestamppb.New(now),
	}
}

// outside says whether a holder holds its gate outside a running worker: then the gate takes a slot of its own.
// The caller does not hold g.mu: the tasks have their own lock.
func (g *Gates) outside(taskID string) bool {
	return taskID == "" || g.tasks == nil || !g.tasks.Works(taskID)
}

// Outside counts the gates held outside any running worker, by a person's terminal, a lead, a script or Djinn's
// merges and pushes, and of them the merges' and pushes' (Request.First): each takes a worker's slot, so no new worker
// starts on it while it is held. A worker's gate is in the worker's own slot. A gate held shared, an admission or a
// light command under a gate's name, takes no slot (G9 of L04): only one held alone does, and a heavy admission.
func (g *Gates) Outside() (n, merges int) {
	g.mu.Lock()
	holders := make([]*waiter, 0, len(g.held))
	for _, h := range g.held {
		holders = append(holders, h)
	}
	for h := range g.shared {
		if h.heavy {
			holders = append(holders, h)
		}
	}
	g.mu.Unlock()
	for _, h := range holders {
		if g.outside(h.taskID) {
			n++
			if h.first {
				merges++
			}
		}
	}
	return n, merges
}

// pass grants each free gate to its first waiter, unless the machine is under pressure or lacks the memory the
// waiter's command peaked at; it tells every other waiter why it waits. Djinn's merges and pushes come first, the
// first come first; then the others, the first wish of the rank first, then the first azima of its rank, then the first
// come. A waiter for a merge or a push that cannot have its gate yet keeps it from the others; within a class, one the
// memory holds goes before one it does not. A gate held shared waits in the same order: an admission (Request.Shared)
// for the pressure and the memory only, a light command under a gate's name also for one that holds that name alone
// and for a merge that waits for it. A heavy command, gate or admission, also waits while another task's heavy command
// holds its own, and a merge or a push that waits so keeps the turn from the heavy commands of the workers; one under a
// gate's name does not wait for the light commands that share it. On macOS, the memory available holds no command
// (Policy.Room): the pressure level does. Normal admits; at warning, a heavy command beside a measured holder waits
// (Policy.Warned), as long as for the memory at most; at critical, every waiter waits (Policy.GatePressure).
func (g *Gates) pass(ctx context.Context) {
	var snap machine.Snapshot
	var policy machine.Policy
	if g.machine != nil {
		snap, policy = g.machine.Snapshot(), g.machine.Policy()
	}
	pressure := policy.GatePressure(snap)
	g.mu.Lock()
	var ids []string
	for _, w := range g.queue {
		if w.taskID != "" && !w.first {
			ids = append(ids, w.taskID)
		}
	}
	g.mu.Unlock()
	ranks, places := map[string]int{}, map[string]int{}
	if g.tasks != nil && len(ids) > 0 {
		ranks, places = g.tasks.Ranks(ctx, ids), g.tasks.Places(ctx, ids)
	}
	rank := func(w *waiter) int {
		if r, ok := ranks[w.taskID]; ok {
			return r
		}
		return math.MaxInt // Outside any task, or unknown: after the ranked wishes.
	}
	place := func(w *waiter) int {
		if p, ok := places[w.taskID]; ok {
			return p
		}
		return math.MaxInt // Part of no azima: after the ranked azimas of its wish.
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	var held uint64
	for _, h := range g.held {
		held += h.peak
	}
	for h := range g.shared {
		held += h.peak
	}
	queue := slices.Clone(g.queue)
	slices.SortStableFunc(queue, func(a, b *waiter) int {
		switch {
		case a.first != b.first:
			if a.first {
				return -1
			}
			return 1
		case a.first:
			return cmp.Compare(a.seq, b.seq)
		}
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(place(a), place(b)), cmp.Compare(a.seq, b.seq))
	})
	// The merges and pushes that wait for each gate in this pass, and why the first of them waits; heavyTurn stands
	// for the turn of the heavy commands.
	const heavyTurn = "\x00heavy"
	merges, mergeWhy, mergeBase, mergeCause := map[string]int{}, map[string]string{}, map[string]string{}, map[string]string{}
	// The first come of those that wait to hold a declared gate alone, by name: who comes after it to share it goes
	// behind it.
	aloneNext := map[string]*waiter{}
	for _, w := range g.queue {
		if o := aloneNext[w.name]; w.exclusive && (o == nil || w.seq < o.seq) {
			aloneNext[w.name] = w
		}
	}
	for _, w := range queue {
		// why is told the waiter now; base is the same without the time its holder has held, kept for Waited.
		var why, base, cause string
		var kind machinev1.GateWaitCause
		h := g.held[w.name]
		if w.name == Commands {
			h = nil
		}
		var sharer, ahead *waiter
		if h == nil && w.exclusive {
			sharer = g.sharer(w)
		}
		if a := aloneNext[w.name]; h == nil && w.shared && a != nil && a.seq < w.seq {
			ahead = a
		}
		heavy := g.heavyHolder(w)
		switch {
		case h != nil:
			base, cause = "held by "+h.who, "held by "+h.who
			why = fmt.Sprintf("%s, for %s", base, time.Since(h.since).Round(time.Second))
			kind = machinev1.GateWaitCause_GATE_WAIT_CAUSE_HELD
			if h.ifrit {
				// Its out time moves with each action of the developer: the waiter is told the one known as the cause changed.
				why, cause = "held by ifrit "+h.who+"; released when you leave", "held by ifrit "+h.who
				if !h.until.IsZero() {
					why += ", at " + harness.ClockText(h.until, time.Now()) + " at the latest"
				}
				base = why
			}
			if n := merges[w.name]; !w.first && n > 0 {
				then := fmt.Sprintf(", then %d merges or pushes go first", n)
				if n == 1 {
					then = ", then 1 merge or push goes first"
				}
				why, base, cause = why+then, base+then, cause+then
			}
		case sharer != nil:
			base = "shared by " + sharer.who
			why = fmt.Sprintf("%s, for %s", base, time.Since(sharer.since).Round(time.Second))
			cause, kind = base, machinev1.GateWaitCause_GATE_WAIT_CAUSE_HELD
		case ahead != nil:
			// Named with what the one ahead waits for: the gate's holder, or the first that shares it.
			why = "behind " + ahead.who + ", which waits to hold it alone"
			cause, kind = "behind "+ahead.who, machinev1.GateWaitCause_GATE_WAIT_CAUSE_HELD
			if s := g.sharer(ahead); s != nil {
				why, cause = why+", shared by "+s.who, cause+", shared by "+s.who
			}
			base = why
		case pressure != "":
			why = "the machine is under pressure: " + pressure
			base, cause, kind = why, why, machinev1.GateWaitCause_GATE_WAIT_CAUSE_PRESSURE
		case !w.first && w.name != Commands && merges[w.name] > 0:
			why = "a merge or a push goes first: " + mergeWhy[w.name]
			base, cause, kind = "a merge or a push goes first: "+mergeBase[w.name], "merge first: "+mergeCause[w.name], machinev1.GateWaitCause_GATE_WAIT_CAUSE_MERGE_FIRST
		case heavy != nil:
			base = "heavy commands go one at a time: " + heavy.who + " runs one"
			why = fmt.Sprintf("%s, for %s", base, time.Since(heavy.since).Round(time.Second))
			cause, kind = "heavy: "+heavy.who, machinev1.GateWaitCause_GATE_WAIT_CAUSE_HEAVY
		case w.heavy && !w.first && merges[heavyTurn] > 0:
			why = "a merge or a push goes first: " + mergeWhy[heavyTurn]
			base, cause, kind = "a merge or a push goes first: "+mergeBase[heavyTurn], "merge first: "+mergeCause[heavyTurn], machinev1.GateWaitCause_GATE_WAIT_CAUSE_MERGE_FIRST
		default:
			why = policy.Room(snap, w.what, w.peak, held)
			if why == "" && w.heavy {
				why = policy.Warned(snap, w.what, held)
			}
			if why != "" {
				if w.memory.IsZero() {
					w.memory = time.Now()
				}
				if time.Since(w.memory) < g.memWait {
					base, cause, kind = why, "memory", machinev1.GateWaitCause_GATE_WAIT_CAUSE_MEMORY
					break
				}
				w.forced = fmt.Sprintf("admitted after %s waiting for memory, the most a command waits for it: %s",
					g.memWait, why)
			}
			held += w.peak
			w.since = time.Now()
			if g.granted != nil {
				w.since = g.granted()
			}
			w.until = w.latest()
			if w.shared {
				g.shared[w] = true
			} else {
				g.held[w.name] = w
			}
			g.queue = slices.DeleteFunc(g.queue, func(o *waiter) bool { return o == w })
			close(w.granted)
			continue
		}
		if w.first {
			turns := []string{w.name}
			if w.heavy {
				turns = append(turns, heavyTurn)
			}
			for _, turn := range turns {
				if merges[turn] == 0 {
					mergeWhy[turn], mergeBase[turn], mergeCause[turn] = why, base, cause
				}
				merges[turn]++
			}
			why = "a merge or a push, served before the workers: " + why
			base = "a merge or a push, served before the workers: " + base
		}
		if w.label != "" {
			why = w.label + ": " + why // The worker reads which gate, of which project, declared where.
		}
		// The holder's time and the memory free change every second: tell the waiter only when the cause changes.
		w.waitCause = kind
		if cause != w.cause {
			w.why, w.cause = why, cause
			w.wait(base, kind)
			select {
			case w.changed <- struct{}{}:
			default:
			}
		}
	}
}

// wait keeps the reason text, of kind, that w waits for from now, for Waited: the pressure and the memory, whose
// figures change as it waits, keep the stint they started and the last figures. The caller holds g.mu.
func (w *waiter) wait(text string, kind machinev1.GateWaitCause) {
	if n := len(w.stints); n > 0 {
		last := &w.stints[n-1]
		if last.text == text || last.kind == kind && (kind == machinev1.GateWaitCause_GATE_WAIT_CAUSE_PRESSURE ||
			kind == machinev1.GateWaitCause_GATE_WAIT_CAUSE_MEMORY) {
			last.text = text
			return
		}
	}
	w.stints = append(w.stints, stint{text: text, kind: kind, from: time.Now()})
}

// heavyHolder is the heavy command w waits for, when w is heavy: one of another task, or of no task, that holds its
// gate or its admission; nil when none does. A task's own heavy commands never wait for each other: one of them may
// run the other (a worker's go tool task check that runs djinn gate run). The caller holds g.mu.
func (g *Gates) heavyHolder(w *waiter) *waiter {
	if !w.heavy {
		return nil
	}
	var found *waiter
	look := func(h *waiter) {
		if h.heavy && (w.taskID == "" || h.taskID != w.taskID) && (found == nil || h.since.Before(found.since)) {
			found = h
		}
	}
	for _, h := range g.held {
		look(h)
	}
	for h := range g.shared {
		look(h)
	}
	return found
}

// sharer is the first granted of those that share the declared gate w waits to hold alone, leaving out w's own task's:
// one of its commands may run the other (a beside command whose script runs djinn gate run with the gate), and would
// wait for itself. nil when none does. The caller holds g.mu.
func (g *Gates) sharer(w *waiter) *waiter {
	var found *waiter
	for h := range g.shared {
		if h.name == w.name && (w.taskID == "" || h.taskID != w.taskID) && (found == nil || h.since.Before(found.since)) {
			found = h
		}
	}
	return found
}

// note tells the waiter's task, if any, what happens to its gate.
func (g *Gates) note(w *waiter, text string) {
	if w.taskID == "" || g.tasks == nil {
		return
	}
	g.tasks.Note(w.taskID, harness.Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: "gate " + w.name + ": " + text})
}

// Held gives the names of the gates the task holds now, alone or shared, in order, Commands among them while one of its
// commands is admitted: its worker is neither paused, nor stopped for a slot, nor judged silent meanwhile.
func (g *Gates) Held(taskID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for name, h := range g.held {
		if taskID != "" && h.taskID == taskID {
			out = append(out, name)
		}
	}
	for h := range g.shared {
		if taskID != "" && h.taskID == taskID && !slices.Contains(out, h.name) {
			out = append(out, h.name)
		}
	}
	slices.Sort(out)
	return out
}

// Waiting gives the names of the gates the task waits for now, in order, Commands among them while one of its commands
// waits for its admission.
func (g *Gates) Waiting(taskID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, w := range g.queue {
		if taskID != "" && w.taskID == taskID && !slices.Contains(out, w.name) {
			out = append(out, w.name)
		}
	}
	slices.Sort(out)
	return out
}

// IfritWaits names, by the task of each lit ifrit, who waits for a gate it holds, the first come first: "W15 (Its
// title): go tool task e2e, for stack". The lead's brief says it, to offer the developer to end the ifrit.
func (g *Gates) IfritWaits() map[string][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string][]string{}
	for _, w := range g.queue {
		if h := g.held[w.name]; h != nil && h.ifrit {
			out[h.taskID] = append(out[h.taskID], w.who+", for "+w.name)
		}
	}
	return out
}

// State is a gate held or waited for.
type State struct {
	Name, Holder, HolderTaskID string
	Project                    string    // the holder's project, by name; "" when none
	WaitingProjects            []string  // the waiters' projects, by name, in the order of Waiting; "" for one of none
	Since, Until               time.Time // Until: when it is taken back at the latest; zero when not known
	Waiting                    []string
	WaitingTaskIDs             []string          // the tasks of the waiters, in the same order; a waiter outside any task left out
	TakesSlot                  bool              // held alone outside a running worker
	Shared                     int               // how many light commands share it; Holder is the first of them unless one holds it alone
	Sharers                    []string          // who share it, each with what it runs, the first granted first
	SharerTaskIDs              []string          // the tasks of the sharers, in the same order; a sharer outside any task left out
	SharerProjects             []string          // the sharers' projects, by name, in the order of Sharers; "" for one of none
	Ifrit                      bool              // held by a lit ifrit, until it goes out or ends
	Use                        *planv1.Resources // what the holder's process uses; nil when not read
}

// List gives the gates held or waited for, by name, alone or shared; the admissions are not listed.
func (g *Gates) List() []State {
	g.mu.Lock()
	byName := map[string]*State{}
	get := func(name string) *State {
		if byName[name] == nil {
			byName[name] = &State{Name: name}
		}
		return byName[name]
	}
	for name, h := range g.held {
		s := get(name)
		s.Holder, s.HolderTaskID, s.Project, s.Since, s.Until, s.Use, s.Ifrit = h.who, h.taskID, h.project, h.since, h.until, h.use, h.ifrit
	}
	alone := map[string]bool{}
	for name := range g.held {
		alone[name] = true
	}
	// Each name's light commands, the first granted first; equal grant times (a coarse clock, Windows) go by arrival.
	sharers := map[string][]*waiter{}
	for h := range g.shared {
		if h.name != Commands {
			sharers[h.name] = append(sharers[h.name], h)
		}
	}
	for name, hs := range sharers {
		slices.SortFunc(hs, func(a, b *waiter) int {
			if c := a.since.Compare(b.since); c != 0 {
				return c
			}
			return cmp.Compare(a.seq, b.seq)
		})
		s := get(name)
		s.Shared = len(hs)
		for _, h := range hs {
			s.Sharers = append(s.Sharers, h.who)
			s.SharerProjects = append(s.SharerProjects, h.project)
			if h.taskID != "" {
				s.SharerTaskIDs = append(s.SharerTaskIDs, h.taskID)
			}
		}
		if h := hs[0]; !alone[name] {
			s.Holder, s.HolderTaskID, s.Project, s.Since, s.Until, s.Use = h.who, h.taskID, h.project, h.since, h.until, h.use
		}
	}
	for _, w := range g.queue {
		if w.name == Commands {
			continue // An admission is no gate to list: Held and Waiting tell its task's.
		}
		s := get(w.name)
		s.Waiting = append(s.Waiting, w.who)
		s.WaitingProjects = append(s.WaitingProjects, w.project)
		if w.taskID != "" {
			s.WaitingTaskIDs = append(s.WaitingTaskIDs, w.taskID)
		}
	}
	g.mu.Unlock()
	out := make([]State, 0, len(byName))
	for _, s := range byName {
		s.TakesSlot = alone[s.Name] && g.outside(s.HolderTaskID)
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Intervals gives the intervals of the gates released after cutoff, the first released first, then those held now:
// all of them when all is true, else those Shown says.
func (g *Gates) Intervals(cutoff time.Time, all bool) []*machinev1.GateInterval {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []*machinev1.GateInterval
	for _, iv := range g.log.Since(ctx, cutoff) {
		if all || Shown(iv) {
			out = append(out, iv)
		}
	}
	g.mu.Lock()
	holders := slices.Collect(maps.Values(g.held))
	for h := range g.shared {
		holders = append(holders, h)
	}
	g.mu.Unlock()
	outside := make([]bool, len(holders))
	for i, h := range holders {
		outside[i] = g.outside(h.taskID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, h := range holders {
		if h.since.IsZero() || !g.holds(h) {
			continue
		}
		if iv := g.interval(h, outside[i]); all || Shown(iv) {
			out = append(out, iv)
		}
	}
	return out
}
