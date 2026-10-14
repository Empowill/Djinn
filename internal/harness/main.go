package harness

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Keeping up with main: a wish's integration branch, a branch of its own, falls behind the project's main branch as
// others merge there. Djinn merges main into it by itself, no model: on a cadence, at most hourly by default, and at
// once when the running Djinn finds a release, it fetches main; once main holds a release the branch lacks (or any
// commit, as the settings say), it merges main in the wish's integration worktree, as it merges a task's branch:
// generated files made again on a conflict only in them, the commit checks through their gates, the branch moved only
// if git finds it where the merge started, the person's clean checkout following. A conflict in code, or a red check,
// starts a correction worker, as for a task. Never a rebase: a merge costs no model, and keeps the history already
// pushed, which a rebase would rewrite and push only by force. Under the per-azima push strategy, each azima's branch
// keeps up with main the same way, its state recorded on the azima (azima_branch.go); one that holds nothing of its
// own beyond main just moves to main's commit.

// methodMain is how the journal records a merge of main: the request is the merge (planv1.MainMerge).
const methodMain = "harness/main"

// fetchTimeout is how long a fetch of main may take before Djinn gives up on it, until the next look.
const fetchTimeout = 2 * time.Minute

// mainTested is a merge of main's commit main, shown as the person names main, with release the newest release it
// brings, onto the integration branch at old, tested green as sha while the branch could not move: moving it later
// needs no new run, nor a new look at main.
type mainTested struct {
	old, main, sha, shown, release string
}

// LookAtMain makes the integration's next pass fetch main at once, whatever the cadence: a release was found.
func (h *Harness) LookAtMain() {
	h.mainNow.Store(true)
	h.kickIntegrate()
}

// mainPass keeps the integration branches of the active wishes up with their projects' main branches, where a look is
// due.
func (h *Harness) mainPass(ctx context.Context) {
	now := h.mainNow.Swap(false)
	wishes, err := store.List[*planv1.Wish](ctx, h.store, nil)
	if err != nil {
		logx.L().Error("read the wishes", "loop", "main", "err", err)
		return
	}
	for _, wish := range wishes {
		if s := wish.GetState(); s == planv1.WishState_WISH_STATE_PAUSED || s == planv1.WishState_WISH_STATE_GRANTED {
			continue
		}
		for _, projectID := range wish.GetProjectIds() {
			if ctx.Err() != nil {
				return
			}
			h.keepUp(ctx, wish, projectID, now)
		}
	}
}

// mainLine is a branch that keeps up with main in one project of a wish: the wish's integration branch, or under the
// per-azima push strategy an azima's branch (azima set), where the azima records how it stands (Azima.main).
type mainLine struct {
	wish    *planv1.Wish
	project *planv1.Project
	branch  string
	azima   *planv1.Task
}

// key is the line in h.mainTested.
func (l mainLine) key() string {
	k := l.wish.GetId() + "/" + l.project.GetId()
	if l.azima != nil {
		k += "/" + l.azima.GetId()
	}
	return k
}

// lineState is where the line l stands against main; nil before anything.
func (h *Harness) lineState(ctx context.Context, l mainLine) *planv1.WishMain {
	if l.azima == nil {
		return mainState(l.wish, l.project.GetId())
	}
	t, err := store.Get[*planv1.Task](ctx, h.store, l.azima.GetId())
	if err != nil {
		return nil
	}
	return t.GetAzima().GetMain()
}

// editLine changes, with edit, where the line l stands against main.
func (h *Harness) editLine(ctx context.Context, l mainLine, edit func(*planv1.WishMain)) {
	if l.azima == nil {
		h.editMain(ctx, l.wish.GetId(), l.project.GetId(), edit)
		return
	}
	h.editAzima(ctx, l.azima.GetId(), azimaMain(l.project.GetId(), edit))
}

// azimaMain is edit applied to where an azima's branch in the project projectID stands against main.
func azimaMain(projectID string, edit func(*planv1.WishMain)) func(*planv1.Azima) {
	return func(e *planv1.Azima) {
		if e.Main == nil {
			e.Main = &planv1.WishMain{ProjectId: projectID}
		}
		edit(e.Main)
	}
}

