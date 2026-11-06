package harness

// The push before a merge request (Q331): on GitLab, a branch pushed with no request open runs a branch pipeline; the
// request opened a minute later runs its own on the same commit, and nothing cancels the first. Over 7 days in dolmen,
// 60 commits ran both, 25 of them on Djinn's branches (tilasm L56). So the push of an azima's branch that a request
// follows goes with the push option ci.skip: the request's pipeline is the one CI run of that commit. Every other push
// goes as any: one whose branch has a request open (ci.skip would then skip the request's pipeline too), one while the
// azima's parts still run, one of a wish's own branch, one to a forge that is not GitLab.

import (
	"context"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// ciSkip is the push option that tells GitLab to start no pipeline for the push.
const ciSkip = "ci.skip"

// skipsBranchCI tells whether the push of branch, of wish in project, goes with ci.skip: the project on GitLab, its
// setting ci_skip_before_request on; branch an azima's, not done, every part committed, so that Djinn proposes its
// request once it is pushed (azimaPRPass), or already proposed it and waits for the answer; and no request open on the
// branch, as glab says. When glab cannot say, the push goes as any: a second pipeline costs less than none.
func (h *Harness) skipsBranchCI(ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch string) bool {
	settings, err := plan.LoadSettings(h.home, project)
	if err != nil || !settings.CISkipBeforeRequest || forgeOf(ctx, project) != plan.ForgeGitLab ||
		!h.pushesPerAzima(ctx, h.store, wish) {
		return false
	}
	azima := azimaOwning(h.wishAzimas(ctx, wish), branch)
	if azima == nil || azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
		return false
	}
	if done, _ := h.azimaPartsDoneAndGreen(ctx, wish.GetId(), azima.GetId()); !done {
		return false
	}
	switch azima.GetAzima().GetPr().GetState() {
	case planv1.AzimaPrState_AZIMA_PR_STATE_UNSPECIFIED:
		if azimaPRBase(ctx, project.GetDirectory(), settings, azima) == "" {
			return false // No base to aim at: Djinn proposes no request.
		}
	case planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED:
		if !h.requestAsked(ctx, azima) {
			return false // Answered "Not yet", or withdrawn: no request follows.
		}
	default:
		return false // Open, or merged: its pipeline runs on each push.
	}
	open, known := h.requestOpen(ctx, project.GetDirectory(), branch)
	return known && !open
}

// requestAsked tells whether Djinn's question proposing azima's request is open, waiting for the answer.
func (h *Harness) requestAsked(ctx context.Context, azima *planv1.Task) bool {
	questions, err := store.List[*planv1.Question](ctx, h.store, store.Where{"wish_id": azima.GetWishId()})
	if err != nil {
		return false
	}
	for _, q := range questions {
		if q.GetTaskId() == azima.GetId() && q.GetDjinnMove().GetKind() == planv1.MoveKind_MOVE_KIND_PULL_REQUEST &&
			q.GetAnswer() == nil && q.GetWithdrawal() == nil {
			return true
		}
	}
	return false
}

// requestOpen tells whether a merge request is open from branch in repo, as glab mr list says; known is false when
// glab could not say (not installed, no token, no network).
func (h *Harness) requestOpen(ctx context.Context, repo, branch string) (open, known bool) {
	var list []struct {
		IID int `json:"iid"`
	}
	if !h.forgeJSON(ctx, repo, []string{"glab", "mr", "list", "--source-branch", branch, "--output", "json"}, &list) {
		return false, false
	}
	return len(list) > 0, true
}

// pushOptionsRefused tells whether git said the remote takes no push option: the push then goes again without.
func pushOptionsRefused(out string) bool {
	return strings.Contains(out, "does not support push options")
}
