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
	// Gates is the number of gates held outside the running workers: each takes a slot.
	Gates int
	// Resuming is the number of workers paused by the load bar or for an investigator, which resume before any new
	// task (Q123): each keeps its slot from a new work task. An investigator goes before them.
	Resuming int
	// Available is the memory available to new programs, in bytes; 0 when unknown, and then the memory holds no
	// worker back. Policy weighs it against what a worker of the task's provider typically takes
	// (machine.Policy.WorkerRoom).
	Available uint64
	// Total is the machine's total RAM in bytes; 0 when unknown.
	Total  uint64
	Policy machine.Policy
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
	started map[string]bool // the workers started in this pass: each takes a slot and memory
	now     time.Time
	// The first task the memory held in this pass, and why: the tasks after it wait for it.
	short      *planv1.Task
	shortWhy   string
	peaks      map[string][]uint64 // the peak memory of the finished workers of each provider, the latest first
	branchHead BranchResolver
	isAncestor AncestryChecker
	ancestry   map[[2]string]bool // (branchHead, integrationHead) -> isAncestor
	isRunning  func(taskID string) bool
}

// AncestryChecker tells whether commit a is an ancestor of commit b in projectID:
// git merge-base --is-ancestor a b.
type AncestryChecker func(projectID, a, b string) bool

// BranchResolver resolves the head SHA of branch in projectID, and tells whether the branch has no commit beyond
// the point it started from (noCommits).
type BranchResolver func(projectID, branch string) (head string, noCommits bool)

// WithAncestry equips the situation with functions to check branch heads and ancestry in Git repositories.
func (s *Situation) WithAncestry(branchHead BranchResolver, isAncestor AncestryChecker) *Situation {
	s.branchHead = branchHead
	s.isAncestor = isAncestor
	return s
}

// WithRunning equips the situation with a function to check if a task currently has an active worker run.
// A task whose status is RUNNING but has no active run (a lost process) is not counted as running.
func (s *Situation) WithRunning(isRunning func(taskID string) bool) *Situation {
	s.isRunning = isRunning
	return s
}

func (s *Situation) taskRunning(t *planv1.Task) bool {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		return false
	}
	if s.isRunning != nil {
		return s.isRunning(t.GetId())
	}
	return true
}

