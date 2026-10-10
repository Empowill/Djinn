package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

const fakeGhScript = `#!/bin/sh
set -e
cmd="$1 $2"
arg3="${3:-}"
all_args="$*"
printf '%s\n' "$*" >> "$GH_DATA/calls"
expr=""
while [ $# -gt 0 ]; do
	case $1 in
	--jq) expr=$2; shift ;;
	esac
	shift
done

case $cmd in
"pr view")
	target_safe=$(printf '%s' "$arg3" | tr '/:' '__')
	file=""
	if [ -f "$GH_DATA/view-$target_safe.json" ]; then
		file="$GH_DATA/view-$target_safe.json"
	elif [ -f "$GH_DATA/view.json" ]; then
		file="$GH_DATA/view.json"
	else
		echo "no pull request found for $arg3" >&2
		exit 1
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
"pr checks")
	file=""
	if [ -f "$GH_DATA/checks-$arg3.json" ]; then
		file="$GH_DATA/checks-$arg3.json"
	elif [ -f "$GH_DATA/checks.json" ]; then
		file="$GH_DATA/checks.json"
	else
		echo "no checks found for $arg3" >&2
		exit 1
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
"pr edit")
	printf '%s\n' "$all_args" >> "$GH_DATA/edits"
	exit 0
	;;
"pr list")
	file="$GH_DATA/list.json"
	if [ ! -f "$file" ]; then
		echo "[]"
		exit 0
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
*)
	echo "fake gh: unknown command $cmd" >&2
	exit 1
	;;
esac
`

const fakeGlabScript = `#!/bin/sh
set -e
cmd="$1 $2"
arg3="${3:-}"
all_args="$*"
printf '%s\n' "$*" >> "$GLAB_DATA/calls"
expr=""
pipeline=""
while [ $# -gt 0 ]; do
	case $1 in
	--jq) expr=$2; shift ;;
	--pipeline-id) pipeline=$2; shift ;;
	esac
	shift
done

case $cmd in
"mr view")
	target_safe=$(printf '%s' "$arg3" | tr '/:' '__')
	file=""
	if [ -f "$GLAB_DATA/mr-$target_safe.json" ]; then
		file="$GLAB_DATA/mr-$target_safe.json"
	elif [ -f "$GLAB_DATA/mr.json" ]; then
		file="$GLAB_DATA/mr.json"
	else
		echo "no MR found for $arg3" >&2
		exit 1
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
"mr update")
	printf '%s\n' "$all_args" >> "$GLAB_DATA/updates"
	exit 0
	;;
"mr list")
	file="$GLAB_DATA/list.json"
	if [ ! -f "$file" ]; then
		echo "[]"
		exit 0
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
"ci get")
	file=""
	if [ -n "$pipeline" ] && [ -f "$GLAB_DATA/pipeline-$pipeline.json" ]; then
		file="$GLAB_DATA/pipeline-$pipeline.json"
	elif [ -f "$GLAB_DATA/pipeline.json" ]; then
		file="$GLAB_DATA/pipeline.json"
	else
		echo "{}"
		exit 0
	fi
	if [ -n "$expr" ]; then
		exec jq -r "$expr" "$file"
	else
		exec cat "$file"
	fi
	;;
*)
	echo "fake glab: unknown command $cmd" >&2
	exit 1
	;;
esac
`

