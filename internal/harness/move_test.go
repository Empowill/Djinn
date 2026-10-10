package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	in.finished(t, "W1", map[string]string{"app/plan/verify.md": "# Docs\n- [ ] verify UI (needs: a Mac)\n"})
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

// TestComputedMoveTwoPushesOneQuestion: two pushes of the same commit sha
// raise at most one install question.
func TestComputedMoveTwoPushesOneQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
	in.pass(t, 0)
	in.pass(t, 10*time.Minute)

	// First pass after delay: exactly one install question raised
	questions, _ := store.List[*planv1.Question](t.Context(), in.db, nil)
	count := 0
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 install question after first push and delay, got %d", count)
	}

	// A second pass / push of the same sha
	in.pass(t, 10*time.Minute)

	questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
	count = 0
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected still exactly 1 install question after second push, got %d", count)
	}

	// A second wish targeting the same project also does not duplicate the question
	w2, err := in.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Second wish", ProjectIds: []string{in.projectID},
	}))
	if err == nil && w2 != nil {
		in.pass(t, 10*time.Minute)
		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		count = 0
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected 1 install question even with second wish, got %d", count)
		}
	}
}

// TestComputedMoveInstallDoneNoNewQuestionAfterRestart: once an install is done,
// no new question is raised after restarting Djinn.
func TestComputedMoveInstallDoneNoNewQuestionAfterRestart(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
	in.pass(t, 0)
	in.pass(t, 10*time.Minute)

	questions, _ := store.List[*planv1.Question](t.Context(), in.db, nil)
	var installQ *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			installQ = q
			break
		}
	}
	if installQ == nil {
		t.Fatalf("no install question asked before restart")
	}

	// Developer answers "Install and restart"
	did := in.answer(t, installQ, planv1.Choice_CHOICE_A)
	if !strings.HasPrefix(did, "Djinn installed build") {
		t.Fatalf("what Djinn did: %q; want prefix 'Djinn installed build'", did)
	}

	// Restart Djinn
	in.restart(t)

	// Advance clock and run integration pass
	in.pass(t, 10*time.Minute)

	// Verify no new install question was raised
	questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
	openInstallCount := 0
	totalInstallCount := 0
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			totalInstallCount++
			if q.GetAnswer() == nil {
				openInstallCount++
			}
		}
	}
	if openInstallCount != 0 {
		t.Errorf("expected 0 open install questions after restart, got %d", openInstallCount)
	}
	if totalInstallCount != 1 {
		t.Errorf("expected exactly 1 total install question (the answered one), got %d", totalInstallCount)
	}
}

// TestComputedMoveNewerBuildSupersedesOldQuestion: when a newer build is integrated,
// the old uninstalled question is closed with a note, and exactly one new question is raised.
func TestComputedMoveNewerBuildSupersedesOldQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")

	// First build
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
	in.pass(t, 0)
	in.pass(t, 10*time.Minute)

	questions, _ := store.List[*planv1.Question](t.Context(), in.db, nil)
	var q1 *planv1.Question
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			q1 = q
			break
		}
	}
	if q1 == nil {
		t.Fatalf("no install question asked for first build")
	}
	if q1.GetAnswer() != nil {
		t.Fatalf("expected q1 to be open, got answer: %v", q1.GetAnswer())
	}

	// Second build integrated
	in.finished(t, "W2", map[string]string{"app/src/a.txt": "v2\n"})
	in.pass(t, 0)
	in.pass(t, 10*time.Minute)

	// Check questions
	questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
	var q1Updated, q2 *planv1.Question
	openCount := 0
	for _, q := range questions {
		if strings.Contains(q.GetText(), "Install and restart") {
			if q.GetId() == q1.GetId() {
				q1Updated = q
			} else {
				q2 = q
			}
			if q.GetAnswer() == nil {
				openCount++
			}
		}
	}

	if q1Updated == nil || q1Updated.GetAnswer() == nil {
		t.Fatalf("expected old question q1 to be closed, got: %v", q1Updated)
	}
	if !strings.Contains(q1Updated.GetAnswer().GetNote(), "superseded by build") {
		t.Errorf("q1 note = %q, want containing 'superseded by build'", q1Updated.GetAnswer().GetNote())
	}
	if q2 == nil {
		t.Fatalf("expected new question q2 to be raised, but none found")
	}
	if q2.GetAnswer() != nil {
		t.Errorf("expected q2 to be open, got answer: %v", q2.GetAnswer())
	}
	if openCount != 1 {
		t.Errorf("expected exactly 1 open question, got %d", openCount)
	}
}

