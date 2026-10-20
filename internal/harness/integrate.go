package harness

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Integration (T07): a worker's work counts once it is in its wish's integration branch, tested. When a worker ends
// done in a project that names a check, or into a branch of its wish's own in one that names none, its task's
// integration is pending; Djinn then commits the work at once, task by task, by itself, no model: a task whose
// worktree holds changes not committed goes to a review worker instead (review.go), and nothing commits them blindly;
// in a worktree of its own per wish and project, never the person's checkout, it merges the task's branch, makes the
// generated files again on a conflict only in them, runs the project's commit checks (checks.go) each through a gate,
// moves the integration branch when they pass, and removes the task's worktree when clean. Pushing the branch to its
// remote follows on a cadence (push.go).

// How the journal records the integration.
const (
	methodIntegrate = "harness/integrate" // a task's integration moved on, Djinn recorded a wish's integration branch, or asked what to do with work that failed; the request is the task, the wish, or the question
	methodCommit    = "harness/commit"    // a batch was committed into its wish's integration branch; the request is the commit (planv1.IntegrationCommit)
)

// The gates the integration's commands run under, as a worker's would: djinn gate run gen. Each check runs under
// the gate of its name, djinn gate run test, and the setup under djinn gate run setup.
const (
	genGate     = "gen"
	installGate = "install"
)

// TakeGate takes the gate name, for the task taskID, to run the command what in the folder dir, and returns how to
// give it back; first when Djinn takes it to merge a task's work into its branch, settle such a merge or check a push,
// which goes before the workers' requests (Q26), not to install a build. It calls waiting, when not nil, each time the
// reason it waits changes. djinn up takes the gates of internal/gate, as djinn gate run does.
type TakeGate func(ctx context.Context, name, taskID, what, dir string, first bool, waiting func(why string)) (give func(), err error)

// RunCommand runs args in dir, and returns the end of its output and its exit code; an error when it could not run.
type RunCommand func(ctx context.Context, dir string, args []string) (out string, code int, err error)

// WithGates runs the integration's commands under the gates take gives; without it, they run at once.
func WithGates(take TakeGate) Option { return func(h *Harness) { h.gates = take } }

// WithCommands runs the integration's commands with run, instead of the processes they name. Tests give a fake.
func WithCommands(run RunCommand) Option { return func(h *Harness) { h.commands = run } }

// TaskSummary is what a task of the build brought, and its worker's final words.
type TaskSummary struct {
	Code    string
	Title   string
	Summary string
}

// Built is a wish's integration branch as Djinn pushed it, in a project whose settings name an install command: djinn
// up proposes to install it, and to restart on it (WithBuilt).
type Built struct {
	WishID, WishTitle, ProjectID, Project, Branch, Sha string
	// The tasks whose work the push brought, by code.
	Tasks []string
	// The titles of the commits the push brought, the latest first.
	Changes []string
	// What changed, one per task: its code and title, and its worker's summary.
	Summaries []TaskSummary
	// The install command.
	Install string
	// The commit the running Djinn was built from, when the project holds it: Tasks, Summaries and Changes are then
	// what the build adds to it (compare.go). Empty: they are what the push brought.
	Base string
	// The running Djinn was built from Branch.
	Running bool
	// How many commits, merges aside, the build adds to the running Djinn; Changes holds the latest.
	ChangesCount int
	// The titles of the commits the running Djinn holds and the build does not, the latest first, and how many.
	Lacks      []string
	LacksCount int
	// The line of the running Djinn this build carries on from another branch: Sha holds the commit the running
	// Djinn was built from (compare.go). The branch that one was built from, or its short commit; empty for a build of
	// that branch (Running) and for a parallel one.
	Continues string
	// Djinn installs it by itself (installsItself): a build of the running Djinn's line, the push checks passed, in a
	// project whose install_builds is on. Other builds are only offered.
	Auto bool
}

// WithBuilt calls built after each push of an integration branch in a project whose settings name an install command.
func WithBuilt(built func(Built)) Option { return func(h *Harness) { h.built = built } }

// WithIntegrateTick sets how often the integration looks at the work waiting without being woken: an hour passes
// without telling anyone.
func WithIntegrateTick(d time.Duration) Option { return func(h *Harness) { h.integrateTick = d } }

// Integrate starts committing the tasks' finished work into their wishes' integration branches, by itself, and
// pushing them: woken when a task ends done or a push is approved, and every minute. djinn up calls it once the
// harness has recovered; Close stops it, and the work it was on is integrated again at the next start.
func (h *Harness) Integrate() {
	h.integrating.Do(func() {
		h.integrateDone = make(chan struct{})
		go func() {
			defer logx.Crash("integrate")
			defer close(h.integrateDone)
			tick := time.NewTicker(cmp.Or(h.integrateTick, time.Minute))
			defer tick.Stop()
			for {
				h.integratePass(h.ctx)
				select {
				case <-h.ctx.Done():
					return
				case <-h.integrateKick:
				case <-tick.C:
				}
			}
		}()
	})
}

// kickIntegrate asks the integration for a pass: a task's work waits. The pushes run apart look whether it is queued for
// their branch (queueMoved).
func (h *Harness) kickIntegrate() {
	select {
	case h.integrateKick <- struct{}{}:
	default:
	}
	h.queueMoved()
}

// pendIntegration marks the work of t, a task its worker just finished, as waiting to be merged, when Djinn
// integrates it: a work task on a branch of its own, in a project whose settings name a check, or, naming none, into
// a branch of the wish's own (uncheckedBranch). Otherwise it returns why the work stays out when its branch holds
// commits the integration branch lacks: no done task with commits is left out silently (W4 of wish Dew, 12/10/2026).
func (h *Harness) pendIntegration(t *planv1.Task) (bool, string) {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || t.GetBranch() == "" || plan.IsAzima(t) {
		return false, ""
	}
	if dispatch.ChangesNoCode(t) { // Said whether its branch holds commits or not: no integration, ever.
		if dispatch.Mockup(t) {
			return false, mockupLeft
		}
		return false, investigationLeft
	}
	ctx := context.Background()
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil || !project.GetGit() {
		return false, ""
	}
	if why := nothingOfItsOwn(ctx, project.GetDirectory(), t); why != "" {
		return false, why
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		t.Integration = &planv1.TaskIntegration{
			State:      planv1.IntegrationState_INTEGRATION_STATE_PENDING,
			Reason:     fmt.Sprintf("settings unreadable: %v", err),
			UpdateTime: timestamppb.New(h.now()),
		}
		return true, ""
	}
	if !settings.Integrates() {
		wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
		if err != nil {
			return false, ""
		}
		// Work held out of its azima's branch waits, as in a checked project (heldOutError).
		if _, err := h.uncheckedBranch(ctx, wish, project, settings, t); err != nil && !errors.As(err, new(*heldOutError)) {
			into := plan.IntegrationBranchOf(wish, project.GetId())
			if into == "" {
				into, _ = plan.ReferenceName(ctx, h.home, project)
			}
			if !branchHoldsUnmergedCommits(ctx, project.GetDirectory(), t.GetBranch(), into) {
				return false, ""
			}
			return false, err.Error()
		}
	}
	t.Integration = &planv1.TaskIntegration{
		State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, UpdateTime: timestamppb.New(h.now()),
	}
	return true, ""
}

// uncheckedBranch is the branch Djinn merges the work of t, a task of wish, into in project, a project whose settings
// name no check: the wish's own integration branch there, as djinn wish set-integration names it; per azima, the
// branch the developer named for t's azima (djinn task update <azima> --branch); merged as any work, no check run (W4
// of wish Dew, 12/10/2026). Otherwise "" and why the work stays out: Djinn commits nothing untested into the project's
// main branch, nor into an azima's branch Djinn named, which lands there; a *heldOutError when the work waits, its azima
// done or its branch deleted (the lead told).
func (h *Harness) uncheckedBranch(ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, t *planv1.Task) (string, error) {
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		if azima, err := h.taskAzima(ctx, t); err == nil && azima.GetAzima().GetNamed() {
			branch, err := h.azimaBranchFor(ctx, wish, project, settings, azima)
			if err != nil {
				return "", err
			}
			return branch, nil
		}
		return "", errors.New(errNoChecks.Error() + ", and Djinn merges no untested work into a branch it named for an azima")
	}
	branch := plan.IntegrationBranchOf(wish, project.GetId())
	if branch != "" {
		if main, _, err := resolveMainSha(ctx, project.GetDirectory(), settings); err == nil && !strings.EqualFold(main, branch) {
			return branch, nil
		}
	}
	return "", errors.New(errNoChecks.Error() + ", and untested work goes only to a wish's own branch (djinn wish set-integration --create-branch)")
}

// nothingOfItsOwn says why the work of t, a done task, has nothing to merge: its branch holds no commit beyond the one
// it started from, and its worktree nothing not committed; "" when it has work. Such a task is not merged, and no merge
// commit is made for it (W196: task W103 of wish Dew 13 oct, 17/10/2026, deleted branches and committed nothing, yet
// Djinn made its azima's deleted branch again, an empty merge in it, and pushed it). A review or a correction worker
// merges the work it reviews or corrects: never left out here.
func nothingOfItsOwn(ctx context.Context, repo string, t *planv1.Task) string {
	if t.GetReview() != nil || t.GetCorrection() != nil || !branchHasNoCommits(ctx, repo, t.GetBranch()) {
		return ""
	}
	if status, err := uncommitted(ctx, t); err != nil || len(status) > 0 {
		return "" // Changes not committed: a review worker judges them (reviewUncommitted).
	}
	return nothingToMerge
}

// nothingToMerge is why a done task with no commit of its own is not merged, told as it is in its end's line.
const nothingToMerge = "nothing to merge: no commit of its own"

// leaveOut takes the work of t, waiting to be merged, out of the integration for why: no record left, as a task Djinn
// does not integrate, said in an event, and its end told to the lead with why.
func (h *Harness) leaveOut(ctx context.Context, t *planv1.Task, why string) {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Task](ctx, tx, t.GetId())
		if err != nil {
			return err
		}
		cur.Integration = nil
		if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
			return err
		}
		if err := tx.Put(cur); err != nil {
			return err
		}
		return putEvents(ctx, tx, newEvent(cur.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "integration: none: " + why}))
	})
	if err != nil {
		logx.L().Error("record the integration", "loop", "integrate", "task", t.GetCode(), "err", err)
		return
	}
	h.notify()
	if tellsDone(t) && !toldDoneAtEnd(t, true) {
		h.tellDone(ctx, t, notIntegrated(why), "")
	}
}

// notIntegrated is what the lead hears of a done task's work left out of its wish's branch, for the reason left; ""
// when none. Work that brings nothing is not said left out: there is nothing to merge.
func notIntegrated(left string) string {
	switch left {
	case "", nothingToMerge:
		return left
	case investigationLeft, mockupLeft:
		return "integration none: " + left
	}
	return "not integrated: " + left
}

// recoverIntegrations puts back to pending the work a previous djinn up was integrating when it stopped: its batch
// is integrated again. It returns those tasks' codes.
func (h *Harness) recoverIntegrations(ctx context.Context, tasks []*planv1.Task) []string {
	var cut []*planv1.Task
	for _, t := range tasks {
		if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING {
			cut = append(cut, t)
		}
	}
	if len(cut) > 0 {
		h.settleIntegration(ctx, cut, pending(cut[0].GetIntegration().GetBranch(), ""),
			"integration: djinn up stopped during it; Djinn integrates the work again", nil)
	}
	codes := make([]string, len(cut))
	for i, t := range cut {
		codes[i] = t.GetCode()
	}
	return codes
}

