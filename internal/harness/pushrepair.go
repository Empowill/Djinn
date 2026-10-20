package harness

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// One repair per red (W272, decision Q55 A). On 17/10/2026 the push checks of feat/qol stayed red for hours: the lead
// spawned its own repair, W248, which Djinn did not see (it looked for correction workers only), and Djinn started its
// correction, W267, beside it. Now a task spawned from outside Djinn (the lead, the developer, a question's plan) that
// repairs the red push checks of its wish's integration branch, marked so (spawn --repair) or naming them in its title
// or prompt (aimsAtPushChecks), becomes their correction: Task.correction, adopted, and the push names it
// (WishPush.corrected_by). Djinn then starts no correction of its own while it runs, the task starts while the push
// is held, and once its work is in, the push checks run again, as after Djinn's own correction. While a correction of
// that red runs, Djinn's or the lead's, another repair of it is refused, naming the one that runs.

// repairWords are what a title says of the push checks it repairs.
var repairWords = regexp.MustCompile(`(?i)\b(red|fail(s|ed|ing|ure)?|repair(s|ed|ing)?|fix(es|ed|ing)?|broken)\b`)

// aimsAtPushChecks tells whether a task titled title and asked prompt repairs the push checks p holds red: its title
// names the push checks and their failure ("Repair feat/qol: push checks red at 76e975e2"), or its title or its prompt
// names the push checks and the commit they failed at ("feat/qol's push checks are red at 76e975e2"). A prompt that
// names the push checks alone (a worker told not to run them) repairs nothing.
func aimsAtPushChecks(title, prompt string, p *planv1.WishPush) bool {
	title, prompt = strings.ToLower(title), strings.ToLower(prompt)
	if strings.Contains(title, "push check") && repairWords.MatchString(title) {
		return true
	}
	for _, sha := range []string{p.GetFailedSha(), p.GetFailure().GetBase()} {
		if len(sha) < 8 {
			continue
		}
		at := strings.ToLower(short8(sha))
		for _, text := range []string{title, prompt} {
			if strings.Contains(text, "push check") && strings.Contains(text, at) {
				return true
			}
		}
	}
	return false
}

// redPush is the push of wish's integration branch in projectID while its push checks are red; nil when they are not.
func redPush(wish *planv1.Wish, projectID string) *planv1.WishPush {
	p := pushState(wish, projectID)
	if p.GetHeld() == "" || p.GetFailure() == nil || p.GetUnchecked() {
		return nil
	}
	return p
}

// pushRepairOf is the correction among tasks that repairs the red push checks p holds in the wish wishID and the
// project projectID: the one the push names, or a repair Djinn took that the push does not name yet (adoptRepair
// runs once the spawn is written); nil when none runs, waits to, or waits for its work to merge.
func (h *Harness) pushRepairOf(p *planv1.WishPush, wishID, projectID string, tasks []*planv1.Task) *planv1.Task {
	for _, t := range tasks {
		if t.GetWishId() != wishID || t.GetProjectId() != projectID || t.GetCorrection() == nil {
			continue
		}
		if (t.GetId() == p.GetCorrectedBy() || t.GetCorrection().GetAdopted()) && !h.correctionOver(t) {
			return t
		}
	}
	return nil
}

// claimRepair makes task, which req spawns from outside Djinn in project, the correction of wish's red push checks
// when it repairs them (req.repair, or aimsAtPushChecks), and refuses it, naming the one that runs, while another
// correction repairs the same red. tasks are every task, as the spawn read them. While Djinn asks the person about the
// push, the task stays plain work: their answer says what repairs it.
func (h *Harness) claimRepair(req *planv1.TaskServiceSpawnRequest, wish *planv1.Wish, project *planv1.Project, task *planv1.Task,
	tasks []*planv1.Task,
) error {
	work := task.GetProvider() != planv1.Provider_PROVIDER_WATCH && !dispatch.ChangesNoCode(task) && !dispatch.Ifrit(task)
	if !work {
		if req.GetRepair() {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("--repair is for work: a watcher, an ifrit, an investigation or a mockup repairs no branch"))
		}
		return nil
	}
	branch := plan.IntegrationBranchOf(wish, project.GetId())
	p := redPush(wish, project.GetId())
	if p == nil {
		if req.GetRepair() {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the push checks of %s are not red: nothing to repair; spawn it without --repair", branch))
		}
		return nil
	}
	if !req.GetRepair() && !aimsAtPushChecks(req.GetTitle(), req.GetPrompt(), p) {
		return nil
	}
	ctx := context.Background()
	branch = h.heldBranch(ctx, wish, project.GetDirectory(), project.GetId())
	if c := h.pushRepairOf(p, wish.GetId(), project.GetId(), tasks); c != nil {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s already repairs the push checks of %s (red %s): one repair per red; follow it (djinn task watch %s --last 5), "+
				"or stop it before you spawn another (djinn task stop %s)", c.GetCode(), branch, p.GetHeld(), c.GetId(), c.GetId()))
	}
	if p.GetQuestionId() != "" {
		return nil
	}
	task.Correction = &planv1.TaskCorrection{Failure: proto.CloneOf(p.GetFailure()), Attempt: p.GetAttempts() + 1, Adopted: true}
	if task.GetPartOf() == "" {
		task.PartOf = h.pushRepairAzima(ctx, wish, branch) // As Djinn's own correction (W32).
	}
	return nil
}

