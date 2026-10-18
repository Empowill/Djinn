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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// checking writes the project's settings as the developer's file, with what each fake command named exits with.
func (in *integration) checking(t *testing.T, settings string, exits map[string]int) {
	t.Helper()
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"), settings)
	in.mu.Lock()
	defer in.mu.Unlock()
	in.exits = exits
}

// exit sets what the fake command name exits with.
func (in *integration) exit(name string, code int) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.exits[name] = code
}

// ran are the commands run since the last call, by their first word, with the gates taken: "setup W1".
func (in *integration) ran() (runs, gates []string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, r := range in.runs {
		runs = append(runs, strings.Fields(r)[0])
	}
	gates, in.runs, in.gates = in.gates, nil, nil
	return runs, gates
}

// checkRuns are the project's last runs of its checks and its setup: "lint ok at 1a2b3c4d".
func (in *integration) checkRuns(t *testing.T) []string {
	t.Helper()
	project, err := store.Get[*planv1.Project](t.Context(), in.db, in.projectID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range project.GetCheckRuns() {
		state := "red"
		if r.GetPassed() {
			state = "ok"
		}
		out = append(out, r.GetName()+" "+state+" at "+short8(r.GetSha()))
	}
	slices.Sort(out)
	return out
}

const lintAtCommit = "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"

// linted records a run of lint by task's worker through djinn gate run, as the costs service keeps it: exiting code,
// on the tree of its worktree's last commit.
func (in *integration) linted(t *testing.T, task *planv1.Task, code int32) {
	t.Helper()
	tree := in.git(t, task.GetWorktree(), "rev-parse", "HEAD^{tree}")
	err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		all, err := store.List[*machinev1.CommandCost](t.Context(), tx, store.Where{"project_id": in.projectID})
		if err != nil {
			return err
		}
		cost := &machinev1.CommandCost{Id: store.NewID(), ProjectId: in.projectID, Command: "lint", Gate: "lint"}
		if len(all) > 0 {
			cost = all[0]
		}
		cost.RecentRuns = append(cost.RecentRuns, &machinev1.CommandRun{Gate: "lint", ExitCode: code, TaskId: task.GetId(), Tree: tree})
		if err := tx.Journal("local", "test/linted", cost); err != nil {
			return err
		}
		return tx.Put(cost)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// questions are the questions of the wish, open or closed.
func (in *integration) questions(t *testing.T) []*planv1.Question {
	t.Helper()
	qs, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// handCommit commits on the integration branch, as the developer does by hand, in its checkout: a file of its own, so
// the tree changes (push checks are not run twice on a tree, W245); the push checks' fake says whether they pass. It
// returns the new tip.
func (in *integration) handCommit(t *testing.T) string {
	t.Helper()
	fixTests(t, filepath.Join(in.repo, "app"), "Fix the tests by hand")
	return in.tip(t)
}

// fixTests commits a fix in dir, a worktree or a folder of one, as a worker or the developer does: a file of its own,
// which changes the tree; the push checks' fake says whether they pass.
func fixTests(t *testing.T, dir, message string) {
	t.Helper()
	writeFile(t, dir, "FIXED.md", message+"\n")
	commitAll(t, dir, message)
}

// withdraw withdraws the question q, as the lead does with djinn question withdraw.
func (in *integration) withdraw(t *testing.T, q *planv1.Question) {
	t.Helper()
	q = proto.CloneOf(q)
	q.Withdrawal = &planv1.Withdrawal{Note: "asked by mistake", CreateTime: timestamppb.Now(), Actor: planv1.Closer_CLOSER_LEAD}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/withdraw", q); err != nil {
			return err
		}
		return tx.Put(q)
	}); err != nil {
		t.Fatal(err)
	}
}

// leftToTheLead makes the push of W1's work red, its correction attempts none: left to the lead at the branch's tip.
func (in *integration) leftToTheLead(t *testing.T) (bare, old string) {
	t.Helper()
	in.checking(t, "correction_attempts: 0\n"+
		"checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
		"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
	in.testCode = 1
	bare = in.remote(t)
	old = in.tip(t)
	azima := in.azima(t, in.wishID, "Letters")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	in.pass(t, 0)
	if state := in.pushState(t); !strings.HasSuffix(state.GetHeld(), "; left to the lead") || state.GetFailedSha() != in.tip(t) {
		t.Fatalf("the push %v; want held, left to the lead at the branch's tip", state)
	}
	return bare, old
}

// uncheckedDew is wish Dew as the incident of its W4 found it (12/10/2026): two Git projects, the second naming no
// check, its checkout on main and clean; a fake worker that commits a file in its worktree. It returns the second
// project's folder, with the wish and the project.
func uncheckedDew(t *testing.T) (e *env, queue *leadQueue, wishID, projectID, repo string) {
	t.Helper()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_FAKE] = corrector{work: func(dir, _ string) string {
		if err := os.WriteFile(filepath.Join(dir, "silae.txt"), []byte("columns\n"), 0o600); err != nil {
			return "fail " + err.Error()
		}
		for _, args := range [][]string{{"add", "silae.txt"}, {"commit", "--quiet", "-m", "silae: a refused read leaves its columns"}} {
			if out, err := git(context.Background(), dir, args...); err != nil {
				return "fail " + err.Error() + ": " + out
			}
		}
		return "text Columns left out of a refused read."
	}}
	e = upWith(t, t.TempDir(), providers)
	queue = &leadQueue{}
	e.h.NotifyLead(queue.notify)
	wishID, projectID = twoProjects(t, e)
	project, err := store.Get[*planv1.Project](t.Context(), e.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	repo = project.GetDirectory()
	if _, err := git(t.Context(), repo, "branch", "-M", "main"); err != nil {
		t.Fatal(err)
	}
	// The wish was made with the checkout on main: its integration branch there.
	if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: wishID, ProjectId: projectID, Branch: "main",
	})); err != nil {
		t.Fatal(err)
	}
	return e, queue, wishID, projectID, repo
}

