package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// reviewer is the review worker that reviews task's work left not committed, once it has ended.
func (in *integration) reviewer(t *testing.T, task *planv1.Task) *planv1.Task {
	t.Helper()
	got, texts := in.integration(t, task)
	if got.GetReviewedBy() == "" {
		t.Fatalf("%s's work has no review worker: %v; events %q", task.GetCode(), got, texts)
	}
	return in.ended(t, got.GetReviewedBy())
}

// wishTasks are the wish's tasks.
func (in *integration) wishTasks(t *testing.T) []*planv1.Task {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

// TestReviewUncommitted verifies that Djinn reviews uncommitted work left in a worktree before integrating.
func TestReviewUncommitted(t *testing.T) {
	t.Parallel()

	// A worktree whose work is committed, a file the project's .gitignore ignores left in it,
	// integrates as it is: no review worker, the ignored file left out.
	t.Run("clean_worktree", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		in := integrating(t)
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n", ".gitignore": "*.log\n"})
		in.leave(t, w1, map[string]string{"app/build.log": "built\n"})

		in.pass(t, 0)
		if got := in.states(t, w1); got != "W1 COMMITTED" {
			t.Fatalf("a clean worktree: %s", got)
		}
		if n := len(in.wishTasks(t)); n != 1 {
			t.Errorf("%d tasks: a review worker started for a clean worktree", n)
		}
		if out := in.git(t, in.repo, "ls-tree", "-r", "--name-only", in.tip(t), "app"); strings.Contains(out, "build.log") ||
			!strings.Contains(out, "app/src/a.txt") {
			t.Errorf("the branch holds:\n%s", out)
		}
	})

	// A worktree with changes not committed is not merged and nothing commits them blindly; a review worker starts by
	// itself in that worktree, the files and their diff in its first prompt; it commits what belongs to the task and
	// drops the rest, and its work integrates like any task's, the reviewed work with it.
	t.Run("uncommitted_work", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		in := integrating(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "The sources")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n", "app/README.md": "# App, with b\n", "app/scratch.txt": "debug\n"})
		var dir, before, status string
		in.correctWith(func(d, _ string) string {
			dir = d
			before, _ = git(t.Context(), d, "log", "-1", "--format=%s")
			status, _ = git(t.Context(), d, "status", "--porcelain")
			if err := os.Remove(filepath.Join(d, "scratch.txt")); err != nil {
				t.Error(err)
			}
			commitAll(t, d, "Add b, named in the readme")
			return "text Kept src/b.txt and README.md, the task's; dropped scratch.txt, debug output."
		})

		in.pass(t, 0)
		w2 := in.reviewer(t, w1)
		got, texts := in.integration(t, w1)
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED || got.GetReason() != "uncommitted: reviewed by W2" ||
			got.GetAttempts() != 1 || got.GetBranch() != in.branch {
			t.Fatalf("W1's integration %v", got)
		}
		want := "uncommitted: W1's worktree holds changes not committed (app/README.md, app/scratch.txt, app/src/b.txt); not merged, " +
			"nothing committed; W2 reviews it, attempt 1 of 2"
		if texts[len(texts)-1] != want {
			t.Errorf("W1's last event %q; want %q", texts[len(texts)-1], want)
		}
		if in.tip(t) != old || len(in.runs) != 0 || len(in.commits(t)) != 0 {
			t.Errorf("merged: the branch at %s from %s; runs %v; commits %v", in.tip(t), old, in.runs, in.commits(t))
		}
		if before != "Work of W1" || status != "M app/README.md\n?? app/scratch.txt\n?? app/src/b.txt" {
			t.Errorf("the reviewer found the last commit %q and the changes\n%s", before, status)
		}
		if w2.GetCode() != "W2" || w2.GetPartOf() != azima.GetId() || w2.GetProvider() != planv1.Provider_PROVIDER_CLAUDE ||
			!slices.Equal(w2.GetReview().GetTaskIds(), []string{w1.GetId()}) || w2.GetReview().GetAttempt() != 1 ||
			w2.GetBranch() != w1.GetBranch() || w2.GetWorktree() != w1.GetWorktree() || w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("the review worker %v", w2)
		}
		if dir != filepath.Join(w1.GetWorktree(), "app") {
			t.Errorf("W2 ran in %s", dir)
		}
		prompt, err := firstPrompt(in.db, w2.GetId())
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"the work of W1 into " + in.branch, "The task, W1: Work of W1. Its prompt:\n\n    work\n",
			"     M app/README.md\n    ?? app/scratch.txt\n    ?? app/src/b.txt\n", "    -# App\n    +# App, with b\n",
			"    +++ b/app/scratch.txt\n    +debug\n", "    +++ b/app/src/b.txt\n    +b", "Commit what belongs to the task's change",
			"Its worker's final note:\n\n    (none)\n", "When in doubt, leave it out and say so", "`git add -A`, `git add .` and `git commit -a` are refused",
			"its commit checks (`djinn gate run test -- test`) run through their gates", "Committed: <path>, <path>. Left: <path> (why), <path> (why)."} {
			if !strings.Contains(prompt, s) {
				t.Errorf("W2's prompt lacks %q:\n%s", s, prompt)
			}
		}
		if got, _ := in.integration(t, w2); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			t.Fatalf("W2's work: %v", got)
		}

		in.pass(t, time.Second)
		if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
			t.Fatalf("after the review: %s", got)
		}
		tip := in.tip(t)
		got, texts = in.integration(t, w1)
		if got.GetSha() != tip || got.GetReason() != "uncommitted: reviewed by W2" || got.GetReviewedBy() != w2.GetId() ||
			texts[len(texts)-1] != "committed into "+in.branch+" as "+tip[:8]+", its work not committed reviewed by W2" {
			t.Errorf("W1's integration %v; events %q", got, texts)
		}
		if out := in.git(t, in.repo, "ls-tree", "-r", "--name-only", tip, "app"); out != "app/README.md\napp/gen/index.txt\napp/src/.keep\napp/src/a.txt\napp/src/b.txt" {
			t.Errorf("the branch holds:\n%s", out)
		}
		if out := in.git(t, in.repo, "log", "--format=%s", "-2", w1.GetBranch()); out != "Add b, named in the readme\nWork of W1" {
			t.Errorf("W1's branch ends with:\n%s", out)
		}
		commits := in.commits(t)
		if len(commits) != 1 || commits[0].GetOldSha() != old || !slices.Equal(commits[0].GetTaskIds(), []string{w2.GetId(), w1.GetId()}) {
			t.Errorf("journal: %v", commits)
		}
	})

	// A worker leaves its fix not committed, saying so, with a screenshot and an env file. Its review worker commits the
	// fix alone, by its name, and leaves the rest where it is, naming each path in its final note.
	t.Run("leaves_non_task_files", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		in := integrating(t)
		queue := &leadQueue{}
		in.h.NotifyLead(queue.notify)
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.h.Note(w1.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "The fix is in the worktree: nothing committed."})
		in.leave(t, w1, map[string]string{"app/src/a.txt": "a, fixed\n", "app/shot.png": "\x89PNG\x00", "app/.env": "TOKEN=1\n"})
		var prompt string
		in.correctWith(func(d, p string) string {
			prompt = p
			if _, err := git(t.Context(), d, "add", "--", "src/a.txt"); err != nil {
				t.Error(err)
			}
			if _, err := git(t.Context(), d, "commit", "--quiet", "-m", "Fix a"); err != nil {
				t.Error(err)
			}
			return "text Committed: src/a.txt. Left: shot.png (a screenshot of the check), .env (a secret)."
		})

		in.pass(t, 0)
		w2 := in.reviewer(t, w1)
		for _, s := range []string{"Its worker's final note:\n\n    The fix is in the worktree: nothing committed.\n",
			"     M app/src/a.txt\n    ?? app/.env  (a secret or an env file: never commit it)\n" +
				"    ?? app/shot.png  (an image or a video: a screenshot, unless the change uses it)\n",
			"new file app/shot.png: binary", "A worker may leave real work not committed, even saying it commits nothing: commit it."} {
			if !strings.Contains(prompt, s) {
				t.Errorf("W2's prompt lacks %q:\n%s", s, prompt)
			}
		}

		in.pass(t, time.Second)
		if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
			t.Fatalf("after the review: %s", got)
		}
		tip := in.tip(t)
		if out := in.git(t, in.repo, "ls-tree", "-r", "--name-only", tip, "app"); strings.Contains(out, "shot.png") || strings.Contains(out, ".env") {
			t.Errorf("the branch holds what the review left:\n%s", out)
		}
		if out := in.git(t, in.repo, "show", tip+":app/src/a.txt"); out != "a, fixed" {
			t.Errorf("a.txt in the branch: %q", out)
		}
		if status, _ := git(t.Context(), w1.GetWorktree(), "status", "--porcelain"); status != "?? app/.env\n?? app/shot.png" {
			t.Errorf("the worktree keeps:\n%s", status)
		}
		want := "integration: W2 left not committed, as its note says: app/.env, app/shot.png; its worktree keeps them"
		if texts := storedTexts(t, in.db, w2.GetId()); !slices.Contains(texts, want) {
			t.Errorf("W2's events %q lack %q", texts, want)
		}
		var merged *planv1.LeadEvent
		for _, e := range queue.all() {
			if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED {
				merged = e
			}
		}
		detail := "W2 reviewed the work W1 left not committed: Committed: src/a.txt. Left: shot.png (a screenshot of the check), " +
			".env (a secret). Its worktree keeps what it left: " + w1.GetWorktree()
		if merged == nil || merged.GetText() != "Djinn: W2 merged into "+in.branch+" as "+tip[:8] || merged.GetDetail() != detail {
			t.Errorf("the merge event %v; want its detail %q", merged, detail)
		}
	})

	// A review worker that fails, then one that leaves the work not committed in turn, spend the attempts;
	// trying again starts a new review, its attempts counted from one.
	t.Run("attempts_then_question", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		in := integrating(t)
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n"})
		reviews := 0
		in.correctWith(func(d, _ string) string {
			in.mu.Lock()
			defer in.mu.Unlock()
			switch reviews++; reviews {
			case 1:
				return "fail I cannot tell what belongs to it"
			case 2:
				return "text I leave it as it is."
			}
			commitAll(t, d, "Add b")
			return ""
		})

		in.pass(t, 0)
		w2 := in.reviewer(t, w1)
		if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
			t.Fatalf("W2 %v", w2)
		}
		in.pass(t, time.Second)
		w3 := in.reviewer(t, w1)
		if w3.GetReview().GetAttempt() != 2 || !slices.Equal(w3.GetReview().GetTaskIds(), []string{w1.GetId()}) {
			t.Fatalf("the second attempt %v", w3)
		}
		if prompt, _ := firstPrompt(in.db, w3.GetId()); !strings.HasSuffix(prompt, "This is attempt 2: the review before yours left changes not committed.") {
			t.Errorf("W3's prompt ends:\n%s", prompt[max(0, len(prompt)-200):])
		}
		if _, texts := in.integration(t, w1); texts[len(texts)-1] != "W2, its review worker, failed: I cannot tell what belongs to it; "+
			"W3 reviews it, attempt 2 of 2" {
			t.Errorf("W1's events %q", texts)
		}
		in.pass(t, time.Second)
		if got := in.states(t, w1, w3); got != "W1 UNCOMMITTED, W3 UNCOMMITTED" {
			t.Fatalf("two attempts spent: %s", got)
		}
		if texts := storedTexts(t, in.db, w3.GetId()); slices.ContainsFunc(texts, func(s string) bool { return strings.Contains(s, "as its note says") }) {
			t.Errorf("W3's leftover taken as judged: %q", texts)
		}
		got, texts := in.integration(t, w3)
		if got.GetQuestionId() != "" || got.GetAttempts() != 2 || got.GetReviewedBy() != "" ||
			!strings.HasSuffix(texts[len(texts)-1], "; left to the lead") {
			t.Errorf("W3's integration %v; events %q", got, texts)
		}
		in.pass(t, time.Hour)
		if n := len(in.wishTasks(t)); n != 3 || len(in.commits(t)) != 0 {
			t.Fatalf("%d tasks, commits %v; want 3 tasks, no commit", n, in.commits(t))
		}

		q := in.attachQuestion(t, w1, w3)
		if did := in.answer(t, q, planv1.Choice_CHOICE_A); did != "Djinn started W4: Merge: Work of W1 (W1) — work left not committed" {
			t.Errorf("what Djinn did: %q", did)
		}
		got, texts = in.integration(t, w1)
		if got.GetQuestionId() != "" || got.GetAttempts() != 1 || got.GetReason() != "uncommitted: reviewed by W4" ||
			texts[len(texts)-1] != "you said to try again ("+q.GetCode()+"); W4 reviews it, attempt 1 of 2" {
			t.Fatalf("tried again: %v; events %q", got, texts)
		}
		w4 := in.reviewer(t, w1)
		if prompt, _ := firstPrompt(in.db, w4.GetId()); !strings.HasSuffix(prompt,
			"The review before yours left these paths not committed without naming them after Left: in its final note: app/src/b.txt.") {
			t.Errorf("W4's prompt ends:\n%s", prompt[max(0, len(prompt)-200):])
		}
		in.pass(t, time.Second)
		if got := in.states(t, w1, w3, w4); got != "W1 COMMITTED, W3 COMMITTED, W4 COMMITTED" {
			t.Errorf("after the third review: %s", got)
		}
		if out := in.git(t, in.repo, "show", in.tip(t)+":app/src/b.txt"); out != "b" {
			t.Errorf("b.txt in the branch: %q", out)
		}
	})

	// A correction worker that leaves its work not committed is not merged either: a review worker commits it, in the
	// correction's worktree, which concludes the merge; its success brings in the correction's work and the work it corrected.
	t.Run("correction_work", func(t *testing.T) {
		testx.Portable(t)
		t.Parallel()
		in := integrating(t)
		azima := in.azima(t, in.wishID, "The readme")
		partOf := func(task *planv1.Task) { task.PartOf = azima.GetId() }
		w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"}, partOf)
		w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"}, partOf)
		in.correctWith(func(d, prompt string) string {
			if strings.HasPrefix(prompt, "Djinn could not integrate") {
				writeFile(t, d, "README.md", "# One and Two\n") // Settled, not committed.
				return ""
			}
			commitAll(t, d, "Settle the readme")
			return ""
		})

		in.pass(t, 0) // W1 goes in; W2 conflicts with it, which starts W3.
		w3 := in.correction(t, w2)
		in.pass(t, time.Second)
		w4 := in.reviewer(t, w3)
		if !slices.Equal(w4.GetReview().GetTaskIds(), []string{w3.GetId()}) || w4.GetWorktree() != w3.GetWorktree() {
			t.Fatalf("the review of W3's work %v", w4)
		}
		if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 CONFLICT, W3 UNCOMMITTED" {
			t.Fatalf("W3's work not committed: %s", got)
		}
		if n := len(in.commits(t)); n != 1 {
			t.Fatalf("%d commits; want W1's alone: %v", n, in.commits(t))
		}
		in.pass(t, time.Second)
		if got := in.states(t, w1, w2, w3, w4); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED, W4 COMMITTED" {
			t.Fatalf("after the review: %s", got)
		}
		tip := in.tip(t)
		if got, _ := in.integration(t, w2); got.GetReason() != "corrected by W3" || got.GetSha() != tip {
			t.Errorf("W2's integration %v", got)
		}
		if got, _ := in.integration(t, w3); got.GetReason() != "uncommitted: reviewed by W4" {
			t.Errorf("W3's integration %v", got)
		}
		if out := in.git(t, in.repo, "show", tip+":app/README.md"); out != "# One and Two" {
			t.Errorf("README.md in the branch: %q", out)
		}
		if _, err := os.Stat(w3.GetWorktree()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("W3's worktree, all committed, is still there: %v", err)
		}
		for _, task := range []*planv1.Task{w1, w2} {
			if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", task.GetBranch(), tip); err != nil {
				t.Errorf("%s's branch is not in %s: %v", task.GetCode(), in.branch, err)
			}
		}
	})

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, in *integration, w1 *planv1.Task, azima *planv1.Task, lead *leadListener)
	}{
		{
			name: "fails_after_commit",
			run: func(t *testing.T, in *integration, w1 *planv1.Task, _ *planv1.Task, _ *leadListener) {
				in.correctWith(func(d, _ string) string {
					commitAll(t, d, "Add b")
					return "fail out of budget"
				})
				in.pass(t, 0)
				in.reviewer(t, w1)
				in.pass(t, time.Second) // W2 failed: W1 waits again.
				in.pass(t, time.Second)
				got, texts := in.integration(t, w1)
				if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || !slices.Contains(texts,
					"W2, its review worker, failed: out of budget; W1's worktree holds nothing not committed any more: it waits to be merged") {
					t.Fatalf("W1's integration %v; events %q", got, texts)
				}
				if out := in.git(t, in.repo, "show", in.tip(t)+":app/src/b.txt"); out != "b" {
					t.Errorf("b.txt in the branch: %q", out)
				}
			},
		},
		{
			name: "azima_gone",
			run: func(t *testing.T, in *integration, w1 *planv1.Task, azima *planv1.Task, _ *leadListener) {
				dropTask(t, in.db, azima)
				in.correctWith(func(string, string) string { return "" })
				in.pass(t, 0)
				w2 := in.reviewer(t, w1)
				if w2.GetPartOf() != "" {
					t.Errorf("W2 part of %q, an azima that is gone", w2.GetPartOf())
				}
				want := "part of no azima: W1 was part of " + azima.GetId()[:8] + ", which is no longer one of the wish's azimas"
				if texts := storedTexts(t, in.db, w2.GetId()); !slices.Contains(texts, want) {
					t.Errorf("W2's events %q lack %q", texts, want)
				}
			},
		},
		{
			name: "cannot_start",
			run: func(t *testing.T, in *integration, w1 *planv1.Task, _ *planv1.Task, lead *leadListener) {
				delete(in.h.providers, planv1.Provider_PROVIDER_CLAUDE) // The project's provider: its review worker cannot start.
				in.pass(t, 0)
				got, texts := in.integration(t, w1)
				if got.GetReviewedBy() != "" ||
					!strings.Contains(texts[len(texts)-1], "; the review worker could not start: invalid_argument: provider") ||
					!strings.HasSuffix(texts[len(texts)-1], "; left to the lead") {
					t.Errorf("W1's integration %v; events %q", got, texts)
				}
				if got.GetQuestionId() != "" {
					t.Errorf("W1 question_id = %q; want empty", got.GetQuestionId())
				}
				questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(questions) != 0 {
					t.Errorf("got %d questions; want 0", len(questions))
				}
				if paras := lead.all(); len(paras) == 0 || !strings.Contains(paras[len(paras)-1], "The move is yours: retry a review, drop the work") {
					t.Errorf("expected lead notification, got %v", paras)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testx.Portable(t)
			t.Parallel()
			in := integrating(t)
			lead := &leadListener{}
			in.h.TellLeads(lead.tell)
			azima := in.azima(t, in.wishID, "Absorbed")
			w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
			in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n"})
			tc.run(t, in, w1, azima, lead)
		})
	}
}

