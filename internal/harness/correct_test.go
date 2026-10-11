package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// corrector is the fake agent of a test's correction workers: before it plays its script, it does what work says in
// its folder, and its script becomes what work returns, its own prompt when work returns "".
type corrector struct {
	Fake
	work func(dir, prompt string) string
}

func (c corrector) Start(ctx context.Context, spec Spec) (Worker, error) {
	if script := c.work(spec.Dir, spec.Prompt); script != "" {
		spec.Prompt = script
	}
	return c.Fake.Start(ctx, spec)
}

// correctWith makes the fake agent correct as work says. The test sets it before any worker starts.
func (in *integration) correctWith(work func(dir, prompt string) string) {
	in.h.providers[planv1.Provider_PROVIDER_FAKE] = corrector{work: work}
}

// correction is the correction worker that corrects task's work, once it has ended.
func (in *integration) correction(t *testing.T, task *planv1.Task) *planv1.Task {
	t.Helper()
	got, texts := in.integration(t, task)
	if got.GetCorrectedBy() == "" {
		t.Fatalf("%s's work has no correction worker: %v; events %q", task.GetCode(), got, texts)
	}
	return in.ended(t, got.GetCorrectedBy())
}

// attachQuestion attaches a question to task's integration record to test answering it.
func (in *integration) attachQuestion(t *testing.T, tasks ...*planv1.Task) *planv1.Question {
	t.Helper()
	if len(tasks) == 0 {
		t.Fatal("attachQuestion: no tasks")
	}
	q := &planv1.Question{
		Id:         store.NewID(),
		WishId:     in.wishID,
		Code:       "Q01",
		Text:       "The work of " + tasks[0].GetCode() + " does not go into " + in.branch,
		CreateTime: timestamppb.Now(),
	}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/question", q); err != nil {
			return err
		}
		if err := plan.Ask(t.Context(), tx, q); err != nil {
			return err
		}
		for _, task := range tasks {
			got, err := store.Get[*planv1.Task](t.Context(), tx, task.GetId())
			if err != nil {
				return err
			}
			if got.Integration == nil {
				got.Integration = &planv1.TaskIntegration{}
			}
			got.Integration.QuestionId = q.GetId()
			if err := tx.Put(got); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

// answer answers q with choice, as the plan services do once the answer is stored, and gives what Djinn says it did.
func (in *integration) answer(t *testing.T, q *planv1.Question, choice planv1.Choice) string {
	t.Helper()
	q = proto.CloneOf(q)
	q.Answer = &planv1.Answer{Choice: choice, CreateTime: timestamppb.Now()}
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/answer", q); err != nil {
			return err
		}
		return tx.Put(q)
	}); err != nil {
		t.Fatal(err)
	}
	return in.h.Answered(t.Context(), q)
}

// dropTask deletes task from the store as Djinn did before Delete kept an azima with parts: nothing else changes.
func dropTask(t *testing.T, db *store.Store, task *planv1.Task) {
	t.Helper()
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/drop", task); err != nil {
			return err
		}
		return tx.Delete(task)
	}); err != nil {
		t.Fatal(err)
	}
}

// storedTexts are the texts of the stored events of the task id.
func storedTexts(t *testing.T, db *store.Store, id string) []string {
	t.Helper()
	events, err := store.List[*planv1.TaskEvent](t.Context(), db, store.Where{"task_id": id})
	if err != nil {
		t.Fatal(err)
	}
	return eventTexts(events)
}

