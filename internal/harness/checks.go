package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Checks: a project names the commands Djinn runs on its work (ProjectSettings.checks), each before a task's work is
// committed into its wish's integration branch, before that branch is pushed, or both; and the command that makes a
// fresh worktree ready (setup). Each runs in the wish's integration worktree, through a gate of its name, and the
// project keeps its last run. A red commit check fails the merge, as a conflict does: a correction worker starts. A
// red push check holds the push, said in the tasks' events and on the wish; still red, Djinn asks.

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

// The options of the question Djinn asks once the push checks stay red, answered by their letter.
const (
	checkAgainOption    = "Check again, then push if they pass: once %s is fixed, or what made them fail"
	pushUncheckedOption = "Push it without the push checks, this once"
	leaveHeldOption     = "Leave it: Djinn checks again when the next push is due"
)

// check runs run's command, a check or the setup, in dir under the gate of its name, and records how it ended as the
// project's last run of it. It returns why it failed, "" when it passed, and the end of its output; stopped when djinn
// up stopped during it, nothing recorded.
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
	}
	end := h.now()
	run.EndTime, run.DurationMs = timestamppb.New(end), end.Sub(start).Milliseconds()
	h.recordCheck(ctx, projectID, run)
	return run.GetReason(), out, false
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
		log.Printf("djinn: check %s: %v", run.GetName(), err)
	}
}

// setUp makes the integration worktree wt ready with the project's setup command, run in dir, the project's folder
// there, for the wish wishID and the task taskID: once, then again when the command or its lock files change. It
// returns why it failed, "" when the worktree is ready, and the end of its output; stopped when djinn up stopped
// during it.
func (h *Harness) setUp(
	ctx context.Context, project *planv1.Project, settings plan.Settings, wishID, taskID, wt, dir string,
) (why, out string, stopped bool) {
	if settings.Setup == "" {
		return "", "", false
	}
	key, err := setupKey(ctx, dir, settings.Setup)
	if err != nil {
		return "read the lock files: " + err.Error(), "", false
	}
	stamp := setupStamp(wt)
	if b, err := os.ReadFile(stamp); err == nil && string(b) == key {
		return "", "", false
	}
	sha, err := git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return err.Error(), "", false
	}
	run := &planv1.CheckRun{
		Name: setupName, Setup: true, Command: settings.Setup, WishId: wishID, TaskId: taskID, Sha: sha,
	}
	if why, out, stopped = h.check(ctx, project.GetId(), run, dir); why != "" || stopped {
		return why, out, stopped
	}
	if err := os.WriteFile(stamp, []byte(key), 0o600); err != nil {
		log.Printf("djinn: setup %s: %v", project.GetName(), err)
	}
	return "", out, false
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

// pushChecks runs the project's push checks on sha, the tip of the wish's integration branch, in its integration
// worktree; a check that passed on that very commit already, as a commit check, is not run again. committed are the
// tasks the push brings: their events say each check. It returns why the push is held, "" when it may go; stopped
// when djinn up stopped during them.
func (h *Harness) pushChecks(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, branch, sha string,
	committed []*planv1.Task,
) (why, out string, stopped bool) {
	checks := settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_PUSH)
	if len(checks) == 0 {
		return "", "", false
	}
	cur, err := store.Get[*planv1.Project](ctx, h.store, project.GetId())
	if err != nil {
		return err.Error(), "", false
	}
	checks = slices.DeleteFunc(slices.Clone(checks), func(c *planv1.ProjectCheck) bool {
		return slices.ContainsFunc(cur.GetCheckRuns(), func(r *planv1.CheckRun) bool {
			return !r.GetSetup() && r.GetPassed() && r.GetSha() == sha && r.GetCommand() == c.GetCommand() &&
				strings.EqualFold(r.GetName(), c.GetName())
		})
	})
	if len(checks) == 0 {
		return "", "", false
	}
	h.integrateMu.Lock() // The push checks use the integration worktree.
	defer h.integrateMu.Unlock()
	wt := integrationDir(h.home, project.GetId(), wish.GetId())
	dir, _, err := integrationWorktree(ctx, project.GetDirectory(), wt, sha)
	if err != nil {
		return "prepare the integration worktree: " + err.Error(), "", false
	}
	if why, out, stopped = h.setUp(ctx, project, settings, wish.GetId(), "", wt, dir); why != "" || stopped {
		return setupName + ": " + why, out, stopped
	}
	for _, c := range checks {
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: checking %s at %s before the push: %s (%s)",
			branch, short8(sha), c.GetName(), c.GetCommand()))
		run := &planv1.CheckRun{
			Name: c.GetName(), Command: c.GetCommand(), When: planv1.CheckWhen_CHECK_WHEN_PUSH, WishId: wish.GetId(), Sha: sha,
		}
		if why, out, stopped = h.check(ctx, project.GetId(), run, dir); why != "" || stopped {
			return c.GetName() + ": " + why, out, stopped
		}
	}
	return "", "", false
}

// holdPush holds the push of wish's integration branch in project, at sha, its push checks red for why, out the end
// of the output: said in the events of the tasks it would bring, committed, and on the wish. Red twice in a row, or
// once no work of the wish is left to commit there, Djinn asks the person.
func (h *Harness) holdPush(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, committed []*planv1.Task, branch, sha, why, out string,
) {
	held := fmt.Sprintf("at %s, %s", short8(sha), headline(why))
	var runs int32
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), project.GetId(), func(p *planv1.WishPush) {
			p.Held, p.HeldRuns, p.Approved, p.Unchecked = held, p.GetHeldRuns()+1, false, false
			runs = p.GetHeldRuns()
		})
	})
	if err != nil {
		log.Printf("djinn: push %s: %v", branch, err)
		return
	}
	h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push of %s is held: its push checks are red %s", branch, held))
	h.notify()
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		log.Printf("djinn: push %s: %v", branch, err)
		return
	}
	if runs < 2 && workLeft(tasks, project.GetId()) {
		return // The next work committed may make them pass: Djinn checks again when the next push is due.
	}
	ctxt := fmt.Sprintf("**What failed.** %s, on %s at %s.", capital(headline(why)), branch, short8(sha))
	if s := strings.TrimSpace(strings.TrimPrefix(tail(out), ":\n")); s != "" {
		ctxt += "\n\n```\n" + s + "\n```"
	}
	ctxt += "\n\nDjinn runs the push checks before each push, and pushes nothing red unless you say so."
	q := h.pushQuestion(wish, fmt.Sprintf("The push checks of %s stay red: %s. What should Djinn do?", branch, headline(why)),
		ctxt, []string{fmt.Sprintf(checkAgainOption, branch), pushUncheckedOption, leaveHeldOption})
	if h.askPush(ctx, wish.GetId(), project.GetId(), q, "") == nil {
		h.pushSteps(ctx, committed, fmt.Sprintf("integration: the push checks of %s stay red; Djinn asks you %s", branch, q.GetCode()))
	}
}

// workLeft tells whether some work of tasks, in the project projectID, may still be committed: a task not finished,
// or finished and waiting to be committed. Azimas and watchers bring none.
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
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
			return false
		}
		return true
	})
}

// errNoChecks is why Djinn does not integrate a project's work: its settings name no check.
var errNoChecks = errors.New("the project's settings name no check")