// integratePass commits the finished work waiting, branch by branch, in batches (repair.go): the work queued as one
// batch, a branch that conflicts after it alone, nothing but a correction's own work while it repairs the branch. It
// pushes the integration branches whose push is due after each commit, and the pushes the person approved; then it
// merges main into the integration branches where it is due (main.go).
func (h *Harness) integratePass(ctx context.Context) {
	ctx = withPassMemo(ctx)
	start := time.Now()
	logx.L().Debug("integration pass begins")
	defer func() { logx.Took(slog.LevelDebug, "integration pass ended", start) }()
	tasks, err := store.List[*planv1.Task](ctx, h.store, nil)
	if err != nil {
		logx.L().Error("read the tasks", "loop", "integrate", "err", err)
		return
	}
	h.correctionsEnded(ctx, tasks)
	var waiting []*planv1.Task
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || h.running(t.GetId()) {
			continue
		}
		if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			waiting = append(waiting, t)
			continue
		}
	}
	slices.SortStableFunc(waiting, func(a, b *planv1.Task) int {
		return a.GetEndTime().AsTime().Compare(b.GetEndTime().AsTime())
	})
	// The work waiting goes by branch, each branch's in the order its first task ended (repair.go).
	var lanes []*lane
	for _, t := range waiting {
		wish, err1 := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
		project, err2 := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
		if err := errors.Join(err1, err2); err != nil {
			logx.L().Error("read the wish and the project", "loop", "integrate", "task", t.GetCode(), "wish", t.GetWishId(),
				"project", t.GetProjectId(), "err", err)
			continue
		}
		// Work queued before Djinn left out a task with no commit of its own (pendIntegration) never takes a branch.
		if why := nothingOfItsOwn(ctx, project.GetDirectory(), t); why != "" {
			h.leaveOut(ctx, t, why)
			continue
		}
		branch := h.laneOf(ctx, wish, project, t)
		i := slices.IndexFunc(lanes, func(l *lane) bool {
			return branch != "" && l.branch == branch && l.wish.GetId() == wish.GetId() && l.project.GetId() == project.GetId()
		})
		if i < 0 {
			lanes = append(lanes, &lane{wish: wish, project: project, branch: branch})
			i = len(lanes) - 1
		}
		lanes[i].tasks = append(lanes[i].tasks, t)
	}
	var merged []*mergedBatch
	for _, l := range lanes {
		if ctx.Err() != nil {
			break // Stopping: what is merged already is still told.
		}
		merged = append(merged, h.integrateLane(ctx, l)...)
	}
	// Decision Q173: repeated lines of one kind within a pass go out as one line per (wishID, branch).
	if len(merged) > 0 {
		type mergeGroup struct {
			wishID  string
			branch  string
			codes   []string
			sha     string
			moves   []string
			reviews []string
		}
		var groups []*mergeGroup
		groupIndex := make(map[string]int)
		for _, m := range merged {
			key := m.wishID + "|" + m.branch
			idx, ok := groupIndex[key]
			if !ok {
				groupIndex[key] = len(groups)
				groups = append(groups, &mergeGroup{
					wishID:  m.wishID,
					branch:  m.branch,
					codes:   slices.Clone(m.codes),
					sha:     m.sha,
					moves:   slices.Clone(m.moves),
					reviews: slices.Clone(m.reviews),
				})
			} else {
				g := groups[idx]
				for _, c := range m.codes {
					if !slices.Contains(g.codes, c) {
						g.codes = append(g.codes, c)
					}
				}
				g.sha = m.sha
				for _, mv := range m.moves {
					if !slices.Contains(g.moves, mv) {
						g.moves = append(g.moves, mv)
					}
				}
				g.reviews = append(g.reviews, m.reviews...)
			}
		}
		for _, g := range groups {
			leadLine := leadMergeLine(strings.Join(g.codes, ", "), g.branch, g.sha, g.moves)
			h.tellLeadDetail(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, g.wishID, strings.Join(g.codes, ", "), leadLine,
				strings.Join(g.reviews, "\n"))
		}
	}
	for _, pass := range []struct {
		name string
		run  func(context.Context)
	}{{"approved pushes", h.pushApproved}, {"push", h.pushPass}, {"main", h.mainPass}, {"moves", h.computedMovesPass}} {
		if pass.name != "moves" && h.netHolds() {
			continue // Pushes and fetches wait for the network (network.go).
		}
		start := time.Now()
		pass.run(ctx)
		logx.Took(slog.LevelDebug, "pass ended", start, "pass", pass.name)
	}
}

// running tells whether a worker runs for the task id: a task continued after it was done.
func (h *Harness) running(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[id] != nil
}

// pending is the integration of work that waits to be merged, and why when it waits for something else.
func pending(branch, why string) *planv1.TaskIntegration {
	return &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_PENDING, Branch: branch, Reason: why}
}

// tested is a batch whose tests passed while its branch could not move, from old to sha: committing it again needs no
// new run.
type tested struct {
	old, sha string
	ids      []string
}

// mergedBatch is a batch committed to a branch in an integration pass, with what its review workers say of their
// commit (reviewLine).
type mergedBatch struct {
	wishID  string
	branch  string
	codes   []string
	sha     string
	moves   []string
	reviews []string
}

// taskBatchCodes returns the slice of codes for the tasks in batch.
func taskBatchCodes(batch []*planv1.Task) []string {
	c := make([]string, len(batch))
	for i, t := range batch {
		c[i] = t.GetCode()
	}
	return c
}

// integrateBatch commits the work of batch, a task of wish in project, with the work a review or a correction worker
// of it reviews or corrects, into the wish's integration branch, records where each task's work stands, and tells
// whether it is committed. A task whose worktree holds changes not committed is not merged: a review worker judges
// them. The worktrees of the work committed are removed when clean.
func (h *Harness) integrateBatch(ctx context.Context, wish *planv1.Wish, project *planv1.Project, batch []*planv1.Task) (bool, *mergedBatch) {
	logx.Lock(&h.integrateMu, "integration") // The push checks use the integration worktree too.
	defer h.integrateMu.Unlock()
	settings, err := plan.LoadSettings(h.home, project)
	var branch, why string
	var ok bool
	switch {
	case err != nil:
		why = fmt.Sprintf("settings unreadable: %v", err)
	case !settings.Integrates():
		var err error
		if branch, err = h.uncheckedBranch(ctx, wish, project, settings, batch[0]); err != nil {
			branch, why = plan.IntegrationBranchOf(wish, project.GetId()), err.Error()
			if held := (*heldOutError)(nil); errors.As(err, &held) {
				branch = held.branch
			}
		}
	default:
		branch, why, ok = h.targetBranch(ctx, wish, project, settings, batch)
		if !ok {
			return false, nil
		}
	}
	if why == "" {
		// Merged into a branch whose remote holds commits it lacks, the work could not be pushed: the branch is brought
		// up to the remote's first, or nothing merges (remote_branch.go).
		why = h.catchUp(ctx, wish, project.GetDirectory(), branch, true)
	}
	if why != "" {
		h.settleIntegration(ctx, batch, pending(branch, why), "integration: waiting: "+why, nil)
		return false, nil
	}
	if batch = h.reviewUncommitted(ctx, wish, project, settings, branch, batch); len(batch) == 0 {
		return false, nil
	}
	var remaining []*planv1.Task
	var alreadyIn []*planv1.Task
	head := integrationHead(ctx, project.GetDirectory(), branch) // Once for the batch: nothing moves it before its merges.
	for _, t := range batch {
		if taskWorkIn(ctx, project.GetDirectory(), t, head) {
			alreadyIn = append(alreadyIn, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	if len(alreadyIn) > 0 {
		committed := &planv1.TaskIntegration{
			State:  planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
			Branch: branch,
			Sha:    head,
		}
		h.settleIntegration(ctx, alreadyIn, committed, "integration: committed: "+branch+" holds the work already, at "+short8(head), nil)
		settled := append(slices.Clone(alreadyIn), h.settleReviewed(ctx, alreadyIn, committed)...)
		settled = append(settled, h.settleCorrected(ctx, settled, committed)...)
		h.settleMain(ctx, wish, project, settled, committed)
		for _, t := range settled {
			h.dropWorktree(ctx, project, t)
		}
		h.tellNothingMerged(ctx, alreadyIn, branch)
		h.wake()
		if len(remaining) == 0 {
			return true, nil
		}
		batch = remaining
	}
	h.setDoing("the integration of " + codes(batch) + " into " + branch)
	in, text, commit, batch := h.commitBatch(ctx, wish, project, settings, branch, batch)
	if len(batch) == 0 {
		return false, nil // Every branch of the batch conflicted: each merges alone (leaveBatch).
	}
	if ctx.Err() != nil && in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		h.cutPass()
		in, text, commit = pending(branch, ""), "integration: djinn up stopped during it; Djinn integrates the work again", nil
	}
	in.Branch = branch
	if in.GetFailure() != nil {
		h.failed(ctx, wish, project, settings, batch, in, text)
		return false, nil
	}
	h.settleIntegration(ctx, batch, in, text, commit)
	if in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		return false, nil
	}
	settled := append(slices.Clone(batch), h.settleReviewed(ctx, batch, in)...)
	settled = append(settled, h.settleCorrected(ctx, settled, in)...)
	h.settleMain(ctx, wish, project, settled, in) // A correction worker of main's merge brought main in.
	for _, t := range settled {
		h.dropWorktree(ctx, project, t)
	}
	var reviews []string
	for _, t := range batch {
		if t.GetReview() != nil {
			reviews = append(reviews, h.reviewLine(ctx, t))
		}
	}
	if commit == nil || commit.GetOldSha() == in.GetSha() {
		// Decision Q173: no merge line for a batch that moves nothing; its tasks' ends are told once all the same.
		h.tellNothingMerged(ctx, batch, branch)
		h.wake()
		return true, nil
	}
	oldSha := commit.GetOldSha()
	moves := h.handleMergeMoves(ctx, wish, project, branch, oldSha, in.GetSha())
	h.wake() // The tasks that waited for this work to be committed may start, from it.
	return true, &mergedBatch{
		wishID:  wish.GetId(),
		branch:  branch,
		codes:   taskBatchCodes(batch),
		sha:     in.GetSha(),
		moves:   moves,
		reviews: reviews,
	}
}

// tellNothingMerged tells the lead that the work workers of batch ended done, their work bringing nothing to branch:
// no merge line tells it (incident W1 of wish Dew, 12/10/2026). One told when it ended is not told again. A review
// worker's end comes with what it committed and left (reviewLine).
func (h *Harness) tellNothingMerged(ctx context.Context, batch []*planv1.Task, branch string) {
	for _, t := range batch {
		switch {
		case t.GetReview() != nil:
			h.tellLeadDetail(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, t.GetWishId(), t.GetCode(),
				"Djinn: "+t.GetCode()+" done, nothing to merge into "+branch+".", h.reviewLine(ctx, t))
		case tellsDone(t) && !toldDoneAtEnd(t, true):
			h.tellDone(ctx, t, "nothing to merge into "+branch, "")
		}
	}
}

// reviewLine is what the lead reads of review worker r's end: the work it reviewed, the line of its final note that
// says what it committed and what it left and why (reviewReport), or that it says nothing of it, and the worktree that
// keeps what it left.
func (h *Harness) reviewLine(ctx context.Context, r *planv1.Task) string {
	line := r.GetCode() + " reviewed the work left not committed"
	if served := taskCodes(ctx, h.store, r.GetReview().GetTaskIds()); served != "" {
		line = r.GetCode() + " reviewed the work " + served + " left not committed"
	}
	note := h.finalNote(ctx, r.GetId())
	switch report := reviewReport(note); {
	case report != "":
		line += ": " + report
	case note != "":
		line += "; its final note says nothing of what it committed and left, it begins: " + firstLine(note)
	default:
		line += "; it left no final note."
	}
	if left, err := uncommitted(ctx, r); err == nil && len(left) > 0 {
		line += " Its worktree keeps what it left: " + r.GetWorktree()
	}
	return line
}

// dropWorktree removes the worktree of t, whose work is committed, as djinn task clean does: its branch stays. A
// worktree with changes not committed stays, said; so does the worktree of a task a worker runs again.
func (h *Harness) dropWorktree(ctx context.Context, project *planv1.Project, t *planv1.Task) {
	ctx = context.WithoutCancel(ctx)
	cur, err := store.Get[*planv1.Task](ctx, h.store, t.GetId())
	if err != nil || cur.GetWorktree() == "" || h.running(t.GetId()) {
		return
	}
	text := "worktree removed, branch " + cur.GetBranch() + " kept"
	// A review worker works in the worktree of the task it reviews: the second of them finds it removed already.
	// git worktree remove refuses a worktree holding changes, its own status read: the status is read again only to say
	// why it stays.
	if _, err := os.Stat(cur.GetWorktree()); !errors.Is(err, os.ErrNotExist) {
		if err := removeWorktree(ctx, project.GetDirectory(), cur.GetWorktree(), false); err != nil {
			text = "worktree kept: " + err.Error()
			if changes, err := git(ctx, cur.GetWorktree(), "status", "--porcelain"); err == nil && changes != "" {
				text = "worktree kept: it holds changes not committed, " + cur.GetWorktree()
			}
		}
	}
	kept := strings.HasPrefix(text, "worktree kept")
	if !kept && cur.GetScratch() != "" {
		_ = os.RemoveAll(cur.GetScratch())
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		task, err := store.Get[*planv1.Task](ctx, tx, t.GetId())
		if err != nil {
			return err
		}
		if !kept {
			task.Worktree = ""
			task.Scratch = ""
		}
		if err := tx.Journal(actorHarness, methodIntegrate, task); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return putEvents(ctx, tx, newEvent(task.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text}))
	})
	if err != nil {
		logx.L().Error("record the worktree", "loop", "integrate", "task", t.GetCode(), "err", err)
	}
	h.notify()
}

// How long the lists of a build proposed are: the commits' titles, and a worker's last paragraph.
const (
	buildChanges = 30
	buildSummary = 400
)

// build is the integration branch as push pushed it, with the work of tasks, to propose for installing.
func (h *Harness) build(ctx context.Context, wish *planv1.Wish, project *planv1.Project, install string, push *planv1.IntegrationPush, tasks []*planv1.Task) Built {
	b := Built{
		WishID: wish.GetId(), WishTitle: wish.GetTitle(), ProjectID: project.GetId(), Project: project.GetName(),
		Branch: push.GetBranch(), Sha: push.GetNewSha(), Changes: push.GetCommits(), Install: install,
	}
	for _, t := range tasks {
		b.Tasks = append(b.Tasks, t.GetCode())
		b.Summaries = append(b.Summaries, TaskSummary{
			Code:    t.GetCode(),
			Title:   t.GetTitle(),
			Summary: h.lastWords(ctx, t.GetId()),
		})
	}
	h.compare(ctx, project, &b)
	return b
}

// lastWords is the last paragraph the worker of the task wrote, on one line, where it says what it did;
// "" when it wrote nothing.
func (h *Harness) lastWords(ctx context.Context, taskID string) string {
	var last *planv1.TaskEvent
	err := store.Latest(ctx, h.store, store.Where{"task_id": taskID}, func(e *planv1.TaskEvent) bool {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && strings.TrimSpace(e.GetText()) != "" {
			last = e
		}
		return last == nil
	})
	if err != nil || last == nil {
		return ""
	}
	paragraphs := strings.Split(strings.TrimSpace(last.GetText()), "\n\n")
	words := strings.Join(strings.Fields(paragraphs[len(paragraphs)-1]), " ")
	if r := []rune(words); len(r) > buildSummary {
		words = string(r[:buildSummary-1]) + "…"
	}
	return words
}