// spawnDone spawns the work of W4 in the project and waits for its fake worker to end.
func spawnDone(t *testing.T, e *env, wishID, projectID string) *planv1.Task {
	t.Helper()
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, ProjectId: projectID, Title: "Silae: a refused read leaves its columns",
		Prompt: "Leave the columns of a refused read.", Provider: planv1.Provider_PROVIDER_FAKE,
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

// leadLines are the lead's events about code, kind and text.
func leadLines(q *leadQueue, code string) []string {
	var lines []string
	for _, e := range q.all() {
		if e.GetCode() == code {
			lines = append(lines, strings.ToLower(strings.TrimPrefix(e.GetKind().String(), "LEAD_EVENT_KIND_"))+" "+e.GetText())
		}
	}
	return lines
}

// integratedInto checks that task's work is committed into branch of repo, its commit there, the checkout left on main
// at main, clean, and the lead told once, by the merge line.
func integratedInto(t *testing.T, e *env, queue *leadQueue, task *planv1.Task, repo, branch, main string) {
	t.Helper()
	got, err := store.Get[*planv1.Task](t.Context(), e.db, task.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if in := got.GetIntegration(); in.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || in.GetBranch() != branch {
		t.Fatalf("integration %v, want committed into %s", in, branch)
	}
	if _, err := git(t.Context(), repo, "merge-base", "--is-ancestor", "refs/heads/"+task.GetBranch(), "refs/heads/"+branch); err != nil {
		t.Errorf("%s does not hold the work of %s", branch, task.GetBranch())
	}
	head, _ := git(t.Context(), repo, "symbolic-ref", "--short", "HEAD")
	tip, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
	status, _ := git(t.Context(), repo, "status", "--porcelain")
	if head != "main" || tip != main || status != "" {
		t.Errorf("the checkout is on %q at %s, changes %q: want main at %s, clean", head, tip, status, main)
	}
	lines := leadLines(queue, task.GetCode())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "merged ") || !strings.Contains(lines[0], branch) {
		t.Errorf("lead events %q, want one merge line into %s", lines, branch)
	}
}

// TestEnsureSetup: the setup makes the integration worktree ready before its first check, and runs again only when a
// lock file changes, the worktree is made anew, or node_modules is missing beside package.json.
func TestEnsureSetup(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("once per worktree", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "setup: \"setup\"\n"+lintAtCommit, map[string]int{"setup": 0, "lint": 0})
		in.ran()

		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
		in.pass(t, 0)
		runs, gates := in.ran()
		if !slices.Equal(runs, []string{"setup", "lint"}) || !slices.Equal(gates, []string{"setup W1", "lint W1"}) {
			t.Errorf("W1: runs %q, gates %q; want setup then lint, each through its gate", runs, gates)
		}
		if got, want := in.checkRuns(t), []string{"lint ok at " + short8(in.tip(t)), "setup ok at " + short8(in.tip(t))}; !slices.Equal(got, want) {
			t.Errorf("the project's runs %q; want %q", got, want)
		}

		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint"}) {
			t.Errorf("W2, no lock file changed: %q; want lint only", runs)
		}
	})

	t.Run("again on lockfile or new worktree", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "setup: \"setup\"\n"+lintAtCommit, map[string]int{"setup": 0, "lint": 0})
		in.finished(t, "W1", map[string]string{"app/package-lock.json": "{}\n"})
		in.pass(t, 0)
		in.ran()

		in.finished(t, "W2", map[string]string{"app/package-lock.json": "{\"lockfileVersion\": 3}\n"})
		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"setup", "lint"}) {
			t.Errorf("W2 changes a lock file: %q; want setup then lint", runs)
		}

		if err := os.RemoveAll(integrationDir(in.home, in.projectID, in.wishID)); err != nil {
			t.Fatal(err)
		}
		in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"setup", "lint"}) {
			t.Errorf("W3, in a worktree made anew: %q; want setup then lint", runs)
		}
	})

	t.Run("when node_modules missing", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "setup: \"setup\"\n"+lintAtCommit, map[string]int{"setup": 0, "lint": 0})
		in.finished(t, "W1", map[string]string{"app/package.json": "{}\n", "app/.gitignore": "node_modules\n"})
		in.pass(t, 0)
		in.ran()

		in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"setup", "lint"}) {
			t.Errorf("W2 with missing node_modules: %q; want setup then lint", runs)
		}

		wt := integrationDir(in.home, in.projectID, in.wishID)
		nm := filepath.Join(wt, "app", "node_modules")
		if err := os.MkdirAll(nm, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nm, "pkg.txt"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		in.ran()

		in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint"}) {
			t.Errorf("W3 with present node_modules: %q; want lint only", runs)
		}
	})
}

