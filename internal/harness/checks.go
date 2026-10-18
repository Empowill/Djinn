package harness

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Checks: a project names the commands Djinn runs on its work (ProjectSettings.checks), each before a task's work is
// committed into its wish's integration branch, before that branch is pushed, or both; and the command that makes a
// fresh worktree ready (setup). Each runs in the wish's integration worktree, the push checks in its push worktree
// (push_apart.go), through a gate of its name, and the project keeps its last run. A red commit check fails the
// merge, as a conflict does: a correction worker starts. A red push check holds the push, said in the tasks' events
// and on the wish; due again on the same tree, it is not run again: Djinn starts a correction worker, and tells the
// lead only once that fails or its attempts are spent. A red check keeps its whole output in a file (check_output.go).

// methodCheck is how the journal records a check's run: the request is the run (planv1.CheckRun).
const methodCheck = "harness/check"

// setupName is the name, and the gate, of a project's setup command.
const setupName = "setup"

// lockFiles are the files a setup command reads, wherever they are in the project: when one changes, the setup runs
// again in each integration worktree.
var lockFiles = []string{
	"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb", "go.sum",
	"Cargo.lock", "Gemfile.lock", "composer.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "mix.lock", "pubspec.lock",
	"Package.resolved", "gradle.lockfile",
}

// check runs run's command, a check or the setup, in dir under the gate of its name, and records how it ended as the
// project's last run of it; red, its whole output is kept in a file (keepOutput), the tests it names failing with it.
// It returns why it failed, "" when it passed, and its output; stopped when djinn up stopped during it, nothing
// recorded.
func (h *Harness) check(ctx context.Context, projectID string, run *planv1.CheckRun, dir string) (why, out string, stopped bool) {
	start := h.now()
	out, code, err := h.command(ctx, run.GetName(), run.GetTaskId(), run.GetCommand(), dir)
	if ctx.Err() != nil {
		return "", "", true
	}
	run.ExitCode, run.Passed = int32(code), err == nil && code == 0
	switch {
	case err != nil:
		run.Reason = fmt.Sprintf("%s could not run: %v", run.GetCommand(), err)
	case code != 0:
		run.Reason = fmt.Sprintf("%s exited %d%s", run.GetCommand(), code, tail(out))
		run.Output = strings.TrimPrefix(tail(out), ":\n")
		run.Failed = failingTests(out)
		run.OutputFile = h.keepOutput(projectID, run, out)
	}
	end := h.now()
	run.EndTime, run.DurationMs = timestamppb.New(end), end.Sub(start).Milliseconds()
	h.recordCheck(ctx, projectID, run)
	return run.GetReason(), out, false
}

// passedOnTree is checks without those a task of batch had its worker pass on sha's tree, read in the worktree wt:
// the worker's last run of the check's command through djinn gate run exited 0 on that very tree (CommandRun.tree),
// so the same command on the same files needs no second run. Each one left out is said in that task's events. A tree
// not read, a run that failed, a change after it, or no run at all: the check stays.
func (h *Harness) passedOnTree(
	ctx context.Context, projectID string, checks []*planv1.ProjectCheck, batch []*planv1.Task, wt, sha string,
) []*planv1.ProjectCheck {
	tree, err := git(ctx, wt, "rev-parse", sha+"^{tree}")
	if err != nil || tree == "" {
		return checks
	}
	costs, err := store.List[*machinev1.CommandCost](ctx, h.store, store.Where{"project_id": projectID})
	if err != nil || len(costs) == 0 {
		return checks
	}
	return slices.DeleteFunc(slices.Clone(checks), func(c *planv1.ProjectCheck) bool {
		line := strings.Join(strings.Fields(c.GetCommand()), " ")
		i := slices.IndexFunc(costs, func(o *machinev1.CommandCost) bool { return strings.EqualFold(o.GetCommand(), line) })
		if i < 0 {
			return false
		}
		for _, t := range batch {
			if lastRunPassed(costs[i].GetRecentRuns(), t.GetId(), tree) {
				h.integrationStep(ctx, t, "commit checks: "+c.GetName()+" passed by the worker on this tree, not run again")
				return true
			}
		}
		return false
	})
}

// lastRunPassed tells whether the last of runs the task taskID made exited 0 on tree.
func lastRunPassed(runs []*machinev1.CommandRun, taskID, tree string) bool {
	for _, r := range slices.Backward(runs) {
		if r.GetTaskId() == taskID {
			return r.GetExitCode() == 0 && r.GetTree() == tree
		}
	}
	return false
}

