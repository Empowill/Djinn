package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// setAzimaStrategy sets the push strategy of wish to azima.
func (in *integration) setAzimaStrategy(t *testing.T) {
	t.Helper()
	strat := planv1.PushStrategy_PUSH_STRATEGY_AZIMA
	_, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId:       in.wishID,
		PushStrategy: &strat,
	}))
	if err != nil {
		t.Fatal(err)
	}
}

// stored is the task id as the store holds it, what Djinn records of an azima included.
func (in *integration) stored(t *testing.T, id string) *planv1.Task {
	t.Helper()
	task, err := store.Get[*planv1.Task](t.Context(), in.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

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

func putTaskInDB(t *testing.T, db *store.Store, task *planv1.Task) {
	t.Helper()
	err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", task); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err != nil {
		t.Fatal(err)
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

func TestNameAzimaBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// Replays the switch of wish Dew (12/10/2026): in dolmen, a project naming no check, the wish pushed per wish into
	// its own branch, named after its ticket, where the work of its only azima T1 was merged. Switching it to per azima
	// as it was would have cut T1 a new djinn/T1-silae from main, its work left behind: Djinn refuses, saying how to
	// keep it. T1 adopts the branch, as it is; the switch goes; T1's next work merges into that same branch, untested
	// as before, never cut again nor moved.
	t.Run("adopts wish branch then pushes per azima", func(t *testing.T) {
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
	})

	// Naming an azima's branch that does not exist cuts it from main under that name, recorded; its work merges into
	// it. Refused for main, a name Git refuses, another azima's branch, while a merge runs into the azima's branch, and
	// once the azima's branch holds work the new one would lack.
	t.Run("cuts from main and validates", func(t *testing.T) {
		t.Parallel()
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
	})

	// Replays wish Dew (13-14/10/2026): a branch the remote holds is made from the remote's, no upstream set, though
	// the azima's branch so far holds a newer main; its work merges into it and is pushed there.
	t.Run("from the remote", func(t *testing.T) {
		t.Parallel()
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
	})

	// An azima's branch named that is here behind the remote's is brought up to it; one that diverged from it is
	// refused, left as it is.
	t.Run("apart from the remote", func(t *testing.T) {
		t.Parallel()
		r := withRequests(t)
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
	})
}

func TestCutAzimaBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// Under the per-azima strategy an azima's branch is cut from main's commit the moment the azima is made, and
	// recorded on it: its branch, the main it lands in, the commit. Main moving on later does not cut it again: its
	// work merges into the branch as cut, which keeps its name once the title changes.
	t.Run("when made", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		cut := in.tip(t)

		t1 := in.azima(t, in.wishID, "One Alpha")
		b1 := "djinn/T1-one-alpha"
		if got := in.git(t, in.repo, "rev-parse", "refs/heads/"+b1); got != cut {
			t.Fatalf("%s at %s when made, want main's %s", b1, got, cut)
		}
		want := &planv1.Azima{Branch: b1, BaseBranch: in.branch, CutFrom: cut}
		if got := in.stored(t, t1.GetId()).GetAzima(); got.GetBranch() != want.GetBranch() ||
			got.GetBaseBranch() != want.GetBaseBranch() || got.GetCutFrom() != want.GetCutFrom() {
			t.Fatalf("recorded %v, want %v", got, want)
		}
		if got := in.get(t, t1.GetId()).GetAzima(); got.GetBaseBranch() != in.branch || got.GetCutFrom() != cut {
			t.Errorf("read %v, want it to land in %s, cut from %s", got, in.branch, cut)
		}

		// Its first work ends; then main moves on, and the title changes.
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		writeFile(t, in.repo, "app/src/later.txt", "later\n")
		in.git(t, in.repo, "add", ".")
		in.git(t, in.repo, "commit", "--quiet", "-m", "Later on main")
		later := in.tip(t)
		if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
			a, err := store.Get[*planv1.Task](t.Context(), tx, t1.GetId())
			if err != nil {
				return err
			}
			a.Title = "Renamed"
			if err := tx.Journal("test", "rename", a); err != nil {
				return err
			}
			return tx.Put(a)
		}); err != nil {
			t.Fatal(err)
		}

		in.pass(t, 0)
		if got := in.get(t, w1.GetId()).GetIntegration(); got.GetBranch() != b1 ||
			got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Fatalf("w1 integration = %v, want committed into %s", got, b1)
		}
		if base := in.git(t, in.repo, "merge-base", "refs/heads/"+b1, later); base != cut {
			t.Errorf("%s cut again: its merge base with main is %s, want %s", b1, base, cut)
		}
		if got := in.stored(t, t1.GetId()).GetAzima().GetCutFrom(); got != cut {
			t.Errorf("cut_from = %s, want %s", got, cut)
		}
		if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", "--quiet", "refs/heads/djinn/T1-renamed"); err == nil {
			t.Error("a renamed azima got a second branch")
		}
	})

	// An azima made before its wish was switched to per azima has no branch; its first part to start cuts it from main,
	// recorded, and starts from it.
	t.Run("when first part starts", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		t1 := in.azima(t, in.wishID, "Made Before")
		b1 := "djinn/T1-made-before"
		if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b1); err == nil {
			t.Fatalf("%s cut under the per-wish strategy", b1)
		}
		in.setAzimaStrategy(t)
		cut := in.tip(t)

		part := &planv1.Task{
			Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W1", Title: "First part", PartOf: t1.GetId(),
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now(),
		}
		putTask(t, in.db, part, "first part")
		wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
		if err != nil {
			t.Fatal(err)
		}
		settings, err := plan.LoadSettings(in.home, project)
		if err != nil {
			t.Fatal(err)
		}
		if from := in.h.workerStartBranch(t.Context(), wish, project, settings, part); from != b1 {
			t.Fatalf("the part starts from %q, want %s", from, b1)
		}
		if got := in.git(t, in.repo, "rev-parse", "refs/heads/"+b1); got != cut {
			t.Errorf("%s at %s, want main's %s", b1, got, cut)
		}
		if got := in.stored(t, t1.GetId()).GetAzima(); got.GetCutFrom() != cut || got.GetBranch() != b1 {
			t.Errorf("recorded %v, want %s cut from %s", got, b1, cut)
		}
	})

	// In a wish that pushes per azima, no azima waits for another: spawning one after another, spawning work of one
	// after work of another, setting such a dependency (depend, update), grouping such work into an azima, or syncing a
	// plan file whose after line names another azima, are refused, naming them.
	t.Run("per azima refuses azima dependencies", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		ctx := t.Context()
		refused := func(what string, err error, want string) {
			t.Helper()
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no azima waits for another") ||
				!strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v, want refused naming %q", what, err, want)
			}
		}

		t1 := in.azima(t, in.wishID, "Base")
		_, err := in.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, Title: "Dependent", Kind: planv1.TaskKind_TASK_KIND_AZIMA, DependsOn: []string{t1.GetCode()},
		}))
		refused("spawn --after", err, "(T2 after T1)")
		t2 := in.azima(t, in.wishID, "Other")

		work := func(code, partOf string, deps ...string) *planv1.Task {
			task := &planv1.Task{
				Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: code, Title: "Work " + code, PartOf: partOf,
				DependsOn: deps, Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now(),
			}
			putTask(t, in.db, task, "work")
			return task
		}
		w1 := work("W1", t1.GetId())
		_, err = in.tasks.Spawn(ctx, connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: in.wishID, Title: "Builds on W1", Prompt: "build", PartOf: t2.GetCode(), DependsOn: []string{w1.GetCode()}, Later: true,
		}))
		refused("spawn work of T2 after W1", err, "(T2 after T1)")

		w2 := work("W2", t2.GetId())
		_, err = in.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: w2.GetId(), After: []string{"W1"}}))
		refused("depend", err, "(T2 after T1)")
		_, err = in.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: t2.GetId(), After: []string{"T1"}}))
		refused("depend an azima", err, "(T2 after T1)")
		_, err = in.tasks.Update(ctx, connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: w2.GetId(), After: []string{"W1"}}))
		refused("update --after", err, "(T2 after T1)")

		loose := work("W3", "", w1.GetId())
		_, err = in.group(t, loose, t2.GetCode())
		refused("group", err, "(T2 after T1)")

		// Inside one azima, as usual.
		w4 := work("W4", t2.GetId())
		if _, err := in.tasks.Depend(ctx, connect.NewRequest(&planv1.TaskServiceDependRequest{TaskId: w4.GetId(), After: []string{"W2"}})); err != nil {
			t.Errorf("work after work of its own azima: %v", err)
		}
		if _, err := in.group(t, loose, t1.GetCode()); err != nil {
			t.Errorf("work after work of the azima it joins: %v", err)
		}

		writeFile(t, in.repo, "app/plan/third.md", "---\ncode: T3\nstatus: open\nafter: T1\n---\n\n# T3 · Third\n")
		_, err = in.plans.Sync(ctx, connect.NewRequest(&planv1.PlanServiceSyncRequest{WishId: in.wishID}))
		refused("plan sync", err, "(T3 after T1)")
		writeFile(t, in.repo, "app/plan/third.md", "---\ncode: T3\nstatus: open\n---\n\n# T3 · Third\n")
		if _, err := in.plans.Sync(ctx, connect.NewRequest(&planv1.PlanServiceSyncRequest{WishId: in.wishID})); err != nil {
			t.Fatalf("plan sync without after: %v", err)
		}
		// Made from its file, its branch is cut from main.
		if got := in.git(t, in.repo, "rev-parse", "refs/heads/djinn/T3-third"); got != in.tip(t) {
			t.Errorf("djinn/T3-third at %s, want main's %s", got, in.tip(t))
		}
	})
}

