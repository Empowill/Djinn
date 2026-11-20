package harness

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// setIntegration names branch the integration branch of the wish in project, made at once when create.
func (e *env) setIntegration(t *testing.T, wishID, projectID, branch string, create bool) {
	t.Helper()
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: branch, CreateBranch: create,
	})); err != nil {
		t.Fatal(err)
	}
}

// TestSetIntegrationCreateBranchSameName: a branch named without --create-branch is absent; set-integration run again
// with --create-branch on the same name makes it, from the reference branch, not from itself (W184: three runs for
// W109 answered ok and made nothing).
func TestSetIntegrationCreateBranchSameName(t *testing.T) {
	t.Parallel()
	const branch = "djinn/T17-gate-stack"
	repo := gitRepo(t)
	e := upWith(t, t.TempDir(), testProviders())
	wishID, projectID := e.wish(t, repo)
	e.setIntegration(t, wishID, projectID, branch, false)
	if _, err := git(t.Context(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		t.Fatalf("%s made without --create-branch", branch)
	}
	e.setIntegration(t, wishID, projectID, branch, true)
	got, err := git(t.Context(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("set-integration --create-branch on the recorded name left the branch absent: %v", err)
	}
	if main, _ := git(t.Context(), repo, "rev-parse", "HEAD"); got != main {
		t.Errorf("%s at %s, want the reference branch's tip %s", branch, got, main)
	}
	e.setIntegration(t, wishID, projectID, branch, true) // There already: left as it is, no error.
}

// TestIntegrateMakesAbsentBranch: the integration makes the wish's branch, named without being made, instead of
// waiting on it in silence (W184), says so on the task, and commits the work into it.
func TestIntegrateMakesAbsentBranch(t *testing.T) {
	t.Parallel()
	const branch = "djinn/T17-gate-stack"
	repo := gitRepo(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = hookProvider{Provider: Fake{}, before: commitWork}
	e := upWith(t, t.TempDir(), providers)
	wishID, projectID := e.wish(t, repo)
	e.setIntegration(t, wishID, projectID, branch, false)
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Work into an absent branch", Prompt: "text done",
		Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetTask().GetId()
	e.watch(t.Context(), t, id, 0)
	if got := e.get(t, id); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("task = %v, want DONE", got)
	}
	e.h.integratePass(t.Context())
	in := e.get(t, id).GetIntegration()
	if in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != branch {
		t.Fatalf("integration = %v, want COMMITTED into %s", in, branch)
	}
	tip, err := git(t.Context(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || tip != in.GetSha() {
		t.Errorf("%s at %q (%v), want the committed %s", branch, tip, err, in.GetSha())
	}
	main, _ := git(t.Context(), repo, "symbolic-ref", "--short", "HEAD") // main or master, by the machine's git.
	if _, err := git(t.Context(), repo, "merge-base", "--is-ancestor", main, branch); err != nil {
		t.Errorf("%s was not made from %s: %v", branch, main, err)
	}
	events := e.watch(t.Context(), t, id, 0)
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), branch+" was not in the project's repository: Djinn made it from "+main)
	}) {
		t.Errorf("the task does not say the branch was made: %v", events)
	}
}
