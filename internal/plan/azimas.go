package plan

// Azimas are the tasks of kind AZIMA (Arabic ʿazīma, the incantation that binds and commands a djinn): the plan of a
// wish as a graph, which no worker runs. Work is part of an azima (Task.part_of), and an azima depends on other tasks
// like work does. Where an azima stands is computed on every read (FillAzimas). A project's plan/ folder holds one Markdown file per azima, its front matter read by djinn plan sync
// (ReadAzimaFiles), which writes back what the azima depends on (WriteAfter).

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// IsAzima tells whether the task is an azima: no worker ever runs it.
func IsAzima(t *planv1.Task) bool { return t.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA }

// FillAzimas sets Task.azima on the azimas among tasks, from the tasks of their wish among tasks: give it every task of
// the wishes read. An azima is done when its status is and none of its parts is still to finish: a part that runs or
// waits keeps it in progress, whatever its plan file says. It is in progress when one of its parts has a worker on it
// or is done, or an azima part of it is under way; open otherwise. In progress with a watcher as its only live work
// (onlyWatched: the babysit of its pull request), it is watching. Once every part is finished, its azimas done or to
// validate in their turn, it is to validate (AWAITING_PROOF): nothing is left for Djinn, and the person checks its
// plan file's boxes (Task.proof_needs say what each one needs) and validates it. It is ready when every task it
// depends on is done.
func FillAzimas(tasks []*planv1.Task) {
	byID := make(map[string]*planv1.Task, len(tasks))
	parts := map[string][]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
		if p := t.GetPartOf(); p != "" {
			parts[p] = append(parts[p], t)
		}
	}
	// under says whether an azima is under way, through its parts and theirs; seen guards a cycle the store refuses.
	var under func(id string, seen map[string]bool) bool
	under = func(id string, seen map[string]bool) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, p := range parts[id] {
			if p.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || working(p) || IsAzima(p) && under(p.GetId(), seen) {
				return true
			}
		}
		return false
	}
	// state is where an azima stands, its azima parts first; a cycle the store refuses reads as in progress.
	states := map[string]planv1.AzimaState{}
	var state func(t *planv1.Task) planv1.AzimaState
	state = func(t *planv1.Task) planv1.AzimaState {
		if s, ok := states[t.GetId()]; ok {
			return s
		}
		states[t.GetId()] = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
		s := planv1.AzimaState_AZIMA_STATE_OPEN
		// unfinished tells whether a part is still to finish: work not finished, or an azima not done.
		unfinished := slices.ContainsFunc(parts[t.GetId()], func(p *planv1.Task) bool {
			if IsAzima(p) {
				return state(p) != planv1.AzimaState_AZIMA_STATE_DONE
			}
			return !Finished(p) || taskMergingOrFailed(p)
		})
		// left tells whether a part is still to finish: work not finished, or an azima neither done nor to validate.
		left := slices.ContainsFunc(parts[t.GetId()], func(p *planv1.Task) bool {
			if IsAzima(p) {
				ps := state(p)
				return ps != planv1.AzimaState_AZIMA_STATE_DONE && ps != planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
			}
			return !Finished(p) || taskMergingOrFailed(p)
		})
		switch {
		case t.GetDraft():
			s = planv1.AzimaState_AZIMA_STATE_DRAFT
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !unfinished:
			s = planv1.AzimaState_AZIMA_STATE_DONE
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE:
			// Closed, but work part of it still runs or waits: in progress until it ends.
			s = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
		case under(t.GetId(), map[string]bool{}):
			s = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
			if !left {
				s = planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
			}
		}
		if s == planv1.AzimaState_AZIMA_STATE_IN_PROGRESS && onlyWatched(parts[t.GetId()], state) {
			s = planv1.AzimaState_AZIMA_STATE_WATCHING
		}
		states[t.GetId()] = s
		return s
	}
	for _, t := range tasks {
		if !IsAzima(t) {
			t.Azima = nil
			continue
		}
		e := &planv1.Azima{
			State:  state(t),
			Ready:  true,
			Branch: AzimaBranchName(t),
		}
		// What Djinn records of its branch and pull request stays: its branch is cut from main, never from another's.
		if existing := t.GetAzima(); existing != nil {
			e.Sync = existing.GetSync()
			e.Pr = existing.GetPr()
			e.Main = existing.GetMain()
			if existing.GetCutFrom() != "" {
				e.CutFrom, e.BaseBranch, e.Named = existing.GetCutFrom(), existing.GetBaseBranch(), existing.GetNamed()
			}
		}
		// A merge counts with the original task it serves (MergeHost): it runs, its original runs.
		running := map[string]bool{}
		for _, p := range parts[t.GetId()] {
			if h := MergeHost(p, byID); h != "" {
				running[h] = running[h] || working(p) || taskMerging(p)
				continue
			}
			// A cancelled part is no work left: neither in the total nor waiting.
			if p.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED && !IsAzima(p) {
				continue
			}
			e.Parts++
			if IsAzima(p) {
				if state(p) == planv1.AzimaState_AZIMA_STATE_DONE {
					e.PartsDone++
				}
			} else if p.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !taskMergingOrFailed(p) {
				e.PartsDone++
			}
			running[p.GetId()] = running[p.GetId()] || working(p) || taskMerging(p)
		}
		for _, r := range running {
			if r {
				e.PartsRunning++
			}
		}
		for _, id := range t.GetDependsOn() {
			dep := byID[id]
			depDone := false
			if dep != nil {
				if IsAzima(dep) {
					depDone = state(dep) == planv1.AzimaState_AZIMA_STATE_DONE
				} else {
					depDone = (dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !taskMergingOrFailed(dep)) || dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED
				}
			}
			if !depDone {
				e.Ready = false
			}
		}
		t.Azima = e
	}
}