// TestCorrectACodeConflict: a conflict in code starts a correction worker by itself, part of the azima, of the failed
// task's provider, in a worktree on the failed merge, the conflict in its first prompt; its success commits its work
// and the failed task's, which says it was corrected. The task committed before stays as it went in.
func TestCorrectACodeConflict(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	old := in.tip(t)
	azima := in.azima(t, in.wishID, "The readme")
	partOf := func(task *planv1.Task) { task.PartOf = azima.GetId() }
	w1 := in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"}, partOf)
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"}, partOf)
	var conflict string
	in.correctWith(func(dir, _ string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "README.md"))
		conflict = string(b)
		writeFile(t, dir, "README.md", "# One and Two\n")
		commitAll(t, dir, "Settle the readme") // Which concludes the merge.
		return ""
	})

	in.pass(t, 0) // W1 goes in; W2 conflicts with it.
	w3 := in.correction(t, w2)
	got, texts := in.integration(t, w2)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetAttempts() != 1 ||
		got.GetFailure().GetMergeBranch() != w2.GetBranch() || !slices.Equal(got.GetFailure().GetFiles(), []string{"app/README.md"}) {
		t.Fatalf("W2's integration %v", got)
	}
	want := "conflict: W2 conflicts with " + in.branch + " in app/README.md; " + in.branch + " stays as it was; " +
		w3.GetCode() + " corrects it, attempt 1 of 2"
	if texts[len(texts)-1] != want {
		t.Errorf("W2's last event %q; want %q", texts[len(texts)-1], want)
	}
	if w1, _ := in.integration(t, w1); w1.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED || w1.GetCorrectedBy() != "" {
		t.Errorf("W1, committed before W2 alone: %v", w1)
	}
	// The correction worker: work part of the azima, of W2's provider, Djinn's own, its first prompt saying the
	// conflict, its worktree on W1 merged and W2's merge under way.
	if w3.GetCode() != "W3" || w3.GetPartOf() != azima.GetId() || w3.GetProvider() != planv1.Provider_PROVIDER_FAKE ||
		w3.GetCorrection().GetAttempt() != 1 || w3.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("the correction worker %v", w3)
	}
	prompt, err := firstPrompt(in.db, w3.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"could not integrate the work of W2 into " + in.branch, "the merge of " + w2.GetBranch() +
		" is under way", "- app/README.md", "Settle each conflict", "its commit checks (`djinn gate run test -- test`) run through their gates", "run `gen` once the code is settled"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("W3's prompt lacks %q:\n%s", s, prompt)
		}
	}
	if !strings.Contains(conflict, "<<<<<<<") || !strings.Contains(conflict, "# One") || !strings.Contains(conflict, "# Two") {
		t.Errorf("W3's worktree did not hold the conflict: %q", conflict)
	}
	if got, _ := in.integration(t, w3); got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_PENDING {
		t.Fatalf("W3's work: %v", got)
	}

	// Its work integrates at once, alone, like any task's: its success commits W2's with it.
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2, w3); got != "W1 COMMITTED, W2 COMMITTED, W3 COMMITTED" {
		t.Fatalf("after the correction: %s", got)
	}
	tip := in.tip(t)
	got, texts = in.integration(t, w2)
	if got.GetSha() != tip || got.GetReason() != "corrected by W3" || texts[len(texts)-1] != "committed into "+in.branch+" as "+
		tip[:8]+", corrected by W3" {
		t.Errorf("W2's integration %v; events %q", got, texts)
	}
	if out := in.git(t, in.repo, "show", tip+":app/README.md"); out != "# One and Two" {
		t.Errorf("README.md in the branch: %q", out)
	}
	for _, task := range []*planv1.Task{w1, w2, w3} {
		if _, err := git(t.Context(), in.repo, "merge-base", "--is-ancestor", task.GetBranch(), tip); err != nil {
			t.Errorf("%s's branch is not in %s: %v", task.GetCode(), in.branch, err)
		}
	}
	commits := in.commits(t)
	if len(commits) != 2 || commits[0].GetOldSha() != old || !slices.Equal(commits[0].GetTaskIds(), []string{w1.GetId()}) ||
		!slices.Equal(commits[1].GetTaskIds(), []string{w3.GetId(), w2.GetId()}) {
		t.Errorf("journal: %v", commits)
	}
}

