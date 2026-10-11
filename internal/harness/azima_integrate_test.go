package harness

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
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

// TestAzimaIndependentBranchesFromMain verifies that two independent azimas get two branches created from main,
// and their tasks integrate into their respective azima branches.
func TestAzimaIndependentBranchesFromMain(t *testing.T) {
	testx.Portable(t)
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

// TestAzimaDependentBranchStacked verifies that a dependent azima starts from the branch of the azima it depends on
// while that dependency is not merged into main.
func TestAzimaDependentBranchStacked(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	b1 := "djinn/T1-base"
	sha1 := in.git(t, in.repo, "rev-parse", "refs/heads/"+b1)

	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)

	b2 := "djinn/T2-dependent"
	sha2 := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	// sha1 (tip of base azima) must be an ancestor of sha2 (dependent branch tip).
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", sha1, sha2); err != nil {
		t.Fatalf("%s is not an ancestor of %s: %v", b1, b2, err)
	}

	task2 := in.get(t, w2.GetId())
	if task2.GetIntegration().GetBranch() != b2 {
		t.Fatalf("w2 branch = %s, want %s", task2.GetIntegration().GetBranch(), b2)
	}
	if task2.GetIntegration().GetSha() != sha2 {
		t.Fatalf("w2 sha = %s, want %s", task2.GetIntegration().GetSha(), sha2)
	}
}

// TestAzimaDependentStartsFromMainWhenBaseMerged verifies that when the base azima is already merged into main,
// a later dependent azima starts from main.
func TestAzimaDependentStartsFromMainWhenBaseMerged(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)

	t1 := in.azima(t, in.wishID, "Base")
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	b1 := "djinn/T1-base"
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	newMainSha := in.tip(t)

	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)

	b2 := "djinn/T2-dependent"
	sha2 := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	// Dependent branch starts from main (which includes the merge commit newMainSha).
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", newMainSha, sha2); err != nil {
		t.Fatalf("merged main %s is not an ancestor of %s: %v", newMainSha, b2, err)
	}

	task2 := in.get(t, w2.GetId())
	if task2.GetIntegration().GetBranch() != b2 {
		t.Fatalf("w2 branch = %s, want %s", task2.GetIntegration().GetBranch(), b2)
	}
}

// TestAzimaFollowMainRebaseUnpushed verifies that an unpushed stacked branch rebases on main when the base azima is
// merged into main, and task integration SHAs are updated.
func TestAzimaFollowMainRebaseUnpushed(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)

	b2 := "djinn/T2-dependent"
	w2OldSha := in.get(t, w2.GetId()).GetIntegration().GetSha()

	// Merge base azima T1 into main.
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	newMainSha := in.tip(t)

	// Follow main in integration pass.
	in.pass(t, 0)

	sha2Rebased := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	// b2 now contains the new mainSha.
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", newMainSha, sha2Rebased); err != nil {
		t.Fatalf("new main %s is not an ancestor of rebased %s: %v", newMainSha, b2, err)
	}

	// Task W2's integration SHA was updated.
	w2New := in.get(t, w2.GetId())
	if w2New.GetIntegration().GetSha() == w2OldSha {
		t.Fatal("w2 integration sha was not updated after rebase")
	}
	if w2New.GetIntegration().GetSha() != sha2Rebased {
		t.Fatalf("w2 sha = %s, want %s", w2New.GetIntegration().GetSha(), sha2Rebased)
	}
}