// watches tells a watcher (Provider.PROVIDER_WATCH): a task that watches, such as the babysit of a pull request, and
// has nothing to commit (dispatch.Watcher).
func watches(t *planv1.Task) bool { return t.GetProvider() == planv1.Provider_PROVIDER_WATCH }

// onlyWatched tells whether an azima's only live work, among its parts, is a watcher: a watcher part runs, or an azima
// part is watching, and no work part runs, waits or is left to merge, every other azima part done or to validate. A
// watcher that does not run is no work either. state is where an azima part stands.
func onlyWatched(parts []*planv1.Task, state func(*planv1.Task) planv1.AzimaState) bool {
	watched := false
	for _, p := range parts {
		switch {
		case IsAzima(p):
			switch state(p) {
			case planv1.AzimaState_AZIMA_STATE_WATCHING:
				watched = true
			case planv1.AzimaState_AZIMA_STATE_DONE, planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF:
			default:
				return false
			}
		case watches(p):
			watched = watched || working(p)
		case !Finished(p) || taskMergingOrFailed(p):
			return false
		}
	}
	return watched
}

// accents are folded to their letter in a slug.
var accents = strings.NewReplacer(
	"\u00e0", "a", "\u00e2", "a", "\u00e4", "a", "\u00e1", "a", "\u00e7", "c", "\u00e9", "e", "\u00e8", "e", "\u00ea", "e", "\u00eb", "e",
	"\u00ee", "i", "\u00ef", "i", "\u00ed", "i", "\u00f4", "o", "\u00f6", "o", "\u00f3", "o", "\u00f9", "u", "\u00fb", "u", "\u00fc", "u", "\u00fa", "u", "\u00f1", "n",
)