// TestCorrectRedTests: red tests start a correction worker on the merged work, the command and its output in its
// first prompt.
func TestCorrectRedTests(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin\nFAIL"
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	var merged bool
	in.correctWith(func(dir, _ string) string {
		_, err := os.Stat(filepath.Join(dir, "src", "a.txt"))
		merged = err == nil
		in.mu.Lock()
		in.testCode = 0
		in.mu.Unlock()
		return ""
	})

	in.pass(t, 0)
	w2 := in.correction(t, w1)
	prompt, err := firstPrompt(in.db, w2.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"could not integrate the work of W1", "The command, run in the project's folder: `test`",
		"    --- FAIL: TestLogin\n    FAIL", "Make the tests pass"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("W2's prompt lacks %q:\n%s", s, prompt)
		}
	}
	if !merged || w2.GetTitle() != "Make the tests pass with the work of W1" {
		t.Errorf("W2 %q started without the merged work: %v", w2.GetTitle(), merged)
	}
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2); got != "W1 COMMITTED, W2 COMMITTED" {
		t.Errorf("after the correction: %s", got)
	}
}

// TestCorrectionAttemptsThenAQuestion: two corrections whose work fails in turn spend the attempts; Djinn then
// leaves it to the lead, and starts nothing more by itself.
func TestCorrectionAttemptsThenAQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode, in.testOut = 1, "--- FAIL: TestLogin"
	in.correctWith(func(string, string) string { return "" })
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, time.Hour)
	w2 := in.correction(t, w1)
	in.pass(t, time.Second)
	w3 := in.correction(t, w1)
	if w3.GetId() == w2.GetId() || w3.GetCorrection().GetAttempt() != 2 ||
		!slices.Equal(w3.GetCorrection().GetFailure().GetTaskIds(), []string{w1.GetId(), w2.GetId()}) {
		t.Fatalf("the second attempt %v", w3)
	}
	in.pass(t, time.Second)
	if got := in.states(t, w1, w2, w3); got != "W1 RED, W2 RED, W3 RED" {
		t.Fatalf("two attempts failed: %s", got)
	}
	got, texts := in.integration(t, w3)
	if got.GetQuestionId() != "" || got.GetAttempts() != 2 || got.GetCorrectedBy() != "" ||
		!strings.HasSuffix(texts[len(texts)-1], "; left to the lead") {
		t.Errorf("W3's integration %v; events %q", got, texts)
	}
	questions, err := store.List[*planv1.Question](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil || len(questions) != 0 {
		t.Fatalf("%d questions in store, want 0: %v", len(questions), err)
	}
	// Nothing more starts by itself, and the rest of the wish goes on.
	in.pass(t, time.Hour)
	tasks, err := store.List[*planv1.Task](t.Context(), in.db, store.Where{"wish_id": in.wishID})
	if err != nil || len(tasks) != 3 {
		t.Fatalf("%d tasks, %v; want 3", len(tasks), err)
	}
}