// InstallStep is where an install stands, as Install tells it.
type InstallStep int

const (
	// InstallWaiting: it waits for its turn, for what Install says with it.
	InstallWaiting InstallStep = iota + 1
	// InstallPreparing: it checks the build out in its worktree, and sets it up.
	InstallPreparing
	// InstallBuilding: it runs the install command.
	InstallBuilding
)

// Install runs the install command of the project's settings on sha, a build of the wish's integration branch there:
// in the project's install worktree, checked out at it, never the person's checkout nor an integration worktree, under
// the install gate. It never waits for an integration: the build is a commit already made, and its worktree is its
// own. One install runs at a time. progress, when not nil, is told each step as it comes, and what the install waits
// for when it waits ("" otherwise). It returns the end of the command's output, and an error when it did not end well.
func (h *Harness) Install(ctx context.Context, wishID, projectID, sha string, progress func(step InstallStep, waiting string)) (string, error) {
	if progress == nil {
		progress = func(InstallStep, string) {}
	}
	ctx = context.WithValue(ctx, installingKey{}, true)
	wish, err1 := store.Get[*planv1.Wish](ctx, h.store, wishID)
	project, err2 := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err := errors.Join(err1, err2); err != nil {
		return "", err
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		return "", err
	}
	if settings.Install == "" {
		return "", errors.New("the project's settings name no install command")
	}
	branch := plan.IntegrationBranchOf(wish, projectID)
	branches := []string{branch}
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		branches = append(branches, h.azimaBranchesOf(ctx, wish, project)...)
	}
	found := false
	for _, b := range branches {
		if b == "" {
			continue
		}
		if _, err := git(ctx, project.GetDirectory(), "merge-base", "--is-ancestor", sha, "refs/heads/"+b); err == nil {
			found = true
			branch = b
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%s is not a commit of the wish's integration branch %s", short8(sha), branch)
	}
	if !h.installMu.TryLock() {
		h.mu.Lock()
		other := h.installing
		h.mu.Unlock()
		progress(InstallWaiting, "the install of "+short8(other)+" to end")
		h.installMu.Lock()
	}
	defer h.installMu.Unlock()
	h.mu.Lock()
	h.installing = sha
	h.mu.Unlock()
	step := InstallPreparing
	progress(step, "")
	ctx = context.WithValue(ctx, gateWaitKey{}, func(why string) {
		if why == "" {
			progress(step, "")
		} else {
			progress(InstallWaiting, why)
		}
	})
	wt := installDir(h.home, projectID)
	dir, _, err := integrationWorktree(ctx, project.GetDirectory(), wt, sha)
	if err != nil {
		return "", fmt.Errorf("prepare the install worktree: %w", err)
	}
	if why, out, _, _ := h.setUp(ctx, project, settings, wishID, "", wt, dir); why != "" {
		return out, fmt.Errorf("%s: %s", setupName, why)
	}
	step = InstallBuilding
	progress(step, "")
	out, code, err := h.command(ctx, installGate, "", settings.Install, dir)
	switch {
	case err != nil:
		return out, fmt.Errorf("%s could not run: %w", settings.Install, err)
	case code != 0:
		return out, fmt.Errorf("%s exited %d%s", settings.Install, code, tail(out))
	}
	h.mu.Lock()
	h.installed[projectID] = sha
	h.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(installedFile(h.home, projectID)), 0o755) // The write below fails too.
	if err := fsx.WriteAtomic(installedFile(h.home, projectID), []byte(sha+"\n"), 0o600); err != nil {
		logx.L().Warn("record the installed build", "project", projectID, "sha", short8(sha), "err", err)
	}
	h.closeInstallQuestions(ctx, projectID, sha)
	return out, nil
}

// installDir is the worktree the builds of a project are installed from, whatever their wish: in Djinn's data folder,
// next to its integration worktrees, apart from them so that an install never waits for an integration.
func installDir(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "install")
}

// installedFile is the file that records the sha of the build installed last for projectID.
func installedFile(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "installed")
}

// installedSha returns the sha of the build installed last in projectID:
// checked from memory first, then installed file, then the install worktree's HEAD.
func (h *Harness) installedSha(ctx context.Context, projectID string) string {
	h.mu.Lock()
	sha, ok := h.installed[projectID]
	h.mu.Unlock()
	if ok && sha != "" {
		return sha
	}
	if data, err := os.ReadFile(installedFile(h.home, projectID)); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			h.mu.Lock()
			h.installed[projectID] = s
			h.mu.Unlock()
			return s
		}
	}
	wt := installDir(h.home, projectID)
	if _, err := os.Stat(wt); err == nil {
		if s := commitOf(ctx, wt, "HEAD"); s != "" {
			h.mu.Lock()
			h.installed[projectID] = s
			h.mu.Unlock()
			return s
		}
	}
	return ""
}

// closeInstallQuestions closes open install questions for projectID when sha was installed:
// the question for sha is marked installed (Choice A); open questions for older shas are marked superseded (Choice B).
func (h *Harness) closeInstallQuestions(ctx context.Context, projectID, sha string) {
	questions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		return
	}
	wishesByID := make(map[string]*planv1.Wish)
	s := short8(sha)
	for _, q := range questions {
		if !q.GetMove() || q.GetAnswer() != nil {
			continue
		}
		if !strings.Contains(q.GetText(), "Install and restart") {
			continue
		}
		if !h.isQuestionForProject(ctx, q, projectID, wishesByID) {
			continue
		}
		if strings.Contains(q.GetText(), s) {
			_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "installed")
		} else {
			_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_B, fmt.Sprintf("superseded by build %s", s))
		}
	}
}

// detectInstallReason inspects the wish, project, and git commits to determine if an install and restart
// question is necessary under InstallQuestions_WHEN_NEEDED. Returns nil when no question should be raised.
func (h *Harness) detectInstallReason(
	ctx context.Context,
	wish *planv1.Wish,
	project *planv1.Project,
	branch, inst, sha string,
) *installReason {
	repo := project.GetDirectory()
	tasks, _ := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})

	// 1. Broken build: running build crashed or has a panic/sigsegv error, or a failed check after install
	for _, t := range tasks {
		errLower := strings.ToLower(t.GetError())
		if strings.Contains(errLower, "panic:") ||
			strings.Contains(errLower, "crash") ||
			strings.Contains(errLower, "segmentation fault") ||
			strings.Contains(errLower, "sigsegv") ||
			strings.Contains(errLower, "signal: killed") ||
			strings.Contains(errLower, "check failed") ||
			strings.Contains(errLower, "install check failed") {
			code := t.GetCode()
			if code == "" {
				code = t.GetId()
			}
			return &installReason{
				kind:       "broken",
				whyText:    "running build is broken (crash)",
				whyContext: fmt.Sprintf("the running build crashed or failed checks in %s (%s)", code, t.GetError()),
			}
		}
	}

	// 2. Commit log of the new build
	rangeArg := sha
	if inst != "" && inst != sha {
		if _, err := git(ctx, repo, "merge-base", inst, sha); err == nil {
			rangeArg = inst + ".." + sha
		}
	}
	logOut, _ := git(ctx, repo, "log", "--format=%s%n%b", rangeArg)
	if logOut == "" {
		logOut, _ = git(ctx, repo, "log", "-n", "50", "--format=%s%n%b", sha)
	}

	// 3. Fix of a task or question developer is waiting on
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED &&
			t.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING &&
			t.GetStatus() != planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
			continue
		}
		code := t.GetCode()
		if code == "" {
			code = t.GetId()
		}
		pattern := `(?i)\b(` + regexp.QuoteMeta(code)
		if t.GetId() != "" && t.GetId() != code {
			pattern += `|` + regexp.QuoteMeta(t.GetId())
		}
		pattern += `)\b`
		codeRe := regexp.MustCompile(pattern)
		matchesLog := codeRe.MatchString(logOut) || (t.GetTitle() != "" && strings.Contains(strings.ToLower(logOut), strings.ToLower(t.GetTitle())))
		matchesFork := false
		for _, t2 := range tasks {
			if (t2.GetIntegration().GetSha() == sha || t2.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE) &&
				(t2.GetForkOf() == code || t2.GetDecision() == code) {
				matchesFork = true
				break
			}
		}
		if matchesLog || matchesFork {
			desc := code
			if t.GetTitle() != "" {
				desc = fmt.Sprintf("%s (%s)", code, t.GetTitle())
			}
			return &installReason{
				kind:       "fix",
				whyText:    fmt.Sprintf("carries fix for %s", code),
				whyContext: fmt.Sprintf("it carries the fix for %s", desc),
			}
		}
	}

	// Check waiting questions
	questions, _ := store.List[*planv1.Question](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	for _, q := range questions {
		if q.GetAnswer() != nil {
			continue
		}
		code := q.GetCode()
		if code == "" {
			code = q.GetId()
		}
		pattern := `(?i)\b(` + regexp.QuoteMeta(code)
		if q.GetId() != "" && q.GetId() != code {
			pattern += `|` + regexp.QuoteMeta(q.GetId())
		}
		pattern += `)\b`
		codeRe := regexp.MustCompile(pattern)
		if codeRe.MatchString(logOut) {
			return &installReason{
				kind:       "fix",
				whyText:    fmt.Sprintf("carries fix for %s", code),
				whyContext: fmt.Sprintf("it carries the fix for question %s", code),
			}
		}
	}

	// 4. Change that only takes effect after restart and matters now (harness fix for failing workers)
	diffOut, _ := git(ctx, repo, "diff", "--name-only", rangeArg)
	if diffOut == "" {
		diffOut, _ = git(ctx, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
	}
	var harnessFiles []string
	for _, line := range strings.Split(diffOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "cmd/") || strings.HasPrefix(line, "internal/harness/") || strings.HasPrefix(line, "internal/") {
			harnessFiles = append(harnessFiles, line)
		}
	}
	if len(harnessFiles) > 0 {
		var failingWorkers []string
		for _, t := range tasks {
			if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_FAILED || t.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
				c := t.GetCode()
				if c == "" {
					c = t.GetId()
				}
				failingWorkers = append(failingWorkers, c)
			}
		}
		if len(failingWorkers) > 0 {
			workersList := strings.Join(failingWorkers, ", ")
			return &installReason{
				kind:       "restart",
				whyText:    fmt.Sprintf("harness fix for %s", workersList),
				whyContext: fmt.Sprintf("harness changes take effect after restart and affect failing workers %s", workersList),
			}
		}
	}

	return nil
}

// isQuestionForProject tells whether question q belongs to a wish that targets projectID.
func (h *Harness) isQuestionForProject(ctx context.Context, q *planv1.Question, projectID string, wishesByID map[string]*planv1.Wish) bool {
	if q.GetWishId() == "" {
		return false
	}
	w, ok := wishesByID[q.GetWishId()]
	if !ok {
		var err error
		w, err = store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
		if err != nil {
			return false
		}
		wishesByID[q.GetWishId()] = w
	}
	return slices.Contains(w.GetProjectIds(), projectID)
}

// closeMove records answer on an open move question q, journaled and notified.
func (h *Harness) closeMove(ctx context.Context, q *planv1.Question, choice planv1.Choice, note string) error {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
		if err != nil {
			return err
		}
		if cur.GetAnswer() != nil {
			return nil
		}
		cur.Answer = &planv1.Answer{
			Choice:     choice,
			Note:       note,
			CreateTime: timestamppb.New(h.now()),
		}
		if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
			return err
		}
		return tx.Put(cur)
	})
	if err != nil {
		logx.L().Error("close the move question", "loop", "move", "question", q.GetCode(), "wish", q.GetWishId(), "err", err)
		return err
	}
	q.Answer = &planv1.Answer{
		Choice:     choice,
		Note:       note,
		CreateTime: timestamppb.New(h.now()),
	}
	h.notify()
	return nil
}

// integrationBranch is the branch wish integrates its work into in project, and why there is none. A wish that records
// none takes the project's reference branch, recorded on the wish; never the branch the developer's copy has checked
// out (W227).
func (h *Harness) integrationBranch(ctx context.Context, wish *planv1.Wish, project *planv1.Project) (string, string) {
	if b := plan.IntegrationBranchOf(wish, project.GetId()); b != "" {
		return b, ""
	}
	b, _ := plan.ReferenceName(ctx, h.home, project)
	if b == "" {
		return "", "the project has no reference branch: name the integration branch with djinn wish set-integration"
	}
	plan.SetIntegrationBranch(wish, project.GetId(), b)
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Wish](ctx, tx, wish.GetId())
		if err != nil {
			return err
		}
		plan.SetIntegrationBranch(cur, project.GetId(), b)
		if err := tx.Journal(actorHarness, methodIntegrate, cur); err != nil {
			return err
		}
		return tx.Put(cur)
	})
	if err != nil {
		return "", "record the integration branch: " + err.Error()
	}
	return b, ""
}

