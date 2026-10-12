package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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