// keepUp merges the main branch of the project projectID into wish's integration branch there, and under the
// per-azima push strategy into the branches of its azimas not done, when a look is due (now: whatever the cadence)
// and main holds what the settings wait for.
func (h *Harness) keepUp(ctx context.Context, wish *planv1.Wish, projectID string, now bool) {
	project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err != nil || !project.GetGit() {
		return
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil || !settings.Integrates() || settings.MergeMain == planv1.MergeMain_MERGE_MAIN_OFF {
		return
	}
	var lines []mainLine
	if strat, _ := plan.ResolveWishPushStrategy(wish, settings); strat == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		for _, azima := range h.wishAzimas(ctx, wish) {
			branch := azimaBranchName(azima)
			// A draft takes no correction worker: it keeps up once opened.
			if azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || azima.GetDraft() ||
				commitOf(ctx, project.GetDirectory(), "refs/heads/"+branch) == "" {
				continue
			}
			lines = append(lines, mainLine{wish: wish, project: project, branch: branch, azima: azima})
		}
	}
	// The wish's integration branch first; an azima that adopted it keeps it up as its own.
	if branch := plan.IntegrationBranchOf(wish, projectID); branch != "" &&
		!slices.ContainsFunc(lines, func(l mainLine) bool { return l.branch == branch }) {
		lines = slices.Insert(lines, 0, mainLine{wish: wish, project: project, branch: branch})
	}
	fetched := map[string]error{} // main is fetched once for every line of a look.
	for _, l := range lines {
		if ctx.Err() != nil {
			return
		}
		h.keepLineUp(ctx, l, settings, now, fetched)
	}
}

// keepLineUp merges main into the branch of the line l when a look is due and main holds what the settings wait for;
// fetched holds main's fetches already made in this look, by ref.
func (h *Harness) keepLineUp(ctx context.Context, l mainLine, settings plan.Settings, now bool, fetched map[string]error) {
	branch := l.branch
	state := h.lineState(ctx, l)
	if id := state.GetCorrectedBy(); id != "" {
		if c, err := store.Get[*planv1.Task](ctx, h.store, id); err == nil && !h.correctionOver(c) {
			return // A correction worker settles main's merge: its work brings main in.
		}
		// It is over: Djinn looks at main again at once, an attempt in vain counted.
		h.editLine(ctx, l, func(m *planv1.WishMain) { m.CorrectedBy = "" })
		now = true
	}
	repo := l.project.GetDirectory()
	key := l.key()
	// Main merged into a branch whose remote holds commits it lacks could not be pushed (remote_branch.go).
	if why := apart(ctx, repo, settings, branch); why != "" {
		h.editLine(ctx, l, func(m *planv1.WishMain) { m.Held = why })
		h.tellApart(ctx, l.wish, why)
		return
	}
	if last := state.GetCheckTime(); !now && last != nil && h.now().Sub(last.AsTime()) < settings.MergeMainEvery {
		// Not time to look at main yet; a merge tested green moves the branch as soon as it can.
		if t, ok := h.mainTested[key]; ok {
			if old, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
				h.mergeMain(ctx, l, settings, t.shown, t.main, old, t.release, state.GetAttempts())
			}
		}
		return
	}
	looked := func(edit func(*planv1.WishMain)) {
		h.editLine(ctx, l, func(m *planv1.WishMain) {
			m.CheckTime = timestamppb.New(h.now())
			edit(m)
		})
	}
	held := func(why string) func(*planv1.WishMain) { return func(m *planv1.WishMain) { m.Held = why } }
	main, ref, err := mainRef(ctx, repo, settings.MainBranch)
	switch {
	case err == nil && strings.EqualFold(main, branch):
		looked(held("")) // The wish integrates into main itself.
		return
	case err == nil:
		var ok bool
		if err, ok = fetched[ref]; !ok {
			err = h.fetchMain(ctx, l.wish.GetId(), repo, ref)
			fetched[ref] = err
		}
	}
	if err != nil {
		looked(held(err.Error()))
		return
	}
	mainSha, err1 := git(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	old, err2 := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err := errors.Join(err1, err2); err != nil {
		looked(held("read " + shortRef(ref) + " and " + branch + ": " + err.Error()))
		return
	}
	if mainSha == state.GetFailedSha() {
		looked(func(*planv1.WishMain) {}) // Djinn gave up on that merge, as held says: it merges main again once main moves.
		return
	}
	if n, err := git(ctx, repo, "rev-list", "--count", old+".."+mainSha); err != nil || n == "0" {
		delete(h.mainTested, key)
		looked(func(m *planv1.WishMain) { m.Held, m.FailedSha = "", "" }) // The branch holds main already.
		return
	}
	release := newestRelease(ctx, repo, mainSha, old)
	if release == "" && settings.MergeMain == planv1.MergeMain_MERGE_MAIN_RELEASE {
		looked(held("")) // main holds no release the branch lacks: its commits wait for the next one.
		return
	}
	looked(func(*planv1.WishMain) {})
	h.mergeMain(ctx, l, settings, shortRef(ref), mainSha, old, release, state.GetAttempts())
}

