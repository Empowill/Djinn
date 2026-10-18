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

// TestBriefed verifies worker and ifrit prompt rules appended by briefed:
// - worker prompts include move rules, question shape, and commit rules for editing tasks (W360, Q246)
// - ifrit prompts carry their own rules in place of waiting in the foreground (T51, Q11).
func TestBriefed(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("worker prompt move and commit rules", func(t *testing.T) {
		// 1. With checks: rule is appended next to ChecksBrief.
		r1 := &run{
			task:   &planv1.Task{Branch: "w1-branch", Provider: planv1.Provider_PROVIDER_CLAUDE},
			checks: "Djinn checks this project's work with `go tool task test`.",
		}
		got1 := briefed(r1, "do the work", false)
		wantChecks := "Djinn checks this project's work with `go tool task test`. " + workerCommitRule + " " + workerRules
		if !strings.Contains(got1, wantChecks) {
			t.Errorf("briefed with checks:\ngot: %q\nwant to contain: %q", got1, wantChecks)
		}
		if !strings.Contains(got1, workerHandRule) {
			t.Errorf("briefed with checks missing workerHandRule:\ngot: %q", got1)
		}
		// Q246: a check is never asked of the developer; a question is for a decision or a person's move only, its
		// command at the top of the context.
		for _, want := range []string{
			"Never ask the developer for a check, a measure, a log to read, a CI result to look at or a command to run",
			"do it yourself when you can",
			"say in your final note what remains to verify",
			"the lead spawns a task for it or lets a watcher report it",
			"a CI result comes from the PR's babysit watcher",
			"A question to the developer is only for a decision or a move only a person can make",
			"ask it with `djinn question ask` before you end. " + plan.QuestionShape,
		} {
			if !strings.Contains(got1, want) {
				t.Errorf("worker prompt lacks %q:\n%s", want, got1)
			}
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
			if want := "\n\n" + workerCommitRule + " " + workerRules; !strings.HasSuffix(got, want) {
				t.Errorf("provider %s prompt:\ngot: %q\nwant suffix: %q", provider, got, want)
			}
		}
		for name, task := range map[string]*planv1.Task{
			"review":        {Branch: "w-branch", Provider: planv1.Provider_PROVIDER_CLAUDE, Review: &planv1.TaskReview{}},
			"correction":    {Branch: "w-branch", Provider: planv1.Provider_PROVIDER_CLAUDE, Correction: &planv1.TaskCorrection{}},
			"investigation": {Branch: "w-branch", Provider: planv1.Provider_PROVIDER_CLAUDE, Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION},
			"ifrit":         {Branch: "w-branch", Provider: planv1.Provider_PROVIDER_CLAUDE, Kind: planv1.TaskKind_TASK_KIND_IFRIT},
		} {
			r := &run{task: task, ifrit: name == "ifrit"}
			if got := briefed(r, "prompt text", false); strings.Contains(got, workerCommitRule) {
				t.Errorf("%s prompt has the commit rule: %q", name, got)
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
	})

	t.Run("ifrit prompt rules", func(t *testing.T) {
		checks := plan.Settings{Checks: []*planv1.ProjectCheck{{Name: "lint", Command: "make lint",
			When: []planv1.CheckWhen{planv1.CheckWhen_CHECK_WHEN_COMMIT}}}}
		ifrit := &run{ifrit: true, task: &planv1.Task{Branch: "w12-front", Provider: planv1.Provider_PROVIDER_CLAUDE,
			Kind: planv1.TaskKind_TASK_KIND_IFRIT}}
		ifrit.setChecks(checks)
		got := briefed(ifrit, "Make the button primary.", false)
		for _, want := range []string{
			"Make the button primary.\n\n" + checks.IfritChecksBrief() + " ",
			"After each request: make the change, say in three lines at most what changed and how to see it",
			"then end your turn; Djinn keeps you alive",
			"`djinn serve run <name> \"<command>\"`",
			"never with `&` or `nohup`",
			"Never wait in a loop or sleep.",
			"only the tests a request names, when it asks",
			workerHandRule, workerCheckRule, workerMoveRule,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("ifrit prompt lacks %q:\n%s", want, got)
			}
		}
		for _, unwanted := range []string{workerWaitRule, "A worker runs the commit checks"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("ifrit prompt has %q:\n%s", unwanted, got)
			}
		}

		outside := &run{ifrit: true, task: &planv1.Task{Provider: planv1.Provider_PROVIDER_CLAUDE, Kind: planv1.TaskKind_TASK_KIND_IFRIT}}
		if got := briefed(outside, "Try the stack.", false); got != "Try the stack.\n\n"+ifritRules {
			t.Errorf("ifrit outside Git: %q", got)
		}

		task := &run{task: &planv1.Task{Branch: "w13-back", Provider: planv1.Provider_PROVIDER_CLAUDE}}
		task.setChecks(checks)
		if got := briefed(task, "Fix it.", false); strings.Contains(got, ifritRule) || !strings.Contains(got, workerWaitRule) {
			t.Errorf("a task's prompt: %q", got)
		}
	})
}

