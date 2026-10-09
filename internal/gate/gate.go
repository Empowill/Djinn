// Package gate grants the gates: shared resources (code generation, the local stack, an end-to-end run, a paid
// model run) that one holder uses at a time, granted only while the machine is not under pressure, the first wish
// of the rank first. A holder keeps its gate until it gives it back, or its connection ends.
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
)

// Tasks is what the gates need from the tasks: to name a holder, to rank waiters by their wish, and to tell a
// task's events. The harness is one.
type Tasks interface {
	Describe(ctx context.Context, taskID string) (string, error)
	Ranks(ctx context.Context, taskIDs []string) map[string]int
	Note(taskID string, ev harness.Event)
}

// Gates grants the gates. Its zero value is not usable: New makes one.
type Gates struct {
	pressure func() string // why the machine is under pressure, "" when it is not
	tasks    Tasks         // nil: holders outside any task only
	tick     time.Duration // how often a waiter looks at the pressure again

	mu    sync.Mutex
	held  map[string]*waiter // by gate name
	queue []*waiter          // in arrival order
	seq   int
}

// waiter is a holder of a gate, or one that waits for it.
type waiter struct {
	name, taskID, who string
	seq               int
	since             time.Time
	granted           chan struct{} // closed when the gate is granted
	changed           chan struct{} // why changed
	why               string        // guarded by Gates.mu
}

// New returns the gates of a machine whose pressure is given by pressure (nil: never under pressure), the holders'
// tasks being tasks (nil: none known).
func New(pressure func() string, tasks Tasks) *Gates {
	if pressure == nil {
		pressure = func() string { return "" }
	}
	return &Gates{pressure: pressure, tasks: tasks, tick: time.Second, held: map[string]*waiter{}}
}

// Name is a gate's name as Djinn keeps it: names ignore case.
func Name(s string) string { return strings.ToLower(s) }

// Take waits until the gate is granted, and returns the function that gives it back; calling it again does
// nothing. taskID, when not empty, is the task the gate is taken for: its events say when it waits, takes and gives
// the gate back. what says what the holder runs. waiting is called each time the reason to wait changes. Take
// fails when ctx ends first.
func (g *Gates) Take(ctx context.Context, name, taskID, what string, waiting func(why string)) (func(), error) {
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
	if what != "" {
		who += ": " + what
	}
	g.mu.Lock()
	g.seq++
	w := &waiter{
		name: name, taskID: taskID, who: who, seq: g.seq,
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
// the machine is under pressure; it tells every other waiter why it waits.
func (g *Gates) pass(ctx context.Context) {
	pressure := g.pressure()
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
	queue := slices.Clone(g.queue)
	slices.SortStableFunc(queue, func(a, b *waiter) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.seq, b.seq)) })
	for _, w := range queue {
		var why string
		switch h := g.held[w.name]; {
		case h != nil:
			why = fmt.Sprintf("held by %s, for %s", h.who, time.Since(h.since).Round(time.Second))
		case pressure != "":
			why = "the machine is under pressure: " + pressure
		default:
			w.since = time.Now()
			g.held[w.name] = w
			g.queue = slices.DeleteFunc(g.queue, func(o *waiter) bool { return o == w })
			close(w.granted)
			continue
		}
		// The holder's time changes every second: tell the waiter only when the holder or the cause changes.
		if cause(why) != cause(w.why) {
			w.why = why
			select {
			case w.changed <- struct{}{}:
			default:
			}
		}
	}
}

// cause is a reason to wait without the time it gives.
func cause(why string) string {
	if i := strings.LastIndex(why, ", for "); i >= 0 && strings.HasPrefix(why, "held by ") {
		return why[:i]
	}
	return why
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