// correctionOver tells whether c, a correction worker, is over: its work is committed, carries a question,
// or has no integration record; or it failed, stopped, or was cancelled.
func (h *Harness) correctionOver(c *planv1.Task) bool {
	if c == nil {
		return true
	}
	switch c.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return !h.running(c.GetId())
	case planv1.TaskStatus_TASK_STATUS_STOPPED:
		return !h.running(c.GetId()) && !h.stoppedLately(c)
	case planv1.TaskStatus_TASK_STATUS_DONE:
		in := c.GetIntegration()
		return in == nil || in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetQuestionId() != ""
	}
	return false
}

// stopGrace is how long a correction or review worker stopped on request stays the correction: a lead who moves it to
// another agent stops it, changes its provider or model, and continues it within a minute; a person who changes its
// prompt, a few. Past it, the worker is over, and Djinn goes on without it. While it lasts, the push stays held.
const stopGrace = 10 * time.Minute

// stoppedLately tells whether c was stopped on request less than stopGrace ago: it may be continued, as the same task.
// A stop with no end time is an old one.
func (h *Harness) stoppedLately(c *planv1.Task) bool {
	end := c.GetEndTime()
	return c.GetStatus() == planv1.TaskStatus_TASK_STATUS_STOPPED && end != nil && h.now().Sub(end.AsTime()) < stopGrace
}

// isCancelled tells whether c is a cancelled task.
func isCancelled(c *planv1.Task) bool {
	if c == nil {
		return false
	}
	return c.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED
}

// mergeMain merges main's commit mainSha, as shown names main, into the branch of the line l at old, in the wish's
// integration worktree, checks it, and moves the branch when the commit checks pass; a conflict in code or a red
// check start a correction worker, after attempts in vain. release is the newest release main brings. An azima's
// branch that holds nothing of its own beyond main moves to main's commit, nothing to merge nor check.
func (h *Harness) mergeMain(
	ctx context.Context, l mainLine, settings plan.Settings, shown, mainSha, old, release string, attempts int32,
) {
	logx.Lock(&h.integrateMu, "integration") // The tasks' merges and the push checks use the integration worktree too.
	defer h.integrateMu.Unlock()
	key := l.key()
	repo := l.project.GetDirectory()
	done, ok := h.mainTested[key]
	message := "djinn: merge " + shown
	switch {
	case l.azima != nil && isAncestor(ctx, repo, old, mainSha):
		done, message = mainTested{old: old, main: mainSha, sha: mainSha, shown: shown, release: release}, "djinn: follow "+shown
	case !ok || done.old != old || done.main != mainSha:
		delete(h.mainTested, key)
		sha, f, why := h.mergeAndCheckMain(ctx, l.wish, l.project, settings, l.branch, shown, mainSha, old, release)
		switch {
		case ctx.Err() != nil:
			return // djinn up stops: main is merged again at the next start.
		case f != nil:
			h.mainFailed(ctx, l, settings, shown, f, attempts)
			return
		case why != "":
			h.holdMain(ctx, l, why)
			return
		}
		done = mainTested{old: old, main: mainSha, sha: sha, shown: shown, release: release}
		h.mainTested[key] = done
	}
	holder, why, moved := moveBranch(ctx, repo, l.branch, old, done.sha, message)
	if why != "" {
		if moved {
			delete(h.mainTested, key)
			h.mainNow.Store(true)
			why += ": Djinn merges main again"
		}
		h.holdMain(ctx, l, why)
		return
	}
	delete(h.mainTested, key)
	h.mainMerged(ctx, l, settings, shown, mainSha, old, done.sha, release, "", holder)
}