// Slug keeps lower-case letters and digits, joined by single dashes, at most n characters cut on a dash.
func Slug(s string, n int) string {
	s = accents.Replace(strings.ToLower(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > n {
		out = out[:n]
		if i := strings.LastIndexByte(out, '-'); i > 0 {
			out = out[:i]
		}
	}
	return strings.Trim(out, "-")
}

// AzimaBranchName is the branch of an azima under the per-azima push strategy: the one recorded when Djinn cut it or
// the developer named it (Azima.cut_from), else djinn/<code>-<slug of its title>, or djinn/<code>.
func AzimaBranchName(a *planv1.Task) string {
	if a == nil {
		return ""
	}
	if e := a.GetAzima(); e.GetCutFrom() != "" && e.GetBranch() != "" {
		return e.GetBranch()
	}
	s := Slug(a.GetTitle(), 40)
	if s != "" {
		return fmt.Sprintf("djinn/%s-%s", a.GetCode(), s)
	}
	return fmt.Sprintf("djinn/%s", a.GetCode())
}

// DependencyAzimas finds all unique azimas that azima (or its work tasks) depends on among tasks.
func DependencyAzimas(azima *planv1.Task, tasks []*planv1.Task) []*planv1.Task {
	if azima == nil {
		return nil
	}
	taskMap := make(map[string]*planv1.Task, len(tasks))
	parts := make(map[string][]*planv1.Task)
	for _, t := range tasks {
		taskMap[t.GetId()] = t
		if p := t.GetPartOf(); p != "" {
			parts[p] = append(parts[p], t)
		}
	}

	azimaMemo := make(map[string]*planv1.Task, len(tasks))
	findAzima := func(taskID string) *planv1.Task {
		if a, ok := azimaMemo[taskID]; ok {
			return a
		}
		curr := taskMap[taskID]
		visited := make(map[string]bool)
		var res *planv1.Task
		for curr != nil && !visited[curr.GetId()] {
			visited[curr.GetId()] = true
			if IsAzima(curr) {
				res = curr
				break
			}
			if curr.GetPartOf() == "" {
				break
			}
			curr = taskMap[curr.GetPartOf()]
		}
		for id := range visited {
			azimaMemo[id] = res
		}
		return res
	}

	depAzimaMap := make(map[string]*planv1.Task)
	for _, depID := range azima.GetDependsOn() {
		if a := findAzima(depID); a != nil && a.GetId() != azima.GetId() {
			depAzimaMap[a.GetId()] = a
		}
	}

	var checkParts func(parentID string)
	seenParts := make(map[string]bool)
	checkParts = func(parentID string) {
		for _, p := range parts[parentID] {
			if seenParts[p.GetId()] {
				continue
			}
			seenParts[p.GetId()] = true
			if !IsAzima(p) {
				for _, depID := range p.GetDependsOn() {
					if da := findAzima(depID); da != nil && da.GetId() != azima.GetId() {
						depAzimaMap[da.GetId()] = da
					}
				}
				checkParts(p.GetId())
			}
		}
	}
	checkParts(azima.GetId())

	var res []*planv1.Task
	for _, da := range depAzimaMap {
		res = append(res, da)
	}
	slices.SortStableFunc(res, func(a, b *planv1.Task) int {
		return strings.Compare(a.GetCode(), b.GetCode())
	})
	return res
}

// AzimaDependency is an azima that waits for others, itself or through the work part of it (DependencyAzimas). Under
// the per-azima push strategy Djinn refuses it: each azima's branch is cut from main and lands there on its own, so
// none holds another's work.
type AzimaDependency struct {
	Azima *planv1.Task
	After []*planv1.Task
}

// AzimaDependencies lists the azimas among tasks, every task of a wish, that wait for others, in the order of their
// codes.
func AzimaDependencies(tasks []*planv1.Task) []AzimaDependency {
	var out []AzimaDependency
	for _, t := range tasks {
		if !IsAzima(t) {
			continue
		}
		if after := DependencyAzimas(t, tasks); len(after) > 0 {
			out = append(out, AzimaDependency{Azima: t, After: after})
		}
	}
	slices.SortFunc(out, func(a, b AzimaDependency) int { return CompareCodes(a.Azima.GetCode(), b.Azima.GetCode()) })
	return out
}

// AzimaDependenciesText says deps in a line: "T2 after T1; T3 after T1, T2".
func AzimaDependenciesText(deps []AzimaDependency) string {
	parts := make([]string, 0, len(deps))
	for _, d := range deps {
		codes := make([]string, 0, len(d.After))
		for _, a := range d.After {
			codes = append(codes, a.GetCode())
		}
		slices.SortFunc(codes, CompareCodes)
		parts = append(parts, d.Azima.GetCode()+" after "+strings.Join(codes, ", "))
	}
	return strings.Join(parts, "; ")
}

// RefuseAzimaDependencies refuses graph, the tasks of a wish that pushes per azima as a change would leave them, when
// it makes an azima wait for another that it did not wait for among tasks, the wish's tasks as they are: an azima
// spawned or synced from its plan file with one after it, a dependency set, a task grouped into another azima. What a
// wish waited for before it was switched stays.
func RefuseAzimaDependencies(tasks, graph []*planv1.Task) error {
	had := map[[2]string]bool{}
	for _, d := range AzimaDependencies(tasks) {
		for _, a := range d.After {
			had[[2]string{d.Azima.GetId(), a.GetId()}] = true
		}
	}
	var added []AzimaDependency
	for _, d := range AzimaDependencies(graph) {
		d.After = slices.DeleteFunc(slices.Clone(d.After), func(a *planv1.Task) bool {
			return had[[2]string{d.Azima.GetId(), a.GetId()}]
		})
		if len(d.After) > 0 {
			added = append(added, d)
		}
	}
	if len(added) == 0 {
		return nil
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"the wish pushes per azima: no azima waits for another, each one's branch cut from main (%s)",
		AzimaDependenciesText(added)))
}

// RefuseSwitchToAzima refuses switching a wish to the per-azima push strategy while its azimas, among tasks, wait for
// each other, naming them in a line.
func RefuseSwitchToAzima(tasks []*planv1.Task) error {
	deps := AzimaDependencies(tasks)
	if len(deps) == 0 {
		return nil
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"per azima refused: azimas depend on each other (%s)", AzimaDependenciesText(deps)))
}

