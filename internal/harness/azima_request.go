package harness

// An azima pushed with no request (W593, wish Dew, 07/11/2026: the branches of T41, T42, T43 and T45 were on origin with
// no pull request, though the wish asked a draft for each azima; each lead saw one batch, none opened them). Under the
// per-azima strategy, an azima's branch pushed and ahead of main, with no pull or merge request on the forge, is
// recorded as its request proposed (AZIMA_PR_STATE_PROPOSED, the state the question "Open a pull request for T07?"
// records too): the brief says so on the azima's line, and its lead hears it once, by a MOVE event, until the request
// opens. Djinn's own question comes only once the azima's parts are done and green; the draft is due from the first
// push (lead note azima-draft-request).

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// noRequest tells whether what a forge's command printed, failing to view a branch's request, says the branch has
// none: gh's "no pull requests found for branch", glab's "no open merge request available".
func noRequest(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "no pull request") || strings.Contains(s, "no open merge request") ||
		strings.Contains(s, "no merge request")
}

// pushedAzimaPR looks, for azima of wish, whose parts are not all done and green, and whose branch is pushed, for its
// request on the forge: one open is recorded open; none, unopenedAzimaPR says so. An azima done or merged into main, or
// whose request is known open or merged, asks the forge nothing.
func (h *Harness) pushedAzimaPR(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, azima *planv1.Task, branch string,
) {
	if azima.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || azima.GetAzima().GetMerged() != nil {
		return
	}
	switch azima.GetAzima().GetPr().GetState() {
	case planv1.AzimaPrState_AZIMA_PR_STATE_OPEN, planv1.AzimaPrState_AZIMA_PR_STATE_MERGED:
		return
	}
	repo := project.GetDirectory()
	if !aheadOfMain(ctx, repo, settings, branch) {
		return
	}
	pr, answered := h.queryForgePR(ctx, repo, project, branch)
	switch {
	case pr != nil && pr.State == "OPEN":
		h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{Url: pr.URL, State: planv1.AzimaPrState_AZIMA_PR_STATE_OPEN})
	case pr == nil && answered:
		h.unopenedAzimaPR(ctx, wish, project, settings, azima, branch)
	}
}

// unopenedAzimaPR records the request of azima, whose branch is pushed and has none on the forge, proposed, and tells
// the lead of wish, the first time, to open it as a draft. A branch that holds nothing main lacks needs none.
func (h *Harness) unopenedAzimaPR(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, settings plan.Settings, azima *planv1.Task, branch string,
) {
	if azima.GetAzima().GetPr().GetState() == planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED ||
		!aheadOfMain(ctx, project.GetDirectory(), settings, branch) {
		return
	}
	h.recordAzimaPR(ctx, azima, &planv1.AzimaPr{State: planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED})
	line := unopenedLine(azima.GetCode(), requestKind(forgeOf(ctx, project)))
	h.tellLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, wish.GetId(), azima.GetCode(), line)
}

// unopenedLine is the line that tells the lead the branch of the azima code is pushed with no request of kind (PR, MR):
// short, a digest line keeps 160 characters.
func unopenedLine(code, kind string) string {
	return fmt.Sprintf("Djinn: %s pushed, no %s: open it as a draft, by a task (djinn lead notes azima-draft-request).", code, kind)
}

// aheadOfMain tells whether branch of repo holds commits the project's main branch, as Djinn names it, lacks.
func aheadOfMain(ctx context.Context, repo string, settings plan.Settings, branch string) bool {
	_, main, err := resolveMainSha(ctx, repo, settings)
	if err != nil {
		return false
	}
	out, err := git(ctx, repo, "rev-list", "--count", main+"..refs/heads/"+branch)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return err == nil && n > 0
}
