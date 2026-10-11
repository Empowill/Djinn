package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
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

// TestSetupOncePerWorktree: the setup makes the integration worktree ready before its first check, once, while no lock
// file changes. Each runs through the gate of its name, and the project keeps their last runs.
func TestSetupOncePerWorktree(t *testing.T) {
	testx.Portable(t)
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
	testx.Portable(t)
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

// TestSetupWhenNodeModulesMissing: setup runs when package.json exists and node_modules is missing, even without
// lock file changes.
func TestSetupWhenNodeModulesMissing(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.checking(t, "setup: \"setup\"\n"+lintAtCommit, map[string]int{"setup": 0, "lint": 0})
	in.finished(t, "W1", map[string]string{"app/package.json": "{}\n", "app/.gitignore": "node_modules\n"})
	in.pass(t, 0)
	in.ran()

	// W1 ran setup, but the fake setup command created no node_modules.
	// W2 commits without changing lock files: setup runs again because node_modules is missing.
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"})
	in.pass(t, time.Second)
	if runs, _ := in.ran(); !slices.Equal(runs, []string{"setup", "lint"}) {
		t.Errorf("W2 with missing node_modules: %q; want setup then lint", runs)
	}

	// Now simulate setup having installed node_modules.
	wt := integrationDir(in.home, in.projectID, in.wishID)
	nm := filepath.Join(wt, "app", "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "pkg.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	in.ran()

	// W3 commits without changing lock files: with node_modules present, setup is skipped.
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})
	in.pass(t, time.Second)
	if runs, _ := in.ran(); !slices.Equal(runs, []string{"lint"}) {
		t.Errorf("W3 with present node_modules: %q; want lint only", runs)
	}
}

