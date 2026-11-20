package plan

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// holdFixture is waitFixture's wish with more work: W2 planned, the azima T3 and its part W4, all not started, and W5
// done.
func holdFixture(t *testing.T) (*Questions, map[string]*planv1.Task) {
	t.Helper()
	q, w1, _ := waitFixture(t)
	pending := planv1.TaskStatus_TASK_STATUS_PENDING
	tasks := map[string]*planv1.Task{"W1": w1}
	t3 := &planv1.Task{Id: store.NewID(), WishId: w1.GetWishId(), Code: "T3", Title: "Cache", Status: pending,
		Kind: planv1.TaskKind_TASK_KIND_AZIMA}
	for _, x := range []*planv1.Task{
		{Id: store.NewID(), WishId: w1.GetWishId(), Code: "W2", Title: "Wire the cache", Status: pending, Scheduled: true},
		t3,
		{Id: store.NewID(), WishId: w1.GetWishId(), Code: "W4", Title: "Size it", Status: pending, Scheduled: true,
			PartOf: t3.GetId()},
		{Id: store.NewID(), WishId: w1.GetWishId(), Code: "W5", Title: "Measure", Status: planv1.TaskStatus_TASK_STATUS_DONE},
	} {
		tasks[x.GetCode()] = x
	}
	if err := q.Store.Tx(t.Context(), func(tx *store.Tx) error {
		for _, x := range tasks {
			if err := put(tx, x); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return q, tasks
}

// heldBy reads the tasks of the wish again, and gives the codes of those that question holds.
func heldBy(t *testing.T, q *Questions, question *planv1.Question) ([]*planv1.Task, []string) {
	t.Helper()
	tasks, err := store.List[*planv1.Task](t.Context(), q.Store, store.Where{"wish_id": question.GetWishId()})
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, x := range tasks {
		if slices.Contains(x.GetHoldQuestionIds(), question.GetId()) {
			codes = append(codes, x.GetCode())
		}
	}
	slices.Sort(codes)
	return tasks, codes
}

// TestQuestionHoldsWork (W107, Q64 C): the lead asks a question that holds work not started, a task and an azima; the
// tasks keep it, and it is blocking, naming them, whatever its marks. A revision changes what it holds, the tasks it
// no longer names released. Work that started, the asker's own task, or a task the wish lacks is refused.
func TestQuestionHoldsWork(t *testing.T) {
	t.Parallel()
	q, tasks := holdFixture(t)
	wish := tasks["W1"].GetWishId()
	res, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which cache?", Options: []string{"Keep", "Drop"}, WishId: wish, Holds: []string{"w2, T3"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	question := res.Msg.GetQuestion()
	all, held := heldBy(t, q, question)
	if !slices.Equal(held, []string{"T3", "W2"}) {
		t.Fatalf("held %v, want T3 and W2", held)
	}
	blocked := render.Blocked(all)
	if got := render.UrgencyOf(question, blocked); got != render.Blocking {
		t.Errorf("urgency %d, want blocking", got)
	}
	if got := slices.Sorted(slices.Values(blocked[question.GetId()])); !slices.Equal(got, []string{"T3", "W2"}) {
		t.Errorf("blocks %v, want T3 and W2", got)
	}

	for _, c := range []struct {
		holds string
		code  connect.Code
		want  string
	}{
		{"W5", connect.CodeFailedPrecondition, "--holds W5: it is done; a question holds only work that has not started"},
		{"W1", connect.CodeFailedPrecondition, "--holds W1: it is running"},
		{"W9", connect.CodeInvalidArgument, "--holds W9: not a task of the wish; plan it after the question instead"},
	} {
		_, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Ship it?", WishId: wish, Holds: []string{c.holds},
		}))
		if connect.CodeOf(err) != c.code || !strings.Contains(err.Error(), c.want) {
			t.Errorf("--holds %s: %v, want %q", c.holds, err, c.want)
		}
	}
	w1 := tasks["W1"]
	tasks["W1"].Status = planv1.TaskStatus_TASK_STATUS_PENDING // As if not started: still the asker's own task.
	if err := q.Store.Tx(t.Context(), func(tx *store.Tx) error { return put(tx, w1) }); err != nil {
		t.Fatal(err)
	}
	ctx := WithCaller(t.Context(), &WorkerCaller{TaskID: w1.GetId(), WishID: wish})
	if _, err := q.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Mine?", WishId: wish, TaskId: w1.GetId(), Holds: []string{"W1"},
	})); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "with --wait") {
		t.Errorf("--holds of the asker's own task: %v", err)
	}

	revise := func(holds ...string) []string {
		t.Helper()
		if _, err := q.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: question.GetId()}}, Holds: holds,
		})); err != nil {
			t.Fatal(err)
		}
		_, held := heldBy(t, q, question)
		return held
	}
	if held := revise("T3"); !slices.Equal(held, []string{"T3"}) {
		t.Errorf("revised to T3: held %v", held)
	}
	if _, err := q.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: question.GetId()}}, Recommendation: "Keep",
	})); err != nil {
		t.Fatal(err)
	}
	if _, held := heldBy(t, q, question); !slices.Equal(held, []string{"T3"}) {
		t.Errorf("a revision without --holds changed them: held %v", held)
	}
	if held := revise(""); len(held) != 0 {
		t.Errorf("revised with --holds \"\": held %v", held)
	}
	all, _ = heldBy(t, q, question)
	if got := render.UrgencyOf(question, render.Blocked(all)); got != render.Later {
		t.Errorf("holding nothing: urgency %d, want later", got)
	}
}