// recordCheck keeps run as the project's last run of its check, or of its setup, with its journal entry.
func (h *Harness) recordCheck(ctx context.Context, projectID string, run *planv1.CheckRun) {
	ctx = context.WithoutCancel(ctx)
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		project, err := store.Get[*planv1.Project](ctx, tx, projectID)
		if err != nil {
			return err
		}
		project.CheckRuns = slices.DeleteFunc(project.CheckRuns, func(r *planv1.CheckRun) bool {
			return r.GetSetup() == run.GetSetup() && strings.EqualFold(r.GetName(), run.GetName())
		})
		project.CheckRuns = append(project.CheckRuns, run)
		if err := tx.Journal(actorHarness, methodCheck, run); err != nil {
			return err
		}
		return tx.Put(project)
	})
	if err != nil {
		logx.L().Error("record a check run", "project", projectID, "check", run.GetName(), "wish", run.GetWishId(), "err", err)
	}
}

// setUp makes the integration worktree wt ready with the project's setup command, run in dir, the project's folder
// there, for the wish wishID and the task taskID: once, then again when the command or its lock files change. It
// returns why it failed, "" when the worktree is ready, its output, and its run when it ran red; stopped when djinn
// up stopped during it.
func (h *Harness) setUp(
	ctx context.Context, project *planv1.Project, settings plan.Settings, wishID, taskID, wt, dir string,
) (why, out string, red *planv1.CheckRun, stopped bool) {
	if settings.Setup == "" {
		return "", "", nil, false
	}
	key, err := setupKey(ctx, dir, settings.Setup)
	if err != nil {
		return "read the lock files: " + err.Error(), "", nil, false
	}
	stamp := setupStamp(wt)
	if b, err := os.ReadFile(stamp); err == nil && string(b) == key && setupInstalled(dir) {
		return "", "", nil, false
	}
	sha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return err.Error(), "", nil, false
	}
	run := &planv1.CheckRun{
		Name: setupName, Setup: true, Command: settings.Setup, WishId: wishID, TaskId: taskID, Sha: sha,
	}
	if why, out, stopped = h.check(ctx, project.GetId(), run, dir); why != "" || stopped {
		return why, out, run, stopped
	}
	if err := fsx.WriteAtomic(stamp, []byte(key), 0o600); err != nil {
		logx.L().Warn("record the setup", "project", project.GetName(), "wish", wishID, "file", stamp, "err", err)
	}
	return "", out, nil, false
}

// setupInstalled reports whether the directory's dependencies installed by setup are present.
func setupInstalled(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		nm := filepath.Join(dir, "node_modules")
		entries, err := os.ReadDir(nm)
		if err != nil || len(entries) == 0 {
			return false
		}
	}
	return true
}

// setupStamp is the file that says what the setup of the integration worktree wt last ran on: beside it, in Djinn's
// data folder.
func setupStamp(wt string) string { return wt + ".setup" }

// setupKey names what a setup command makes a worktree ready from: the command, and the lock files of the project's
// folder dir as its index holds them.
func setupKey(ctx context.Context, dir, setup string) (string, error) {
	args := []string{"ls-files", "--stage", "--"}
	for _, name := range lockFiles {
		args = append(args, ":(glob)**/"+name)
	}
	files, err := git(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(setup + "\n" + files))
	return hex.EncodeToString(sum[:]), nil
}