// TestARedCommitCheck: the checks run before a commit, in their order; a red one fails the merge, as red tests did,
// and starts a correction worker, whose first prompt says the checks Djinn runs and when. A push check does not run
// at the commit. Djinn's former test command is the check test at commit (the other integration tests).
func TestARedCommitCheck(t *testing.T) {
	testx.Portable(t)
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
// again, it starts a correction worker. A second red does not spawn another worker while it runs; a failed correction
// asks the person, who can retry, take it over, or push without checks.
func TestPushChecksHoldThePush(t *testing.T) {
	testx.Portable(t)
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
	if !strings.Contains(state.GetHeld(), "test: test exited 1") || state.GetHeldRuns() != 2 {
		t.Fatalf("the push %v; want held twice, W2's azima ending after W1's", state)
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

	// W3 fails once released: asking the person what to do.
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
}

// TestPushWithoutTheChecks: red once no work is left to commit, Djinn asks at once when correction attempts are spent;
// "push it without the checks" pushes once, saying so. A check that passed on the tip as a commit check does not run
// again before the push.
func TestPushWithoutTheChecks(t *testing.T) {
	testx.Portable(t)
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
}

// TestPushChecksCorrectionCleared: a push held with a correction whose integration record was cleared
// runs its checks again on the next pass and pushes when green.
func TestPushChecksCorrectionCleared(t *testing.T) {
	testx.Portable(t)
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
}

// TestPushChecksCorrectionCancelled: a push held with a correction that was cancelled
// clears its link, runs its checks again on the next pass and pushes when green.
func TestPushChecksCorrectionCancelled(t *testing.T) {
	testx.Portable(t)
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

	// Cancel W2 (status 10).
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

	in.pass(t, time.Second)
	tip := in.tip(t)
	if got := in.remoteTip(t, bare); got != tip {
		t.Fatalf("push checks passed, not pushed: origin at %s, want %s", got, tip)
	}
	if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
		t.Errorf("after push succeeded, push state: %v", state)
	}
}

// TestPushCheckFailedCorrectionLeavesCommittedAndDependents: a failed push check and a failed correction
// leave committed tasks COMMITTED and their dependents free to start; the push stays held and then
// pushes once corrected.
func TestPushCheckFailedCorrectionLeavesCommittedAndDependents(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.checking(t, lintAtCommit+
		"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
	in.testCode = 1
	bare := in.remote(t)
	old := in.tip(t)
	azima := in.azima(t, in.wishID, "First")

	// W1 finishes and commits into the integration branch.
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	// W2 depends on W1. Before W1 commits, W2 waits.
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
			// Correction worker creates a conflicting edit on app/src/a.txt
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

	// Pass 1: W1 commits into the branch.
	// Push check fails on tip (testCode = 1). Push is held.
	// Correction worker W3 is spawned.
	in.pass(t, 0)

	// Remote has not received the red push.
	if got := in.remoteTip(t, bare); got != old {
		t.Fatalf("pushed red: origin at %s", got)
	}

	// Verify W1 remains COMMITTED and its CorrectedBy is NOT set.
	w1In, _ := in.integration(t, w1)
	if w1In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w1In.GetCorrectedBy() != "" {
		t.Fatalf("W1 integration after push check fail = %v; want COMMITTED with no CorrectedBy", w1In)
	}

	// Push state is held and corrected by W3.
	state := in.pushState(t)
	if state.GetHeld() == "" || state.GetCorrectedBy() == "" {
		t.Fatalf("push state %v; want held and corrected by worker", state)
	}
	w3ID := state.GetCorrectedBy()
	w3 := in.get(t, w3ID)
	if w3.GetCode() != "W3" {
		t.Fatalf("correction worker is %s, want W3", w3.GetCode())
	}

	// Dependent task W2 is free to start because W1 is COMMITTED!
	w2 = in.until(t, w2.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	// In W2, modify app/src/a.txt so W3's commit conflicts on merge into the branch.
	writeFile(t, w2.GetWorktree(), "app/src/a.txt", "a modified by W2\n")
	commitAll(t, w2.GetWorktree(), "W2 modifies a.txt")
	in.release(t, w2.GetId())
	in.ended(t, w2.GetId())

	// Pass 2: W2 integrates and commits its change into the branch first.
	in.pass(t, time.Second)

	// Now release correction worker W3: its merge into the branch conflicts with W2's commit!
	in.release(t, w3ID)
	in.ended(t, w3ID)
	in.pass(t, time.Second)

	// Verify W1 and W2 STILL have state COMMITTED and no CorrectedBy!
	w1In, _ = in.integration(t, w1)
	if w1In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w1In.GetCorrectedBy() != "" {
		t.Errorf("W1 integration after correction conflict = %v; want COMMITTED", w1In)
	}
	w2In, _ := in.integration(t, w2)
	if w2In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w2In.GetCorrectedBy() != "" {
		t.Errorf("W2 integration after correction conflict = %v; want COMMITTED", w2In)
	}

	// A new dependent task WDep depending on W1 is planned: it must be free to start!
	wDep := in.mustSpawn(t, in.wishID, "Dependent", "wait\ntext dependent", &planv1.TaskServiceSpawnRequest{
		DependsOn: []string{"W1"},
	})
	// Because W1 is COMMITTED, WDep does NOT wait on W1 ("waits for W1 to be committed (conflict)")
	// It is dispatched and starts running!
	wDep = in.until(t, wDep.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
	in.release(t, wDep.GetId())
	in.ended(t, wDep.GetId())

	// The correction worker W3 has CONFLICT integration state on itself.
	w3In, _ := in.integration(t, w3)
	if w3In.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT {
		t.Errorf("W3 integration = %v; want CONFLICT", w3In)
	}

	// Push remains held, left to the lead.
	state = in.pushState(t)
	if state.GetQuestionId() != "" {
		t.Fatalf("push question %s; want none (left to the lead)", state.GetQuestionId())
	}
	if !strings.HasSuffix(state.GetHeld(), "; left to the lead") {
		t.Fatalf("push held %q; want suffix '; left to the lead'", state.GetHeld())
	}
	q := in.attachPushQuestion(t)

	// Answer Choice A to retry correction. Now push checks pass.
	in.mu.Lock()
	in.testCode = 0
	in.mu.Unlock()
	in.answer(t, q, planv1.Choice_CHOICE_A)

	// Retried correction worker runs and ends.
	wRetry := in.ended(t, in.pushState(t).GetCorrectedBy())
	if wRetry == nil {
		t.Fatal("expected retried correction worker")
	}

	// Pass 3: push checks pass and the branch pushes!
	in.pass(t, time.Second)
	tip := in.tip(t)
	if got := in.remoteTip(t, bare); got != tip {
		t.Fatalf("push did not succeed after correction: remote at %s, want %s", got, tip)
	}
	if state := in.pushState(t); state.GetHeld() != "" || state.GetCorrectedBy() != "" {
		t.Errorf("after push, pushState = %v; want cleared", state)
	}

	// W1, W2, and wDep all remain COMMITTED!
	wantStates := fmt.Sprintf("W1 COMMITTED, W2 COMMITTED, %s COMMITTED", wDep.GetCode())
	if got := in.states(t, w1, w2, wDep); got != wantStates {
		t.Errorf("final task states = %s; want %s", got, wantStates)
	}
}
