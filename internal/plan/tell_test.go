package plan

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func (c clients) answer(t *testing.T, q *planv1.Question, choice planv1.Choice, note string) {
	t.Helper()
	if _, err := c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: choice, Note: note,
		By: planv1.Closer_CLOSER_DEVELOPER,
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
		if got := AnswerLine(q, ""); got != want {
			t.Errorf("%v:\n got %s\nwant %s", choice, got, want)
		}
	}
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A}
	if got, want := AnswerLine(q, ""), `Djinn: Q43 answered A — "Olive". Turn it into tasks: you hold the plan's graph (djinn wish brief w1).`; got != want {
		t.Errorf("without a note:\n got %s\nwant %s", got, want)
	}
	if got, want := EnlightenLine(q, "what does each cost?", ""),
		`Djinn: Q43, the developer wants to know more before answering. Note: "what does each cost?". `+
			`Investigate, then revise Q43: djinn wish brief w1 has the context.`; got != want {
		t.Errorf("enlighten:\n got %s\nwant %s", got, want)
	}
	// An answer Djinn settles itself: the line says what Djinn did, never to act on it.
	if got, want := AnswerLine(q, "Djinn started W168: Settle the conflict of W156 with main."),
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
	briefNoLead, err := BuildBrief(ctx, c.store, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(briefNoLead.Moving, "Q01") || !strings.Contains(briefNoLead.Moving, "Paraffin") {
		t.Errorf("brief without lead: got %q, want Q01 decision", briefNoLead.Moving)
	}

	// A lead is set, then resumed: waiting answers are told at start, then new answers reach the running lead in order.
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id})); err != nil {
		t.Fatal(err)
	}
	if want := "claude --resume " + session + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Errorf("opened %q, want %q", leads.opened, want)
	}
	leads.shown = nil
	q2, q3 := c.ask(t, id, "Brass", "Glass"), c.ask(t, id)
	c.answer(t, q2, planv1.Choice_CHOICE_A, "the old one")
	c.answer(t, q3, planv1.Choice_CHOICE_YES, "")
	want := []string{
		terminal + `: Djinn: Q01 answered B — "Paraffin". Turn it into tasks: you hold the plan's graph (djinn wish brief ` + id + ").",
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

	// The lead's terminal runs a shell: the line would run as a command, so it is not typed.
	leads.running[terminal], leads.said = []string{"/bin/sh"}, nil
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if len(leads.said) != 0 {
		t.Errorf("typed in a shell: %q", leads.said)
	}
	briefShell, err := BuildBrief(ctx, c.store, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(briefShell.Moving, "Q05") {
		t.Errorf("brief when shell ran: got %q, want Q05 decision", briefShell.Moving)
	}

	// A paused wish's lead is told while it runs, and not reopened once it stopped.
	leads.running[terminal] = []string{"/bin/sh", "-c", "claude --resume " + session}
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: id})); err != nil {
		t.Fatal(err)
	}
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	delete(leads.running, terminal)
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if len(leads.said) != 1 || !strings.Contains(leads.said[0], "Q06 answered yes") || len(leads.opened) != 1 {
		t.Errorf("paused: said %q, opened %q; want Q06 told, nothing reopened", leads.said, leads.opened)
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
	// An investigator worker was started: silent in the lead terminal per Decision Q173 (lead hears when it ends).
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
	if len(leads.said) != 0 {
		t.Errorf("with investigator: said %q, want nothing (silent in terminal)", leads.said)
	}
}

