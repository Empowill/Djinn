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
	"github.com/empowill/djinn/internal/store"
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

// TestInvestigatorPrompt verifies that investigatorPrompt injects Tour 0, the full chronological sequence
// of rounds (Enlighten notes and Revisions), --before, and linked/blocked tasks, while anchoring the worker to the
// original subject from Tour 0.
func TestInvestigatorPrompt(t *testing.T) {
	t.Parallel()
	q := &planv1.Question{
		Id:             "q-id-1",
		Code:           "Q45",
		WishId:         "w-id-1",
		TaskId:         "t-asker",
		Text:           "How should investigator workers inspect git and history?",
		Options:        []string{"Expand djinnOnly + history", "Keep minimal"},
		Context:        "Tour 2 revised context.\n\n" + plan.HistoryHeader + "\n\nold history",
		Recommendation: "A: full history and git read",
		Before:         "W12, W13",
		Revision:       2,
		Rounds: []*planv1.Round{
			{
				Kind:           planv1.RoundKind_ROUND_KIND_ENLIGHTEN,
				Note:           "First enlighten note: check permissions.",
				Options:        []string{"Option A0", "Option B0"},
				Context:        "Tour 0 initial context before any revision.",
				Recommendation: "A: initial rec",
			},
			{
				Kind:           planv1.RoundKind_ROUND_KIND_REVISE,
				TaskId:         "t-inv-1",
				Options:        []string{"Option A0", "Option B0"},
				Context:        "Tour 0 initial context before any revision.",
				Recommendation: "A: initial rec",
			},
			{
				Kind:           planv1.RoundKind_ROUND_KIND_ENLIGHTEN,
				Note:           "Second enlighten note: what about Tour 0 in prompt?",
				Options:        []string{"Option A1", "Option B1"},
				Context:        "Tour 1 context after first revision.",
				Recommendation: "B: first rev rec",
			},
			{
				Kind:           planv1.RoundKind_ROUND_KIND_REVISE,
				TaskId:         "t-inv-2",
				Options:        []string{"Option A1", "Option B1"},
				Context:        "Tour 1 context after first revision.",
				Recommendation: "B: first rev rec",
			},
			{
				Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN,
				Note: "Third enlighten note: verify blocked tasks too.",
			},
		},
	}
	tasks := []*planv1.Task{
		{Id: "t-asker", Code: "W10", Title: "Ask architecture question", Status: planv1.TaskStatus_TASK_STATUS_DONE},
		{Id: "t-wait", Code: "W12", Title: "Implement investigator fix", Status: planv1.TaskStatus_TASK_STATUS_WAITING, Decision: "Q45"},
		{Id: "t-edit", Code: "W13", Title: "Paused on edit", Status: planv1.TaskStatus_TASK_STATUS_PAUSED, EditQuestionId: "q-id-1"},
	}
	title, prompt := investigatorPrompt(q, "Third enlighten note: verify blocked tasks too.", "", "Moving brief.", "", tasks...)
	if title != "Q45: enlighten" {
		t.Errorf("title = %q, want Q45: enlighten", title)
	}
	for _, want := range []string{
		"Before (--before): W12, W13",
		"## Linked and blocked tasks",
		"- W10 (Ask architecture question) [done]: asked Q45",
		"- W12 (Implement investigator fix) [waiting]: waiting on decision Q45",
		"- W13 (Paused on edit) [paused]: waiting on edit question Q45",
		"## Question history (from Tour 0)",
		"### Tour 0 (initial question state)",
		"- Subject: Q45: How should investigator workers inspect git and history?",
		"- Options: A: Option A0 | B: Option B0",
		"- Recommendation: A: initial rec",
		"Tour 0 initial context before any revision.",
		"### Round 1 — Enlighten request (Tour 0)\n- Note: First enlighten note: check permissions.",
		"### Round 2 — Revision 1 (Tour 1) (task t-inv-1)",
		"Tour 1 context after first revision.",
		"### Round 3 — Enlighten request (Tour 1)\n- Note: Second enlighten note: what about Tour 0 in prompt?",
		"### Round 4 — Revision 2 (Tour 2) (task t-inv-2)",
		"Tour 2 revised context.",
		"### Round 5 — Enlighten request (Tour 2)\n- Note: Third enlighten note: verify blocked tasks too.",
		"Stay anchored to the original subject from Tour 0 (`Q45: How should investigator workers inspect git and history?`)",
		"`djinn question revise Q45 --wish-id w-id-1 --context \"…\" --recommendation \"…\"`",
		"git branch",
		"git rev-parse",
		"git ls-tree",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "old history") {
		t.Errorf("prompt did not strip auto-appended HistoryHeader from current context:\n%s", prompt)
	}
}