// mergeAndCheckMain merges main's commit mainSha into branch at old in the wish's integration worktree, settles a
// conflict only in generated files, makes it ready and runs the commit checks there. It returns the commit checked
// green; or what failed, as a correction worker starts from it; or why main waits.
func (h *Harness) mergeAndCheckMain(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, shown, mainSha, old, release string,
) (string, *planv1.IntegrationFailure, string) {
	wt := integrationDir(h.home, project.GetId(), wish.GetId())
	dir, prefix, err := integrationWorktree(ctx, project.GetDirectory(), wt, old)
	if err != nil {
		return "", nil, "prepare the integration worktree: " + err.Error()
	}
	msg := fmt.Sprintf("Merge %s into %s", shown, branch)
	if release != "" {
		msg = fmt.Sprintf("Merge %s (%s) into %s", shown, release, branch)
	}
	if _, err := git(ctx, wt, "merge", "--no-ff", "--no-edit", "-m", msg, mainSha); err != nil {
		conflicts := conflicted(ctx, wt)
		conflict := func(why string) (string, *planv1.IntegrationFailure, string) {
			_, _ = git(ctx, wt, "merge", "--abort")
			return "", &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Base: old, MergeBranch: mainSha, Files: conflicts,
				Reason: why, MainSha: mainSha,
			}, ""
		}
		switch {
		case errors.Is(err, errNoIdentity):
			_, _ = git(ctx, wt, "merge", "--abort")
			return "", nil, noIdentity
		case len(conflicts) == 0:
			_, _ = git(ctx, wt, "merge", "--abort")
			return "", nil, fmt.Sprintf("merge %s: %v", shown, err)
		case !generatedOnly(conflicts, prefix, settings.Generated) || settings.Generate == "":
			return conflict(fmt.Sprintf("%s conflicts with %s in %s", shown, branch, strings.Join(conflicts, ", ")))
		}
		if why := h.settleGenerated(ctx, shown, "", wt, dir, settings.Generate, conflicts, func() string {
			failed, _, _ := h.setUp(ctx, project, settings, wish.GetId(), "", wt, dir)
			return failed
		}); why != "" {
			return conflict(why)
		}
	}
	sha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err.Error()
	}
	command, why, out, stopped := h.commitChecks(ctx, wish, project, settings, nil, sha, wt, dir, func(*planv1.ProjectCheck) {})
	switch {
	case stopped != "":
		return "", nil, stopped
	case why != "":
		return "", &planv1.IntegrationFailure{
			State: planv1.IntegrationState_INTEGRATION_STATE_RED, Base: sha, Command: command, Output: tail(out), Reason: why,
			MainSha: mainSha,
		}, ""
	}
	return sha, nil, ""
}

// mainFailed records a merge of main that conflicted in code or tested red, as f says, after attempts correction
// workers in vain: another one starts while the project's correction_attempts allow it; past them, Djinn merges main
// again only once main moves.
func (h *Harness) mainFailed(
	ctx context.Context, l mainLine, settings plan.Settings, shown string, f *planv1.IntegrationFailure, attempts int32,
) {
	wish, project, branch := l.wish, l.project, l.branch
	what := "conflict"
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		what = "red"
	}
	text := fmt.Sprintf("%s: %s; %s stays as it was", what, headline(f.GetReason()), branch)
	if int(attempts) < settings.CorrectionAttempts {
		// Part of the azima whose branch it is, its work merges into that branch.
		c, err := h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
			WishId: wish.GetId(), ProjectId: project.GetId(), Title: mergeLine(shown, failureWhat(f)), PartOf: l.azima.GetId(),
			Prompt: correctionPrompt(shown, fmt.Sprintf("%s at %s", shown, short8(f.GetMainSha())), f, branch, settings, attempts+1),
		}, &planv1.Task{Correction: &planv1.TaskCorrection{Failure: f, Attempt: attempts + 1}})
		if err == nil {
			h.editLine(ctx, l, func(m *planv1.WishMain) {
				m.CorrectedBy, m.Attempts = c.GetId(), attempts+1
				m.Held = fmt.Sprintf("%s; %s corrects it, attempt %d of %d", text, c.GetCode(), attempts+1, settings.CorrectionAttempts)
			})
			return
		}
		text += "; the correction worker could not start: " + err.Error()
	} else if attempts > 0 {
		text += fmt.Sprintf("; %d correction workers did not get it in", attempts)
	}
	h.editLine(ctx, l, func(m *planv1.WishMain) {
		m.FailedSha, m.Attempts = f.GetMainSha(), 0
		m.Held = text + "; Djinn merges " + shown + " again once it moves"
	})
	h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), "", fmt.Sprintf("Djinn: the merge of %s into %s failed: %s. The move is yours: retry a correction, or leave it waiting (Djinn merges again once main moves).",
		shown, branch, headline(f.GetReason())))
}

