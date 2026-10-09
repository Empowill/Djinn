// Package gate grants the gates: shared resources (code generation, the local stack, an end-to-end run, a paid
// model run) that one holder uses at a time, granted only while the machine is not under pressure and has the memory
// the command peaked at, the first wish of the rank first. A holder keeps its gate until it gives it back, or its
// connection ends.
package gate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
)

// Tasks is what the gates need from the tasks: to name a holder, to rank waiters by their wish, and to tell a
// task's events. The harness is one.
type Tasks interface {
	Describe(ctx context.Context, taskID string) (string, error)
	Ranks(ctx context.Context, taskIDs []string) map[string]int
	Note(taskID string, ev harness.Event)
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

// Gates grants the gates. Its zero value is not usable: New makes one.
type Gates struct {
	machine Machine       // nil: never under pressure, its memory unknown
	costs   Costs         // nil: no command measured
	tasks   Tasks         // nil: holders outside any task only
	tick    time.Duration // how often a waiter looks at the machine again

	mu    sync.Mutex
	held  map[string]*waiter // by gate name
	queue []*waiter          // in arrival order
	seq   int
}

// waiter is a holder of a gate, or one that waits for it.
type waiter struct {
	name, taskID, who, what string
	peak                    uint64 // the command's highest measured peak memory; 0 when never measured
	seq                     int
	since                   time.Time
	granted                 chan struct{} // closed when the gate is granted
	changed                 chan struct{} // the cause to wait changed
	why, cause              string        // guarded by Gates.mu
}

// New returns the gates of machine m (nil: never under pressure, its memory unknown), the commands' measured costs
// being costs (nil: none measured), the holders' tasks being tasks (nil: none known).
func New(m Machine, costs Costs, tasks Tasks) *Gates {
	return &Gates{machine: m, costs: costs, tasks: tasks, tick: time.Second, held: map[string]*waiter{}}
}

// Name is a gate's name as Djinn keeps it: names ignore case.
func Name(s string) string { return strings.ToLower(s) }

// Take waits until the gate is granted, and returns the function that gives it back; calling it again does
// nothing. taskID, when not empty, is the task the gate is taken for: its events say when it waits, takes and gives
// the gate back. what says what the holder runs, in the folder dir: a command measured in its project waits until
// the machine has the memory it peaked at. waiting is called each time the reason to wait changes. Take fails when
// ctx ends first.
func (g *Gates) Take(ctx context.Context, name, taskID, what, dir string, waiting func(why string)) (func(), error) {
	name = Name(name)
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
			peak = g.costs.Peak(ctx, taskID, dir, what)
		}
	}
	g.mu.Lock()
	g.seq++
	w := &waiter{
		name: name, taskID: taskID, who: who, what: what, peak: peak, seq: g.seq,
		granted: make(chan struct{}), changed: make(chan struct{}, 1),
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
			var once sync.Once
			return func() { once.Do(func() { g.give(w) }) }, nil
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

// give gives the gate back, and grants it to the next waiter.
func (g *Gates) give(w *waiter) {
	g.mu.Lock()
	if g.held[w.name] == w {
		delete(g.held, w.name)
	}
	g.mu.Unlock()
	g.note(w, "given back after "+time.Since(w.since).Round(time.Second).String())
	g.pass(context.Background())
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
	Since                      time.Time
	Waiting                    []string
}

// List gives the gates held or waited for, by name.
func (g *Gates) List() []State {
	g.mu.Lock()
	defer g.mu.Unlock()
	byName := map[string]*State{}
	get := func(name string) *State {
		if byName[name] == nil {
			byName[name] = &State{Name: name}
		}
		return byName[name]
	}
	for name, h := range g.held {
		s := get(name)
		s.Holder, s.HolderTaskID, s.Since = h.who, h.taskID, h.since
	}
	for _, w := range g.queue {
		s := get(w.name)
		s.Waiting = append(s.Waiting, w.who)
	}
	out := make([]State, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b State) int { return strings.Compare(a.Name, b.Name) })
	return out
}