// TestMergeMoveDiffWithGoCodeDoesNotRaise: a diff with Go code containing "- [ ]"
// raises nothing; the lead line has no move suffix and no "%s".
func TestMergeMoveDiffWithGoCodeDoesNotRaise(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	in.finished(t, "W1", map[string]string{
		"app/src/boxes.go": "package src\n\nimport \"fmt\"\n\nfunc formatBoxes(box, needs string) string {\n\treturn fmt.Sprintf(\"- [ ] %s (needs: %s)\", box, needs)\n}\n",
	})
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Fatalf("expected 0 questions raised for Go code diff, got %d: %v", len(questions), questions)
	}

	if len(told) == 0 {
		t.Fatalf("lead was not told of merge")
	}
	lastTold := told[len(told)-1]
	afterDjinn := strings.TrimPrefix(lastTold, "Djinn: ")
	if strings.Contains(afterDjinn, ":") {
		t.Errorf("lead line %q has unexpected moves suffix", lastTold)
	}
	if strings.Contains(lastTold, "%s") {
		t.Errorf("lead line %q contains unfilled '%%s'", lastTold)
	}
}

// TestMergeMovePlanFileGainingNeedsBoxRaisesOneMove: a plan file gaining a needs box
// raises one move with its box text; the line has no "%s".
func TestMergeMovePlanFileGainingNeedsBoxRaisesOneMove(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	in.finished(t, "W1", map[string]string{
		"app/plan/feature.md": "# Feature\n\n- [ ] Window opens in Cocoa (needs: a Mac)\n",
	})
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 1 {
		t.Fatalf("expected exactly 1 question raised, got %d: %v", len(questions), questions)
	}
	q := questions[0]
	if !q.GetMove() {
		t.Errorf("question Move = false, want true")
	}
	wantText := "Check Window opens in Cocoa on a Mac?"
	if q.GetText() != wantText {
		t.Errorf("question text %q, want %q", q.GetText(), wantText)
	}
	if !strings.Contains(q.GetContext(), "Window opens in Cocoa") || !strings.Contains(q.GetContext(), "a Mac") {
		t.Errorf("question context %q missing box or needs text", q.GetContext())
	}
	for _, text := range []string{q.GetText(), q.GetContext()} {
		if strings.Contains(text, "%s") {
			t.Errorf("question text/context %q contains unfilled '%%s'", text)
		}
	}

	if len(told) == 0 {
		t.Fatalf("lead was not told of merge")
	}
	lastTold := told[len(told)-1]
	wantSuffix := ": check Window opens in Cocoa on a Mac"
	if !strings.HasSuffix(lastTold, wantSuffix) {
		t.Errorf("lead line %q does not end with %q", lastTold, wantSuffix)
	}
	if strings.Contains(lastTold, "%s") {
		t.Errorf("lead line %q contains unfilled '%%s'", lastTold)
	}
}