// TestCommitCheckPassed: Djinn does not run a commit check again when the task's worker passed it, through
// its gate, on the very tree Djinn is about to commit, and says so in the task's events; nor the setup, nothing left
// to run. A file changed after the worker's lint, or a lint that failed last, and the check runs as before.
func TestCommitCheckPassed(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.checking(t, "setup: \"setup\"\n"+lintAtCommit, map[string]int{"setup": 0, "lint": 0})
	const skipped = "commit checks: lint passed by the worker on this tree, not run again"
	said := func(task *planv1.Task) bool {
		return slices.ContainsFunc(in.events(t, task), func(e *planv1.TaskEvent) bool { return e.GetText() == skipped })
	}

	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.linted(t, w1, 0)
	in.pass(t, 0)
	if runs, gates := in.ran(); len(runs) != 0 || len(gates) != 0 {
		t.Errorf("W1, linted by its worker on the same tree: runs %q, gates %q; want none", runs, gates)
	}
	if got := in.states(t, w1); got != "W1 COMMITTED" || !said(w1) {
		t.Errorf("W1: %s, said %v", got, said(w1))
	}

	// The worker linted, then changed a file and committed it: Djinn lints.
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.linted(t, w2, 0)
	in.leave(t, w2, map[string]string{"app/src/b.txt": "b, changed after the lint\n"})
	commitAll(t, w2.GetWorktree(), "Change after the lint")
	in.pass(t, time.Second)
	if runs, _ := in.ran(); !slices.Equal(runs, []string{"setup", "lint"}) || said(w2) {
		t.Errorf("W2, a file changed after the worker's lint: runs %q, said %v; want setup then lint", runs, said(w2))
	}

	// The worker's last lint failed on this very tree, an earlier one passed: Djinn lints, and its red stops the merge.
	w3 := in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
	in.linted(t, w3, 0)
	in.linted(t, w3, 1)
	in.exit("lint", 1)
	in.pass(t, time.Second)
	if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint"}) || said(w3) {
		t.Errorf("W3, its worker's last lint failed: runs %q, said %v; want lint", runs, said(w3))
	}
	if got := in.states(t, w3); got != "W3 RED" {
		t.Errorf("W3: %s; want RED from Djinn's own lint", got)
	}
}

// TestCheck_RedCommitCheck: the checks run before a commit, in their order; a red one fails the merge, as red tests did,
// and starts a correction worker, whose first prompt says the checks Djinn runs and when. A push check does not run
// at the commit.
func TestCheck_RedCommitCheck(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.checking(t, "setup: \"setup\"\n"+lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n",
		map[string]int{"setup": 0, "lint": 1})
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	var prompt string
	in.correctWith(func(_, p string) string {
		prompt = p
		in.exit("lint", 0)
		return "text fixed"
	})

	in.pass(t, 0)
	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_RED || got.GetReason() != "lint exited 1:\nlint ran" {
		t.Fatalf("W1's integration %v; events %q", got, texts)
	}
	if runs, _ := in.ran(); slices.Contains(runs, "test") {
		t.Errorf("a push check ran at the commit: %q", runs)
	}
	w2 := in.correction(t, w1)
	for _, s := range []string{"The command, run in the project's folder: `lint`", "its commit checks (`djinn gate run lint -- lint`)",
		"Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- lint`; before it pushes, " +
			"with `djinn gate run test -- test`. A worker runs the commit checks before it ends", "A fresh worktree needs `setup` first."} {
		if !strings.Contains(prompt, s) {
			t.Errorf("%s's prompt lacks %q:\n%s", w2.GetCode(), s, prompt)
		}
	}
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
		t.Errorf("after the correction: %s", got)
	}
}

