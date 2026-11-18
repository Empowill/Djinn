package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Djinn frees by itself the worktrees and scratch of finished tasks, by the rules of Q75 (Task.freed):
//   - a task done 30 days ago whose work is integrated (Q81): its worktree and scratch go, its branch stays; a task
//     cancelled, done with its work dropped, failed or stopped is not freed by its age, only by its azima or wish;
//   - a task of an azima validated, its request merged (Azima.merged, its pull request merged) or closed as done by the
//     developer or the lead: at once, its local branch too;
//   - a task of a wish granted, which validates all its azimas: the same, at once, in cascade.
// Never lose work: a worktree holding changes not committed, or commits found neither in the integration branch nor on
// the remote, stays, and so do its scratch and its branch; a local branch with such commits stays. Each is logged with
// why, on the task and on its azima. A remote branch is never touched. A task freed resumes by a fork (forkInstead).

// freeAfter is how long after its end a task done and integrated keeps its worktree and scratch (Q75, Q81).
const freeAfter = 30 * 24 * time.Hour

// freeEvery is how often the freeing pass runs without being kicked: the age of a task passes without telling anyone.
const freeEvery = 10 * time.Minute

// freeKeptAgain is how long a task the pass kept something of waits before the pass looks at it again: its changes are
// not committed by themselves, and its log line is not written every pass.
const freeKeptAgain = 6 * time.Hour

// freeWalk is how long the pass measures a folder before removing it: past it, the bytes freed are a floor.
const freeWalk = 30 * time.Second

// freeing is the pass that frees the folders of finished tasks: its loop, and how far its last pass went.
type freeing struct {
	every time.Duration // 0: no loop (WithFreeEvery)
	kick  chan struct{} // asks for a pass at once: an azima validated, a wish granted
	done  chan struct{} // closed when the loop has stopped; nil until Schedule starts it

	mu     sync.Mutex
	last   *machinev1.DiskFreeing // the last pass that found something to do; guarded by mu
	looked map[string]freeLook    // by task identifier: what the pass did of it last; guarded by mu
}

// freeLook is what a pass did of a task: by which rule, when, and whether it kept something of it.
type freeLook struct {
	rule planv1.FreeRule
	at   time.Time
	kept bool
}

// WithFreeEvery sets how often the freeing pass runs without being kicked; 0 runs none, kicked or not.
func WithFreeEvery(d time.Duration) Option { return func(h *Harness) { h.free.every = d } }

// Free asks for a freeing pass at once: an azima was validated, a wish granted.
func (h *Harness) Free() {
	select {
	case h.free.kick <- struct{}{}:
	default:
	}
}

// startFree starts the freeing loop, from Schedule.
func (h *Harness) startFree() {
	if h.free.every <= 0 {
		return
	}
	h.free.done = make(chan struct{})
	go h.freeLoop()
}

func (h *Harness) freeLoop() {
	defer logx.Crash("free")
	defer close(h.free.done)
	tick := time.NewTicker(h.free.every)
	defer tick.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.free.kick:
		case <-tick.C:
		}
		h.freePass(h.ctx)
	}
}

// Freeing is how far the last freeing pass that found something to do is, nil before the first: a window bar follows it.
func (h *Harness) Freeing() *machinev1.DiskFreeing {
	h.free.mu.Lock()
	defer h.free.mu.Unlock()
	if h.free.last == nil {
		return nil
	}
	return proto.CloneOf(h.free.last)
}

// freeJob is a task the pass frees, by rule, with its project (nil outside any) and its nearest azima (nil for none).
type freeJob struct {
	task    *planv1.Task
	rule    planv1.FreeRule
	project *planv1.Project
	azima   *planv1.Task
}

// freed is what the pass did of one task: the bytes it freed, what it removed, and why it kept the rest.
type freed struct {
	bytes                        uint64
	worktree, scratch, branchCut bool
	kept                         []string
}