// projectless makes a wish of no project, as the developer's "Install Wispr Flow on Linux" was, and returns it.
func (e *env) projectless(t *testing.T) string {
	t.Helper()
	w, err := e.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Install a tool"}))
	if err != nil {
		t.Fatal(err)
	}
	return w.Msg.GetWish().GetId()
}

// enlightenLost stores a request to investigate code as djinn up did before it hung and was made to quit: the
// request alone, neither its investigator started nor the lead told.
func (e *env) enlightenLost(t *testing.T, wishID, code, note string) {
	t.Helper()
	q := &plan.Questions{Store: e.db}
	if _, err := q.Enlighten(t.Context(), connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: code}}, Note: note, WishId: wishID,
	})); err != nil {
		t.Fatal(err)
	}
}

// leadEvents are the lead's events about the wish, queued in the store as djinn up queues them.
func (e *env) leadEvents(t *testing.T, wishID string) []*planv1.LeadEvent {
	t.Helper()
	events, err := store.List[*planv1.LeadEvent](t.Context(), e.db, store.Where{"wish_id": wishID})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestEnlighten(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// An answer never starts a worker: the lead turns answers into tasks itself.
	t.Run("answer_starts_no_worker", func(t *testing.T) {
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
	})

	// "Enlighten me" starts one investigator with the developer's note, verbatim; a revision it makes is
	// attributed to it, and the lead hears it.
	t.Run("investigator", func(t *testing.T) {
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
		for _, want := range []string{
			"What the developer wants to know:\n\n" + note + "\n\n",
			"djinn question revise Q01 --wish-id " + wishID,
			"Recommended: A: one file",
			"## The shape of the context\n\n" + plan.QuestionShape,
			"WebSearch and WebFetch, or search_web and read_url_content",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("the prompt misses %q:\n%s", want, prompt)
			}
		}
		if got, want := lead.all(), []string{wishID + ": Djinn: W1 (Q01: enlighten) ended: revised Q01: it waits for " +
			"the developer again."}; !slices.Equal(got, want) {
			t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
		}
	})

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
		e := up(t, t.TempDir(), WithEnlightenWorkers())
		wishID, _ := e.wish(t, fakeProject(t, "enlighten_workers: false\n"))
		q := e.asker(wishID)
		q1 := q.ask(t, "")
		q.enlighten(t, q1.GetCode(), "tell me more")
		q.answer(t, q1.GetCode(), planv1.Choice_CHOICE_A, "")
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Fatalf("%d investigators, want none", len(invs))
		}
		if all := e.list(t, wishID); len(all) != 0 {
			t.Fatalf("%d tasks, want none", len(all))
		}
	})

	t.Run("override_enlighten_workers_harness", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithEnlightenWorkers(false))
		wishID, _ := e.wish(t, fakeProject(t, ""))
		q := e.asker(wishID)
		q1 := q.ask(t, "")
		q.enlighten(t, q1.GetCode(), "tell me more")
		q.answer(t, q1.GetCode(), planv1.Choice_CHOICE_A, "")
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Fatalf("%d investigators, want none", len(invs))
		}
		if all := e.list(t, wishID); len(all) != 0 {
			t.Fatalf("%d tasks, want none", len(all))
		}
	})

	// When the project's provider cannot run read-only (antigravity), question workers
	// fall back to claude with sonnet, and the start event says why ("antigravity cannot run read-only: claude");
	// an explicit question_provider names the provider they run on instead.
	t.Run("provider", func(t *testing.T) {
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

		// Strong model configured on project is inherited by investigator when question_model is not set.
		dirStrong := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dirStrong, ".agents"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dirStrong, plan.SettingsFile), []byte("provider: PROVIDER_FAKE\nmodel: \"strong-opus\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		wishIDStrong, _ := e.wish(t, dirStrong)
		qStrong := e.asker(wishIDStrong)
		askedStrong := qStrong.ask(t, "")
		qStrong.enlighten(t, askedStrong.GetCode(), "check with strong model")
		invsStrong := e.roles(t, wishIDStrong, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invsStrong) != 1 {
			t.Fatalf("%d investigators, want 1", len(invsStrong))
		}
		if invsStrong[0].GetProvider() != planv1.Provider_PROVIDER_FAKE || invsStrong[0].GetModel() != "strong-opus" {
			t.Errorf("investigator provider = %s, model = %q; want fake, strong-opus", invsStrong[0].GetProvider(), invsStrong[0].GetModel())
		}
		e.ended(t, invsStrong[0].GetId())
	})

	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = Fake{} // The default provider outside any project: no paid model.

	// Replays W536: a wish of no project gets its investigator outside any project, in an empty folder, reading.
	t.Run("without_project_live", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		e := upWith(t, home, providers)
		wishID := e.projectless(t)
		q := e.asker(wishID)
		asked := q.ask(t, "wait")
		q.enlighten(t, asked.GetCode(), "are my machine's specs enough first?")
		invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invs) != 1 {
			t.Fatalf("%d investigators, want 1", len(invs))
		}
		inv := invs[0]
		if inv.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || inv.GetProjectId() != "" ||
			inv.GetAccess() != planv1.TaskAccess_TASK_ACCESS_DJINN {
			t.Errorf("investigator: %v, want running in no project, with djinn's commands only", inv)
		}
		e.release(t, inv.GetId())
		e.ended(t, inv.GetId())
		if !strings.Contains(e.prompt(t, inv.GetId()), "are my machine's specs enough first?") {
			t.Error("the investigator was not given the developer's note")
		}
	})

	t.Run("without_project_lost_then_investigator", func(t *testing.T) {
		t.Parallel()
		e := upWith(t, t.TempDir(), providers)
		wishID := e.projectless(t)
		asked := e.asker(wishID).ask(t, "wait")
		e.enlightenLost(t, wishID, asked.GetCode(), "are my machine's specs enough first?")
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Fatalf("%d investigators before the pass, want none", len(invs))
		}
		e.h.resumeInvestigations(t.Context())
		invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invs) != 1 || invs[0].GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			t.Fatalf("investigators after the pass: %v, want one running", invs)
		}
		// Another pass while it works starts no second one, and tells it nothing again.
		e.h.resumeInvestigations(t.Context())
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)); n != 1 {
			t.Errorf("%d investigators after a second pass, want 1", n)
		}
		e.release(t, invs[0].GetId())
		e.ended(t, invs[0].GetId())
	})

	t.Run("without_project_lost_then_lead", func(t *testing.T) {
		t.Parallel()
		e := upWith(t, t.TempDir(), providers, WithEnlightenWorkers(false))
		e.h.NotifyLead(func(ctx context.Context, ev *planv1.LeadEvent) error {
			return plan.QueueLeadEvent(ctx, e.db, ev, time.Now())
		})
		wishID := e.projectless(t)
		asked := e.asker(wishID).ask(t, "")
		e.enlightenLost(t, wishID, asked.GetCode(), "are my machine's specs enough first?")
		e.h.resumeInvestigations(t.Context())
		e.h.resumeInvestigations(t.Context()) // Told once: the next start tells it no more.
		events := e.leadEvents(t, wishID)
		if len(events) != 1 || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN ||
			events[0].GetCode() != asked.GetCode() {
			t.Fatalf("lead events: %v, want one to investigate %s", events, asked.GetCode())
		}
		want := plan.EnlightenLine(asked, "are my machine's specs enough first?", "")
		if events[0].GetText() != want {
			t.Errorf("lead event %q, want %q", events[0].GetText(), want)
		}
		if n := len(e.list(t, wishID)); n != 0 {
			t.Errorf("%d tasks, want none: the lead investigates", n)
		}
	})

	queue := func(e *env) {
		e.h.NotifyLead(func(ctx context.Context, ev *planv1.LeadEvent) error {
			return plan.QueueLeadEvent(ctx, e.db, ev, time.Now())
		})
	}
	// lost stores a request to investigate a new question of a wish of a project, then stops djinn up before its
	// investigator starts.
	lost := func(t *testing.T, home string) (wishID string, asked *planv1.Question) {
		t.Helper()
		e := up(t, home, WithEnlightenWorkers())
		wishID, _ = e.wish(t, fakeProject(t, ""))
		asked = e.asker(wishID).ask(t, "wait") // Its investigator works until the test lets it go.
		e.enlightenLost(t, wishID, asked.GetCode(), "which are the impacts?")
		e.down()
		return wishID, asked
	}

	// Replays W557: a request stored before djinn up stopped gets an investigator at the next start.
	t.Run("survives_restart_investigator", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		wishID, asked := lost(t, home)
		e := up(t, home, WithEnlightenWorkers())
		queue(e)
		e.h.ResumeInvestigations() // As djinn up does once the lead's events go out.
		var invs []*planv1.Task
		waitFor(t, "an investigator", func() bool {
			invs = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
			return len(invs) > 0
		})
		inv := e.until(t, invs[0].GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		if inv.GetQuestion() != asked.GetCode() || inv.GetProjectId() == "" {
			t.Errorf("investigator: %v, want one on %s in its project", inv, asked.GetCode())
		}
		if events := e.leadEvents(t, wishID); slices.ContainsFunc(events, func(ev *planv1.LeadEvent) bool {
			return ev.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN
		}) {
			t.Errorf("lead events: %v, want none to investigate: the investigator does", events)
		}
		e.release(t, inv.GetId())
		e.ended(t, inv.GetId())
		if !strings.Contains(e.prompt(t, inv.GetId()), "which are the impacts?") {
			t.Error("the investigator was not given the developer's note")
		}
	})

	t.Run("survives_restart_lead", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		wishID, asked := lost(t, home)
		e := up(t, home, WithEnlightenWorkers(false))
		queue(e)
		e.h.resumeInvestigations(t.Context())
		e.h.resumeInvestigations(t.Context()) // Told once: another start tells it no more.
		events := e.leadEvents(t, wishID)
		if len(events) != 1 || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN ||
			events[0].GetCode() != asked.GetCode() {
			t.Fatalf("lead events: %v, want one to investigate %s", events, asked.GetCode())
		}
		if invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR); len(invs) != 0 {
			t.Errorf("%d investigators, want none: the lead investigates", len(invs))
		}
	})

	t.Run("survives_restart_start_hangs", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		wishID, asked := lost(t, home)
		e := up(t, home, WithEnlightenWorkers(), WithEnlightenWait(50*time.Millisecond))
		queue(e)
		e.h.questioning.Lock() // Held, as the start that hung in a deadlock held it.
		start := time.Now()
		e.h.resumeInvestigations(t.Context())
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("the pass took %s behind a start that hangs", took)
		}
		events := e.leadEvents(t, wishID)
		if len(events) != 1 || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN ||
			events[0].GetCode() != asked.GetCode() {
			t.Errorf("lead events: %v, want one to investigate %s", events, asked.GetCode())
		}
		// The start goes on once what held it lets go: the investigator works beside the lead.
		e.h.questioning.Unlock()
		var invs []*planv1.Task
		waitFor(t, "the late investigator", func() bool {
			invs = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
			return len(invs) > 0
		})
		e.release(t, invs[0].GetId())
		e.ended(t, invs[0].GetId())
	})

	// Replays another project (16/10/2026): an investigator, W19, ended on refused djinn commands and did nothing;
	// Djinn started W20 in its place, which closes the failed one, continued in it; the retry is not started a third time;
	// it leaves the lead's failed list once another one revised its question, and closes when the question is answered.
	t.Run("failed_investigator_retry", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithEnlightenWorkers())
		lead := &told{}
		e.h.TellLeads(lead.tell)
		wishID, _ := e.wish(t, fakeProject(t, ""))
		q := e.asker(wishID)
		asked := q.ask(t, "refuse Bash djinn task list --wish-id w | grep Q17")
		q.enlighten(t, asked.GetCode(), "")
		first := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)[0]
		e.ended(t, first.GetId())
		var invs []*planv1.Task
		waitFor(t, "the investigator started again", func() bool {
			invs = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
			return len(invs) == 2
		})
		again := invs[1]
		e.ended(t, again.GetId())
		w1 := e.until(t, first.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
		if c := w1.GetClosed(); c.GetActor() != planv1.Closer_CLOSER_LEAD || c.GetContinuedIn() != again.GetCode() || w1.GetError() != "" {
			t.Errorf("the failed investigator: %v, closure %v: want done, continued in %s", w1.GetStatus(), c, again.GetCode())
		}
		failed := func() []string {
			res, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID, Failed: true}))
			if err != nil {
				t.Fatal(err)
			}
			var codes []string
			for _, task := range res.Msg.GetTasks() {
				codes = append(codes, task.GetCode())
			}
			return codes
		}
		// The retry failed too: it is the lead's to take up, and Djinn starts no third one.
		waitFor(t, "the lead told of the retry's end", func() bool { return len(lead.all()) == 2 })
		if got := e.get(t, again.GetId()); got.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
			t.Fatalf("the retry: %v, want failed", got.GetStatus())
		}
		if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)); n != 2 {
			t.Errorf("%d investigators, want 2: no third start", n)
		}
		if got := failed(); !slices.Equal(got, []string{again.GetCode()}) {
			t.Errorf("failed list %q, want only %s", got, again.GetCode())
		}
		// Another one revises the question: the failed retry has nothing left to find.
		if _, err := q.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: asked.GetCode()}}, WishId: wishID,
			Context: "SQLite costs nothing to run.",
		})); err != nil {
			t.Fatal(err)
		}
		if got := failed(); len(got) != 0 {
			t.Errorf("failed list %q after the revision, want none", got)
		}
		q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_A, "")
		w2 := e.until(t, again.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_DONE))
		if c := w2.GetClosed(); c.GetActor() != planv1.Closer_CLOSER_LEAD || c.GetContinuedIn() != "" || c.GetNote() == "" {
			t.Errorf("the failed retry's closure: %v", c)
		}
	})

	// An investigator that finishes done with refused calls (W14, W15 in Rocket Time, L49) is closed once its
	// question is answered or withdrawn, so its refused calls no longer hold back the wish's grant.
	t.Run("refused_done_closed_on_answer_and_withdraw", func(t *testing.T) {
		t.Parallel()
		e := up(t, t.TempDir(), WithEnlightenWorkers())
		wishID, _ := e.wish(t, fakeProject(t, ""))
		q := e.asker(wishID)

		q1 := q.ask(t, "wait")
		q.enlighten(t, q1.GetCode(), "check grep")
		invs := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invs) != 1 {
			t.Fatalf("%d investigators, want 1", len(invs))
		}
		w1 := invs[0]
		e.release(t, w1.GetId())
		e.ended(t, w1.GetId())

		q2 := q.ask(t, "wait")
		q.enlighten(t, q2.GetCode(), "check grep again")
		invs = e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
		if len(invs) != 2 {
			t.Fatalf("%d investigators, want 2", len(invs))
		}
		w2 := invs[1]
		e.release(t, w2.GetId())
		e.ended(t, w2.GetId())

		for _, id := range []string{w1.GetId(), w2.GetId()} {
			if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
				task, err := store.Get[*planv1.Task](t.Context(), tx, id)
				if err != nil {
					return err
				}
				task.Refused = &planv1.TaskRefusals{Count: 1, Names: []string{"Bash"}}
				if err := tx.Journal(actorHarness, "test", task); err != nil {
					return err
				}
				return tx.Put(task)
			}); err != nil {
				t.Fatal(err)
			}
		}

		q.answer(t, q1.GetCode(), planv1.Choice_CHOICE_A, "")
		waitFor(t, "answered question closes its held investigator", func() bool {
			return e.get(t, w1.GetId()).GetClosed() != nil
		})
		if e.get(t, w2.GetId()).GetClosed() != nil {
			t.Fatal("open question's investigator should stay unclosed until its question moves on")
		}

		if _, err := q.Withdraw(t.Context(), connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q2.GetCode()}},
			WishId:   wishID,
			Note:     "settled",
		})); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "withdrawn question closes its held investigator", func() bool {
			return e.get(t, w2.GetId()).GetClosed() != nil
		})
	})
}