// TestMoveTemplatesNoUnfilledFormatVerbs: checks that every template helper fills all its format verbs.
func TestMoveTemplatesNoUnfilledFormatVerbs(t *testing.T) {
	testx.Portable(t)

	assertNoVerb := func(name, s string) {
		t.Helper()
		for _, verb := range []string{"%s", "%d", "%v", "%+v", "%#v", "%!", "%(EXTRA"} {
			if strings.Contains(s, verb) {
				t.Errorf("%s: contains unfilled format verb %q: %q", name, verb, s)
			}
		}
	}

	assertQuestion := func(name string, q *planv1.Question) {
		t.Helper()
		if q == nil {
			t.Fatalf("%s: question is nil", name)
		}
		assertNoVerb(name+".Text", q.GetText())
		assertNoVerb(name+".Context", q.GetContext())
		for _, opt := range q.GetOptions() {
			assertNoVerb(name+".Option", opt)
		}
	}

	// leadMergeLine
	assertNoVerb("leadMergeLine.empty", leadMergeLine("W1", "main", "1234567890abcdef", nil))
	assertNoVerb("leadMergeLine.withMoves", leadMergeLine("W1, W2", "feat/test", "1234567890abcdef", []string{"start a release", "push tag v1.0.0"}))
	assertNoVerb("leadMergeLine.percentInMoves", leadMergeLine("W1", "main", "1234567890abcdef", []string{"100% verified"}))

	// needsMoveName & needsMoveQuestion
	assertNoVerb("needsMoveName.normal", needsMoveName("Window opens", "a Mac"))
	assertNoVerb("needsMoveName.emptyBox", needsMoveName("", "a Mac"))
	assertNoVerb("needsMoveName.percent", needsMoveName("100% Cocoa", "a Mac"))
	assertQuestion("needsMoveQuestion.normal", needsMoveQuestion("wish1", "Window opens", "a Mac"))
	assertQuestion("needsMoveQuestion.emptyBox", needsMoveQuestion("wish1", "", "a Mac"))
	assertQuestion("needsMoveQuestion.percent", needsMoveQuestion("wish1", "100% Cocoa", "a Mac"))

	// tagMoveName & tagMoveQuestion
	assertNoVerb("tagMoveName", tagMoveName("v1.2.3"))
	assertQuestion("tagMoveQuestion", tagMoveQuestion("wish1", "v1.2.3", "origin"))

	// releaseMoveQuestion
	assertQuestion("releaseMoveQuestion", releaseMoveQuestion("wish1", "1234567890abcdef"))

	// prMoveName, prMoveQuestion & prGrowsMoveName
	assertNoVerb("prMoveName", prMoveName("main"))
	assertNoVerb("prGrowsMoveName", prGrowsMoveName("2"))
	assertNoVerb("prGrowsMoveName.empty", prGrowsMoveName(""))
	assertQuestion("prMoveQuestion", prMoveQuestion("wish1", "main", "Title", "Body text"))
	assertQuestion("prMoveQuestion.percentInBody", prMoveQuestion("wish1", "main", "Title 100%", "Coverage at 99%"))

	// installMoveQuestion
	assertQuestion("installMoveQuestion", installMoveQuestion("wish1", "1234567890abcdef", "feat/branch", 10*time.Minute))

	// azimaProofMoveQuestion
	assertQuestion("azimaProofMoveQuestion", azimaProofMoveQuestion("wish1", "t1", "T01", "a Mac", []string{"- [ ] Cocoa (needs: a Mac)"}))
}

// TestMergeMoveOpenPRDeduplicatedAndAheadGreen: "open a pull request to main" appears
// once per subject (deduplicated across tasks/passes), and only when the branch is really ahead and green.
func TestMergeMoveOpenPRDeduplicatedAndAheadGreen(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	_ = in.remote(t)

	initialSha := in.tip(t)
	in.git(t, in.repo, "branch", "main", initialSha)
	in.git(t, in.repo, "push", "origin", "main")

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\nmain_branch: \"main\"\n")

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	// Create an azima with task W1
	azima := in.azima(t, in.wishID, "Big Feature")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a1\n"}, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	// First pass integrates W1. Azima ends, branch pushed to origin (ahead and green),
	// exactly 1 PR question is asked.
	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	prCount := 0
	for _, q := range questions {
		if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
			prCount++
		}
	}
	if prCount != 1 {
		t.Fatalf("expected exactly 1 PR question after settling and pushing green, got %d", prCount)
	}

	// Another integration pass does not duplicate the question
	in.pass(t, 0)

	questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	prCount = 0
	for _, q := range questions {
		if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
			prCount++
		}
	}
	if prCount != 1 {
		t.Fatalf("expected still exactly 1 PR question (deduplicated), got %d", prCount)
	}

	// An ongoing second wish targeting the same project also does not duplicate the question
	w2, err := in.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Second wish", ProjectIds: []string{in.projectID},
	}))
	if err == nil && w2 != nil {
		in.pass(t, 0)
		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		prCount = 0
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
				prCount++
			}
		}
		if prCount != 1 {
			t.Fatalf("expected still exactly 1 PR question across projects/wishes, got %d", prCount)
		}
	}
}