// New is the situation of tasks and wishes, all of them as the store lists them (oldest first), the projects in Git being git (a project not in it is
// not), and machine what the machine allows (nil: nothing limits).
func New(tasks []*planv1.Task, wishes []*planv1.Wish, git map[string]bool, machine *Machine) *Situation {
	plan.FillAzimas(tasks)
	s := &Situation{
		tasks: tasks, byID: make(map[string]*planv1.Task, len(tasks)), wishes: make(map[string]*planv1.Wish, len(wishes)),
		rank: map[string]int{}, git: git, machine: machine, started: map[string]bool{},
		ancestry: make(map[[2]string]bool), now: time.Now(),
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
	// It waits for a dependency's work to be committed into the wish's integration branch: the integration commits
	// that work at once.
	Commit bool
	// It waits for the machine alone: no slot free, the machine under pressure, or its memory short. Forced
	// (djinn task start), it may start. A task that waits for one going first before it does not.
	Machine bool
	// It waits for a slot alone: the machine is not under pressure and its memory holds it. An investigator pauses
	// a work task for it (the harness's preemptForSlot): it takes that task's slot, never one more.
	Slot bool
}

// Planned tells whether Djinn starts the task by itself, once it is ready: a task planned on this machine, or one
// whose worker Djinn resumes (cut short by a restart, or by its provider's usage limit). An azima never is: no worker
// runs it.
func Planned(t *planv1.Task) bool {
	if plan.IsAzima(t) {
		return false
	}
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

// QuestionWorker tells whether Djinn started the task on a question (Task.role): it reads and runs djinn's commands,
// no build nor gate, so it holds no write scope; an agent all the same, it takes a slot like any worker (W577: the
// load bar's count is every agent that runs), and waits while the machine is under pressure or its memory would not
// hold it. On a full machine, an investigator takes the slot of a work task it pauses (Decision.Slot).
func QuestionWorker(t *planv1.Task) bool { return t.GetRole() != planv1.TaskRole_TASK_ROLE_UNSPECIFIED }

// Light tells whether the task writes nothing in its project's folder: a watcher, or a question worker. Only a watcher
// takes no slot.
func Light(t *planv1.Task) bool { return Watcher(t) || QuestionWorker(t) }

// Resuming tells whether the task is one Djinn resumes.
func Resuming(t *planv1.Task) bool { return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING }

// Pass decides for every planned task, the first wish of the rank first, then the oldest: each task it starts
// counts as writing and takes a slot for the ones after it.
func (s *Situation) Pass() []Decision {
	var out []Decision
	for _, t := range s.Order() {
		why, failed := s.Held(t)
		machine, slot := false, false
		if why == "" && failed == "" {
			var first bool
			if why, first, slot = s.full(t); why == "" {
				s.Start(t)
			}
			machine = why != "" && !first
		}
		out = append(out, Decision{Task: t, Why: why, Failed: failed, Commit: why != "" && s.awaitsCommit(t),
			Machine: machine, Slot: slot})
	}
	return out
}

// Order is the planned tasks in the strict 5-tier order:
//  1. Investigator workers (Enlighten me), followed by answer workers;
//  2. Correction workers, followed by review workers;
//  3. Resumed work tasks (RESUMING: limits, restarts, preemptions);
//  4. Work tasks that unblock others (graph's critical path: most planned and waiting dependents first);
//  5. Other work tasks.
//
// Within each level and sub-level, ties are broken by the wish's rank, then the oldest first, then by task ID.
// The tasks of a wish that is not active come last; Blocker keeps them waiting.
func (s *Situation) Order() []*planv1.Task {
	var out []*planv1.Task
	for _, t := range s.tasks {
		if Planned(t) {
			out = append(out, t)
		}
	}
	depCounts := s.dependentCounts()
	pos := func(wishID string) int {
		if p, ok := s.rank[wishID]; ok {
			return p
		}
		return len(s.rank)
	}
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		aLevel, aSub := taskPriority(a, depCounts)
		bLevel, bSub := taskPriority(b, depCounts)
		return cmp.Or(
			cmp.Compare(aLevel, bLevel),
			cmp.Compare(aSub, bSub),
			cmp.Compare(pos(a.GetWishId()), pos(b.GetWishId())),
			a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()),
			strings.Compare(a.GetId(), b.GetId()),
		)
	})
	return out
}

func taskPriority(t *planv1.Task, depCounts map[string]int) (level int, sub int) {
	switch {
	case t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR:
		return 1, 0
	case QuestionWorker(t):
		return 1, 1
	case t.GetCorrection() != nil:
		return 2, 0
	case t.GetReview() != nil:
		return 2, 1
	case Resuming(t):
		return 3, 0
	case depCounts[t.GetId()] > 0:
		return 4, -depCounts[t.GetId()]
	default:
		return 5, 0
	}
}

// isWaitingWorkTask tells whether a task is a planned or waiting work task in the dependency graph.
func isWaitingWorkTask(t *planv1.Task) bool {
	if plan.IsAzima(t) || Watcher(t) || QuestionWorker(t) {
		return false
	}
	return Planned(t) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING
}

// dependentCounts returns the count of planned and waiting work tasks that depend on each task directly and transitively.
func (s *Situation) dependentCounts() map[string]int {
	children := make(map[string][]string)
	for _, t := range s.tasks {
		for _, depID := range t.GetDependsOn() {
			if parent, ok := s.byID[depID]; ok {
				parent = s.forkedAs(parent)
				children[parent.GetId()] = append(children[parent.GetId()], t.GetId())
			} else {
				children[depID] = append(children[depID], t.GetId())
			}
		}
	}

	counts := make(map[string]int)
	for _, t := range s.tasks {
		if !Planned(t) {
			continue
		}
		visited := map[string]bool{t.GetId(): true}
		queue := []string{t.GetId()}
		count := 0

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]

			for _, childID := range children[curr] {
				if visited[childID] {
					continue
				}
				visited[childID] = true
				if child, ok := s.byID[childID]; ok && isWaitingWorkTask(child) {
					count++
				}
				queue = append(queue, childID)
			}
		}
		counts[t.GetId()] = count
	}
	return counts
}

// Start counts the task as started: it takes a slot and memory, and writes in its project unless it is a question
// worker; a watcher takes nothing.
func (s *Situation) Start(t *planv1.Task) {
	if !Watcher(t) {
		s.started[t.GetId()] = true
	}
}