// TestHoldPush: before a push, the push checks run on the branch's tip; red, the push is held, corrected or left to
// the lead, and cleared once green or pushed without checks.
func TestHoldPush(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("red holds and corrects then retries", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode, in.testOut = 1, "--- FAIL: TestLogin"
		bare := in.remote(t)
		old := in.tip(t)
		first, second := in.azima(t, in.wishID, "First"), in.azima(t, in.wishID, "Second")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = first.GetId() })
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = second.GetId() })

		var (
			attempts int
			prompt   string
		)
		in.correctWith(func(dir, p string) string {
			attempts++
			prompt = p
			if attempts == 1 {
				return "wait\nfail could not fix tests"
			}
			fixTests(t, dir, "Fix the tests")
			return "text push checks fixed"
		})

		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed with its push checks red: origin at %s", got)
		}
		_, texts := in.integration(t, w1)
		held := "the push of " + in.branch + " is held: its push checks are red at "
		if !slices.ContainsFunc(texts, func(s string) bool { return strings.HasPrefix(s, held) && strings.HasSuffix(s, "test: test exited 1") }) {
			t.Errorf("W1's events %q lack %q", texts, held)
		}
		_, gates := in.ran()
		if !slices.Contains(gates, "test -") || slices.Contains(gates, "lint -") {
			t.Errorf("gates %q; want the push check test through its gate, the commit check lint not again", gates)
		}
		state := in.pushState(t)
		if !strings.Contains(state.GetHeld(), "test: test exited 1") || state.GetHeldRuns() != 1 {
			t.Fatalf("the push %v; want held once, W1 and W2 merged as one batch", state)
		}
		if state.GetCorrectedBy() == "" || state.GetAttempts() != 1 {
			t.Fatalf("the push %v; want corrected by a worker, attempt 1", state)
		}
		w3ID := state.GetCorrectedBy()
		w3 := in.get(t, w3ID)
		if w3.GetCode() != "W3" {
			t.Errorf("correction worker code %s; want W3", w3.GetCode())
		}
		for _, s := range []string{
			"The push checks of " + in.branch + " failed: test: test exited 1.",
			"The command, run in the project's folder: `test`",
			"--- FAIL: TestLogin",
		} {
			if !strings.Contains(prompt, s) {
				t.Errorf("W3 prompt lacks %q:\n%s", s, prompt)
			}
		}
		if _, texts := in.integration(t, w1); !slices.Contains(texts, "the push of "+in.branch+" is held, corrected by "+w3.GetCode()) {
			t.Errorf("W1's events %q lack push held corrected by W3", texts)
		}
		if _, texts := in.integration(t, w2); !slices.Contains(texts, "the push of "+in.branch+" is held, corrected by "+w3.GetCode()) {
			t.Errorf("W2's events %q lack push held corrected by W3", texts)
		}

		// While W3 runs, a second pass does not spawn another correction worker.
		in.pass(t, 0)
		if got := in.pushState(t).GetCorrectedBy(); got != w3ID {
			t.Errorf("second pass while running changed correction worker to %s; want %s", got, w3ID)
		}

		// W3 fails once released: left to the lead.
		if _, err := in.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: w3ID, Text: "wake"})); err != nil {
			t.Fatal(err)
		}
		in.ended(t, w3ID)
		in.pass(t, time.Second)

		state = in.pushState(t)
		if state.GetQuestionId() != "" {
			t.Fatalf("push question %s; want none (left to the lead)", state.GetQuestionId())
		}
		if !strings.HasSuffix(state.GetHeld(), "; left to the lead") {
			t.Fatalf("push held %q; want suffix '; left to the lead'", state.GetHeld())
		}
		if _, texts := in.integration(t, w2); !slices.Contains(texts, "the push checks of "+in.branch+" stay red; left to the lead") {
			t.Errorf("W2's events %q lack left to the lead", texts)
		}

		// Answering Choice A retries with a new correction worker W4.
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		q := in.attachPushQuestion(t)
		if did := in.answer(t, q, planv1.Choice_CHOICE_A); !strings.HasPrefix(did, "Djinn started W4:") {
			t.Errorf("what Djinn did: %q", did)
		}
		w4 := in.ended(t, in.pushState(t).GetCorrectedBy())
		if w4.GetCode() != "W4" {
			t.Errorf("retried worker code %s; want W4", w4.GetCode())
		}
		in.pass(t, time.Second)
		if got, tip := in.remoteTip(t, bare), in.tip(t); got != tip {
			t.Fatalf("checked again green, not pushed: origin at %s, the branch at %s", got, tip)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetHeldRuns() != 0 || state.GetQuestionId() != "" {
			t.Errorf("the push still held: %v", state)
		}
	})

	t.Run("push without checks", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "correction_attempts: 0\n"+
			"checks { name: \"lint\" command: \"lint\" when: [CHECK_WHEN_COMMIT, CHECK_WHEN_PUSH] }\n"+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "Letters")
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })

		in.pass(t, 0)
		if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint", "test"}) {
			t.Errorf("runs %q; want lint at the commit, then test before the push, lint not again", runs)
		}
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}
		if state := in.pushState(t); state.GetHeldRuns() != 1 || state.GetQuestionId() != "" || !strings.HasSuffix(state.GetHeld(), "; left to the lead") {
			t.Fatalf("no work left: the push %v; want left to the lead at once", state)
		}
		q := in.attachPushQuestion(t)
		if did := in.answer(t, q, planv1.Choice_CHOICE_C); did != "Djinn pushes at the integration's next pass, without the push checks" {
			t.Errorf("what Djinn did: %q", did)
		}
		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("not pushed without the checks: origin at %s", got)
		}
		if runs, _ := in.ran(); slices.Contains(runs, "test") {
			t.Errorf("the push checks ran again: %q", runs)
		}
		text := "pushed " + in.branch + " to origin as " + tip[:8] + ", 1 commit (you said to push it; without the push checks, as you said)"
		if _, texts := in.integration(t, w1); !slices.Contains(texts, text) {
			t.Errorf("W1's events %q lack %q", texts, text)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetUnchecked() {
			t.Errorf("after the push: %v", state)
		}
	})

	t.Run("correction stopped then continued", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		var starts int
		in.correctWith(func(dir, prompt string) string {
			starts++
			if starts == 1 {
				return "wait\ntext stopped before this"
			}
			fixTests(t, dir, "Fix the tests")
			return "text push checks fixed"
		})

		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}
		w2ID := in.pushState(t).GetCorrectedBy()
		if w2ID == "" {
			t.Fatalf("the push %v; want corrected by a worker", in.pushState(t))
		}

		if _, err := in.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: w2ID})); err != nil {
			t.Fatal(err)
		}
		in.pass(t, time.Second)
		state := in.pushState(t)
		if state.GetQuestionId() != "" || strings.HasSuffix(state.GetHeld(), "; left to the lead") || state.GetCorrectedBy() != w2ID {
			t.Fatalf("just after the stop, the push %v; want still held, corrected by the stopped worker, nobody asked", state)
		}

		if _, err := in.tasks.Continue(t.Context(), connect.NewRequest(&planv1.TaskServiceContinueRequest{TaskId: w2ID})); err != nil {
			t.Fatal(err)
		}
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		if w2 := in.ended(t, w2ID); w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Fatalf("continued correction %s ended %v", w2.GetCode(), w2.GetStatus())
		}
		in.pass(t, stopGrace)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("correction committed, push checks green, not pushed: origin at %s, the branch at %s; push %v", got, tip, in.pushState(t))
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" || state.GetQuestionId() != "" {
			t.Errorf("after the push, the push still held: %v", state)
		}
		if qs := in.questions(t); len(qs) != 0 {
			t.Errorf("questions %v; want none", qs)
		}
		tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(slices.DeleteFunc(tasks, func(t *planv1.Task) bool { return t.GetCorrection() == nil })); n != 1 {
			t.Errorf("%d correction workers; want the one, continued", n)
		}
	})

	t.Run("question withdrawn", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare, old := in.leftToTheLead(t)
		q := in.attachPushQuestion(t)
		tip := in.handCommit(t)
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()

		in.pass(t, time.Second)
		if got := in.remoteTip(t, bare); got != old || in.pushState(t).GetQuestionId() != q.GetId() {
			t.Fatalf("pushed while Djinn's question was open: origin at %s, the push %v", got, in.pushState(t))
		}

		in.withdraw(t, q)
		in.pass(t, time.Second)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("question withdrawn, branch green: not pushed: origin at %s, the branch at %s; the push %v", got, tip, in.pushState(t))
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetQuestionId() != "" {
			t.Errorf("after the push, the push still held: %v", state)
		}
	})

	t.Run("pushed by hand while held", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		bare, _ := in.leftToTheLead(t)
		tip := in.handCommit(t)
		in.git(t, in.repo, "push", "--quiet", "origin", in.branch)
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		in.ran()

		in.pass(t, time.Second)
		if runs, _ := in.ran(); !slices.Contains(runs, "test") {
			t.Errorf("runs %q; want the push checks on the branch pushed by hand", runs)
		}
		state := in.pushState(t)
		if state.GetHeld() != "" || state.GetFailedSha() != "" || state.GetLast().GetNewSha() != tip {
			t.Fatalf("the push %v; want no longer held, last pushed at %s", state, tip)
		}
		if pushes := in.pushes(t); len(pushes) != 1 || pushes[0].GetNewSha() != tip {
			t.Errorf("pushes recorded %v; want the one by hand, at %s", pushes, tip)
		}
		if got := in.remoteTip(t, bare); got != tip {
			t.Errorf("origin at %s; want %s", got, tip)
		}
	})

	t.Run("red again on stale question", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		in.remote(t)
		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		var starts int
		in.correctWith(func(dir, prompt string) string {
			if starts++; starts == 1 {
				return "wait\nfail could not fix tests"
			}
			return "text nothing to do"
		})

		in.pass(t, 0)
		w2ID := in.pushState(t).GetCorrectedBy()
		if _, err := in.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: w2ID, Text: "wake"})); err != nil {
			t.Fatal(err)
		}
		w2 := in.ended(t, w2ID)
		in.pass(t, time.Second)
		if state := in.pushState(t); state.GetCorrectedBy() != "" || state.GetAttempts() != 1 {
			t.Fatalf("after %s failed, the push %v; want one attempt spent, no correction running", w2.GetCode(), state)
		}
		q := in.attachPushQuestion(t)
		tip := in.handCommit(t)

		if _, err := in.h.Push(t.Context(), in.wishID, in.projectID, ""); err == nil {
			t.Fatal("pushed with its push checks red")
		}
		state := in.pushState(t)
		if state.GetQuestionId() != "" || state.GetFailedSha() != tip || state.GetCorrectedBy() == "" || state.GetAttempts() != 2 {
			t.Fatalf("red again at %s, the push %v; want the stale question let go, a second correction at the new tip", short8(tip), state)
		}
		got, err := store.Get[*planv1.Question](t.Context(), in.db, q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if got.GetWithdrawal() == nil || !strings.Contains(got.GetWithdrawal().GetNote(), short8(tip)) {
			t.Errorf("the stale question %s: withdrawal %v; want withdrawn by Djinn, saying the new tip", got.GetCode(), got.GetWithdrawal())
		}
	})
}

