package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// dewBranch is the integration branch of wish Dew on 12/10/2026, named after its ticket as dolmen's gitlab-sync requires.
const dewBranch = "ET-4368-silae-refused-read-leaves-columns-empty"

// nameBranch names the branch of the azima id, as djinn task update <azima> --branch does.
func (e *env) nameBranch(t *testing.T, id, branch string) (*planv1.Task, error) {
	t.Helper()
	res, err := e.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: id, Branch: proto.String(branch)}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetTask(), nil
}

// spawnPart spawns work part of azima in the project, its fake worker committing the file name, and waits for it to end.
func spawnPart(t *testing.T, e *env, wishID, projectID, azima, name string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, PartOf: azima, Title: "Silae: " + name, Prompt: "file " + name,
		Provider: planv1.Provider_PROVIDER_FAKE,
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

// TestDewAdoptsItsBranchThenPushesPerAzima replays the switch of wish Dew (12/10/2026): in dolmen, a project naming no
// check, the wish pushed per wish into its own branch, named after its ticket, where the work of its only azima T1 was
// merged. Switching it to per azima as it was would have cut T1 a new djinn/T1-silae from main, its work left behind:
// Djinn refuses, saying how to keep it. T1 adopts the branch, as it is; the switch goes; T1's next work merges into
// that same branch, untested as before, never cut again nor moved.
func TestDewAdoptsItsBranchThenPushesPerAzima(t *testing.T) {
	t.Parallel()
	e, _, wishID, projectID, repo := uncheckedDew(t)
	e.h.providers[planv1.Provider_PROVIDER_FAKE] = corrector{work: func(dir, prompt string) string {
		name, ok := strings.CutPrefix(prompt, "file ")
		if !ok {
			return ""
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600); err != nil {
			return "fail " + err.Error()
		}
		for _, args := range [][]string{{"add", name}, {"commit", "--quiet", "-m", "silae: " + name}} {
			if out, err := git(context.Background(), dir, args...); err != nil {
				return "fail " + err.Error() + ": " + out
			}
		}
		return "text Committed " + name + "."
	}}
	main, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: dewBranch, CreateBranch: true,
	})); err != nil {
		t.Fatal(err)
	}
	made, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Silae", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t1 := made.Msg.GetTask()
	w4 := spawnPart(t, e, wishID, projectID, t1.GetId(), "w4.txt")
	e.h.integratePass(t.Context())
	if in := e.get(t, w4.GetId()).GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != dewBranch {
		t.Fatalf("W4's integration %v, want committed into %s", in, dewBranch)
	}
	tip, _ := git(t.Context(), repo, "rev-parse", "refs/heads/"+dewBranch)

	// The switch would strand W4's work: refused, with the command that keeps it.
	switchTo := func() error {
		_, err := e.wishes.PushStrategy(t.Context(), connect.NewRequest(&planv1.WishServicePushStrategyRequest{
			WishId: wishID, Strategy: planv1.PushStrategy_PUSH_STRATEGY_AZIMA.Enum(),
		}))
		return err
	}
	if err := switchTo(); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "djinn task update T1 --branch "+dewBranch) {
		t.Fatalf("switch before T1 adopts its branch: %v, want refused saying how to adopt it", err)
	}

	// A work task has no branch to name.
	if _, err := e.nameBranch(t, w4.GetId(), dewBranch); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "only an azima has a branch of its own") {
		t.Errorf("naming W4's branch: %v, want refused", err)
	}

	// T1 adopts the branch as it is: recorded with main and the commit of main it was cut from.
	adopted, err := e.nameBranch(t, t1.GetId(), dewBranch)
	if err != nil {
		t.Fatal(err)
	}
	if a := adopted.GetAzima(); a.GetBranch() != dewBranch || a.GetBaseBranch() != "main" || a.GetCutFrom() != main || !a.GetNamed() {
		t.Errorf("T1 adopted %v, want %s landing in main, cut from %s, named", a, dewBranch, main)
	}
	if got, _ := git(t.Context(), repo, "rev-parse", "refs/heads/"+dewBranch); got != tip {
		t.Errorf("%s moved to %s on adoption, want %s", dewBranch, got, tip)
	}
	if err := switchTo(); err != nil {
		t.Fatalf("switch once T1 adopted its branch: %v", err)
	}

	// T1's next work merges into the branch it adopted, untested: never cut again, no djinn/T1 branch.
	w5 := spawnPart(t, e, wishID, projectID, t1.GetId(), "w5.txt")
	e.h.integratePass(t.Context())
	if in := e.get(t, w5.GetId()).GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != dewBranch {
		t.Fatalf("W5's integration %v, want committed into %s", in, dewBranch)
	}
	if _, err := git(t.Context(), repo, "merge-base", "--is-ancestor", tip, "refs/heads/"+dewBranch); err != nil {
		t.Errorf("%s no longer holds W4's work at %s", dewBranch, tip)
	}
	if base, _ := git(t.Context(), repo, "merge-base", "refs/heads/main", "refs/heads/"+dewBranch); base != main {
		t.Errorf("%s cut again: its merge base with main is %s, want %s", dewBranch, base, main)
	}
	if out, _ := git(t.Context(), repo, "branch", "--list", "djinn/*"); out != "" {
		t.Errorf("branches cut for the azima: %q, want none", out)
	}
	if a := e.get(t, t1.GetId()).GetAzima(); a.GetBranch() != dewBranch || a.GetCutFrom() != main {
		t.Errorf("T1 reads %v, want %s cut from %s", a, dewBranch, main)
	}
}