// writing tells whether a task writes in its project now: its worker runs or is paused, it was just started, or it waits for
// the answer to its edit question and may start again any time.
func (s *Situation) writing(t *planv1.Task) bool {
	if Light(t) {
		return false
	}
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING:
		return s.taskRunning(t)
	case planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_WAITING:
		return true
	}
	return s.started[t.GetId()]
}

// Blocker says why the task cannot start now (why), or why it never will (failed); both empty when it can start.
// Its wish must be active (not paused nor granted); then come the dependencies, its provider's usage limit, the write
// scopes, and the machine; a watcher only waits for its wish and its dependencies, a question worker for the machine's
// pressure and memory too.
// A wish the situation does not know does not hold the task.
func (s *Situation) Blocker(t *planv1.Task) (why, failed string) {
	if why, failed = s.Held(t); why != "" || failed != "" {
		return why, failed
	}
	why, _, _ = s.full(t)
	return why, ""
}

// Held is Blocker without the machine: what holds the task whatever the machine allows (its wish, a dependency, a
// push hold, its provider's usage limit, a write scope). A task forced to start (djinn task start) needs Held empty.
func (s *Situation) Held(t *planv1.Task) (why, failed string) {
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
			if plan.IsAzima(d) && (d.GetAzima() == nil || d.GetAzima().GetState() != planv1.AzimaState_AZIMA_STATE_DONE) {
				break
			}
			// Work Djinn integrates counts once committed into the wish's integration branch, which the task starts
			// from (T07); until then it waits, through a conflict or red tests a worker corrects too.
			if waits, state := uncommitted(d); waits {
				if why == "" {
					why = fmt.Sprintf("waits for %s to be committed%s", d.GetCode(), state)
				}
				continue
			}
			if !plan.IsAzima(d) && !integrationDropped(d) && d.GetBranch() != "" && d.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
				if !s.workMerged(d) && why == "" {
					intBranch := s.integrationBranch(d)
					why = fmt.Sprintf("waits for %s's work to reach %s", d.GetCode(), intBranch)
				}
			}
			continue
		case planv1.TaskStatus_TASK_STATUS_CANCELLED:
			continue
		case planv1.TaskStatus_TASK_STATUS_FAILED:
			if why == "" {
				why = fmt.Sprintf("waits while its dependency %s is failed", ended)
			}
			continue
		case planv1.TaskStatus_TASK_STATUS_STOPPED:
			return "", fmt.Sprintf("its dependency %s ended %s", ended, status(d.GetStatus()))
		}
		// Interrupted or resuming: Djinn resumes it by itself, so the task waits for it. An azima waits to be marked
		// done. The azima a task is part of is never waited for: only what it depends on.
		if why == "" && plan.IsAzima(d) {
			why = fmt.Sprintf("waits for the azima %s to be done", d.GetCode())
		} else if why == "" {
			why = fmt.Sprintf("waits for %s (%s)", d.GetCode(), status(d.GetStatus()))
		}
	}
	if why != "" {
		return why, ""
	}
	if why := s.pushHeld(t); why != "" {
		return why, ""
	}
	if why := s.limited(t); why != "" {
		return why, ""
	}
	if Watcher(t) {
		return "", "" // It sleeps until its command prints: no slot, and it writes nothing.
	}
	// In Git, worktrees separate the writers; a question worker writes nothing.
	if id := t.GetProjectId(); id != "" && !s.git[id] && !QuestionWorker(t) {
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
	return "", ""
}

// awaitsCommit tells whether a dependency of t is done, its work waiting to be committed.
func (s *Situation) awaitsCommit(t *planv1.Task) bool {
	for _, id := range t.GetDependsOn() {
		if d, ok := s.byID[id]; ok {
			d = s.forkedAs(d)
			if waits, _ := uncommitted(d); waits && d.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
				return true
			}
		}
	}
	return false
}

// integrationDropped tells whether the branch work of d, a task done, was dropped by hand (djinn task done --drop,
// which closes the task without queuing integration, or left out when answering an integration question).
func integrationDropped(d *planv1.Task) bool {
	if d.GetClosed() != nil && d.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED {
		return true
	}
	return strings.HasPrefix(d.GetIntegration().GetReason(), "left out (")
}