func TestAzimaBranchFor(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("independent branches from main", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		mainSha := in.tip(t)

		t1 := in.azima(t, in.wishID, "One Alpha")
		t2 := in.azima(t, in.wishID, "Two Beta")
		part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
		part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)

		in.pass(t, 0)

		b1 := "djinn/T1-one-alpha"
		b2 := "djinn/T2-two-beta"

		sha1 := in.git(t, in.repo, "rev-parse", "refs/heads/"+b1)
		sha2 := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

		// Both branches are based on mainSha.
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", mainSha, sha1); err != nil {
			t.Fatalf("%s is not an ancestor of %s: %v", mainSha, b1, err)
		}
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", mainSha, sha2); err != nil {
			t.Fatalf("%s is not an ancestor of %s: %v", mainSha, b2, err)
		}

		// The two branches are independent: neither contains the other's tip.
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", sha1, sha2); err == nil {
			t.Fatalf("%s should not be an ancestor of %s", b1, b2)
		}
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", sha2, sha1); err == nil {
			t.Fatalf("%s should not be an ancestor of %s", b2, b1)
		}

		// Task integration records name the azima branches.
		task1 := in.get(t, w1.GetId())
		if task1.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			task1.GetIntegration().GetBranch() != b1 || task1.GetIntegration().GetSha() != sha1 {
			t.Fatalf("w1 integration = %v, want COMMITTED on %s at %s", task1.GetIntegration(), b1, sha1)
		}
		task2 := in.get(t, w2.GetId())
		if task2.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			task2.GetIntegration().GetBranch() != b2 || task2.GetIntegration().GetSha() != sha2 {
			t.Fatalf("w2 integration = %v, want COMMITTED on %s at %s", task2.GetIntegration(), b2, sha2)
		}
	})

	t.Run("task with no azima uses wish branch", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}) // PartOf is empty.
		in.pass(t, 0)

		w1Task := in.get(t, w1.GetId())
		if w1Task.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
			w1Task.GetIntegration().GetBranch() != in.branch || w1Task.GetIntegration().GetSha() != in.tip(t) {
			t.Fatalf("w1 integration = %v, want COMMITTED on %s at %s", w1Task.GetIntegration(), in.branch, in.tip(t))
		}
	})

	t.Run("per wish mode unchanged", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)

		t1 := in.azima(t, in.wishID, "First")
		t2 := in.azima(t, in.wishID, "Second")
		part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
		part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)

		in.pass(t, 0)

		if got := in.get(t, w1.GetId()).GetIntegration().GetBranch(); got != in.branch {
			t.Fatalf("w1 branch = %s, want wish branch %s", got, in.branch)
		}
		if got := in.get(t, w2.GetId()).GetIntegration().GetBranch(); got != in.branch {
			t.Fatalf("w2 branch = %s, want wish branch %s", got, in.branch)
		}
		for _, b := range []string{"refs/heads/djinn/T1-first", "refs/heads/djinn/T2-second"} {
			if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", b); err == nil {
				t.Fatalf("%s should not exist in per-wish mode", b)
			}
		}
	})
}

