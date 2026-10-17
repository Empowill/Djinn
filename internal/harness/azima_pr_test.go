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
"pr create")
	printf '%s\n' "$all_args" >> "$GH_DATA/creates"
	echo "https://github.com/acme/app/pull/41"
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

// TestAzimaPRBaseIsMain: an azima's pull request targets main, as recorded when its branch was cut, else as the
// project names it; never the branch of an azima it would wait for.
func TestAzimaPRBaseIsMain(t *testing.T) {
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
}

// TestAzimaPRProposalFlow verifies that when an azima branch is pushed and its parts are done and green,
// Djinn proposes opening its PR with exact title, description, and base, then waits.
func TestAzimaPRProposalFlow(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	_, dataDir := setupFakeGh(t)

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
	created, err := os.ReadFile(filepath.Join(dataDir, "creates"))
	if err != nil {
		t.Fatalf("gh pr create never ran: %v", err)
	}
	if want := "pr create --draft --head " + b1 + " --base " + in.branch + " --title Implement feature alpha."; !strings.HasPrefix(string(created), want) {
		t.Errorf("gh ran %q, want it to begin with %q", created, want)
	}
	if got := in.get(t, t1.GetId()).GetAzima().GetPr(); got.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN ||
		got.GetUrl() != "https://github.com/acme/app/pull/41" {
		t.Errorf("PR recorded %v, want open at its address", got)
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
	prView := `{"number":42,"state":"OPEN","baseRefName":"main","mergeable":"MERGEABLE","url":"https://github.com/acme/app/pull/42"}`
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
	if got := in.get(t, t1.GetId()).GetAzima().GetPr(); got.GetState() != planv1.AzimaPrState_AZIMA_PR_STATE_OPEN ||
		got.GetUrl() != "https://github.com/acme/app/pull/42" {
		t.Errorf("PR = %v, want open at its URL", got)
	}

	// Answering Choice A notes ready to merge.
	did := in.answer(t, readyQ, planv1.Choice_CHOICE_A)
	if did != "Djinn noted: ready to merge" {
		t.Errorf("what Djinn did: %q", did)
	}
}

// TestAzimaPRKeptNeverRetargeted: each azima's pull request targets main from the start and follows its branch:
// another one merged leaves it as it is, never re-targeted; the merged one closes its azima, shown merged.
func TestAzimaPRKeptNeverRetargeted(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.setAzimaStrategy(t)
	in.remote(t)
	_, dataDir := setupFakeGh(t)

	t1 := in.azima(t, in.wishID, "Base")
	t2 := in.azima(t, in.wishID, "Second")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = t2.GetId() })
	in.pass(t, 0)
	b1, b2 := "djinn/T1-base", "djinn/T2-second"
	for _, b := range []string{b1, b2} {
		in.git(t, in.repo, "push", "origin", b)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b, in.git(t, in.repo, "rev-parse", "refs/heads/"+b))
	}
	b2Sha := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2)

	pr1 := fmt.Sprintf(`{"number":101,"state":"MERGED","baseRefName":%q,"mergeable":"UNKNOWN","url":"https://github.com/acme/app/pull/101"}`, in.branch)
	pr2 := fmt.Sprintf(`{"number":102,"state":"OPEN","baseRefName":%q,"mergeable":"MERGEABLE","url":"https://github.com/acme/app/pull/102"}`, in.branch)
	for name, data := range map[string]string{"view-djinn_T1-base.json": pr1, "view-djinn_T2-second.json": pr2} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	calls, err := os.ReadFile(filepath.Join(dataDir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "pr edit") {
		t.Errorf("a pull request was re-targeted: %s", calls)
	}
	if got := in.git(t, in.repo, "rev-parse", "refs/heads/"+b2); got != b2Sha {
		t.Errorf("%s moved to %s when T1 merged, want it at %s", b2, got, b2Sha)
	}
}

// TestAzimaPRGlabReady verifies the ready-to-merge question with GitLab (glab), and that a merge request merged
// closes its azima without re-targeting any other.
func TestAzimaPRGlabReady(t *testing.T) {
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
	t2 := in.azima(t, in.wishID, "Second")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = t1.GetId() })
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = t2.GetId() })
	in.pass(t, 0)
	b1, b2 := "djinn/T1-base", "djinn/T2-second"
	for _, b := range []string{b1, b2} {
		in.git(t, in.repo, "push", "origin", b)
		in.git(t, in.repo, "update-ref", "refs/remotes/origin/"+b, in.git(t, in.repo, "rev-parse", "refs/heads/"+b))
	}

	// MR !201 is OPEN, mergeable, and green pipeline.
	mr1Open := `{"iid":201,"state":"opened","target_branch":"main","web_url":"https://gitlab.example.com/acme/project/-/merge_requests/201","detailed_merge_status":"mergeable","head_pipeline":{"id":99,"status":"success"}}`
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
	if got := in.get(t, t1.GetId()).GetAzima().GetPr().GetUrl(); got != "https://gitlab.example.com/acme/project/-/merge_requests/201" {
		t.Errorf("MR URL = %q", got)
	}

	// Now MR !201 is MERGED, and MR !202 for T2 is OPEN, targeting main.
	mr1Merged := `{"iid":201,"state":"merged","target_branch":"main","detailed_merge_status":"not_applicable"}`
	if err := os.WriteFile(filepath.Join(dataDir, "mr-djinn_T1-base.json"), []byte(mr1Merged), 0o644); err != nil {
		t.Fatal(err)
	}
	mr2Open := `{"iid":202,"state":"opened","target_branch":"main","detailed_merge_status":"mergeable","head_pipeline":{"id":100,"status":"running"}}`
	if err := os.WriteFile(filepath.Join(dataDir, "mr-djinn_T2-second.json"), []byte(mr2Open), 0o644); err != nil {
		t.Fatal(err)
	}
	in.git(t, in.repo, "merge", "--quiet", "--no-ff", b1, "-m", "Merge "+b1+" into main")

	in.pass(t, 0)

	if in.get(t, t1.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("t1 status = %v, want DONE", in.get(t, t1.GetId()).GetStatus())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "updates")); err == nil {
		t.Error("a merge request was re-targeted")
	}
}

// TestBabysitPRWatchesEveryAzimaPR verifies that babysit-pr watcher watches every azima PR,
// speaking only when something needs the lead, and exits once all are merged.
func TestBabysitPRWatchesEveryAzimaPR(t *testing.T) {
	t.Parallel()
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
	if !strings.Contains(outStr, "PR #101 · checks failed at a1: lint") {
		t.Errorf("output missing PR 101 failure: %s", outStr)
	}
	if !strings.Contains(outStr, "PR #102 · all checks pass at b2 (2)") {
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