// freePass frees what the rules of Q75 free now, the azimas validated and wishes granted first, and records how far it
// is in h.free.last as it goes.
func (h *Harness) freePass(ctx context.Context) {
	if h.home == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	jobs, err := h.freeJobs(ctx)
	if err != nil {
		logx.L().Error("read what to free", "loop", "free", "err", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	start := h.now()
	progress := &machinev1.DiskFreeing{Total: int32(len(jobs)), StartTime: timestamppb.New(start)}
	byAzima := map[string]*machinev1.DiskFreeingAzima{}
	for _, j := range jobs {
		if j.rule == planv1.FreeRule_FREE_RULE_GRANT && !slices.Contains(progress.WishIds, j.task.GetWishId()) {
			progress.WishIds = append(progress.WishIds, j.task.GetWishId())
		}
		if j.azima == nil || j.rule == planv1.FreeRule_FREE_RULE_AGE {
			continue
		}
		a := byAzima[j.azima.GetId()]
		if a == nil {
			a = &machinev1.DiskFreeingAzima{AzimaId: j.azima.GetId(), WishId: j.azima.GetWishId()}
			byAzima[j.azima.GetId()] = a
			progress.Azimas = append(progress.Azimas, a)
		}
		a.Total++
	}
	h.setFreeing(progress)
	for _, j := range jobs {
		if h.ctx.Err() != nil {
			break
		}
		got := h.freeTask(ctx, j)
		h.free.mu.Lock()
		progress.Done++
		progress.FreedBytes += got.bytes
		a := byAzima[j.azima.GetId()]
		if j.rule == planv1.FreeRule_FREE_RULE_AGE {
			a = nil
		}
		if a != nil {
			a.Done++
			a.FreedBytes += got.bytes
		}
		if len(got.kept) > 0 {
			progress.Kept++
			if a != nil {
				a.Kept++
			}
		}
		h.free.looked[j.task.GetId()] = freeLook{rule: j.rule, at: h.now(), kept: len(got.kept) > 0}
		var line string
		if a != nil {
			line = azimaFreeLine(a, j.task, got)
		}
		h.free.mu.Unlock()
		h.recordFreed(ctx, j, got, line)
	}
	h.free.mu.Lock()
	progress.EndTime = timestamppb.New(h.now())
	h.free.mu.Unlock()
	logx.L().Info("free the folders of finished tasks", "loop", "free", "tasks", progress.GetDone(),
		"kept", progress.GetKept(), "freed", gbText(progress.GetFreedBytes()), "took", h.now().Sub(start).Round(time.Millisecond))
	h.notify()
}

// setFreeing makes p the pass the window follows.
func (h *Harness) setFreeing(p *machinev1.DiskFreeing) {
	h.free.mu.Lock()
	h.free.last = p
	h.free.mu.Unlock()
}

// freeJobs are the tasks the rules of Q75 free now: those of the wishes granted and the azimas validated first, then
// those done and integrated 30 days ago; each with something on disk, or a local branch to look at, and not looked at
// lately by the same rule.
func (h *Harness) freeJobs(ctx context.Context) ([]freeJob, error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return nil, err
	}
	granted := map[string]bool{}
	for _, w := range wishes {
		granted[w.GetId()] = w.GetState() == planv1.WishState_WISH_STATE_GRANTED
	}
	byProject := map[string]*planv1.Project{}
	for _, p := range projects {
		byProject[p.GetId()] = p
	}
	byID := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	now := h.now()
	inUse := map[string]bool{} // The worktrees workers run in: a review worker's is the task's it reviews.
	for _, t := range tasks {
		if t.GetWorktree() != "" && h.running(t.GetId()) {
			inUse[filepath.Clean(t.GetWorktree())] = true
		}
	}
	var jobs []freeJob
	for _, t := range tasks {
		if plan.IsAzima(t) || h.running(t.GetId()) ||
			slices.ContainsFunc(h.freeWorktrees(t), func(p string) bool { return inUse[p] }) {
			continue
		}
		var azima *planv1.Task
		if a := byID[t.GetPartOf()]; a != nil && plan.IsAzima(a) {
			azima = a
		}
		rule := freeRuleOf(t, granted[t.GetWishId()], byID, now)
		if rule == planv1.FreeRule_FREE_RULE_UNSPECIFIED {
			continue
		}
		h.free.mu.Lock()
		if h.free.looked == nil {
			h.free.looked = map[string]freeLook{}
		}
		look, seen := h.free.looked[t.GetId()]
		h.free.mu.Unlock()
		if seen && look.rule >= rule && (!look.kept || now.Sub(look.at) < freeKeptAgain) {
			continue
		}
		project := byProject[t.GetProjectId()]
		if len(h.freeWorktrees(t)) == 0 && len(h.freeScratch(t)) == 0 && !hasBranch(ctx, rule, t, project) {
			h.free.mu.Lock()
			h.free.looked[t.GetId()] = freeLook{rule: rule, at: now} // Nothing left of it: not looked at again.
			h.free.mu.Unlock()
			continue
		}
		jobs = append(jobs, freeJob{task: t, rule: rule, project: project, azima: azima})
	}
	// The cascades of a grant and of an azima validated go first, by azima, then the old tasks; each by code.
	slices.SortStableFunc(jobs, func(a, b freeJob) int {
		return cmp.Or(
			cmp.Compare(freeOrder(a.rule), freeOrder(b.rule)),
			strings.Compare(a.task.GetWishId(), b.task.GetWishId()),
			plan.CompareCodes(a.azima.GetCode(), b.azima.GetCode()),
			plan.CompareCodes(a.task.GetCode(), b.task.GetCode()))
	})
	return jobs, nil
}

// freeOrder puts the cascades, which the developer waits for, before the old tasks.
func freeOrder(r planv1.FreeRule) int {
	if r == planv1.FreeRule_FREE_RULE_AGE {
		return 1
	}
	return 0
}

// freeRuleOf is the rule of Q75 that frees t now, unspecified for none: its wish granted, an azima it is part of
// validated, or done 30 days ago with its work integrated (Q81: one status, done and integrated, frees by age). A
// cascade frees only a task finished whose work is not merging.
func freeRuleOf(t *planv1.Task, granted bool, byID map[string]*planv1.Task, now time.Time) planv1.FreeRule {
	finished := freeFinished(t)
	if finished && granted {
		return planv1.FreeRule_FREE_RULE_GRANT
	}
	if finished && validatedAzima(t, byID) != nil {
		return planv1.FreeRule_FREE_RULE_AZIMA
	}
	if freeIntegrated(t) {
		if end := endedAt(t); !end.IsZero() && !now.Before(end.Add(freeAfter)) {
			return planv1.FreeRule_FREE_RULE_AGE
		}
	}
	return planv1.FreeRule_FREE_RULE_UNSPECIFIED
}

// freeIntegrated tells a task done whose work is in its integration branch: merged, or committed into it. A task done
// with its work dropped (done --drop) has no integration left, and one done with nothing to merge none either.
func freeIntegrated(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE &&
		t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED
}

// freeFinished tells a task no worker runs nor will run without a person, whose work is not merging: finished, or
// failed.
func freeFinished(t *planv1.Task) bool {
	switch t.GetIntegration().GetState() {
	case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
		return false
	}
	return plan.Finished(t) || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED
}

// validatedAzima is the azima t is part of, or one that azima is part of, that is validated; nil for none.
func validatedAzima(t *planv1.Task, byID map[string]*planv1.Task) *planv1.Task {
	seen := map[string]bool{}
	for a := byID[t.GetPartOf()]; a != nil && plan.IsAzima(a) && !seen[a.GetId()]; a = byID[a.GetPartOf()] {
		seen[a.GetId()] = true
		if azimaValidated(a) {
			return a
		}
	}
	return nil
}

// azimaValidated tells an azima whose work is validated (Q75): all in main (Azima.merged), its pull request merged, or
// marked done by the developer or the lead.
func azimaValidated(a *planv1.Task) bool {
	if a.GetDraft() {
		return false
	}
	if a.GetAzima().GetMerged() != nil || a.GetAzima().GetPr().GetState() == planv1.AzimaPrState_AZIMA_PR_STATE_MERGED {
		return true
	}
	if a.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		return false
	}
	switch a.GetClosed().GetActor() {
	case planv1.Closer_CLOSER_DEVELOPER, planv1.Closer_CLOSER_LEAD:
		return true
	}
	return false
}