// TestOutrun: a failed investigator, or one done with refused calls, is outrun once its question is answered,
// withdrawn, or revised after it started by another than itself; a revision of its own, an earlier one, another
// question or another role leave it to the lead.
func TestOutrun(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 16, 13, 9, 0, 0, time.UTC)
	before, after := timestamppb.New(at.Add(-time.Minute)), timestamppb.New(at.Add(time.Minute))
	inv := &planv1.Task{Id: "w19", Code: "W19", WishId: "w", Question: "Q17", Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR,
		Status: planv1.TaskStatus_TASK_STATUS_FAILED, CreateTime: timestamppb.New(at)}
	held := &planv1.Task{Id: "w14", Code: "W14", WishId: "w", Question: "Q45", Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR,
		Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.New(at),
		Refused: &planv1.TaskRefusals{Count: 1, Names: []string{"Bash"}}}
	revise := func(task string, when *timestamppb.Timestamp) *planv1.Round {
		return &planv1.Round{Kind: planv1.RoundKind_ROUND_KIND_REVISE, TaskId: task, CreateTime: when}
	}
	for name, c := range map[string]struct {
		task *planv1.Task
		q    *planv1.Question
		want bool
	}{
		"open":            {inv, &planv1.Question{Code: "Q17", WishId: "w"}, false},
		"answered":        {inv, &planv1.Question{Code: "Q17", WishId: "w", Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_A}}, true},
		"withdrawn":       {inv, &planv1.Question{Code: "Q17", WishId: "w", Withdrawal: &planv1.Withdrawal{}}, true},
		"revised by W20":  {inv, &planv1.Question{Code: "Q17", WishId: "w", Rounds: []*planv1.Round{revise("w20", after)}}, true},
		"revised by lead": {inv, &planv1.Question{Code: "Q17", WishId: "w", Rounds: []*planv1.Round{revise("", after)}}, true},
		"revised by it":   {inv, &planv1.Question{Code: "Q17", WishId: "w", Rounds: []*planv1.Round{revise("w19", after)}}, false},
		"revised before":  {inv, &planv1.Question{Code: "Q17", WishId: "w", Rounds: []*planv1.Round{revise("w18", before)}}, false},
		"other question":  {inv, &planv1.Question{Code: "Q18", WishId: "w", Answer: &planv1.Answer{}}, false},
		"other wish":      {inv, &planv1.Question{Code: "Q17", WishId: "v", Answer: &planv1.Answer{}}, false},
		"held open":       {held, &planv1.Question{Code: "Q45", WishId: "w", Rounds: []*planv1.Round{revise("w14", after)}}, false},
		"held answered":   {held, &planv1.Question{Code: "Q45", WishId: "w", Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_A}}, true},
		"held withdrawn":  {held, &planv1.Question{Code: "Q45", WishId: "w", Withdrawal: &planv1.Withdrawal{}}, true},
		"clean done": {&planv1.Task{Id: "w15", Code: "W15", WishId: "w", Question: "Q45", Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR,
			Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.New(at)},
			&planv1.Question{Code: "Q45", WishId: "w", Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_A}}, false},
		"a worker": {&planv1.Task{Code: "W3", WishId: "w", Question: "Q17", Status: planv1.TaskStatus_TASK_STATUS_FAILED},
			&planv1.Question{Code: "Q17", WishId: "w", Answer: &planv1.Answer{}}, false},
	} {
		if got := outrun(c.task, []*planv1.Question{c.q}); got != c.want {
			t.Errorf("%s: outrun = %v, want %v", name, got, c.want)
		}
	}
}