// TestAnswerAFailedIntegration: trying again starts a new correction, its attempts counted from one; taking it leaves
// the work out, saying so.
func TestAnswerAFailedIntegration(t *testing.T) {
	testx.Portable(t)
	// Djinn settles the question itself.
	in := integrating(t)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"provider: PROVIDER_FAKE\ntest: \"test\"\ncorrection_attempts: 1\n")
	in.testCode = 1
	in.correctWith(func(string, string) string { return "" })
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, time.Hour)
	w2 := in.correction(t, w1)
	in.pass(t, time.Second)
	q := in.attachQuestion(t, w1)

	// Try again: a new correction, its attempts counted from one; the lead hears what Djinn did, not to act.
	if did := in.answer(t, q, planv1.Choice_CHOICE_A); did != "Djinn started W3: Make the tests pass with the work of W1" {
		t.Errorf("what Djinn did: %q", did)
	}
	got, texts := in.integration(t, w1)
	if got.GetQuestionId() != "" || got.GetAttempts() != 1 || got.GetCorrectedBy() == "" ||
		texts[len(texts)-1] != "you said to try again ("+q.GetCode()+"); W3 corrects it, attempt 1 of 1" {
		t.Fatalf("tried again: %v; events %q", got, texts)
	}
	w3 := in.correction(t, w1)
	if !slices.Equal(w3.GetCorrection().GetFailure().GetTaskIds(), []string{w1.GetId(), w2.GetId()}) {
		t.Errorf("W3 corrects %v", w3.GetCorrection().GetFailure().GetTaskIds())
	}
	in.pass(t, time.Second)
	q = in.attachQuestion(t, w1)

	// I take it: the work stays out, and says so.
	if did := in.answer(t, q, planv1.Choice_CHOICE_C); did != "Djinn leaves the work of W1 to the developer: it stays out of "+
		in.branch+" until they bring it in" {
		t.Errorf("what Djinn did: %q", did)
	}
	got, texts = in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_RED || !strings.HasPrefix(got.GetReason(), "you take it ("+q.GetCode()+"): ") ||
		texts[len(texts)-1] != "you take it ("+q.GetCode()+"); it stays out of "+in.branch+" until you bring it in" {
		t.Errorf("taken: %v; events %q", got, texts)
	}
}

// TestACorrectionWorkerThatFails: a correction worker that fails counts as an attempt; with the project's
// correction_attempts spent, Djinn asks.
func TestACorrectionWorkerThatFails(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\ncorrection_attempts: 1\n")
	in.correctWith(func(string, string) string { return "fail I cannot settle it" })
	in.finished(t, "W1", map[string]string{"app/README.md": "# One\n"})
	w2 := in.finished(t, "W2", map[string]string{"app/README.md": "# Two\n"})
	in.finished(t, "W3", map[string]string{"app/src/c.txt": "c\n"})

	in.pass(t, 0) // W2 conflicts with W1, gone in before it.
	w4 := in.correction(t, w2)
	if w4.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		t.Fatalf("W4 %v", w4)
	}
	lead := &leadListener{}
	in.h.TellLeads(lead.tell)
	in.pass(t, time.Second)
	got, texts := in.integration(t, w2)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_CONFLICT || got.GetAttempts() != 1 ||
		got.GetQuestionId() != "" ||
		texts[len(texts)-1] != "W4, its correction worker, failed: I cannot settle it; left to the lead" {
		t.Errorf("W2's integration %v; events %q", got, texts)
	}
	if paras := lead.all(); len(paras) == 0 || !strings.Contains(paras[len(paras)-1], "The move is yours: retry a correction, drop the work") {
		t.Errorf("expected lead notification, got %v", paras)
	}
	// Leave it: the work stays out, and nothing starts.
	q := in.attachQuestion(t, w2)
	if did := in.answer(t, q, planv1.Choice_CHOICE_B); did != "Djinn leaves the work of W2 out of "+in.branch {
		t.Errorf("what Djinn did: %q", did)
	}
	if got, texts := in.integration(t, w2); !strings.HasPrefix(got.GetReason(), "left out ("+q.GetCode()+"): W2 conflicts") ||
		texts[len(texts)-1] != "left out of "+in.branch+" ("+q.GetCode()+")" {
		t.Errorf("left: %v; events %q", got, texts)
	}
}

// TestCorrectWorkWhoseAzimaIsGone: the azima of the failed work was deleted (as W158 did before Delete refused it):
// the correction worker starts all the same, part of no azima, and says so in its events.
func TestCorrectWorkWhoseAzimaIsGone(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	azima := in.azima(t, in.wishID, "Absorbed")
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
	dropTask(t, in.db, azima)
	in.correctWith(func(string, string) string { return "" })

	in.pass(t, 0)
	w2 := in.correction(t, w1)
	if w2.GetPartOf() != "" {
		t.Errorf("W2 part of %q, an azima that is gone", w2.GetPartOf())
	}
	want := "part of no azima: W1 was part of " + azima.GetId()[:8] + ", which is no longer one of the wish's azimas"
	if texts := storedTexts(t, in.db, w2.GetId()); !slices.Contains(texts, want) {
		t.Errorf("W2's events %q lack %q", texts, want)
	}
}