// uncommitted tells whether the work of d, a task done, waits to be committed into its wish's integration branch:
// not when it is committed, nor when Djinn does not integrate it. state is " (conflict)" or " (red)" when its merge
// met one, " (uncommitted)" while its worktree holds changes not committed, "" while it waits or is being committed.
func uncommitted(d *planv1.Task) (waits bool, state string) {
	if integrationDropped(d) {
		return false, ""
	}
	switch d.GetIntegration().GetState() {
	case planv1.IntegrationState_INTEGRATION_STATE_UNSPECIFIED, planv1.IntegrationState_INTEGRATION_STATE_COMMITTED:
		return false, ""
	case planv1.IntegrationState_INTEGRATION_STATE_CONFLICT:
		return true, " (conflict)"
	case planv1.IntegrationState_INTEGRATION_STATE_RED:
		return true, " (red)"
	case planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED:
		return true, " (uncommitted)"
	}
	return true, ""
}

// workMerged tells whether the work of a done dependency with a branch is in its wish's integration branch:
// its branch's head is an ancestor of the integration branch's head, or it has no commit beyond the point it started from.
func (s *Situation) workMerged(d *planv1.Task) bool {
	if s.branchHead == nil || s.isAncestor == nil {
		return false
	}
	bHead, noCommits := s.branchHead(d.GetProjectId(), d.GetBranch())
	if noCommits {
		return true
	}
	if bHead == "" {
		return false
	}
	intBranch := s.integrationBranch(d)
	intHead, _ := s.branchHead(d.GetProjectId(), intBranch)
	if intHead == "" {
		return false
	}
	key := [2]string{bHead, intHead}
	if merged, ok := s.ancestry[key]; ok {
		return merged
	}
	merged := s.isAncestor(d.GetProjectId(), bHead, intHead)
	if s.ancestry == nil {
		s.ancestry = make(map[[2]string]bool)
	}
	s.ancestry[key] = merged
	return merged
}

// integrationBranch returns the integration branch name of d's wish for d's project.
func (s *Situation) integrationBranch(d *planv1.Task) string {
	if wish, ok := s.wishes[d.GetWishId()]; ok {
		if b := plan.IntegrationBranchOf(wish, d.GetProjectId()); b != "" {
			return b
		}
		if p := plan.PushStateOf(wish, d.GetProjectId()); p != nil && p.GetLast().GetBranch() != "" {
			return p.GetLast().GetBranch()
		}
		if len(wish.GetIntegrationBranches()) > 0 {
			return wish.GetIntegrationBranches()[0].GetBranch()
		}
	}
	return "the integration branch"
}

func short8(sha string) string { return sha[:min(8, len(sha))] }

// pushCheckHeld extracts the short sha and check name for a held push.
func pushCheckHeld(p *planv1.WishPush) (sha, check string) {
	if f := p.GetFailure(); f != nil {
		sha = short8(cmp.Or(p.GetFailedSha(), f.GetBase()))
		check, _, _ = strings.Cut(f.GetReason(), ":")
		check = strings.TrimSpace(check)
		if check == "" {
			check = f.GetCommand()
		}
	}
	if sha == "" {
		sha = short8(p.GetFailedSha())
	}
	if (sha == "" || check == "") && strings.HasPrefix(p.GetHeld(), "at ") {
		rest := strings.TrimPrefix(p.GetHeld(), "at ")
		shaPart, whyPart, ok := strings.Cut(rest, ", ")
		if ok {
			if sha == "" {
				sha = short8(shaPart)
			}
			if check == "" {
				check, _, _ = strings.Cut(whyPart, ":")
				check = strings.TrimSpace(check)
			}
		}
	}
	if sha == "" {
		sha = "HEAD"
	}
	if check == "" {
		check = "checks"
	}
	return sha, check
}