// TestClearPushCorrection: a push held with a correction whose integration record was cleared or whose task was
// cancelled clears its link, runs its checks again on the next pass, and pushes when green.
func TestClearPushCorrection(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("record cleared", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })

		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}
		state := in.pushState(t)
		if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Fatalf("the push %v; want held and corrected by a worker", state)
		}
		w2ID := state.GetCorrectedBy()
		in.ended(t, w2ID)

		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		in.handCommit(t)

		none := "none"
		if _, err := in.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
			TaskId:      w2ID,
			Integration: &none,
		})); err != nil {
			t.Fatal(err)
		}
		if got := in.pushState(t).GetCorrectedBy(); got != "" {
			t.Errorf("after clearing record, corrected_by = %q; want empty", got)
		}

		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("push checks passed, not pushed: origin at %s, want %s", got, tip)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
			t.Errorf("after push succeeded, push state: %v", state)
		}
	})

	t.Run("correction cancelled", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "checks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n"+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "Letters")
		in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })

		in.pass(t, 0)
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}
		state := in.pushState(t)
		if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Fatalf("the push %v; want held and corrected by a worker", state)
		}
		w2ID := state.GetCorrectedBy()
		in.ended(t, w2ID)

		if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
			w2, err := store.Get[*planv1.Task](t.Context(), tx, w2ID)
			if err != nil {
				return err
			}
			w2.Status = 10
			if err := tx.Journal("test", "put", w2); err != nil {
				return err
			}
			return tx.Put(w2)
		}); err != nil {
			t.Fatal(err)
		}

		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		in.handCommit(t)

		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("push checks passed, not pushed: origin at %s, want %s", got, tip)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
			t.Errorf("after push succeeded, push state: %v", state)
		}
	})
}

