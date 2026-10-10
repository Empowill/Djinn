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

// TestConverter: an answer starts exactly one converter, with the decision in its prompt; it reads and runs djinn's
// commands, in the project's folder, takes no slot of the machine even when they are all taken, and the lead hears
// when it ends.
func TestConverter(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	lim := &limit{slots: 1}
	e := up(t, t.TempDir(), WithQuestionWorkers(), WithCapacity(lim.capacity))
	lead := &told{}
	e.h.TellLeads(lead.tell)
	dir := fakeProject(t, "question_model: \"cheap\"\nquestion_budget_usd: 0.5\n")
	wishID, _ := e.wish(t, dir)
	busy := e.spawn(t, wishID, "wait") // It takes the one slot.
	q := e.asker(wishID)
	asked := q.ask(t, "usage 9000 400 0.031")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_B, "Postgres, we need several writers")

	converters := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if len(converters) != 1 {
		t.Fatalf("%d converters, want 1", len(converters))
	}
	c := converters[0]
	if c.GetQuestion() != "Q01" || c.GetTitle() != "Q01 → tasks" || c.GetAccess() != planv1.TaskAccess_TASK_ACCESS_DJINN ||
		c.GetWorktree() != "" || c.GetModel() != "cheap" || c.GetMaxBudgetUsd() != 0.5 || c.GetProvider() != planv1.Provider_PROVIDER_FAKE {
		t.Errorf("converter: %v", c)
	}
	if c.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING && c.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("converter %s, %s: it should not wait for the slot the busy task holds", c.GetStatus(), c.GetWaitReason())
	}
	prompt := e.prompt(t, c.GetId())
	for _, want := range []string{
		"Q01: Which store?", "- B: Postgres", "## The answer\n\nB: Postgres", "Postgres, we need several writers",
		"--decision Q01", "djinn task spawn " + wishID, "djinn question ask", "## Where the wish stands", "Run Djinn on itself",
		"First look for the azima each task belongs to", "only for a will no existing one carries",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt misses %q:\n%s", want, prompt)
		}
	}
	t.Logf("a converter's prompt: %d bytes, about %d tokens", len(prompt), len(prompt)/4)

	done := e.ended(t, c.GetId())
	if done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE || done.GetUsage().GetCostUsd() != 0.031 {
		t.Errorf("converter ended %s, cost $%v", done.GetStatus(), done.GetUsage().GetCostUsd())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	events := e.watch(ctx, t, c.GetId(), 0)
	if i := slices.IndexFunc(events, func(ev *planv1.TaskEvent) bool { return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL }); i >= 0 {
		t.Errorf("the fake ran a line of Djinn's own prompt: %s", events[i].GetText())
	}
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return strings.Contains(ev.GetText(), "turns the decision Q01 into tasks; reading, and running only djinn's commands")
	}) {
		t.Errorf("no start event says what it is: %v", events)
	}
	if got, want := lead.all(), []string{wishID + ": Djinn: " + c.GetCode() + " (Q01 → tasks) ended. Nothing came of Q01: act on it, " +
		"djinn wish brief " + wishID + " has the context."}; !slices.Equal(got, want) {
		t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
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
	e := up(t, t.TempDir(), WithQuestionWorkers())
	lead := &told{}
	e.h.TellLeads(lead.tell)
	wishID, _ := e.wish(t, fakeProject(t, ""))
	q := e.asker(wishID)
	asked := q.ask(t, "wait") // It works until the test lets it go.
	// What the developer typed in the card's note, as they typed it.
	note := "what does each cost to run?\nAnd the \"smoke\" of the wick — and why?"
	q.enlighten(t, asked.GetCode(), note)
	investigators := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)
	if len(investigators) != 1 || len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)) != 0 {
		t.Fatalf("%d investigators, want 1 and no converter", len(investigators))
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
	if got, want := lead.all(), []string{wishID + ": Djinn: " + inv.GetCode() + " (Q01: enlighten) ended: revised Q01: it waits for " +
		"the developer again."}; !slices.Equal(got, want) {
		t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
	}
}

