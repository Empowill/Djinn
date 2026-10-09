package plan

import (
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
		planv1.Choice_CHOICE_B:   `Djinn: Q43 answered B — "Paraffin". Note: "brighter, then cheaper". Act on it: djinn wish brief w1 has the context.`,
		planv1.Choice_CHOICE_YES: `Djinn: Q43 answered yes. Note: "brighter, then cheaper". Act on it: djinn wish brief w1 has the context.`,
	} {
		q.Answer = &planv1.Answer{Choice: choice, Note: "brighter,\nthen cheaper"}
		if got := AnswerLine(q); got != want {
			t.Errorf("%v:\n got %s\nwant %s", choice, got, want)
		}
	}
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A}
	if got, want := AnswerLine(q), `Djinn: Q43 answered A — "Olive". Act on it: djinn wish brief w1 has the context.`; got != want {
		t.Errorf("without a note:\n got %s\nwant %s", got, want)
	}
	if got, want := EnlightenLine(q, "what does each cost?"),
		`Djinn: Q43, the developer wants to know more before answering. Note: "what does each cost?". `+
			`Investigate, then revise Q43: djinn wish brief w1 has the context.`; got != want {
		t.Errorf("enlighten:\n got %s\nwant %s", got, want)
	}
}

func TestAnswerReachesTheLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
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
		terminal + `: Djinn: Q02 answered A — "Brass". Note: "the old one". Act on it: djinn wish brief ` + id + " has the context.",
		terminal + ": Djinn: Q03 answered yes. Act on it: djinn wish brief " + id + " has the context.",
	}
	if strings.Join(leads.said, "\n") != strings.Join(want, "\n") {
		t.Errorf("said\n%s\nwant\n%s", strings.Join(leads.said, "\n"), strings.Join(want, "\n"))
	}
	if len(leads.shown) != 0 {
		t.Errorf("shown %q: telling the lead does not take the window", leads.shown)
	}

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
	if len(leads.said) != 1 || !strings.Contains(leads.said[0], "Q05 answered yes") || len(leads.opened) != 1 {
		t.Errorf("paused: said %q, opened %q; want Q05 told, nothing reopened", leads.said, leads.opened)
	}
}
