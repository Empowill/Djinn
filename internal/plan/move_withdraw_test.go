package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestQuestionMove: moving an open question to another wish keeps its text, context, options,
// rounds, marks, and icon, retires its old code in the source wish (so codes are never reused),
// gives it the next code in the target wish, and leaves a trace decision block in the source wish.
// If --follow is given, tasks naming the question as their decision follow it into the target wish,
// updating their decision to the new code, renumbering if codes conflict, dropping external dependencies.
// Running tasks prevent moving. Decided or withdrawn questions cannot be moved.
func TestQuestionMove(t *testing.T) {
	ctx := t.Context()
	fake := &workers{}
	c := serve(t, WithWorkers(fake))

	w1Res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Source Wish"}))
	if err != nil {
		t.Fatal(err)
	}
	w1 := w1Res.Msg.GetWish().GetId()

	w2Res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Target Wish"}))
	if err != nil {
		t.Fatal(err)
	}
	w2 := w2Res.Msg.GetWish().GetId()

	// Existing question in target wish to test next code
	c.ask(t, w2, "option A", "option B") // Target wish gets Q01

	// Source wish questions
	q1 := c.ask(t, w1, "Yes", "No") // Source wish gets Q01

	// Add rounds to q1 via enlighten then revise
	ref1 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q1.GetCode()}}
	_, err = c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: ref1, Note: "Need details", WishId: w1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	revRes, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref1, WishId: w1, Context: "Details here", Recommendation: "A: Yes",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q1 = revRes.Msg.GetQuestion()

	// Move q1 to w2
	moveRes, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: ref1,
		Wish:     w2,
		WishId:   w1,
	}))
	if err != nil {
		t.Fatalf("move question: %v", err)
	}
	movedQ := moveRes.Msg.GetQuestion()
	if movedQ.GetCode() != "Q02" {
		t.Errorf("moved question code = %s, want Q02", movedQ.GetCode())
	}
	if movedQ.GetWishId() != w2 {
		t.Errorf("moved question wish_id = %s, want %s", movedQ.GetWishId(), w2)
	}
	if len(movedQ.GetRounds()) != 2 || movedQ.GetRounds()[0].GetNote() != "Need details" {
		t.Errorf("moved question rounds = %v", movedQ.GetRounds())
	}

	// Verify old code Q01 in source wish is retired and not reused
	qNextSource := c.ask(t, w1, "Alpha", "Beta")
	if qNextSource.GetCode() == "Q01" {
		t.Errorf("retired code Q01 was reused in source wish; got %s", qNextSource.GetCode())
	}
	if qNextSource.GetCode() != "Q02" {
		t.Errorf("next code in source wish = %s, want Q02", qNextSource.GetCode())
	}

	// Verify decision block in source wish
	blocks, err := store.List[*planv1.Block](ctx, c.store, store.Where{"wish_id": w1})
	if err != nil {
		t.Fatal(err)
	}
	var trace *planv1.Block
	for _, b := range blocks {
		if b.GetKind() == "decision" && strings.Contains(b.GetTitle(), "moved to Target Wish as Q02") {
			trace = b
			break
		}
	}
	if trace == nil {
		t.Errorf("expected trace decision block in source wish, got blocks: %v", blocks)
	} else if trace.GetContent() != q1.GetText() {
		t.Errorf("trace block content = %q, want %q", trace.GetContent(), q1.GetText())
	}

	// Cannot move to same wish
	refMoved := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: movedQ.GetCode()}}
	if _, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: refMoved,
		Wish:     w2,
		WishId:   w2,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("move to same wish error = %v, want InvalidArgument", err)
	}

	// Answer the moved question
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: refMoved,
		WishId:   w2,
		Choice:   planv1.Choice_CHOICE_A,
	})); err != nil {
		t.Fatalf("answer moved question: %v", err)
	}

	// Cannot move decided question
	if _, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: refMoved,
		Wish:     w1,
		WishId:   w2,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("move decided question error = %v, want FailedPrecondition", err)
	}
}

