// Package dispatch decides which planned task starts now: the scheduler's rules, in plain Go, apart from the store
// and the workers. A pass reads a Situation (the tasks, the wishes, the projects, the machine) and gives a Decision
// for each planned task: start it, let it wait with its reason, or fail it. The harness acts on the decisions; the
// dispatch bench (internal/dispatch/bench) feeds it cases written by hand.
package dispatch

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/usage"
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
	// Gates is the number of gates held outside the running workers: each takes a slot. Merges is how many of them
	// Djinn holds to merge a task's work, settle a merge or check a push.
	Gates, Merges int
	// Keep keeps one slot free for the gates of merges and pushes, when the machine has more than one (Q26): a new
	// worker never takes it, so a merge or a push never waits behind workers on a full machine. A merge's gate held
	// outside the workers takes that slot. djinn up keeps it once its gates are served.
	Keep bool
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
	place   map[string]int // position of each azima among its wish's (plan.AzimaPlaces)
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
	// questionCode is the code of a question by its identifier (Q51), for the reason of a task whose dependency waits
	// on a blocking question; "" when unknown.
	questionCode func(id string) string
	// network is what the network allows now (W285); nil: it holds nothing back.
	network *Network
}

// Network is what the network allows now (W285).
type Network struct {
	// Down says since when the network is down, or that it may be while Djinn probes it: no worker starts then, a
	// watcher neither. "" when it is up.
	Down string
	// OnePerPass: the network is back, and the work the outage stopped resumes one task per pass, so that its workers
	// do not all call their providers at once; the tasks after it in the order wait their turn. Spent: the harness
	// resumed a paused worker in this pass already, and no task starts besides.
	OnePerPass, Spent bool
}

// WithNetwork tells the situation what the network allows now; nil: nothing holds back.
func (s *Situation) WithNetwork(n *Network) *Situation {
	s.network = n
	return s
}

