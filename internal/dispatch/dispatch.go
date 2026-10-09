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
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
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
	// Available is the memory available to new programs, in bytes; 0 when unknown, and then the memory holds no
	// worker back. Policy weighs it against what a worker of the task's provider typically takes
	// (machine.Policy.WorkerRoom).
	Available uint64
	Policy    machine.Policy
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
	now     time.Time
	// The first task the memory held in this pass, and why: the tasks after it wait for it.
	short    *planv1.Task
	shortWhy string
	peaks    map[string][]uint64 // the peak memory of the finished workers of each provider, the latest first
}

// New is the situation of tasks and wishes, all of them as the store lists them (oldest first), the projects in Git being git (a project not in it is
// not), and machine what the machine allows (nil: nothing limits).
func New(tasks []*planv1.Task, wishes []*planv1.Wish, git map[string]bool, machine *Machine) *Situation {
	s := &Situation{
		tasks: tasks, byID: make(map[string]*planv1.Task, len(tasks)), wishes: make(map[string]*planv1.Wish, len(wishes)),
		rank: map[string]int{}, git: git, machine: machine, started: map[string]bool{}, now: time.Now(),
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

// At sets the time the situation is read at, which a usage limit's reset is compared with: the harness's clock.
func (s *Situation) At(now time.Time) *Situation {
	s.now = now
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

// Planned tells whether Djinn starts the task by itself, once it is ready: a task planned on this machine, or one
// whose worker Djinn resumes (cut short by a restart, or by its provider's usage limit).
func Planned(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_PENDING:
		return t.GetScheduled() && t.GetStartTime() == nil
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		return t.GetScheduled()
	}
	return false
}

// Watcher tells whether the task is a watcher: a command, no agent. It takes no slot and writes nothing, so neither
// the machine nor the write scopes hold it, and it holds no other task.
func Watcher(t *planv1.Task) bool { return t.GetProvider() == planv1.Provider_PROVIDER_WATCH }

// Resuming tells whether the task is one Djinn resumes.
func Resuming(t *planv1.Task) bool { return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING }

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

// Order is the planned tasks: the ones Djinn resumes first, as they ran before; then by the rank of their wish, then
// the oldest first. The tasks of a wish that is not active come last; Blocker keeps them waiting.
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
	first := func(t *planv1.Task) int {
		if Resuming(t) {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		return cmp.Or(cmp.Compare(first(a), first(b)), cmp.Compare(pos(a.GetWishId()), pos(b.GetWishId())),
			a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), strings.Compare(a.GetId(), b.GetId()))
	})
	return out
}

// Start counts the task as started: it writes in its project, and takes a slot. A watcher does neither.
func (s *Situation) Start(t *planv1.Task) {
	if !Watcher(t) {
		s.started[t.GetId()] = true
	}
}

// writing tells whether a task writes in its project now: its worker runs or is paused, it was just started, or it waits for
// the answer to its edit question and may start again any time.
func (s *Situation) writing(t *planv1.Task) bool {
	if Watcher(t) {
		return false
	}
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_WAITING:
		return true
	}
	return s.started[t.GetId()]
}

// Blocker says why the task cannot start now (why), or why it never will (failed); both empty when it can start.
// Its wish must be active (not paused nor granted); then come the dependencies, its provider's usage limit, the write
// scopes, and the machine; a watcher only waits for its wish and its dependencies.
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
		// A dependency resumed as a fork is its fork: "W1, resumed as W5," when it ended, W5 while it waits.
		ended := d.GetCode()
		if as := s.forkedAs(d); as != d {
			ended, d = fmt.Sprintf("%s, resumed as %s,", ended, as.GetCode()), as
		}
		switch d.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE:
			continue
		case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
			// Failed covers a task resumed maxResumes times without finishing: Djinn resumes it no more.
			return "", fmt.Sprintf("its dependency %s ended %s", ended, status(d.GetStatus()))
		}
		// Interrupted or resuming: Djinn resumes it by itself, so the task waits for it.
		if why == "" {
			why = fmt.Sprintf("waits for %s (%s)", d.GetCode(), status(d.GetStatus()))
		}
	}
	if why != "" {
		return why, ""
	}
	if why := s.limited(t); why != "" {
		return why, ""
	}
	if Watcher(t) {
		return "", "" // It sleeps until its command prints: no slot, and it writes nothing.
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
	return s.full(t), ""
}

