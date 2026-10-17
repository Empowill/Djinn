package plan

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// waitFixture is a store with a wish and its work task W1, the questions service on it, and what its lead was told.
func waitFixture(t *testing.T) (*Questions, *planv1.Task, *[]*planv1.Question) {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	wish := &planv1.Wish{Id: store.NewID(), Title: "Cache", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := &planv1.Task{Id: store.NewID(), WishId: wish.GetId(), Code: "W1", Title: "Pick a cache",
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_FAKE}
	if err := s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", task); err != nil {
			return err
		}
		if err := tx.Put(wish); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	var asked []*planv1.Question
	q := &Questions{Store: s, Asked: []func(_ context.Context, q *planv1.Question){
		func(_ context.Context, q *planv1.Question) { asked = append(asked, q) },
	}}
	return q, task, &asked
}

// put journals m, then stores it.
func put(tx *store.Tx, m proto.Message) error {
	if err := tx.Journal("test", "test/put", m); err != nil {
		return err
	}
	return tx.Put(m)
}

// ask asks a question as the worker of task, waiting for the answer or not.
func ask(t *testing.T, q *Questions, task *planv1.Task, wait bool) (*planv1.Question, error) {
	t.Helper()
	ctx := WithCaller(t.Context(), &WorkerCaller{TaskID: task.GetId(), WishID: task.GetWishId()})
	res, err := q.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which cache?", Options: []string{"Keep the cache", "Drop the cache"}, WishId: task.GetWishId(),
		TaskId: task.GetId(), Wait: wait,
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetQuestion(), nil
}

// answer answers question in the store, as the developer.
func answerIn(t *testing.T, q *Questions, question *planv1.Question) {
	t.Helper()
	question.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A, CreateTime: timestamppb.Now(),
		Actor: planv1.Closer_CLOSER_DEVELOPER}
	if err := q.Store.Tx(t.Context(), func(tx *store.Tx) error { return put(tx, question) }); err != nil {
		t.Fatal(err)
	}
}

// TestBlockingQuestionCaps (W260): a worker asks one blocking question at a time and two in all, about its own task,
// and three that do not block; past them it is told to decide. The lead hears of a blocking one; the window shows it
// with the task that waits for it.
func TestBlockingQuestionCaps(t *testing.T) {
	q, task, asked := waitFixture(t)
	first, err := ask(t, q, task, true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.GetWait() || first.GetAboutTaskId() != task.GetId() || len(*asked) != 1 || !(*asked)[0].GetWait() {
		t.Errorf("first blocking question = %v, asked %v", first, *asked)
	}
	if _, err := ask(t, q, task, true); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), first.GetCode()+" already waits for its answer, one blocking question at a time") {
		t.Errorf("a second open blocking question: %v", err)
	}
	answerIn(t, q, first)
	second, err := ask(t, q, task, true)
	if err != nil {
		t.Fatalf("a blocking question once the first is answered: %v", err)
	}
	answerIn(t, q, second)
	_, err = ask(t, q, task, true)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(),
		"W1 asked its 2 blocking questions ("+first.GetCode()+", "+second.GetCode()+"): decide, and say it in your final note") {
		t.Errorf("a third blocking question: %v", err)
	}
	for range FreeCap {
		if _, err := ask(t, q, task, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ask(t, q, task, false); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "W1 asked its 3 questions that do not block") || !strings.Contains(err.Error(), decideLine) {
		t.Errorf("a fourth question that does not block: %v", err)
	}
	// The lead and the developer ask as many as they need, and never with --wait.
	for range 2 {
		if _, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Ship it?", WishId: task.GetWishId(),
		})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Ship it?", WishId: task.GetWishId(), TaskId: task.GetId(), Wait: true,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "--wait is for a worker") {
		t.Errorf("--wait outside a worker: %v", err)
	}
}

// TestBlockingQuestionKinds: an investigation ends with its question, and an ifrit never waits: neither asks with
// --wait; nor does a question that plans tasks, as its answer comes back to its worker.
func TestBlockingQuestionKinds(t *testing.T) {
	q, task, _ := waitFixture(t)
	for _, c := range []struct {
		kind planv1.TaskKind
		want string
	}{
		{planv1.TaskKind_TASK_KIND_INVESTIGATION, "an investigation ends with its question"},
		{planv1.TaskKind_TASK_KIND_IFRIT, "only a work task waits for an answer"},
	} {
		task.Kind = c.kind
		if err := q.Store.Tx(t.Context(), func(tx *store.Tx) error { return put(tx, task) }); err != nil {
			t.Fatal(err)
		}
		if _, err := ask(t, q, task, true); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s asks with --wait: %v", c.kind, err)
		}
		if _, err := ask(t, q, task, false); err != nil {
			t.Errorf("%s asks without --wait: %v", c.kind, err)
		}
	}
	task.Kind = planv1.TaskKind_TASK_KIND_WORK
	if err := q.Store.Tx(t.Context(), func(tx *store.Tx) error { return put(tx, task) }); err != nil {
		t.Fatal(err)
	}
	ctx := WithCaller(t.Context(), &WorkerCaller{TaskID: task.GetId(), WishID: task.GetWishId()})
	_, err := q.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which cache?", Options: []string{"Keep", "Drop"}, WishId: task.GetWishId(), TaskId: task.GetId(),
		Wait: true, Plan: []string{"option: A\ntitle: Keep the cache\n\nKeep it."},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "it plans no task") {
		t.Errorf("a blocking question with a plan: %v", err)
	}
}

// TestDecisionPrompt: the decision a worker is continued with names the question, the option and its letter, the
// note, and who answered when the lead did.
func TestDecisionPrompt(t *testing.T) {
	q := &planv1.Question{Code: "Q51", Options: []string{"Keep the cache.", "Drop the cache"},
		Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_B, Note: " keep it small. "}}
	if got, want := DecisionPrompt(q), "Decision Q51: Drop the cache (B) - keep it small. Go on with your task."; got != want {
		t.Errorf("DecisionPrompt = %q, want %q", got, want)
	}
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A, Actor: planv1.Closer_CLOSER_LEAD}
	got := DecisionPrompt(q)
	if want := "Decision Q51, by the lead: Keep the cache (A). Go on with your task."; got != want {
		t.Errorf("DecisionPrompt = %q, want %q", got, want)
	}
	if !IsDecision(got, "Q51") || IsDecision(got, "Q5") || IsDecision("Decision Q51 is late", "Q51") {
		t.Error("IsDecision does not tell the decision of Q51 alone")
	}
	yes := &planv1.Question{Code: "Q52", Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}}
	if got, want := DecisionPrompt(yes), "Decision Q52: yes. Go on with your task."; got != want {
		t.Errorf("DecisionPrompt = %q, want %q", got, want)
	}
}