// heldBranch is the branch whose red push checks hold the push of wish in the project projectID, at repo: the one
// the hold names (W35); in a hold recorded before it, the one its run held; else the wish's branch whose tip is, or holds, the commit they were red at, an azima's first; else the
// wish's integration branch. Under the per-azima strategy it is not the last branch pushed: that may be another
// azima's (W33).
func (h *Harness) heldBranch(ctx context.Context, wish *planv1.Wish, repo, projectID string) string {
	p := pushState(wish, projectID)
	if b := p.GetHeldBranch(); b != "" {
		return b
	}
	if run := p.GetRun(); run.GetStage() == planv1.PushStage_PUSH_STAGE_HELD && run.GetBranch() != "" {
		return run.GetBranch()
	}
	branch := plan.IntegrationBranchOf(wish, projectID)
	if !h.pushesPerAzima(ctx, h.store, wish) {
		return branch
	}
	var branches []string
	for _, a := range h.wishAzimas(ctx, wish) {
		branches = append(branches, azimaBranchName(a))
	}
	branches = append(branches, branch)
	tips := make([]string, len(branches))
	for i, b := range branches {
		tips[i] = commitOf(ctx, repo, "refs/heads/"+b)
	}
	red := slices.DeleteFunc([]string{p.GetFailedSha(), p.GetFailure().GetBase()}, func(sha string) bool { return sha == "" })
	for _, sha := range red {
		if i := slices.Index(tips, sha); i >= 0 {
			return branches[i]
		}
	}
	for i, tip := range tips { // The branch moved since: a commit of the developer's on it.
		for _, sha := range red {
			if tip != "" && isAncestor(ctx, repo, sha, tip) {
				return branches[i]
			}
		}
	}
	return branch
}

// adoptRepair records c, a repair Djinn took as the correction of its wish's red push checks (claimRepair), on the
// push: Djinn starts no correction while it runs (holdPush), and the push checks run again once its work is in. A
// push left to the lead is theirs no longer: c repairs it. c's events and those of the work the push holds say so.
func (h *Harness) adoptRepair(ctx context.Context, c *planv1.Task) {
	ctx = context.WithoutCancel(ctx)
	wish, err := store.Get[*planv1.Wish](ctx, h.store, c.GetWishId())
	if err != nil {
		logx.L().Error("adopt a repair", "loop", "push", "task", c.GetCode(), "err", err)
		return
	}
	branch := h.repairedBranch(ctx, wish, c.GetProjectId())
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), c.GetProjectId(), func(p *planv1.WishPush) {
			p.CorrectedBy, p.Attempts = c.GetId(), c.GetCorrection().GetAttempt()
			p.Held = strings.TrimSuffix(p.GetHeld(), "; left to the lead")
		})
	})
	if err != nil {
		logx.L().Error("adopt a repair", "loop", "push", "task", c.GetCode(), "wish", wish.GetId(), "err", err)
		return
	}
	h.Note(c.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: repairNote(c, branch)})
	if held, err := tasksByID(ctx, h.store, c.GetCorrection().GetFailure().GetTaskIds()); err == nil {
		h.pushSteps(ctx, held, fmt.Sprintf("integration: the push of %s is held, corrected by %s", branch, c.GetCode()))
	}
	h.notify()
}

// repairNote says that Djinn took c as the correction of the red push checks of branch: what its spawner reads, and
// its events.
func repairNote(c *planv1.Task, branch string) string {
	return fmt.Sprintf("Djinn takes %s as the correction of the red push checks of %s: it starts no correction of its own, "+
		"and refuses another repair of them, while %s runs; once its work is in, the push checks run again.",
		c.GetCode(), branch, c.GetCode())
}

// spawnRepairNote is repairNote for the spawner of t, when Djinn took it as a repair; "" otherwise.
func (h *Harness) spawnRepairNote(ctx context.Context, t *planv1.Task) string {
	if !t.GetCorrection().GetAdopted() {
		return ""
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return ""
	}
	return repairNote(t, h.repairedBranch(ctx, wish, t.GetProjectId()))
}

// repairedBranch is the branch whose red push checks a repair Djinn took in wish, in the project projectID, repairs:
// an azima's under the per-azima strategy, not the wish's own (W33).
func (h *Harness) repairedBranch(ctx context.Context, wish *planv1.Wish, projectID string) string {
	project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err != nil {
		return plan.IntegrationBranchOf(wish, projectID)
	}
	return h.heldBranch(ctx, wish, project.GetDirectory(), projectID)
}