// forkedAs is the task that took over d: d itself, or, when d was cut short and resumed as a fork of its session
// (render.ForkedAs), or closed as continued in a fork (Closure.continued_in), that fork, followed to the last one.
func (s *Situation) forkedAs(d *planv1.Task) *planv1.Task {
	seen := map[string]bool{}
	for !seen[d.GetId()] {
		seen[d.GetId()] = true
		code := d.GetClosed().GetContinuedIn()
		if d.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
			code = render.ForkedAs(d, s.tasks)
		}
		i := slices.IndexFunc(s.tasks, func(o *planv1.Task) bool { return o.GetWishId() == d.GetWishId() && o.GetCode() == code })
		if code == "" || i < 0 {
			break
		}
		d = s.tasks[i]
	}
	return d
}

// limited says why the task waits for a usage limit: its own, until it resets, or its provider's, which a task
// waiting for it holds; "" when none holds.
func (s *Situation) limited(t *planv1.Task) string {
	if Resuming(t) && t.GetResumeAfter() != nil && t.GetResumeAfter().AsTime().After(s.now) {
		return cmp.Or(t.GetWaitReason(), "its provider's usage limit")
	}
	var holder *planv1.Task
	for _, o := range s.tasks {
		if o.GetId() == t.GetId() || !Resuming(o) || o.GetResumeAfter() == nil || !o.GetResumeAfter().AsTime().After(s.now) ||
			provider(o) != provider(t) {
			continue
		}
		if holder == nil || o.GetResumeAfter().AsTime().After(holder.GetResumeAfter().AsTime()) {
			holder = o
		}
	}
	if holder == nil {
		return ""
	}
	return fmt.Sprintf("%s waits for %s", provider(t), cmp.Or(holder.GetWaitReason(), "its usage limit"))
}

// provider names the task's agent; none is claude.
func provider(t *planv1.Task) string {
	if t.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
		return "claude"
	}
	return strings.ToLower(strings.TrimPrefix(t.GetProvider().String(), "PROVIDER_"))
}

// full says why the task's worker may not start now, or "" when it may: a slot is free, and the memory holds a
// worker of its provider. Once the memory holds a task, the ones after it wait for it: they keep their order.
func (s *Situation) full(t *planv1.Task) string {
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
	if m.Available == 0 {
		return ""
	}
	if s.short != nil {
		return fmt.Sprintf("%s goes first: %s", s.short.GetCode(), s.shortWhy)
	}
	peak, measured := s.typical(provider(t))
	why := m.Policy.WorkerRoom(m.Available, s.growing(), provider(t), peak, measured)
	if why != "" {
		s.short, s.shortWhy = t, why
	}
	return why
}

// typical is the typical peak memory of a worker of the provider, and how many measured workers gave it
// (machine.Policy.Typical): the finished workers of that provider, the latest first.
func (s *Situation) typical(name string) (uint64, int) {
	if s.peaks == nil {
		var ended []*planv1.Task
		for _, t := range s.tasks {
			if t.GetEndTime() != nil && t.GetResources().GetPeakMemoryBytes() > 0 && !Watcher(t) {
				ended = append(ended, t)
			}
		}
		slices.SortStableFunc(ended, func(a, b *planv1.Task) int {
			return b.GetEndTime().AsTime().Compare(a.GetEndTime().AsTime())
		})
		s.peaks = map[string][]uint64{}
		for _, t := range ended {
			s.peaks[provider(t)] = append(s.peaks[provider(t)], t.GetResources().GetPeakMemoryBytes())
		}
	}
	return s.machine.Policy.Typical(s.peaks[name])
}

// growing is the memory the workers running may still take: for each, the typical peak of its provider less what it
// uses now (all of it before its first reading), and in full for each worker this pass started.
func (s *Situation) growing() uint64 {
	var sum uint64
	for _, t := range s.tasks {
		running := t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && !Watcher(t)
		if !running && !s.started[t.GetId()] {
			continue
		}
		peak, _ := s.typical(provider(t))
		if uses := t.GetResources().GetMemoryBytes(); running && uses < peak {
			sum += peak - uses
		} else if !running {
			sum += peak
		}
	}
	return sum
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
