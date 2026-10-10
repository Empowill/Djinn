package harness

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
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
// pushed, which a rebase would rewrite and push only by force.

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
		log.Printf("djinn: main: %v", err)
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

// keepUp merges the main branch of the project projectID into wish's integration branch there, when a look is due
// (now: whatever the cadence) and main holds what the settings wait for.
func (h *Harness) keepUp(ctx context.Context, wish *planv1.Wish, projectID string, now bool) {
	project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
	branch := plan.IntegrationBranchOf(wish, projectID)
	if err != nil || !project.GetGit() || branch == "" {
		return
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil || !settings.Integrates() || settings.MergeMain == planv1.MergeMain_MERGE_MAIN_OFF {
		return
	}
	state := mainState(wish, projectID)
	if id := state.GetCorrectedBy(); id != "" {
		if c, err := store.Get[*planv1.Task](ctx, h.store, id); err == nil && !h.correctionOver(c) {
			return // A correction worker settles main's merge: its work brings main in.
		}
		// It is over: Djinn looks at main again at once, an attempt in vain counted.
		h.editMain(ctx, wish.GetId(), projectID, func(m *planv1.WishMain) { m.CorrectedBy = "" })
		now = true
	}
	repo := project.GetDirectory()
	key := wish.GetId() + "/" + projectID
	if last := state.GetCheckTime(); !now && last != nil && h.now().Sub(last.AsTime()) < settings.MergeMainEvery {
		// Not time to look at main yet; a merge tested green moves the branch as soon as it can.
		if t, ok := h.mainTested[key]; ok {
			if old, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
				h.mergeMain(ctx, wish, project, settings, branch, t.shown, t.main, old, t.release, state.GetAttempts())
			}
		}
		return
	}
	looked := func(edit func(*planv1.WishMain)) {
		h.editMain(ctx, wish.GetId(), projectID, func(m *planv1.WishMain) {
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
		err = fetchMain(ctx, repo, ref)
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
	h.mergeMain(ctx, wish, project, settings, branch, shortRef(ref), mainSha, old, release, state.GetAttempts())
}

// correctionOver tells whether c, the correction worker of main's merge, is over: its work is committed, main with it;
// or it failed or was stopped, or its own work waits for the person, main left out.
func (h *Harness) correctionOver(c *planv1.Task) bool {
	switch c.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
		return !h.running(c.GetId())
	case planv1.TaskStatus_TASK_STATUS_DONE:
		in := c.GetIntegration()
		return in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetQuestionId() != ""
	}
	return false
}

// mergeMain merges main's commit mainSha, as shown names main, into branch at old, in the wish's integration
// worktree, checks it, and moves the branch when the commit checks pass; a conflict in code or a red check start a
// correction worker, after attempts in vain. release is the newest release main brings.
func (h *Harness) mergeMain(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, shown, mainSha, old, release string,
	attempts int32,
) {
	h.integrateMu.Lock() // The tasks' merges and an install use the integration worktree too.
	defer h.integrateMu.Unlock()
	key := wish.GetId() + "/" + project.GetId()
	done, ok := h.mainTested[key]
	if !ok || done.old != old || done.main != mainSha {
		delete(h.mainTested, key)
		sha, f, why := h.mergeAndCheckMain(ctx, wish, project, settings, branch, shown, mainSha, old, release)
		switch {
		case ctx.Err() != nil:
			return // djinn up stops: main is merged again at the next start.
		case f != nil:
			h.mainFailed(ctx, wish, project, settings, branch, shown, f, attempts)
			return
		case why != "":
			h.holdMain(ctx, wish.GetId(), project.GetId(), why)
			return
		}
		done = mainTested{old: old, main: mainSha, sha: sha, shown: shown, release: release}
		h.mainTested[key] = done
	}
	holder, why, moved := moveBranch(ctx, project.GetDirectory(), branch, old, done.sha, "djinn: merge "+shown)
	if why != "" {
		if moved {
			delete(h.mainTested, key)
			h.mainNow.Store(true)
			why += ": Djinn merges main again"
		}
		h.holdMain(ctx, wish.GetId(), project.GetId(), why)
		return
	}
	delete(h.mainTested, key)
	h.mainMerged(ctx, wish, project, settings, shown, mainSha, old, done.sha, release, "", holder)
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
	if _, err := git(ctx, wt, "merge", "--no-ff", "--no-edit", "--quiet", "-m", msg, mainSha); err != nil {
		conflicts := conflicted(ctx, wt)
		conflict := func(why string) (string, *planv1.IntegrationFailure, string) {
			_, _ = git(ctx, wt, "merge", "--abort")
			return "", &planv1.IntegrationFailure{
				State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, Base: old, MergeBranch: mainSha, Files: conflicts,
				Reason: why, MainSha: mainSha,
			}, ""
		}
		switch {
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
	command, why, out, stopped := h.commitChecks(ctx, wish, project, settings, "", sha, wt, dir, func(*planv1.ProjectCheck) {})
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
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, shown string,
	f *planv1.IntegrationFailure, attempts int32,
) {
	what := "conflict"
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		what = "red"
	}
	text := fmt.Sprintf("%s: %s; %s stays as it was", what, headline(f.GetReason()), branch)
	if int(attempts) < settings.CorrectionAttempts {
		c, err := h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, &planv1.TaskServiceSpawnRequest{
			WishId: wish.GetId(), ProjectId: project.GetId(), Title: mainCorrectionTitle(f, shown, branch),
			Prompt: correctionPrompt(shown, fmt.Sprintf("%s at %s", shown, short8(f.GetMainSha())), f, branch, settings, attempts+1),
		}, &planv1.Task{Correction: &planv1.TaskCorrection{Failure: f, Attempt: attempts + 1}})
		if err == nil {
			h.editMain(ctx, wish.GetId(), project.GetId(), func(m *planv1.WishMain) {
				m.CorrectedBy, m.Attempts = c.GetId(), attempts+1
				m.Held = fmt.Sprintf("%s; %s corrects it, attempt %d of %d", text, c.GetCode(), attempts+1, settings.CorrectionAttempts)
			})
			return
		}
		text += "; the correction worker could not start: " + err.Error()
	} else if attempts > 0 {
		text += fmt.Sprintf("; %d correction workers did not get it in", attempts)
	}
	h.editMain(ctx, wish.GetId(), project.GetId(), func(m *planv1.WishMain) {
		m.FailedSha, m.Attempts = f.GetMainSha(), 0
		m.Held = text + "; Djinn merges " + shown + " again once it moves"
	})
}

// mainCorrectionTitle is the title of a correction worker of main's failed merge f into branch.
func mainCorrectionTitle(f *planv1.IntegrationFailure, shown, branch string) string {
	if f.GetState() == planv1.IntegrationState_INTEGRATION_STATE_RED {
		return fmt.Sprintf("Make the tests pass with %s merged into %s", shown, branch)
	}
	return fmt.Sprintf("Settle the conflict of %s with %s", shown, branch)
}

// settleMain records main as merged into the integration branch, in.Sha, by the work of a correction worker of its
// merge among tasks, just committed.
func (h *Harness) settleMain(ctx context.Context, wish *planv1.Wish, project *planv1.Project, tasks []*planv1.Task, in *planv1.TaskIntegration) {
	for _, c := range tasks {
		mainSha := c.GetCorrection().GetFailure().GetMainSha()
		if mainSha == "" {
			continue
		}
		repo := project.GetDirectory()
		old, err := git(ctx, repo, "rev-parse", in.GetSha()+"^1")
		if err != nil {
			log.Printf("djinn: main: %v", err)
			continue
		}
		settings, _ := plan.LoadSettings(h.home, project)
		shown := cmp.Or(settings.MainBranch, "main")
		if _, ref, err := mainRef(ctx, repo, settings.MainBranch); err == nil {
			shown = shortRef(ref)
		}
		h.mainMerged(ctx, wish, project, settings, shown, mainSha, old, in.GetSha(), newestRelease(ctx, repo, mainSha, old), c.GetId(), "")
	}
}

// mainMerged records main's commit mainSha as merged into the integration branch, moved from old to sha, release the
// newest release it brought, by the correction worker correctedBy if one settled it: in the journal and on the wish.
// A release brought into a project that names an install command is proposed for installing.
func (h *Harness) mainMerged(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, shown, mainSha, old, sha, release,
	correctedBy, holder string,
) {
	ctx = context.WithoutCancel(ctx)
	repo := project.GetDirectory()
	branch := plan.IntegrationBranchOf(wish, project.GetId())
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
		return editMain(ctx, tx, wish.GetId(), project.GetId(), func(m *planv1.WishMain) {
			m.Last, m.CorrectedBy, m.Attempts, m.FailedSha, m.Held = record, "", 0, "", ""
		})
	})
	if err != nil {
		log.Printf("djinn: main: %v", err)
	}
	text := fmt.Sprintf("main: merged %s into %s as %s, %s", shown, branch, short8(sha), commitsText(int(record.GetCount()), nil))
	if release != "" {
		text += ", " + release
	}
	if holder != "" {
		text += "; your checkout of it, " + holder + ", follows"
	}
	log.Printf("djinn: %s", text)
	h.notify()
	h.wake() // The tasks that start next start from main's commits.
	if release != "" && settings.Install != "" && h.built != nil {
		h.built(Built{
			WishID: wish.GetId(), WishTitle: wish.GetTitle(), ProjectID: project.GetId(), Project: project.GetName(), Branch: branch,
			Sha: sha, Changes: record.GetCommits(), Install: settings.Install,
			Checks: []string{fmt.Sprintf("%s merged into %s: %s", shown, branch, commitsText(int(record.GetCount()), nil)+", "+release)},
		})
	}
}