// TestPushHeldBlockReason: while push checks are red, planned work tasks wait for the branch to be green again,
// while already-committed tasks stay COMMITTED; once corrected or approved unchecked, waiting tasks start from the
// green head.
func TestPushHeldBlockReason(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("failed correction leaves committed and dependents", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		old := in.tip(t)
		azima := in.azima(t, in.wishID, "First")

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
			task.PartOf = azima.GetId()
		})

		w2 := in.mustSpawn(t, in.wishID, "W2", "wait\ntext two", &planv1.TaskServiceSpawnRequest{
			DependsOn: []string{"W1"},
		})
		if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || w2.GetWaitReason() != "waits for W1 to be committed" {
			t.Fatalf("before W1 commit, W2 = %v; want pending waiting for W1 to be committed", w2)
		}

		var attempts int
		in.correctWith(func(dir, prompt string) string {
			if !strings.HasPrefix(prompt, "The push checks of ") {
				return ""
			}
			attempts++
			if attempts == 1 {
				file := "src/a.txt"
				if !strings.HasSuffix(dir, "/app") {
					file = "app/src/a.txt"
				}
				writeFile(t, dir, file, "conflicting edit from push correction\n")
				commitAll(t, dir, "Push correction conflicting edit")
				return "wait\ntext done"
			}
			return "text push checks fixed"
		})

		wait := make(chan struct{})
		in.mu.Lock()
		in.testWait = wait
		in.mu.Unlock()
		in.passApart(t, 0)
		w2 = in.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		close(wait)
		in.h.waitPushes()
		if state := in.pushState(t); state.GetHeldRuns() != 1 || state.GetCorrectedBy() != "" {
			t.Fatalf("push state %v; want held once, no correction while W2 runs", state)
		}
		in.mu.Lock()
		in.testWait = nil
		in.mu.Unlock()

		// Pass 2: red twice in a row, the correction worker W3 is spawned.
		in.pass(t, 0)

		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}

		w1In, _ := in.integration(t, w1)
		if w1In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w1In.GetCorrectedBy() != "" {
			t.Fatalf("W1 integration after push check fail = %v; want COMMITTED with no CorrectedBy", w1In)
		}

		state := in.pushState(t)
		if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Fatalf("push state %v; want held and corrected by worker", state)
		}
		w3ID := state.GetCorrectedBy()
		w3 := in.get(t, w3ID)
		if w3.GetCode() != "W3" {
			t.Fatalf("correction worker is %s, want W3", w3.GetCode())
		}

		writeFile(t, w2.GetWorktree(), "app/src/b.txt", "b by W2\n")
		commitAll(t, w2.GetWorktree(), "W2 adds b.txt")

		writeFile(t, in.repo, "app/src/a.txt", "a modified by hand\n")
		commitAll(t, in.repo, "a.txt by hand")

		in.release(t, w3ID)
		in.ended(t, w3ID)
		in.pass(t, time.Second)

		w1In, _ = in.integration(t, w1)
		if w1In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w1In.GetCorrectedBy() != "" {
			t.Errorf("W1 integration after correction conflict = %v; want COMMITTED", w1In)
		}

		wDep := in.mustSpawn(t, in.wishID, "Dependent", "wait\ntext dependent", &planv1.TaskServiceSpawnRequest{
			DependsOn: []string{"W1"},
		})
		wDep = in.get(t, wDep.GetId())
		if wDep.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !strings.Contains(wDep.GetWaitReason(), "to be green again") {
			t.Fatalf("wDep while push held = %v; want pending waiting to be green again", wDep)
		}

		w3In, _ := in.integration(t, w3)
		if w3In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT {
			t.Errorf("W3 integration = %v; want CONFLICT", w3In)
		}

		state = in.pushState(t)
		if state.GetQuestionId() != "" {
			t.Fatalf("push question %s; want none (left to the lead)", state.GetQuestionId())
		}
		if !strings.HasSuffix(state.GetHeld(), "; left to the lead") {
			t.Fatalf("push held %q; want suffix '; left to the lead'", state.GetHeld())
		}
		q := in.attachPushQuestion(t)

		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		in.answer(t, q, planv1.Choice_CHOICE_A)

		wRetry := in.ended(t, in.pushState(t).GetCorrectedBy())
		if wRetry == nil {
			t.Fatal("expected retried correction worker")
		}

		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("push did not succeed after correction: remote at %s, want %s", got, tip)
		}
		if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
			t.Errorf("after push, pushState = %v; want cleared", state)
		}

		in.release(t, w2.GetId())
		in.ended(t, w2.GetId())
		in.pass(t, time.Second)
		if w2In, _ := in.integration(t, w2); w2In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w2In.GetCorrectedBy() != "" {
			t.Errorf("W2 integration = %v; want COMMITTED", w2In)
		}

		wDep = in.until(t, wDep.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		writeFile(t, wDep.GetWorktree(), "app/src/dep.txt", "dep\n")
		commitAll(t, wDep.GetWorktree(), "Dependent adds dep.txt")
		in.release(t, wDep.GetId())
		in.ended(t, wDep.GetId())
		in.pass(t, time.Second)

		wantStates := fmt.Sprintf("W1 COMMITTED, W2 COMMITTED, %s COMMITTED", wDep.GetCode())
		if got := in.states(t, w1, w2, wDep); got != wantStates {
			t.Errorf("final task states = %s; want %s", got, wantStates)
		}
	})

	t.Run("blocks planned tasks until green", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		azima := in.azima(t, in.wishID, "Feature")

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
			task.PartOf = azima.GetId()
		})

		in.correctWith(func(dir, prompt string) string {
			if strings.HasPrefix(prompt, "The push checks of ") {
				in.mu.Lock()
				in.testCode = 0
				in.mu.Unlock()
				fixTests(t, dir, "Fix the tests")
				return "text push checks fixed"
			}
			return ""
		})

		in.pass(t, 0)
		state := in.pushState(t)
		if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
			t.Fatalf("push state %v; want held and corrected", state)
		}

		w2 := in.mustSpawn(t, in.wishID, "W2 Work", "wait\ntext w2", nil)
		w2 = in.get(t, w2.GetId())
		if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			t.Fatalf("W2 status = %v; want PENDING", w2.GetStatus())
		}
		expectedWaitPrefix := fmt.Sprintf("waits for %s to be green again (push checks red at ", in.branch)
		if !strings.HasPrefix(w2.GetWaitReason(), expectedWaitPrefix) {
			t.Fatalf("W2 waitReason = %q; want prefix %q", w2.GetWaitReason(), expectedWaitPrefix)
		}

		wCorr := in.ended(t, state.GetCorrectedBy())
		if wCorr == nil {
			t.Fatal("expected correction worker")
		}

		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("remote at %s, want tip %s", got, tip)
		}
		if s := in.pushState(t); s.GetHeld() != "" {
			t.Fatalf("push state still held: %v", s)
		}

		w2 = in.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		wtHead := in.git(t, w2.GetWorktree(), "rev-parse", "HEAD")
		if wtHead != tip {
			t.Fatalf("W2 started at %s; want green tip %s", wtHead, tip)
		}
		in.release(t, w2.GetId())
		in.ended(t, w2.GetId())
		in.pass(t, time.Second)

		if got := in.states(t, w1, w2); !strings.Contains(got, "W1 COMMITTED") || !strings.Contains(got, "COMMITTED") {
			t.Errorf("states = %s; want COMMITTED", got)
		}
	})

	t.Run("lifts when approved unchecked", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "correction_attempts: 0\n"+
			lintAtCommit+
			"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		in.testCode = 1
		bare := in.remote(t)
		azima := in.azima(t, in.wishID, "Feature")

		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
			task.PartOf = azima.GetId()
		})

		in.pass(t, 0)
		if state := in.pushState(t); state.GetQuestionId() != "" || !strings.HasSuffix(state.GetHeld(), "; left to the lead") {
			t.Fatalf("the push %v; want held, left to the lead, no question", state)
		}

		w2 := in.mustSpawn(t, in.wishID, "W2 Work", "wait\ntext w2", nil)
		w2 = in.get(t, w2.GetId())
		if w2.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING || !strings.Contains(w2.GetWaitReason(), "to be green again") {
			t.Fatalf("W2 before answer = %v; want pending waiting to be green again", w2)
		}

		in.answer(t, in.attachPushQuestion(t), planv1.Choice_CHOICE_C)

		w2 = in.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		in.release(t, w2.GetId())
		in.ended(t, w2.GetId())

		in.pass(t, time.Second)
		tip := in.tip(t)
		if got := in.remoteTip(t, bare); got != tip {
			t.Fatalf("remote at %s, want %s", got, tip)
		}

		_, texts := in.integration(t, w1)
		foundTell := slices.ContainsFunc(texts, func(txt string) bool {
			return strings.Contains(txt, "without push checks")
		})
		if !foundTell {
			t.Errorf("W1 events %v did not mention pushing without push checks", texts)
		}
	})
}