// commitBatch merges the branches of batch, tasks whose worktree holds nothing not committed, into the integration
// branch in the wish's integration worktree, checks the result, and moves the branch when the commit checks pass. It
// returns where the batch's work stands, the event that says it, the commit to journal when the branch moved, and the
// batch without the branches that conflicted and left it (leaveBatch): none left, nothing else is said.
func (h *Harness) commitBatch(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch string, batch []*planv1.Task,
) (*planv1.TaskIntegration, string, *planv1.IntegrationCommit, []*planv1.Task) {
	repo := project.GetDirectory()
	wait := func(why string) (*planv1.TaskIntegration, string, *planv1.IntegrationCommit, []*planv1.Task) {
		return pending(branch, why), "integration: waiting: " + why, nil, batch
	}
	old, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return wait("the branch " + branch + " is not in the project's repository")
	}
	if err := checkIdentity(ctx, repo); errors.Is(err, errNoIdentity) {
		return wait(noIdentity)
	}
	taskIDs := ids(batch)
	key := wish.GetId() + "/" + project.GetId() + "/" + branch
	done, ok := h.tested[key]
	if !ok || done.old != old || !slices.Equal(done.ids, taskIDs) {
		h.settleIntegration(ctx, batch, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: branch,
		}, "integration: integrating into "+branch+", with "+codes(batch), nil)
		var in *planv1.TaskIntegration
		var text string
		var left []leftOut
		done.sha, in, text, left = h.mergeAndTest(ctx, wish, project, settings, branch, old, batch)
		if len(left) > 0 {
			h.leaveBatch(ctx, left, branch)
			batch = slices.DeleteFunc(slices.Clone(batch), func(t *planv1.Task) bool {
				return slices.ContainsFunc(left, func(l leftOut) bool { return l.task.GetId() == t.GetId() })
			})
			if len(batch) == 0 {
				return nil, "", nil, nil
			}
			taskIDs = ids(batch)
		}
		if in != nil {
			return in, text, nil, batch
		}
		done.old, done.ids = old, taskIDs
		h.tested[key] = done
	}
	sha := done.sha
	commit := &planv1.IntegrationCommit{
		WishId: wish.GetId(), ProjectId: project.GetId(), Branch: branch, OldSha: old, NewSha: sha,
		TaskIds: append(append(taskIDs, correctedIDs(batch)...), reviewedIDs(batch)...),
	}
	committed := &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Sha: sha}
	if sha == old {
		delete(h.tested, key)
		return committed, "integration: committed: " + branch + " holds the work already, at " + short8(sha), nil, batch
	}
	holder, why, moved := moveBranch(ctx, repo, branch, old, sha, "djinn: integrate "+codes(batch))
	if why != "" {
		if moved {
			delete(h.tested, key)
			why += ": Djinn integrates the work again"
		}
		return wait(why)
	}
	delete(h.tested, key)
	text := fmt.Sprintf("integration: committed into %s as %s, with %s", branch, short8(sha), codes(batch))
	if holder != "" {
		text += "; your checkout of it, " + holder + ", follows"
	}
	return committed, text, commit, batch
}

// moveBranch moves branch from old to sha, tested green, in the repository holding repo, git checking it is still at
// old; the person's checkout of it, if any, follows when clean. It returns that checkout ("" for none); or why the
// branch stays at old, and whether it is because the branch moved meanwhile. Once begun, the move ends whole though
// djinn up stops: the branch moved, its checkout follows it, or the branch goes back (W52: the put back failed on the
// cancel, and the checkout showed the work undone).
func moveBranch(ctx context.Context, repo, branch, old, sha, message string) (holder, why string, moved bool) {
	if ctx.Err() != nil {
		return "", "djinn up stopped before " + branch + " moved", false
	}
	ctx, done := gitx.Uncut(ctx)
	defer done()
	// The person's checkout of the branch, if any, follows it when clean. One with changes is left alone, and the
	// branch with it: moved under it, its next commit would undo the work.
	holder, err := checkoutOf(ctx, repo, branch)
	if err != nil {
		return "", err.Error(), false
	}
	if holder != "" {
		if changes, err := git(ctx, holder, "status", "--porcelain", "--untracked-files=no"); err != nil || changes != "" {
			return "", fmt.Sprintf("tested green as %s; %s stays at %s: your checkout of it, %s, has changes not committed "+
				"and is left as it is; Djinn moves the branch once they are committed or put aside", short8(sha), branch, short8(old), holder), false
		}
	}
	if _, err := git(ctx, repo, "update-ref", "-m", message, "refs/heads/"+branch, sha, old); err != nil {
		return "", branch + " moved during the integration", true
	}
	if holder != "" {
		// A two-way merge from the old tip to the new one: what a fast-forward does to the files, refused when one in
		// the way is not tracked.
		if _, err := git(ctx, holder, "read-tree", "-m", "-u", old, sha); err != nil {
			if _, back := git(ctx, repo, "update-ref", "refs/heads/"+branch, old, sha); back != nil {
				logx.L().Error("put the branch back", "loop", "integrate", "branch", branch, "at", short8(old), "err", back)
			}
			return "", fmt.Sprintf("tested green as %s; %s stays at %s: your checkout of it, %s, cannot follow: %v",
				short8(sha), branch, short8(old), holder, err), false
		}
	}
	return holder, "", false
}

// mergeAndTest merges batch's branches from old in the wish's integration worktree, one on top of the other, makes it
// ready with the setup, and runs the commit checks there, once for the batch. It returns the commit checked green; or
// where the batch stands when it is not, and the event that says it. A conflict in code, or a red check, says what
// failed as a correction worker starts from it (IntegrationFailure). In a batch of several, a branch that conflicts
// leaves it, its merge undone, the others merged without it: it is among left, with why.
func (h *Harness) mergeAndTest(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, old string, batch []*planv1.Task,
) (sha string, in *planv1.TaskIntegration, text string, left []leftOut) {
	fail := func(state planv1.IntegrationState, why string, f *planv1.IntegrationFailure) (string, *planv1.TaskIntegration, string, []leftOut) {
		what := "conflict"
		if state == planv1.IntegrationState_INTEGRATION_STATE_RED {
			what = "red"
		}
		if f != nil {
			f.State, f.Reason = state, why
		}
		return "", &planv1.TaskIntegration{State: state, Reason: why, Failure: f},
			"integration: " + what + ": " + why + "; " + branch + " stays as it was", left
	}
	wait := func(why string) (string, *planv1.TaskIntegration, string, []leftOut) {
		return "", pending(branch, why), "integration: waiting: " + why, left
	}
	repo := project.GetDirectory()
	wt := integrationDir(h.home, project.GetId(), wish.GetId())
	dir, prefix, err := integrationWorktree(ctx, repo, wt, old)
	if err != nil {
		return wait("prepare the integration worktree: " + err.Error())
	}
	// A merge begun ends, or is aborted, though djinn up stops: none is left half done in the worktree (W52).
	keep, done := gitx.Uncut(ctx)
	defer done()
	var merged []*planv1.Task
	empty := false // A merge that changed nothing was undone.
	head := old    // The worktree's HEAD, as integrationWorktree or the last merge left it; "" to read it again.
	for _, t := range batch {
		if head == "" {
			if head, err = git(ctx, wt, "rev-parse", "HEAD"); err != nil {
				return wait(err.Error())
			}
		}
		base := head
		head = "" // A merge that did not end as foreseen leaves it to read.
		// What a correction starts from, when this merge conflicts: the merges before it, and this one again.
		conflict := func(files []string) *planv1.IntegrationFailure {
			return &planv1.IntegrationFailure{Base: base, MergeBranch: t.GetBranch(), Files: files, TaskIds: append(ids(merged), t.GetId())}
		}
		// In a batch of several, a branch that conflicts leaves it, its merge undone (repair.go).
		leave := len(batch) > 1
		msg := fmt.Sprintf("Merge branch '%s' into %s", t.GetBranch(), branch)
		if ctx.Err() != nil {
			return wait("djinn up stopped")
		}
		_, err = git(keep, wt, "merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, t.GetBranch())
		if err == nil {
			var isEmpty bool
			if isEmpty, head = emptyMerge(keep, wt, base); isEmpty {
				h.integrationStep(ctx, t, "integration: "+t.GetBranch()+" changes nothing in "+branch+": no merge commit")
				empty = true
				continue
			}
			h.integrationStep(ctx, t, "integration: merged "+t.GetBranch())
			merged = append(merged, t)
			continue
		}
		conflicts := conflicted(keep, wt)
		var why string
		switch {
		case errors.Is(err, errNoIdentity):
			// Not a conflict: the person sets the identity, and the next pass merges.
			_, _ = git(keep, wt, "merge", "--abort")
			return wait(noIdentity)
		case len(conflicts) == 0:
			_, _ = git(keep, wt, "merge", "--abort")
			why := fmt.Sprintf("merge %s: %v", t.GetCode(), err)
			if leave {
				left = append(left, leftOut{task: t, why: why})
				continue
			}
			return fail(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, why, nil)
		case !generatedOnly(conflicts, prefix, settings.Generated) || settings.Generate == "":
			why = fmt.Sprintf("%s conflicts with %s in %s", t.GetCode(), branch, strings.Join(conflicts, ", "))
		default:
			why = h.settleGenerated(ctx, t.GetCode(), t.GetId(), wt, dir, settings.Generate, conflicts, func() string {
				failed, _, _, _ := h.setUp(ctx, project, settings, wish.GetId(), t.GetId(), wt, dir)
				return failed
			})
		}
		if why != "" {
			if _, err := git(keep, wt, "merge", "--abort"); err != nil && leave {
				// The merge settling generated files may have committed: the batch goes back to the merges before it.
				_, _ = git(keep, wt, "reset", "--quiet", "--hard", base)
			}
			if leave {
				left = append(left, leftOut{task: t, why: why})
				continue
			}
			return fail(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, why, conflict(conflicts))
		}
		var isEmpty bool
		if isEmpty, head = emptyMerge(keep, wt, base); isEmpty {
			h.integrationStep(ctx, t, "integration: "+t.GetBranch()+" changes nothing in "+branch+": no merge commit")
			empty = true
			continue
		}
		h.integrationStep(ctx, t, fmt.Sprintf("integration: merged %s; its conflict, only in generated files (%s), settled by %s",
			t.GetBranch(), strings.Join(conflicts, ", "), settings.Generate))
		merged = append(merged, t)
	}
	if len(merged) == 0 {
		if empty {
			return old, nil, "", left // Nothing merged moves the branch: it holds the work already.
		}
		return "", nil, "", left
	}
	if sha = head; sha == "" {
		if sha, err = git(ctx, wt, "rev-parse", "HEAD"); err != nil {
			return wait(err.Error())
		}
	}
	red := func(command, why, out string, run *planv1.CheckRun) (string, *planv1.TaskIntegration, string, []leftOut) {
		f := keptOutput(&planv1.IntegrationFailure{Base: sha, Command: command, Output: tail(out), TaskIds: ids(merged)}, run)
		if step := outputStep(f, run); step != "" {
			for _, t := range merged {
				h.integrationStep(ctx, t, step)
			}
		}
		return fail(planv1.IntegrationState_INTEGRATION_STATE_RED, why, f)
	}
	if h.netWaits(commitKey(wish, project), codes(merged)+"@"+old) {
		return wait("its commit checks failed on the network; Djinn runs them again once it is back")
	}
	command, why, out, run, stopped := h.commitChecks(ctx, wish, project, settings, merged, sha, wt, dir, func(c *planv1.ProjectCheck) {
		// The window shows the check that runs, and since when (TaskIntegration.check).
		h.settleIntegration(ctx, merged, &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: branch, Check: c.GetName(),
		}, fmt.Sprintf("integration: checking %s: %s (%s)", codes(merged), c.GetName(), c.GetCommand()), nil)
		h.setDoing(fmt.Sprintf("the integration of %s into %s, checking %s", codes(merged), branch, c.GetName()))
	})
	switch {
	case stopped != "":
		return wait(stopped)
	case why != "" && h.netCheckFailed(commitKey(wish, project), codes(merged)+"@"+old, why+"\n"+out):
		// Not the code's failure: run again once the network is back, never red nor corrected (network.go).
		return wait("its commit checks failed on the network (" + headline(why) + "); Djinn runs them again once it is back")
	case why != "":
		return red(command, why, out, run)
	}
	return sha, nil, "", left
}

// commitKey names the commit checks of wish's integration branch in project, for netCheckFailed.
func commitKey(wish *planv1.Wish, project *planv1.Project) string {
	return "commit/" + wish.GetId() + "/" + project.GetId()
}

// commitChecks makes the integration worktree wt ready with the setup, then runs the project's commit checks on sha
// there, in dir, for the tasks of batch (none: for no task), each through its gate, saying each with step before it
// runs. A check a task's worker passed on sha's tree is not run again, said in that task's events (passedOnTree). It
// returns the command that failed, why, its output, and its run when it ran red; or why it stopped, when djinn up
// stopped.
func (h *Harness) commitChecks(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, batch []*planv1.Task,
	sha, wt, dir string, step func(*planv1.ProjectCheck),
) (command, why, out string, red *planv1.CheckRun, stopped string) {
	checks := settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_COMMIT)
	taskID := ""
	if len(batch) > 0 {
		taskID = batch[0].GetId()
		checks = h.passedOnTree(ctx, project.GetId(), checks, batch, wt, sha)
	}
	if len(checks) == 0 {
		return "", "", "", nil, ""
	}
	if why, out, red, halted := h.setUp(ctx, project, settings, wish.GetId(), taskID, wt, dir); halted {
		return "", "", "", nil, "djinn up stopped during the setup"
	} else if why != "" {
		return settings.Setup, setupName + ": " + why, out, red, ""
	}
	for _, c := range checks {
		step(c)
		run := &planv1.CheckRun{
			Name: c.GetName(), Command: c.GetCommand(), When: planv1.CheckWhen_CHECK_WHEN_COMMIT, WishId: wish.GetId(),
			TaskId: taskID, Sha: sha,
		}
		if why, out, halted := h.check(ctx, project.GetId(), run, dir); halted {
			return "", "", "", nil, "djinn up stopped during the check " + c.GetName()
		} else if why != "" {
			return c.GetCommand(), why, out, run, ""
		}
	}
	return "", "", "", nil, ""
}