// TestRemoteTags: remoteTags returns peeled tags correctly.
func TestRemoteTags(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	bare := in.remote(t)

	// Create lightweight tag
	in.git(t, in.repo, "tag", "v1.0.0")
	// Create annotated tag
	in.git(t, in.repo, "tag", "-a", "v2.0.0", "-m", "release v2.0.0")
	in.git(t, in.repo, "push", bare, "refs/tags/v1.0.0", "refs/tags/v2.0.0")

	tags, err := remoteTags(t.Context(), in.repo, "origin")
	if err != nil {
		t.Fatal(err)
	}
	headSha := in.git(t, in.repo, "rev-parse", "HEAD")
	if tags["v1.0.0"] != headSha {
		t.Errorf("v1.0.0 sha = %q, want %q", tags["v1.0.0"], headSha)
	}
	if tags["v2.0.0"] != headSha {
		t.Errorf("v2.0.0 sha = %q, want %q (peeled)", tags["v2.0.0"], headSha)
	}
}

// TestTagMoveQuestion verifies that every move question helper sets its urgency level (W258)
// and fills all format verbs without leaving raw %s/%v placeholders.
func TestTagMoveQuestion(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("computed question levels", func(t *testing.T) {
		tagQ := tagMoveQuestion("w1", "p1", "v1.0.0", "origin")
		if !tagQ.GetMove() || tagQ.GetBefore() != "before the release" {
			t.Errorf("tagMoveQuestion: move=%v, before=%q; want move=true, before='before the release'", tagQ.GetMove(), tagQ.GetBefore())
		}

		relQ := releaseMoveQuestion("w1", "abcdef123456")
		if !relQ.GetMove() || relQ.GetBefore() != "before the release" {
			t.Errorf("releaseMoveQuestion: move=%v, before=%q; want move=true, before='before the release'", relQ.GetMove(), relQ.GetBefore())
		}

		prQ := prMoveQuestion("w1", "p1", "main", "djinn/w1", "Feature", "Body")
		if !prQ.GetMove() || prQ.GetBefore() != "before the merge" {
			t.Errorf("prMoveQuestion: move=%v, before=%q; want move=true, before='before the merge'", prQ.GetMove(), prQ.GetBefore())
		}

		instQ := installMoveQuestion("w1", "p1", "abcdef123456", "feat", time.Hour)
		if !instQ.GetMove() || instQ.GetBefore() != "" {
			t.Errorf("installMoveQuestion: move=%v, before=%q; want move=true, before=''", instQ.GetMove(), instQ.GetBefore())
		}

		needsQ := needsMoveQuestion("w1", "build", "a Mac")
		if !needsQ.GetMove() || needsQ.GetBefore() != "" {
			t.Errorf("needsMoveQuestion: move=%v, before=%q; want move=true, before=''", needsQ.GetMove(), needsQ.GetBefore())
		}

		azimaQ := azimaProofMoveQuestion("w1", "t1", "W1", "a Mac", []string{"- [ ] test"})
		if !azimaQ.GetMove() || azimaQ.GetBefore() != "" {
			t.Errorf("azimaProofMoveQuestion: move=%v, before=%q; want move=true, before=''", azimaQ.GetMove(), azimaQ.GetBefore())
		}

		h := &Harness{}
		pushQ := h.pushQuestion(&planv1.Wish{Id: "w1"}, "Push?", "ctx", []string{"A", "B"})
		if pushQ.GetBefore() != "before the push" {
			t.Errorf("pushQuestion: before=%q; want 'before the push'", pushQ.GetBefore())
		}
	})

	t.Run("templates have no unfilled format verbs", func(t *testing.T) {
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

		assertNoVerb("leadMergeLine.empty", leadMergeLine("W1", "main", "1234567890abcdef", nil))
		assertNoVerb("leadMergeLine.withMoves", leadMergeLine("W1, W2", "feat/test", "1234567890abcdef", []string{"start a release", "push tag v1.0.0"}))
		assertNoVerb("leadMergeLine.percentInMoves", leadMergeLine("W1", "main", "1234567890abcdef", []string{"100% verified"}))
		assertNoVerb("leadAlreadyInLine", leadAlreadyInLine("W1", "main"))

		assertNoVerb("needsMoveName.normal", needsMoveName("Window opens", "a Mac"))
		assertNoVerb("needsMoveName.emptyBox", needsMoveName("", "a Mac"))
		assertNoVerb("needsMoveName.percent", needsMoveName("100% Cocoa", "a Mac"))
		assertQuestion("needsMoveQuestion.normal", needsMoveQuestion("wish1", "Window opens", "a Mac"))
		assertQuestion("needsMoveQuestion.emptyBox", needsMoveQuestion("wish1", "", "a Mac"))
		assertQuestion("needsMoveQuestion.percent", needsMoveQuestion("wish1", "100% Cocoa", "a Mac"))

		assertNoVerb("tagMoveName", tagMoveName("v1.2.3"))
		assertQuestion("tagMoveQuestion", tagMoveQuestion("wish1", "p1", "v1.2.3", "origin"))

		assertQuestion("releaseMoveQuestion", releaseMoveQuestion("wish1", "1234567890abcdef"))

		assertNoVerb("prMoveName", prMoveName("main"))
		assertNoVerb("prGrowsMoveName", prGrowsMoveName("2"))
		assertNoVerb("prGrowsMoveName.empty", prGrowsMoveName(""))
		assertQuestion("prMoveQuestion", prMoveQuestion("wish1", "p1", "main", "djinn/w1", "Title", "Body text"))
		assertQuestion("prMoveQuestion.percentInBody", prMoveQuestion("wish1", "p1", "main", "djinn/w1", "Title 100%", "Coverage at 99%"))

		assertQuestion("installMoveQuestion", installMoveQuestion("wish1", "p1", "1234567890abcdef", "feat/branch", 10*time.Minute))
		assertQuestion("azimaProofMoveQuestion", azimaProofMoveQuestion("wish1", "t1", "T01", "a Mac", []string{"- [ ] Cocoa (needs: a Mac)"}))
	})
}