// AzimaOf is the azima t is part of among byID, the tasks of its wish by id, walking up part_of: t itself when it is
// an azima; nil when none.
func AzimaOf(t *planv1.Task, byID map[string]*planv1.Task) *planv1.Task {
	seen := map[string]bool{}
	for t != nil && !seen[t.GetId()] {
		if IsAzima(t) {
			return t
		}
		seen[t.GetId()] = true
		t = byID[t.GetPartOf()]
	}
	return nil
}

// RefuseStranding refuses switching wish to the push strategy to when work of it, among tasks, merged into a branch,
// would be left there, its further work going to another: per azima, the work of an azima merged into the wish's
// integration branch while the azima's own branch is another; per wish, work merged into an azima's branch other than
// the wish's integration branch. The work of an azima stays where it is once the azima adopts the branch it lives on
// (djinn task update <azima> --branch <branch>), which the refusal says. Work of no azima stays in the wish's branch.
func RefuseStranding(wish *planv1.Wish, tasks []*planv1.Task, to planv1.PushStrategy) error {
	byID := map[string]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	type strand struct {
		azima       *planv1.Task
		codes       []string
		lives, goes string
	}
	var strands []*strand
	for _, t := range tasks {
		in := t.GetIntegration()
		if in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || IsAzima(t) {
			continue
		}
		wishBranch := IntegrationBranchOf(wish, t.GetProjectId())
		lives := cmp.Or(in.GetBranch(), wishBranch)
		azima := AzimaOf(t, byID)
		goes := wishBranch
		if to == planv1.PushStrategy_PUSH_STRATEGY_AZIMA && azima != nil {
			goes = AzimaBranchName(azima)
		}
		if lives == "" || goes == "" || strings.EqualFold(lives, goes) {
			continue
		}
		i := slices.IndexFunc(strands, func(s *strand) bool { return s.azima == azima && s.lives == lives })
		if i < 0 {
			strands = append(strands, &strand{azima: azima, lives: lives, goes: goes})
			i = len(strands) - 1
		}
		strands[i].codes = append(strands[i].codes, t.GetCode())
	}
	if len(strands) == 0 {
		return nil
	}
	slices.SortFunc(strands, func(a, b *strand) int { return CompareCodes(a.azima.GetCode(), b.azima.GetCode()) })
	var said, adopt []string
	for _, s := range strands {
		slices.SortFunc(s.codes, CompareCodes)
		whose := strings.Join(s.codes, ", ")
		if s.azima != nil {
			whose += " (" + s.azima.GetCode() + ")"
		}
		next := "the wish's"
		if to == planv1.PushStrategy_PUSH_STRATEGY_AZIMA && s.azima != nil {
			next = "its azima's"
			adopt = append(adopt, fmt.Sprintf("djinn task update %s --branch %s", s.azima.GetCode(), s.lives))
		}
		said = append(said, fmt.Sprintf("the work of %s is merged into %s, %s goes to %s", whose, s.lives, next, s.goes))
	}
	text := fmt.Sprintf("cannot switch wish %q to per %s: it would strand merged work: %s", wish.GetTitle(),
		PushStrategyWord(to), strings.Join(said, "; "))
	if len(adopt) > 0 {
		text += "; have each azima adopt the branch its work lives on first (" + strings.Join(adopt, "; ") + "), then switch"
	}
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(text))
}

