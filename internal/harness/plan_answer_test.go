package harness

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/testx"
)

// planEnv is a wish of a test where questions carry planned tasks (decision Q25).
type planEnv struct {
	*env
	wishID    string
	questions planv1connect.QuestionServiceClient
}

func newPlanEnv(t *testing.T) planEnv {
	t.Helper()
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	return planEnv{env: e, wishID: wishID, questions: planv1connect.NewQuestionServiceClient(e.srv.Client(), e.srv.URL)}
}

// ask asks a question of two options, Olive and Paraffin, A's plan plan.
func (p planEnv) ask(t *testing.T, plan string) *planv1.Question {
	t.Helper()
	res, err := p.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", WishId: p.wishID, Options: []string{"Olive", "Paraffin"}, Plan: []string{plan},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

func (p planEnv) answer(t *testing.T, q *planv1.Question, note string) *planv1.Question {
	t.Helper()
	res, err := p.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}, Choice: planv1.Choice_CHOICE_A,
		Note: note, WishId: p.wishID, By: planv1.Closer_CLOSER_DEVELOPER,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

func (p planEnv) launch(t *testing.T, req *planv1.QuestionServiceLaunchRequest) (*planv1.QuestionServiceLaunchResponse, error) {
	t.Helper()
	req.WishId = p.wishID
	res, err := p.questions.Launch(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// tasks are the wish's tasks.
func (p planEnv) tasks(t *testing.T) []*planv1.Task {
	t.Helper()
	res, err := p.env.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: p.wishID}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTasks()
}

// spawn spawns a task of the wish: an azima when kind says so, else a fake worker that plays prompt, after the tasks
// after names.
func (p planEnv) spawn(t *testing.T, title, prompt string, kind planv1.TaskKind, after ...string) *planv1.Task {
	t.Helper()
	req := &planv1.TaskServiceSpawnRequest{WishId: p.wishID, Title: title, Kind: kind}
	if kind != planv1.TaskKind_TASK_KIND_AZIMA {
		req.Provider, req.Prompt, req.After = planv1.Provider_PROVIDER_FAKE, prompt, after
	}
	res, err := p.env.tasks.Spawn(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetTask()
}

func questionRef(q *planv1.Question) *planv1.QuestionRef {
	return &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}
}

// TestLaunchPlan tests QuestionService.Answer and QuestionService.Launch for questions carrying planned tasks (Q25).
func TestLaunchPlan(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("plain", func(t *testing.T) {
		t.Parallel()
		p := newPlanEnv(t)
		q := p.ask(t, "option: A\ntitle: Light the olive lamp\nprovider: fake\n\ntext lit")
		if len(q.GetPlans()) != 1 || q.GetPlans()[0].GetOption() != planv1.Choice_CHOICE_A || q.GetPlans()[0].GetPrompt() != "text lit" {
			t.Fatalf("plans = %v", q.GetPlans())
		}
		answered := p.answer(t, q, "")
		tasks := p.tasks(t)
		if len(tasks) != 1 || tasks[0].GetTitle() != "Light the olive lamp" || tasks[0].GetDecision() != q.GetCode() ||
			tasks[0].GetProvider() != planv1.Provider_PROVIDER_FAKE {
			t.Fatalf("tasks = %v", tasks)
		}
		if got, want := p.lastDid(), tasks[0].GetCode()+" launched as planned"; got != want {
			t.Errorf("did %q, want %q", got, want)
		}
		if got := answered.GetPlans()[0].GetTaskId(); got != tasks[0].GetId() {
			t.Errorf("the answer's plan holds task %q, want %s", got, tasks[0].GetId())
		}
		if _, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q)}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), "launched already: "+tasks[0].GetCode()) {
			t.Errorf("a second launch: %v", err)
		}
	})

	t.Run("note", func(t *testing.T) {
		t.Parallel()
		p := newPlanEnv(t)
		q := p.ask(t, "title: Light the olive lamp\noption: a\nprovider: fake\n\ntext lit")
		p.answer(t, q, "but use the blue wick")
		if tasks := p.tasks(t); len(tasks) != 0 {
			t.Fatalf("a note launched %v", tasks)
		}
		if got, want := p.lastDid(), "Its plan waits for you, as the answer has a note: djinn question launch "+q.GetCode()+
			" launches it"; got != want {
			t.Errorf("did %q, want %q", got, want)
		}
		res, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q), Title: "Light it, blue wick"})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetTask().GetTitle() != "Light it, blue wick" || res.GetTask().GetDecision() != q.GetCode() {
			t.Errorf("launched %v", res.GetTask())
		}
		if plan := res.GetQuestion().GetPlans()[0]; plan.GetTaskId() != res.GetTask().GetId() || plan.GetTitle() != "Light it, blue wick" {
			t.Errorf("the plan after launch: %v", plan)
		}
		if _, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q)}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("a second launch: %v", err)
		}
		if n := len(p.tasks(t)); n != 1 {
			t.Errorf("%d tasks, want the one launched", n)
		}
	})

	t.Run("dependency", func(t *testing.T) {
		t.Parallel()
		p := newPlanEnv(t)
		before := p.spawn(t, "Buy oil", "sleep 1m", planv1.TaskKind_TASK_KIND_UNSPECIFIED)
		blocked := p.spawn(t, "Trim the wick", "text trimmed", planv1.TaskKind_TASK_KIND_UNSPECIFIED, before.GetCode())
		q := p.ask(t, "option: A\ntitle: Light the olive lamp\nprovider: fake\nafter: "+before.GetCode()+"\nblocks: "+
			blocked.GetCode()+"\n\ntext lit")
		plan := q.GetPlans()[0]
		if plan.GetAfter()[0] != before.GetId() || plan.GetBlocks()[0] != blocked.GetId() || len(plan.GetDependencies()) != 2 ||
			plan.GetDependencies()[1].GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			t.Fatalf("plan = %v", plan)
		}
		p.answer(t, q, "")
		if n := len(p.tasks(t)); n != 2 {
			t.Fatalf("%d tasks: a plan with dependencies launched by itself", n)
		}
		if got := p.lastDid(); !strings.HasPrefix(got, "Its plan waits for you, as it comes after tasks and blocks others:") {
			t.Errorf("did %q", got)
		}
		res, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q)})
		if err != nil {
			t.Fatal(err)
		}
		if deps := res.GetTask().GetDependsOn(); len(deps) != 1 || deps[0] != before.GetId() {
			t.Errorf("the task comes after %v, want %s", deps, before.GetId())
		}
		if deps := p.get(t, blocked.GetId()).GetDependsOn(); !slices.Equal(deps, []string{before.GetId(), res.GetTask().GetId()}) {
			t.Errorf("%s waits for %v, want the launched task", blocked.GetCode(), deps)
		}
	})

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		p := newPlanEnv(t)
		azima := p.spawn(t, "Lamps", "", planv1.TaskKind_TASK_KIND_AZIMA)
		q := p.ask(t, "option: A\ntitle: Light the olive lamp\nprovider: fake\npart-of: "+azima.GetCode()+"\n\ntext lit")
		if _, err := p.env.tasks.Done(t.Context(), connect.NewRequest(&planv1.TaskServiceDoneRequest{TaskId: azima.GetId(), Note: "lit"})); err != nil {
			t.Fatal(err)
		}
		p.answer(t, q, "")
		if n := len(p.tasks(t)); n != 1 {
			t.Fatalf("%d tasks: a stale plan launched", n)
		}
		if got := p.lastDid(); !strings.HasPrefix(got, "Its plan no longer holds (azima "+azima.GetCode()+" is done), not launched") {
			t.Errorf("did %q", got)
		}
		if got := p.get(t, azima.GetId()).GetStatus(); got != planv1.TaskStatus_TASK_STATUS_DONE {
			t.Errorf("the azima is %v: the plan reopened it", got)
		}
		_, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q)})
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "no longer holds: azima "+azima.GetCode()+" is done") {
			t.Errorf("launch as it is: %v", err)
		}
		none := ""
		res, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q), PartOf: &none})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetTask().GetPartOf() != "" {
			t.Errorf("launched part of %q, want none", res.GetTask().GetPartOf())
		}

		// A dependency cancelled since the question: the plan no longer holds either.
		before := p.spawn(t, "Buy oil", "sleep 1m", planv1.TaskKind_TASK_KIND_UNSPECIFIED)
		q2 := p.ask(t, "option: A\ntitle: Light the olive lamp again\nprovider: fake\nafter: "+before.GetCode())
		if _, err := p.env.tasks.Cancel(t.Context(), connect.NewRequest(&planv1.TaskServiceCancelRequest{TaskId: before.GetId(), Note: "no oil"})); err != nil {
			t.Fatal(err)
		}
		p.answer(t, q2, "")
		if got := p.lastDid(); !strings.HasPrefix(got, "Its plan no longer holds ("+before.GetCode()+", which it comes after, was cancelled)") {
			t.Errorf("did %q", got)
		}
	})

	t.Run("no_plan", func(t *testing.T) {
		t.Parallel()
		p := newPlanEnv(t)
		q := p.ask(t, "option: B\ntitle: Light the paraffin lamp\nprovider: fake")
		p.answer(t, q, "")
		if n := len(p.tasks(t)); n != 0 {
			t.Errorf("%d tasks: an option without a plan launched one", n)
		}
		if got := p.lastDid(); got != "" {
			t.Errorf("did %q, want nothing", got)
		}
		if _, err := p.launch(t, &planv1.QuestionServiceLaunchRequest{Question: questionRef(q)}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), "planned no task") {
			t.Errorf("launch: %v", err)
		}
	})
}
