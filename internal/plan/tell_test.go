package plan

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func (c clients) answer(t *testing.T, q *planv1.Question, choice planv1.Choice, note string) {
	t.Helper()
	if _, err := c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice, Note: note,
	})); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerLine(t *testing.T) {
	q := &planv1.Question{Code: "Q43", WishId: "w1", Options: []string{"Olive", "Paraffin"}}
	for choice, want := range map[planv1.Choice]string{
		planv1.Choice_CHOICE_B:   `Djinn: Q43 answered B — "Paraffin". Note: "brighter, then cheaper". Turn it into tasks: you hold the plan's graph (djinn wish brief w1).`,
		planv1.Choice_CHOICE_YES: `Djinn: Q43 answered yes. Note: "brighter, then cheaper". Turn it into tasks: you hold the plan's graph (djinn wish brief w1).`,
	} {
		q.Answer = &planv1.Answer{Choice: choice, Note: "brighter,\nthen cheaper"}
		if got := AnswerLine(q, "", ""); got != want {
			t.Errorf("%v:\n got %s\nwant %s", choice, got, want)
		}
	}
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A}
	if got, want := AnswerLine(q, "", ""), `Djinn: Q43 answered A — "Olive". Turn it into tasks: you hold the plan's graph (djinn wish brief w1).`; got != want {
		t.Errorf("without a note:\n got %s\nwant %s", got, want)
	}
	if got, want := EnlightenLine(q, "what does each cost?", ""),
		`Djinn: Q43, the developer wants to know more before answering. Note: "what does each cost?". `+
			`Investigate, then revise Q43: djinn wish brief w1 has the context.`; got != want {
		t.Errorf("enlighten:\n got %s\nwant %s", got, want)
	}
	// A question worker took it: the lead is informed, not asked to act.
	if got, want := AnswerLine(q, "W12", ""), `Djinn: Q43 answered A — "Olive". W12 turns it into tasks; you will hear when it ends.`; got != want {
		t.Errorf("with a converter:\n got %s\nwant %s", got, want)
	}
	// An answer Djinn settles itself: the line says what Djinn did, never to act on it.
	if got, want := AnswerLine(q, "", "Djinn started W168: Settle the conflict of W156 with main."),
		`Djinn: Q43 answered A — "Olive". Djinn started W168: Settle the conflict of W156 with main.`; got != want {
		t.Errorf("settled by Djinn:\n got %s\nwant %s", got, want)
	}
	if got, want := EnlightenLine(q, "", "W13"),
		`Djinn: Q43, the developer wants to know more before answering. W13 investigates, then revises it; you will `+
			`hear when it ends.`; got != want {
		t.Errorf("with an investigator:\n got %s\nwant %s", got, want)
	}
}

func TestAnswerReachesTheLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	settled := "" // What the harness did with the next answer.
	c := serve(t, WithLeads(leads), WithAnswered(func(context.Context, *planv1.Question) string { return settled }))
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	folder := project.Msg.GetProject().GetDirectory()
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Tell me", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	terminal := LeadTerminal(id)

	// No lead at all: the answer stays in the brief, nothing opens.
	q1 := c.ask(t, id, "Olive", "Paraffin")
	c.answer(t, q1, planv1.Choice_CHOICE_B, "")
	if len(leads.opened) != 0 || len(leads.said) != 0 {
		t.Errorf("without a lead: opened %q, said %q", leads.opened, leads.said)
	}

	// A lead that does not run is reopened on its session, without showing the window, then told; the next answer
	// goes to the same terminal, in order.
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	q2, q3 := c.ask(t, id, "Brass", "Glass"), c.ask(t, id)
	c.answer(t, q2, planv1.Choice_CHOICE_A, "the old one")
	c.answer(t, q3, planv1.Choice_CHOICE_YES, "")
	if want := "claude --resume " + session + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Errorf("opened %q, want %q", leads.opened, want)
	}
	want := []string{
		terminal + `: Djinn: Q02 answered A — "Brass". Note: "the old one". Turn it into tasks: you hold the plan's graph (djinn wish brief ` + id + ").",
		terminal + ": Djinn: Q03 answered yes. Turn it into tasks: you hold the plan's graph (djinn wish brief " + id + ").",
	}
	if strings.Join(leads.said, "\n") != strings.Join(want, "\n") {
		t.Errorf("said\n%s\nwant\n%s", strings.Join(leads.said, "\n"), strings.Join(want, "\n"))
	}
	if len(leads.shown) != 0 {
		t.Errorf("shown %q: telling the lead does not take the window", leads.shown)
	}

	// An answer Djinn settles itself (a failed integration, a push, an edit): the lead hears what Djinn did with it.
	settled = "Djinn started W168: Settle the conflict of W156 with main"
	c.answer(t, c.ask(t, id, "Try again", "Leave it"), planv1.Choice_CHOICE_A, "")
	settled = ""
	if got, want := leads.said[len(leads.said)-1], terminal+`: Djinn: Q04 answered A — "Try again". `+
		`Djinn started W168: Settle the conflict of W156 with main.`; got != want {
		t.Errorf("settled by Djinn: said\n%s\nwant\n%s", got, want)
	}
	leads.said = leads.said[:len(leads.said)-1]

	// A converter worker was started for the question: the lead hears that the worker turns it into tasks.
	qConv := c.ask(t, id)
	c.put(t, &planv1.Task{
		Id: "task-conv", WishId: id, Code: "W12", Question: qConv.GetCode(),
		Role: planv1.TaskRole_TASK_ROLE_CONVERTER, Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
	})
	c.answer(t, qConv, planv1.Choice_CHOICE_YES, "")
	if got, want := leads.said[len(leads.said)-1], terminal+": Djinn: "+qConv.GetCode()+" answered yes. W12 turns it into tasks; you will hear when it ends."; got != want {
		t.Errorf("with converter: said\n%s\nwant\n%s", got, want)
	}
	leads.said = leads.said[:len(leads.said)-1]

	// The lead's terminal runs a shell: the line would run as a command, so it is not typed.
	leads.running[terminal], leads.said = []string{"/bin/sh"}, nil
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if len(leads.said) != 0 {
		t.Errorf("typed in a shell: %q", leads.said)
	}

	// A paused wish's lead is told while it runs, and not reopened once it stopped.
	leads.running[terminal] = []string{"/bin/sh", "-c", "claude --resume " + session}
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: id})); err != nil {
		t.Fatal(err)
	}
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	delete(leads.running, terminal)
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if len(leads.said) != 1 || !strings.Contains(leads.said[0], "Q07 answered yes") || len(leads.opened) != 1 {
		t.Errorf("paused: said %q, opened %q; want Q07 told, nothing reopened", leads.said, leads.opened)
	}
}

// "Enlighten me" reaches the lead as an answer does: one line, so the lead investigates without being told. What the
// developer typed reaches it whole, quotes and all, however long: only its line breaks become spaces.
func TestEnlightenReachesTheLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Dig", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	q := c.ask(t, id, "Olive", "Paraffin")
	asked := strings.Repeat("What does each cost? ", 15)
	note := asked + "\nAnd the \"smoke\" of the wick — and why?"
	if _, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		WishId: id, Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}, Note: note,
	})); err != nil {
		t.Fatal(err)
	}
	want := LeadTerminal(id) + ": Djinn: Q01, the developer wants to know more before answering. Note: \"" + asked +
		"And the \"smoke\" of the wick — and why?\". Investigate, then revise Q01: djinn wish brief " + id + " has the context."
	if len(leads.said) != 1 || leads.said[0] != want {
		t.Errorf("said %q, want %q", leads.said, want)
	}

	leads.said = nil
	// An investigator worker was started: the lead hears that the worker investigates.
	qInv := c.ask(t, id)
	c.put(t, &planv1.Task{
		Id: "task-inv", WishId: id, Code: "W13", Question: qInv.GetCode(),
		Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR, Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
	})
	if _, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		WishId: id, Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: qInv.GetCode()}}, Note: "investigate this",
	})); err != nil {
		t.Fatal(err)
	}
	wantInv := LeadTerminal(id) + ": Djinn: " + qInv.GetCode() + `, the developer wants to know more before answering. Note: "investigate this". W13 investigates, then revises it; you will hear when it ends.`
	if len(leads.said) != 1 || leads.said[0] != wantInv {
		t.Errorf("with investigator: said %q, want %q", leads.said, wantInv)
	}
}