// HasUnfinishedParts tells whether an azima has parts still to finish: work not finished,
// or an azima part of it not done.
func HasUnfinishedParts(id string, tasks []*planv1.Task) bool {
	tasks = WithAzimas(tasks)
	for _, t := range tasks {
		if t.GetPartOf() != id {
			continue
		}
		if IsAzima(t) {
			if t.GetAzima().GetState() != planv1.AzimaState_AZIMA_STATE_DONE {
				return true
			}
		} else if !Finished(t) || taskMergingOrFailed(t) {
			return true
		}
	}
	return false
}

// DoneWithProblems tells a task whose worker ended done while its permissions refused some of its calls, with nobody
// to approve them (Task.refused): it may not have done its job. Nobody looked at it yet: a run again sets Task.refused
// anew, and marking it done (djinn task done, the developer or the lead) closes it as seen. Djinn proposes no grant
// while one is left: the lead heard of its refused calls and decides (Q273); the window shows a plain done.
func DoneWithProblems(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && t.GetRefused().GetCount() > 0 && t.GetClosed() == nil
}

// Finished tells a task of work finished: done, stopped on request, or cut short for good. Djinn resumes by itself
// every task it can (RESUMING), so one left interrupted (resumed as a fork, imported, its worktree gone) is history.
func Finished(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return true
	}
	return false
}

// WithAzimas is tasks with Task.azima set on their azimas, as clones: the store's own messages stay as they are.
func WithAzimas(tasks []*planv1.Task) []*planv1.Task {
	if !slices.ContainsFunc(tasks, IsAzima) {
		return tasks
	}
	out := make([]*planv1.Task, len(tasks))
	for i, t := range tasks {
		out[i] = proto.CloneOf(t)
	}
	FillAzimas(out)
	return out
}

// working tells whether a worker is on the task now: running, paused, or about to resume.
func working(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING:
		return true
	}
	return false
}

// taskMerging tells whether a done task's work is currently merging.
func taskMerging(t *planv1.Task) bool {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		return false
	}
	s := t.GetIntegration().GetState()
	return s == planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		s == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING
}