// pushChecks runs the project's push checks on sha, the tip of a branch of the wish as the push began, in its push
// worktree (pushDir), which pushMu holds: merges go on meanwhile in its integration worktree. A check that passed on
// that very commit already, as a commit check, is not run again. committed are the
// tasks the push brings: their events say each check, and running is told the name of each as it starts. It returns
// the failed check's command, why the push is held, "" when it may go, its output, and its run when it ran red;
// stopped when djinn up stopped during them.
func (h *Harness) pushChecks(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, sha string,
	committed []*planv1.Task, running func(check string),
) (cmd, why, out string, red *planv1.CheckRun, stopped bool) {
	checks := settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_PUSH)
	if len(checks) == 0 {
		return "", "", "", nil, false
	}
	cur, err := store.Get[*planv1.Project](ctx, h.store, project.GetId())
	if err != nil {
		return "", err.Error(), "", nil, false
	}
	checks = slices.DeleteFunc(slices.Clone(checks), func(c *planv1.ProjectCheck) bool {
		return slices.ContainsFunc(cur.GetCheckRuns(), func(r *planv1.CheckRun) bool {
			return !r.GetSetup() && r.GetPassed() && r.GetSha() == sha && r.GetCommand() == c.GetCommand() &&
				strings.EqualFold(r.GetName(), c.GetName())
		})
	})
	if len(checks) == 0 {
		return "", "", "", nil, false
	}
	running(checks[0].GetName())
	wt := pushDir(h.home, project.GetId(), wish.GetId())
	dir, _, err := integrationWorktree(ctx, project.GetDirectory(), wt, sha)
	if err != nil {
		return "", "prepare the push worktree: " + err.Error(), "", nil, false
	}
	if why, out, red, stopped = h.setUp(ctx, project, settings, wish.GetId(), "", wt, dir); why != "" || stopped {
		return cmp.Or(settings.Setup, setupName), setupName + ": " + why, out, red, stopped
	}
	for i, c := range checks {
		if i > 0 {
			running(c.GetName())
		}
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: checking %s at %s before the push: %s (%s)",
			branch, short8(sha), c.GetName(), c.GetCommand()))
		run := &planv1.CheckRun{
			Name: c.GetName(), Command: c.GetCommand(), When: planv1.CheckWhen_CHECK_WHEN_PUSH, WishId: wish.GetId(), Sha: sha,
		}
		if why, out, stopped = h.check(ctx, project.GetId(), run, dir); why != "" || stopped {
			return c.GetCommand(), c.GetName() + ": " + why, out, run, stopped
		}
	}
	return "", "", "", nil, false
}

// pushCorrectionPrompt is the first prompt of the attempt-th correction worker of a failed push check on branch: what failed,
// the command, its output tail, and the file of its whole output.
func pushCorrectionPrompt(branch string, f *planv1.IntegrationFailure, settings plan.Settings, attempt int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The push checks of %s failed: %s.\n\n", branch, headline(f.GetReason()))
	fmt.Fprintf(&b, "You correct it. Your worktree is on %s, where a check failed. The command, run in the project's folder: `%s`.", branch, f.GetCommand())
	if out := strings.TrimSpace(strings.TrimPrefix(tail(f.GetOutput()), ":\n")); out != "" {
		b.WriteString(" Its output ends with:\n\n")
		for line := range strings.Lines(out) {
			b.WriteString("    " + line)
		}
	}
	b.WriteString(outputLines(f))
	b.WriteString("\n\nDjinn does not run the push checks again on a tree they were red on: only a commit of yours runs them again.")
	fmt.Fprintf(&b, " Make the push checks pass without undoing what the branch did. Commit your work before you end, with a message in the repository's style (`git log` shows it), and leave nothing not committed: Djinn commits nothing blindly, and a review worker would judge what you leave. Djinn then integrates your branch like any task's, its commit checks (%s) run through their gates. Its success lets Djinn run the push checks again.", settings.CommitGates())
	if attempt > 1 {
		fmt.Fprintf(&b, "\n\nThis is attempt %d: the correction before yours failed.", attempt)
	}
	return b.String()
}

// spawnPushCorrection starts the attempt-th correction worker for a failed push check on branch, at the failure's base
// sha, on the project's provider and model, whatever the tasks of the wish ran on (Q95).
func (h *Harness) spawnPushCorrection(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch string,
	f *planv1.IntegrationFailure, attempt int32,
) (*planv1.Task, error) {
	req := &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: project.GetId(),
		Title:  mergeLine("push checks", failedChecks(f)),
		Prompt: pushCorrectionPrompt(branch, f, settings, attempt),
	}
	made := &planv1.Task{Correction: &planv1.TaskCorrection{Failure: proto.CloneOf(f), Attempt: attempt}}
	return h.spawn(context.WithoutCancel(ctx), planv1connect.TaskServiceSpawnProcedure, req, made)
}