func TestAnswerRoutingToPrincipalAndWaitingLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{running: map[string][]string{}}
	c := serve(t, WithLeads(leads))
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	projID := project.Msg.GetProject().GetId()

	// Create wish 1 (Principal by rank 1) and wish 2.
	made1, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Principal Wish", ProjectIds: []string{projID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish1ID := made1.Msg.GetWish().GetId()
	term1 := LeadTerminal(wish1ID)

	made2, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Relecture in Djinn", ProjectIds: []string{projID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish2ID := made2.Msg.GetWish().GetId()
	term2 := LeadTerminal(wish2ID)

	// Set lead on wish 1 and mark its terminal as running.
	session1 := "session-lead-1"
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: wish1ID, SessionId: session1}); err != nil {
		t.Fatal(err)
	}
	leads.running[term1] = []string{"/bin/sh", "-c", "claude --resume " + session1}

	// 1. Wish without a lead (wish 2 has no lead at all): answer goes to principal lead's terminal.
	q1 := c.ask(t, wish2ID, "Olive", "Paraffin")
	c.answer(t, q1, planv1.Choice_CHOICE_B, "for warmth")

	want1 := term1 + `: Djinn: Q01 answered B — "Paraffin" in Relecture in Djinn (wish ` +
		wish2ID + "): turn it into tasks there; djinn wish brief " + wish2ID + ` has its context. Note: "for warmth".`
	if len(leads.said) != 1 || leads.said[0] != want1 {
		t.Errorf("answer without lead: said %q, want %q", leads.said, want1)
	}
	// Verify no worker was started for the question.
	tasks, err := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": wish2ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("worker task was started: %v", tasks)
	}
	leads.said = nil

	// 2. Wish with its own lead: answer goes only to it, not to principal lead.
	session2 := "session-lead-2"
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: wish2ID, SessionId: session2}); err != nil {
		t.Fatal(err)
	}
	leads.running[term2] = []string{"/bin/sh", "-c", "claude --resume " + session2}

	q2 := c.ask(t, wish2ID)
	c.answer(t, q2, planv1.Choice_CHOICE_YES, "")

	want2 := term2 + ": Djinn: Q02 answered yes. Turn it into tasks: you hold the plan's graph (djinn wish brief " + wish2ID + ")."
	if len(leads.said) != 1 || leads.said[0] != want2 {
		t.Errorf("answer with own lead: said %q, want %q", leads.said, want2)
	}
	leads.said = nil

	// 3. No lead runs at all: answer waits.
	delete(leads.running, term1)
	delete(leads.running, term2)

	q3 := c.ask(t, wish2ID)
	c.answer(t, q3, planv1.Choice_CHOICE_YES, "")

	if len(leads.said) != 0 {
		t.Errorf("when no lead runs: said %q, want nothing", leads.said)
	}
	storedQ3, err := store.Get[*planv1.Question](ctx, c.store, q3.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !storedQ3.GetWaitingLead() {
		t.Errorf("stored question waiting_lead = false, want true")
	}

	// 4. Next lead to start (wish 1 resumed) is told of every waiting answer.
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: wish1ID})); err != nil {
		t.Fatal(err)
	}
	want3 := term1 + ": Djinn: Q03 answered yes in Relecture in Djinn (wish " +
		wish2ID + "): turn it into tasks there; djinn wish brief " + wish2ID + " has its context."
	if len(leads.said) != 1 || leads.said[0] != want3 {
		t.Errorf("resumed lead told of waiting answers: said %q, want %q", leads.said, want3)
	}

	storedQ3After, err := store.Get[*planv1.Question](ctx, c.store, q3.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if storedQ3After.GetWaitingLead() {
		t.Errorf("after being told, waiting_lead = true, want false")
	}
}

func TestLeadAnswerNotToldToLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Lead answer", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	leads.running = map[string][]string{
		LeadTerminal(id): {"/bin/sh", "-c", "claude --resume " + session},
	}

	// 1. Answer from the lead's terminal (Lead set to wish id, as $DJINN_LEAD does):
	// attributed to CLOSER_LEAD and NOT told to the lead.
	q1 := c.ask(t, id, "A", "B")
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q1.GetId()}},
		Choice:   planv1.Choice_CHOICE_A,
		Lead:     id,
	})); err != nil {
		t.Fatal(err)
	}
	if len(leads.said) != 0 {
		t.Errorf("lead answer was told to the lead: %v", leads.said)
	}
	saved1 := c.questionOf(t, q1.GetId())
	if saved1.GetAnswer().GetActor() != planv1.Closer_CLOSER_LEAD {
		t.Errorf("actor = %v, want CLOSER_LEAD", saved1.GetAnswer().GetActor())
	}

	// 2. Answer from a plain shell (neither Lead nor TaskId set, no By):
	// defaults to CLOSER_DEVELOPER and IS told to the lead.
	q2 := c.ask(t, id, "A", "B")
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q2.GetId()}},
		Choice:   planv1.Choice_CHOICE_B,
	})); err != nil {
		t.Fatal(err)
	}
	if len(leads.said) != 1 {
		t.Fatalf("plain shell answer was not told to the lead: %v", leads.said)
	}
	if !strings.Contains(leads.said[0], "Q02 answered B") {
		t.Errorf("said = %q, want Q02 answered B", leads.said[0])
	}
	saved2 := c.questionOf(t, q2.GetId())
	if saved2.GetAnswer().GetActor() != planv1.Closer_CLOSER_DEVELOPER {
		t.Errorf("actor = %v, want CLOSER_DEVELOPER", saved2.GetAnswer().GetActor())
	}
	leads.said = nil

	// 3. Answer by a worker ($DJINN_TASK_ID): attributed to its task.
	taskID := uuid.NewString()
	c.put(t, &planv1.Task{
		Id: taskID, WishId: id, Code: "W01", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
	})
	q3 := c.ask(t, id, "A", "B")
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q3.GetId()}},
		Choice:   planv1.Choice_CHOICE_A,
		TaskId:   taskID,
	})); err != nil {
		t.Fatal(err)
	}
	saved3 := c.questionOf(t, q3.GetId())
	if saved3.GetAnswer().GetActor() != planv1.Closer_CLOSER_UNSPECIFIED || saved3.GetAnswer().GetTaskId() != taskID {
		t.Errorf("worker answer: actor = %v, taskId = %q, want CLOSER_UNSPECIFIED and %q",
			saved3.GetAnswer().GetActor(), saved3.GetAnswer().GetTaskId(), taskID)
	}
}