// TestAzimaFollowMainRebaseUnpushedWithRemoteAskMode verifies that when a remote exists but the branch was never pushed
// (e.g. ask mode where push has not been approved), Djinn rebases the unpushed branch onto main.
func TestAzimaFollowMainRebaseUnpushedWithRemoteAskMode(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	if _, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: in.wishID, PushMode: planv1.PushMode_PUSH_MODE_ASK,
	})); err != nil {
		t.Fatal(err)
	}

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)

	b2 := "djinn/T2-dependent"
	w2OldSha := in.get(t, w2.GetId()).GetIntegration().GetSha()

	// Merge base azima T1 into main.
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	newMainSha := in.tip(t)

	// Follow main in integration pass.
	in.pass(t, 0)

	sha2Rebased := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	// b2 now contains the new mainSha.
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", newMainSha, sha2Rebased); err != nil {
		t.Fatalf("new main %s is not an ancestor of rebased %s: %v", newMainSha, b2, err)
	}

	// Task W2's integration SHA was updated.
	w2New := in.get(t, w2.GetId())
	if w2New.GetIntegration().GetSha() == w2OldSha {
		t.Fatal("w2 integration sha was not updated after rebase")
	}
	if w2New.GetIntegration().GetSha() != sha2Rebased {
		t.Fatalf("w2 sha = %s, want %s", w2New.GetIntegration().GetSha(), sha2Rebased)
	}
}

// TestAzimaFollowMainMergePushed verifies that an already pushed stacked branch merges main without force when the
// base azima is merged into main.
func TestAzimaFollowMainMergePushed(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)

	b2 := "djinn/T2-dependent"
	// Push b2 to origin and update remote tracking ref.
	in.git(t, in.repo, "push", "origin", b2)
	b2PushedSha := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b2, b2PushedSha)

	// Merge base azima T1 into main.
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	newMainSha := in.tip(t)

	// Follow main in integration pass.
	in.pass(t, 0)

	sha2Merged := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	// b2 contains newMainSha.
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", newMainSha, sha2Merged); err != nil {
		t.Fatalf("new main %s is not an ancestor of %s: %v", newMainSha, b2, err)
	}

	// b2PushedSha is still an ancestor: no force-push, merge instead of rebase.
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", b2PushedSha, sha2Merged); err != nil {
		t.Fatalf("previously pushed tip %s is not an ancestor of %s (history rewritten): %v", b2PushedSha, b2, err)
	}

	// Tip commit message confirms a merge.
	lastMsg := in.git(t, in.repo, "log", "-n", "1", "--format=%s", sha2Merged)
	if !strings.Contains(lastMsg, "Merge branch") {
		t.Fatalf("expected merge commit message, got %q", lastMsg)
	}
}

// TestAzimaFollowMainConflictRaisesQuestion verifies that a conflict when following main raises a question to the
// developer with the conflict icon and options.
func TestAzimaFollowMainConflictRaisesQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.noCorrection(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/conflict.txt": "from T1\n"}, part1)
	in.pass(t, 0)

	w2 := in.finished(t, "W2", map[string]string{"app/src/conflict.txt": "from T2\n"}, part2)
	in.pass(t, 0)

	// Merge T1 into main and add conflicting edit on main.
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	writeFile(t, in.repo, "app/src/conflict.txt", "from main\n")
	in.git(t, in.repo, "commit", "-a", "-m", "Conflicting change on main")

	// Follow main fails with conflict.
	in.pass(t, 0)

	w2Task := in.get(t, w2.GetId())
	if w2Task.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT {
		t.Fatalf("w2 state = %v, want CONFLICT", w2Task.GetIntegration().GetState())
	}
	qid := w2Task.GetIntegration().GetQuestionId()
	if qid == "" {
		t.Fatal("expected question_id on conflicted task, got empty")
	}

	q, err := store.Get[*planv1.Question](t.Context(), in.db, qid)
	if err != nil {
		t.Fatalf("get question %s: %v", qid, err)
	}
	if q.GetIcon() != "🔀" {
		t.Fatalf("question icon = %s, want 🔀", q.GetIcon())
	}
	if !slices.Contains(q.GetOptions(), retryOption) {
		t.Fatalf("question options %v do not contain retryOption", q.GetOptions())
	}
}

// TestAzimaTaskWithNoAzimaUsesWishBranch verifies that a rare task with no azima integrates into the wish's own
// integration branch.
func TestAzimaTaskWithNoAzimaUsesWishBranch(t *testing.T) {
	testx.Portable(t)
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