// settleGenerated settles a merge of what, a task's code or a branch, whose conflicts are all in generated files: it
// takes the side merged in, makes the worktree ready, makes them again with the command generate, in dir, under the
// gate gen for the task taskID ("" for none), and commits the merge in the worktree wt. It returns why it could not.
func (h *Harness) settleGenerated(
	ctx context.Context, what, taskID, wt, dir, generate string, conflicts []string, ready func() string,
) string {
	keep, done := gitx.Uncut(ctx) // A stop cuts the commands, not git's steps.
	defer done()
	if _, err := git(keep, wt, append([]string{"checkout", "--theirs", "--"}, conflicts...)...); err != nil {
		return err.Error()
	}
	if _, err := git(keep, wt, append([]string{"add", "--"}, conflicts...)...); err != nil {
		return err.Error()
	}
	if why := ready(); why != "" {
		return fmt.Sprintf("%s conflicts in generated files (%s), and the setup failed: %s", what, strings.Join(conflicts, ", "), why)
	}
	out, code, err := h.command(ctx, genGate, taskID, generate, dir)
	switch {
	case err != nil:
		return fmt.Sprintf("%s conflicts in generated files (%s), and %s could not run: %v", what, strings.Join(conflicts, ", "), generate, err)
	case code != 0:
		return fmt.Sprintf("%s conflicts in generated files (%s), and %s exited %d%s", what, strings.Join(conflicts, ", "), generate, code, tail(out))
	}
	if ctx.Err() != nil {
		return "djinn up stopped"
	}
	if _, err := git(keep, wt, "add", "--all"); err != nil {
		return err.Error()
	}
	if _, err := git(keep, wt, "commit", "--no-edit", "--quiet"); err != nil {
		return err.Error()
	}
	return ""
}

// integrationStep adds an event to the task t, saying a step of its integration.
func (h *Harness) integrationStep(ctx context.Context, t *planv1.Task, text string) {
	h.writeAlone(ctx, actorHarness, methodEvent, nil, t.GetId(), nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})
}

// settleIntegration records where the work of batch stands, with an event saying text on each task, and the commit
// when the branch moved, in one transaction. Each task is read again first: only its integration changes, whatever
// happened to it meanwhile. A task already where in says gets nothing new.
func (h *Harness) settleIntegration(
	ctx context.Context, batch []*planv1.Task, in *planv1.TaskIntegration, text string, commit *planv1.IntegrationCommit,
) {
	ctx = context.WithoutCancel(ctx)
	in.UpdateTime = timestamppb.New(h.now())
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if commit != nil {
			if err := tx.Journal(actorHarness, methodCommit, commit); err != nil {
				return err
			}
			if err := tx.Change(&planv1.ProjectSync{WishId: commit.GetWishId(), Branch: commit.GetBranch()}); err != nil {
				return err
			}
		}
		for _, b := range batch {
			t, err := store.Get[*planv1.Task](ctx, tx, b.GetId())
			if err != nil {
				return err
			}
			if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED &&
				in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
				continue
			}
			b.Integration = in
			next := in
			// Work that merges alone keeps merging alone while it waits or merges (repair.go).
			if t.GetIntegration().GetAlone() && !in.GetAlone() && (in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
				in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING) {
				next = proto.CloneOf(in)
				next.Alone = true
			}
			if sameIntegration(t.GetIntegration(), next) {
				continue
			}
			t.Integration = proto.CloneOf(next)
			if err := tx.Journal(actorHarness, methodIntegrate, t); err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			if err := putEvents(ctx, tx, newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text})); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logx.L().Error("record the integration", "loop", "integrate", "task", codes(batch), "err", err)
	}
	h.notify()
	h.queueMoved()
	for _, b := range batch {
		if len(b.GetComments()) > 0 {
			if _, err := h.checkCommentsResolution(ctx, b); err != nil {
				logx.L().Warn("check the comments resolved", "loop", "integrate", "task", b.GetCode(), "err", err)
			}
		}
	}
}

// sameIntegration tells whether a and b say the same, whenever they said it.
func sameIntegration(a, b *planv1.TaskIntegration) bool {
	return a.GetState() == b.GetState() && a.GetBranch() == b.GetBranch() && a.GetSha() == b.GetSha() && a.GetReason() == b.GetReason() &&
		a.GetCorrectedBy() == b.GetCorrectedBy() && a.GetReviewedBy() == b.GetReviewedBy() && a.GetAttempts() == b.GetAttempts() &&
		a.GetQuestionId() == b.GetQuestionId() && a.GetAlone() == b.GetAlone() && a.GetCheck() == b.GetCheck() &&
		proto.Equal(a.GetFailure(), b.GetFailure())
}

// ids lists the tasks' identifiers.
func ids(batch []*planv1.Task) []string {
	out := make([]string, len(batch))
	for i, t := range batch {
		out[i] = t.GetId()
	}
	return out
}

// codes lists the tasks' codes: "W2, W3".
func codes(batch []*planv1.Task) string {
	c := make([]string, len(batch))
	for i, t := range batch {
		c[i] = t.GetCode()
	}
	return strings.Join(c, ", ")
}

// short8 is a commit's first eight characters.
func short8(sha string) string { return sha[:min(8, len(sha))] }

// integrationDir is the worktree a wish integrates its work in, for one project: in Djinn's data folder, next to the
// tasks' worktrees.
func integrationDir(home, projectID, wishID string) string {
	return filepath.Join(home, "projects", projectID, "integration", wishID)
}

// integrationWorktree makes wt, a worktree of the repository holding repo, ready to merge into: detached at old,
// without changes. It returns the project's folder within it, and the project's folder in the repository ("app/"; ""
// at its root). Its caller holds the worktree alone (integrateMu, installMu): an index.lock there is a dead git's, taken
// away (unlockWorktree), or every reset there would fail (W52). Once begun, it ends though djinn up stops.
func integrationWorktree(ctx context.Context, repo, wt, old string) (string, string, error) {
	prefix, err := git(ctx, repo, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	ctx, done := gitx.Uncut(ctx)
	defer done()
	dir := filepath.Join(wt, filepath.FromSlash(prefix))
	if _, err := os.Stat(wt); err == nil {
		if top, err := git(ctx, wt, "rev-parse", "--show-toplevel"); err == nil && samePath(top, wt) {
			unlockWorktree(wt)
			_, _ = git(ctx, wt, "merge", "--abort")
			_, _ = git(ctx, wt, "rebase", "--abort")
			if _, err := git(ctx, wt, "reset", "--quiet", "--hard", old); err != nil {
				return "", "", err
			}
			_, err = git(ctx, wt, "clean", "-fdq")
			return dir, prefix, err
		}
	}
	// Not a worktree any more, if it ever was: it is Djinn's own folder, made again, and set up again.
	if err := os.RemoveAll(wt); err != nil {
		return "", "", err
	}
	if err := os.Remove(setupStamp(wt)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if _, err := git(ctx, repo, "worktree", "prune"); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(wt), 0o700); err != nil {
		return "", "", err
	}
	_, err = git(ctx, repo, "worktree", "add", "--quiet", "--detach", wt, old)
	return dir, prefix, err
}

// emptyMerge tells whether the merge just committed in the worktree wt changes nothing from base, the commit it was
// made on; it is then undone: no merge commit without a change is ever made (W196). head is the commit the worktree
// is then at, read with the trees in one git: base once undone, "" when unknown.
func emptyMerge(ctx context.Context, wt, base string) (empty bool, head string) {
	out, err := git(ctx, wt, "rev-parse", "HEAD", "HEAD^{tree}", base+"^{tree}")
	read := strings.Fields(out)
	if err != nil || len(read) != 3 {
		return false, ""
	}
	if read[1] != read[2] {
		return false, read[0]
	}
	if _, err := git(ctx, wt, "reset", "--quiet", "--hard", base); err != nil {
		return false, ""
	}
	return true, base
}

// samePath tells whether a and b are the same folder, symbolic links resolved.
func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && filepath.Clean(ra) == filepath.Clean(rb)
}

// conflicted lists the files a merge left in conflict in the worktree wt, as the repository names them.
func conflicted(ctx context.Context, wt string) []string {
	out, err := git(ctx, wt, "diff", "--name-only", "--diff-filter=U")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// generatedOnly tells whether every file of files, as the repository names them, is a generated one: in the project's
// folder prefix, matching one of its globs.
func generatedOnly(files []string, prefix string, globs []string) bool {
	for _, f := range files {
		rel, ok := strings.CutPrefix(f, prefix)
		if !ok || !slices.ContainsFunc(globs, func(g string) bool { return matchGlob(g, rel) }) {
			return false
		}
	}
	return len(files) > 0
}

// matchGlob tells whether name, a path with slashes, matches pattern: "**" stands for any number of folders, other
// parts are path.Match patterns of one name.
func matchGlob(pattern, name string) bool {
	return matchParts(strings.Split(path.Clean(pattern), "/"), strings.Split(name, "/"))
}

func matchParts(pattern, name []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchParts(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], name[0]); !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// checkoutOf is the folder of the checkout that has branch checked out, "" when none has: the person's, or another
// worktree.
func checkoutOf(ctx context.Context, repo, branch string) (string, error) {
	out, err := git(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	dir := ""
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			dir = p
		}
		if line == "branch refs/heads/"+branch {
			return filepath.FromSlash(dir), nil
		}
	}
	return "", nil
}

// command runs the command line, its words split on spaces, in dir, under the gate name taken for the task taskID.
func (h *Harness) command(ctx context.Context, name, taskID, line, dir string) (string, int, error) {
	args := strings.Fields(line)
	if len(args) == 0 {
		return "", -1, errors.New("no command")
	}
	if h.gates != nil {
		waits, _ := ctx.Value(gateWaitKey{}).(func(why string))
		var waiting func(string)
		if waits != nil {
			waiting = func(why string) { waits("gate " + name + ": " + why) }
		}
		// Every command Djinn runs under a gate merges, settles a merge or checks a push, but an install's.
		_, installing := ctx.Value(installingKey{}).(bool)
		give, err := h.gates(ctx, name, taskID, line, dir, !installing, waiting)
		if err != nil {
			return "", -1, fmt.Errorf("gate %s: %w", name, err)
		}
		defer give()
		if waits != nil {
			waits("") // Taken: it runs.
		}
	}
	run, _ := ctx.Value(installRunKey{}).(installRun)
	if h.commands != nil {
		out, code, err := h.commands(ctx, dir, args)
		if run.output != nil {
			for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
				run.output(line)
			}
		}
		return out, code, err
	}
	low := false
	if run.low {
		prog, rest, started := lowCommand(args[0], args[1:])
		args, low = append([]string{prog}, rest...), !started // Not started at its level: set once started.
	}
	track := h.children.tracker("", name+" check")
	if h.scopes != nil {
		// In a systemd scope of its own, the Djinn line counts all the command uses, its test binaries that end
		// first too (W347); its tracker stops the whole scope.
		sc := h.scopes.Plain(name)
		args = append(slices.Clone(sc.Prefix), args...)
		if in := track; in != nil && sc.Cgroup != "" {
			track = func(pid int, _, mark string) func() { return in(pid, sc.Cgroup, mark) }
		}
	}
	return runTracked(ctx, dir, args, track, run.output, low)
}

// gateWaitKey is the context key of a function told why a command waits for its gate ("gate install: held by …"), and
// "" once it is taken: Install says it to the window.
type gateWaitKey struct{}

// installRunKey is the context key of how Install's commands run (InstallWith).
type installRunKey struct{}

// installingKey is the context key that marks Install's commands: their gates do not go first.
type installingKey struct{}

type installRun struct {
	output func(line string) // told each line a command writes; nil: none
	low    bool              // at a low-priority worker's CPU priority
}

// InstallWith returns ctx for Install: each line its commands write (the setup's, the install command's) told to
// output as it comes (nil: none), and, when low, those commands run at a low-priority worker's CPU priority: an
// install Djinn starts by itself.
func InstallWith(ctx context.Context, output func(line string), low bool) context.Context {
	return context.WithValue(ctx, installRunKey{}, installRun{output: output, low: low})
}

// runCommand runs args in dir, and returns the end of what it wrote and its exit code.
func runCommand(ctx context.Context, dir string, args []string) (string, int, error) {
	return runTracked(ctx, dir, args, nil, nil, false)
}