// holdPush holds the push of wish's integration branch in project, at sha, its push checks red for why (with command), out
// their output and red the check's run (nil when none ran): said in the events of the tasks it would bring, committed,
// with the file of its whole output, and on the wish. Once no work of the wish is left to commit there, Djinn starts a
// correction worker (correctPush); else it waits, and the push due again on the same tree starts it (holdSeen).
func (h *Harness) holdPush(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, committed []*planv1.Task, branch, sha, command, why, out string,
	red *planv1.CheckRun,
) {
	cur := pushState(wish, project.GetId())
	if h.pushHeldAlready(ctx, wish, project, branch, sha, cur) {
		return
	}
	held := fmt.Sprintf("at %s, %s", short8(sha), headline(why))
	f := keptOutput(&planv1.IntegrationFailure{
		State:   planv1.IntegrationState_INTEGRATION_STATE_RED,
		Base:    sha,
		Command: command,
		Output:  tail(out),
		Reason:  why,
		TaskIds: ids(committed),
	}, red)
	var runs int32
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
			p.Held, p.HeldRuns, p.Approved, p.Unchecked = held, p.GetHeldRuns()+1, false, false
			p.FailedSha, p.Failure = sha, f
			runs = p.GetHeldRuns()
		})
	})
	if err != nil {
		logx.L().Error("hold the push", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push of %s is held: its push checks are red %s", branch, held))
	if step := outputStep(f, red); step != "" {
		h.pushSteps(ctx, committed, step)
	}
	h.notify()
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		logx.L().Error("read the tasks", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	if runs < 2 && workLeft(tasks, project.GetId()) {
		return // The next work committed may make them pass: Djinn checks again when the next push is due.
	}
	h.correctPush(ctx, wish, project, committed, branch, sha, f, cur.GetAttempts(), tasks)
}

// holdSeen holds the push of wish's integration branch in project, at sha, on f, the failure of its push checks red on
// that very tree already (seenRed): they do not run again, and Djinn starts its correction at once (correctPush),
// unless one runs, Djinn asks about it, it is left to the lead, or a correction repairs the branch, whose work goes in
// first. Nothing is said while it waits: each pass may look.
func (h *Harness) holdSeen(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, committed []*planv1.Task, branch, sha string,
	f *planv1.IntegrationFailure,
) {
	cur := pushState(wish, project.GetId())
	if strings.HasSuffix(cur.GetHeld(), "; left to the lead") {
		// Theirs still, the tree unchanged: the tip is recorded, so the next pass does not look again (pushIfDue).
		if cur.GetFailedSha() != sha {
			err := h.store.Tx(ctx, func(tx *store.Tx) error {
				return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) { p.FailedSha = sha })
			})
			if err != nil {
				logx.L().Error("hold the push", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
			}
		}
		return
	}
	if h.pushHeldAlready(ctx, wish, project, branch, sha, cur) {
		return
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		logx.L().Error("read the tasks", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	if h.repairOf(ctx, wish, project, branch, tasks) != nil {
		return // Its work goes in first (W233): the tree changes, and the push checks run on it then.
	}
	was := f.GetBase()
	f = proto.CloneOf(f)
	f.Base = sha
	if len(committed) > 0 {
		f.TaskIds = ids(committed)
	}
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
			p.Held, p.Approved, p.Unchecked = fmt.Sprintf("at %s, %s", short8(sha), headline(f.GetReason())), false, false
			p.FailedSha, p.Failure = sha, f
		})
	})
	if err != nil {
		logx.L().Error("hold the push", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push checks of %s at %s do not run again: they were red on this very tree at %s (%s); "+
		"the whole output is in %s", branch, short8(sha), short8(was), headline(f.GetReason()), f.GetOutputFile()))
	h.notify()
	h.correctPush(ctx, wish, project, committed, branch, sha, f, cur.GetAttempts(), tasks)
}

// pushHeldAlready tells whether the push of wish's branch in project, held as cur says, needs nothing more for push
// checks red at sha: Djinn asks the person about these very checks, they were left to the lead at sha, or a
// correction runs. A question about other checks is withdrawn: nobody would act on the hold while it stays open.
func (h *Harness) pushHeldAlready(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, sha string, cur *planv1.WishPush,
) bool {
	if id := cur.GetQuestionId(); id != "" {
		if cur.GetHeld() != "" && cur.GetFailedSha() == sha {
			return true // The question is about these very checks: the answer says what Djinn does.
		}
		// Red at another commit than the one Djinn asked about: the question is stale. Djinn withdraws it and acts on
		// these checks instead.
		h.withdrawPushQuestion(ctx, wish.GetId(), project.GetId(), id,
			fmt.Sprintf("Djinn: the push checks of %s ran again at %s and are red; Djinn acts on them instead.", branch, short8(sha)))
	}
	if strings.HasSuffix(cur.GetHeld(), "; left to the lead") && cur.GetFailedSha() == sha {
		return true
	}
	if id := cur.GetCorrectedBy(); id != "" {
		if c, err := store.Get[*planv1.Task](ctx, h.store, id); err == nil && !h.correctionOver(c) {
			return true
		}
	}
	return false
}