// settleMain records main as merged into the integration branch, in.Sha, by the work of a correction worker of its
// merge among tasks, just committed: the wish's integration branch, or the azima's whose branch in.Branch is.
func (h *Harness) settleMain(ctx context.Context, wish *planv1.Wish, project *planv1.Project, tasks []*planv1.Task, in *planv1.TaskIntegration) {
	l := mainLine{wish: wish, project: project, branch: plan.IntegrationBranchOf(wish, project.GetId())}
	// Per azima, the wish's branch is the azima's when it adopted it (keepUp).
	if b := in.GetBranch(); b != "" && (!strings.EqualFold(b, l.branch) || h.pushesPerAzima(ctx, h.store, wish)) {
		if azima := azimaOwning(h.wishAzimas(ctx, wish), b); azima != nil {
			l.branch, l.azima = b, azima
		}
	}
	for _, c := range tasks {
		mainSha := c.GetCorrection().GetFailure().GetMainSha()
		if mainSha == "" {
			continue
		}
		repo := project.GetDirectory()
		old, err := git(ctx, repo, "rev-parse", in.GetSha()+"^1")
		if err != nil {
			logx.L().Error("record main merged by a correction", "loop", "main", "wish", wish.GetId(), "branch", l.branch,
				"task", c.GetCode(), "err", err)
			continue
		}
		settings, _ := plan.LoadSettings(h.home, project)
		shown := cmp.Or(settings.MainBranch, "main")
		if _, ref, err := mainRef(ctx, repo, settings.MainBranch); err == nil {
			shown = shortRef(ref)
		}
		h.mainMerged(ctx, l, settings, shown, mainSha, old, in.GetSha(), newestRelease(ctx, repo, mainSha, old), c.GetId(), "")
	}
}

// mainMerged records main's commit mainSha as merged into the branch of the line l, moved from old to sha, release the
// newest release it brought, by the correction worker correctedBy if one settled it: in the journal and on the wish,
// or on the azima. A release brought into a wish's integration branch, in a project that names an install command,
// is proposed for installing.
func (h *Harness) mainMerged(
	ctx context.Context, l mainLine, settings plan.Settings, shown, mainSha, old, sha, release, correctedBy, holder string,
) {
	ctx = context.WithoutCancel(ctx)
	wish, project, branch := l.wish, l.project, l.branch
	repo := project.GetDirectory()
	record := &planv1.MainMerge{
		WishId: wish.GetId(), ProjectId: project.GetId(), Branch: branch, Main: shown, MainSha: mainSha, OldSha: old, NewSha: sha,
		Release: release, MergeTime: timestamppb.New(h.now()), CorrectedBy: correctedBy,
	}
	if n, err := git(ctx, repo, "rev-list", "--count", "--no-merges", old+".."+mainSha); err == nil {
		count, _ := strconv.ParseInt(n, 10, 32)
		record.Count = int32(count)
	}
	if out, err := git(ctx, repo, "log", "--no-merges", "--format=%s", "-n", strconv.Itoa(pushTitles), old+".."+mainSha); err == nil && out != "" {
		record.Commits = strings.Split(out, "\n")
	}
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorHarness, methodMain, record); err != nil {
			return err
		}
		merged := func(m *planv1.WishMain) {
			m.Last, m.CorrectedBy, m.Attempts, m.FailedSha, m.Held = record, "", 0, "", ""
		}
		if l.azima != nil {
			return editAzima(ctx, tx, l.azima.GetId(), azimaMain(project.GetId(), merged))
		}
		return editMain(ctx, tx, wish.GetId(), project.GetId(), merged)
	})
	if err != nil {
		logx.L().Error("record main merged", "loop", "main", "wish", wish.GetId(), "branch", branch, "err", err)
	}
	kv := []any{"loop", "main", "wish", wish.GetId(), "main", shown, "branch", branch, "sha", short8(sha), "commits", record.GetCount()}
	if release != "" {
		kv = append(kv, "release", release)
	}
	if holder != "" {
		kv = append(kv, "checkout", holder) // The person's checkout of the branch follows.
	}
	logx.L().Info("main merged", kv...)
	h.notify()
	h.wake() // The tasks that start next start from main's commits.
	if release != "" && settings.Install != "" && h.built != nil && l.azima == nil {
		merged := TaskSummary{Title: fmt.Sprintf("%s merged into %s: %s", shown, branch, commitsText(int(record.GetCount()), nil)+", "+release)}
		b := Built{
			WishID: wish.GetId(), WishTitle: wish.GetTitle(), ProjectID: project.GetId(), Project: project.GetName(), Branch: branch,
			Sha: sha, Changes: record.GetCommits(), Install: settings.Install, Summaries: []TaskSummary{merged},
		}
		h.compare(ctx, project, &b)
		if b.Base != "" {
			b.Summaries = append(b.Summaries, merged)
		}
		h.built(b)
	}
}