// networkHeld says why the network holds back t, which would start: it is down, or back and a task resumed in this
// pass already; "" when it holds nothing back. A watcher takes no slot: it waits only while the network is down.
func (s *Situation) networkHeld(t *planv1.Task) string {
	n := s.network
	switch {
	case n == nil:
		return ""
	case n.Down != "":
		return n.Down
	case n.OnePerPass && n.Spent && !Watcher(t):
		return "the network is back: Djinn resumes the work it stopped, one task per pass"
	}
	return ""
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

// WithQuestions equips the situation with what names a question by its identifier: a task whose dependency waits on
// a blocking question (Task.wait_question_id) waits "for Q51".
func (s *Situation) WithQuestions(code func(id string) string) *Situation {
	s.questionCode = code
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
	s.place = plan.AzimaPlaces(tasks)
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
	// It waits for room on the machine: Machine, or a task the memory holds goes before it (Task.room_wait). Neither
	// its wish, a dependency, a push hold, a usage limit nor a write scope holds it.
	Room bool
	// It waits for a slot alone: the machine is not under pressure and its memory holds it. An investigator, an
	// investigation, a merge or a repair pauses a work task for it (the harness's preemptForSlot): it takes that task's
	// slot, never one more.
	Slot bool
	// What holds it while it waits (Task.hold): unspecified when it starts or fails.
	Hold planv1.TaskHold
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

// Ifrit tells whether the task is an ifrit (TASK_KIND_IFRIT): a worker that stays lit while the developer is there.
// The developer waits for it: it starts beyond the slots, as a task started by hand does, and only the machine's
// pressure or its memory hold it.
func Ifrit(t *planv1.Task) bool { return t.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT }

// Investigation tells whether the task is an investigation (TASK_KIND_INVESTIGATION): a short study or audit that ends
// in a tilasm and a question, its commits never merged. It works in a worktree of its own, so it takes a slot like any
// worker; it is served first, as an investigator is (ServedFirst), at most InvestigationCap of them at once.
func Investigation(t *planv1.Task) bool {
	return t.GetKind() == planv1.TaskKind_TASK_KIND_INVESTIGATION
}

// Mockup tells whether the task is a mockup task (TASK_KIND_MOCKUP): framed like an investigation, it puts a tilasm's
// next version, then revises the question it iterates on (Task.question).
func Mockup(t *planv1.Task) bool { return t.GetKind() == planv1.TaskKind_TASK_KIND_MOCKUP }

// ChangesNoCode tells whether the task's work never reaches its project's code: an investigation or a mockup, whose
// commits Djinn never merges. The other workers' state holds none of them: neither its wish's red push checks, nor a
// merge in conflict or its repair (pushHeld), nor the merge freeze while a branch is repaired (it merges nothing), nor
// a write scope; and its work never holds a task that depends on it once done.
func ChangesNoCode(t *planv1.Task) bool { return Investigation(t) || Mockup(t) }

// ServedFirst tells whether the task is served before the planned work, and may take, on a full machine, the slot of a
// work task it pauses (Decision.Slot): an investigator (Enlighten me), an investigation or a mockup. None is held by
// the paused workers that resume first, nor preempted itself.
func ServedFirst(t *planv1.Task) bool {
	return t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR || ChangesNoCode(t)
}

// InvestigationCap is how many investigations and mockups run at once on a machine of slots: one on a machine of three
// slots or fewer, two above, so that they never take more than a third of the work's slots, and never starve it.
func InvestigationCap(slots int) int {
	if slots <= 3 {
		return 1
	}
	return 2
}

// Light tells whether the task writes nothing in its project's folder: a watcher, or a question worker. Only a watcher
// takes no slot.
func Light(t *planv1.Task) bool { return Watcher(t) || QuestionWorker(t) }

// Resuming tells whether the task is one Djinn resumes.
func Resuming(t *planv1.Task) bool { return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING }

// Urgent tells whether the lead marked the task urgent (Task.urgent) and it is still to run or runs: planned, waiting,
// running or paused. While planned it goes before every other planned task, takes a slot whatever the machine, as
// djinn task start does, and its integration branch's red push checks do not hold it. A watcher or an azima never is:
// neither takes a slot. It is of one of two classes: a repair, which unblocks the wish and goes first, or a task for
// the developer's quick feedback (Feedback). At most one task of a wish is urgent of each class at a time (UrgentOf).
func Urgent(t *planv1.Task) bool {
	if !t.GetUrgent() || Watcher(t) || plan.IsAzima(t) {
		return false
	}
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.TaskStatus_TASK_STATUS_RUNNING,
		planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_WAITING:
		return true
	}
	return false
}

// Feedback tells whether the task is urgent for the developer's quick feedback (Task.feedback): a mockup or a change
// they asked to see, after the wish's urgent repair.
func Feedback(t *planv1.Task) bool { return Urgent(t) && t.GetFeedback() }

// UrgentOf is the urgent task of the wish among tasks (Urgent) of the class feedback says (Feedback, or a repair), other
// than the task except; nil when none is.
func UrgentOf(tasks []*planv1.Task, wishID, except string, feedback bool) *planv1.Task {
	for _, t := range tasks {
		if t.GetWishId() == wishID && t.GetId() != except && Urgent(t) && t.GetFeedback() == feedback {
			return t
		}
	}
	return nil
}

// Pass decides for every planned task, in Order: the first wish of the rank first, then its first azima, then the
// oldest. Each task it starts
// counts as writing and takes a slot for the ones after it. An urgent task starts whatever the machine (Urgent).
func (s *Situation) Pass() []Decision {
	var out []Decision
	for _, t := range s.Order() {
		why, failed, hold := s.held(t)
		machine, room, slot := false, false, false
		if why == "" && failed == "" {
			if why = s.networkHeld(t); why != "" {
				machine, room = true, true // djinn task start may start it all the same.
				hold = planv1.TaskHold_TASK_HOLD_NETWORK
			} else {
				var first bool
				if why, first, slot = s.full(t); why == "" {
					s.Start(t)
					if s.network != nil && s.network.OnePerPass && !Watcher(t) {
						s.network.Spent = true
					}
				}
				room = why != ""
				machine = room && !first
				if room {
					hold = planv1.TaskHold_TASK_HOLD_PLACE
				}
			}
		}
		if failed != "" {
			hold = planv1.TaskHold_TASK_HOLD_UNSPECIFIED
		}
		out = append(out, Decision{Task: t, Why: why, Failed: failed, Commit: why != "" && s.awaitsCommit(t),
			Machine: machine, Room: room, Slot: slot, Hold: hold})
	}
	return out
}

// Order is the planned tasks in the strict tiered order, the urgent tasks before all (Urgent), a repair before a task
// for feedback:
//  1. Investigator workers (Enlighten me), followed by answer workers, then investigations and mockups;
//  2. Correction workers, followed by review workers;
//  3. Resumed work tasks (RESUMING: limits, restarts, preemptions);
//  4. Work tasks: the first wish of the rank first, then the work of the first azima of its rank (Task.rank, work of
//     no azima after it), then the tasks that unblock others (graph's critical path: most planned and waiting
//     dependents first), then the others.
//
// Within the first tiers and their sub-levels, ties are broken by the wish's rank, then its azimas' rank; then, in
// every tier, the oldest first, then by task ID. The tasks of a wish that is not active come last; Blocker keeps them
// waiting.
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
	// place is the position of the task's azima among its wish's: work of no azima comes after the azimas'.
	place := func(t *planv1.Task) int {
		if p, ok := s.place[t.GetPartOf()]; ok {
			return p
		}
		return math.MaxInt
	}
	slices.SortStableFunc(out, func(a, b *planv1.Task) int {
		aLevel, aSub := taskPriority(a, depCounts)
		bLevel, bSub := taskPriority(b, depCounts)
		ranks := cmp.Or(cmp.Compare(pos(a.GetWishId()), pos(b.GetWishId())), cmp.Compare(place(a), place(b)))
		if aLevel == workLevel && bLevel == workLevel {
			// The developer's order comes before the critical path: a higher azima's work before what unblocks a lower one's.
			ranks, aSub, bSub = cmp.Or(ranks, cmp.Compare(aSub, bSub)), 0, 0
		}
		return cmp.Or(
			cmp.Compare(aLevel, bLevel),
			cmp.Compare(aSub, bSub),
			ranks,
			a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()),
			strings.Compare(a.GetId(), b.GetId()),
		)
	})
	return out
}