// pushHeld tells whether t waits because the push of its wish's integration branch is held on red push checks.
func (s *Situation) pushHeld(t *planv1.Task) string {
	if t.GetCorrection() != nil || QuestionWorker(t) {
		return ""
	}
	wish := s.wishes[t.GetWishId()]
	if wish == nil || t.GetProjectId() == "" {
		return ""
	}
	p := plan.PushStateOf(wish, t.GetProjectId())
	if p == nil || p.GetHeld() == "" || p.GetUnchecked() {
		return ""
	}
	if p.GetCorrectedBy() == t.GetId() {
		return ""
	}
	branch := s.integrationBranch(t)
	sha, check := pushCheckHeld(p)
	return fmt.Sprintf("waits for %s to be green again (push checks red at %s: %s)", branch, sha, check)
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

// Provider names the task's agent; none is claude.
func Provider(t *planv1.Task) string {
	if t.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
		return "claude"
	}
	return strings.ToLower(strings.TrimPrefix(t.GetProvider().String(), "PROVIDER_"))
}

func provider(t *planv1.Task) string { return Provider(t) }

// full says why the task's worker may not start now, or "" when it may: a slot is free, and the memory holds a worker
// of its provider. Once the memory holds a task, the ones after it wait for it: they keep their order.
// first tells that the task waits for one before it in that order, not for the machine: djinn task start never jumps it.
// slot tells that it waits for a slot alone, its memory holding it: an investigator may take a paused task's slot.
func (s *Situation) full(t *planv1.Task) (why string, first, slot bool) {
	m := s.machine
	if m == nil || Watcher(t) { // A watcher sleeps until its command prints: no slot.
		return "", false, false
	}
	if m.Pressure != "" {
		return "the machine is under pressure: " + m.Pressure, false, false
	}
	// A slot each for the workers running, the ones this pass started, the gates held outside them, and the paused
	// workers that resume first.
	resuming := m.Resuming
	if t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
		resuming = 0
	}
	if running := m.Running + len(s.started); running+m.Gates+resuming >= m.Slots {
		workers := "1 worker runs"
		if running != 1 {
			workers = fmt.Sprintf("%d workers run", running)
		}
		switch m.Gates {
		case 0:
		case 1:
			workers += " and 1 gate is held outside the workers"
		default:
			workers += fmt.Sprintf(" and %d gates are held outside the workers", m.Gates)
		}
		switch resuming {
		case 0:
		case 1:
			workers += ", 1 paused worker resumes first"
		default:
			workers += fmt.Sprintf(", %d paused workers resume first", resuming)
		}
		why = workers + ", the most this machine holds (" + m.Rule + ")"
		if t.GetRole() != planv1.TaskRole_TASK_ROLE_INVESTIGATOR {
			return why, false, false
		}
		// An investigator may take the slot of a work task it pauses: only where the memory holds it.
		if room, first := s.room(t); room != "" {
			return room, first, false
		}
		return why, false, true
	}
	room, first := s.room(t)
	return room, first, false
}

// room says why the memory does not hold the task's worker now, or "" when it does: what a worker of the task's
// provider typically takes. Once the memory holds a task, the ones after it wait for it: first says so.
func (s *Situation) room(t *planv1.Task) (why string, first bool) {
	m := s.machine
	if m.Available == 0 {
		return "", false
	}
	if s.short != nil {
		switch {
		case s.short.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR:
			return "an investigator goes first: " + s.shortWhy, true
		case s.short.GetCorrection() != nil:
			return "a correction goes first: " + s.shortWhy, true
		case s.short.GetReview() != nil:
			return "a review goes first: " + s.shortWhy, true
		default:
			code := cmp.Or(s.short.GetCode(), "a work task")
			return fmt.Sprintf("%s goes first: %s", code, s.shortWhy), true
		}
	}
	peak, measured := s.typical(provider(t))
	why = m.Policy.WorkerRoom(m.Total, m.Available, s.resident(), s.growing(), s.engaged(), provider(t), peak, measured)
	if why != "" {
		s.short, s.shortWhy = t, why
	}
	return why, false
}

// ProviderPeaks returns the measured p95 memories (falling back to peak memories if p95 is unset)
// of finished workers, by provider, the latest first: the worker memory guardrail's baseline, never tied to the notch.
func ProviderPeaks(tasks []*planv1.Task) map[string][]uint64 {
	return ProviderMemory(tasks, 95)
}