// runTracked is runCommand; track, when set, is told of the command as it starts, and what it returns is called once
// it ended; lines, when set, each line the command writes, as it writes it. low sets a low-priority worker's CPU
// priority on the command once started.
func runTracked(
	ctx context.Context, dir string, args []string, track func(pid int, cgroup, mark string) func(), lines func(string), low bool,
) (string, int, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	// A djinn command it runs (djinn gate run in a check) is Djinn's, not the developer's: it keeps no ifrit lit. Its
	// Go builds are trimmed (goenv.go).
	cmd.Env = append(trimmedGoFlags(os.Environ()), "DJINN_CALLER="+CallerDjinn)
	// At ctx's end (Djinn stops), what the command started dies with it: a check's test runner, left alone, would
	// hold the output open, and Djinn's shutdown with it, for as long as the tests run (W504).
	ownGroup(cmd, false)
	cmd.Cancel = func() error {
		kill(&process{cmd: cmd})
		return nil
	}
	cmd.WaitDelay = gitx.WaitDelay
	out := &tailBuffer{max: 16 << 10}
	var w io.Writer = out
	if lines != nil {
		lw := &lineWriter{line: lines}
		defer lw.flush()
		w = io.MultiWriter(out, lw)
	}
	cmd.Stdout, cmd.Stderr = w, w
	start := time.Now()
	err := cmd.Start()
	if err == nil {
		if low {
			applyPriority(cmd, true)
		}
		if track != nil {
			defer track(cmd.Process.Pid, "", "")()
		}
		err = cmd.Wait()
	}
	logx.Ran(ctx, cmd, start, err)
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return out.String(), exit.ExitCode(), nil
	case err != nil:
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}

// lineWriter tells line each line written to it, without its end of line, once the line is whole.
type lineWriter struct {
	line func(string)
	rest []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.rest = append(w.rest, p...)
	for {
		i := bytes.IndexByte(w.rest, '\n')
		if i < 0 {
			break
		}
		w.line(strings.TrimRight(string(w.rest[:i]), "\r"))
		w.rest = w.rest[i+1:]
	}
	if len(w.rest) > 4<<10 { // A line that never ends is told in pieces.
		w.flush()
	}
	return len(p), nil
}

// flush tells what is left of a line not ended.
func (w *lineWriter) flush() {
	if len(w.rest) > 0 {
		w.line(string(w.rest))
		w.rest = nil
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	bytes.Buffer
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n, _ := b.Buffer.Write(p)
	if over := b.Len() - b.max; over > 0 {
		b.Next(over)
	}
	return n, nil
}

// tail is the last lines of a command's output, for a reason: ": " and them, or "" for none.
func tail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	lines = lines[max(0, len(lines)-10):]
	s := strings.TrimSpace(strings.Join(lines, "\n"))
	if s == "" {
		return ""
	}
	return ":\n" + s[max(0, len(s)-2000):]
}

// tellLead hands the lead of wishID an event of kind about code, in line (toLead).
// What it tells already happened: djinn up stopping does not cancel the telling, or the lead never learns it.
func (h *Harness) tellLead(ctx context.Context, kind planv1.LeadEventKind, wishID, code, line string) {
	h.tellLeadDetail(ctx, kind, wishID, code, line, "")
}

// tellLeadDetail is tellLead with what the lead reads whole under the line (LeadEvent.detail).
func (h *Harness) tellLeadDetail(ctx context.Context, kind planv1.LeadEventKind, wishID, code, line, detail string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	e := &planv1.LeadEvent{Kind: kind, WishId: wishID, Code: code, Text: line, Detail: detail}
	if err := h.toLeadEvent(ctx, e); err != nil && !errors.Is(err, plan.ErrNoLead) {
		logx.L().Error("tell the lead", "wish", wishID, "task", code, "kind", kind.String(), "err", err)
	}
}

// askMove records q as a question for a move only the developer can make, journaled and notified.
func (h *Harness) askMove(ctx context.Context, q *planv1.Question) error {
	ctx = context.WithoutCancel(ctx)
	q.Id = store.NewID()
	q.CreateTime = timestamppb.New(h.now())
	q.Move = true
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodIntegrate, q); err != nil {
			return err
		}
		return plan.Ask(ctx, tx, q)
	})
	if err != nil {
		logx.L().Error("ask a move", "loop", "move", "wish", q.GetWishId(), "err", err)
		return err
	}
	h.notify()
	return nil
}

// isWishSettled tells whether every task in wishID is settled green with no task running or integrating.
func (h *Harness) isWishSettled(ctx context.Context, wishID string) bool {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil || len(tasks) == 0 {
		return false
	}
	for _, t := range tasks {
		if h.running(t.GetId()) {
			return false
		}
		st := t.GetIntegration().GetState()
		if st == planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
			st == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING ||
			st == planv1.IntegrationState_INTEGRATION_STATE_RED ||
			st == planv1.IntegrationState_INTEGRATION_STATE_CONFLICT {
			return false
		}
	}
	return true
}

// draftPR drafts a title and markdown body for a pull request from branch to ref in project.
func (h *Harness) draftPR(ctx context.Context, wish *planv1.Wish, project *planv1.Project, ref, branch string) (string, string) {
	repo := project.GetDirectory()
	title := wish.GetTitle()
	if title == "" {
		title = "Merge " + branch
	}
	var body strings.Builder
	if title != "" {
		body.WriteString("## Summary\n\n")
		body.WriteString(title)
		body.WriteString("\n\n")
	}
	logOut, err := git(ctx, repo, "log", "--no-merges", "--format=- %s", ref+"..refs/heads/"+branch)
	if err == nil && strings.TrimSpace(logOut) != "" {
		body.WriteString("### Commits\n\n")
		body.WriteString(strings.TrimSpace(logOut))
		body.WriteString("\n\n")
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err == nil {
		var azimas []string
		for _, t := range plan.WithAzimas(tasks) {
			if plan.IsAzima(t) {
				azimas = append(azimas, fmt.Sprintf("- **%s**: %s", t.GetCode(), t.GetTitle()))
			}
		}
		if len(azimas) > 0 {
			body.WriteString("### Azimas\n\n")
			body.WriteString(strings.Join(azimas, "\n"))
			body.WriteString("\n")
		}
	}
	return title, strings.TrimSpace(body.String())
}

// handleMergeMoves inspects the commits merged from oldSha to sha: local tags created,
// release workflow changes, needs: box, or an integration branch worth a pull request.
// It raises a question for any move not already asked, and returns the names of moves asked.
func (h *Harness) handleMergeMoves(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, oldSha, sha string,
) []string {
	if sha == "" {
		return nil
	}
	repo := project.GetDirectory()
	existing, _ := store.List[*planv1.Question](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	hasQuestion := func(match func(q *planv1.Question) bool) bool {
		for _, q := range existing {
			if q.GetMove() && match(q) {
				return true
			}
		}
		return false
	}

	var moves []string

	// 1. Tag created locally
	var tags []string
	if oldSha != "" && oldSha != sha {
		if out, err := git(ctx, repo, "tag", "--merged", sha, "--no-merged", oldSha); err == nil && strings.TrimSpace(out) != "" {
			for _, t := range strings.Fields(out) {
				if t != "" && !slices.Contains(tags, t) {
					tags = append(tags, t)
				}
			}
		}
	}
	var remTags map[string]string
	var remote string
	if len(tags) > 0 {
		remote, _ = pushTarget(ctx, repo, branch)
		if remote == "" {
			remote = "origin"
		}
		remTags, _ = remoteTags(ctx, repo, remote)
	}
	for _, tag := range tags {
		localSha, err := git(ctx, repo, "rev-parse", "refs/tags/"+tag+"^{commit}")
		if err != nil {
			localSha, _ = git(ctx, repo, "rev-parse", "refs/tags/"+tag)
		}
		localSha = strings.TrimSpace(localSha)
		if remTags != nil {
			if remoteSha, ok := remTags[tag]; ok && remoteSha == localSha {
				continue
			}
		}
		moveName := tagMoveName(tag)
		if !slices.Contains(moves, moveName) {
			asked := hasQuestion(func(q *planv1.Question) bool {
				return strings.Contains(q.GetText(), tag)
			})
			if !asked {
				q := tagMoveQuestion(wish.GetId(), project.GetId(), tag, remote)
				if err := h.askMove(ctx, q); err == nil {
					existing = append(existing, q)
					moves = append(moves, moveName)
				}
			}
		}
	}

	// 2. Release files changed
	var files []string
	if oldSha != "" && oldSha != sha {
		if out, err := git(ctx, repo, "diff", "--name-only", oldSha, sha); err == nil {
			files = strings.Fields(out)
		}
	} else {
		if out, err := git(ctx, repo, "show", "--name-only", "--format=", sha); err == nil {
			files = strings.Fields(out)
		}
	}
	hasRelease := false
	for _, f := range files {
		if isReleaseFile(f) {
			hasRelease = true
			break
		}
	}
	if hasRelease {
		if !slices.Contains(moves, releaseMoveName) {
			asked := hasQuestion(func(q *planv1.Question) bool {
				return strings.Contains(strings.ToLower(q.GetText()), "release")
			})
			if !asked {
				q := releaseMoveQuestion(wish.GetId(), sha)
				if err := h.askMove(ctx, q); err == nil {
					existing = append(existing, q)
					moves = append(moves, releaseMoveName)
				}
			}
		}
	}

	// 3. needs: box added
	if slices.ContainsFunc(files, isPlanFile) {
		var diffOut string
		if oldSha != "" && oldSha != sha {
			diffOut, _ = git(ctx, repo, "diff", "-U0", oldSha, sha, "--", plan.PlanDir+"/*.md")
		} else {
			diffOut, _ = git(ctx, repo, "show", "-U0", "--format=", sha, "--", plan.PlanDir+"/*.md")
		}
		prefix, _ := git(ctx, repo, "rev-parse", "--show-prefix")
		prefix = strings.TrimSpace(prefix)
		curFile := ""
		for _, line := range strings.Split(diffOut, "\n") {
			if strings.HasPrefix(line, "+++ ") {
				curFile = strings.TrimPrefix(line, "+++ ")
				curFile = strings.TrimPrefix(curFile, "b/")
				if prefix != "" {
					curFile = strings.TrimPrefix(curFile, prefix)
				}
				continue
			}
			if !isPlanFile(curFile) {
				continue
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				added := strings.TrimPrefix(line, "+")
				box, machine, ok := plan.ReadTaskItemNeeds(added)
				if ok {
					moveName := needsMoveName(box, machine)
					if !slices.Contains(moves, moveName) {
						asked := hasQuestion(func(q *planv1.Question) bool {
							tl := strings.ToLower(q.GetText())
							return strings.Contains(tl, strings.ToLower(machine)) && (box == "" || strings.Contains(tl, strings.ToLower(box)))
						})
						if !asked {
							q := needsMoveQuestion(wish.GetId(), box, machine)
							if err := h.askMove(ctx, q); err == nil {
								existing = append(existing, q)
								moves = append(moves, moveName)
							}
						}
					}
				}
			}
		}
	}

	// 4. Worth a pull request to main
	main, ref, ok := h.isPRWorthy(ctx, wish, project, branch, sha)
	if ok {
		if prNum, hasPR := openPRFromBranch(ctx, repo, branch); hasPR {
			if prNum != "" {
				moves = append(moves, prGrowsMoveName(prNum))
			}
			for _, q := range existing {
				if q.GetMove() && q.GetAnswer() == nil && strings.Contains(strings.ToLower(q.GetText()), "pull request") {
					note := "PR open"
					if prNum != "" {
						note = fmt.Sprintf("PR #%s open", prNum)
					}
					_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, note)
				}
			}
		} else {
			allQuestions, _ := store.List[*planv1.Question](ctx, h.store, nil)
			wishes, _ := store.List[*planv1.Wish](ctx, h.store, nil)
			wishesByID := make(map[string]*planv1.Wish, len(wishes))
			for _, w := range wishes {
				wishesByID[w.GetId()] = w
			}
			if !h.hasPRQuestion(ctx, project.GetId(), allQuestions, wishesByID) {
				moveName := prMoveName(main)
				if !slices.Contains(moves, moveName) {
					title, body := h.draftPR(ctx, wish, project, ref, branch)
					q := prMoveQuestion(wish.GetId(), project.GetId(), main, branch, title, body)
					if err := h.askMove(ctx, q); err == nil {
						existing = append(existing, q)
						moves = append(moves, moveName)
					}
				}
			}
		}
	}

	return moves
}

// computedMovesPass checks for moves Djinn computes by itself across active wishes and projects:
// uninstalled builds, PRs to main, azimas awaiting proof, and release workflows.
func (h *Harness) computedMovesPass(ctx context.Context) {
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		return
	}
	allQuestions, err := store.List[*planv1.Question](ctx, h.store, nil)
	if err != nil {
		return
	}
	wishesByID := make(map[string]*planv1.Wish, len(wishes))
	for _, w := range wishes {
		wishesByID[w.GetId()] = w
	}
	for _, wish := range wishes {
		if ctx.Err() != nil {
			return
		}
		if s := wish.GetState(); s == planv1.WishState_WISH_STATE_PAUSED || s == planv1.WishState_WISH_STATE_GRANTED {
			continue
		}

		// 1. Azima awaiting proof
		tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
		if err == nil {
			for _, t := range plan.WithAzimas(tasks) {
				if plan.IsAzima(t) {
					if t.GetAzima().GetState() == planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF {
						asked := false
						for _, q := range allQuestions {
							if q.GetMove() && q.GetTaskId() == t.GetId() {
								asked = true
								break
							}
						}
						if !asked {
							needs := t.GetProofNeeds()
							if len(needs) == 0 {
								continue
							}
							needsWords := plan.NeedsWords(needs)
							var boxes []string
							for _, n := range needs {
								boxes = append(boxes, fmt.Sprintf("- [ ] %s (needs: %s)", n.GetBox(), n.GetNeeds()))
							}
							q := azimaProofMoveQuestion(wish.GetId(), t.GetId(), t.GetCode(), needsWords, boxes)
							if err := h.askMove(ctx, q); err == nil {
								allQuestions = append(allQuestions, q)
							}
						}
					} else {
						for _, q := range allQuestions {
							if q.GetMove() && q.GetTaskId() == t.GetId() && q.GetAnswer() == nil {
								_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "proof settled")
							}
						}
					}
				}
			}
		}

		// 2. Projects checks (uninstalled build and PR to main)
		for _, projectID := range wish.GetProjectIds() {
			project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
			if err != nil {
				continue
			}
			repo := project.GetDirectory()
			branch, _ := h.integrationBranch(ctx, wish, project)
			if branch == "" {
				continue
			}
			settings, err := plan.LoadSettings(h.home, project)
			if err != nil {
				continue
			}

			// (a) Uninstalled build
			if settings.Install != "" {
				sha, _ := git(ctx, repo, "rev-parse", "refs/heads/"+branch)
				inst := h.installedSha(ctx, projectID)
				if sha != "" && inst == sha {
					h.closeInstallQuestions(ctx, projectID, sha)
				} else if sha != "" && inst != sha {
					s := short8(sha)
					// Supersede open install questions for older shas in this project
					for _, q := range allQuestions {
						if q.GetMove() && q.GetAnswer() == nil && strings.Contains(q.GetText(), "Install and restart") && !strings.Contains(q.GetText(), s) && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
							_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_B, fmt.Sprintf("superseded by build %s", s))
						}
					}
					tsStr, err := git(ctx, repo, "log", "-1", "--format=%ct", sha)
					if err == nil {
						ts, _ := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
						commitTime := time.Unix(ts, 0)
						delay := cmp.Or(h.uninstalledDelay, defaultUninstalledDelay)
						if h.now().Sub(commitTime) >= delay {
							iq := settings.InstallQuestions
							if iq == planv1.InstallQuestions_INSTALL_QUESTIONS_UNSPECIFIED {
								iq = plan.DefaultInstallQuestions
							}
							switch iq {
							case planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER:
								// Never ask install or restart questions.
							case planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED:
								reason := h.detectInstallReason(ctx, wish, project, branch, inst, sha)
								if reason != nil {
									asked := false
									for _, q := range allQuestions {
										if q.GetMove() && q.GetAnswer() == nil && strings.Contains(q.GetText(), "Install and restart") && strings.Contains(q.GetText(), reason.whyText) && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
											asked = true
											break
										}
									}
									if !asked {
										q := installMoveQuestionWithReason(wish.GetId(), projectID, sha, branch, reason)
										if err := h.askMove(ctx, q); err == nil {
											allQuestions = append(allQuestions, q)
										}
									}
								}
							case planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS:
								asked := false
								for _, q := range allQuestions {
									if q.GetMove() && strings.Contains(q.GetText(), "Install and restart") && strings.Contains(q.GetText(), s) && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
										asked = true
										break
									}
								}
								if !asked {
									q := installMoveQuestion(wish.GetId(), projectID, sha, branch, delay)
									if err := h.askMove(ctx, q); err == nil {
										allQuestions = append(allQuestions, q)
									}
								}
							}
						}
					}
				}
			}

			// (b) PR to main: wish settled, pushed to remote, push checks green, ahead of remote default branch
			if h.netHolds() {
				continue // The forge cannot say whether one is open: none would look missing (network.go).
			}
			strat, _ := plan.ResolveWishPushStrategy(wish, settings)
			if strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
				allQuestions = h.azimaPRPass(ctx, wish, project, settings, allQuestions)
				continue
			}

			main, ref, ok := h.isPRWorthy(ctx, wish, project, branch, "")
			if ok {
				if prNum, hasPR := openPRFromBranch(ctx, repo, branch); !hasPR {
					if !h.hasPRQuestion(ctx, projectID, allQuestions, wishesByID) {
						title, body := h.draftPR(ctx, wish, project, ref, branch)
						q := prMoveQuestion(wish.GetId(), project.GetId(), main, branch, title, body)
						if err := h.askMove(ctx, q); err == nil {
							allQuestions = append(allQuestions, q)
						}
					}
				} else {
					for _, q := range allQuestions {
						if q.GetMove() && q.GetAnswer() == nil && strings.Contains(strings.ToLower(q.GetText()), "pull request") && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
							note := "PR open"
							if prNum != "" {
								note = fmt.Sprintf("PR #%s open", prNum)
							}
							_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, note)
						}
					}
				}
			} else {
				// If branch caught up to main (count == 0), close any open PR questions
				main, ref, err := mainRef(ctx, repo, settings.MainBranch)
				if err == nil && !strings.EqualFold(main, branch) {
					countStr, err := git(ctx, repo, "rev-list", "--count", ref+"..refs/heads/"+branch)
					count, _ := strconv.Atoi(strings.TrimSpace(countStr))
					if err == nil && count == 0 {
						for _, q := range allQuestions {
							if q.GetMove() && q.GetAnswer() == nil && strings.Contains(strings.ToLower(q.GetText()), "pull request") && h.isQuestionForProject(ctx, q, projectID, wishesByID) {
								_ = h.closeMove(ctx, q, planv1.Choice_CHOICE_A, "merged to "+main)
							}
						}
					}
				}
			}
		}
	}
}