// taskMergingOrFailed tells whether a done task's work is merging or failed to merge.
func taskMergingOrFailed(t *planv1.Task) bool {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		return false
	}
	s := t.GetIntegration().GetState()
	return s == planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		s == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING ||
		s == planv1.IntegrationState_INTEGRATION_STATE_CONFLICT ||
		s == planv1.IntegrationState_INTEGRATION_STATE_RED
}

// CompareCodes orders task codes as a person reads them: by letter, then by number, T2 before T10.
func CompareCodes(a, b string) int {
	split := func(s string) (string, int) {
		i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
		if i < 0 {
			return strings.ToUpper(s), -1
		}
		n, err := strconv.Atoi(s[i:])
		if err != nil {
			return strings.ToUpper(s), -1
		}
		return strings.ToUpper(s[:i]), n
	}
	pa, na := split(a)
	pb, nb := split(b)
	return cmp.Or(strings.Compare(pa, pb), cmp.Compare(na, nb), strings.Compare(a, b))
}

// PlanDir is the folder of a project that holds its plan files.
const PlanDir = "plan"

// AzimaFile is what a plan file says of its azima: its front matter, between two lines "---" at the top, and its title,
// the first heading after it ("# T07 · The orchestrator", the code left out).
type AzimaFile struct {
	// Path is the file, as its project names it: plan/8e8d3d76-orchestrator.md.
	Path   string
	ID     string
	Code   string
	Wish   string
	Phase  string
	Status string
	Title  string
	// Description is what the file holds after its title heading: its goal and body.
	Description string
	// After is what the file says the azima depends on, by code.
	After []string
	// DoneWhen is what its Done-when sections say: their boxes, and what the unchecked ones need.
	DoneWhen DoneWhen
}

// Done tells whether the file says its azima is done.
func (f AzimaFile) Done() bool { return strings.EqualFold(f.Status, "done") }

// Draft tells whether the file says its azima is a draft.
func (f AzimaFile) Draft() bool { return strings.EqualFold(f.Status, "draft") }

// Closes tells whether the file closes its azima: its status says done, or every box of its Done-when section is
// checked. A draft azima never closes.
func (f AzimaFile) Closes() bool { return !f.Draft() && (f.Done() || f.DoneWhen.AllChecked()) }

// ReadAzimaFiles reads the plan files of a project's folder: plan/*.md (and plan/*/*.md) with a front matter that gives a code, and their
// Done-when section. A file without one (the README) is not an azima's.
func ReadAzimaFiles(project string) ([]AzimaFile, error) {
	names, err := filepath.Glob(filepath.Join(project, PlanDir, "*.md"))
	if err != nil {
		return nil, err
	}
	subNames, err := filepath.Glob(filepath.Join(project, PlanDir, "*", "*.md"))
	if err == nil {
		names = append(names, subNames...)
	}
	slices.Sort(names)
	var out []AzimaFile
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		f, ok := parseAzimaFile(data)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(project, name)
		if err != nil {
			f.Path = PlanDir + "/" + filepath.Base(name)
		} else {
			f.Path = filepath.ToSlash(rel)
		}
		// f.Path has slashes: path, not filepath, splits it on Windows too.
		dir := path.Dir(f.Path)
		if f.Wish == "" && dir != PlanDir && strings.HasPrefix(dir, PlanDir+"/") {
			f.Wish = path.Base(dir)
		}
		out = append(out, f)
	}
	return out, nil
}

