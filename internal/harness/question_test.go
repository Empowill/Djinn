package harness

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/testx"
)

// fakeProject is a project folder whose workers, question workers included, run the fake provider, with more
// settings when given.
func fakeProject(t *testing.T, more string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plan.SettingsFile), []byte("provider: PROVIDER_FAKE\n"+more), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type asker struct {
	planv1connect.QuestionServiceClient
	wishID string
}

func (e *env) asker(wishID string) asker {
	return asker{planv1connect.NewQuestionServiceClient(e.srv.Client(), e.srv.URL), wishID}
}

func (q asker) ask(t *testing.T, context string) *planv1.Question {
	t.Helper()
	res, err := q.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", WishId: q.wishID, Options: []string{"SQLite", "Postgres"}, Context: context,
		Recommendation: "A: one file, no server",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

func (q asker) answer(t *testing.T, code string, choice planv1.Choice, note string) {
	t.Helper()
	if _, err := q.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: code}}, Choice: choice, Note: note, WishId: q.wishID,
	})); err != nil {
		t.Fatal(err)
	}
}

func (q asker) enlighten(t *testing.T, code, note string) {
	t.Helper()
	if _, err := q.Enlighten(t.Context(), connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: code}}, Note: note, WishId: q.wishID,
	})); err != nil {
		t.Fatal(err)
	}
}

// roles are the question workers of the wish, oldest first.
func (e *env) roles(t *testing.T, wishID string, role planv1.TaskRole) []*planv1.Task {
	t.Helper()
	res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.Task
	for _, task := range res.Msg.GetTasks() {
		if task.GetRole() == role {
			out = append(out, task)
		}
	}
	return out
}

// prompt is the first event of the task: what it was asked.
func (e *env) prompt(t *testing.T, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	events := e.watch(ctx, t, id, 0)
	if len(events) == 0 || events[0].GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
		t.Fatalf("task %s: no prompt first: %v", id, eventKinds(events))
	}
	return events[0].GetText()
}

// TestAnswerStartsNoWorker: an answer never starts a worker: the lead turns answers into tasks itself.
func TestAnswerStartsNoWorker(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	lim := &limit{slots: 1}
	e := up(t, t.TempDir(), WithCapacity(lim.capacity))
	dir := fakeProject(t, "question_model: \"cheap\"\nquestion_budget_usd: 0.5\n")
	wishID, _ := e.wish(t, dir)
	busy := e.spawn(t, wishID, "wait") // It takes the one slot.
	q := e.asker(wishID)
	asked := q.ask(t, "Which store?")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_B, "Postgres, we need several writers")

	if tasks := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(tasks) != 0 {
		t.Fatalf("%d investigators, want none", len(tasks))
	}
	all := e.list(t, wishID)
	if len(all) != 1 || all[0].GetId() != busy.GetId() {
		t.Errorf("wish tasks: %v, want only busy task", all)
	}
	if e.get(t, busy.GetId()).GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.Error("the busy task should still run")
	}
}

// TestInvestigator: "Enlighten me" starts one investigator with the developer's note, verbatim; a revision it makes is
// attributed to it, and the lead hears it.
func TestInvestigator(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithEnlightenWorkers())
	lead := &told{}
	e.h.TellLeads(lead.tell)
	wishID, _ := e.wish(t, fakeProject(t, ""))
	q := e.asker(wishID)
	asked := q.ask(t, "wait") // It works until the test lets it go.
	// What the developer typed in the card's note, as they typed it.
	note := "what does each cost to run?\nAnd the \"smoke\" of the wick — and why?"
	q.enlighten(t, asked.GetCode(), note)
	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("%d investigators, want 1", len(investigators))
	}
	inv := investigators[0]
	if inv.GetTitle() != "Q01: enlighten" || inv.GetQuestion() != "Q01" {
		t.Errorf("investigator: %v", inv)
	}
	// As the investigator does from its command line, with $DJINN_TASK_ID.
	if _, err := q.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}, WishId: wishID,
		Context: "SQLite costs nothing to run.", TaskId: inv.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	e.release(t, inv.GetId())
	e.ended(t, inv.GetId())
	prompt := e.prompt(t, inv.GetId())
	for _, want := range []string{"What the developer wants to know:\n\n" + note + "\n\n", "djinn question revise Q01 --wish-id " + wishID, "Recommended: A: one file"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt misses %q:\n%s", want, prompt)
		}
	}
	if got, want := lead.all(), []string{wishID + ": Djinn: W1 (Q01: enlighten) ended: revised Q01: it waits for " +
		"the developer again."}; !slices.Equal(got, want) {
		t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
	}
}