func setupFakeGh(t *testing.T) (binDir, dataDir string) {
	t.Helper()
	binDir, dataDir = t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(fakeGhScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("GH_DATA", dataDir)
	return binDir, dataDir
}

func setupFakeGlab(t *testing.T) (binDir, dataDir string) {
	t.Helper()
	binDir, dataDir = t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "glab"), []byte(fakeGlabScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("GLAB_DATA", dataDir)
	return binDir, dataDir
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

// TestAzimaPRDraftProposal tests PR proposal drafting from the azima goal and parts.
func TestAzimaPRDraftProposal(t *testing.T) {
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

// TestAzimaPRBaseCalculation tests PR base calculation for independent and stacked azimas.
func TestAzimaPRBaseCalculation(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{store: db}
	ctx := t.Context()
	wishID := "wish-1"

	t1 := &planv1.Task{
		Id:     "t1",
		WishId: wishID,
		Code:   "T1",
		Title:  "Alpha Base",
		Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	t2 := &planv1.Task{
		Id:        "t2",
		WishId:    wishID,
		Code:      "T2",
		Title:     "Beta Stacked",
		Kind:      planv1.TaskKind_TASK_KIND_AZIMA,
		Status:    planv1.TaskStatus_TASK_STATUS_PENDING,
		DependsOn: []string{"t1"},
	}
	t3 := &planv1.Task{
		Id:        "t3",
		WishId:    wishID,
		Code:      "T3",
		Title:     "Gamma Leaf",
		Kind:      planv1.TaskKind_TASK_KIND_AZIMA,
		Status:    planv1.TaskStatus_TASK_STATUS_PENDING,
		DependsOn: []string{"t2"},
	}

	for _, task := range []*planv1.Task{t1, t2, t3} {
		putTaskInDB(t, db, task)
	}

	settings := plan.Settings{MainBranch: "main"}

	// Independent azima T1 has base "main".
	base1, err := h.azimaPRBase(ctx, "", settings, t1)
	if err != nil || base1 != "main" {
		t.Fatalf("t1 base = %q, %v; want main", base1, err)
	}

	// Stacked azima T2 has base "djinn/T1-alpha-base".
	base2, err := h.azimaPRBase(ctx, "", settings, t2)
	if err != nil || base2 != "djinn/T1-alpha-base" {
		t.Fatalf("t2 base = %q, %v; want djinn/T1-alpha-base", base2, err)
	}

	// Stacked azima T3 has base "djinn/T2-beta-stacked".
	base3, err := h.azimaPRBase(ctx, "", settings, t3)
	if err != nil || base3 != "djinn/T2-beta-stacked" {
		t.Fatalf("t3 base = %q, %v; want djinn/T2-beta-stacked", base3, err)
	}

	// When T1 is merged (status DONE), T2's base becomes "main".
	t1.Status = planv1.TaskStatus_TASK_STATUS_DONE
	putTaskInDB(t, db, t1)

	base2AfterT1Done, err := h.azimaPRBase(ctx, "", settings, t2)
	if err != nil || base2AfterT1Done != "main" {
		t.Fatalf("t2 base after t1 done = %q, %v; want main", base2AfterT1Done, err)
	}

	// T3's base remains T2 while T2 is unmerged.
	base3AfterT1Done, err := h.azimaPRBase(ctx, "", settings, t3)
	if err != nil || base3AfterT1Done != "djinn/T2-beta-stacked" {
		t.Fatalf("t3 base = %q, %v; want djinn/T2-beta-stacked", base3AfterT1Done, err)
	}

	// When T2 is also merged (status DONE), T3's base becomes "main".
	t2.Status = planv1.TaskStatus_TASK_STATUS_DONE
	putTaskInDB(t, db, t2)

	base3AfterT2Done, err := h.azimaPRBase(ctx, "", settings, t3)
	if err != nil || base3AfterT2Done != "main" {
		t.Fatalf("t3 base after t2 done = %q, %v; want main", base3AfterT2Done, err)
	}
}

// TestAzimaPRProposalFlow verifies that when an azima branch is pushed and its parts are done and green,
// Djinn proposes opening its PR with exact title, description, and base, then waits.
func TestAzimaPRProposalFlow(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	setupFakeGh(t)

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

	// Answering Choice A notes opening the PR.
	did := in.answer(t, prQ, planv1.Choice_CHOICE_A)
	if did != "Djinn noted: open pull request" {
		t.Errorf("what Djinn did: %q", did)
	}
}

// TestAzimaPRStackedProposal verifies that a stacked azima proposes a PR targeting the branch of the azima it depends on.
func TestAzimaPRStackedProposal(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	setupFakeGh(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	t2.Description = "## Goal\nImplement dependent feature.\n"
	putTaskInDB(t, in.db, t2)

	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "push", "origin", b1)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)
	b2 := "djinn/T2-dependent"
	in.git(t, in.repo, "push", "origin", b2)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b2, in.git(t, in.repo, "rev-parse", "refs/heads/"+b2))

	in.pass(t, 0)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var t2Q *planv1.Question
	for _, q := range questions {
		if q.GetTaskId() == t2.GetId() && strings.Contains(strings.ToLower(q.GetText()), "pull request") {
			t2Q = q
			break
		}
	}
	if t2Q == nil {
		t.Fatalf("no PR question for T2; questions: %v", questions)
	}
	if want := "Open a pull request for T2 to " + b1 + "?"; t2Q.GetText() != want {
		t.Errorf("question text = %q, want %q", t2Q.GetText(), want)
	}
	if !strings.Contains(t2Q.GetContext(), "**Base**: "+b1) {
		t.Errorf("context lacks base %s: %q", b1, t2Q.GetContext())
	}
}

// TestAzimaPRReadyQuestion verifies that when an azima PR is mergeable and green, Djinn asks the developer it is ready.
func TestAzimaPRReadyQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	_, dataDir := setupFakeGh(t)

	t1 := in.azima(t, in.wishID, "One Alpha")
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.pass(t, 0)

	b1 := "djinn/T1-one-alpha"
	in.git(t, in.repo, "push", "origin", b1)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

	// Fake gh returns PR #42 for branch b1: OPEN, mergeable, and all checks green.
	prView := `{"number":42,"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE"}`
	if err := os.WriteFile(filepath.Join(dataDir, "view-djinn_T1-one-alpha.json"), []byte(prView), 0o644); err != nil {
		t.Fatal(err)
	}
	prChecks := `[{"name":"test","bucket":"pass"},{"name":"lint","bucket":"pass"}]`
	if err := os.WriteFile(filepath.Join(dataDir, "checks-42.json"), []byte(prChecks), 0o644); err != nil {
		t.Fatal(err)
	}

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

	// Answering Choice A notes ready to merge.
	did := in.answer(t, readyQ, planv1.Choice_CHOICE_A)
	if did != "Djinn noted: ready to merge" {
		t.Errorf("what Djinn did: %q", did)
	}
}

// TestAzimaPRRetargetAfterMerge verifies that when an azima PR is merged, Djinn marks the azima as merged,
// retargets dependent open PRs to main, and updates stacked branches.
func TestAzimaPRRetargetAfterMerge(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	_, dataDir := setupFakeGh(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "push", "origin", b1)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

	b2 := "djinn/T2-dependent"
	in.git(t, in.repo, "push", "origin", b2)
	b2Sha := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b2, b2Sha)

	// Simulate PR #101 for T1 being MERGED, and PR #102 for T2 being OPEN targeting T1's branch.
	pr1View := `{"number":101,"state":"MERGED","baseRefName":"main","mergeable":"UNKNOWN"}`
	if err := os.WriteFile(filepath.Join(dataDir, "view-djinn_T1-base.json"), []byte(pr1View), 0o644); err != nil {
		t.Fatal(err)
	}
	pr2View := fmt.Sprintf(`{"number":102,"state":"OPEN","baseRefName":%q,"mergeable":"MERGEABLE"}`, b1)
	if err := os.WriteFile(filepath.Join(dataDir, "view-djinn_T2-dependent.json"), []byte(pr2View), 0o644); err != nil {
		t.Fatal(err)
	}
	pr2Checks := `[{"name":"test","bucket":"pass"}]`
	if err := os.WriteFile(filepath.Join(dataDir, "checks-102.json"), []byte(pr2Checks), 0o644); err != nil {
		t.Fatal(err)
	}

	// Merge T1 into main locally (as a merged PR would do on origin).
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")
	newMainSha := in.tip(t)

	// Integration pass detects T1 merged: marks T1 done, retargets PR #102 to main, and updates b2 stacked branch.
	in.pass(t, 0)

	// 1. T1 is marked done.
	t1Task := in.get(t, t1.GetId())
	if t1Task.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("t1 status = %v, want DONE", t1Task.GetStatus())
	}

	// 2. Fake gh received retarget edit for PR #102 to main.
	editsBytes, err := os.ReadFile(filepath.Join(dataDir, "edits"))
	if err != nil {
		t.Fatalf("expected edits file from fake gh: %v", err)
	}
	edits := string(editsBytes)
	if want := "pr edit 102 --base " + in.branch; !strings.Contains(edits, want) {
		t.Fatalf("edits = %q, want containing %q", edits, want)
	}

	// 3. Stacked branch b2 was updated to include newMainSha.
	sha2Updated := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)
	if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", newMainSha, sha2Updated); err != nil {
		t.Fatalf("new main %s is not an ancestor of updated %s: %v", newMainSha, b2, err)
	}
}