// targetBranch determines the branch to merge the batch into:
// When the push strategy is azima, each azima gets its own integration branch (djinn/<azima code>-<slug of title>),
// cut from main if it does not exist yet (azimaBranch). A task with no azima goes to the wish's own integration branch.
// In per-wish mode, it returns the wish's integration branch as today. Work held out of its azima's branch, the azima
// done or its branch deleted, gets that branch and why it waits (azimaBranchFor).
func (h *Harness) targetBranch(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, batch []*planv1.Task,
) (branch, why string, ok bool) {
	strat, _ := plan.ResolveWishPushStrategy(wish, settings)
	if strat != planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		b, why := h.integrationBranch(ctx, wish, project)
		return b, why, true
	}
	azima, err := h.taskAzima(ctx, batch[0])
	if err != nil {
		return "", "find azima: " + err.Error(), false
	}
	if azima == nil {
		b, why := h.integrationBranch(ctx, wish, project)
		return b, why, true
	}
	if branch, err = h.azimaBranchFor(ctx, wish, project, settings, azima); err != nil {
		if held := (*heldOutError)(nil); errors.As(err, &held) {
			return held.branch, held.why, true
		}
		return "", err.Error(), false
	}
	return branch, "", true
}

// pushRepairAzima is the azima a correction of the red push checks of branch, in wish, is part of: the one whose branch
// it is, when the wish pushes per azima, so that targetBranch merges the correction's work into the branch it repairs,
// its worktree's base, never into the wish's own branch (W32: W28 and W29, repairs of djinn/T1-…, merged into main).
// "" for the wish's own branch.
func (h *Harness) pushRepairAzima(ctx context.Context, wish *planv1.Wish, branch string) string {
	if branch == "" || !h.pushesPerAzima(ctx, h.store, wish) {
		return ""
	}
	return azimaOwning(h.wishAzimas(ctx, wish), branch).GetId()
}

// azimaBranchName is the integration branch name of an azima: djinn/<code-slug> or djinn/<code>.
func azimaBranchName(a *planv1.Task) string {
	return plan.AzimaBranchName(a)
}

// taskAzima finds the azima task that t is part of, walking up the part_of chain if needed.
func (h *Harness) taskAzima(ctx context.Context, t *planv1.Task) (*planv1.Task, error) {
	if t == nil {
		return nil, nil
	}
	if plan.IsAzima(t) {
		return t, nil
	}
	curr := t
	visited := make(map[string]bool)
	for curr.GetPartOf() != "" && !visited[curr.GetId()] {
		visited[curr.GetId()] = true
		parent, err := store.Get[*planv1.Task](ctx, h.store, curr.GetPartOf())
		if err != nil {
			return nil, err
		}
		if plan.IsAzima(parent) {
			return parent, nil
		}
		curr = parent
	}
	return nil, nil
}

// azimaTasks returns all non-azima tasks that belong to azimaID under wishID.
func (h *Harness) azimaTasks(ctx context.Context, wishID, azimaID string) ([]*planv1.Task, error) {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	taskMap := make(map[string]*planv1.Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.GetId()] = t
	}
	isUnder := func(t *planv1.Task) bool {
		curr := t
		visited := make(map[string]bool)
		for curr.GetPartOf() != "" && !visited[curr.GetId()] {
			visited[curr.GetId()] = true
			if curr.GetPartOf() == azimaID {
				return true
			}
			curr = taskMap[curr.GetPartOf()]
			if curr == nil {
				break
			}
		}
		return false
	}
	var out []*planv1.Task
	for _, t := range tasks {
		if !plan.IsAzima(t) && isUnder(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// wishAzimas returns all azima tasks in wish.
func (h *Harness) wishAzimas(ctx context.Context, wish *planv1.Wish) []*planv1.Task {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil
	}
	var azimas []*planv1.Task
	for _, t := range tasks {
		if plan.IsAzima(t) {
			azimas = append(azimas, t)
		}
	}
	return azimas
}

// filledAzimas are the azimas of wish, each with where it stands (plan.FillAzimas, read from all its tasks).
func (h *Harness) filledAzimas(ctx context.Context, wish *planv1.Wish) []*planv1.Task {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil
	}
	plan.FillAzimas(tasks)
	return slices.DeleteFunc(tasks, func(t *planv1.Task) bool { return !plan.IsAzima(t) })
}

// azimaBranchesOf lists all existing azima branches for wish in project's repo.
func (h *Harness) azimaBranchesOf(ctx context.Context, wish *planv1.Wish, project *planv1.Project) []string {
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil
	}
	repo := project.GetDirectory()
	var branches []string
	for _, t := range tasks {
		if plan.IsAzima(t) {
			b := azimaBranchName(t)
			if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
				branches = append(branches, b)
			}
		}
	}
	return branches
}

// resolveMainSha finds the project's reference branch, as mainRef names it from the main_branch setting, and its
// commit: the remote's as this repository last fetched it, when it holds the branch; else the local branch, in a
// repository without a remote or whose remote lacks it. A repository where mainRef finds none gives its local main or
// master. Never the developer's HEAD, nor the branch their copy has checked out: without a reference branch, an error
// says to name one (W227).
func resolveMainSha(ctx context.Context, repo string, settings plan.Settings) (name, sha string, err error) {
	mainName, _, err := mainRef(ctx, repo, settings.MainBranch)
	localSha := ""
	if err == nil {
		localSha = commitOf(ctx, repo, "refs/heads/"+mainName)
	} else {
		// A remote that holds neither its HEAD, nor main, nor master: Djinn's default names, in the copy.
		for _, name := range []string{"main", "master"} {
			if localSha = commitOf(ctx, repo, "refs/heads/"+name); localSha != "" {
				mainName = name
				break
			}
		}
	}
	var remoteSha string
	remote, target := pushTarget(ctx, repo, mainName)
	if remote != "" && target != "" {
		remoteSha = commitOf(ctx, repo, "refs/remotes/"+remote+"/"+target)
	}
	if sha := cmp.Or(remoteSha, localSha); sha != "" {
		return mainName, sha, nil
	}
	return "", "", errors.New("the project has no main branch: name it with djinn project set-branch, or main_branch in its settings")
}

// referenceFetchEvery is how often Djinn fetches a project's reference branch before a worker starts from it, or an
// azima's branch is cut from it: once a minute at most, the remote's branch as last fetched in between.
const referenceFetchEvery = time.Minute

// referenceFetchTimeout is how long that fetch may take: a remote that does not answer holds no worker longer, the
// branch then read as this repository last fetched it.
const referenceFetchTimeout = 15 * time.Second

// fetchReference fetches the reference branch of the repository holding repo, as mainRef names it from settings, from
// its remote, when it was not fetched within referenceFetchEvery: what a worker starts from, and an azima's branch is cut
// from, is the remote's branch, never what the copy last pulled. A failure is logged: the branch is then read as last
// fetched.
func (h *Harness) fetchReference(ctx context.Context, repo string, settings plan.Settings) {
	_, ref, err := mainRef(ctx, repo, settings.MainBranch)
	remoteBranch, ok := strings.CutPrefix(ref, "refs/remotes/")
	if err != nil || !ok {
		return
	}
	now := time.Now() // A pace of the network, not of the plan: the real clock.
	h.mu.Lock()
	due := now.Sub(h.referenceFetched[repo]) >= referenceFetchEvery
	if due {
		h.referenceFetched[repo] = now
	}
	h.mu.Unlock()
	if !due {
		return
	}
	remote, target, _ := strings.Cut(remoteBranch, "/")
	fetching, cancel := context.WithTimeout(ctx, referenceFetchTimeout)
	defer cancel()
	if _, err := gitx.FetchBranch(fetching, repo, remote, target); err != nil {
		logx.L().Warn("fetch the reference branch", "repo", repo, "ref", remoteBranch, "err", err)
	}
}

// workerStartBranch is the branch a worker starts from, the one its work lands in (startPoint reads it): per wish, the
// wish's integration branch; per azima, the branch of the task's azima, cut from the reference branch when its first
// part starts if it was not when the azima was made, and for work of no azima the wish's integration branch, the
// reference branch unless the developer named another. "" for a wish that records none: the reference branch then.
// Never the branch the developer's copy has checked out (W227).
func (h *Harness) workerStartBranch(ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, t *planv1.Task) string {
	strat, _ := plan.ResolveWishPushStrategy(wish, settings)
	if strat != planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		return plan.IntegrationBranchOf(wish, project.GetId())
	}
	azima, err := h.taskAzima(ctx, t)
	if err != nil || azima == nil {
		return plan.IntegrationBranchOf(wish, project.GetId())
	}
	branch, err := h.azimaBranch(ctx, project, settings, azima)
	if err != nil {
		logx.L().Warn("cut the azima branch", "task", t.GetCode(), "azima", azima.GetCode(), "project", project.GetId(), "err", err)
		return plan.IntegrationBranchOf(wish, project.GetId())
	}
	return branch
}