// holdMain says why main is not merged into the wish's integration branch now.
func (h *Harness) holdMain(ctx context.Context, wishID, projectID, why string) {
	h.editMain(ctx, wishID, projectID, func(m *planv1.WishMain) { m.Held = why })
}

// editMain changes, with edit, where the wish wishID stands against main in the project projectID.
func (h *Harness) editMain(ctx context.Context, wishID, projectID string, edit func(*planv1.WishMain)) {
	ctx = context.WithoutCancel(ctx)
	if err := h.store.Tx(ctx, func(tx *store.Tx) error { return editMain(ctx, tx, wishID, projectID, edit) }); err != nil {
		log.Printf("djinn: main: %v", err)
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

// fetchMain fetches ref, the project's main branch as mainRef gives it, and the tags, from its remote into the
// repository holding repo, with the person's own credentials and without ever prompting for them; a local branch
// fetches nothing.
func fetchMain(ctx context.Context, repo, ref string) error {
	remoteBranch, ok := strings.CutPrefix(ref, "refs/remotes/")
	if !ok {
		return nil
	}
	remote, target, _ := strings.Cut(remoteBranch, "/")
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "fetch", "--quiet", "--tags", remote, "+refs/heads/"+target+":"+ref)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetch %s from %s: %s", target, remote, cmp.Or(strings.TrimSpace(out.String()), err.Error()))
	}
	return nil
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
		if err := fetchMain(ctx, repo, ref); err != nil {
			log.Printf("djinn: release %s: %v", tag, err)
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