// TestAzimaPRGlabRetargetAndReady verifies the ready-to-merge question and retarget after merge using GitLab (glab).
func TestAzimaPRGlabRetargetAndReady(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	_, dataDir := setupFakeGlab(t)

	// Set project remote to GitLab.
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

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Dependent", t1.GetCode())
	part1 := func(task *planv1.Task) { task.PartOf = t1.GetId() }
	part2 := func(task *planv1.Task) { task.PartOf = t2.GetId() }

	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, part1)
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, part2)
	in.pass(t, 0)
	b1 := "djinn/T1-base"
	in.git(t, in.repo, "push", "origin", b1)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b1, in.git(t, in.repo, "rev-parse", "refs/heads/"+b1))

	b2 := "djinn/T2-dependent"
	in.git(t, in.repo, "push", "origin", b2)
	in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b2, in.git(t, in.repo, "rev-parse", "refs/heads/"+b2))

	// MR !201 is OPEN, mergeable, and green pipeline.
	mr1Open := `{"iid":201,"state":"opened","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"id":99,"status":"success"}}`
	if err := os.WriteFile(filepath.Join(dataDir, "mr-djinn_T1-base.json"), []byte(mr1Open), 0o644); err != nil {
		t.Fatal(err)
	}

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

	// Now MR !201 is MERGED, and MR !202 for T2 is OPEN with target_branch b1.
	mr1Merged := `{"iid":201,"state":"merged","target_branch":"main","detailed_merge_status":"not_applicable"}`
	if err := os.WriteFile(filepath.Join(dataDir, "mr-djinn_T1-base.json"), []byte(mr1Merged), 0o644); err != nil {
		t.Fatal(err)
	}
	mr2Open := fmt.Sprintf(`{"iid":202,"state":"opened","target_branch":%q,"detailed_merge_status":"mergeable","head_pipeline":{"id":100,"status":"running"}}`, b1)
	if err := os.WriteFile(filepath.Join(dataDir, "mr-djinn_T2-dependent.json"), []byte(mr2Open), 0o644); err != nil {
		t.Fatal(err)
	}

	// Merge T1 into main locally.
	in.git(t, in.repo, "checkout", in.branch)
	in.git(t, in.repo, "merge", "--no-ff", b1, "-m", "Merge "+b1+" into main")

	in.pass(t, 0)

	// T1 is marked done.
	if in.get(t, t1.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("t1 status = %v, want DONE", in.get(t, t1.GetId()).GetStatus())
	}

	// glab received mr update 202 --target-branch main.
	updatesBytes, err := os.ReadFile(filepath.Join(dataDir, "updates"))
	if err != nil {
		t.Fatalf("expected updates file from fake glab: %v", err)
	}
	updates := string(updatesBytes)
	if want := "mr update 202 --target-branch " + in.branch; !strings.Contains(updates, want) {
		t.Fatalf("updates = %q, want containing %q", updates, want)
	}
}