// TestQuestionWorkersOff: a project's settings, or a harness without question workers, start none: the lead acts.
func TestQuestionWorkersOff(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		opts     []Option
		settings string
	}{
		"settings": {opts: []Option{WithQuestionWorkers()}, settings: "question_workers: false\n"},
		"harness":  {},
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

// TestOneConverterAtATime: a second answer to a question whose converter still works starts no other: the converter
// is told the new answer.
func TestOneConverterAtATime(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir(), WithQuestionWorkers())
	wishID, _ := e.wish(t, fakeProject(t, ""))
	q := e.asker(wishID)
	asked := q.ask(t, "wait") // The converter works until a message comes: the second answer.
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_A, "")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_B, "on second thought")
	converters := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if len(converters) != 1 {
		t.Fatalf("%d converters, want 1", len(converters))
	}
	e.ended(t, converters[0].GetId())
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	events := e.watch(ctx, t, converters[0].GetId(), 0)
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE &&
			strings.Contains(ev.GetText(), "The developer answered Q01 again: B: Postgres. Their note: on second thought")
	}) {
		t.Errorf("the converter was not told the new answer: %v", eventKinds(events))
	}
	// Once it ended, a new answer starts a new converter.
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_A, "")
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)); n != 2 {
		t.Errorf("%d converters after it ended, want 2", n)
	}
}

// TestQuestionEndLine: what the lead hears when a question worker ends: what it spawned from its question, asked or
// revised, and what is left to the lead.
func TestQuestionEndLine(t *testing.T) {
	t.Parallel()
	at := timestamppb.New(time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC))
	later := timestamppb.New(at.AsTime().Add(time.Minute))
	before := timestamppb.New(at.AsTime().Add(-time.Minute))
	conv := &planv1.Task{Id: "c", Code: "W5", WishId: "w", Title: "Q02 → tasks", Question: "Q02",
		Role: planv1.TaskRole_TASK_ROLE_CONVERTER, Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: at}
	tasks := []*planv1.Task{
		{Code: "W1", Decision: "Q02", CreateTime: before}, // spawned by the lead, before
		{Code: "W6", Decision: "Q02", CreateTime: later},
		{Code: "W7", Decision: "q02", CreateTime: later},
		{Code: "W8", Decision: "Q03", CreateTime: later},
	}
	questions := []*planv1.Question{{Code: "Q04", TaskId: "c"}, {Code: "Q02"}}
	if got, want := questionEndLine(conv, tasks, questions, ""), "Djinn: W5 (Q02 → tasks) ended: spawned W6, W7 from Q02; asked Q04."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	conv.Status, conv.Error = planv1.TaskStatus_TASK_STATUS_FAILED, "exit code 1"
	if got, want := questionEndLine(conv, tasks[:1], nil, ""), "Djinn: W5 (Q02 → tasks) failed (exit code 1). Nothing came of "+
		"Q02: act on it, djinn wish brief w has the context."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := questionEndLine(conv, tasks, nil, ""), "Djinn: W5 (Q02 → tasks) failed (exit code 1): spawned W6, W7 from "+
		"Q02. Check what is left of Q02: djinn wish brief w has the context."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	inv := &planv1.Task{Id: "i", Code: "W9", WishId: "w", Title: "Q02: enlighten", Question: "Q02",
		Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR, Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: at}
	if got, want := questionEndLine(inv, nil, questions, ""), "Djinn: W9 (Q02: enlighten) ended. Q02 still waits for a "+
		"revision: djinn task get i has what it found."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	refused := &planv1.Task{Id: "r", Code: "W10", WishId: "w", Title: "Q02 → tasks", Question: "Q02",
		Role: planv1.TaskRole_TASK_ROLE_CONVERTER, Status: planv1.TaskStatus_TASK_STATUS_FAILED, CreateTime: at,
		Error: refusedError(`Bash {"command":"djinn task list | grep Q02;\n sed -n 1p x.go"}`)}
	if got, want := questionEndLine(refused, nil, nil, "W11"), "Djinn: W10 (Q02 → tasks) failed: its command was refused: "+
		"djinn task list | grep Q02; sed -n 1p x.go. Djinn starts it again: W11, told to run each djinn command alone; you "+
		"will hear when it ends."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := questionEndLine(refused, nil, nil, ""), "Djinn: W10 (Q02 → tasks) failed: its command was refused: "+
		"djinn task list | grep Q02; sed -n 1p x.go. Nothing came of Q02: act on it, djinn wish brief w has the context."; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := refusedError("Write notes.md"); got != "its command was refused: Write notes.md" {
		t.Errorf("refusedError = %q", got)
	}
}