// TestAzimaBranchNamedIsCutFromMain: naming an azima's branch that does not exist cuts it from main under that name,
// recorded; its work merges into it. Refused for main, a name Git refuses, another azima's branch, while a merge runs
// into the azima's branch, and once the azima's branch holds work the new one would lack.
func TestAzimaBranchNamedIsCutFromMain(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	cut := in.tip(t)
	t1 := in.azima(t, in.wishID, "One Alpha")
	t2 := in.azima(t, in.wishID, "Two Beta")

	named, err := in.nameBranch(t, t1.GetId(), "ET-1-one")
	if err != nil {
		t.Fatal(err)
	}
	if a := named.GetAzima(); a.GetBranch() != "ET-1-one" || a.GetCutFrom() != cut || a.GetBaseBranch() != in.branch || !a.GetNamed() {
		t.Errorf("T1 named %v, want ET-1-one cut from %s, landing in %s", a, cut, in.branch)
	}
	if got := in.git(t, in.repo, "rev-parse", "refs/heads/ET-1-one"); got != cut {
		t.Errorf("ET-1-one at %s, want main's %s", got, cut)
	}
	if _, err := in.nameBranch(t, t1.GetId(), "ET-1-one"); err != nil {
		t.Errorf("naming it again: %v, want nothing to do", err)
	}

	for _, c := range []struct {
		id, branch, says string
		code             connect.Code
	}{
		{t1.GetId(), in.branch, "is main", connect.CodeFailedPrecondition},
		{t1.GetId(), "ET..1", "no valid branch name", connect.CodeInvalidArgument},
		{t2.GetId(), "ET-1-one", "the branch of azima T1 already", connect.CodeFailedPrecondition},
	} {
		if _, err := in.nameBranch(t, c.id, c.branch); connect.CodeOf(err) != c.code || !strings.Contains(err.Error(), c.says) {
			t.Errorf("naming %s: %v, want %v saying %q", c.branch, err, c.code, c.says)
		}
	}

	// A merge into T1's branch runs: the branch is not renamed under it.
	merging := &planv1.Task{
		Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W9", Title: "Merging", PartOf: t1.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now(),
		Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: "ET-1-one"},
	}
	putTask(t, in.db, merging, "merging")
	if _, err := in.nameBranch(t, t1.GetId(), "ET-1-two"); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "the work of W9 is being merged into ET-1-one") {
		t.Errorf("naming while W9 merges: %v, want refused naming the merge", err)
	}
	merging.Integration = nil
	putTaskInDB(t, in.db, merging)

	// T1's work merges into its named branch; renamed to a new branch then, that work would be left behind.
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	in.pass(t, 0)
	if got := in.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != "ET-1-one" {
		t.Fatalf("W1's integration %v, want committed into ET-1-one", got)
	}
	if _, err := in.nameBranch(t, t1.GetId(), "ET-1-two"); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "holds commits "+in.branch+" lacks") {
		t.Errorf("renaming a branch holding work: %v, want refused", err)
	}
	if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", "--quiet", "refs/heads/ET-1-two"); err == nil {
		t.Error("ET-1-two cut by a refused naming")
	}
}