// holdMain says why main is not merged into the branch of the line l now.
func (h *Harness) holdMain(ctx context.Context, l mainLine, why string) {
	h.editLine(ctx, l, func(m *planv1.WishMain) { m.Held = why })
}

// editMain changes, with edit, where the wish wishID stands against main in the project projectID.
func (h *Harness) editMain(ctx context.Context, wishID, projectID string, edit func(*planv1.WishMain)) {
	ctx = context.WithoutCancel(ctx)
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return editMain(ctx, tx, wishID, projectID, edit) }); err != nil {
		logx.L().Error("record where a wish stands against main", "loop", "main", "wish", wishID, "project", projectID, "err", err)
	}
	h.notify()
}

// editMain changes, with edit, where the wish wishID stands against main in the project projectID, in tx.
func editMain(ctx context.Context, tx *store.Tx, wishID, projectID string, edit func(*planv1.WishMain)) error {
	wish, err := store.Get[*planv1.Wish](ctx, tx, wishID)
	if err != nil {
		return err
	}
	m := mainState(wish, projectID)
	if m == nil {
		m = &planv1.WishMain{ProjectId: projectID}
		wish.Mains = append(wish.Mains, m)
	}
	before := proto.CloneOf(m)
	edit(m)
	if proto.Equal(before, m) {
		return nil
	}
	if err := tx.Journal(actorHarness, methodIntegrate, wish); err != nil {
		return err
	}
	return tx.Put(wish)
}

// mainState is where wish's integration branch stands against main in the project projectID; nil before anything.
func mainState(wish *planv1.Wish, projectID string) *planv1.WishMain {
	for _, m := range wish.GetMains() {
		if strings.EqualFold(m.GetProjectId(), projectID) {
			return m
		}
	}
	return nil
}

// mainRef is the project's main branch in the repository holding repo, and the ref Djinn merges it from: the remote's
// branch when the repository has a remote (refs/remotes/origin/main), else the local one. name is the main_branch
// setting; not set, the remote's default branch (its HEAD, as a clone records it), else main, else master.
func mainRef(ctx context.Context, repo, name string) (main, ref string, err error) {
	memo := passMemoOf(ctx)
	if main, ref, ok := memo.mainOf(repo, name); ok {
		return main, ref, nil
	}
	main, ref, err = findMain(ctx, repo, name)
	if err == nil {
		memo.keepMain(repo, name, main, ref)
	}
	return main, ref, err
}

// findMain is mainRef, read from git.
func findMain(ctx context.Context, repo, name string) (main, ref string, err error) {
	if name != "" {
		remote, target := pushTarget(ctx, repo, name)
		if remote == "" {
			return name, "refs/heads/" + name, nil
		}
		return name, "refs/remotes/" + remote + "/" + target, nil
	}
	remote, _ := pushTarget(ctx, repo, "HEAD") // origin, else the only remote
	if remote != "" {
		if head, err := git(ctx, repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
			name := strings.TrimPrefix(head, remote+"/")
			return name, "refs/remotes/" + remote + "/" + name, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		if remote != "" {
			if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+name); err == nil {
				return name, "refs/remotes/" + remote + "/" + name, nil
			}
		} else if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name, "refs/heads/" + name, nil
		}
	}
	return "", "", errors.New("the project has no main branch: name it with main_branch in its settings")
}