// claudeQuestions is djinn up with question workers, played by the test binary replaying a Claude fixture, in a
// project whose settings give them to Claude; the lead's lines are told.
func claudeQuestions(t *testing.T, fixture string) (e *env, lead *told, wishID string) {
	t.Helper()
	fakeEnv, _, _ := fake{provider: "claude", fixture: fixture, end: "eof"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envClaude{Claude: Claude{Command: os.Args[0], Grace: time.Second}, env: fakeEnv, dir: t.TempDir()}
	e = upWith(t, t.TempDir(), providers, WithQuestionWorkers())
	lead = &told{}
	e.h.TellLeads(lead.tell)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plan.SettingsFile), []byte("provider: PROVIDER_CLAUDE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wishID, _ = e.wish(t, dir)
	return e, lead, wishID
}

// refusedCompound is the command the question workers of the Claude fixtures join grep and sed to.
const refusedCompound = "djinn task list --wish-id 01a11833-a440-7479-a067-52615c91da70 | grep Q01; sed -n 1,40p internal/store/store.go"

// TestConverterRefusedThenSpawns: a converter whose compound command is refused runs the djinn command alone, and the
// task it spawns counts: it ends done, and the lead hears what it spawned. Its prompt says how to work within its
// access.
func TestConverterRefusedThenSpawns(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, lead, wishID := claudeQuestions(t, "question-refused-then-spawned")
	q := e.asker(wishID)
	asked := q.ask(t, "")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_B, "")
	converters := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if len(converters) != 1 {
		t.Fatalf("%d converters, want 1", len(converters))
	}
	c := converters[0]
	// As its plain djinn command does, once the compound one was refused; the fake waits for it.
	if _, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Move the store to Postgres", Prompt: "done", Provider: planv1.Provider_PROVIDER_FAKE, Decision: "Q01",
	})); err != nil {
		t.Fatal(err)
	}
	e.release(t, c.GetId())
	if done := e.ended(t, c.GetId()); done.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("converter ended %s (%s), want done", done.GetStatus(), done.GetError())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	events := e.watch(ctx, t, c.GetId(), 0)
	if !slices.ContainsFunc(events, func(ev *planv1.TaskEvent) bool {
		return ev.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_STATUS && strings.Contains(ev.GetText(), "permission denied: Bash") &&
			strings.Contains(ev.GetText(), "| grep Q01")
	}) {
		t.Errorf("no refusal recorded: %v", eventKinds(events))
	}
	prompt := events[0].GetText()
	for _, want := range []string{"## How to work within your access", "in a Bash call of its own: no pipe", "Read, Grep and Glob tools",
		"not that Bash is"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt misses %q", want)
		}
	}
	if got, want := lead.all(), []string{wishID + ": Djinn: W1 (Q01 → tasks) ended: spawned W2 from Q01."}; !slices.Equal(got, want) {
		t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
	}
	if n := len(e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)); n != 1 {
		t.Errorf("%d converters, want 1: nothing to start again", n)
	}
}

// TestQuestionWorkerGivesUp: a converter that ends without doing anything after a refusal fails, the refusal named;
// Djinn starts it again once, told what was refused, and the lead hears both; the second one giving up the same way
// is not started again.
func TestQuestionWorkerGivesUp(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e, lead, wishID := claudeQuestions(t, "question-gives-up")
	q := e.asker(wishID)
	asked := q.ask(t, "")
	q.answer(t, asked.GetCode(), planv1.Choice_CHOICE_B, "")
	waitFor(t, "the lead to hear of both converters", func() bool { return len(lead.all()) == 2 })
	converters := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_CONVERTER)
	if len(converters) != 2 {
		t.Fatalf("%d converters, want 2: one, then its retry", len(converters))
	}
	for _, c := range converters {
		c = e.ended(t, c.GetId())
		if c.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED || c.GetError() != refusedPrefix+refusedCompound {
			t.Errorf("%s ended %s (%s), want failed, the refusal named", c.GetCode(), c.GetStatus(), c.GetError())
		}
	}
	if prompt := e.prompt(t, converters[1].GetId()); !strings.Contains(prompt, "## Before you\n\nW1 had this job before you, "+
		"and ended without doing anything after this call was refused: "+refusedCompound+". Only that form was refused") {
		t.Errorf("the retry is not told what was refused:\n%s", prompt)
	}
	refused := "failed: " + refusedPrefix + refusedCompound + "."
	want := []string{
		wishID + ": Djinn: W1 (Q01 → tasks) " + refused + " Djinn starts it again: W2, told to run each djinn command alone; " +
			"you will hear when it ends.",
		wishID + ": Djinn: W2 (Q01 → tasks) " + refused + " Nothing came of Q01: act on it, djinn wish brief " + wishID +
			" has the context.",
	}
	got := lead.all()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the lead was told\n%q\nwant\n%q", got, want)
	}
}