// endedAt is when t ended: the latest of its worker's end, its cancellation and its closing.
func endedAt(t *planv1.Task) time.Time {
	var end time.Time
	for _, at := range []*timestamppb.Timestamp{t.GetEndTime(), t.GetCancelled().GetCreateTime(), t.GetClosed().GetCreateTime()} {
		if at != nil && at.AsTime().After(end) {
			end = at.AsTime()
		}
	}
	return end
}

// freeBranchRule tells whether rule frees t's local branch too, and whether there may be one to look at: a branch named
// in a Git project, not deleted by an earlier pass.
func freeBranchRule(rule planv1.FreeRule, t *planv1.Task, project *planv1.Project) bool {
	return rule != planv1.FreeRule_FREE_RULE_AGE && t.GetBranch() != "" && project.GetGit() &&
		!t.GetFreed().GetBranchDeleted() && t.GetReview() == nil
}

// hasBranch tells whether rule frees t's local branch and it is there, in its project's repository.
func hasBranch(ctx context.Context, rule planv1.FreeRule, t *planv1.Task, project *planv1.Project) bool {
	if !freeBranchRule(rule, t, project) || !isDir(project.GetDirectory()) {
		return false
	}
	sha, err := git(ctx, project.GetDirectory(), "rev-parse", "--verify", "--quiet", "refs/heads/"+t.GetBranch()+"^{commit}")
	return err == nil && sha != ""
}