// TestQuestionWorkersDefaultsAndOverrides verifies:
// - Defaults: an answer starts no worker; Enlighten me starts an investigator.
// - Settings overrides: enlighten_workers: false turns investigator off.
// - Harness overrides: WithEnlightenWorkers(false) stops investigator.
func TestQuestionWorkersDefaultsAndOverrides(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		q := e.asker(wishID)

		// Defaults: answer starts no worker.
		q1 := q.ask(t, "")
		q.answer(t, q1.GetCode(), planv1.Choice_CHOICE_A, "")
		if tasks := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(tasks) != 0 {
			t.Fatalf("defaults: %d investigators, want none", len(tasks))
		}
		if all := e.list(t, wishID); len(all) != 0 {
			t.Fatalf("defaults: %d tasks, want none", len(all))
		}

		// Defaults: enlighten starts an investigator.
		q2 := q.ask(t, "")
		q.enlighten(t, q2.GetCode(), "tell me more")
		invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invs) != 1 {
			t.Fatalf("defaults: %d investigators, want 1", len(invs))
		}
		if invs[0].GetQuestion() != "Q02" || invs[0].GetTitle() != "Q02: enlighten" {
			t.Errorf("defaults: investigator: %v", invs[0])
		}
		e.ended(t, invs[0].GetId())
	})

	t.Run("override_enlighten_workers_settings", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir())
		wishID, _ := e.wish(t, fakeProject(t, "enlighten_workers: false\n"))
		q := e.asker(wishID)
		q1 := q.ask(t, "")
		q.enlighten(t, q1.GetCode(), "tell me more")
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Fatalf("%d investigators, want none", len(invs))
		}
	})

	t.Run("override_enlighten_workers_harness", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithEnlightenWorkers(false))
		wishID, _ := e.wish(t, fakeProject(t, ""))
		q := e.asker(wishID)
		q1 := q.ask(t, "")
		q.enlighten(t, q1.GetCode(), "tell me more")
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Fatalf("%d investigators, want none", len(invs))
		}
	})
}

// TestQuestionWorkersOff: a project's settings or a harness with enlighten workers off start none: the lead acts.
func TestQuestionWorkersOff(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		opts     []Option
		settings string
	}{
		"settings": {opts: []Option{WithEnlightenWorkers()}, settings: "enlighten_workers: false\n"},
		"harness":  {opts: []Option{WithEnlightenWorkers(false)}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := up(t, t.TempDir(), c.opts...)
			wishID, _ := e.wish(t, fakeProject(t, c.settings))
			q := e.asker(wishID)
			asked := q.ask(t, "")
			q.enlighten(t, asked.GetCode(), "")
			q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_A, "")
			res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
			if err != nil {
				t.Fatal(err)
			}
			if n := len(res.Msg.GetTasks()); n != 0 {
				t.Errorf("%d tasks, want none", n)
			}
		})
	}
}