func TestIntegrate_AzimaBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// An azima's branch keeps up with main as a wish's integration branch does, by the same merge_main rules: main
	// merged into it, never a rebase, its state recorded on the azima.
	t.Run("keeps up with main", func(t *testing.T) {
		t.Parallel()
		u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
		u.setAzimaStrategy(t)
		t1 := u.azima(t, u.wishID, "With Work")
		t2 := u.azima(t, u.wishID, "Without Work")
		b1, b2 := "djinn/T1-with-work", "djinn/T2-without-work"
		u.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		u.pass(t, 0)
		worked := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1)

		fix := u.onMain(t, "Fix on main", map[string]string{"app/src/fix.txt": "fix\n"})
		u.pass(t, 2*time.Hour) // The next look at main, as merge_main_minutes says.

		tip := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1)
		if !isAncestor(t.Context(), u.repo, fix, tip) || !isAncestor(t.Context(), u.repo, worked, tip) {
			t.Fatalf("%s at %s: holds main's %s: %v, its work %s: %v", b1, tip, fix, isAncestor(t.Context(), u.repo, fix, tip),
				worked, isAncestor(t.Context(), u.repo, worked, tip))
		}
		if parents := strings.Fields(u.git(t, u.repo, "log", "-1", "--format=%P", tip)); len(parents) != 2 || parents[0] != worked {
			t.Errorf("%s's tip has parents %v: want a merge onto %s", b1, parents, worked)
		}
		state := u.stored(t, t1.GetId()).GetAzima().GetMain()
		if state.GetLast().GetMainSha() != fix || state.GetLast().GetBranch() != b1 || state.GetLast().GetNewSha() != tip ||
			state.GetHeld() != "" {
			t.Errorf("T1's state against main = %v", state)
		}
		if got := u.git(t, u.repo, "rev-parse", "refs/heads/"+b2); got != fix {
			t.Errorf("%s at %s: want main's %s, nothing of its own to merge", b2, got, fix)
		}
		if got := u.stored(t, t2.GetId()).GetAzima().GetMain().GetLast().GetNewSha(); got != fix {
			t.Errorf("T2's last merge of main moved it to %s, want %s", got, fix)
		}

		// merge_main off: main stays out, as for the wish's branch.
		writeFile(t, u.home, filepath.Join("projects", u.projectID, "settings.txtpb"),
			"provider: PROVIDER_FAKE\ngenerated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\nmerge_main: MERGE_MAIN_OFF\n")
		u.onMain(t, "Another fix", map[string]string{"app/src/fix2.txt": "fix\n"})
		u.pass(t, 2*time.Hour)
		if got := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1); got != tip {
			t.Errorf("merge_main off, %s moved to %s", b1, got)
		}
	})

	// Main conflicting with an azima's branch starts a correction worker as for a wish's, part of the azima: its work
	// merges into the azima's branch, which then holds main, recorded on the azima as merged by it.
	t.Run("main conflict starts correction", func(t *testing.T) {
		t.Parallel()
		u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
		u.setAzimaStrategy(t)
		t1 := u.azima(t, u.wishID, "Readme")
		b1 := "djinn/T1-readme"
		u.finished(t, "W1", map[string]string{"app/README.md": "# Branch\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		u.pass(t, 0)
		old := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1)
		theirs := u.onMain(t, "Main's readme", map[string]string{"app/README.md": "# Main\n"})
		u.correctWith(func(dir, _ string) string {
			writeFile(t, dir, "README.md", "# Main and Branch\n")
			commitAll(t, dir, "Settle the readme with main")
			return ""
		})

		u.pass(t, 2*time.Hour)
		state := u.stored(t, t1.GetId()).GetAzima().GetMain()
		if got := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1); got != old || state.GetCorrectedBy() == "" {
			t.Fatalf("after the conflict: %s at %s, %v", b1, got, state)
		}
		c := u.ended(t, state.GetCorrectedBy())
		if c.GetPartOf() != t1.GetId() || c.GetCorrection().GetFailure().GetMainSha() != theirs {
			t.Fatalf("the correction worker %v", c)
		}

		u.pass(t, time.Second)
		tip := u.git(t, u.repo, "rev-parse", "refs/heads/"+b1)
		if !isAncestor(t.Context(), u.repo, theirs, tip) || u.git(t, u.repo, "show", tip+":app/README.md") != "# Main and Branch" {
			t.Fatalf("after the correction: %s", u.git(t, u.repo, "show", tip+":app/README.md"))
		}
		state = u.stored(t, t1.GetId()).GetAzima().GetMain()
		if state.GetCorrectedBy() != "" || state.GetHeld() != "" || state.GetLast().GetCorrectedBy() != c.GetId() ||
			state.GetLast().GetMainSha() != theirs {
			t.Errorf("T1's state against main after the correction: %v", state)
		}
	})

	// A branch made by the old rule, stacked on the branch of the azima it depended on and pushed, is never cut again
	// nor rewritten: its new work merges into it, and once the azima it was stacked on lands in main, main is merged
	// into it, its pushed history kept.
	t.Run("old stacked branch keeps working", func(t *testing.T) {
		t.Parallel()
		u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
		t1 := u.azima(t, u.wishID, "Base")
		t2 := u.azima(t, u.wishID, "Dependent", t1.GetCode())
		if err := u.db.Tx(t.Context(), func(tx *store.Tx) error {
			w, err := store.Get[*planv1.Wish](t.Context(), tx, u.wishID)
			if err != nil {
				return err
			}
			w.PushStrategy = planv1.PushStrategy_PUSH_STRATEGY_AZIMA
			if err := tx.Journal("test", "switched", w); err != nil {
				return err
			}
			return tx.Put(w)
		}); err != nil {
			t.Fatal(err)
		}
		b1, b2 := "djinn/T1-base", "djinn/T2-dependent"
		stack := filepath.Join(t.TempDir(), "stack")
		u.git(t, u.repo, "worktree", "add", "--quiet", "-b", b1, stack, u.main)
		writeFile(t, stack, "app/src/base.txt", "base\n")
		commitAll(t, stack, "Base work")
		u.git(t, stack, "switch", "--quiet", "-c", b2)
		writeFile(t, stack, "app/src/dependent.txt", "dependent\n")
		commitAll(t, stack, "Dependent work")
		u.git(t, stack, "switch", "--quiet", "--detach")
		u.git(t, u.repo, "push", "--quiet", "origin", b1, b2)
		pushed := u.git(t, u.repo, "rev-parse", "refs/heads/"+b2)

		// T1 lands in main, as its merged pull request does.
		u.git(t, u.other, "fetch", "--quiet", "origin", b1)
		u.git(t, u.other, "merge", "--quiet", "--no-ff", "-m", "Merge "+b1, "FETCH_HEAD")
		u.git(t, u.other, "push", "--quiet", "origin", "HEAD:refs/heads/"+u.main)
		landed := u.git(t, u.other, "rev-parse", "HEAD")

		w2 := u.finished(t, "W2", map[string]string{"app/src/more.txt": "more\n"}, func(task *planv1.Task) { task.PartOf = t2.GetId() })
		u.pass(t, 0)

		tip := u.git(t, u.repo, "rev-parse", "refs/heads/"+b2)
		if !isAncestor(t.Context(), u.repo, pushed, tip) {
			t.Fatalf("%s rewritten: its pushed %s is not in %s", b2, pushed, tip)
		}
		if got := u.get(t, w2.GetId()).GetIntegration(); got.GetBranch() != b2 || got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
			t.Errorf("W2 integration = %v, want committed into %s", got, b2)
		}
		if !isAncestor(t.Context(), u.repo, landed, tip) {
			t.Errorf("%s does not hold main's %s", b2, landed)
		}
		if got := u.stored(t, t2.GetId()).GetAzima().GetCutFrom(); got != "" {
			t.Errorf("an old branch recorded as cut from %s", got)
		}
	})

	// W196: a worker that ends done with no commit of its own is not merged: no integration record, said in its end's
	// line to the lead; the integration branch does not move, nothing is pushed.
	t.Run("no commit task is not merged", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)
		queue := &leadQueue{}
		in.h.NotifyLead(queue.notify)
		old := in.tip(t)
		task := in.spawn(t, in.wishID, "text Deleted the branches: nothing to commit.")
		in.watch(t.Context(), t, task.GetId(), 0)
		in.pass(t, 0)

		got, texts := in.integration(t, task)
		if got != nil || len(texts) > 0 {
			t.Errorf("integration %v, events %q; want none", got, texts)
		}
		want := "Djinn: " + task.GetCode() + " done, " + nothingToMerge + ". Its final note: "
		if lines := endedEvents(queue, task.GetCode()); len(lines) != 1 || !strings.HasPrefix(lines[0], want) {
			t.Errorf("ended events %q; want one, %q…", lines, want)
		}
		if tip := in.tip(t); tip != old || in.remoteTip(t, bare) != old || len(in.commits(t)) > 0 || len(in.pushes(t)) > 0 {
			t.Errorf("branch at %s, origin at %s, commits %v, pushes %v; want all at %s, none", tip, in.remoteTip(t, bare),
				in.commits(t), in.pushes(t), old)
		}
	})

	// Work whose merge changes nothing, its commits undoing each other, makes no merge commit: the branch stays where
	// it is, and the lead hears there was nothing to merge.
	t.Run("empty merge makes no commit", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		var told []string
		in.h.TellLeads(func(_ context.Context, _, line string) error {
			told = append(told, line)
			return nil
		})
		old := in.tip(t)
		w3 := in.finished(t, "W3", map[string]string{"app/src/b.txt": "b\n"})
		in.git(t, w3.GetWorktree(), "rm", "--quiet", "app/src/b.txt")
		commitAll(t, w3.GetWorktree(), "Drop b.txt again")
		in.pass(t, 0)

		got, texts := in.integration(t, w3)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != old {
			t.Errorf("W3 %v; want committed at %s", got, old)
		}
		if !slices.Contains(texts, w3.GetBranch()+" changes nothing in "+in.branch+": no merge commit") {
			t.Errorf("W3's events %q lack the merge undone", texts)
		}
		if tip := in.tip(t); tip != old || len(in.commits(t)) > 0 {
			t.Errorf("branch at %s, commits %v; want at %s, none", tip, in.commits(t), old)
		}
		if want := []string{"Djinn: W3 done, nothing to merge into " + in.branch + ". It left no final note."}; !slices.Equal(told, want) {
			t.Errorf("told %q; want %q", told, want)
		}
	})

	// An azima's branch, pushed then deleted, is never made again nor pushed for work done in it since, the azima done
	// or reopened by that work: the work waits and the lead hears why once, in one line. Named again, the branch takes
	// the work.
	t.Run("done or deleted branch not made again", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name   string
			status planv1.TaskStatus
			why    func(branch string) string
		}{
			{"done", planv1.TaskStatus_TASK_STATUS_DONE, func(b string) string {
				return "azima T1 is done: Djinn merges into its branch " + b + " and pushes it no more; reopen T1 or drop the work"
			}},
			{"reopened", planv1.TaskStatus_TASK_STATUS_PENDING, func(b string) string {
				return b + ", the branch of azima T1, is deleted: Djinn makes it no more; reopen T1 (djinn task update T1 --branch) or drop the work"
			}},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				in := integrating(t)
				in.setAzimaStrategy(t)
				bare := in.remote(t)
				queue := &leadQueue{}
				in.h.NotifyLead(queue.notify)
				t1 := in.azima(t, in.wishID, "Tenant isolation")
				branch := "djinn/T1-tenant-isolation"
				in.git(t, in.repo, "push", "--quiet", "origin", branch) // Its merge request's, closed since.
				pushed := in.git(t, bare, "rev-parse", "refs/heads/"+branch)
				in.git(t, in.repo, "branch", "--quiet", "-D", branch)
				azima := in.stored(t, t1.GetId())
				azima.Status = c.status
				putTaskInDB(t, in.db, azima)

				w2 := in.finished(t, "W2", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
				in.pass(t, 0)
				in.pass(t, 0)

				if sha := commitOf(t.Context(), in.repo, "refs/heads/"+branch); sha != "" {
					t.Errorf("%s made again at %s", branch, sha)
				}
				if got := in.git(t, bare, "rev-parse", "refs/heads/"+branch); got != pushed || len(in.pushes(t)) > 0 {
					t.Errorf("origin's %s at %s, pushes %v; want at %s, none", branch, got, in.pushes(t), pushed)
				}
				why := c.why(branch)
				if got := in.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING ||
					got.GetReason() != why || got.GetBranch() != branch {
					t.Errorf("W2 %v; want pending into %s: %s", got, branch, why)
				}
				var lines []string
				for _, e := range queue.all() {
					if strings.Contains(e.GetText(), branch) {
						lines = append(lines, e.GetText())
					}
				}
				if want := []string{"Djinn: " + why + "."}; !slices.Equal(lines, want) {
					t.Errorf("lead told %q; want %q", lines, want)
				}
				if c.status == planv1.TaskStatus_TASK_STATUS_DONE {
					return
				}
				// Named again by the developer, the branch is made from the remote's, and the work merges into it.
				if _, err := in.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
					TaskId: t1.GetId(), Branch: &branch,
				})); err != nil {
					t.Fatal(err)
				}
				in.pass(t, 0)
				if got := in.get(t, w2.GetId()).GetIntegration(); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED ||
					!isAncestor(t.Context(), in.repo, pushed, got.GetSha()) {
					t.Errorf("W2 named again %v; want committed on %s's %s", got, branch, pushed[:8])
				}
			})
		}
	})

	t.Run("live azima merges and pushes", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		bare := in.remote(t)
		t1 := in.azima(t, in.wishID, "Tenant isolation")
		branch := "djinn/T1-tenant-isolation"
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		in.pass(t, 0)

		got := in.get(t, w1.GetId()).GetIntegration()
		tip := in.git(t, in.repo, "rev-parse", "refs/heads/"+branch)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || got.GetSha() != tip {
			t.Fatalf("W1 %v; want committed at %s's %s", got, branch, tip[:8])
		}
		if remote := in.git(t, bare, "rev-parse", "refs/heads/"+branch); remote != tip {
			t.Errorf("origin's %s at %s; want pushed at %s", branch, remote, tip)
		}
	})
}