// freeWorktrees is t's own worktree when it is on disk: the folder named after it in the data folder, or the one it
// records. A review worker works in the worktree of the task it reviews, which is that task's: never its own.
func (h *Harness) freeWorktrees(t *planv1.Task) []string {
	if t.GetProjectId() == "" {
		return nil
	}
	return onDiskPaths(t.GetId(), worktreeDir(h.home, t.GetProjectId(), t.GetId()), t.GetWorktree())
}

// freeScratch is t's scratch folders on disk, in Djinn's data folder only.
func (h *Harness) freeScratch(t *planv1.Task) []string {
	var paths []string
	if t.GetProjectId() != "" {
		paths = append(paths, workScratchDir(h.home, t.GetProjectId(), t.GetId()))
	}
	if s := t.GetScratch(); s != "" && isSubfolder(filepath.Clean(s), filepath.Clean(h.home)) {
		paths = append(paths, s)
	}
	return onDiskPaths(t.GetId(), paths...)
}

// onDiskPaths is paths, cleaned, without the empty, the doubles, those not on disk, and those not named after the task
// id.
func onDiskPaths(id string, paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if filepath.Base(p) != id || slices.Contains(out, p) {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// freeTask frees what the rule of j frees of its task, under the scheduler's lock so that no continue starts it
// meanwhile, and says what it freed and why it kept the rest.
func (h *Harness) freeTask(ctx context.Context, j freeJob) freed {
	var got freed
	trees, scratch := h.freeWorktrees(j.task), h.freeScratch(j.task)
	sizes := map[string]uint64{}
	for _, p := range slices.Concat(trees, scratch) {
		sizes[p] = walk(ctx, p, freeWalk, nil, false).bytes
	}
	logx.Lock(&h.sched, "scheduler")
	defer h.sched.Unlock()
	t, err := store.Get[*planv1.Task](ctx, h.store, j.task.GetId())
	if err != nil {
		got.kept = append(got.kept, "its task could not be read: "+err.Error())
		return got
	}
	if h.running(t.GetId()) || !freeFinished(t) {
		got.kept = append(got.kept, "it runs again")
		return got
	}
	dir := j.project.GetDirectory()
	branchCut := freeBranchRule(j.rule, t, j.project)
	if len(trees) > 0 && (dir == "" || !isDir(dir)) {
		got.kept = append(got.kept, fmt.Sprintf("worktree kept: the project's folder %q is not on this machine", dir))
	}
	safe := func() []string { return h.safeTips(ctx, dir, t, !branchCut) }
	treeKept := len(got.kept) > 0
	for _, wt := range trees {
		if treeKept {
			break
		}
		if why := holdsWork(ctx, dir, wt, safe()); why != "" {
			got.kept, treeKept = append(got.kept, "worktree kept: "+why), true
			break
		}
		if err := removeWorktree(ctx, dir, wt, false); err != nil {
			got.kept, treeKept = append(got.kept, "worktree kept: "+err.Error()), true
			break
		}
		got.worktree, got.bytes = true, got.bytes+sizes[wt]
	}
	if treeKept {
		return got // Its scratch and its branch stay with it.
	}
	for _, s := range scratch {
		if err := os.RemoveAll(s); err != nil {
			got.kept = append(got.kept, "scratch kept: "+err.Error())
			continue
		}
		got.scratch, got.bytes = true, got.bytes+sizes[s]
	}
	if !branchCut {
		return got
	}
	ref := "refs/heads/" + t.GetBranch()
	tip, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil || tip == "" {
		return got // Gone already.
	}
	if why := unsaved(ctx, dir, tip, safe()); why != "" {
		got.kept = append(got.kept, "branch "+t.GetBranch()+" kept: "+why)
		return got
	}
	if _, err := git(ctx, dir, "branch", "-D", t.GetBranch()); err != nil {
		got.kept = append(got.kept, "branch "+t.GetBranch()+" kept: "+err.Error())
		return got
	}
	got.branchCut = true
	return got
}

// safeTips are the commits whose history holds work safely, in the repository of dir: the integration branch t merged
// into, the branches of its azimas, the project's current commit, and t's own branch when it stays. Each is named for a
// log line as name=sha.
func (h *Harness) safeTips(ctx context.Context, dir string, t *planv1.Task, keepBranch bool) []string {
	names := []string{t.GetIntegration().GetBranch()}
	if keepBranch {
		names = append(names, t.GetBranch())
	}
	seen := map[string]bool{}
	for id := t.GetPartOf(); id != "" && !seen[id]; {
		seen[id] = true
		a, err := store.Get[*planv1.Task](ctx, h.store, id)
		if err != nil || !plan.IsAzima(a) {
			break
		}
		names = append(names, a.GetAzima().GetBranch())
		id = a.GetPartOf()
	}
	var tips []string
	if head, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil && head != "" {
		tips = append(tips, "HEAD="+head)
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		if sha, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+n+"^{commit}"); err == nil && sha != "" {
			tips = append(tips, n+"="+sha)
		}
	}
	return tips
}

// holdsWork says why the worktree wt holds work, "" when it holds none: changes not committed, or a HEAD with commits
// found neither in the safe tips (safeTips) nor on the remote.
func holdsWork(ctx context.Context, dir, wt string, safe []string) string {
	status, err := gitRaw(ctx, wt, "status", "--porcelain")
	if err != nil {
		return "its status could not be read: " + err.Error()
	}
	if strings.TrimSpace(status) != "" {
		return "it holds changes not committed, " + wt
	}
	head, err := git(ctx, wt, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || head == "" {
		return "" // A worktree without a commit holds none.
	}
	return unsaved(ctx, dir, head, safe)
}

// unsaved says how many commits of tip are found neither in the safe tips nor on any remote of the repository of dir,
// "" for none.
func unsaved(ctx context.Context, dir, tip string, safe []string) string {
	args := []string{"rev-list", "--count", tip, "--not", "--remotes"}
	var names []string
	for _, s := range safe {
		name, sha, _ := strings.Cut(s, "=")
		args = append(args, sha)
		if name != "HEAD" {
			names = append(names, name)
		}
	}
	out, err := git(ctx, dir, args...)
	if err != nil {
		return "its commits could not be counted: " + err.Error()
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return fmt.Sprintf("git rev-list: %q is not a count", out)
	}
	if n == 0 {
		return ""
	}
	where := "the integration branch"
	if len(names) > 0 {
		where = strings.Join(names, ", ")
	}
	return fmt.Sprintf("%d commits found neither in %s nor on the remote", n, where)
}

// freeWhy says the rule that freed a task, for its event.
func freeWhy(j freeJob) string {
	switch j.rule {
	case planv1.FreeRule_FREE_RULE_GRANT:
		return "its wish was granted"
	case planv1.FreeRule_FREE_RULE_AZIMA:
		if j.azima != nil {
			return "its azima " + j.azima.GetCode() + " was validated"
		}
		return "its azima was validated"
	}
	return fmt.Sprintf("it ended %d days ago, its work integrated", int(freeAfter.Hours()/24))
}

// freeText is the event of the task j freed: what went and what stayed, and why.
func freeText(j freeJob, got freed) string {
	var parts []string
	switch {
	case got.worktree && got.scratch:
		parts = append(parts, "worktree and scratch freed")
	case got.worktree:
		parts = append(parts, "worktree freed")
	case got.scratch:
		parts = append(parts, "scratch freed")
	}
	if b := j.task.GetBranch(); b != "" && (got.worktree || got.scratch || got.branchCut) {
		switch {
		case got.branchCut:
			parts = append(parts, "local branch "+b+" deleted, the remote's untouched")
		case !freeBranchRule(j.rule, j.task, j.project):
			parts = append(parts, "branch "+b+" kept")
		}
	}
	parts = append(parts, got.kept...)
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, "; ") + " (" + freeWhy(j) + ")"
	if got.bytes > 0 {
		text += ": " + gbText(got.bytes) + " freed"
	}
	if got.worktree {
		text += ". It resumes by a fork: djinn task spawn --fork " + j.task.GetCode()
	}
	return text
}

// azimaFreeLine is the event of azima a for task t of its cascade: how far it is, and what went of t or stayed, and why.
func azimaFreeLine(a *machinev1.DiskFreeingAzima, t *planv1.Task, got freed) string {
	what := t.GetCode() + " freed"
	if len(got.kept) > 0 {
		what = t.GetCode() + " " + strings.Join(got.kept, "; ")
	}
	return fmt.Sprintf("freeing its tasks' folders: %d/%d, %s freed, %d kept; %s", a.GetDone(), a.GetTotal(),
		gbText(a.GetFreedBytes()), a.GetKept(), what)
}

// recordFreed records on j's task what went (Task.freed, its worktree and scratch cleared) and its event, and on its
// azima the cascade's line; a kept task is logged with why.
func (h *Harness) recordFreed(ctx context.Context, j freeJob, got freed, azimaLine string) {
	text := freeText(j, got)
	if text == "" && azimaLine == "" {
		return
	}
	if len(got.kept) > 0 {
		logx.L().Warn("kept a finished task's folders", "loop", "free", "task", j.task.GetCode(), "why", strings.Join(got.kept, "; "))
	} else {
		logx.L().Info("freed a finished task's folders", "loop", "free", "task", j.task.GetCode(), "rule", j.rule.String(),
			"freed", gbText(got.bytes), "branch_deleted", got.branchCut)
	}
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, j.task.GetId())
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		changed := false
		if got.worktree && t.GetWorktree() != "" && len(h.freeWorktrees(t)) == 0 {
			t.Worktree, changed = "", true
		}
		if got.scratch && t.GetScratch() != "" && len(h.freeScratch(t)) == 0 {
			t.Scratch, changed = "", true
		}
		if got.worktree || got.scratch || got.branchCut {
			f := proto.CloneOf(t.GetFreed())
			if f == nil || got.worktree || got.scratch {
				f = &planv1.Freed{Time: timestamppb.New(h.now()), BranchDeleted: f.GetBranchDeleted()}
			}
			f.Rule = max(f.GetRule(), j.rule)
			f.BranchDeleted = f.GetBranchDeleted() || got.branchCut
			if !proto.Equal(f, t.GetFreed()) {
				t.Freed, changed = f, true
			}
		}
		if err := tx.Journal(actorHarness, methodFree, t); err != nil {
			return err
		}
		if changed {
			if err := tx.Put(t); err != nil {
				return err
			}
		}
		if text != "" {
			if err := putEvents(ctx, tx, newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})); err != nil {
				return err
			}
		}
		if azimaLine != "" && j.azima != nil {
			return putEvents(ctx, tx, newEvent(j.azima.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: azimaLine}))
		}
		return nil
	})
	if err != nil {
		logx.L().Error("record a finished task's folders freed", "loop", "free", "task", j.task.GetCode(), "err", err)
	}
}

// methodFree journals what the freeing pass changes of a task.
const methodFree = "harness/free"

// isDir tells whether path is a folder on this machine.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// freedWhy says the rule that freed a task's worktree, for the refusal to continue it.
func freedWhy(r planv1.FreeRule) string {
	switch r {
	case planv1.FreeRule_FREE_RULE_GRANT:
		return "its wish being granted"
	case planv1.FreeRule_FREE_RULE_AZIMA:
		return "its azima being validated"
	}
	return fmt.Sprintf("%d days after it ended", int(freeAfter.Hours()/24))
}
