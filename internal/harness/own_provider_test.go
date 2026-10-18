package harness

import (
	"path/filepath"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// ownProvider sets the project of in on claude and ownModel, its other settings these, and makes the fake agent run
// what work says on claude and on antigravity alike: a worker Djinn started on the wrong one still runs, its provider
// then tells.
func (in *integration) ownProvider(t *testing.T, settings string, work func(dir, prompt string) string) {
	t.Helper()
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"provider: PROVIDER_CLAUDE\nmodel: \""+ownModel+"\"\n"+settings)
	in.correctWith(work)
	in.h.providers[planv1.Provider_PROVIDER_ANTIGRAVITY] = corrector{work: work}
}

// ownModel is the project's model in the tests of Djinn's own workers.
const ownModel = "claude-opus-5-5"

// onAntigravity makes a task run on antigravity, with a model of its own.
func onAntigravity(task *planv1.Task) {
	task.Provider, task.Model = planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3-pro"
}

// assertOwnProvider fails unless the worker w Djinn started for task runs on the project's provider and model, and
// task keeps its own.
func (in *integration) assertOwnProvider(t *testing.T, w, task *planv1.Task) {
	t.Helper()
	if w.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || w.GetModel() != ownModel {
		t.Errorf("%s runs on %s, %q; want the project's claude, %q", w.GetCode(), w.GetProvider(), w.GetModel(), ownModel)
	}
	if got := in.get(t, task.GetId()); got.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY || got.GetModel() != "gemini-3-pro" {
		t.Errorf("%s now on %s, %q; want its own antigravity", task.GetCode(), got.GetProvider(), got.GetModel())
	}
}

// TestCorrectionOnTheProjectsProvider (Q95): a correction worker of red commit checks runs on the project's provider
// and model, not on those of the task whose work it corrects.
func TestCorrectionOnTheProjectsProvider(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.testCode = 1
	in.ownProvider(t, "test: \"test\"\n", func(string, string) string {
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		return ""
	})
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, onAntigravity)

	in.pass(t, 0)
	in.assertOwnProvider(t, in.correction(t, w1), w1)
}

// TestReviewOnTheProjectsProvider (Q95): a review worker of work left not committed runs on the project's provider and
// model, not on those of the task whose worktree it reviews.
func TestReviewOnTheProjectsProvider(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.ownProvider(t, "test: \"test\"\n", func(dir, _ string) string {
		commitAll(t, dir, "Add b")
		return ""
	})
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, onAntigravity)
	in.leave(t, w1, map[string]string{"app/src/b.txt": "b\n"})

	in.pass(t, 0)
	in.assertOwnProvider(t, in.reviewer(t, w1), w1)
}

// TestPushCorrectionOnTheProjectsProvider (Q95): a correction worker of red push checks runs on the project's provider
// and model, not on those of the tasks the push brings.
func TestPushCorrectionOnTheProjectsProvider(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	in.ownProvider(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", func(dir, _ string) string {
		fixTests(t, dir, "Fix the tests")
		return ""
	})
	in.exits = map[string]int{"lint": 0}
	in.testCode = 1
	in.remote(t)
	azima := in.azima(t, in.wishID, "Letters") // The push waits for it.
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, onAntigravity, func(task *planv1.Task) {
		task.PartOf = azima.GetId()
	})

	in.pass(t, 0)
	state := in.pushState(t)
	if state.GetCorrectedBy() == "" {
		t.Fatalf("the push %v; want corrected by a worker", state)
	}
	in.assertOwnProvider(t, in.ended(t, state.GetCorrectedBy()), w1)
}