func TestDraftAzimaPR(t *testing.T) {
	t.Parallel()
	h := &Harness{}

	// Case 1: Azima with ## Goal heading in description.
	azima1 := &planv1.Task{
		Id:    "azima-1",
		Code:  "T1",
		Title: "First Azima Title",
		Description: `## Goal
One sentence describing the azima goal.

### Details
Some additional context here.`,
	}
	parts1 := []*planv1.Task{
		{Code: "W2", Title: "Second part of work", Decision: "Q70"},
		{Code: "W1", Title: "First part of work"},
	}

	title1, desc1 := h.draftAzimaPR(context.Background(), azima1, parts1)
	if want := "One sentence describing the azima goal."; title1 != want {
		t.Errorf("title = %q, want %q", title1, want)
	}
	wantDesc1 := "## Goal\n\nOne sentence describing the azima goal.\n\n### Parts\n\n- **W1**: First part of work\n- **W2**: Second part of work (decision: Q70)"
	if desc1 != wantDesc1 {
		t.Errorf("desc =\n%s\nwant:\n%s", desc1, wantDesc1)
	}

	// Case 2: Azima with Goal: prefix.
	azima2 := &planv1.Task{
		Id:          "azima-2",
		Code:        "T2",
		Title:       "Second Azima",
		Description: "Goal: Implement the core protocol.",
	}
	title2, _ := h.draftAzimaPR(context.Background(), azima2, nil)
	if want := "Implement the core protocol."; title2 != want {
		t.Errorf("title = %q, want %q", title2, want)
	}

	// Case 3: Empty description falls back to title.
	azima3 := &planv1.Task{
		Id:    "azima-3",
		Code:  "T3",
		Title: "Fallback to Title",
	}
	title3, _ := h.draftAzimaPR(context.Background(), azima3, nil)
	if want := "Fallback to Title"; title3 != want {
		t.Errorf("title = %q, want %q", title3, want)
	}
}

