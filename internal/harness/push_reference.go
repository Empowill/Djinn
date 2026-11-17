package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// errReferencePush says that Djinn refused to push a project's reference branch: it reaches the remote only through a
// merged pull or merge request (refuseReference).
var errReferencePush = errors.New("a project's reference branch is never pushed")

// refuseReference refuses the push of branch, of wish in project, when it is the project's reference branch, main on
// most projects, as Djinn names it (resolveMainSha), or when its name on the remote, target, is: whatever the wish, its
// push strategy and the project, that branch reaches its remote only through a merged pull or merge request. Before
// it, a wish that integrated into main with the wish strategy had Djinn push main ten times from 07/11/2026, with no
// pull request (tilasm L09); the forge's branch protection did not stop it, the local token being an admin's. Only the
// reference branch is refused, not the other branches the forge protects: Djinn reads it with no network call, before
// each push. The refusal says the command that gives the wish a branch of its own, on the push's line and, once per
// refusal, in the events of committed; nil when branch is not the reference branch.
func (h *Harness) refuseReference(
	ctx context.Context, wish *planv1.Wish, project *planv1.Project, branch, target string, committed []*planv1.Task, run *pushRun,
) error {
	settings, _ := plan.LoadSettings(h.home, project)
	main, _, _ := resolveMainSha(ctx, project.GetDirectory(), settings) // None found: no branch to keep from a push.
	if main == "" || !strings.EqualFold(branch, main) && !strings.EqualFold(target, main) {
		return nil
	}
	note := referenceRefusal(wish, project, branch, main)
	if cur := pushState(wish, project.GetId()).GetRun(); cur.GetBranch() != branch ||
		cur.GetStage() != planv1.PushStage_PUSH_STAGE_FAILED || cur.GetNote() != note {
		h.pushSteps(ctx, committed, "integration: "+note)
		logx.L().Warn("push refused: reference branch", "loop", "push", "wish", wish.GetId(), "project", project.GetId(),
			"branch", branch)
	}
	run.set(ctx, planv1.PushStage_PUSH_STAGE_FAILED, "", note)
	return fmt.Errorf("%w: %s", errReferencePush, note)
}

// referenceRefusal says why Djinn does not push branch, of wish in project, whose reference branch is main, and the
// command that gives the wish a branch of its own there.
func referenceRefusal(wish *planv1.Wish, project *planv1.Project, branch, main string) string {
	what := branch + " is the reference branch of " + cmp.Or(project.GetName(), project.GetId())
	if !strings.EqualFold(branch, main) {
		what = branch + " goes to " + main + ", the reference branch of " + cmp.Or(project.GetName(), project.GetId())
	}
	return fmt.Sprintf("Djinn never pushes %s: %s, which reaches its remote only through a merged pull or merge request. "+
		"The wish %q integrates into it; give it a branch of its own, pushed and proposed as a pull request: "+
		"djinn wish set-integration %s --project-id %s --branch <branch> --create-branch",
		branch, what, wish.GetTitle(), wish.GetId(), project.GetId())
}