// TestBabysitPRWatchesEveryAzimaPR verifies that babysit-pr watcher watches every azima PR,
// speaking only when something needs the lead, and exits once all are merged.
func TestBabysitPRWatchesEveryAzimaPR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("watch.sh is a POSIX shell script")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("fake gh filters with jq for watch.sh")
	}

	script, err := filepath.Abs(filepath.Join("..", "..", ".agents", "skills", "babysit-pr", "watch.sh"))
	if err != nil {
		t.Fatal(err)
	}

	repoDir, binDir, dataDir, homeDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(fakeGhScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Init git repo with djinn branches.
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s (%v)", args, out, err)
		}
	}
	run(repoDir, "git", "init", "--quiet")
	run(repoDir, "git", "config", "user.name", "djinn")
	run(repoDir, "git", "config", "user.email", "djinn@example.com")
	if err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "add", "file.txt")
	run(repoDir, "git", "commit", "-m", "init")
	run(repoDir, "git", "branch", "djinn/T1-one")
	run(repoDir, "git", "branch", "djinn/T2-two")

	// Set up fake PRs for 101 and 102.
	list := `[{"number":101,"headRefName":"djinn/T1-one"},{"number":102,"headRefName":"djinn/T2-two"}]`
	if err := os.WriteFile(filepath.Join(dataDir, "list.json"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}

	view101 := `{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"a1","comments":[],"reviews":[]}`
	view102 := `{"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"","headRefOid":"b2","comments":[],"reviews":[]}`
	if err := os.WriteFile(filepath.Join(dataDir, "view-101.json"), []byte(view101), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "view-102.json"), []byte(view102), 0o644); err != nil {
		t.Fatal(err)
	}

	// PR 101 has a failed check, PR 102 is green.
	checks101 := `[{"name":"lint","bucket":"fail"},{"name":"test","bucket":"pass"}]`
	checks102 := `[{"name":"lint","bucket":"pass"},{"name":"test","bucket":"pass"}]`
	if err := os.WriteFile(filepath.Join(dataDir, "checks-101.json"), []byte(checks101), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "checks-102.json"), []byte(checks102), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", script, "101,102", "1", "1")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GH_DATA="+dataDir,
		"DJINN_HOME="+homeDir,
		"DJINN_TASK_ID=watch-azimas-task",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("watch.sh failed: %s (%v)", out, err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "PR #101 · checks failed: lint") {
		t.Errorf("output missing PR 101 failure: %s", outStr)
	}
	if !strings.Contains(outStr, "PR #102 · all checks pass (2)") {
		t.Errorf("output missing PR 102 pass: %s", outStr)
	}

	// Next look: both PRs are merged.
	merged101 := `{"state":"MERGED","mergeable":"UNKNOWN","reviewDecision":"","headRefOid":"a1","comments":[],"reviews":[]}`
	merged102 := `{"state":"MERGED","mergeable":"UNKNOWN","reviewDecision":"","headRefOid":"b2","comments":[],"reviews":[]}`
	if err := os.WriteFile(filepath.Join(dataDir, "view-101.json"), []byte(merged101), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "view-102.json"), []byte(merged102), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd2 := exec.Command("sh", script, "101,102", "1", "1")
	cmd2.Dir = repoDir
	cmd2.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GH_DATA="+dataDir,
		"DJINN_HOME="+homeDir,
		"DJINN_TASK_ID=watch-azimas-task",
	)
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("watch.sh look 2 failed: %s (%v)", out2, err)
	}
	out2Str := string(out2)
	if !strings.Contains(out2Str, "MERGED: PR #101 is merged.") {
		t.Errorf("output missing PR 101 merged: %s", out2Str)
	}
	if !strings.Contains(out2Str, "MERGED: PR #102 is merged.") {
		t.Errorf("output missing PR 102 merged: %s", out2Str)
	}
}