// reTagClash matches git's error line when a tag fetch is rejected because it would clobber an existing local tag.
var reTagClash = regexp.MustCompile(`!\s+\[rejected\]\s+(\S+)\s+->\s+(\S+)\s+\(would clobber existing tag\)`)

// fetchMain fetches ref, the project's main branch as mainRef gives it, and the tags, from its remote into the
// repository holding repo, with the person's own credentials and without ever prompting for them; a local branch
// fetches nothing. Clashing tags are reported once on the wish wishID, without stopping the merge of main.
func (h *Harness) fetchMain(ctx context.Context, wishID, repo, ref string) error {
	remoteBranch, ok := strings.CutPrefix(ref, "refs/remotes/")
	if !ok {
		return nil
	}
	remote, target, _ := strings.Cut(remoteBranch, "/")
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	cmd, cancelFetch := gitx.Command(ctx, repo, "fetch", "--no-tags", remote, "+refs/heads/"+target+":"+ref)
	defer cancelFetch()
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetch %s from %s: %s", target, remote, cmp.Or(strings.TrimSpace(out.String()), err.Error()))
	}
	tagCmd, cancelTags := gitx.Command(ctx, repo, "fetch", "--tags", remote)
	defer cancelTags()
	tagCmd.Env = append(tagCmd.Env, "LC_ALL=C")
	var tagOut bytes.Buffer
	tagCmd.Stdout, tagCmd.Stderr = &tagOut, &tagOut
	_ = tagCmd.Run()
	if wishID != "" {
		seen := make(map[string]bool)
		for _, m := range reTagClash.FindAllStringSubmatch(tagOut.String(), -1) {
			tag := m[1]
			if seen[tag] {
				continue
			}
			seen[tag] = true
			if err := h.reportTagClash(ctx, wishID, remote, tag); err != nil {
				logx.L().Error("report a tag clash", "loop", "main", "wish", wishID, "tag", tag, "remote", remote, "err", err)
			}
		}
	}
	return nil
}

// reportTagClash records once on wish wishID that the local tag differs from remote's, tolerating the clash.
func (h *Harness) reportTagClash(ctx context.Context, wishID, remote, tag string) error {
	msg := fmt.Sprintf("tag %s differs from %s's: kept the local one", tag, remote)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		blocks, err := store.List[*planv1.Block](ctx, tx, store.Where{"wish_id": wishID})
		if err != nil {
			return err
		}
		for _, b := range blocks {
			if b.GetTitle() == msg || b.GetContent() == msg {
				return nil
			}
		}
		req := &planv1.BlockServicePutRequest{
			WishId:  wishID,
			Kind:    "report",
			Title:   msg,
			Content: msg,
		}
		if err := tx.Journal(actorHarness, planv1connect.BlockServicePutProcedure, req); err != nil {
			return err
		}
		_, err = plan.PutBlock(ctx, tx, req)
		return err
	})
	if err == nil {
		h.notify()
	}
	return err
}

// shortRef is a ref as a person names it: origin/main, main.
func shortRef(ref string) string {
	if s, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
		return s
	}
	return strings.TrimPrefix(ref, "refs/heads/")
}

// newestRelease is the newest release main holds and branch lacks, both commits of the repository holding repo: a tag
// v1.2.3 whose commit main holds, or whose commit's parent it does (a release commit carries the built interface on
// top of main, off any branch), and branch does not; "" for none.
func newestRelease(ctx context.Context, repo, main, branch string) string {
	out, err := git(ctx, repo, "for-each-ref", "--format=%(refname:short)", "refs/tags/v*")
	if err != nil || out == "" {
		return ""
	}
	tags := slices.DeleteFunc(strings.Split(out, "\n"), func(t string) bool { return !semver.IsValid(t) })
	slices.SortFunc(tags, func(a, b string) int { return semver.Compare(b, a) })
	for _, tag := range tags {
		base := releaseBase(ctx, repo, tag, main)
		switch {
		case base == "":
		case isAncestor(ctx, repo, base, branch):
			return "" // The branch holds it, and the older ones with it.
		default:
			return tag
		}
	}
	return ""
}

