package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
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

// TestSetupOncePerWorktree: the setup makes the integration worktree ready before its first check, once, while no lock
// file changes. Each runs through the gate of its name, and the project keeps their last runs.
func TestSetupOncePerWorktree(t *testing.T) {
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
}

// TestSetupAgain: the setup runs again when a lock file changes, and in an integration worktree made anew.
func TestSetupAgain(t *testing.T) {
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
}

// TestARedCommitCheck: the checks run before a commit, in their order; a red one fails the merge, as red tests did,
// and starts a correction worker, whose first prompt says the checks Djinn runs and when. A push check does not run
// at the commit. Djinn's former test command is the check test at commit (the other integration tests).
func TestARedCommitCheck(t *testing.T) {
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
		"Djinn checks this project's work before it commits a task's work, with `djinn gate run lint -- lint`; before it pushes, " +
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

// TestPushChecksHoldThePush: before a push, the push checks run on the branch's tip; red, the push is held, said in
// the tasks' events and on the wish. Red while work is left to commit, Djinn checks again at the next push due; red
// again, it asks. Checking again once they pass pushes, and the push held is forgotten.
func TestPushChecksHoldThePush(t *testing.T) {
	in := integrating(t)
	in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin"
	bare := in.remote(t)
	old := in.tip(t)
	first, second := in.azima(t, in.wishID, "First"), in.azima(t, in.wishID, "Second")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = first.GetId() })
	w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, func(task *planv1.Task) { task.PartOf = second.GetId() })

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
	if !strings.Contains(state.GetHeld(), "test: test exited 1") || state.GetHeldRuns() != 2 {
		t.Fatalf("the push %v; want held twice, W2's azima ending after W1's", state)
	}
	q := in.pushQuestion(t)
	if !strings.HasPrefix(q.GetText(), "The push checks of "+in.branch+" stay red: test: test exited 1.") ||
		!strings.Contains(q.GetContext(), "--- FAIL: TestLogin") || len(q.GetOptions()) != 3 {
		t.Errorf("question %q, context %q, options %q", q.GetText(), q.GetContext(), q.GetOptions())
	}
	if _, texts := in.integration(t, w2); !slices.Contains(texts, "the push checks of "+in.branch+" stay red; Djinn asks you "+q.GetCode()) {
		t.Errorf("W2's events %q lack the question", texts)
	}

	in.mu.Lock()
	in.testCode = 0
	in.mu.Unlock()
	in.answer(t, q, planv1.Choice_CHOICE_A)
	in.pass(t, time.Second)
	if got, tip := in.remoteTip(t, bare), in.tip(t); got != tip {
		t.Fatalf("checked again green, not pushed: origin at %s, the branch at %s", got, tip)
	}
	if state := in.pushState(t); state.GetHeld() != "" || state.GetHeldRuns() != 0 || state.GetQuestionId() != "" {
		t.Errorf("the push still held: %v", state)
	}
}

// TestPushWithoutTheChecks: red once no work is left to commit, Djinn asks at once; "push it without the checks"
// pushes once, saying so. A check that passed on the tip as a commit check does not run again before the push.
func TestPushWithoutTheChecks(t *testing.T) {
	in := integrating(t)
	in.checking(t, "checks { name: \"lint\" command: \"lint\" when: [CHECK_WHEN_COMMIT, CHECK_WHEN_PUSH] }\n"+
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
	if state := in.pushState(t); state.GetHeldRuns() != 1 || state.GetQuestionId() == "" {
		t.Fatalf("no work left: the push %v; want a question at once", state)
	}
	in.answer(t, in.pushQuestion(t), planv1.Choice_CHOICE_B)
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
}