// TestQuestionEndLine: what the lead hears when an investigator ends: revised or still waiting for a revision.
func TestQuestionEndLine(t *testing.T) {
	t.Parallel()
	at := timestamppb.New(time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC))
	inv := &planv1.Task{Id: "i", Code: "W9", WishId: "w", Title: "Q02: enlighten", Question: "Q02",
		Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR, Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: at}
	questions := []*planv1.Question{{Code: "Q04", TaskId: "i"}, {Code: "Q02"}}
	if got, want := questionEndLine(inv, nil, questions, ""), "Djinn: W9 (Q02: enlighten) ended: asked Q04. Q02 still waits for a "+
		"revision: djinn task get i has what it found."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	questionsRevised := []*planv1.Question{
		{Code: "Q02", Rounds: []*planv1.Round{{Kind: planv1.RoundKind_ROUND_KIND_REVISE, TaskId: "i"}}},
	}
	if got, want := questionEndLine(inv, nil, questionsRevised, ""), "Djinn: W9 (Q02: enlighten) ended: revised Q02: it waits for the developer again."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	refused := &planv1.Task{Id: "r", Code: "W10", WishId: "w", Title: "Q02: enlighten", Question: "Q02",
		Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR, Status: planv1.TaskStatus_TASK_STATUS_FAILED, CreateTime: at,
		Error: refusedError(`Bash {"command":"djinn task list | grep Q02;\n sed -n 1p x.go"}`)}
	if got, want := questionEndLine(refused, nil, nil, "W11"), "Djinn: W10 (Q02: enlighten) failed: its command was refused: "+
		"djinn task list | grep Q02; sed -n 1p x.go. Djinn starts it again: W11, told to run each djinn command alone; you "+
		"will hear when it ends."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := questionEndLine(refused, nil, nil, ""), "Djinn: W10 (Q02: enlighten) failed: its command was refused: "+
		"djinn task list | grep Q02; sed -n 1p x.go. Q02 still waits for a revision: djinn task get r has what it found."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := refusedError("Write notes.md"); got != "its command was refused: Write notes.md" {
		t.Errorf("refusedError = %q", got)
	}
}

// TestQuestionWorkerProvider: when the project's provider cannot run read-only (antigravity), question workers
// fall back to claude with sonnet, and the start event says why ("antigravity cannot run read-only: claude");
// an explicit question_provider names the provider they run on instead.
func TestQuestionWorkerProvider(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = Fake{}
	e := upWith(t, t.TempDir(), providers, WithEnlightenWorkers())

	// Fallback to Claude when the project's provider is Antigravity.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plan.SettingsFile), []byte("provider: PROVIDER_ANTIGRAVITY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wishID, _ := e.wish(t, dir)
	q := e.asker(wishID)
	asked := q.ask(t, "")
	q.enlighten(t, asked.GetCode(), "tell me more")

	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 {
		t.Fatalf("%d investigators, want 1", len(investigators))
	}
	c := investigators[0]
	if c.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || c.GetModel() != plan.DefaultQuestionModel {
		t.Errorf("investigator provider = %s, model = %q; want claude, %s", c.GetProvider(), c.GetModel(), plan.DefaultQuestionModel)
	}
	e.ended(t, c.GetId())
	events := e.watch(t.Context(), t, c.GetId(), 0)
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), "started claude (antigravity cannot run read-only: claude)")
	}) {
		t.Errorf("no start event with fallback text: %v", events)
	}

	// question_provider explicitly set: runs on that provider without fallback.
	dirExplicit := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirExplicit, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirExplicit, plan.SettingsFile), []byte("provider: PROVIDER_ANTIGRAVITY\nquestion_provider: PROVIDER_FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wishIDExplicit, _ := e.wish(t, dirExplicit)
	qExplicit := e.asker(wishIDExplicit)
	askedExplicit := qExplicit.ask(t, "")
	qExplicit.enlighten(t, askedExplicit.GetCode(), "tell me more")

	investigatorsExplicit := e.roles(t, wishIDExplicit, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigatorsExplicit) != 1 {
		t.Fatalf("%d investigators, want 1", len(investigatorsExplicit))
	}
	ce := investigatorsExplicit[0]
	if ce.GetProvider() != planv1.Provider_PROVIDER_FAKE {
		t.Errorf("investigator provider = %s; want fake", ce.GetProvider())
	}
	e.ended(t, ce.GetId())
	eventsExplicit := e.watch(t.Context(), t, ce.GetId(), 0)
	if !slices.ContainsFunc(eventsExplicit, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), "started fake") && !strings.Contains(ev.GetText(), "cannot run read-only")
	}) {
		t.Errorf("start event for explicit provider: %v", eventsExplicit)
	}
}