// parseAzimaFile reads a plan file's front matter, title and description; false when it has no front matter with a code.
func parseAzimaFile(data []byte) (AzimaFile, bool) {
	var f AzimaFile
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return f, false
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "id":
			f.ID = value
		case "code":
			f.Code = value
		case "wish":
			f.Wish = strings.Trim(value, `"'`)
		case "phase":
			f.Phase = value
		case "status":
			f.Status = value
		case "after":
			f.After = strings.Fields(strings.ReplaceAll(value, ",", " "))
		}
	}
	if f.Code == "" {
		return f, false
	}
	f.DoneWhen = ReadDoneWhen(body)
	sc := bufio.NewScanner(bytes.NewReader(body))
	var afterTitle []string
	foundTitle := false
	for sc.Scan() {
		line := sc.Text()
		if !foundTitle {
			if title, ok := strings.CutPrefix(line, "# "); ok {
				f.Title = azimaTitle(strings.TrimSpace(title), f.Code)
				foundTitle = true
			}
			continue
		}
		afterTitle = append(afterTitle, line)
	}
	if foundTitle {
		f.Description = strings.TrimSpace(strings.Join(afterTitle, "\n"))
	} else {
		f.Description = strings.TrimSpace(string(body))
	}
	return f, true
}

// azimaTitle is a plan file's heading without its code: "T07 · The orchestrator" is "The orchestrator".
func azimaTitle(heading, code string) string {
	rest, ok := strings.CutPrefix(heading, code)
	if !ok || rest != "" && !strings.ContainsAny(rest[:1], " ·:-—") {
		return heading
	}
	if rest = strings.TrimLeft(rest, " ·:-—"); rest == "" {
		return heading
	}
	return rest
}

// splitFrontMatter splits a file into the lines of its front matter and what follows it; false without a front matter.
func splitFrontMatter(data []byte) (lines []string, body []byte, ok bool) {
	rest, found := bytes.CutPrefix(data, []byte("---\n"))
	if !found {
		if rest, found = bytes.CutPrefix(data, []byte("---\r\n")); !found {
			return nil, nil, false
		}
	}
	for len(rest) > 0 {
		line, next, _ := bytes.Cut(rest, []byte("\n"))
		text := strings.TrimRight(string(line), "\r")
		if text == "---" {
			return lines, next, true
		}
		lines = append(lines, text)
		rest = next
	}
	return nil, nil, false
}