// ProviderMemory returns the memory of finished workers at percentile of their series (MemoryAt), by provider, the
// latest first.
func ProviderMemory(tasks []*planv1.Task, percentile int) map[string][]uint64 {
	var ended []*planv1.Task
	for _, t := range tasks {
		if t.GetEndTime() != nil && !Watcher(t) && MemoryAt(t.GetResources(), percentile) > 0 {
			ended = append(ended, t)
		}
	}
	slices.SortStableFunc(ended, func(a, b *planv1.Task) int {
		return b.GetEndTime().AsTime().Compare(a.GetEndTime().AsTime())
	})
	peaks := map[string][]uint64{}
	for _, t := range ended {
		p := Provider(t)
		peaks[p] = append(peaks[p], MemoryAt(t.GetResources(), percentile))
	}
	return peaks
}

// MemoryAt is a worker's memory at percentile of its series: the 50th, 60th, 70th, 80th or 95th, the first of them at
// or above percentile; the 95th when that one was never recorded (a worker measured before Djinn kept it), then the
// peak.
func MemoryAt(res *planv1.Resources, percentile int) uint64 {
	var v uint64
	switch {
	case percentile <= 50:
		v = res.GetP50MemoryBytes()
	case percentile <= 60:
		v = res.GetP60MemoryBytes()
	case percentile <= 70:
		v = res.GetP70MemoryBytes()
	case percentile <= 80:
		v = res.GetP80MemoryBytes()
	}
	return cmp.Or(v, res.GetP95MemoryBytes(), res.GetPeakMemoryBytes())
}

// WorkerPeakWarmup is the delay before a running worker's own measured p95 memory replaces
// the provider forecast in engaged memory calculations.
//
// Rationale: Agents (such as Claude Code or Antigravity) take 1 to 2 minutes during startup to
// authenticate, inspect repository status, and spin up language servers. By 3 minutes, resident
// memory stabilizes into representative peaks. This matches Djinn's machine idle threshold
// (DefaultIdleThreshold = 3 * time.Minute).
const WorkerPeakWarmup = 3 * time.Minute

// taskRunDuration returns how long a worker has run across all its runs up to now.
func taskRunDuration(t *planv1.Task, now time.Time) time.Duration {
	if len(t.GetRuns()) > 0 {
		var total time.Duration
		for _, r := range t.GetRuns() {
			if r.GetStartTime() == nil {
				continue
			}
			rStart := r.GetStartTime().AsTime()
			var rEnd time.Time
			switch {
			case r.GetEndTime() != nil:
				rEnd = r.GetEndTime().AsTime()
			case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING:
				rEnd = now
			case t.GetEndTime() != nil:
				rEnd = t.GetEndTime().AsTime()
			default:
				rEnd = rStart
			}
			if rEnd.After(rStart) {
				total += rEnd.Sub(rStart)
			}
		}
		return total
	}
	if t.GetStartTime() != nil {
		rStart := t.GetStartTime().AsTime()
		var rEnd time.Time
		switch {
		case t.GetEndTime() != nil:
			rEnd = t.GetEndTime().AsTime()
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING:
			rEnd = now
		default:
			rEnd = rStart
		}
		if rEnd.After(rStart) {
			return rEnd.Sub(rStart)
		}
	}
	if r := t.GetRunning().AsDuration(); r > 0 {
		return r
	}
	return 0
}

// workerEngaged returns the memory engaged by a worker: what it is expected to hold (workerExpected) + margin.
func workerEngaged(t *planv1.Task, p machine.Policy, typical uint64, now time.Time) uint64 {
	return workerExpected(t, p, typical, now) + p.WorkerMargin
}

// workerExpected returns the memory a worker is expected to hold: max(its own memory at the notch's percentile,
// resident memory) once running for at least WorkerPeakWarmup or when paused with measures, and max(typical, resident
// memory) before that or without measurements, typical being its provider's at that percentile.
func workerExpected(t *planv1.Task, p machine.Policy, typical uint64, now time.Time) uint64 {
	measure := MemoryAt(t.GetResources(), p.MemoryPercentile())
	uses := t.GetResources().GetMemoryBytes()
	hasMeasure := measure > 0 || uses > 0

	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED {
		if hasMeasure {
			return max(measure, uses)
		}
		return typical
	}
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING && hasMeasure && taskRunDuration(t, now) >= WorkerPeakWarmup {
		return max(measure, uses)
	}
	return max(typical, uses)
}