// TestOpenPRFromBranchParsing tests parsing of gh pr list output in openPRFromBranch.
func TestOpenPRFromBranchParsing(t *testing.T) {
	testx.Portable(t)
	bin := t.TempDir()

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := t.Context()

	setFakeGh := func(content string, exitCode int) {
		outPath := filepath.Join(bin, "gh.out")
		if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sh := fmt.Sprintf("#!/bin/sh\ncat %q\nexit %d\n", outPath, exitCode)
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(sh), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Standard TSV format
	setFakeGh("2\tRelease v0.1.1\tfeat/wails-go\tOPEN\t2026-10-10T12:54:15Z\n", 0)
	prNum, ok := openPRFromBranch(ctx, "", "feat/wails-go")
	if !ok || prNum != "2" {
		t.Fatalf("expected ('2', true), got (%q, %v)", prNum, ok)
	}

	// 2. Hash prefix (#2)
	setFakeGh("#2\tRelease v0.1.1\tfeat/wails-go\tOPEN\t2026-10-10T12:54:15Z\n", 0)
	prNum, ok = openPRFromBranch(ctx, "", "feat/wails-go")
	if !ok || prNum != "2" {
		t.Fatalf("expected ('2', true), got (%q, %v)", prNum, ok)
	}

	// 3. JSON format
	setFakeGh("[{\"number\": 42, \"state\": \"OPEN\"}]\n", 0)
	prNum, ok = openPRFromBranch(ctx, "", "feat/wails-go")
	if !ok || prNum != "42" {
		t.Fatalf("expected ('42', true), got (%q, %v)", prNum, ok)
	}

	// 4. Empty output (no open PRs)
	setFakeGh("", 0)
	prNum, ok = openPRFromBranch(ctx, "", "feat/wails-go")
	if ok || prNum != "" {
		t.Fatalf("expected ('', false), got (%q, %v)", prNum, ok)
	}

	// 5. gh error (exit 1)
	setFakeGh("error: connection refused\n", 1)
	prNum, ok = openPRFromBranch(ctx, "", "feat/wails-go")
	if ok || prNum != "" {
		t.Fatalf("expected ('', false) on gh error, got (%q, %v)", prNum, ok)
	}
}

// TestMergeMoveOpenPRExistsGrowsWithThisWork: when an open PR from that branch exists on GitHub/forge,
// Djinn says "PR #2 grows with this work" once in the merge line, and asks no PR question.
func TestMergeMoveOpenPRExistsGrowsWithThisWork(t *testing.T) {
	testx.Portable(t)
	bin := t.TempDir()
	callsFile := filepath.Join(bin, "calls.txt")
	ghOut := filepath.Join(bin, "gh.out")
	if err := os.WriteFile(ghOut, []byte("2\tRelease v0.1.1\tmaster\tOPEN\t2026-10-10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeScript := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
    cat %q
    exit 0
fi
exit 1
`, callsFile, ghOut)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBat := fmt.Sprintf(`@echo off
echo %%* >> %q
if "%%1"=="pr" if "%%2"=="list" (
    type %q
    exit /b 0
)
exit /b 1
`, callsFile, ghOut)
	_ = os.WriteFile(filepath.Join(bin, "gh.bat"), []byte(fakeBat), 0o755)

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	in := integrating(t)
	_ = in.remote(t)

	initialSha := in.tip(t)
	in.git(t, in.repo, "branch", "main", initialSha)
	in.git(t, in.repo, "push", "origin", "main")

	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\nmain_branch: \"main\"\n")

	var told []string
	in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
		told = append(told, line)
		return nil
	})

	// Create an azima with task W1
	azima := in.azima(t, in.wishID, "Big Feature")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a1\n"}, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	// Pass integrates W1. Azima settles, branch pushed green,
	// but PR #2 is open: says "PR #2 grows with this work", asks no PR question.
	in.pass(t, 0)

	// Verify fake gh was called
	calls, err := os.ReadFile(callsFile)
	if err != nil {
		t.Fatalf("expected fake gh to be called, err: %v", err)
	}
	if !strings.Contains(string(calls), "pr list --head") {
		t.Fatalf("expected fake gh to receive 'pr list --head', got: %s", string(calls))
	}

	// Verify no PR question was asked
	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range questions {
		if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
			t.Fatalf("expected NO pull request question because PR #2 is open, but got question: %q", q.GetText())
		}
	}

	// Verify the lead line includes "PR #2 grows with this work" and NOT "open a pull request to main"
	foundGrows := false
	for _, line := range told {
		if strings.Contains(line, "PR #2 grows with this work") {
			foundGrows = true
		}
		if strings.Contains(line, "open a pull request") {
			t.Fatalf("lead line should not announce 'open a pull request', got: %s", line)
		}
	}
	if !foundGrows {
		t.Fatalf("expected lead line with 'PR #2 grows with this work', told: %v", told)
	}
}