func TestAzimaPRBase(t *testing.T) {
	t.Parallel()

	t.Run("is main", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		repo := gitRepo(t)
		main := plan.CheckedOutBranch(ctx, repo)
		recorded := &planv1.Task{
			Id: "t1", Code: "T1", Title: "Alpha", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
			Azima: &planv1.Azima{Branch: "djinn/T1-alpha", BaseBranch: "trunk", CutFrom: "0123456789abcdef0123456789abcdef01234567"},
		}
		if got := azimaPRBase(ctx, repo, plan.Settings{}, recorded); got != "trunk" {
			t.Errorf("recorded base = %q, want trunk", got)
		}
		old := &planv1.Task{
			Id: "t2", Code: "T2", Title: "Beta", Kind: planv1.TaskKind_TASK_KIND_AZIMA, DependsOn: []string{"t1"},
			Azima: &planv1.Azima{BaseBranch: "djinn/T1-alpha"},
		}
		if got := azimaPRBase(ctx, repo, plan.Settings{}, old); got != main {
			t.Errorf("base of an azima not cut = %q, want main %q", got, main)
		}
	})

	// W149: the base of a PR and the main of an azima go through the reference branch the settings name, never main or
	// master in its place; with no reference at all, no PR base is made up.
	t.Run("main fallbacks follow reference", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		repo := gitRepo(t)
		for _, args := range [][]string{
			{"branch", "-M", "master"},
			{"remote", "add", "origin", "https://example.com/repo.git"},
			{"update-ref", "refs/remotes/origin/dev", "HEAD"},
		} {
			if _, err := git(ctx, repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		sha := commitOf(ctx, repo, "HEAD")
		dev := plan.Settings{MainBranch: "dev"}
		if name, got, err := resolveMainSha(ctx, repo, dev); err != nil || name != "dev" || got != sha {
			t.Errorf("main_branch dev, only on the remote: %s %s %v; want dev at %s, not the local master", name, got, err, sha)
		}
		azima := &planv1.Task{Id: "t1", Code: "T1", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Azima: &planv1.Azima{}}
		if got := azimaPRBase(ctx, repo, dev, azima); got != "dev" {
			t.Errorf("PR base with main_branch dev = %q", got)
		}
		if name, _, err := resolveMainSha(ctx, repo, plan.Settings{}); err != nil || name != "master" {
			t.Errorf("no reference on the remote: %s %v; want the local master", name, err)
		}
		if _, err := git(ctx, repo, "branch", "-M", "trunk"); err != nil {
			t.Fatal(err)
		}
		if got := azimaPRBase(ctx, repo, plan.Settings{}, azima); got != "" {
			t.Errorf("PR base with no reference branch = %q, want none", got)
		}
	})
}

