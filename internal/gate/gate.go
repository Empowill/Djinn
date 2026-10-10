// Package gate grants the gates: shared resources (code generation, the local stack, an end-to-end run, a paid
// model run) that one holder uses at a time, granted only while the machine is not under pressure and has the memory
// the command peaked at, the first wish of the rank first. A holder keeps its gate until it gives it back, its
// connection ends, its process ends or its timeout passes. A gate held outside a running worker takes a worker's
// slot.
package gate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
)

// Tasks is what the gates need from the tasks: to name a holder, to rank waiters by their wish, to tell a task's
// events, and whether a task's worker runs, which holds its gates in its own slot. The harness is one.
type Tasks interface {
	Describe(ctx context.Context, taskID string) (string, error)
	Ranks(ctx context.Context, taskIDs []string) map[string]int
	Note(taskID string, ev harness.Event)
	Works(taskID string) bool
}

// Machine is what the gates need from the machine: its pressure, its memory, and the policy that judges them. The
// monitor is one.
type Machine interface {
	Snapshot() machine.Snapshot
	Policy() machine.Policy
}

// Costs gives the highest peak memory a command was measured at, in its project: the task's, else the one holding
// dir; 0 when never measured. machine.Peaks is one.
type Costs interface {
	Peak(ctx context.Context, taskID, dir, command string) uint64
}

// Read reads what the process pid uses, with every process it started: machine.ReadWorker.
type Read func(pid int) (machine.Group, error)

// DefaultTimeout is how long a gate may be held when its holder states no timeout.
const DefaultTimeout = time.Hour

// Gates grants the gates. Its zero value is not usable: New makes one.
type Gates struct {
	machine Machine       // nil: never under pressure, its memory unknown
	costs   Costs         // nil: no command measured
	tasks   Tasks         // nil: holders outside any task only
	tick    time.Duration // how often a waiter looks at the machine again, and a holder's process is looked at
	every   time.Duration // how often a holder's process is read (Measure)
	read    Read          // nil: holders not measured
	freed   func()        // called when a gate is given or taken back (Freed)

	mu    sync.Mutex
	held  map[string]*waiter // by gate name
	queue []*waiter          // in arrival order
	seq   int
}

// waiter is a holder of a gate, or one that waits for it.
type waiter struct {
	name, taskID, who, what string
	peak                    uint64 // the command's highest measured peak memory; 0 when never measured
	pid                     int    // the holder's process; 0 when unknown
	timeout                 time.Duration
	seq                     int
	since, until            time.Time     // when it was granted, and when it is taken back
	granted                 chan struct{} // closed when the gate is granted
	changed                 chan struct{} // the cause to wait changed
	done                    chan struct{} // closed when the gate is given or taken back
	why, cause              string        // guarded by Gates.mu
	back                    string        // why Djinn took the gate back; guarded by Gates.mu
	use                     *planv1.Resources
	cpu                     time.Duration // the holder's CPU time at the last reading
	read                    time.Time     // when it was last read
}

// New returns the gates of machine m (nil: never under pressure, its memory unknown), the commands' measured costs
// being costs (nil: none measured), the holders' tasks being tasks (nil: none known).
func New(m Machine, costs Costs, tasks Tasks) *Gates {
	return &Gates{machine: m, costs: costs, tasks: tasks, tick: time.Second, held: map[string]*waiter{}}
}

// Measure reads, every interval, what each holder whose process is known uses, with read: djinn up gives
// machine.ReadWorker, where the system lets it measure (machine.NotMeasured).
func (g *Gates) Measure(every time.Duration, read Read) { g.every, g.read = every, read }

// Freed calls f each time a gate is given or taken back: a slot may have freed. djinn up wakes the scheduler.
func (g *Gates) Freed(f func()) { g.freed = f }

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
	// Timeout is how long the gate may be held: Djinn takes it back after that. 0: DefaultTimeout.
	Timeout time.Duration
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

// TakenBack says why Djinn took the gate back before its holder gave it: its process ended, or it held it past its
// timeout; "" when it did not.
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
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
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
	var peak uint64
	if what != "" {
		who += ": " + what
		if g.costs != nil {
			peak = g.costs.Peak(ctx, taskID, r.Dir, what)
		}
	}
	g.mu.Lock()
	g.seq++
	w := &waiter{
		name: name, taskID: taskID, who: who, what: what, peak: peak, pid: r.PID, timeout: timeout, seq: g.seq,
		granted: make(chan struct{}), changed: make(chan struct{}, 1), done: make(chan struct{}),
	}
	g.queue = append(g.queue, w)
	g.mu.Unlock()

	tick := time.NewTicker(g.tick)
	defer tick.Stop()
	said := ""
	for {
		g.pass(ctx)
		select {
		case <-w.granted:
			g.note(w, "taken")
			go g.keep(w)
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
			g.queue = slices.DeleteFunc(g.queue, func(o *waiter) bool { return o == w })
			g.mu.Unlock()
			g.pass(context.WithoutCancel(ctx))
			return nil, ctx.Err()
		}
	}
}