// TestACorrectionThatCannotStart: when the correction worker cannot start, the lead is told why and
// it is left to the lead.
func TestACorrectionThatCannotStart(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	lead := &leadListener{}
	in.h.TellLeads(lead.tell)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	delete(in.h.providers, planv1.Provider_PROVIDER_FAKE) // W1's provider: its correction worker cannot start.

	in.pass(t, 0)
	got, texts := in.integration(t, w1)
	if got.GetAttempts() != 0 || got.GetCorrectedBy() != "" ||
		!strings.Contains(texts[len(texts)-1], "; the correction worker could not start: invalid_argument: provider") ||
		!strings.HasSuffix(texts[len(texts)-1], "; left to the lead") {
		t.Errorf("W1's integration %v; events %q", got, texts)
	}
	if got.GetQuestionId() != "" {
		t.Errorf("W1 question_id = %q; want empty", got.GetQuestionId())
	}
	if paras := lead.all(); len(paras) == 0 || !strings.Contains(paras[len(paras)-1], "The move is yours: retry a correction, drop the work") {
		t.Errorf("expected lead notification, got %v", paras)
	}
	questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Errorf("got %d questions; want 0", len(questions))
	}
	// If a question was attached and answered CHOICE_A: Djinn tries to start a correction worker again.
	// Since provider is still missing, it tells the lead and leaves it to the lead.
	q := in.attachQuestion(t, w1)
	if did := in.answer(t, q, planv1.Choice_CHOICE_A); !strings.Contains(did, "The move is yours: retry a correction, drop the work") {
		t.Errorf("what Djinn did: %q", did)
	}
}

// TestIntegrationFailureCorrectionCleared: a task whose integration failed and is corrected by a worker
// clears that link when the correction's integration record is cleared.
func TestIntegrationFailureCorrectionCleared(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, 0)
	w2 := in.correction(t, w1)
	got, _ := in.integration(t, w1)
	if got.GetCorrectedBy() != w2.GetId() {
		t.Fatalf("W1 corrected by %q; want %q", got.GetCorrectedBy(), w2.GetId())
	}

	none := "none"
	if _, err := in.tasks.Update(t.Context(), connect.NewRequest(&planv1.TaskServiceUpdateRequest{
		TaskId:      w2.GetId(),
		Integration: &none,
	})); err != nil {
		t.Fatal(err)
	}

	got, _ = in.integration(t, w1)
	if got.GetCorrectedBy() != "" {
		t.Errorf("after clearing record, W1 corrected by %q; want empty", got.GetCorrectedBy())
	}
}

// TestIntegrationFailureCorrectionCancelled: a task whose integration failed and is corrected by a worker
// clears that link when the correction worker is cancelled.
func TestIntegrationFailureCorrectionCancelled(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, 0)
	w2 := in.correction(t, w1)
	got, _ := in.integration(t, w1)
	if got.GetCorrectedBy() != w2.GetId() {
		t.Fatalf("W1 corrected by %q; want %q", got.GetCorrectedBy(), w2.GetId())
	}

	// Cancel W2 (status 10).
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		c, err := store.Get[*planv1.Task](t.Context(), tx, w2.GetId())
		if err != nil {
			return err
		}
		c.Status = 10
		if err := tx.Journal("test", "put", c); err != nil {
			return err
		}
		return tx.Put(c)
	}); err != nil {
		t.Fatal(err)
	}

	in.pass(t, time.Second)

	got, texts := in.integration(t, w1)
	if got.GetCorrectedBy() != "" {
		t.Errorf("after correction cancelled, W1 corrected by %q; want empty", got.GetCorrectedBy())
	}
	if got.GetQuestionId() != "" {
		t.Errorf("W1 question_id %q; want empty", got.GetQuestionId())
	}
	wantSuffix := w2.GetCode() + ", its correction worker, was cancelled"
	if len(texts) == 0 || !strings.HasSuffix(texts[len(texts)-1], wantSuffix) {
		t.Errorf("W1 events %q; want suffix %q", texts, wantSuffix)
	}
	questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Errorf("got %d questions; want 0", len(questions))
	}
}