// TestQuestionReleasesWork (W107): the answer releases what a question held, a withdrawal too; the worker of a task
// it held, or a part of an azima it held, starts with the decision, or with the withdrawal and its note.
func TestQuestionReleasesWork(t *testing.T) {
	t.Parallel()
	q, tasks := holdFixture(t)
	wish := tasks["W1"].GetWishId()
	askHolding := func(holds string) *planv1.Question {
		t.Helper()
		res, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Which cache?", Options: []string{"Keep the cache", "Drop it"}, WishId: wish, Holds: []string{holds},
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	read := func(code string) *planv1.Task {
		t.Helper()
		x, err := store.Get[*planv1.Task](t.Context(), q.Store, tasks[code].GetId())
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	decided, withdrawn := askHolding("W2"), askHolding("T3")
	for _, code := range []string{"W2", "W4"} {
		if got := HeldPrompt(t.Context(), q.Store, read(code)); got != "" {
			t.Errorf("%s while its question is open: %q, want nothing", code, got)
		}
	}
	if _, err := q.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: decided.GetId()}}, Choice: planv1.Choice_CHOICE_B, Note: "small first",
	})); err != nil {
		t.Fatal(err)
	}
	want := "This task waited for these questions; they are settled:\n- Decision " + decided.GetCode() +
		": Drop it (B) - small first."
	if got := HeldPrompt(t.Context(), q.Store, read("W2")); got != want {
		t.Errorf("W2's worker reads %q, want %q", got, want)
	}
	if _, err := q.Withdraw(t.Context(), connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: withdrawn.GetId()}}, Note: "the azima goes on without it",
	})); err != nil {
		t.Fatal(err)
	}
	want = "This task waited for these questions; they are settled:\n- " + withdrawn.GetCode() +
		" was withdrawn without an answer: the azima goes on without it."
	if got := HeldPrompt(t.Context(), q.Store, read("W4")); got != want {
		t.Errorf("W4, part of T3, reads %q, want %q", got, want)
	}
	// Settled, they are blocking no more: the window and the brief ask only of open questions.
	for _, question := range []*planv1.Question{decided, withdrawn} {
		fresh, err := store.Get[*planv1.Question](t.Context(), q.Store, question.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if openQuestion(fresh) {
			t.Errorf("%s is still open", fresh.GetCode())
		}
	}
}

// TestHoldingQuestions (W107): a task spawned after a question (--after W1,Q64) is held by it while it is open; a
// question decided or withdrawn, or one the wish lacks, is refused.
func TestHoldingQuestions(t *testing.T) {
	t.Parallel()
	questions, tasks := SplitQuestions([]string{"W1,q64", "T3 Q101"})
	if !slices.Equal(questions, []string{"q64", "Q101"}) || !slices.Equal(tasks, []string{"W1", "T3"}) {
		t.Errorf("SplitQuestions = %v, %v", questions, tasks)
	}
	q, all := holdFixture(t)
	wish := all["W1"].GetWishId()
	res, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "Ship it?", WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	open := res.Msg.GetQuestion()
	ids, err := HoldingQuestions(t.Context(), q.Store, wish, []string{strings.ToLower(open.GetCode()), open.GetCode()})
	if err != nil || !slices.Equal(ids, []string{open.GetId()}) {
		t.Errorf("HoldingQuestions(%s) = %v, %v", open.GetCode(), ids, err)
	}
	if _, err := HoldingQuestions(t.Context(), q.Store, wish, []string{"Q99"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a question the wish lacks: %v", err)
	}
	answerIn(t, q, open)
	if _, err := HoldingQuestions(t.Context(), q.Store, wish, []string{open.GetCode()}); connect.CodeOf(err) !=
		connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "is decided: it holds nothing") {
		t.Errorf("a decided question: %v", err)
	}
}