// TestReviewReport verifies review note parsing, path matching, hints, and permissions.
func TestReviewReport(t *testing.T) {
	t.Parallel()

	t.Run("named", func(t *testing.T) {
		for _, c := range []struct {
			note, path string
			want       bool
		}{
			{"Left: app/shot.png (a screenshot).", "app/shot.png", true},
			{"Left: shot.png (a screenshot).", "app/shot.png", true},
			{"Left: src/b.txt.", "app/src/b.txt", true},
			{"Left: `test-results/` (outputs).", "app/test-results/run/trace.zip", true},
			{"Left: app/test-results/ (outputs).", "app/test-results/run/trace.zip", true},
			{"Left: shot.png.bak (a backup).", "app/shot.png", false},
			{"Left: myshot.png.", "app/shot.png", false},
			{"Left: test-results (outputs).", "app/test-results/run/trace.zip", false},
			{"I leave it as it is.", "app/src/b.txt", false},
			{"", "a", false},
		} {
			if got := named(c.note, c.path); got != c.want {
				t.Errorf("named(%q, %q) = %v; want %v", c.note, c.path, got, c.want)
			}
		}
	})

	t.Run("path_hint", func(t *testing.T) {
		const secret, output, image = "a secret or an env file: never commit it",
			"a build or test output, a log or a leftover: leave it out unless the change makes it",
			"an image or a video: a screenshot, unless the change uses it"
		for status, want := range map[string]string{
			"?? .env":                          secret,
			"?? app/.env.local":                secret,
			"?? app/.env.example":              "",
			"?? certs/server.pem":              secret,
			"A  id_ed25519":                    secret,
			"?? e2e/test-results/a/trace.zip":  output,
			"?? debug.log":                     output,
			"?? src/main.go.orig":              output,
			"?? shot.png":                      image,
			"?? docs/site/img/logo.png":        image,
			" M debug.log":                     "",
			"?? src/build.go":                  "",
			"?? internal/plan/build/x.go":      output,
			"R  new.png <- old.png":            "",
			"?? internal/harness/review.go":    "",
			"?? coverage/lcov.info":            output,
			"?? node_modules/.package-lock.js": output,
		} {
			if got := pathHint(status); got != want {
				t.Errorf("pathHint(%q) = %q; want %q", status, got, want)
			}
		}
	})

	t.Run("report", func(t *testing.T) {
		for note, want := range map[string]string{
			"Done.\n\n**Committed:** a.go. **Left:** shot.png (a screenshot).": "Committed: a.go. Left: shot.png (a screenshot).",
			"Done.\n\nCommitted: a.go. Left: nothing.\n":                       "Committed: a.go. Left: nothing.",
			"- Committed: a.go.\nCommitted: a.go, b.go.\nLeft: c (in doubt).":  "Committed: a.go, b.go. Left: c (in doubt).",
			"I kept everything.\nNothing else.":                                "",
		} {
			if got := reviewReport(note); got != want {
				t.Errorf("reviewReport(%q) = %q; want %q", note, got, want)
			}
		}
		paths := []string{"app/src/a.go", "app/shot.png", "app/out.log"}
		for note, want := range map[string][]string{
			"Committed: src/a.go. Left: shot.png (a screenshot), out.log (a log).":              {"app/src/a.go"},
			"Committed: src/a.go, shot.png, out.log. Left: nothing.":                            paths,
			"I committed src/a.go and left shot.png and out.log.":                               paths,
			"Committed: nothing.\nLeft: app/ (in doubt: the task's worker says it is a draft).": nil,
			"Committed: src/a.go. Left: shot.png.":                                              {"app/src/a.go", "app/out.log"},
		} {
			if got := unnamed(note, paths); !slices.Equal(got, want) {
				t.Errorf("unnamed(%q) = %q; want %q", note, got, want)
			}
		}
	})

	t.Run("permissions", func(t *testing.T) {
		project := &djinnv1.Permissions{Edit: true, Commands: []string{"go test", "git clean"}}
		review := withCommit(&planv1.Task{Review: &planv1.TaskReview{}}, project)
		for _, c := range []string{"git add -A", "git add . && git commit -m x", "git commit -am x", "git clean -fdx", "git stash push"} {
			if commandAllowed(review, c) {
				t.Errorf("a review worker may run %q", c)
			}
		}
		for _, c := range []string{"git add -- src/a.go", "git add .agents/skills/a.md", "git commit -m x", "git restore --staged a.go", "go test ./..."} {
			if !commandAllowed(review, c) {
				t.Errorf("a review worker may not run %q", c)
			}
		}
		if deny := claudePermissions(review)["deny"].([]string); !slices.Contains(deny, "Bash(git add -A *)") ||
			!slices.Contains(deny, "Bash(git clean *)") {
			t.Errorf("Claude's deny rules %q", deny)
		}
		correction := withCommit(&planv1.Task{Correction: &planv1.TaskCorrection{}}, project)
		if !commandAllowed(correction, "git clean -fd") || len(correction.GetDeniedCommands()) != 0 {
			t.Errorf("a correction worker's permissions %v", correction)
		}
		if len(project.GetDeniedCommands()) != 0 {
			t.Errorf("the project's permissions changed: %v", project)
		}
	})
}
