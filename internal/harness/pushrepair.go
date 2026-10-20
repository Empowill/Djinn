package harness

import (
	"cmp"
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
	if t := strings.ToLower(title); strings.Contains(t, "push check") && repairWords.MatchString(t) {
		return true
	}
	return namesRed(title, prompt, p)
}

// namesRed tells whether title or prompt names the push checks p holds red and the commit they failed at.
func namesRed(title, prompt string, p *planv1.WishPush) bool {
	title, prompt = strings.ToLower(title), strings.ToLower(prompt)
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

// redPushes are the push of wish in projectID as each branch whose push checks are red sees it, the branch held last
// first (plan.PushHolds): each branch keeps its hold (W37). None when no push checks hold it.
func redPushes(wish *planv1.Wish, projectID string) []*planv1.WishPush {
	return slices.DeleteFunc(plan.PushHolds(pushState(wish, projectID)), func(p *planv1.WishPush) bool {
		return p.GetHeld() == "" || p.GetFailure() == nil || p.GetUnchecked()
	})
}

// heldFor is the hold among holds (plan.PushHolds) that c, a correction, repairs: the one it corrects, else the one
// red at the commit its failure names, else the one hold there is; nil when none.
func heldFor(holds []*planv1.WishPush, c *planv1.Task) *planv1.WishPush {
	if i := slices.IndexFunc(holds, func(p *planv1.WishPush) bool { return p.GetCorrectedBy() == c.GetId() }); i >= 0 {
		return holds[i]
	}
	if base := c.GetCorrection().GetFailure().GetBase(); base != "" {
		if i := slices.IndexFunc(holds, func(p *planv1.WishPush) bool {
			return p.GetFailure().GetBase() == base || p.GetFailedSha() == base
		}); i >= 0 {
			return holds[i]
		}
	}
	if len(holds) == 1 {
		return holds[0]
	}
	return nil
}

// repairedHold is the hold among reds (redPushes) that task, which req spawns among tasks, repairs: the one whose
// commit it names, else the one of its azima's branch, else the branch held last.
func repairedHold(req *planv1.TaskServiceSpawnRequest, task *planv1.Task, reds []*planv1.WishPush, tasks []*planv1.Task) *planv1.WishPush {
	if i := slices.IndexFunc(reds, func(p *planv1.WishPush) bool { return namesRed(req.GetTitle(), req.GetPrompt(), p) }); i >= 0 {
		return reds[i]
	}
	if id := task.GetPartOf(); id != "" {
		if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == id }); i >= 0 {
			branch := azimaBranchName(tasks[i])
			if j := slices.IndexFunc(reds, func(p *planv1.WishPush) bool { return p.GetHeldBranch() == branch }); j >= 0 {
				return reds[j]
			}
		}
	}
	return reds[0]
}

// pushRepairOf is the correction among tasks that repairs the red push checks p, one of reds (redPushes), holds in
// the wish wishID and the project projectID: the one the hold names, or a repair Djinn took for it that the hold does
// not name yet (adoptRepair runs once the spawn is written); nil when none runs, waits to, or waits for its work to
// merge. Another branch's repair does not repair it (W37).
func (h *Harness) pushRepairOf(p *planv1.WishPush, reds []*planv1.WishPush, wishID, projectID string, tasks []*planv1.Task) *planv1.Task {
	for _, t := range tasks {
		if t.GetWishId() != wishID || t.GetProjectId() != projectID || t.GetCorrection() == nil {
			continue
		}
		if (t.GetId() == p.GetCorrectedBy() || t.GetCorrection().GetAdopted() && heldFor(reds, t) == p) && !h.correctionOver(t) {
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
	reds := redPushes(wish, project.GetId())
	if len(reds) == 0 {
		if req.GetRepair() {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the push checks of %s are not red: nothing to repair; spawn it without --repair", branch))
		}
		return nil
	}
	p := repairedHold(req, task, reds, tasks)
	if !req.GetRepair() && !aimsAtPushChecks(req.GetTitle(), req.GetPrompt(), p) {
		return nil
	}
	ctx := context.Background()
	branch = cmp.Or(p.GetHeldBranch(), h.heldBranch(ctx, wish, project.GetDirectory(), project.GetId()))
	if c := h.pushRepairOf(p, reds, wish.GetId(), project.GetId(), tasks); c != nil {
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

// heldBranch is the branch whose red push checks hold the push of wish in the project projectID, at repo, the one its
// fields keep, held last (W37): the one the hold names (W35); in a hold recorded before it, the one its run held; else
// the wish's branch whose tip is, or holds, the commit they were red at, an azima's first; else the wish's integration
// branch. Under the per-azima strategy it is not the last branch pushed: that may be another azima's (W33).
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
	branch, repo := h.repairedBranch(ctx, wish, c)
	edit := h.holdEdit(ctx, wish, repo, c.GetProjectId(), branch, func(p *planv1.WishPush) {
		p.CorrectedBy, p.Attempts = c.GetId(), c.GetCorrection().GetAttempt()
		p.Held = strings.TrimSuffix(p.GetHeld(), "; left to the lead")
	})
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		return editPush(ctx, tx, wish.GetId(), c.GetProjectId(), edit)
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
	branch, _ := h.repairedBranch(ctx, wish, t)
	return repairNote(t, branch)
}

// repairedBranch is the branch whose red push checks c, a repair Djinn took in wish, repairs: an azima's under the
// per-azima strategy, not the wish's own (W33), and among the branches held, the one whose red it took (W37); with
// repo, the directory of c's project.
func (h *Harness) repairedBranch(ctx context.Context, wish *planv1.Wish, c *planv1.Task) (branch, repo string) {
	projectID := c.GetProjectId()
	project, err := store.Get[*planv1.Project](ctx, h.store, projectID)
	if err != nil {
		return plan.IntegrationBranchOf(wish, projectID), ""
	}
	repo = project.GetDirectory()
	if p := heldFor(redPushes(wish, projectID), c); p.GetHeldBranch() != "" {
		return p.GetHeldBranch(), repo
	}
	return h.heldBranch(ctx, wish, repo, projectID), repo
}
