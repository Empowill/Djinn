package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// uncheckedDew is wish Dew as the incident of its W4 found it (12/10/2026): two Git projects, the second naming no
// check, its checkout on main and clean; a fake worker that commits a file in its worktree. It returns the second
// project's folder, with the wish and the project.
func uncheckedDew(t *testing.T) (e *env, queue *leadQueue, wishID, projectID, repo string) {
	t.Helper()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = corrector{work: func(dir, _ string) string {
		if err := os.WriteFile(filepath.Join(dir, "silae.txt"), []byte("columns\n"), 0o600); err != nil {
			return "fail " + err.Error()
		}
		for _, args := range [][]string{{"add", "silae.txt"}, {"commit", "--quiet", "-m", "silae: a refused read leaves its columns"}} {
			if out, err := git(context.Background(), dir, args...); err != nil {
				return "fail " + err.Error() + ": " + out
			}
		}
		return "text Columns left out of a refused read."
	}}
	e = upWith(t, t.TempDir(), providers)
	queue = &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, projectID = twoProjects(t, e)
	project, err := store.Get[*planv1.Project](t.Context(), e.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	repo = project.GetDirectory()
	if _, err := git(t.Context(), repo, "branch", "-M", "main"); err != nil {
		t.Fatal(err)
	}
	// The wish was made with the checkout on main: its integration branch there.
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: "main",
	})); err != nil {
		t.Fatal(err)
	}
	return e, queue, wishID, projectID, repo
}

// spawnDone spawns the work of W4 in the project and waits for its fake worker to end.
func spawnDone(t *testing.T, e *env, wishID, projectID string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Silae: a refused read leaves its columns",
		Prompt: "Leave the columns of a refused read.", Provider: planv1.Provider_PROVIDER_FAKE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, res.Msg.GetTask().GetId(), 0)
	got, err := store.Get[*planv1.Task](t.Context(), e.db, res.Msg.GetTask().GetId())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("task %v (%s), want done", got.GetStatus(), got.GetError())
	}
	return got
}

// leadLines are the lead's events about code, kind and text.
func leadLines(q *leadQueue, code string) []string {
	var lines []string
	for _, e := range q.all() {
		if e.GetCode() == code {
			lines = append(lines, strings.ToLower(strings.TrimPrefix(e.GetKind().String(), "LEAD_EVENT_KIND_"))+" "+e.GetText())
		}
	}
	return lines
}

// integratedInto checks that task's work is committed into branch of repo, its commit there, the checkout left on main
// at main, clean, and the lead told once, by the merge line.
func integratedInto(t *testing.T, e *env, queue *leadQueue, task *planv1.Task, repo, branch, main string) {
	t.Helper()
	got, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if in := got.GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != branch {
		t.Fatalf("integration %v, want committed into %s", in, branch)
	}
	if _, err := git(t.Context(), repo, "merge-base", "--is-ancestor", "refs/heads/"+task.GetBranch(), "refs/heads/"+branch); err != nil {
		t.Errorf("%s does not hold the work of %s", branch, task.GetBranch())
	}
	head, _ := git(t.Context(), repo, "symbolic-ref", "--short", "HEAD")
	tip, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
	status, _ := git(t.Context(), repo, "status", "--porcelain")
	if head != "main" || tip != main || status != "" {
		t.Errorf("the checkout is on %q at %s, changes %q: want main at %s, clean", head, tip, status, main)
	}
	lines := leadLines(queue, task.GetCode())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "merged ") || !strings.Contains(lines[0], branch) {
		t.Errorf("lead events %q, want one merge line into %s", lines, branch)
	}
}

// TestDoneWithACommitIntegratesWithoutChecks replays W4 of wish Dew (12/10/2026): in dolmen, a second project naming
// no check, its checkout on main, the lead gave the wish a branch of its own (set-integration --create-branch, push
// mode ask, never pushed). W4's worker committed and ended done; Djinn never merged it: a project naming no check
// integrated nothing, and the lead only heard that W4 was done. Its work now goes into the wish's branch, untested,
// the checkout of main untouched.
func TestDoneWithACommitIntegratesWithoutChecks(t *testing.T) {
	t.Parallel()
	e, queue, wishID, projectID, repo := uncheckedDew(t)
	main, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: "silae-skip-forbidden", CreateBranch: true,
		PushMode: planv1.PushMode_PUSH_MODE_ASK,
	})); err != nil {
		t.Fatal(err)
	}
	task := spawnDone(t, e, wishID, projectID)
	if task.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING {
		t.Fatalf("integration at the end %v, want pending", task.GetIntegration())
	}
	e.h.integratePass(t.Context())
	integratedInto(t, e, queue, task, repo, "silae-skip-forbidden", main)
}

// TestDoneWithACommitSaysWhyNotIntegrated: the same work, its wish integrating into main, stays out, never silently:
// the task and the lead's one line say why. The lead gives the wish a branch of its own and puts the work back in
// line, as W4 of Dew needs once the fix is installed, and it merges.
func TestDoneWithACommitSaysWhyNotIntegrated(t *testing.T) {
	t.Parallel()
	e, queue, wishID, projectID, repo := uncheckedDew(t)
	main, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
	task := spawnDone(t, e, wishID, projectID)
	if task.GetIntegration() != nil {
		t.Fatalf("integration %v, want none", task.GetIntegration())
	}
	why := "the project's settings name no check, and untested work goes only to a wish's own branch " +
		"(djinn wish set-integration --create-branch)"
	events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if texts := eventTexts(events); !slices.Contains(texts, "integration: none: "+why) {
		t.Errorf("events %q, without why its work stays out", texts)
	}
	want := []string{"ended Djinn: " + task.GetCode() + " done, not integrated: " + why +
		". Its final note: Columns left out of a refused read."}
	if lines := leadLines(queue, task.GetCode()); !slices.Equal(lines, want) {
		t.Errorf("lead events %q, want %q", lines, want)
	}
	e.h.integratePass(t.Context())
	if got, _ := store.Get[*planv1.Task](t.Context(), e.db, task.GetId()); got.GetIntegration() != nil {
		t.Fatalf("integration %v after a pass, want none", got.GetIntegration())
	}

	pend := connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: task.GetId(), Integration: proto.String("pending")})
	if _, err := e.tasks.Update(t.Context(), pend); err == nil || !strings.Contains(err.Error(), why) {
		t.Fatalf("put back in line into main: %v, want refused saying %q", err, why)
	}
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: "silae-skip-forbidden", CreateBranch: true,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Update(t.Context(), pend); err != nil {
		t.Fatal(err)
	}
	queue.mu.Lock()
	queue.events = nil
	queue.mu.Unlock()
	e.h.integratePass(t.Context())
	integratedInto(t, e, queue, task, repo, "silae-skip-forbidden", main)
}