// correctPush starts a correction worker for f, the push checks of branch red at sha, after attempts in vain, tasks
// being the wish's: unless another correction repairs the branch, which goes first; when that fails or the attempts
// are spent, Djinn leaves it to the lead.
func (h *Harness) correctPush(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, committed []*planv1.Task, branch, sha string,
	f *planv1.IntegrationFailure, attempts int32, tasks []*planv1.Task,
) {
	if r := h.repairOf(ctx, wish, project, branch, tasks); r != nil {
		// One correction repairs a branch at a time: once its work is in, the push checks run again (repair.go).
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push of %s stays held; its correction waits for %s, which repairs it",
			branch, r.GetCode()))
		return
	}
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil {
		logx.L().Warn("read the project settings", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	var unstarted error
	if int(attempts) < settings.CorrectionAttempts {
		c, err := h.spawnPushCorrection(ctx, wish, project, settings, branch, f, attempts+1)
		if err == nil {
			err = h.store.Tx(ctx, func(tx *store.Tx) error {
				return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
					p.CorrectedBy, p.Attempts, p.FailedSha, p.Failure = c.GetId(), attempts+1, sha, f
				})
			})
			if err == nil {
				h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push of %s is held, corrected by %s", branch, c.GetCode()))
				h.notify()
				return
			}
		}
		unstarted = err
		logx.L().Error("start a push correction", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
	}
	h.askPushHeld(ctx, wish, project, branch, f, attempts, unstarted, committed)
}

// withdrawPushQuestion withdraws id, the question the push of the wish wishID in the project projectID waits on, with
// note, and lets go of it.
func (h *Harness) withdrawPushQuestion(ctx context.Context, wishID, projectID, id, note string) {
	err := h.store.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		q, err := store.Get[*planv1.Question](ctx, tx, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return err
		case q.GetAnswer() == nil && q.GetWithdrawal() == nil:
			q.Withdrawal = &planv1.Withdrawal{Note: note, CreateTime: timestamppb.New(h.now())}
			if err := tx.Journal(actorHarness, methodIntegrate, q); err != nil {
				return err
			}
			if err := tx.Put(q); err != nil {
				return err
			}
		}
		return editPush(ctx, tx, wishID, projectID, func(p *planv1.WishPush) {
			if p.QuestionId == id {
				p.QuestionId, p.Approved = "", false
			}
		})
	})
	if err != nil {
		logx.L().Error("withdraw the push question", "loop", "push", "wish", wishID, "project", projectID, "question", id, "err", err)
	}
}

// askPushHeld tells the lead that the push checks of branch stay red: retry a correction, drop the work, or leave it waiting.
func (h *Harness) askPushHeld(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string, f *planv1.IntegrationFailure,
	attempts int32, unstarted error, committed []*planv1.Task,
) {
	tip := f.GetBase()
	if sha, err := git(ctx, project.GetDirectory(), "rev-parse", "refs/heads/"+branch); err == nil && sha != "" {
		tip = sha
	}
	err := h.store.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
			p.QuestionId, p.Approved, p.CorrectedBy, p.Failure = "", false, "", f
			p.Held = fmt.Sprintf("at %s, %s; left to the lead", short8(tip), headline(f.GetReason()))
			p.FailedSha = tip
		})
	})
	if err != nil {
		logx.L().Error("leave the push checks to the lead", "loop", "push", "wish", wish.GetId(), "project", project.GetId(), "branch", branch, "err", err)
		return
	}
	line := fmt.Sprintf("Djinn: the push checks of %s stay red: %s. The move is yours: retry a correction, drop the work (djinn task done --drop / djinn task cancel), or leave it waiting.",
		branch, headline(f.GetReason()))
	h.tellLeadDetail(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), "", line, capital(strings.TrimPrefix(outputStep(f, nil), "integration: ")))
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push checks of %s stay red; left to the lead", branch))
	h.notify()
}

// workLeft tells whether some work of tasks, in the project projectID, may still be committed while a push is held:
// a running or integrating task, or an active correction worker or urgent task. Other planned tasks, held on the push
// checks, cannot run until the push is green again. Azimas and watchers bring none.
func workLeft(tasks []*planv1.Task, projectID string) bool {
	return slices.ContainsFunc(tasks, func(t *planv1.Task) bool {
		if t.GetProjectId() != projectID || plan.IsAzima(t) || t.GetProvider() == planv1.Provider_PROVIDER_WATCH {
			return false
		}
		switch t.GetIntegration().GetState() {
		case planv1.IntegrationState_INTEGRATION_STATE_PENDING, planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING:
			return true
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_WAITING:
			return true
		case planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RESUMING:
			return t.GetCorrection() != nil || dispatch.Urgent(t)
		}
		return false
	})
}

// errNoChecks is why Djinn does not integrate a project's work: its settings name no check.
var errNoChecks = errors.New("the project's settings name no check")
