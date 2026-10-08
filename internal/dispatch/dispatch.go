// Package dispatch decides which planned task starts now: the scheduler's rules, in plain Go, apart from the store
// and the workers. A pass reads a Situation (the tasks, the wishes, the projects, the machine) and gives a Decision
// for each planned task: start it, let it wait with its reason, or fail it. The harness acts on the decisions; the
// dispatch bench (internal/dispatch/bench) feeds it cases written by hand.
package dispatch

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// Machine is what the machine allows now.
type Machine struct {
	// Slots is the most workers that run at once, and Rule how it was decided.
	Slots int
	Rule  string
	// Pressure says why the machine is under pressure, "" when it is not: no worker starts then.
	Pressure string
	// Running is the number of workers that run now.
	Running int
}

// Situation is what a pass knows: every task, every wish, which projects are in Git, and the machine. Its zero
// value knows nothing and limits nothing; New fills it.
type Situation struct {
	tasks   []*planv1.Task
	byID    map[string]*planv1.Task
	wishes  map[string]*planv1.Wish
	rank    map[string]int // position of each active wish
	git     map[string]bool
	machine *Machine
	started map[string]bool
}

// New is the situation of tasks and wishes, all of them as the store lists them (oldest first), the projects in Git being git (a project not in it is
// not), and machine what the machine allows (nil: nothing limits).
func New(tasks []*planv1.Task, wishes []*planv1.Wish, git map[string]bool, machine *Machine) *Situation {
	s := &Situation{
		tasks: tasks, byID: make(map[string]*planv1.Task, len(tasks)), wishes: make(map[string]*planv1.Wish, len(wishes)),
		rank: map[string]int{}, git: git, machine: machine, started: map[string]bool{},
	}
	for _, t := range tasks {
		s.byID[t.GetId()] = t
	}
	for _, w := range wishes {
		s.wishes[w.GetId()] = w
	}
	for i, w := range plan.Ranked(wishes) {
		s.rank[w.GetId()] = i
	}
	return s
}

// Decision is what a pass decides for one planned task. Why and Failed both empty: it starts now.
type Decision struct {
	Task *planv1.Task
	// Why it waits.
	Why string
	// Why it never will start: it fails.
	Failed string
}

// Planned tells whether Djinn starts the task by itself, once it is ready.
func Planned(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING && t.GetScheduled() && t.GetStartTime() == nil
}

// Pass decides for every planned task, the first wish of the rank first, then the oldest: each task it starts
// counts as writing and takes a slot for the ones after it.
func (s *Situation) Pass() []Decision {
	var out []Decision
	for _, t := range s.Order() {
		why, failed := s.Blocker(t)
		if why == "" && failed == "" {
			s.Start(t)
		}
		out = append(out, Decision{Task: t, Why: why, Failed: failed})
	}
	return out
}

// Order is the planned tasks, by the rank of their wish, then the oldest first. The tasks of a wish that is not
// active come last; Blocker keeps them waiting.
func (s *Situation) Order() []*planv1.Task {
	var out []*planv1.Task
	for _, t := range s.tasks {
		if Planned(t) {
			out = append(out, t)
		}
	}
	pos := func(wishID string) int {
		if p, ok := s.rank[wishID]; ok {
			return p
		}
		return len(s.rank)
	}
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		return cmp.Or(cmp.Compare(pos(a.GetWishId()), pos(b.GetWishId())),
			a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), strings.Compare(a.GetId(), b.GetId()))
	})
	return out
}

// Start counts the task as started: it writes in its project, and takes a slot.
func (s *Situation) Start(t *planv1.Task) { s.started[t.GetId()] = true }

// writing tells whether a task writes in its project now: its worker runs, it was just started, or it waits for
// the answer to its edit question and may start again any time.
func (s *Situation) writing(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING:
		return true
	}
	return s.started[t.GetId()]
}

// Blocker says why the task cannot start now (why), or why it never will (failed); both empty when it can start.
// Its wish must be active (not paused nor granted); then come the dependencies, the write scopes, and the machine.
// A wish the situation does not know does not hold the task.
func (s *Situation) Blocker(t *planv1.Task) (why, failed string) {
	if wish, ok := s.wishes[t.GetWishId()]; ok && !plan.Active(wish) {
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
			return "", fmt.Sprintf("its dependency %s ended %s", d.GetCode(), status(d.GetStatus()))
		}
		if why == "" {
			why = fmt.Sprintf("waits for %s (%s)", d.GetCode(), status(d.GetStatus()))
		}
	}
	if why != "" {
		return why, ""
	}
	// In Git, worktrees separate the writers.
	if id := t.GetProjectId(); id != "" && !s.git[id] {
		for _, o := range s.tasks {
			if o.GetId() == t.GetId() || o.GetProjectId() != id || !s.writing(o) {
				continue
			}
			if Overlap(t.GetWriteScopes(), o.GetWriteScopes()) {
				return fmt.Sprintf("%s writes %s, which overlaps %s", o.GetCode(), ScopeText(o.GetWriteScopes()),
					ScopeText(t.GetWriteScopes())), ""
			}
		}
	}
	return s.full(), ""
}

// full says why no worker may start now, or "" when one may.
func (s *Situation) full() string {
	m := s.machine
	if m == nil {
		return ""
	}
	if m.Pressure != "" {
		return "the machine is under pressure: " + m.Pressure
	}
	if running := m.Running + len(s.started); running >= m.Slots {
		if running == 1 {
			return "1 worker runs, the most this machine holds (" + m.Rule + ")"
		}
		return fmt.Sprintf("%d workers run, the most this machine holds (%s)", running, m.Rule)
	}
	return ""
}

// Overlap tells whether two sets of write scopes share a path: one names the other, or a folder holding it. No
// scope is the whole folder. Case is ignored, as on macOS and Windows.
func Overlap(a, b []string) bool {
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

// ScopeText writes write scopes for a person.
func ScopeText(scopes []string) string {
	if len(scopes) == 0 {
		return "the whole folder"
	}
	return strings.Join(scopes, ", ")
}

// status is a task status as the command line writes it: TASK_STATUS_DONE is done.
func status(s planv1.TaskStatus) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "TASK_STATUS_"))
}