// TestIntegrationFailureCorrectionStopped: a task whose integration failed and is corrected by a worker
// that was stopped by the lead clears that link and records the stop without asking a question.
func TestIntegrationFailureCorrectionStopped(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	in.pass(t, 0)
	w2 := in.correction(t, w1)
	got, _ := in.integration(t, w1)
	if got.GetCorrectedBy() != w2.GetId() {
		t.Fatalf("W1 corrected by %q; want %q", got.GetCorrectedBy(), w2.GetId())
	}

	// Stop W2 (lead stops it).
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		c, err := store.Get[*planv1.Task](t.Context(), tx, w2.GetId())
		if err != nil {
			return err
		}
		c.Status = planv1.TaskStatus_TASK_STATUS_STOPPED
		if err := tx.Journal("test", "put", c); err != nil {
			return err
		}
		return tx.Put(c)
	}); err != nil {
		t.Fatal(err)
	}

	in.pass(t, time.Second)

	got, texts := in.integration(t, w1)
	if got.GetCorrectedBy() != "" {
		t.Errorf("after correction stopped, W1 corrected by %q; want empty", got.GetCorrectedBy())
	}
	if got.GetQuestionId() != "" {
		t.Errorf("W1 question_id %q; want empty", got.GetQuestionId())
	}
	wantSuffix := w2.GetCode() + ", its correction worker, was stopped"
	if len(texts) == 0 || !strings.HasSuffix(texts[len(texts)-1], wantSuffix) {
		t.Errorf("W1 events %q; want suffix %q", texts, wantSuffix)
	}
	questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Errorf("got %d questions; want 0", len(questions))
	}
}

// TestIntegrationWorkAlreadyInBranchSettlesSilently: a task whose branch commits are already in
// the integration branch settles immediately as COMMITTED without running tests or asking questions.
func TestIntegrationWorkAlreadyInBranchSettlesSilently(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	// Even if testCode is 1 (tests fail), the task shouldn't run tests because it's already in the branch!
	in.testCode = 1
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	// Merge W1's branch directly into the integration branch (master) before integration runs.
	if _, err := git(t.Context(), in.repo, "merge", "--no-ff", "-m", "merge into master", w1.GetBranch()); err != nil {
		t.Fatal(err)
	}

	in.pass(t, time.Second)

	got, texts := in.integration(t, w1)
	if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
		t.Errorf("W1 state %v; want COMMITTED", got.GetState())
	}
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "holds the work already") {
		t.Errorf("W1 events %q; want 'holds the work already'", texts)
	}
	questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Errorf("got %d questions; want 0", len(questions))
	}
}

// TestIntegrationDroppedTaskRaisesNoQuestion: a task whose integration was dropped
// (--drop, task.Integration == nil) triggers no question or correction.
func TestIntegrationDroppedTaskRaisesNoQuestion(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	in.testCode = 1
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

	// Drop W1's integration.
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		c, err := store.Get[*planv1.Task](t.Context(), tx, w1.GetId())
		if err != nil {
			return err
		}
		c.Integration = nil
		if err := tx.Journal("test", "put", c); err != nil {
			return err
		}
		return tx.Put(c)
	}); err != nil {
		t.Fatal(err)
	}

	in.pass(t, time.Second)

	questions, err := store.List[*planv1.Question](t.Context(), in.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 0 {
		t.Errorf("got %d questions; want 0", len(questions))
	}
}