// releaseBase is the commit of main the release tag stands for: its commit when main holds it, else that commit's
// parent when main holds it; "" when main holds neither.
func releaseBase(ctx context.Context, repo, tag, main string) string {
	c, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return ""
	}
	if isAncestor(ctx, repo, c, main) {
		return c
	}
	if p, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", c+"^1"); err == nil && isAncestor(ctx, repo, p, main) {
		return p
	}
	return ""
}

// commitOf is the commit ref names in the repository holding repo; "" when it names none.
func commitOf(ctx context.Context, repo, ref string) string {
	sha, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return sha
}

// isAncestor tells whether the commit a is in the history of b, in the repository holding repo.
func isAncestor(ctx context.Context, repo, a, b string) bool {
	_, err := git(ctx, repo, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// ReleaseFit is what a newer release of Djinn means for a Djinn built from a checkout of one of the projects (go tool
// task install, version local-<commit>).
type ReleaseFit int

const (
	// ReleaseOffer: offered, as to a Djinn installed from a release; the person's click installs it. The checkout is on
	// main, or the release holds the build; or no project holds the build.
	ReleaseOffer ReleaseFit = iota
	// ReleaseInstall: installed by itself, the restart left to the person's click: the checkout is on main, the
	// release holds the build, which had no change not committed, and the project's settings install releases.
	ReleaseInstall
	// ReleaseHeld: not offered: the build holds the release already.
	ReleaseHeld
	// ReleaseMerge: not offered: the checkout is on a branch whose work the release lacks. Djinn merges main into the
	// wishes' integration branches instead, and proposes their build once the release is in.
	ReleaseMerge
)

// Release says what tag, a newer release of Djinn, means for a Djinn built from the commit build, dirty when its tree
// had changes not committed: in the project whose repository holds that commit, main and the tags fetched when the
// repository does not know the release yet, it looks where the person's checkout is. On a branch, the integration
// looks at main at once.
func (h *Harness) Release(ctx context.Context, build string, dirty bool, tag string) (ReleaseFit, error) {
	projects, err := store.List[*planv1.Project](ctx, h.store, nil)
	if err != nil {
		return ReleaseOffer, err
	}
	var project *planv1.Project
	var settings plan.Settings
	for _, p := range projects {
		if !p.GetGit() {
			continue
		}
		if _, err := git(ctx, p.GetDirectory(), "cat-file", "-e", build+"^{commit}"); err != nil {
			continue
		}
		s, err := plan.LoadSettings(h.home, p)
		if err != nil {
			continue
		}
		if project == nil || settings.Install == "" && s.Install != "" { // The project that installs Djinn first.
			project, settings = p, s
		}
	}
	if project == nil {
		return ReleaseOffer, nil
	}
	repo := project.GetDirectory()
	main, ref, _ := mainRef(ctx, repo, settings.MainBranch)
	if main == "" {
		return ReleaseOffer, nil // No main to compare with: the person judges.
	}
	released := commitOf(ctx, repo, "refs/tags/"+tag)
	if released == "" { // A release the repository does not know yet: main and the tags are fetched.
		if err := h.fetchMain(ctx, "", repo, ref); err != nil {
			logx.L().Warn("fetch main to judge a release", "release", tag, "err", err)
		}
		if released = commitOf(ctx, repo, "refs/tags/"+tag); released == "" {
			return ReleaseOffer, nil // Still unknown: the person judges.
		}
	}
	if base := releaseBase(ctx, repo, tag, ref); base != "" && isAncestor(ctx, repo, base, build) || isAncestor(ctx, repo, released, build) {
		return ReleaseHeld, nil
	}
	onMain := strings.EqualFold(plan.CheckedOutBranch(ctx, repo), main)
	inRelease := isAncestor(ctx, repo, build, released)
	switch {
	case onMain && inRelease && !dirty && settings.InstallReleases:
		return ReleaseInstall, nil
	case onMain || inRelease:
		return ReleaseOffer, nil
	}
	h.LookAtMain()
	return ReleaseMerge, nil
}