// give gives the gate back, or takes it back saying why, and grants it to the next waiter.
func (g *Gates) give(w *waiter, why string) {
	g.mu.Lock()
	if g.held[w.name] != w {
		g.mu.Unlock()
		return // Taken back already.
	}
	delete(g.held, w.name)
	w.back = why
	close(w.done)
	g.mu.Unlock()
	if why != "" {
		g.note(w, "taken back: "+why)
	} else {
		g.note(w, "given back after "+time.Since(w.since).Round(time.Second).String())
	}
	g.pass(context.Background())
	if g.freed != nil {
		g.freed()
	}
}

// keep follows a holder while it holds its gate: it reads what its process uses, and takes the gate back once that
// process ends or the timeout passes, so a forgotten hold never blocks the others.
func (g *Gates) keep(w *waiter) {
	tick := time.NewTicker(g.tick)
	defer tick.Stop()
	limit := time.NewTimer(time.Until(w.until))
	defer limit.Stop()
	g.measure(w)
	for {
		select {
		case <-w.done:
			return
		case <-limit.C:
			g.give(w, "held past its timeout of "+w.timeout.String())
			return
		case <-tick.C:
			if w.pid != 0 && !alive(w.pid) {
				g.give(w, fmt.Sprintf("its process %d ended", w.pid))
				return
			}
			if time.Since(w.read) >= g.every {
				g.measure(w)
			}
		}
	}
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

// Outside counts the gates held outside any running worker, by a person's terminal, a lead or a script: each takes
// a worker's slot, so no new worker starts on it while it is held. A worker's gate is in the worker's own slot.
func (g *Gates) Outside() int {
	g.mu.Lock()
	ids := make([]string, 0, len(g.held))
	for _, h := range g.held {
		ids = append(ids, h.taskID)
	}
	g.mu.Unlock()
	n := 0
	for _, id := range ids {
		if g.outside(id) {
			n++
		}
	}
	return n
}

// pass grants each free gate to its first waiter, the first wish of the rank first, then the first come, unless
// the machine is under pressure or lacks the memory the waiter's command peaked at; it tells every other waiter why
// it waits.
func (g *Gates) pass(ctx context.Context) {
	var snap machine.Snapshot
	var policy machine.Policy
	if g.machine != nil {
		snap, policy = g.machine.Snapshot(), g.machine.Policy()
	}
	pressure := policy.Pressure(snap)
	g.mu.Lock()
	var ids []string
	for _, w := range g.queue {
		if w.taskID != "" {
			ids = append(ids, w.taskID)
		}
	}
	g.mu.Unlock()
	ranks := map[string]int{}
	if g.tasks != nil && len(ids) > 0 {
		ranks = g.tasks.Ranks(ctx, ids)
	}
	rank := func(w *waiter) int {
		if r, ok := ranks[w.taskID]; ok {
			return r
		}
		return len(ranks) + 1 // Outside any task, or unknown: after the ranked wishes.
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	var held uint64
	for _, h := range g.held {
		held += h.peak
	}
	queue := slices.Clone(g.queue)
	slices.SortStableFunc(queue, func(a, b *waiter) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.seq, b.seq)) })
	for _, w := range queue {
		var why, cause string
		switch h := g.held[w.name]; {
		case h != nil:
			why, cause = fmt.Sprintf("held by %s, for %s", h.who, time.Since(h.since).Round(time.Second)), "held by "+h.who
		case pressure != "":
			why = "the machine is under pressure: " + pressure
			cause = why
		default:
			if why = policy.Room(snap, w.what, w.peak, held); why != "" {
				cause = "memory"
				break
			}
			held += w.peak
			w.since = time.Now()
			w.until = w.since.Add(w.timeout)
			g.held[w.name] = w
			g.queue = slices.DeleteFunc(g.queue, func(o *waiter) bool { return o == w })
			close(w.granted)
			continue
		}
		// The holder's time and the memory free change every second: tell the waiter only when the cause changes.
		if cause != w.cause {
			w.why, w.cause = why, cause
			select {
			case w.changed <- struct{}{}:
			default:
			}
		}
	}
}

// note tells the waiter's task, if any, what happens to its gate.
func (g *Gates) note(w *waiter, text string) {
	if w.taskID == "" || g.tasks == nil {
		return
	}
	g.tasks.Note(w.taskID, harness.Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_GATE, Text: "gate " + w.name + ": " + text})
}

// Held gives the names of the gates the task holds now, in order.
func (g *Gates) Held(taskID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for name, h := range g.held {
		if taskID != "" && h.taskID == taskID {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Waiting gives the names of the gates the task waits for now, in order.
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

// State is a gate held or waited for.
type State struct {
	Name, Holder, HolderTaskID string
	Since, Until               time.Time
	Waiting                    []string
	TakesSlot                  bool              // held outside a running worker
	Use                        *planv1.Resources // what the holder's process uses; nil when not read
}

// List gives the gates held or waited for, by name.
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
		s.Holder, s.HolderTaskID, s.Since, s.Until, s.Use = h.who, h.taskID, h.since, h.until, h.use
	}
	for _, w := range g.queue {
		s := get(w.name)
		s.Waiting = append(s.Waiting, w.who)
	}
	g.mu.Unlock()
	out := make([]State, 0, len(byName))
	for _, s := range byName {
		s.TakesSlot = s.Holder != "" && g.outside(s.HolderTaskID)
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.Name, b.Name) })
	return out
}
