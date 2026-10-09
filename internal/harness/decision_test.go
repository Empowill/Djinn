package harness

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// TestSpawnDecision: djinn task spawn --decision stores the decision the task comes from, as the question's code or
// the block's id; an open question or a block that is no decision is refused, and no task is made.
func TestSpawnDecision(t *testing.T) {
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	questions := planv1connect.NewQuestionServiceClient(e.srv.Client(), e.srv.URL)
	blocks := planv1connect.NewBlockServiceClient(e.srv.Client(), e.srv.URL)
	ask := func() *planv1.Question {
		res, err := questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: "SQLite?", WishId: wishID}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	q, open := ask(), ask()
	if _, err := questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}, Choice: planv1.Choice_CHOICE_YES, WishId: wishID,
	})); err != nil {
		t.Fatal(err)
	}
	put := func(kind string) string {
		res, err := blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{WishId: wishID, Kind: kind, Title: kind}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetBlock().GetId()
	}
	decision, section := put("decision"), put("section")
	spawn := func(ref string) (*planv1.Task, error) {
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
			WishId: wishID, Title: "Follow " + ref, Provider: planv1.Provider_PROVIDER_FAKE, Later: true, Decision: ref,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	for ref, want := range map[string]string{q.GetCode(): q.GetCode(), q.GetId(): q.GetCode(), decision: decision} {
		task, err := spawn(ref)
		if err != nil {
			t.Fatalf("--decision %s: %v", ref, err)
		}
		if task.GetDecision() != want || e.get(t, task.GetId()).GetDecision() != want {
			t.Errorf("--decision %s: the task keeps %q, want %q", ref, task.GetDecision(), want)
		}
	}
	for ref, code := range map[string]connect.Code{
		open.GetCode(): connect.CodeFailedPrecondition,
		section:        connect.CodeInvalidArgument,
		"Q99":          connect.CodeInvalidArgument,
		"W1":           connect.CodeInvalidArgument,
	} {
		if _, err := spawn(ref); connect.CodeOf(err) != code {
			t.Errorf("--decision %s: %v, want %v", ref, err, code)
		}
	}
	list, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.Msg.GetTasks()); n != 3 {
		t.Errorf("%d tasks, want the 3 spawned from a decision", n)
	}
}