// TestQuestionMoveFollow: tasks naming the question as their decision follow it into the new wish.
// Conflicting task codes are renumbered, decisions updated, external deps dropped. Running tasks prevent move.
func TestQuestionMoveFollow(t *testing.T) {
	ctx := t.Context()
	fake := &workers{}
	c := serve(t, WithWorkers(fake))

	w1Res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Wish 1"}))
	if err != nil {
		t.Fatal(err)
	}
	w1 := w1Res.Msg.GetWish().GetId()

	w2Res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Wish 2"}))
	if err != nil {
		t.Fatal(err)
	}
	w2 := w2Res.Msg.GetWish().GetId()

	q := c.ask(t, w1, "Option 1", "Option 2") // Q01 in w1
	qRef := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}

	// Target wish already has task W1
	targetTask := &planv1.Task{
		Id:     store.NewID(),
		WishId: w2,
		Code:   "W1",
		Title:  "Existing task in target",
		Status: planv1.TaskStatus_TASK_STATUS_DONE,
	}
	c.put(t, targetTask)

	// Source wish has an unrelated task and two tasks dependent on q:
	// task1: W1 in source wish, decision: Q01
	// task2: W2 in source wish, decision: Q01, depends on task1 and unrelatedTask
	unrelatedTask := &planv1.Task{
		Id:     store.NewID(),
		WishId: w1,
		Code:   "W3",
		Title:  "Unrelated task in source",
		Status: planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	task1 := &planv1.Task{
		Id:       store.NewID(),
		WishId:   w1,
		Code:     "W1",
		Title:    "Following task 1",
		Decision: q.GetCode(),
		Status:   planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	task2 := &planv1.Task{
		Id:        store.NewID(),
		WishId:    w1,
		Code:      "W2",
		Title:     "Following task 2",
		Decision:  q.GetCode(),
		DependsOn: []string{task1.GetId(), unrelatedTask.GetId()},
		Status:    planv1.TaskStatus_TASK_STATUS_PENDING,
	}
	c.put(t, unrelatedTask, task1, task2)

	// Move with follow
	moveRes, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: qRef,
		Wish:     w2,
		WishId:   w1,
		Follow:   true,
	}))
	if err != nil {
		t.Fatalf("move with follow: %v", err)
	}

	movedQ := moveRes.Msg.GetQuestion()
	if movedQ.GetCode() != "Q01" { // first question in w2
		t.Errorf("moved question code = %s, want Q01", movedQ.GetCode())
	}

	following := moveRes.Msg.GetTasks()
	if len(following) != 2 {
		t.Fatalf("following tasks count = %d, want 2", len(following))
	}

	// Verify task1 got renumbered because W1 was taken in w2 (should be W2)
	// And task2 got W3
	t1Updated, err := store.Get[*planv1.Task](ctx, c.store, task1.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if t1Updated.GetWishId() != w2 {
		t.Errorf("task1 wish_id = %s, want %s", t1Updated.GetWishId(), w2)
	}
	if t1Updated.GetDecision() != movedQ.GetCode() {
		t.Errorf("task1 decision = %s, want %s", t1Updated.GetDecision(), movedQ.GetCode())
	}
	if t1Updated.GetCode() == "W1" {
		t.Errorf("task1 code should have renumbered from W1; got %s", t1Updated.GetCode())
	}

	t2Updated, err := store.Get[*planv1.Task](ctx, c.store, task2.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if t2Updated.GetWishId() != w2 {
		t.Errorf("task2 wish_id = %s, want %s", t2Updated.GetWishId(), w2)
	}
	if t2Updated.GetDecision() != movedQ.GetCode() {
		t.Errorf("task2 decision = %s, want %s", t2Updated.GetDecision(), movedQ.GetCode())
	}
	// External dependency unrelatedTask should have been dropped, task1 retained
	if len(t2Updated.GetDependsOn()) != 1 || t2Updated.GetDependsOn()[0] != task1.GetId() {
		t.Errorf("task2 dependsOn = %v, want [%s]", t2Updated.GetDependsOn(), task1.GetId())
	}

	// Wakes should have been triggered
	if fake.wakes == 0 {
		t.Errorf("workers.Wake was not called after tasks moved")
	}

	// Now test refusal when following task runs
	qAnother := c.ask(t, w1, "A", "B")
	runningTask := &planv1.Task{
		Id:       store.NewID(),
		WishId:   w1,
		Code:     "W10",
		Decision: qAnother.GetCode(),
		Status:   planv1.TaskStatus_TASK_STATUS_RUNNING,
	}
	c.put(t, runningTask)

	qAnotherRef := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: qAnother.GetCode()}}
	if _, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: qAnotherRef,
		Wish:     w2,
		WishId:   w1,
		Follow:   true,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("move with running task error = %v, want FailedPrecondition", err)
	}
}