func TestAzimaPRPass(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	if _, code, err := (&Harness{}).runCmd(t.Context(), t.TempDir(), []string{"true"}); err != nil || code != 0 {
		t.Fatalf("runCmd default: code=%d, err=%v", code, err)
	}

	// When an azima branch is pushed and its parts are done and green, Djinn proposes opening its PR with exact title,
	// description, and base, then waits.
	t.Run("proposal flow", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		forge := &fakeForge{created: "https://github.com/acme/app/pull/41"}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()

		t1 := in.azima(t, in.wishID, "One Alpha")
		t1.Description = "## Goal\nImplement feature alpha.\n"
		putTaskInDB(t, in.db, t1)

		part1 := func(task *planv1.Task) {
			task.PartOf = t1.GetId()
			task.Decision = "Q70"
		}

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
		in.pass(t, 0)

		b1 := "djinn/T1-one-alpha"
		in.git(t, in.repo, "push", "origin", b1)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

		// Integration pass detects pushed azima with finished parts and proposes PR.
		in.pass(t, 0)

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var prQ *planv1.Question
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
				prQ = q
				break
			}
		}
		if prQ == nil {
			t.Fatalf("no PR proposal question asked; found: %v", questions)
		}
		if !prQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if want := fmt.Sprintf("Open a pull request for T1 to %s?", in.branch); prQ.GetText() != want {
			t.Errorf("question text = %q, want %q", prQ.GetText(), want)
		}
		if prQ.GetTaskId() != t1.GetId() {
			t.Errorf("question task_id = %q, want %q", prQ.GetTaskId(), t1.GetId())
		}
		if !strings.Contains(prQ.GetContext(), "**Title**: Implement feature alpha.") {
			t.Errorf("context lacks title: %q", prQ.GetContext())
		}
		if !strings.Contains(prQ.GetContext(), "**Base**: "+in.branch) {
			t.Errorf("context lacks base: %q", prQ.GetContext())
		}
		if !strings.Contains(prQ.GetContext(), "## Goal\n\nImplement feature alpha.") {
			t.Errorf("context lacks goal: %q", prQ.GetContext())
		}
		if !strings.Contains(prQ.GetContext(), "- **W1**: ") || !strings.Contains(prQ.GetContext(), "(decision: Q70)") {
			t.Errorf("context lacks parts: %q", prQ.GetContext())
		}
		if !slices.Contains(prQ.GetOptions(), "Open pull request") || !slices.Contains(prQ.GetOptions(), "Not yet") {
			t.Errorf("options = %v, want Open pull request and Not yet", prQ.GetOptions())
		}

		// The window shows it proposed.
		if got := in.get(t, t1.GetId()).GetAzima().GetPr().GetState(); got != planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED {
			t.Errorf("PR state = %v, want proposed", got)
		}

		// Answering Choice A opens it as a draft, from the azima's branch into its base, with the drafted title and body.
		did := in.answer(t, prQ, planv1.Choice_CHOICE_A)
		if want := "Djinn opened draft PR #41: https://github.com/acme/app/pull/41"; did != want {
			t.Errorf("what Djinn did: %q, want %q", did, want)
		}
		if want := "gh pr create --draft --head " + b1 + " --base " + in.branch + " --title Implement feature alpha."; !slices.ContainsFunc(forge.called(), func(c string) bool {
			return strings.HasPrefix(c, want)
		}) {
			t.Errorf("forge calls %q, want one beginning with %q", forge.called(), want)
		}
		if got := in.get(t, t1.GetId()).GetAzima().GetPr(); got.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN ||
			got.GetUrl() != "https://github.com/acme/app/pull/41" {
			t.Errorf("PR recorded %v, want open at its address", got)
		}
	})

	// When an azima PR is mergeable and green, Djinn asks the developer it is ready.
	t.Run("ready question", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		b1 := "djinn/T1-one-alpha"
		forge := &fakeForge{
			views: map[string]string{
				b1: `{"number":42,"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","url":"https://github.com/acme/app/pull/42"}`,
			},
			checks: map[string]string{
				"42": `[{"name":"test","bucket":"pass"},{"name":"lint","bucket":"pass"}]`,
			},
		}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()

		t1 := in.azima(t, in.wishID, "One Alpha")
		part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
		in.pass(t, 0)

		in.git(t, in.repo, "push", "origin", b1)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

		in.pass(t, 0)

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var readyQ *planv1.Question
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "ready to merge") {
				readyQ = q
				break
			}
		}
		if readyQ == nil {
			t.Fatalf("no ready to merge question; questions: %v", questions)
		}
		if !readyQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if want := "Pull request #42 for T1 is ready to merge?"; readyQ.GetText() != want {
			t.Errorf("question text = %q, want %q", readyQ.GetText(), want)
		}
		if readyQ.GetTaskId() != t1.GetId() {
			t.Errorf("question task_id = %q, want %q", readyQ.GetTaskId(), t1.GetId())
		}
		if !slices.Contains(readyQ.GetOptions(), "Ready to merge") || !slices.Contains(readyQ.GetOptions(), "Not yet") {
			t.Errorf("options = %v, want Ready to merge and Not yet", readyQ.GetOptions())
		}
		if got := in.get(t, t1.GetId()).GetAzima().GetPr(); got.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN ||
			got.GetUrl() != "https://github.com/acme/app/pull/42" {
			t.Errorf("PR = %v, want open at its URL", got)
		}

		// Answering Choice A notes ready to merge.
		did := in.answer(t, readyQ, planv1.Choice_CHOICE_A)
		if did != "Djinn noted: ready to merge" {
			t.Errorf("what Djinn did: %q", did)
		}
	})

	// Each azima's pull request targets main from the start and follows its branch: another one merged leaves it as it
	// is, never re-targeted; the merged one closes its azima, shown merged.
	t.Run("kept never retargeted", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		b1, b2 := "djinn/T1-base", "djinn/T2-second"
		forge := &fakeForge{
			views:  map[string]string{},
			checks: map[string]string{"102": `[{"name":"test","bucket":"pending"}]`},
		}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()

		t1 := in.azima(t, in.wishID, "Base")
		t2 := in.azima(t, in.wishID, "Second")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = t2.GetId() })
		in.pass(t, 0)
		for _, b := range []string{b1, b2} {
			in.git(t, in.repo, "push", "origin", b)
			in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b, in.git(t, in.repo, "rev-parse", "refs/heads/"+b))
		}
		b2Sha := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

		forge.mu.Lock()
		forge.views[b1] = fmt.Sprintf(`{"number":101,"state":"MERGED","baseRefName":%q,"mergeable":"UNKNOWN","url":"https://github.com/acme/app/pull/101"}`, in.branch)
		forge.views[b2] = fmt.Sprintf(`{"number":102,"state":"OPEN","baseRefName":%q,"mergeable":"MERGEABLE","url":"https://github.com/acme/app/pull/102"}`, in.branch)
		forge.mu.Unlock()
		in.git(t, in.repo, "merge", "--quiet", "--no-ff", b1, "-m", "Merge "+b1+" into main")

		in.pass(t, 0)

		if got := in.get(t, t1.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE ||
			got.GetAzima().GetPr().GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_MERGED {
			t.Fatalf("T1 = %v, PR %v; want done, merged", got.GetStatus(), got.GetAzima().GetPr())
		}
		if got := in.get(t, t2.GetId()).GetAzima().GetPr(); got.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN ||
			got.GetUrl() != "https://github.com/acme/app/pull/102" {
			t.Errorf("T2's PR = %v, want open at its URL", got)
		}
		if slices.ContainsFunc(forge.called(), func(c string) bool { return strings.Contains(c, "pr edit") }) {
			t.Errorf("a pull request was re-targeted: %v", forge.called())
		}
		if got := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2); got != b2Sha {
			t.Errorf("%s moved to %s when T1 merged, want it at %s", b2, got, b2Sha)
		}
	})

	// Verifies the ready-to-merge question with GitLab (glab), and that a merge request merged closes its azima without
	// re-targeting any other.
	t.Run("glab ready", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		in.remote(t)
		b1, b2 := "djinn/T1-base", "djinn/T2-second"
		forge := &fakeForge{views: map[string]string{}}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()

		// Set project remote to GitLab, and also verify detectForge's git remote get-url fallback when project is nil.
		err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
			p, err := store.Get[*planv1.Project](t.Context(), tx, in.projectID)
			if err != nil {
				return err
			}
			p.Remote = "https://gitlab.example.com/acme/project.git"
			if err := tx.Journal("test", "remote", p); err != nil {
				return err
			}
			return tx.Put(p)
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := detectForge(t.Context(), in.repo, nil); got != "gh" {
			t.Errorf("detectForge fallback = %q, want gh", got)
		}

		t1 := in.azima(t, in.wishID, "Base")
		t2 := in.azima(t, in.wishID, "Second")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = t2.GetId() })
		in.pass(t, 0)
		for _, b := range []string{b1, b2} {
			in.git(t, in.repo, "push", "origin", b)
			in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b, in.git(t, in.repo, "rev-parse", "refs/heads/"+b))
		}

		// MR !201 is OPEN, mergeable, and green pipeline.
		forge.mu.Lock()
		forge.views[b1] = `{"iid":201,"state":"opened","target_branch":"main","web_url":"https://gitlab.example.com/acme/project/-/merge_requests/201","detailed_merge_status":"mergeable","head_pipeline":{"id":99,"status":"success"}}`
		forge.mu.Unlock()

		in.pass(t, 0)

		// Ready to merge question asked for MR !201.
		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var readyQ *planv1.Question
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "ready to merge") {
				readyQ = q
				break
			}
		}
		if readyQ == nil {
			t.Fatalf("no ready to merge question for MR !201; questions: %v", questions)
		}
		if want := "Pull request #201 for T1 is ready to merge?"; readyQ.GetText() != want {
			t.Errorf("question text = %q, want %q", readyQ.GetText(), want)
		}
		if got := in.get(t, t1.GetId()).GetAzima().GetPr().GetUrl(); got != "https://gitlab.example.com/acme/project/-/merge_requests/201" {
			t.Errorf("MR URL = %q", got)
		}

		// Now MR !201 is MERGED, and MR !202 for T2 is OPEN, targeting main.
		forge.mu.Lock()
		forge.views[b1] = `{"iid":201,"state":"merged","target_branch":"main","detailed_merge_status":"not_applicable"}`
		forge.views[b2] = `{"iid":202,"state":"opened","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"id":100,"status":"running"}}`
		forge.mu.Unlock()
		in.git(t, in.repo, "merge", "--quiet", "--no-ff", b1, "-m", "Merge "+b1+" into main")

		in.pass(t, 0)

		if in.get(t, t1.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("t1 status = %v, want DONE", in.get(t, t1.GetId()).GetStatus())
		}
		if slices.ContainsFunc(forge.called(), func(c string) bool { return strings.Contains(c, "mr update") }) {
			t.Errorf("a merge request was re-targeted: %v", forge.called())
		}
	})
}