// TestIntegrate_WithoutChecks replays W4 of wish Dew (12/10/2026): a project naming no check integrates into a wish's
// own branch, leaving the checkout of main untouched, and refuses to integrate untested work into main.
func TestIntegrate_WithoutChecks(t *testing.T) {
	t.Parallel()

	t.Run("wish branch integrates", func(t *testing.T) {
		t.Parallel()
		e, queue, wishID, projectID, repo := uncheckedDew(t)
		main, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
		if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wishID, ProjectId: projectID, Branch: "silae-skip-forbidden", CreateBranch: true,
			PushMode: planv1.PushMode_PUSH_MODE_ASK,
		})); err != nil {
			t.Fatal(err)
		}
		task := spawnDone(t, e, wishID, projectID)
		if task.GetIntegration().GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING {
			t.Fatalf("integration at the end %v, want pending", task.GetIntegration())
		}
		e.h.integratePass(t.Context())
		integratedInto(t, e, queue, task, repo, "silae-skip-forbidden", main)
	})

	t.Run("main branch refused until set", func(t *testing.T) {
		t.Parallel()
		e, queue, wishID, projectID, repo := uncheckedDew(t)
		main, _ := git(t.Context(), repo, "rev-parse", "refs/heads/main")
		task := spawnDone(t, e, wishID, projectID)
		if task.GetIntegration() != nil {
			t.Fatalf("integration %v, want none", task.GetIntegration())
		}
		why := "the project's settings name no check, and untested work goes only to a wish's own branch " +
			"(djinn wish set-integration --create-branch)"
		events, err := store.List[*planv1.TaskEvent](t.Context(), e.db, store.Where{"task_id": task.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		if texts := eventTexts(events); !slices.Contains(texts, "integration: none: "+why) {
			t.Errorf("events %q, without why its work stays out", texts)
		}
		want := []string{"ended Djinn: " + task.GetCode() + " done, not integrated: " + why +
			". Its final note: Columns left out of a refused read."}
		if lines := leadLines(queue, task.GetCode()); !slices.Equal(lines, want) {
			t.Errorf("lead events %q, want %q", lines, want)
		}
		e.h.integratePass(t.Context())
		if got, _ := store.Get[*planv1.Task](t.Context(), e.db, task.GetId()); got.GetIntegration() != nil {
			t.Fatalf("integration %v after a pass, want none", got.GetIntegration())
		}

		pend := connect.NewRequest(&planv1.TaskServiceUpdateRequest{TaskId: task.GetId(), Integration: proto.String("pending")})
		if _, err := e.tasks.Update(t.Context(), pend); err == nil || !strings.Contains(err.Error(), why) {
			t.Fatalf("put back in line into main: %v, want refused saying %q", err, why)
		}
		if _, err := e.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wishID, ProjectId: projectID, Branch: "silae-skip-forbidden", CreateBranch: true,
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := e.tasks.Update(t.Context(), pend); err != nil {
			t.Fatal(err)
		}
		queue.mu.Lock()
		queue.events = nil
		queue.mu.Unlock()
		e.h.integratePass(t.Context())
		integratedInto(t, e, queue, task, repo, "silae-skip-forbidden", main)
	})
}