// TestOpenPRFromBranch tests parsing of gh pr list output in openPRFromBranch
// and announcing "PR #2 grows with this work" when an open PR already exists.
func TestOpenPRFromBranch(t *testing.T) {
	testx.Portable(t)

	t.Run("parsing", func(t *testing.T) {
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
	})

	t.Run("open PR exists grows with this work", func(t *testing.T) {
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

		azima := in.azima(t, in.wishID, "Big Feature")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a1\n"}, func(task *planv1.Task) {
			task.PartOf = azima.GetId()
		})

		in.pass(t, 0)

		calls, err := os.ReadFile(callsFile)
		if err != nil {
			t.Fatalf("expected fake gh to be called, err: %v", err)
		}
		if !strings.Contains(string(calls), "pr list --head") {
			t.Fatalf("expected fake gh to receive 'pr list --head', got: %s", string(calls))
		}

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
				t.Fatalf("expected NO pull request question because PR #2 is open, but got question: %q", q.GetText())
			}
		}

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
	})
}

// TestHandleMergeMoves tests merge moves detected during integration:
//   - local tags (new tag proposed and pushed on Choice A, tag already on remote at same commit ignored,
//     tag on remote at different commit proposed, and subsequent merge does not repeat the move)
//   - Go code diff with "- [ ]", plan file gaining a needs: box, release workflow files, and worker-asked move deduplication.
func TestHandleMergeMoves(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("tag", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)

		var told []string
		in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
			told = append(told, line)
			return nil
		})

		// Tag v0.2.0 on remote at the base commit (different from W1's commit).
		in.git(t, in.repo, "tag", "v0.2.0")
		in.git(t, in.repo, "push", bare, "refs/tags/v0.2.0")
		in.git(t, in.repo, "tag", "-d", "v0.2.0")

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		// Tag v0.1.0 on W1's commit and push to remote at the same commit -> not proposed.
		in.git(t, w1.GetWorktree(), "tag", "v0.1.0")
		in.git(t, w1.GetWorktree(), "push", bare, "refs/tags/v0.1.0")
		// Tag v0.2.0 on W1's new commit (differs from remote) -> proposed.
		in.git(t, w1.GetWorktree(), "tag", "v0.2.0")
		// Tag v1.0.0 on W1's commit (new local tag) -> proposed and pushed on Choice A.
		in.git(t, w1.GetWorktree(), "tag", "v1.0.0")

		in.pass(t, 0)

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var q010, q020, tagQ *planv1.Question
		for _, q := range questions {
			switch {
			case strings.Contains(q.GetText(), "v0.1.0"):
				q010 = q
			case strings.Contains(q.GetText(), "v0.2.0"):
				q020 = q
			case strings.Contains(q.GetText(), "v1.0.0"):
				tagQ = q
			}
		}
		if q010 != nil {
			t.Fatalf("unexpected question asked for tag v0.1.0 already on remote: %v", q010)
		}
		if q020 == nil {
			t.Fatalf("expected question for tag v0.2.0 at different commit, got none")
		}
		if tagQ == nil {
			t.Fatalf("no question asked for tag v1.0.0; questions: %v", questions)
		}
		if !tagQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if got := tagQ.GetBefore(); got != "before the release" {
			t.Errorf("question Before = %q, want 'before the release'", got)
		}
		if want := "Push tag v1.0.0 to origin?"; tagQ.GetText() != want {
			t.Errorf("question text %q, want %q", tagQ.GetText(), want)
		}
		if len(told) == 0 || !strings.Contains(told[len(told)-1], "push tag v1.0.0") {
			t.Errorf("lead told %v; want to contain 'push tag v1.0.0'", told)
		}

		// Answering Choice A pushes the tag to bare remote.
		did := in.answer(t, tagQ, planv1.Choice_CHOICE_A)
		if did != "Djinn pushed tag v1.0.0" {
			t.Errorf("what Djinn did: %q, want 'Djinn pushed tag v1.0.0'", did)
		}
		if tagSha := in.git(t, bare, "rev-parse", "refs/tags/v1.0.0"); tagSha == "" {
			t.Errorf("tag v1.0.0 not found on remote")
		}

		// Decision Q173: a subsequent merge does not repeat the move since no question was asked on it.
		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, 0)
		if len(told) < 2 {
			t.Fatalf("expected at least 2 told lines, got %d: %v", len(told), told)
		}
		if last := told[len(told)-1]; strings.Contains(last, "push tag v1.0.0") {
			t.Errorf("subsequent merge repeated move: %q", last)
		}
	})

	t.Run("diff, needs box, release files, and worker deduplication", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)

		var told []string
		in.h.TellLeads(func(ctx context.Context, wishID, line string) error {
			told = append(told, line)
			return nil
		})

		// 1. Go code containing "- [ ] %s (needs: %s)" raises nothing.
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
		if afterDjinn := strings.TrimPrefix(lastTold, "Djinn: "); strings.Contains(afterDjinn, ":") {
			t.Errorf("lead line %q has unexpected moves suffix", lastTold)
		}
		if strings.Contains(lastTold, "%s") {
			t.Errorf("lead line %q contains unfilled '%%s'", lastTold)
		}

		// 2. Plan file gaining a needs: box raises one move with its box text and no "%s".
		in.finished(t, "W2", map[string]string{
			"app/plan/feature.md": "# Feature\n\n- [ ] Window opens in Cocoa (needs: a Mac)\n",
		})
		in.pass(t, 0)

		questions, err = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
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
		if wantText := "Check Window opens in Cocoa on a Mac?"; q.GetText() != wantText {
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
		lastTold = told[len(told)-1]
		if wantSuffix := ": check Window opens in Cocoa on a Mac"; !strings.HasSuffix(lastTold, wantSuffix) {
			t.Errorf("lead line %q does not end with %q", lastTold, wantSuffix)
		}
		if strings.Contains(lastTold, "%s") {
			t.Errorf("lead line %q contains unfilled '%%s'", lastTold)
		}

		// 3. Release workflow files changed -> raises "Start release dry run?".
		in.finished(t, "W3", map[string]string{".github/workflows/release.yml": "name: release\n"})
		in.pass(t, 0)

		questions, err = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var relQ *planv1.Question
		for _, item := range questions {
			if strings.Contains(strings.ToLower(item.GetText()), "release") {
				relQ = item
				break
			}
		}
		if relQ == nil {
			t.Fatalf("no question asked for release files changed; questions: %v", questions)
		}
		if !relQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if got := relQ.GetBefore(); got != "before the release" {
			t.Errorf("question Before = %q, want 'before the release'", got)
		}
		if want := "Start release dry run?"; relQ.GetText() != want {
			t.Errorf("question text %q, want %q", relQ.GetText(), want)
		}
		if !strings.Contains(told[len(told)-1], ": start a release") {
			t.Errorf("lead told %v; want to contain ': start a release'", told)
		}
		if did := in.answer(t, relQ, planv1.Choice_CHOICE_A); did != "Djinn noted: start release dry run" {
			t.Errorf("what Djinn did: %q", did)
		}

		// 4. Worker already asked the move question -> deduplicated and omitted from merge line.
		w4 := in.finished(t, "W4", map[string]string{"app/src/a.txt": "a\n"})
		in.git(t, w4.GetWorktree(), "tag", "v2.5.0")
		askQ := &planv1.Question{
			Id:         store.NewID(),
			WishId:     in.wishID,
			TaskId:     w4.GetId(),
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

		questions, err = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		tag250Count := 0
		for _, item := range questions {
			if strings.Contains(item.GetText(), "v2.5.0") {
				tag250Count++
			}
		}
		if tag250Count != 1 {
			t.Fatalf("expected 1 v2.5.0 question (deduplicated), got %d: %v", tag250Count, questions)
		}
		if strings.Contains(told[len(told)-1], "push tag v2.5.0") {
			t.Errorf("lead told %q; want move 'push tag v2.5.0' omitted because no question was asked on merge", told[len(told)-1])
		}
	})
}

