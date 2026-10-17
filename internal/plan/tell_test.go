package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"

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
	terminal := LeadTerminal

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

	// A lead is set, then resumed: the answers wait in its queue, the one that waited first, in order; nothing is
	// typed in its terminal (decision Q191: its watch hands them out).
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); err != nil {
		t.Fatal(err)
	}
	wantLine, _ := resumeLine(&planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session})
	if want := wantLine + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
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
	if got := told(t, c.store); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("queued\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(leads.said) != 0 || len(leads.shown) != 0 {
		t.Errorf("said %q, shown %q: an answer is queued, never typed nor shown", leads.said, leads.shown)
	}
	take(t, c.store)

	// An answer Djinn settles itself (a failed integration, a push, an edit): the lead hears what Djinn did with it.
	settled = "Djinn started W168: Settle the conflict of W156 with main"
	c.answer(t, c.ask(t, id, "Try again", "Leave it"), planv1.Choice_CHOICE_A, "")
	settled = ""
	if got, want := told(t, c.store), []string{terminal + `: Djinn: Q04 answered A — "Try again". ` +
		`Djinn started W168: Settle the conflict of W156 with main.`}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("settled by Djinn: queued\n%s\nwant\n%s", got, want)
	}
	take(t, c.store)

	// The lead's terminal runs a shell, or the wish is paused and its lead stopped: the answer waits in the queue
	// all the same, never lost, and nothing is typed.
	leads.running[terminal] = []string{"/bin/sh"}
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: id})); err != nil {
		t.Fatal(err)
	}
	delete(leads.running, terminal)
	c.answer(t, c.ask(t, id), planv1.Choice_CHOICE_YES, "")
	if got := told(t, c.store); len(got) != 2 || !strings.Contains(got[0], "Q05 answered yes") || !strings.Contains(got[1], "Q06 answered yes") {
		t.Errorf("queued %q, want Q05 then Q06", got)
	}
	if len(leads.said) != 0 || len(leads.opened) != 1 {
		t.Errorf("said %q, opened %q; want nothing typed, nothing reopened", leads.said, leads.opened)
	}
	briefShell, err := BuildBrief(ctx, c.store, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(briefShell.Moving, "Q05") {
		t.Errorf("brief when shell ran: got %q, want Q05 decision", briefShell.Moving)
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
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
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
	want := LeadTerminal + ": Djinn: Q01, the developer wants to know more before answering. Note: \"" + asked +
		"And the \"smoke\" of the wick — and why?\". Investigate, then revise Q01: djinn wish brief " + id + " has the context."
	if got := told(t, c.store); len(got) != 1 || got[0] != want {
		t.Errorf("queued %q, want %q", got, want)
	}

	take(t, c.store)
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
	if got := told(t, c.store); len(got) != 0 {
		t.Errorf("with investigator: queued %q, want nothing", got)
	}
}

func TestAnswerInAnyWishToldToOneLead(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{running: map[string][]string{}}
	c := serve(t, WithLeads(leads))
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	projID := project.Msg.GetProject().GetId()

	// Create wish 1 and wish 2.
	made1, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Principal Wish", ProjectIds: []string{projID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish1ID := made1.Msg.GetWish().GetId()

	made2, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Relecture in Djinn", ProjectIds: []string{projID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish2ID := made2.Msg.GetWish().GetId()

	// Set the one lead and mark its terminal as running.
	session1 := "session-lead-1"
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session1}); err != nil {
		t.Fatal(err)
	}
	leads.running[LeadTerminal] = []string{"/bin/sh", "-c", "claude --resume " + session1}

	// 1. Answer in wish 2 told to the lead terminal.
	q1 := c.ask(t, wish2ID, "Olive", "Paraffin")
	c.answer(t, q1, planv1.Choice_CHOICE_B, "for warmth")

	want1 := LeadTerminal + `: Djinn: Q01 answered B — "Paraffin". Note: "for warmth". Turn it into tasks: you hold the plan's graph (djinn wish brief ` + wish2ID + ")."
	if got := told(t, c.store); len(got) != 1 || got[0] != want1 {
		t.Errorf("answer in wish 2: queued %q, want %q", got, want1)
	}
	// Verify no worker was started for the question.
	tasks, err := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": wish2ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("worker task was started: %v", tasks)
	}
	take(t, c.store)

	// 2. Answer in wish 1 told to the same lead terminal.
	q2 := c.ask(t, wish1ID)
	c.answer(t, q2, planv1.Choice_CHOICE_YES, "")

	want2 := LeadTerminal + ": Djinn: Q01 answered yes. Turn it into tasks: you hold the plan's graph (djinn wish brief " + wish1ID + ")."
	if got := told(t, c.store); len(got) != 1 || got[0] != want2 {
		t.Errorf("answer in wish 1: queued %q, want %q", got, want2)
	}
	take(t, c.store)

	// 3. No lead runs at all: answer waits.
	delete(leads.running, LeadTerminal)

	q3 := c.ask(t, wish2ID)
	c.answer(t, q3, planv1.Choice_CHOICE_YES, "")

	if len(leads.said) != 0 {
		t.Errorf("when no lead runs: said %q, want nothing typed", leads.said)
	}
	storedQ3, err := store.Get[*planv1.Question](ctx, c.store, q3.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !storedQ3.GetWaitingLead() {
		t.Errorf("stored question waiting_lead = false, want true")
	}

	// 4. The answer waits in the queue, marked waiting for a lead until the lead's watch acknowledges it.
	want3 := LeadTerminal + ": Djinn: Q02 answered yes. Turn it into tasks: you hold the plan's graph (djinn wish brief " + wish2ID + ")."
	if got := told(t, c.store); len(got) != 1 || got[0] != want3 {
		t.Errorf("waiting answer: queued %q, want %q", got, want3)
	}
	digest, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)}))
	if err != nil {
		t.Fatal(err)
	}
	cursor := strings.Fields(digest.Msg.GetText()[strings.Index(digest.Msg.GetText(), "Cursor ")+len("Cursor "):])[0]
	if _, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{
		After: strings.TrimSuffix(cursor, "."), Timeout: durationpb.New(0),
	})); err != nil {
		t.Fatal(err)
	}
	storedQ3After, err := store.Get[*planv1.Question](ctx, c.store, q3.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if storedQ3After.GetWaitingLead() {
		t.Errorf("after the lead acknowledged it, waiting_lead = true, want false")
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
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	leads.running = map[string][]string{
		LeadTerminal: {"/bin/sh", "-c", "claude --resume " + session},
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
	if got := told(t, c.store); len(got) != 0 {
		t.Errorf("lead answer was told to the lead: %v", got)
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
	if got := told(t, c.store); len(got) != 1 || !strings.Contains(got[0], "Q02 answered B") {
		t.Fatalf("plain shell answer: queued %q, want Q02 answered B", got)
	}
	saved2 := c.questionOf(t, q2.GetId())
	if saved2.GetAnswer().GetActor() != planv1.Closer_CLOSER_DEVELOPER {
		t.Errorf("actor = %v, want CLOSER_DEVELOPER", saved2.GetAnswer().GetActor())
	}
	take(t, c.store)

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

// TestTellNeverTypesIntoAShell: a line for the lead goes only to a terminal that runs the lead. For a wish that is
// not active, Tell reopens nothing, and refuses the lead's terminal when it runs a shell, where the line would run as
// a command; for an active one, it refuses such a terminal too.
func TestTellNeverTypesIntoAShell(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Paused", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: id})); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	w := &Wishes{Store: c.store, Leads: leads}

	if err := w.Tell(ctx, id, "Djinn: hello"); !errors.Is(err, ErrNoLead) {
		t.Errorf("tell about a paused wish while no lead runs: %v, want ErrNoLead", err)
	}
	leads.running = map[string][]string{LeadTerminal: {"/bin/sh"}}
	if err := w.Tell(ctx, id, "Djinn: hello"); !errors.Is(err, ErrNoLead) || !strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("tell about a paused wish while the lead's terminal runs a shell: %v, want ErrNoLead", err)
	}
	if err := w.Tell(ctx, "", "Djinn: hello"); err == nil || !strings.Contains(err.Error(), "not the lead") {
		t.Errorf("tell the lead while its terminal runs a shell: %v, want a refusal", err)
	}
	if len(leads.opened) != 0 || len(leads.said) != 0 {
		t.Fatalf("opened %q, said %q: nothing goes to a shell", leads.opened, leads.said)
	}

	leads.running[LeadTerminal] = []string{"/bin/sh", "-c", "claude --resume " + session}
	if err := w.Tell(ctx, id, "Djinn: hello"); err != nil {
		t.Fatal(err)
	}
	if want := []string{LeadTerminal + ": Djinn: hello"}; len(leads.opened) != 0 || !slices.Equal(leads.said, want) {
		t.Errorf("tell a running lead about a paused wish: opened %q, said %q, want %q", leads.opened, leads.said, want)
	}
}

// TestOneAnswerReachesTheLeadOnce replays incident Q253: the window kept the card of an answered question open, so
// the developer sent the same answer again and again (37 times, its note grown once), and the lead's digest held one
// event for each. One answer is one event: the same answer again changes nothing, a new note takes the place of the
// old one in the event that waits, or comes in one event once the lead read the answer; another choice is a change.
func TestOneAnswerReachesTheLeadOnce(t *testing.T) {
	ctx := t.Context()
	leads := &fakeLeads{}
	hooks := 0 // How many times the harness acted on an answer.
	c := serve(t, WithLeads(leads), WithAnswered(func(context.Context, *planv1.Question) string { hooks++; return "" }))
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Name the page", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	q := c.ask(t, id, "Brazier", "Hourglass")
	digest := func() string {
		t.Helper()
		res, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetText()
	}
	answered := func() *planv1.Question {
		t.Helper()
		got, err := store.Get[*planv1.Question](ctx, c.store, q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// The incident: an answer, the same with a longer note, then 35 more times the same.
	c.answer(t, q, planv1.Choice_CHOICE_B, "The hourglass is fine.")
	first := answered().GetAnswer().GetCreateTime().AsTime()
	note := "The hourglass is fine. It centralises the performance worries in this page."
	for range 36 {
		c.answer(t, q, planv1.Choice_CHOICE_B, note)
	}
	if got := answered(); got.GetAnswer().GetNote() != note || !got.GetAnswer().GetCreateTime().AsTime().Equal(first) {
		t.Errorf("answer = %v, want the first one with the new note", got.GetAnswer())
	}
	want := []string{LeadTerminal + ": " + AnswerLine(answered(), "")}
	if got := told(t, c.store); !slices.Equal(got, want) {
		t.Errorf("queued\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if d := digest(); strings.Count(d, "Q01 [Name the page] answer:") != 1 || !strings.Contains(d, "1 event for the lead") {
		t.Errorf("digest:\n%s\nwant one answer event", d)
	}
	if hooks != 1 {
		t.Errorf("the harness acted %d times on one answer", hooks)
	}

	// The lead read it: then 15 edits of the note in a row are one event, with the last note.
	take(t, c.store)
	for i := range 15 {
		c.answer(t, q, planv1.Choice_CHOICE_B, fmt.Sprintf("%s Edit %d.", note, i+1))
	}
	want = []string{LeadTerminal + ": " + NoteEditedLine(answered())}
	if got := told(t, c.store); !slices.Equal(got, want) || !strings.Contains(got[0], "Edit 15.") ||
		!strings.Contains(got[0], `the answer stays B — "Hourglass"`) {
		t.Errorf("after the lead read it, queued\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if hooks != 1 {
		t.Errorf("a note edit made the harness act again: %d", hooks)
	}

	// The same answer once more, after the lead read everything: nothing.
	take(t, c.store)
	c.answer(t, q, planv1.Choice_CHOICE_B, fmt.Sprintf("%s Edit 15.", note))
	if got := told(t, c.store); len(got) != 0 {
		t.Errorf("the same answer again queued %q", got)
	}

	// Another choice is a change, and the line says so; the harness acts on it.
	c.answer(t, q, planv1.Choice_CHOICE_A, "")
	if got := told(t, c.store); len(got) != 1 || !strings.Contains(got[0], `Q01 answer changed from B to A — "Brazier".`) {
		t.Errorf("another choice queued %q", got)
	}
	if hooks != 2 {
		t.Errorf("another choice: the harness acted %d times, want 2", hooks)
	}
}
