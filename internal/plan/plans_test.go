package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

func TestParsePlan(t *testing.T) {
	p, err := ParsePlan("\r\nOption: b\r\ntitle: Port the store\r\nprovider: codex\r\nmodel: gpt-5\r\npart_of: T07\r\n" +
		"after: W1, W2\r\nafter: W3\r\nblocks: W5\r\ntilasm: L01\r\nkind: ifrit\r\nproject-id: p1\r\n\r\nPort it.\r\n\r\nThen test it.\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if p.GetOption() != planv1.Choice_CHOICE_B || p.GetTitle() != "Port the store" || p.GetProvider() != planv1.Provider_PROVIDER_CODEX ||
		p.GetModel() != "gpt-5" || p.GetPartOf() != "T07" || strings.Join(p.GetAfter(), " ") != "W1 W2 W3" ||
		strings.Join(p.GetBlocks(), " ") != "W5" || strings.Join(p.GetTilasm(), " ") != "L01" ||
		p.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT || p.GetProjectId() != "p1" || p.GetPrompt() != "Port it.\n\nThen test it." {
		t.Errorf("parsed %v", p)
	}
	if p, err := ParsePlan("title: Port\nprompt: in a line"); err != nil || p.GetPrompt() != "in a line" ||
		p.GetOption() != planv1.Choice_CHOICE_UNSPECIFIED {
		t.Errorf("a prompt in the header: %v, %v", p, err)
	}
	for text, want := range map[string]string{
		"colour: red":                "plan key \"colour\" is none of option, title, prompt",
		"option: E":                  "plan option \"E\" is none of A, B, C, D, yes",
		"provider: gemini":           "plan provider \"gemini\" is none of antigravity, claude, codex, fake, watch",
		"title: Port\nno colon here": "plan line \"no colon here\" is not \"key: value\"",
		"prompt: one\n\ntwo":         "the plan gives its prompt twice",
	} {
		if _, err := ParsePlan(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
	}
}

// putTasks stores tasks as Djinn would have made them.
func putTasks(t *testing.T, s *store.Store, tasks ...*planv1.Task) {
	t.Helper()
	if err := s.Tx(t.Context(), func(tx *store.Tx) error {
		for _, task := range tasks {
			if err := tx.Journal(actor, "test/put", task); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func testTask(wishID, code, title string, status planv1.TaskStatus) *planv1.Task {
	return &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: title, Status: status, CreateTime: timestamppb.Now()}
}

func (c clients) askPlans(t *testing.T, req *planv1.QuestionServiceAskRequest) (*planv1.Question, error) {
	t.Helper()
	if req.Text == "" {
		req.Text = "Which store?"
	}
	res, err := c.questions.Ask(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetQuestion(), nil
}

// TestAskPlans: a question's options carry their plans, read from a file or given inline, the tasks they name kept by
// identifier with how they stood; a plan that cannot launch is refused when asked.
func TestAskPlans(t *testing.T) {
	c := serve(t)
	wishID := c.wish(t)
	before := testTask(wishID, "W1", "Buy oil", planv1.TaskStatus_TASK_STATUS_RUNNING)
	planned := testTask(wishID, "W2", "Trim the wick", planv1.TaskStatus_TASK_STATUS_PENDING)
	done := testTask(wishID, "W3", "Clean the lamp", planv1.TaskStatus_TASK_STATUS_DONE)
	azima := testTask(wishID, "T1", "Lamps", planv1.TaskStatus_TASK_STATUS_PENDING)
	azima.Kind = planv1.TaskKind_TASK_KIND_AZIMA
	putTasks(t, c.store, before, planned, done, azima)

	file := filepath.Join(t.TempDir(), "plan-b.md")
	if err := os.WriteFile(file, []byte("option: B\ntitle: Light the paraffin lamp\nafter: w1\nblocks: W2\npart-of: t1\n\nLight it."), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{
		WishId: wishID, Options: []string{"Olive", "Paraffin"}, PlanFile: []string{file},
		Plan: []string{"option: A\ntitle: Light the olive lamp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.GetPlans()) != 2 || q.GetPlans()[0].GetOption() != planv1.Choice_CHOICE_A {
		t.Fatalf("plans = %v", q.GetPlans())
	}
	b := q.GetPlans()[1]
	if b.GetAfter()[0] != before.GetId() || b.GetBlocks()[0] != planned.GetId() || b.GetPartOf() != azima.GetId() ||
		b.GetPrompt() != "Light it." || len(b.GetDependencies()) != 2 ||
		b.GetDependencies()[0].GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING || b.GetDependencies()[1].GetCode() != "W2" {
		t.Errorf("plan B = %v", b)
	}
	// The journal keeps what the file said, not where it was.
	commands, err := store.Commands(t.Context(), c.store, func(cmd store.Command) bool {
		return cmd.Method == planv1connect.QuestionServiceAskProcedure
	})
	if err != nil || len(commands) != 1 {
		t.Fatal(commands, err)
	}
	var asked planv1.QuestionServiceAskRequest
	if err := proto.Unmarshal(commands[0].Request, &asked); err != nil {
		t.Fatal(err)
	}
	if len(asked.GetPlanFile()) != 0 || len(asked.GetPlan()) != 2 || !strings.Contains(asked.GetPlan()[1], "Light it.") {
		t.Errorf("the journal keeps %v", &asked)
	}

	// A question without options: its plan is for yes.
	yes, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wishID, Plan: []string{"title: Light it"}})
	if err != nil || yes.GetPlans()[0].GetOption() != planv1.Choice_CHOICE_YES {
		t.Errorf("a plan without options: %v, %v", yes.GetPlans(), err)
	}

	for plan, want := range map[string]string{
		"option: A":                             "the plan for A has no title",
		"option: C\ntitle: x":                   "has 2 options: plan for A to B",
		"title: x":                              "name the option the plan is for",
		"option: A\ntitle: x\nafter: W9":        "the plan for A comes after W9, not a task of the wish",
		"option: A\ntitle: x\nblocks: W3":       "the plan for A blocks W3, which is done: a plan blocks planned tasks only",
		"option: A\ntitle: x\npart-of: W1":      "is part of W1, which is work, not an azima",
		"option: A\ntitle: x\ntilasm: L09":      "tilasm L09 is not a tilasm of the wish",
		"option: A\ntitle: x\nproject-id: nope": "names project nope, not one of the wish's",
	} {
		if _, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wishID, Options: []string{"Olive", "Paraffin"},
			Plan: []string{plan}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", plan, err, want)
		}
	}
	if _, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wishID, Options: []string{"Olive", "Paraffin"},
		Plan: []string{"option: A\ntitle: x", "option: a\ntitle: y"}}); err == nil || !strings.Contains(err.Error(), "two plans for option A") {
		t.Errorf("two plans for A: %v", err)
	}
}

// TestRevisePlans: a revision replaces the plan of each option it names, removes one it names alone, keeps the
// others, and drops the plans of options it takes away.
func TestRevisePlans(t *testing.T) {
	c := serve(t)
	wishID := c.wish(t)
	q, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wishID, Options: []string{"Olive", "Paraffin", "Wax"},
		Plan: []string{"option: A\ntitle: Olive", "option: B\ntitle: Paraffin", "option: C\ntitle: Wax"}})
	if err != nil {
		t.Fatal(err)
	}
	revise := func(req *planv1.QuestionServiceReviseRequest) *planv1.Question {
		t.Helper()
		req.Question = &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}
		res, err := c.questions.Revise(t.Context(), connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	titles := func(q *planv1.Question) string {
		var out []string
		for _, p := range q.GetPlans() {
			out = append(out, choiceLetter(p.GetOption())+"="+p.GetTitle())
		}
		return strings.Join(out, " ")
	}
	if got := titles(revise(&planv1.QuestionServiceReviseRequest{Plan: []string{"option: B\ntitle: Paraffin, trimmed", "option: A"}})); got != "B=Paraffin, trimmed C=Wax" {
		t.Errorf("after replacing B and removing A: %s", got)
	}
	if got := titles(revise(&planv1.QuestionServiceReviseRequest{Options: []string{"Olive", "Paraffin"}})); got != "B=Paraffin, trimmed" {
		t.Errorf("after dropping option C: %s", got)
	}
}

// TestRevisionTellsTheLead: an answer whose plan Djinn left to the lead queues its line and, under it, the question,
// the answer and its note, the plan, how each task it names stands against when it was planned, why it no longer
// holds, and the command that launches it. A new note changes it in place.
func TestRevisionTellsTheLead(t *testing.T) {
	ctx := t.Context()
	did := "Its plan waits for you, as the answer has a note: djinn question launch Q01 launches it"
	c := serve(t, WithLeads(&fakeLeads{}), WithAnswered(func(context.Context, *planv1.Question) string { return did }))
	wishID := c.wish(t)
	oil := testTask(wishID, "W1", "Buy oil", planv1.TaskStatus_TASK_STATUS_RUNNING)
	wick := testTask(wishID, "W2", "Buy a wick", planv1.TaskStatus_TASK_STATUS_PENDING)
	trim := testTask(wishID, "W3", "Trim the wick", planv1.TaskStatus_TASK_STATUS_PENDING)
	putTasks(t, c.store, oil, wick, trim)
	q, err := c.askPlans(t, &planv1.QuestionServiceAskRequest{WishId: wishID, Text: "Which oil?",
		Options: []string{"Olive", "Paraffin"},
		Plan:    []string{"option: A\ntitle: Light the olive lamp\nprovider: fake\nafter: W1, W2\nblocks: W3\n\nLight it with care."}})
	if err != nil {
		t.Fatal(err)
	}
	oil.Status, wick.Status, trim.Title = planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_CANCELLED, "Trim both wicks"
	putTasks(t, c.store, oil, wick, trim)
	c.answer(t, q, planv1.Choice_CHOICE_A, "the blue wick")

	events, err := waitingEvents(ctx, c.store)
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	want := strings.Join([]string{
		`Question: Which oil?`,
		`Answer: A — "Olive". Note: "the blue wick".`,
		`Plan for A: "Light the olive lamp" (fake)`,
		`After: W1 done, changed since the question (was running); W2 cancelled, changed since the question (was pending)`,
		`Blocks: W3 pending, changed since the question (retitled)`,
		`No longer holds: W2, which it comes after, was cancelled.`,
		`Prompt: Light it with care.`,
		`Launch it as it is: djinn question launch Q01; edited: the same with djinn task spawn's flags in place of the ` +
			`plan's (--title, --prompt, --after "" for none…).`,
	}, "\n")
	if got := events[0].GetDetail(); got != want {
		t.Errorf("detail\n%s\nwant\n%s", got, want)
	}
	if !strings.HasSuffix(events[0].GetText(), did+".") {
		t.Errorf("line %q lacks what Djinn did", events[0].GetText())
	}
	digest := (&Wishes{Store: c.store}).digest(ctx, events, "", time.Second)
	if !strings.Contains(digest, "\n  After: W1 done") || !strings.Contains(digest, "\n  Launch it as it is: djinn question launch Q01;") {
		t.Errorf("the digest lacks the detail under the line:\n%s", digest)
	}

	// The developer changes the note: the waiting event takes the new one, its detail too.
	c.answer(t, q, planv1.Choice_CHOICE_A, "the red wick")
	if events, err = waitingEvents(ctx, c.store); err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	if !strings.Contains(events[0].GetDetail(), `Note: "the red wick".`) || !strings.Contains(events[0].GetText(), "the red wick") {
		t.Errorf("after the note changed: %q\n%s", events[0].GetText(), events[0].GetDetail())
	}
}