// TestComputedMovesPass tests computed moves across integration passes:
//   - install_questions: always lifecycle (delay, deduplication across passes/wishes, superseding by newer build,
//     answering Choice A to install, and no new question after restart)
//   - install_questions: when_needed and never
//   - azima proof move question and verification
//   - open PR move question, deduplication across passes/wishes, and fallback when gh is unconfigured.
func TestComputedMovesPass(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("install always lifecycle", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)

		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
		in.pass(t, 0)

		// Immediately after merge: not delayed yet, no install question.
		questions, _ := store.List[*planv1.Question](t.Context(), in.db, nil)
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				t.Fatalf("install question asked too soon: %v", q)
			}
		}

		// Advance clock beyond 5 minutes: raises 1 install question q1.
		in.pass(t, 10*time.Minute)

		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		var q1 *planv1.Question
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				q1 = q
				break
			}
		}
		if q1 == nil {
			t.Fatalf("no install question asked after delay; questions: %v", questions)
		}
		if !q1.GetMove() {
			t.Errorf("question Move = false, want true")
		}

		// A second pass and a second wish targeting the same project do not duplicate q1.
		in.pass(t, 10*time.Minute)
		w2Wish, err := in.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Second wish", ProjectIds: []string{in.projectID},
		}))
		if err == nil && w2Wish != nil {
			in.pass(t, 10*time.Minute)
		}
		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		count := 0
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected 1 install question across passes and second wish, got %d", count)
		}

		// Second build integrated: supersedes q1 and raises q2.
		in.finished(t, "W2", map[string]string{"app/src/a.txt": "v2\n"})
		in.pass(t, 0)
		in.pass(t, 10*time.Minute)

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
		if q2 == nil || q2.GetAnswer() != nil || openCount != 1 {
			t.Fatalf("expected new open question q2 (openCount=%d), got q2=%v", openCount, q2)
		}

		// Answering Choice A on q2 installs the build.
		did := in.answer(t, q2, planv1.Choice_CHOICE_A)
		if !strings.HasPrefix(did, "Djinn installed build") {
			t.Errorf("what Djinn did: %q; want prefix 'Djinn installed build'", did)
		}

		// Next pass and restart do not raise a new install question.
		in.pass(t, time.Minute)
		in.restart(t)
		in.pass(t, 10*time.Minute)

		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		openInstallCount := 0
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") && q.GetAnswer() == nil {
				openInstallCount++
			}
		}
		if openInstallCount != 0 {
			t.Errorf("expected 0 open install questions after restart, got %d", openInstallCount)
		}
	})

	t.Run("install when_needed and never", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)

		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\ninstall_questions: when_needed\n")

		// 1. Routine build under when_needed raises no question.
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
		in.pass(t, 0)
		in.pass(t, 10*time.Minute)

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				t.Fatalf("expected no install question for routine build with when_needed, got %v", q)
			}
		}

		// 2. Failed task W2 exists, and W3 fixes W2. Under install_questions: never, no question is raised.
		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\ninstall_questions: never\n")

		w2 := &planv1.Task{
			Id: store.NewID(), WishId: in.wishID, ProjectId: in.projectID, Code: "W2", Title: "Failing work",
			Kind: planv1.TaskKind_TASK_KIND_WORK, Status: planv1.TaskStatus_TASK_STATUS_FAILED,
			CreateTime: timestamppb.New(in.clock()),
		}
		putTask(t, in.db, w2, "work")

		in.finished(t, "W3", map[string]string{"app/src/a.txt": "v2\n"}, func(task *planv1.Task) {
			task.Title = "Fix W2"
		})
		in.pass(t, 0)
		in.pass(t, 10*time.Minute)

		questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				t.Fatalf("expected no install question with install_questions: never, got %v", q)
			}
		}

		// 3. Switching back to when_needed raises an install question naming W2, deduplicated, and closed when installed.
		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\ninstall_questions: when_needed\n")
		in.pass(t, 10*time.Minute)

		questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		var installQ *planv1.Question
		installCount := 0
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				installCount++
				installQ = q
			}
		}
		if installCount != 1 || installQ == nil {
			t.Fatalf("expected exactly 1 install question for fix build, got %d", installCount)
		}
		if !installQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if !strings.Contains(installQ.GetText(), "W2") || !strings.Contains(installQ.GetContext(), "W2") {
			t.Errorf("question text/context (%q / %q) does not name failed task W2", installQ.GetText(), installQ.GetContext())
		}

		// Another pass after delay does not duplicate the question.
		in.pass(t, 10*time.Minute)
		questions, _ = store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		count := 0
		for _, q := range questions {
			if strings.Contains(q.GetText(), "Install and restart") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected still exactly 1 install question (deduplicated), got %d", count)
		}

		did := in.answer(t, installQ, planv1.Choice_CHOICE_A)
		if !strings.HasPrefix(did, "Djinn installed build") {
			t.Errorf("what Djinn did: %q; want prefix 'Djinn installed build'", did)
		}

		in.pass(t, time.Minute)
		qGot, err := store.Get[*planv1.Question](t.Context(), in.db, installQ.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if qGot.GetAnswer() == nil || qGot.GetAnswer().GetChoice() != planv1.Choice_CHOICE_A {
			t.Errorf("expected question closed with Choice A, got %v", qGot.GetAnswer())
		}
	})

	t.Run("azima proof", func(t *testing.T) {
		t.Parallel()
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

		did := in.answer(t, proofQ, planv1.Choice_CHOICE_A)
		if did != "Djinn marked "+azima.GetCode()+" done" {
			t.Errorf("what Djinn did: %q", did)
		}

		if got := in.get(t, azima.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("azima status = %v, want DONE", got.GetStatus())
		}
	})

	t.Run("open PR", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare := in.remote(t)

		initialSha := in.tip(t)
		in.git(t, in.repo, "branch", "main", initialSha)
		in.git(t, in.repo, "push", "origin", "main")

		writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
			"generate: \"gen\"\ntest: \"test\"\nmain_branch: \"main\"\n")

		azima := in.azima(t, in.wishID, "Feature")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
			task.PartOf = azima.GetId()
		})

		in.pass(t, 0)

		if in.remoteTip(t, bare) == initialSha {
			t.Fatalf("branch was not pushed to origin")
		}

		questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		var prQ *planv1.Question
		prCount := 0
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
				prCount++
				prQ = q
			}
		}
		if prCount != 1 || prQ == nil {
			t.Fatalf("expected 1 PR question asked; got %d: %v", prCount, questions)
		}
		if !prQ.GetMove() {
			t.Errorf("question Move = false, want true")
		}
		if got := prQ.GetBefore(); got != "before the merge" {
			t.Errorf("PR question Before = %q, want 'before the merge'", got)
		}
		if want := "Open a pull request to main?"; prQ.GetText() != want {
			t.Errorf("question text %q, want %q", prQ.GetText(), want)
		}
		if !strings.Contains(prQ.GetContext(), "### ") {
			t.Errorf("context lacks drafted PR title/body: %q", prQ.GetContext())
		}

		// Another integration pass and a second wish targeting the same project do not duplicate the question.
		in.pass(t, 0)
		w2, err := in.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Second wish", ProjectIds: []string{in.projectID},
		}))
		if err == nil && w2 != nil {
			in.pass(t, 0)
		}
		questions, _ = store.List[*planv1.Question](t.Context(), in.db, nil)
		prCount = 0
		for _, q := range questions {
			if strings.Contains(strings.ToLower(q.GetText()), "pull request") {
				prCount++
			}
		}
		if prCount != 1 {
			t.Fatalf("expected still 1 PR question across passes and wishes, got %d", prCount)
		}

		// Answering Choice A tries to open the PR; when gh fails, tells the developer must open it.
		did := in.answer(t, prQ, planv1.Choice_CHOICE_A)
		if want := "Djinn could not open the PR of " + in.branch + " (no gh configured in test): the developer must open it"; did != want {
			t.Errorf("what Djinn did: %q, want %q", did, want)
		}
	})
}