// WorkerMemory returns the engaged memory (sum of peak forecasts or measured peaks for running workers)
// and actual resident memory of running workers.
func WorkerMemory(tasks []*planv1.Task, p machine.Policy, at ...time.Time) (engaged, actual uint64) {
	now := time.Now()
	if len(at) > 0 && !at[0].IsZero() {
		now = at[0]
	}
	peaks := ProviderMemory(tasks, p.MemoryPercentile())
	for _, t := range tasks {
		alive := t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED
		if !alive || Watcher(t) {
			continue
		}
		peak, _ := p.Typical(peaks[provider(t)])
		uses := t.GetResources().GetMemoryBytes()
		engaged += workerEngaged(t, p, peak, now)
		actual += uses
	}
	return engaged, actual
}

// TypicalForecast returns the measured forecast per worker of the providers in use (or all measured providers), at
// the notch's percentile (machine.Policy.Percentile), or p.WorkerForecast() when none is measured.
func TypicalForecast(tasks []*planv1.Task, p machine.Policy) uint64 {
	peaks := ProviderMemory(tasks, p.MemoryPercentile())
	if len(peaks) == 0 {
		return p.WorkerForecast()
	}
	inUse := make(map[string]bool)
	for _, t := range tasks {
		if !Watcher(t) && (t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RUNNING || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PENDING) {
			inUse[provider(t)] = true
		}
	}
	var highest uint64
	for prov := range inUse {
		if provPeaks, ok := peaks[prov]; ok && len(provPeaks) > 0 {
			typical, _ := p.Typical(provPeaks)
			forecast := typical + p.WorkerMargin
			if forecast > highest {
				highest = forecast
			}
		}
	}
	if highest > 0 {
		return highest
	}
	for _, provPeaks := range peaks {
		if len(provPeaks) > 0 {
			typical, _ := p.Typical(provPeaks)
			forecast := typical + p.WorkerMargin
			if forecast > highest {
				highest = forecast
			}
		}
	}
	if highest > 0 {
		return highest
	}
	return p.WorkerForecast()
}

// typical is the typical memory of a worker of the provider at the notch's percentile, and how many measured workers
// gave it (machine.Policy.Typical): the finished workers of that provider, the latest first.
func (s *Situation) typical(name string) (uint64, int) {
	if s.peaks == nil {
		s.peaks = ProviderMemory(s.tasks, s.machine.Policy.MemoryPercentile())
	}
	return s.machine.Policy.Typical(s.peaks[name])
}

// growing is the memory the workers running may still take beyond what they hold: for each, what it is expected to
// hold (workerExpected, as engaged counts it) less what it uses now (all of it before its first reading), and the
// typical memory of its provider for each worker this pass started, question workers included. A paused worker keeps
// its memory.
func (s *Situation) growing() uint64 {
	var sum uint64
	now := cmp.Or(s.now, time.Now())
	for _, t := range s.tasks {
		alive := (s.taskRunning(t) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED) && !Watcher(t)
		if !alive && !s.started[t.GetId()] {
			continue
		}
		peak, _ := s.typical(provider(t))
		if !alive {
			sum += peak
			continue
		}
		if expected, uses := workerExpected(t, s.machine.Policy, peak, now), t.GetResources().GetMemoryBytes(); uses < expected {
			sum += expected - uses
		}
	}
	return sum
}

// resident is the memory the workers running hold now, as last read: the memory available already leaves it out.
func (s *Situation) resident() uint64 {
	var sum uint64
	for _, t := range s.tasks {
		if (s.taskRunning(t) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED) && !Watcher(t) {
			sum += t.GetResources().GetMemoryBytes()
		}
	}
	return sum
}

// Engaged is the sum of peak forecasts or measured peaks of the workers running or started in this pass.
func (s *Situation) Engaged() uint64 {
	return s.engaged()
}

// engaged is the sum of peak forecasts or measured peaks of the workers running or started in this pass.
// A paused worker keeps its memory.
func (s *Situation) engaged() uint64 {
	if s.machine == nil {
		return 0
	}
	var sum uint64
	now := s.now
	if now.IsZero() {
		now = time.Now()
	}
	for _, t := range s.tasks {
		alive := (s.taskRunning(t) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED) && !Watcher(t)
		if !alive && !s.started[t.GetId()] {
			continue
		}
		peak, _ := s.typical(provider(t))
		sum += workerEngaged(t, s.machine.Policy, peak, now)
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