// WriteAfter writes what an azima depends on, by code, into its plan file's front matter, as "after: T02 T05" after
// its status (or last); none removes the line. It tells whether the file changed: a file that says it already is left
// as it is.
func WriteAfter(file string, codes []string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	var out []string
	at := -1
	for _, line := range lines {
		key, _, _ := strings.Cut(line, ":")
		switch strings.TrimSpace(key) {
		case "after":
			continue
		case "status":
			at = len(out) + 1
		}
		out = append(out, line)
	}
	if at < 0 {
		at = len(out)
	}
	if len(codes) > 0 {
		out = slices.Insert(out, at, "after: "+strings.Join(codes, " "))
	}
	if slices.Equal(out, lines) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(body)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteStatus writes an azima's status ("open", "draft", etc.) into its plan file's front matter.
// It tells whether the file changed.
func WriteStatus(file string, status string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	var out []string
	found := false
	for _, line := range lines {
		key, _, hasKey := strings.Cut(line, ":")
		if hasKey && strings.TrimSpace(key) == "status" {
			out = append(out, "status: "+status)
			found = true
		} else {
			out = append(out, line)
		}
	}
	if !found {
		out = append(out, "status: "+status)
	}
	if slices.Equal(out, lines) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(body)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteDescription writes the description of an azima into its plan file, after its title heading.
// It tells whether the file changed.
func WriteDescription(file string, text string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	var titleLine string
	var foundTitle bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# ") {
			titleLine = line
			foundTitle = true
			break
		}
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range lines {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.WriteString(nl)
	if foundTitle {
		b.WriteString(titleLine + nl + nl)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed != "" {
		b.WriteString(trimmed + nl)
	}
	newContent := b.Bytes()
	if bytes.Equal(newContent, data) {
		return false, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, newContent, info.Mode().Perm())
}

// WriteWish sets or removes the wish of an azima in its plan file's front matter.
// If wish is empty, it removes any wish: line.
// It tells whether the file changed.
func WriteWish(file string, wish string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	var out []string
	found := false
	trimmedWish := strings.TrimSpace(wish)
	wishLine := ""
	if trimmedWish != "" {
		if strings.ContainsAny(trimmedWish, ":#\"'\n\r") {
			wishLine = fmt.Sprintf("wish: %q", trimmedWish)
		} else {
			wishLine = "wish: " + trimmedWish
		}
	}
	for _, line := range lines {
		key, _, hasKey := strings.Cut(line, ":")
		if hasKey && strings.TrimSpace(key) == "wish" {
			if wishLine != "" {
				out = append(out, wishLine)
			}
			found = true
		} else {
			out = append(out, line)
		}
	}
	if !found && wishLine != "" {
		insertAt := -1
		for i, line := range out {
			key, _, hasKey := strings.Cut(line, ":")
			if hasKey {
				k := strings.TrimSpace(key)
				if k == "code" || k == "id" {
					insertAt = i + 1
				}
			}
		}
		if insertAt >= 0 && insertAt <= len(out) {
			out = slices.Insert(out, insertAt, wishLine)
		} else {
			out = append(out, wishLine)
		}
	}
	if slices.Equal(out, lines) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(body)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteCode updates an azima's code in its plan file's front matter and title heading.
// It tells whether the file changed.
func WriteCode(file string, code string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	trimmedCode := strings.TrimSpace(code)
	var oldCode string
	var out []string
	found := false
	for _, line := range lines {
		key, val, hasKey := strings.Cut(line, ":")
		if hasKey && strings.TrimSpace(key) == "code" {
			oldCode = strings.TrimSpace(val)
			out = append(out, "code: "+trimmedCode)
			found = true
		} else {
			out = append(out, line)
		}
	}
	if !found {
		out = append([]string{"code: " + trimmedCode}, out...)
	}

	newBody := body
	if oldCode != "" && oldCode != trimmedCode {
		sc := bufio.NewScanner(bytes.NewReader(body))
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "# ") {
				heading := strings.TrimPrefix(line, "# ")
				if strings.HasPrefix(heading, oldCode) {
					rest := strings.TrimPrefix(heading, oldCode)
					if rest == "" || strings.ContainsAny(rest[:1], " ·:-—") {
						newLine := "# " + trimmedCode + rest
						newBody = bytes.Replace(body, []byte(line), []byte(newLine), 1)
					}
				}
				break
			}
		}
	}

	if slices.Equal(out, lines) && bytes.Equal(newBody, body) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(newBody)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteTitle writes an azima's title into its plan file's heading, the first "# " line after its front matter: its
// code and the separator after it stay ("# T07 · The orchestrator"); a file without a heading gets one, code first.
// It tells whether the file changed.
func WriteTitle(file string, code string, title string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	_, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	head := data[:len(data)-len(body)]
	title = strings.TrimSpace(title)
	var newBody []byte
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		heading, ok := strings.CutPrefix(line, "# ")
		if !ok {
			continue
		}
		newHeading := title
		if rest, ok := strings.CutPrefix(heading, code); ok && code != "" && (rest == "" || strings.ContainsAny(rest[:1], " ·:-—")) {
			sep := rest[:len(rest)-len(strings.TrimLeft(rest, " ·:-—"))]
			if strings.TrimLeft(rest, " ·:-—") == "" {
				sep = " · "
			}
			newHeading = code + sep + title
		}
		newBody = bytes.Replace(body, []byte(line), []byte("# "+newHeading), 1)
		break
	}
	if newBody == nil {
		heading := "# " + title
		if code != "" {
			heading = "# " + code + " · " + title
		}
		newBody = append([]byte(nl+heading+nl), body...)
	}
	if bytes.Equal(newBody, body) {
		return false, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, append(slices.Clip(head), newBody...), info.Mode().Perm())
}