// TestQuestionWithdraw: withdrawing an open question closes it without an answer.
// It is excluded from open questions list and Ready calculation, cannot be answered,
// cannot be withdrawn again, and does not produce a decision block.
func TestQuestionWithdraw(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wishID := c.wish(t)

	q1 := c.ask(t, wishID, "Yes", "No")
	q2 := c.ask(t, wishID, "A", "B")

	ref1 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q1.GetCode()}}
	note := "No longer relevant after refactoring"

	withdrawRes, err := c.questions.Withdraw(ctx, connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
		Question: ref1,
		WishId:   wishID,
		Note:     note,
	}))
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	wQ := withdrawRes.Msg.GetQuestion()
	if wQ.GetWithdrawal() == nil {
		t.Fatal("withdrawal is nil on response")
	}
	if wQ.GetWithdrawal().GetNote() != note {
		t.Errorf("withdrawal note = %q, want %q", wQ.GetWithdrawal().GetNote(), note)
	}
	if wQ.GetWithdrawal().GetCreateTime() == nil {
		t.Errorf("withdrawal create_time is nil")
	}

	// List with open=true should return only q2, not q1
	listRes, err := c.questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{
		WishId: wishID,
		Open:   true,
	}))
	if err != nil {
		t.Fatalf("list open questions: %v", err)
	}
	if len(listRes.Msg.GetQuestions()) != 1 || listRes.Msg.GetQuestions()[0].GetCode() != q2.GetCode() {
		t.Errorf("open questions = %v, want only %s", listRes.Msg.GetQuestions(), q2.GetCode())
	}

	// List without open=true returns both
	allRes, err := c.questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{
		WishId: wishID,
		Open:   false,
	}))
	if err != nil {
		t.Fatalf("list all questions: %v", err)
	}
	if len(allRes.Msg.GetQuestions()) != 2 {
		t.Errorf("all questions count = %d, want 2", len(allRes.Msg.GetQuestions()))
	}

	// Ready() calculation should not be blocked by withdrawn question
	taskDone := &planv1.Task{
		Id:     store.NewID(),
		WishId: wishID,
		Code:   "W1",
		Status: planv1.TaskStatus_TASK_STATUS_DONE,
	}
	c.put(t, taskDone)

	// Answer q2
	ref2 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q2.GetCode()}}
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: ref2,
		WishId:   wishID,
		Choice:   planv1.Choice_CHOICE_A,
	})); err != nil {
		t.Fatalf("answer q2: %v", err)
	}

	// Now tasks: [W1 (DONE)], questions: [q1 (withdrawn), q2 (answered)].
	// Ready should be true!
	tasks, _ := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": wishID})
	questions, _ := store.List[*planv1.Question](ctx, c.store, store.Where{"wish_id": wishID})
	if !Ready(tasks, questions) {
		t.Errorf("Ready() = false, want true when all questions are answered or withdrawn")
	}

	// Cannot answer a withdrawn question
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: ref1,
		WishId:   wishID,
		Choice:   planv1.Choice_CHOICE_YES,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("answer withdrawn question error = %v, want FailedPrecondition", err)
	}

	// Cannot withdraw again
	if _, err := c.questions.Withdraw(ctx, connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
		Question: ref1,
		WishId:   wishID,
		Note:     "again",
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("withdraw already withdrawn question error = %v, want FailedPrecondition", err)
	}

	// Cannot move a withdrawn question
	wOther := c.wish(t)
	if _, err := c.questions.Move(ctx, connect.NewRequest(&planv1.QuestionServiceMoveRequest{
		Question: ref1,
		Wish:     wOther,
		WishId:   wishID,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("move withdrawn question error = %v, want FailedPrecondition", err)
	}

	// Withdrawing does NOT create a decision block
	blocks, err := store.List[*planv1.Block](ctx, c.store, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.GetKind() == "decision" && strings.Contains(b.GetTitle(), q1.GetText()) {
			t.Errorf("withdrawn question should not leave a decision block: %v", b)
		}
	}
}
