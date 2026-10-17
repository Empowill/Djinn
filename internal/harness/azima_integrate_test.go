package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

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

// TestAzimaIndependentBranchesFromMain verifies that two independent azimas get two branches created from main,
// and their tasks integrate into their respective azima branches.
func TestAzimaIndependentBranchesFromMain(t *testing.T) {
	testx.Portable(t)
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
	if task1.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("w1 state = %v, want COMMITTED", task1.GetIntegration().GetState())
	}
	if task1.GetIntegration().GetBranch() != b1 {
		t.Fatalf("w1 branch = %s, want %s", task1.GetIntegration().GetBranch(), b1)
	}
	if task1.GetIntegration().GetSha() != sha1 {
		t.Fatalf("w1 sha = %s, want %s", task1.GetIntegration().GetSha(), sha1)
	}

	task2 := in.get(t, w2.GetId())
	if task2.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("w2 state = %v, want COMMITTED", task2.GetIntegration().GetState())
	}
	if task2.GetIntegration().GetBranch() != b2 {
		t.Fatalf("w2 branch = %s, want %s", task2.GetIntegration().GetBranch(), b2)
	}
	if task2.GetIntegration().GetSha() != sha2 {
		t.Fatalf("w2 sha = %s, want %s", task2.GetIntegration().GetSha(), sha2)
	}
}

// TestAzimaBranchCutFromMainWhenMade: under the per-azima strategy an azima's branch is cut from main's commit the
// moment the azima is made, and recorded on it: its branch, the main it lands in, the commit. Main moving on later
// does not cut it again: its work merges into the branch as cut, which keeps its name once the title changes.
func TestAzimaBranchCutFromMainWhenMade(t *testing.T) {
	testx.Portable(t)
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
}

// TestAzimaBranchCutWhenItsFirstPartStarts: an azima made before its wish was switched to per azima has no branch;
// its first part to start cuts it from main, recorded, and starts from it.
func TestAzimaBranchCutWhenItsFirstPartStarts(t *testing.T) {
	testx.Portable(t)
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
}

// TestAzimaBranchKeepsUpWithMain: an azima's branch keeps up with main as a wish's integration branch does, by the
// same merge_main rules: main merged into it, never a rebase, its state recorded on the azima. One that holds
// nothing of its own beyond main moves to main's commit.
func TestAzimaBranchKeepsUpWithMain(t *testing.T) {
	testx.Portable(t)
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
}

// TestAzimaBranchMainConflictStartsACorrection: main conflicting with an azima's branch starts a correction worker
// as for a wish's, part of the azima: its work merges into the azima's branch, which then holds main, recorded on the
// azima as merged by it.
func TestAzimaBranchMainConflictStartsACorrection(t *testing.T) {
	testx.Portable(t)
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
}

// TestAzimaOldStackedBranchKeepsWorking: a branch made by the old rule, stacked on the branch of the azima it depended
// on and pushed, is never cut again nor rewritten: its new work merges into it, and once the azima it was stacked on
// lands in main, main is merged into it, its pushed history kept.
func TestAzimaOldStackedBranchKeepsWorking(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
	// Made per wish, T2 after T1, then switched before Djinn refused that.
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
}

// TestPerAzimaRefusesAzimaDependencies: in a wish that pushes per azima, no azima waits for another: spawning one
// after another, spawning work of one after work of another, setting such a dependency (depend, update), grouping
// such work into an azima, or syncing a plan file whose after line names another azima, are refused, naming them. Work waiting for work
// of its own azima is as usual.
func TestPerAzimaRefusesAzimaDependencies(t *testing.T) {
	testx.Portable(t)
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
}

// TestAzimaTaskWithNoAzimaUsesWishBranch verifies that a rare task with no azima integrates into the wish's own
// integration branch.
func TestAzimaTaskWithNoAzimaUsesWishBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.setAzimaStrategy(t)

	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}) // PartOf is empty.
	in.pass(t, 0)

	w1Task := in.get(t, w1.GetId())
	if w1Task.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Fatalf("w1 state = %v, want COMMITTED", w1Task.GetIntegration().GetState())
	}
	if w1Task.GetIntegration().GetBranch() != in.branch {
		t.Fatalf("w1 branch = %s, want %s (wish branch)", w1Task.GetIntegration().GetBranch(), in.branch)
	}
	if w1Task.GetIntegration().GetSha() != in.tip(t) {
		t.Fatalf("w1 sha = %s, want tip %s", w1Task.GetIntegration().GetSha(), in.tip(t))
	}
}

// TestAzimaPerWishModeUnchanged verifies that when push strategy is per-wish (the default), tasks integrate into
// the wish branch as before, and no azima branches are created.
func TestAzimaPerWishModeUnchanged(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)

	t1 := in.azima(t, in.wishID, "First")
	t2 := in.azima(t, in.wishID, "Second")
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)

	in.pass(t, 0)

	w1Task := in.get(t, w1.GetId())
	w2Task := in.get(t, w2.GetId())

	if w1Task.GetIntegration().GetBranch() != in.branch {
		t.Fatalf("w1 branch = %s, want wish branch %s", w1Task.GetIntegration().GetBranch(), in.branch)
	}
	if w2Task.GetIntegration().GetBranch() != in.branch {
		t.Fatalf("w2 branch = %s, want wish branch %s", w2Task.GetIntegration().GetBranch(), in.branch)
	}

	// Azima branches were not created.
	if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", "refs/heads/djinn/T1-first"); err == nil {
		t.Fatal("djinn/T1-first branch should not exist in per-wish mode")
	}
	if _, err := git(t.Context(), in.repo, "rev-parse", "--verify", "refs/heads/djinn/T2-second"); err == nil {
		t.Fatal("djinn/T2-second branch should not exist in per-wish mode")
	}
}
