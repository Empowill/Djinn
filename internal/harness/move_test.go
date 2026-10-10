package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestWorkerPromptMoveRule verifies that every worker's first prompt (next to ChecksBrief)
// includes the move rule instructing the worker to ask moves only the developer can make
// with `djinn question ask`.
func TestWorkerPromptMoveRule(t *testing.T) {
	testx.Portable(t)

	// 1. With checks: rule is appended next to ChecksBrief.
	r1 := &run{
		task:   &planv1.Task{Branch: "w1-branch", Provider: planv1.Provider_PROVIDER_CLAUDE},
		checks: "Djinn checks this project's work with `go tool task test`.",
	}
	got1 := briefed(r1, "do the work", false)
	wantChecks := "Djinn checks this project's work with `go tool task test`. " + workerMoveRule
	if !strings.Contains(got1, wantChecks) {
		t.Errorf("briefed with checks:\ngot: %q\nwant to contain: %q", got1, wantChecks)
	}

	// 2. Real provider without checks: rule is appended.
	for _, provider := range []planv1.Provider{
		planv1.Provider_PROVIDER_CLAUDE,
		planv1.Provider_PROVIDER_ANTIGRAVITY,
		planv1.Provider_PROVIDER_CODEX,
	} {
		r := &run{
			task: &planv1.Task{Branch: "w-branch", Provider: provider},
		}
		got := briefed(r, "prompt text", false)
		if !strings.HasSuffix(got, "\n\n"+workerMoveRule) {
			t.Errorf("provider %s prompt:\ngot: %q\nwant suffix: %q", provider, got, "\n\n"+workerMoveRule)
		}
	}

	// 3. Read-only or no branch: prompt left as is.
	rReadOnly := &run{
		task:   &planv1.Task{Branch: "w-branch", Provider: planv1.Provider_PROVIDER_CLAUDE},
		checks: "some checks",
	}
	if got := briefed(rReadOnly, "prompt text", true); got != "prompt text" {
		t.Errorf("read-only got %q, want %q", got, "prompt text")
	}

	rNoBranch := &run{
		task:   &planv1.Task{Branch: "", Provider: planv1.Provider_PROVIDER_CLAUDE},
		checks: "some checks",
	}
	if got := briefed(rNoBranch, "prompt text", false); got != "prompt text" {
		t.Errorf("no branch got %q, want %q", got, "prompt text")
	}
}

// TestMergeMoveTag: a worker's merge creates a local tag. Djinn raises a question
// with Move: true to push the tag, the lead line names the move, and answering Choice A pushes it.
func TestMergeMoveTag(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	bare := in.remote(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	// Create local tag v1.0.0 in the worktree on W1's commit before merge
	in.git(t, w1.GetWorktree(), "tag", "v1.0.0")

	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var tagQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "v1.0.0") {
			tagQ = q
			break
		}
	}
	if tagQ == nil {
		t.Fatalf("no question asked for tag v1.0.0; questions: %v", questions)
	}
	if !tagQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	if want := "Push tag v1.0.0 to origin?"; tagQ.GetText() != want {
		t.Errorf("question text %q, want %q", tagQ.GetText(), want)
	}

	// Verify lead was told with move named
	if len(told) == 0 || !strings.Contains(told[len(told)-1], ": push tag v1.0.0") {
		t.Errorf("lead told %v; want to contain ': push tag v1.0.0'", told)
	}

	// Answering Choice A pushes the tag to bare remote
	did := in.answer(t, tagQ, planv1.Choice_CHOICE_A)
	if did != "Djinn pushed tag v1.0.0" {
		t.Errorf("what Djinn did: %q, want 'Djinn pushed tag v1.0.0'", did)
	}
	if tagSha := in.git(t, bare, "rev-parse", "refs/tags/v1.0.0"); tagSha == "" {
		t.Errorf("tag v1.0.0 not found on remote")
	}
}

// TestMergeMoveReleaseFiles: release workflow files changed in worker's merge.
// Djinn raises a question to start release dry run, and lead line names the move.
func TestMergeMoveReleaseFiles(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	in.finished(t, "W1", map[string]string{".github/workflows/release.yml": "name: release\n"})
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var relQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(strings.ToLower(q.GetText()), "release") {
			relQ = q
			break
		}
	}
	if relQ == nil {
		t.Fatalf("no question asked for release files changed; questions: %v", questions)
	}
	if !relQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	if want := "Start release dry run?"; relQ.GetText() != want {
		t.Errorf("question text %q, want %q", relQ.GetText(), want)
	}

	// Verify lead was told
	if len(told) == 0 || !strings.Contains(told[len(told)-1], ": start a release") {
		t.Errorf("lead told %v; want to contain ': start a release'", told)
	}

	did := in.answer(t, relQ, planv1.Choice_CHOICE_A)
	if did != "Djinn noted: start release dry run" {
		t.Errorf("what Djinn did: %q", did)
	}
}

// TestMergeMoveNeedsBox: worker's commit introduces a needs: box.
// Djinn raises a question to check on that machine, and lead line names the move.
func TestMergeMoveNeedsBox(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	in.finished(t, "W1", map[string]string{"README.md": "# Docs\n- [ ] verify UI (needs: a Mac)\n"})
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var checkQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "a Mac") {
			checkQ = q
			break
		}
	}
	if checkQ == nil {
		t.Fatalf("no question asked for needs: box; questions: %v", questions)
	}
	if !checkQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	if !strings.Contains(checkQ.GetText(), "a Mac") {
		t.Errorf("question text %q; want to contain 'a Mac'", checkQ.GetText())
	}

	// Verify lead was told
	if len(told) == 0 || !strings.Contains(told[len(told)-1], "a Mac") {
		t.Errorf("lead told %v; want to mention 'a Mac'", told)
	}
}