// azimaGoal extracts the goal sentence from an azima task.
func azimaGoal(azima *planv1.Task) string {
	desc := strings.TrimSpace(azima.GetDescription())
	if desc != "" {
		lines := strings.Split(desc, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.EqualFold(trimmed, "## Goal") || strings.EqualFold(trimmed, "# Goal") {
				var goalLines []string
				for j := i + 1; j < len(lines); j++ {
					l := strings.TrimSpace(lines[j])
					if strings.HasPrefix(l, "#") {
						break
					}
					if l == "" && len(goalLines) > 0 {
						break
					}
					if l != "" {
						goalLines = append(goalLines, l)
					}
				}
				if len(goalLines) > 0 {
					return strings.Join(goalLines, " ")
				}
			}
		}
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			for _, prefix := range []string{"**goal.**", "**goal:**", "goal:"} {
				if strings.HasPrefix(lower, prefix) {
					goal := strings.TrimSpace(trimmed[len(prefix):])
					if goal != "" {
						return goal
					}
				}
			}
		}
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				return trimmed
			}
		}
	}
	return azima.GetTitle()
}

// azimaPartsDoneAndGreen tells whether all parts under azimaID in wishID are done and green.
func (h *Harness) azimaPartsDoneAndGreen(ctx context.Context, wishID, azimaID string) (bool, []*planv1.Task) {
	tasks, err := h.azimaTasks(ctx, wishID, azimaID)
	if err != nil || len(tasks) == 0 {
		return false, nil
	}
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			return false, nil
		}
		in := t.GetIntegration()
		if in == nil || in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			return false, nil
		}
	}
	return true, tasks
}

// draftAzimaPR drafts title and description for an azima's PR from its goal and parts.
func (h *Harness) draftAzimaPR(ctx context.Context, azima *planv1.Task, parts []*planv1.Task) (string, string) {
	title := azimaGoal(azima)
	var b strings.Builder
	b.WriteString("## Goal\n\n")
	b.WriteString(title)
	b.WriteString("\n\n### Parts\n")
	sortedParts := slices.Clone(parts)
	slices.SortStableFunc(sortedParts, func(a, b *planv1.Task) int {
		return cmp.Compare(a.GetCode(), b.GetCode())
	})
	for _, p := range sortedParts {
		b.WriteString("\n- **")
		b.WriteString(p.GetCode())
		b.WriteString("**: ")
		b.WriteString(p.GetTitle())
		if d := p.GetDecision(); d != "" {
			b.WriteString(" (decision: ")
			b.WriteString(d)
			b.WriteString(")")
		}
	}
	return title, strings.TrimSpace(b.String())
}

// azimaPRBase is the branch an azima's pull request targets: main, the one recorded when its branch was cut, else the
// project's reference branch as Djinn names it (resolveMainSha); "" when there is none. Never another azima's branch,
// and never changed once the PR is open.
func azimaPRBase(ctx context.Context, repo string, settings plan.Settings, azima *planv1.Task) string {
	if base := azima.GetAzima().GetBaseBranch(); base != "" && azima.GetAzima().GetCutFrom() != "" {
		return base
	}
	if main, _, err := resolveMainSha(ctx, repo, settings); err == nil {
		return main
	}
	return settings.MainBranch
}

// detectForge detects whether the project remote uses GitLab ("glab") or GitHub ("gh").
func detectForge(ctx context.Context, repo string, project *planv1.Project) string {
	remote := ""
	if project != nil {
		remote = project.GetRemote()
	}
	if remote == "" && repo != "" {
		if out, err := git(ctx, repo, "remote", "get-url", "origin"); err == nil {
			remote = strings.TrimSpace(out)
		}
	}
	if strings.Contains(strings.ToLower(remote), "gitlab") {
		return "glab"
	}
	return "gh"
}

// forgePR holds information about a pull or merge request on the forge.
type forgePR struct {
	Number    int
	State     string // OPEN, MERGED, CLOSED
	Base      string
	URL       string
	Mergeable bool
	Green     bool
}

// runCmd runs args in dir, using h.commands if set (e.g. in tests) or runCommand otherwise.
func (h *Harness) runCmd(ctx context.Context, dir string, args []string) (string, int, error) {
	run := h.commands
	if run == nil {
		run = runCommand
	}
	out, code, err := run(ctx, dir, args)
	if (err != nil || code != 0) && len(args) > 0 && (args[0] == "gh" || args[0] == "glab") {
		text := out
		if err != nil {
			text += "\n" + err.Error()
		}
		h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, text, false) // "error connecting to api.github.com"
	}
	return out, code, err
}

// queryGhPR queries GitHub via gh for branch's pull request.
func (h *Harness) queryGhPR(ctx context.Context, repo, branch string) *forgePR {
	out, code, err := h.runCmd(ctx, repo, []string{"gh", "pr", "view", branch, "--json", "number,state,baseRefName,mergeable,url"})
	if err != nil || code != 0 {
		return nil
	}
	var view struct {
		Number      int    `json:"number"`
		State       string `json:"state"`
		BaseRefName string `json:"baseRefName"`
		Mergeable   string `json:"mergeable"`
		URL         string `json:"url"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &view); err != nil || view.Number == 0 {
		return nil
	}
	pr := &forgePR{
		Number:    view.Number,
		State:     strings.ToUpper(view.State),
		Base:      view.BaseRefName,
		URL:       view.URL,
		Mergeable: view.Mergeable == "MERGEABLE",
	}
	if pr.State == "MERGED" || pr.State == "CLOSED" {
		return pr
	}
	checksOut, code, err := h.runCmd(ctx, repo, []string{"gh", "pr", "checks", strconv.Itoa(view.Number), "--json", "name,bucket"})
	if err == nil && (code == 0 || checksOut != "") {
		var checks []struct {
			Name   string `json:"name"`
			Bucket string `json:"bucket"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(checksOut)), &checks); err == nil && len(checks) > 0 {
			allGreen := true
			for _, c := range checks {
				if c.Bucket != "pass" && c.Bucket != "skipping" {
					allGreen = false
					break
				}
			}
			pr.Green = allGreen
		}
	}
	return pr
}

// queryGlabMR queries GitLab via glab for branch's merge request.
func (h *Harness) queryGlabMR(ctx context.Context, repo, branch string) *forgePR {
	out, code, err := h.runCmd(ctx, repo, []string{"glab", "mr", "view", branch, "--output", "json"})
	if err != nil || code != 0 {
		return nil
	}
	var view struct {
		IID                 int    `json:"iid"`
		ID                  int    `json:"id"`
		State               string `json:"state"`
		TargetBranch        string `json:"target_branch"`
		WebURL              string `json:"web_url"`
		DetailedMergeStatus string `json:"detailed_merge_status"`
		HeadPipeline        struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		} `json:"head_pipeline"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &view); err != nil {
		return nil
	}
	num := view.IID
	if num == 0 {
		num = view.ID
	}
	if num == 0 {
		return nil
	}
	state := strings.ToUpper(view.State)
	if state == "OPENED" {
		state = "OPEN"
	}
	mergeable := view.DetailedMergeStatus == "mergeable"
	green := view.HeadPipeline.Status == "success"
	return &forgePR{
		Number:    num,
		State:     state,
		Base:      view.TargetBranch,
		URL:       view.WebURL,
		Mergeable: mergeable,
		Green:     green,
	}
}

// forgeTimeout is the longest the questions to a forge about one branch may take: a forge that does not answer holds
// the integration back no longer (W504).
const forgeTimeout = time.Minute

// queryForgePR queries the forge (gh or glab) for branch's pull or merge request.
func (h *Harness) queryForgePR(ctx context.Context, repo string, project *planv1.Project, branch string) *forgePR {
	ctx, cancel := context.WithTimeout(ctx, forgeTimeout)
	defer cancel()
	forge := detectForge(ctx, repo, project)
	if forge == "glab" {
		return h.queryGlabMR(ctx, repo, branch)
	}
	return h.queryGhPR(ctx, repo, branch)
}

// azimaPRPass manages the PR lifecycle for all azimas of wish in project under azima push strategy: proposing to open
// its PR to main, the ready-to-merge notification, and marking it merged; where it stands is recorded on the azima
// (Azima.pr) for the window. The PR follows its branch: each push of the branch updates it, and it targets main from
// the start, never re-targeted.
func (h *Harness) azimaPRPass(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings,
	allQuestions []*planv1.Question,
) []*planv1.Question {
	hasQuestion := func(match func(q *planv1.Question) bool) bool {
		for _, q := range allQuestions {
			if q.GetMove() && match(q) {
				return true
			}
		}
		return false
	}
	repo := project.GetDirectory()
	for _, azima := range h.wishAzimas(ctx, wish) {
		if ctx.Err() != nil {
			return allQuestions
		}
		branch := azimaBranchName(azima)
		if commitOf(ctx, repo, "refs/heads/"+branch) == "" {
			continue
		}
		if !h.isBranchPushed(ctx, wish, project, branch) {
			continue
		}
		ok, parts := h.azimaPartsDoneAndGreen(ctx, wish.GetId(), azima.GetId())
		if !ok {
			continue
		}
		base := azimaPRBase(ctx, repo, settings, azima)

		pr := h.queryForgePR(ctx, repo, project, branch)
		if pr == nil {
			if azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
				continue
			}
			proposed := hasQuestion(func(q *planv1.Question) bool {
				return q.GetTaskId() == azima.GetId() && strings.Contains(strings.ToLower(q.GetText()), "pull request")
			})
			if !proposed && base != "" { // no reference branch for it to aim at: a notice of the project says so
				title, desc := h.draftAzimaPR(ctx, azima, parts)
				q := &planv1.Question{
					WishId:    wish.GetId(),
					TaskId:    azima.GetId(),
					Text:      fmt.Sprintf("Open a pull request for %s to %s?", azima.GetCode(), base),
					Context:   fmt.Sprintf("**Title**: %s\n**Base**: %s\n\n%s", title, base, desc),
					Options:   []string{"Open pull request", "Not yet"},
					Move:      true,
					Before:    "before the merge",
					DjinnMove: &planv1.DjinnMove{Kind: planv1.MoveKind_MOVE_KIND_PULL_REQUEST, Ref: branch, ProjectId: project.GetId()},
				}
				if err := h.askMove(ctx, q); err == nil {
					allQuestions = append(allQuestions, q)
					proposed = true
				}
			}
			if proposed {
				h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{State: planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED})
			}
			continue
		}

		switch pr.State {
		case "OPEN":
			h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{Url: pr.URL, State: planv1.AzimaPrState_AZIMA_PR_STATE_OPEN})
			if pr.Mergeable && pr.Green && azima.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
				if !hasQuestion(func(q *planv1.Question) bool {
					return q.GetTaskId() == azima.GetId() && strings.Contains(strings.ToLower(q.GetText()), "ready to merge")
				}) {
					q := &planv1.Question{
						WishId:    wish.GetId(),
						TaskId:    azima.GetId(),
						Text:      fmt.Sprintf("Pull request #%d for %s is ready to merge?", pr.Number, azima.GetCode()),
						Context:   fmt.Sprintf("Pull request #%d for azima %s (%s) is mergeable and all checks are green.", pr.Number, azima.GetCode(), azima.GetTitle()),
						Options:   []string{"Ready to merge", "Not yet"},
						Move:      true,
						Before:    "before the merge",
						DjinnMove: &planv1.DjinnMove{Kind: planv1.MoveKind_MOVE_KIND_READY_TO_MERGE},
					}
					if err := h.askMove(ctx, q); err == nil {
						allQuestions = append(allQuestions, q)
					}
				}
			}
		case "MERGED":
			// Found merged once its parts are done: its work is in main (merged.go), unless work was added since.
			if azima.GetAzima().GetPr().GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_MERGED {
				h.markMerged(ctx, azima.GetId(), &planv1.AzimaMerged{
					Time: timestamppb.New(h.now()), RequestUrl: pr.URL, MainBranch: base,
				})
			}
			h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{Url: pr.URL, State: planv1.AzimaPrState_AZIMA_PR_STATE_MERGED})
			if azima.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
				_, err := h.Done(ctx, "/plan.v1.TaskService/Done", &planv1.TaskServiceDoneRequest{
					TaskId: azima.GetId(),
					By:     planv1.Closer_CLOSER_LEAD,
					Note:   fmt.Sprintf("merged in pull request #%d", pr.Number),
				})
				if err != nil {
					logx.L().Warn("close the merged azima", "azima", azima.GetCode(), "wish", azima.GetWishId(), "pr", pr.URL, "err", err)
				}
				azima.Status = planv1.TaskStatus_TASK_STATUS_DONE
			}
		}
	}
	return allQuestions
}

// recordAzimaPR records where the pull request of azima stands (Azima.pr), when it changed.
func (h *Harness) recordAzimaPR(ctx context.Context, azima *planv1.Task, pr *planv1.AzimaPr) {
	if proto.Equal(azima.GetAzima().GetPr(), pr) {
		return
	}
	h.editAzima(ctx, azima.GetId(), func(e *planv1.Azima) { e.Pr = pr })
}