// workLevel is the tier of the work tasks (taskPriority): the ones that unblock others, then the others, within the
// order of the wishes and of their azimas.
const workLevel = 4

func taskPriority(t *planv1.Task, depCounts map[string]int) (level int, sub int) {
	switch {
	case Feedback(t):
		return 0, 1
	case Urgent(t):
		return 0, 0
	case t.GetRole() == planv1.TaskRole_TASK_ROLE_INVESTIGATOR:
		return 1, 0
	case QuestionWorker(t):
		return 1, 1
	case ChangesNoCode(t):
		return 1, 2
	case t.GetCorrection() != nil:
		return 2, 0
	case t.GetReview() != nil:
		return 2, 1
	case Resuming(t):
		return 3, 0
	default:
		return workLevel, -depCounts[t.GetId()]
	}
}

// isWaitingWorkTask tells whether a task is a planned or waiting work task in the dependency graph.
func isWaitingWorkTask(t *planv1.Task) bool {
	if plan.IsAzima(t) || Watcher(t) || QuestionWorker(t) || ChangesNoCode(t) {
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
	why, failed, _ = s.held(t)
	return why, failed
}

// held is Held, and what holds the task (Task.hold) when it waits.
func (s *Situation) held(t *planv1.Task) (why, failed string, hold planv1.TaskHold) {
	if wish, ok := s.wishes[t.GetWishId()]; ok && !plan.Active(wish) {
		return "its wish is " + strings.ToLower(strings.TrimPrefix(wish.GetState().String(), "WISH_STATE_")), "",
			planv1.TaskHold_TASK_HOLD_WISH
	}
	hold = planv1.TaskHold_TASK_HOLD_DEPENDENCY
	for _, id := range t.GetDependsOn() {
		d, ok := s.byID[id]
		if !ok {
			return "", "its dependency " + id + " is gone", 0
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
			// An investigation's or a mockup's work never reaches the branch (ChangesNoCode): done, it is done.
			if !plan.IsAzima(d) && !ChangesNoCode(d) && !integrationDropped(d) && d.GetBranch() != "" &&
				d.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
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
			return "", fmt.Sprintf("its dependency %s ended %s", ended, status(d.GetStatus())), 0
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			// Its worker asked a blocking question: the answer continues it, and this one waits for that answer.
			if code := s.waitCode(d); code != "" {
				if why == "" {
					why, hold = fmt.Sprintf("waits for %s, which %s waits for", code, d.GetCode()), planv1.TaskHold_TASK_HOLD_QUESTION
				}
				continue
			}
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
		return why, "", hold
	}
	if why := s.pushHeld(t); why != "" {
		return why, "", planv1.TaskHold_TASK_HOLD_PUSH
	}
	if Watcher(t) {
		return "", "", 0 // It sleeps until its command prints: no slot, and it writes nothing.
	}
	// In Git, worktrees separate the writers; a question worker writes nothing, nor does work that changes no code.
	if id := t.GetProjectId(); id != "" && !s.git[id] && !QuestionWorker(t) && !ChangesNoCode(t) {
		for _, o := range s.tasks {
			if o.GetId() == t.GetId() || o.GetProjectId() != id || !s.writing(o) {
				continue
			}
			if Overlap(t.GetWriteScopes(), o.GetWriteScopes()) {
				return fmt.Sprintf("%s writes %s, which overlaps %s", o.GetCode(), ScopeText(o.GetWriteScopes()),
					ScopeText(t.GetWriteScopes())), "", planv1.TaskHold_TASK_HOLD_PLACE
			}
		}
	}
	if why := s.limited(t); why != "" {
		return why, "", planv1.TaskHold_TASK_HOLD_LIMIT
	}
	return "", "", 0
}

// waitCode is the code of the blocking question d waits on; "" when it waits on none, or when it is not known.
func (s *Situation) waitCode(d *planv1.Task) string {
	id := d.GetWaitQuestionId()
	if id == "" {
		return ""
	}
	if s.questionCode != nil {
		if code := s.questionCode(id); code != "" {
			return code
		}
	}
	return "its question"
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
func pushCheckHeld(p *planv1.PushHold) (sha, check string) {
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

// pushHeld tells why t, a task that starts a worker, waits for its wish's integration branch in its project: the push
// is held on red push checks, or a merge into the branch is in conflict or red and a correction repairs it (Q44, W233:
// a new worker would start from the branch without that work, its own merge conflicting in turn); "" when neither
// holds it. A correction, a review, a question worker, an urgent task, which the lead marks to repair the branch or for
// quick feedback, and an investigation or a mockup, whose work is never merged (ChangesNoCode), never wait; neither do
// the workers Djinn resumes (after a restart, a usage limit, a network outage: they were running, and go on, W371), nor
// a watcher, which starts from no branch and merges nothing. The workers already running go on.
func (s *Situation) pushHeld(t *planv1.Task) string {
	if plan.IsMerge(t) || QuestionWorker(t) || Urgent(t) || ChangesNoCode(t) || Resuming(t) || Watcher(t) {
		return ""
	}
	wish := s.wishes[t.GetWishId()]
	if wish == nil || t.GetProjectId() == "" {
		return ""
	}
	// Each branch held keeps its hold (W37): the correction of any of them goes on; the first by name says why.
	if holds := plan.PushHolds(plan.PushStateOf(wish, t.GetProjectId())); len(holds) > 0 &&
		!slices.ContainsFunc(holds, func(p plan.BranchHold) bool { return p.GetCorrectedBy() == t.GetId() }) {
		sha, check := pushCheckHeld(holds[0].PushHold)
		return fmt.Sprintf("waits for %s to be green again (push checks red at %s: %s)", holds[0].Branch, sha, check)
	}
	for _, c := range s.tasks {
		if c.GetWishId() == t.GetWishId() && strings.EqualFold(c.GetProjectId(), t.GetProjectId()) && repairing(c) {
			return fmt.Sprintf("waits for %s to %s", c.GetCode(), s.repairs(c))
		}
	}
	return ""
}

// repairing tells whether c, a correction worker, still repairs its branch: it is planned, runs or waits to, or it
// ended done and its work waits to be merged, or reviewed (the harness's repairing, without the stop's grace). One
// that failed, was cancelled, stopped or cut short is over, the correction after it, if any, repairing.
func repairing(c *planv1.Task) bool {
	if c.GetCorrection() == nil {
		return false
	}
	switch c.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_CANCELLED, planv1.TaskStatus_TASK_STATUS_STOPPED,
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		return false
	case planv1.TaskStatus_TASK_STATUS_DONE:
		switch in := c.GetIntegration(); in.GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
			return true
		case planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED:
			return in.GetReviewedBy() != ""
		}
		return false
	}
	return true
}

// repairs says what c, a correction worker repairing a branch, does to it: "settle the conflict in feat/qol", "settle
// the merge of main into feat/qol", "repair the red checks of feat/qol".
func (s *Situation) repairs(c *planv1.Task) string {
	f := c.GetCorrection().GetFailure()
	branch := ""
	for _, id := range f.GetTaskIds() {
		if d := s.byID[id]; d != nil && d.GetIntegration().GetBranch() != "" {
			branch = d.GetIntegration().GetBranch()
			break
		}
	}
	branch = cmp.Or(branch, s.integrationBranch(c))
	switch {
	case f.GetMainSha() != "":
		return "settle the merge of main into " + branch
	case f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_CONFLICT:
		return "settle the conflict in " + branch
	case f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED:
		return "repair the red checks of " + branch
	}
	return "repair " + branch
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

var (
	// limitURLRe matches HTTP(S) URLs so a documentation link (such as .../models/standard-paygo) is never read as a model.
	limitURLRe = regexp.MustCompile(`(?i)\bhttps?://\S+`)
	// limitModelRe extracts a model candidate introduced as a model in a limit message or wait reason.
	limitModelRe = regexp.MustCompile(`(?i)(?:\b(?:for|on)\s+model\s+(?:'([^']+)'|"([^"]+)"|([a-z0-9][a-z0-9._/-]*))|\bmodel(?:\s*[:=]|\s+)(?:'([^']+)'|"([^"]+)"|([a-z0-9][a-z0-9._/-]*))|(?:^|[\s'"=(])models/([a-z0-9][a-z0-9._-]*)\b)`)
	// limitTokenRe scans individual tokens in a limit message when a known model identifier appears directly.
	limitTokenRe = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._/-]*`)
	// canonicalModelRe matches canonical model identifiers of Claude, Gemini/Antigravity, and OpenAI/Codex.
	canonicalModelRe = regexp.MustCompile(`^(?:claude-(?:opus|sonnet|haiku|fable)-[a-z0-9._-]+|claude-[0-9][a-z0-9._-]*|gemini-[0-9][a-z0-9._-]*|gpt-[0-9][a-z0-9._-]*|o[134](?:-[a-z0-9._-]+)?|codex-[a-z0-9._-]+)$`)
)

// knownAgentModels maps lowercase model identifiers and display labels (including `agy models`) to their canonical model ID.
var knownAgentModels = map[string]string{
	"gemini-4-argon-high":       "gemini-4-argon-high",
	"gemini 4 argon (high)":     "gemini-4-argon-high",
	"gemini-4-argon":            "gemini-4-argon-high",
	"gemini 4 argon":            "gemini-4-argon-high",
	"gemini-4-argon-medium":     "gemini-4-argon-medium",
	"gemini 4 argon (medium)":   "gemini-4-argon-medium",
	"gemini-4-argon-low":        "gemini-4-argon-low",
	"gemini 4 argon (low)":      "gemini-4-argon-low",
	"gemini-3.8-flash-high":     "gemini-3.8-flash-high",
	"gemini 3.8 flash (high)":   "gemini-3.8-flash-high",
	"gemini-3.8-flash-medium":   "gemini-3.8-flash-medium",
	"gemini 3.8 flash (medium)": "gemini-3.8-flash-medium",
	"gemini-3.8-flash-low":      "gemini-3.8-flash-low",
	"gemini 3.8 flash (low)":    "gemini-3.8-flash-low",
	"gemini-3.7-flash-high":     "gemini-3.7-flash-high",
	"gemini 3.7 flash (high)":   "gemini-3.7-flash-high",
	"gemini-3.7-flash-medium":   "gemini-3.7-flash-medium",
	"gemini 3.7 flash (medium)": "gemini-3.7-flash-medium",
	"gemini-3.7-flash-low":      "gemini-3.7-flash-low",
	"gemini 3.7 flash (low)":    "gemini-3.7-flash-low",
	"gemini-3.6-flash-high":     "gemini-3.6-flash-high",
	"gemini 3.6 flash (high)":   "gemini-3.6-flash-high",
	"gemini-3.6-flash-medium":   "gemini-3.6-flash-medium",
	"gemini 3.6 flash (medium)": "gemini-3.6-flash-medium",
	"gemini-3.6-flash-low":      "gemini-3.6-flash-low",
	"gemini 3.6 flash (low)":    "gemini-3.6-flash-low",
	"gemini-3.1-pro-high":       "gemini-3.1-pro-high",
	"gemini 3.1 pro (high)":     "gemini-3.1-pro-high",
	"gemini-3.1-pro-low":        "gemini-3.1-pro-low",
	"gemini 3.1 pro (low)":      "gemini-3.1-pro-low",
}

// CanonicalModel returns the canonical model identifier when raw names a recognized agent model (from `agy models`, the
// pricing table, or a provider's model family), and "" when raw is empty, a URL, or not a model name (such as
// "standard-paygo").
func CanonicalModel(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "'\".,;:")
	if s == "" || strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "www.") {
		return ""
	}
	lower := strings.ToLower(s)
	if m, ok := knownAgentModels[lower]; ok {
		return m
	}
	if table, err := usage.DefaultTable(); err == nil {
		for id, m := range table.Models {
			if strings.EqualFold(s, id) || (m.Label != "" && strings.EqualFold(s, m.Label)) {
				return strings.ToLower(id)
			}
		}
	}
	s = strings.Trim(s, "()[]{}<>")
	lower = strings.ToLower(s)
	for _, prefix := range []string{"models/", "google/", "anthropic/", "openai/"} {
		if strings.HasPrefix(lower, prefix) {
			lower = strings.TrimPrefix(lower, prefix)
			break
		}
	}
	if lower == "" || strings.Contains(lower, "/") {
		return ""
	}
	if m, ok := knownAgentModels[lower]; ok {
		return m
	}
	if table, err := usage.DefaultTable(); err == nil {
		for id := range table.Models {
			if lower == strings.ToLower(id) {
				return lower
			}
		}
	}
	if canonicalModelRe.MatchString(lower) {
		return lower
	}
	return ""
}

// LimitModel returns the model name a limit message or wait reason proves exhausted, or "" when the message names no
// model. It never reads a model from a URL.
func LimitModel(text string) string {
	clean := limitURLRe.ReplaceAllString(text, " ")
	for _, m := range limitModelRe.FindAllStringSubmatch(clean, -1) {
		for _, g := range m[1:] {
			if g != "" {
				if model := CanonicalModel(g); model != "" {
					return model
				}
			}
		}
	}
	for _, tok := range limitTokenRe.FindAllString(clean, -1) {
		if model := CanonicalModel(tok); model != "" {
			return model
		}
	}
	return ""
}

// taskModel is the model a task runs on, when known: its chosen Model, or the last model recorded in Models.
func taskModel(t *planv1.Task) string {
	if m := strings.TrimSpace(t.GetModel()); m != "" {
		return m
	}
	if ms := t.GetModels(); len(ms) > 0 {
		return strings.TrimSpace(ms[len(ms)-1])
	}
	return ""
}

// limited says why the task waits for a usage limit: its own, until it resets, or a model's limit that another task of
// the same provider on that model hit and still waits for; "" when none holds. A limit message that names no model
// holds only the task that hit it, never every task of the provider (W409).
func (s *Situation) limited(t *planv1.Task) string {
	if Resuming(t) && t.GetResumeAfter() != nil && t.GetResumeAfter().AsTime().After(s.now) {
		return cmp.Or(t.GetWaitReason(), "its provider's usage limit")
	}
	tm := taskModel(t)
	if tm == "" {
		return ""
	}
	tm = cmp.Or(CanonicalModel(tm), tm)
	var holder *planv1.Task
	for _, o := range s.tasks {
		if o.GetId() == t.GetId() || !Resuming(o) || o.GetResumeAfter() == nil || !o.GetResumeAfter().AsTime().After(s.now) ||
			provider(o) != provider(t) {
			continue
		}
		m := LimitModel(cmp.Or(o.GetWaitReason(), o.GetError()))
		if m == "" || !strings.EqualFold(tm, m) {
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
// of its provider; an urgent task always may. Once the memory holds a task, the ones after it wait for it: they keep their order.
// first tells that the task waits for one before it in that order, not for the machine: djinn task start never jumps it.
// slot tells that it waits for a slot alone, its memory holding it: an investigator, a merge or a repair may take the
// slot of a work task it pauses.
func (s *Situation) full(t *planv1.Task) (why string, first, slot bool) {
	m := s.machine
	if m == nil || Watcher(t) { // A watcher sleeps until its command prints: no slot.
		return "", false, false
	}
	if Urgent(t) { // The lead marked it urgent: it takes a slot whatever the others, as djinn task start does.
		return "", false, false
	}
	if m.Pressure != "" {
		return "the machine is under pressure: " + m.Pressure, false, false
	}
	if ChangesNoCode(t) {
		if n, most := s.investigations(), InvestigationCap(m.Slots); n >= most {
			return fmt.Sprintf("%s run, the most investigations this machine runs at once, mockups counted (%d of %d slots)",
				plural(n, "investigation"), most, m.Slots), false, false
		}
	}
	// A slot each for the workers running, the ones this pass started, the gates held outside them, and the paused
	// workers that resume first, except before an investigator, an investigation, a merge or a repair.
	takes := ServedFirst(t) || plan.IsMerge(t)
	resuming := m.Resuming
	if takes {
		resuming = 0
	}
	// One slot kept for the merges and pushes, on a machine of more than one, unless a merge's gate already takes it.
	kept := 0
	if m.Keep && m.Slots > 1 && m.Merges == 0 {
		kept = 1
	}
	if running := m.Running + len(s.started); running+m.Gates+kept+resuming >= m.Slots {
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
		if kept > 0 {
			workers += ", 1 slot is kept for merges and pushes"
		}
		switch resuming {
		case 0:
		case 1:
			workers += ", 1 paused worker resumes first"
		default:
			workers += fmt.Sprintf(", %d paused workers resume first", resuming)
		}
		why = workers + ", the most this machine holds (" + m.Rule + ")"
		if !takes && !Ifrit(t) {
			return why, false, false
		}
		// An investigator, an investigation, a merge or a repair may take the slot of a work task it pauses (W233: a
		// merge before any work, which would start on the branch it settles), and an ifrit starts beyond the slots:
		// only where the memory holds it.
		if room, first := s.room(t); room != "" || Ifrit(t) {
			return room, first, false
		}
		return why, false, true
	}
	room, first := s.room(t)
	return room, first, false
}

// investigations counts the investigations and the mockups that run now, or that this pass started: InvestigationCap
// bounds them.
func (s *Situation) investigations() int {
	n := 0
	for _, o := range s.tasks {
		if ChangesNoCode(o) && (s.taskRunning(o) || s.started[o.GetId()]) {
			n++
		}
	}
	return n
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
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
		case Investigation(s.short):
			return fmt.Sprintf("the investigation %s goes first: %s", s.short.GetCode(), s.shortWhy), true
		case Mockup(s.short):
			return fmt.Sprintf("the mockup %s goes first: %s", s.short.GetCode(), s.shortWhy), true
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
// memory stabilizes into representative peaks.
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