// TestMergeMoveWorkerAlreadyAskedDeduplicated: when the worker already asked the move question,
// Djinn does not ask a duplicate, and the lead line still names the move.
func TestMergeMoveWorkerAlreadyAskedDeduplicated(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.git(t, w1.GetWorktree(), "tag", "v2.5.0")

	// Worker asks the move question before ending
	askQ := &planv1.Question{
		Id:         store.NewID(),
		WishId:     in.wishID,
		TaskId:     w1.GetId(),
		Text:       "Push tag v2.5.0 to origin?",
		Options:    []string{"Push tag v2.5.0", "Skip"},
		Move:       true,
		CreateTime: timestamppb.Now(),
	}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "ask", askQ); err != nil {
			return err
		}
		return plan.Ask(t.Context(), tx, askQ)
	}); err != nil {
		t.Fatal(err)
	}

	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 1 {
		t.Fatalf("expected 1 question (deduplicated), got %d: %v", len(questions), questions)
	}

	// Lead line still names the move
	if len(told) == 0 || !strings.Contains(told[len(told)-1], "push tag v2.5.0") {
		t.Errorf("lead told %v; want to contain 'push tag v2.5.0'", told)
	}
}

// TestComputedMoveUninstalledBuild: an integrated build that has not been installed
// for longer than uninstalledDelay generates an "Install and restart <sha>?" question.
func TestComputedMoveUninstalledBuild(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "v2\n"})
	in.pass(t, 0)

	// Immediately after merge: not delayed yet, no install question
	questions, _ := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			t.Fatalf("install question asked too soon: %v", q)
		}
	}

	// Advance clock beyond 5 minutes
	in.pass(t, 10*time.Minute)

	questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	var installQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			installQ = q
			break
		}
	}
	if installQ == nil {
		t.Fatalf("no install question asked after delay; questions: %v", questions)
	}
	if !installQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}

	// Answering Choice A installs the build
	did := in.answer(t, installQ, planv1.Choice_CHOICE_A)
	if !strings.HasPrefix(did, "Djinn installed build") {
		t.Errorf("what Djinn did: %q; want prefix 'Djinn installed build'", did)
	}

	// Next pass doesn't re-ask
	in.pass(t, time.Minute)
	installCount := 0
	questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			installCount++
		}
	}
	if installCount != 1 {
		t.Errorf("expected exactly 1 install question (deduplicated), got %d", installCount)
	}
}

// TestComputedMoveAzimaProof: an azima whose parts are finished and has proof needs
// enters AZIMA_STATE_AWAITING_PROOF and raises a "Check T01 on <needs>?" question.
// Answering "Verified" marks the azima done.
func TestComputedMoveAzimaProof(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	azima := in.azima(t, in.wishID, "NativeMac")
	azima.ProofNeeds = []*planv1.ProofNeed{
		{Box: "Window opens in Cocoa", Needs: "a Mac", Provers: []planv1.Prover{planv1.Prover_PROVER_MAC}},
	}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", azima); err != nil {
			return err
		}
		return tx.Put(azima)
	}); err != nil {
		t.Fatal(err)
	}

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "mac ui\n"}, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var proofQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "on a Mac") {
			proofQ = q
			break
		}
	}
	if proofQ == nil {
		t.Fatalf("no proof question asked; questions: %v", questions)
	}
	if !proofQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	if want := "Check " + azima.GetCode() + " on a Mac?"; proofQ.GetText() != want {
		t.Errorf("question text %q, want %q", proofQ.GetText(), want)
	}
	if !slices.Equal(proofQ.GetOptions(), []string{"Verified", "Not yet"}) {
		t.Errorf("options %v, want ['Verified', 'Not yet']", proofQ.GetOptions())
	}
	if !strings.Contains(proofQ.GetContext(), "Window opens in Cocoa (needs: a Mac)") {
		t.Errorf("context lacks box details: %q", proofQ.GetContext())
	}

	// Answering "Verified" marks the azima done
	did := in.answer(t, proofQ, planv1.Choice_CHOICE_A)
	if did != "Djinn marked "+azima.GetCode()+" done" {
		t.Errorf("what Djinn did: %q", did)
	}

	got := in.get(t, azima.GetId())
	if got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("azima status = %v, want DONE", got.GetStatus())
	}
}

// TestComputedMoveOpenPR: when integration branch is pushed to origin,
// ahead of origin/main with every check green, and wish is settled,
// Djinn raises "Open a pull request to main?".
func TestComputedMoveOpenPR(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	bare := in.remote(t)

	// main branch at the initial commit
	initialSha := in.tip(t)
	in.git(t, in.repo, "branch", "main", initialSha)
	in.git(t, in.repo, "push", "origin", "main")

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\nmain_branch: \"main\"\n")

	// Azima ends so auto mode pushes the branch to origin
	azima := in.azima(t, in.wishID, "Feature")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	in.pass(t, 0)

	// Branch should be pushed to bare origin
	if in.remoteTip(t, bare) == initialSha {
		t.Fatalf("branch was not pushed to origin")
	}

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
		t.Fatalf("no PR question asked; questions: %v", questions)
	}
	if !prQ.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	if want := "Open a pull request to main?"; prQ.GetText() != want {
		t.Errorf("question text %q, want %q", prQ.GetText(), want)
	}
	if !strings.Contains(prQ.GetContext(), "### ") {
		t.Errorf("context lacks drafted PR title/body: %q", prQ.GetContext())
	}

	// Answering Choice A notes opening the PR
	did := in.answer(t, prQ, planv1.Choice_CHOICE_A)
	if did != "Djinn noted: open pull request" {
		t.Errorf("what Djinn did: %q", did)
	}
}