// requests is an integration per azima whose project has a remote, origin, a bare repository, where teammates push
// the branches of their merge requests; main is at fork.
type requests struct {
	*integration
	bare, fork string
}

// withRequests makes requests.
func withRequests(t *testing.T) *requests {
	t.Helper()
	in := integrating(t)
	r := &requests{integration: in, bare: in.remote(t)}
	for _, dir := range []string{in.repo, r.bare} {
		in.git(t, dir, "config", "maintenance.auto", "false")
		in.git(t, dir, "config", "gc.auto", "0")
	}
	in.setAzimaStrategy(t)
	r.fork = in.tip(t)
	return r
}

// onRemote commits name on branch from the commit from, pushes it to origin as a teammate does, leaves this
// repository without it, and returns the commit.
func (r *requests) onRemote(t *testing.T, from, branch, name string) string {
	t.Helper()
	r.git(t, r.repo, "switch", "--quiet", "--detach", from)
	writeFile(t, r.repo, "app/src/"+name, name+"\n")
	commitAll(t, r.repo, "Request: "+name)
	r.git(t, r.repo, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	sha := r.git(t, r.repo, "rev-parse", "HEAD")
	r.git(t, r.repo, "switch", "--quiet", r.branch)
	return sha
}

// mainMoves commits on main and pushes it, and returns the commit.
func (r *requests) mainMoves(t *testing.T) string {
	t.Helper()
	writeFile(t, r.repo, "app/src/main.txt", "main\n")
	commitAll(t, r.repo, "Main moves")
	r.git(t, r.repo, "push", "--quiet", "origin", r.branch)
	return r.tip(t)
}

// TestAzimaBranchNamedFromTheRemote replays wish Dew (13-14/10/2026): in dolmen, per azima, azimas were named after
// the branches of open merge requests, which origin held and the repository did not. Djinn made each from main: it
// held main's commits and lacked the request's own (ahead 22, behind 1), and nothing could be pushed there. A branch
// the remote holds is made from the remote's, no upstream set, though the azima's branch so far holds a newer main;
// its work merges into it and is pushed there.
func TestAzimaBranchNamedFromTheRemote(t *testing.T) {
	testx.Portable(t)
	r := withRequests(t)
	const mr = "ET-4283-jest-sans-sentry"
	request := r.onRemote(t, r.fork, mr, "jest.txt")
	r.mainMoves(t)

	t1 := r.azima(t, r.wishID, "Jest") // Its branch cut from main as it moved.
	named, err := r.nameBranch(t, t1.GetId(), mr)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+mr); got != request {
		t.Errorf("%s made at %s, want origin's %s", mr, got, request)
	}
	if a := named.GetAzima(); a.GetBranch() != mr || a.GetCutFrom() != r.fork || !a.GetNamed() {
		t.Errorf("T1 named %v, want %s cut from %s, its merge base with main", a, mr, r.fork)
	}
	if out, err := git(t.Context(), r.repo, "config", "branch."+mr+".merge"); err == nil {
		t.Errorf("%s tracks %s, want no upstream", mr, out)
	}

	w1 := r.finished(t, "W1", map[string]string{"app/src/w1.txt": "w1\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	r.pass(t, 0)
	if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != mr {
		t.Fatalf("W1's integration %v, want committed into %s", got, mr)
	}
	if _, err := r.h.Push(t.Context(), r.wishID, r.projectID, mr); err != nil && !errors.Is(err, plan.ErrUpToDate) {
		t.Fatalf("push of %s: %v", mr, err)
	}
	if got, want := r.git(t, r.bare, "rev-parse", "refs/heads/"+mr), r.git(t, r.repo, "rev-parse", "refs/heads/"+mr); got != want {
		t.Errorf("origin's %s at %s, want %s pushed", mr, got, want)
	}
}

// TestAzimaBranchApartFromTheRemote: an azima's branch named that is here behind the remote's is brought up to it; one
// that diverged from it, as Djinn made them from main for wish Dew (13/10/2026), is refused, left as it is. One that
// falls behind later, a teammate pushing to it, is fetched and brought up to the remote's before work merges into it
// (W164); one that diverged later takes no more work and is not pushed, the lead told once, until a person aligns them.
func TestAzimaBranchApartFromTheRemote(t *testing.T) {
	testx.Portable(t)
	r := withRequests(t)
	var mu sync.Mutex
	var told []string
	r.h.NotifyLead(func(_ context.Context, ev *planv1.LeadEvent) error {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, ev.GetText())
		return nil
	})
	const apart, behind = "ET-3063-collab-signes", "hotfix-company-tenant-isolation"
	r.onRemote(t, r.fork, apart, "collab.txt")
	behindRemote := r.onRemote(t, r.fork, behind, "tenant.txt")
	main := r.mainMoves(t)
	r.git(t, r.repo, "branch", apart, main) // As Djinn made it.
	r.git(t, r.repo, "branch", behind, r.fork)

	t1 := r.azima(t, r.wishID, "Tenant")
	if _, err := r.nameBranch(t, t1.GetId(), apart); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), apart+" has diverged from origin/"+apart+" (ahead 1, behind 1)") {
		t.Errorf("naming a diverged branch: %v, want refused saying it diverged", err)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+apart); got != main {
		t.Errorf("%s moved to %s by a refused naming, want %s", apart, got, main)
	}
	if _, err := r.nameBranch(t, t1.GetId(), behind); err != nil {
		t.Fatal(err)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+behind); got != behindRemote {
		t.Errorf("%s at %s, want brought up to origin's %s", behind, got, behindRemote)
	}

	// A teammate pushes to it; this repository has not fetched it.
	teammate := r.onRemote(t, behindRemote, behind, "mate.txt")
	r.git(t, r.repo, "update-ref", "refs/remotes/origin/"+behind, behindRemote)
	w1 := r.finished(t, "W1", map[string]string{"app/src/w1.txt": "w1\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	r.pass(t, 0)
	if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != behind {
		t.Fatalf("W1's integration %v, want committed into %s brought up to origin's", got, behind)
	}
	if !isAncestor(t.Context(), r.repo, teammate, "refs/heads/"+behind) {
		t.Errorf("%s lacks the teammate's %s: not fetched and brought up before the merge", behind, teammate)
	}

	// The teammate pushes another commit over W1's merge, by force: they diverged.
	r.git(t, r.repo, "switch", "--quiet", "--detach", teammate)
	writeFile(t, r.repo, "app/src/mate2.txt", "mate2\n")
	commitAll(t, r.repo, "Request: mate2")
	r.git(t, r.repo, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/"+behind)
	r.git(t, r.repo, "switch", "--quiet", r.branch)
	merged := r.git(t, r.repo, "rev-parse", "refs/heads/"+behind)
	w2 := r.finished(t, "W2", map[string]string{"app/src/w2.txt": "w2\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	r.pass(t, 0)
	r.pass(t, 0)
	want := behind + " has diverged from origin/" + behind + " (ahead 3, behind 1)" // W1's work, pushed before
	if got := r.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		!strings.Contains(got.GetReason(), want) {
		t.Fatalf("W2's integration %v, want pending: %s", got, want)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+behind); got != merged {
		t.Errorf("%s moved to %s, want left at %s", behind, got, merged)
	}
	if _, err := r.h.Push(t.Context(), r.wishID, r.projectID, behind); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("push of %s: %v, want refused: %s", behind, err, want)
	}
	if _, err := r.h.Pull(t.Context(), r.project(t), behind); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("pull of %s: %v, want refused: %s", behind, err, want)
	}
	mu.Lock()
	if n := len(slices.DeleteFunc(slices.Clone(told), func(s string) bool { return !strings.Contains(s, want) })); n != 1 {
		t.Errorf("the lead was told %d times that %s: %q, want once", n, want, told)
	}
	mu.Unlock()

	// The lead aligns them by hand: the work merges.
	r.git(t, r.repo, "switch", "--quiet", behind)
	r.git(t, r.repo, "merge", "--quiet", "--no-edit", "origin/"+behind)
	r.git(t, r.repo, "switch", "--quiet", r.branch)
	r.pass(t, 0)
	if got := r.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetBranch() != behind {
		t.Fatalf("W2's integration %v once aligned, want committed into %s", got, behind)
	}
}

// TestPullBehindTheRemote (W164): a branch Djinn pushes, behind the remote's, is pulled from the projects list, a
// fast-forward; one checked out in the developer's own worktree with changes not committed is left as it is, by the pull
// and by the merge of work into it, which waits and tells the lead; committed, both go on.
func TestPullBehindTheRemote(t *testing.T) {
	testx.Portable(t)
	r := withRequests(t)
	var mu sync.Mutex
	var told []string
	r.h.NotifyLead(func(_ context.Context, ev *planv1.LeadEvent) error {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, ev.GetText())
		return nil
	})
	const branch = "ET-77-pull"
	start := r.onRemote(t, r.fork, branch, "start.txt")
	t1 := r.azima(t, r.wishID, "Pull")
	if _, err := r.nameBranch(t, t1.GetId(), branch); err != nil {
		t.Fatal(err)
	}

	// Pulled from the list: fetched, then fast-forwarded.
	mate := r.onRemote(t, start, branch, "mate.txt")
	r.git(t, r.repo, "update-ref", "refs/remotes/origin/"+branch, start)
	pulls, err := r.h.Pull(t.Context(), r.project(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || pulls[0].GetBranch() != branch || pulls[0].GetOldSha() != start || pulls[0].GetSha() != mate ||
		pulls[0].GetRefused() != "" {
		t.Errorf("pulls %v, want %s from %s to %s", pulls, branch, start, mate)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != mate {
		t.Errorf("%s at %s, want pulled to %s", branch, got, mate)
	}
	if pulls, err := r.h.Pull(t.Context(), r.project(t), ""); err != nil || len(pulls) != 0 {
		t.Errorf("pull again: %v, %v, want nothing to do", pulls, err)
	}

	// The developer's own checkout of it holds changes: nothing moves it.
	// Git names a worktree by its resolved path: on macOS t.TempDir() is a link (docs/pitfalls.md).
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dev := filepath.Join(tmp, "dev")
	r.git(t, r.repo, "worktree", "add", "--quiet", dev, branch)
	writeFile(t, dev, "app/src/start.txt", "mine\n")
	mate2 := r.onRemote(t, mate, branch, "mate2.txt")
	want := "your checkout of it, " + dev + ", has changes not committed"
	if _, err := r.h.Pull(t.Context(), r.project(t), branch); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("pull under a checkout with changes: %v, want refused: %s", err, want)
	}
	w1 := r.finished(t, "W1", map[string]string{"app/src/w1.txt": "w1\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	r.pass(t, 0)
	if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
		!strings.Contains(got.GetReason(), want) {
		t.Fatalf("W1's integration %v, want pending: %s", got, want)
	}
	if got := r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != mate {
		t.Errorf("%s moved to %s under a checkout with changes, want left at %s", branch, got, mate)
	}
	if got := r.git(t, dev, "status", "--porcelain", "--untracked-files=no"); got != "M app/src/start.txt" {
		t.Errorf("the developer's checkout reads %q, want its change kept", got)
	}
	mu.Lock()
	if !slices.ContainsFunc(told, func(s string) bool { return strings.Contains(s, want) }) {
		t.Errorf("the lead was told %q, want: %s", told, want)
	}
	mu.Unlock()

	// Committed: the branch is brought up to the remote's, the checkout following, and the work merges.
	r.git(t, dev, "checkout", "--quiet", "--", ".")
	r.pass(t, 0)
	if got := r.get(t, w1.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("W1's integration %v, want committed", got)
	}
	if !isAncestor(t.Context(), r.repo, mate2, "refs/heads/"+branch) {
		t.Errorf("%s lacks the remote's %s", branch, mate2)
	}
	if got, want := r.git(t, dev, "rev-parse", "HEAD"), r.git(t, r.repo, "rev-parse", "refs/heads/"+branch); got != want {
		t.Errorf("the developer's checkout at %s, want following %s at %s", got, branch, want)
	}
}
