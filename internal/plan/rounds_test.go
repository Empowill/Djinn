package plan

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// TestRounds: the developer asks to investigate, the lead revises, twice; each round keeps what the question said
// before it. A decided question takes no round. The brief lists what to investigate, with its note.
func TestRounds(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	res, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", Options: []string{"Olive", "Paraffin"}, WishId: wish,
		Context: "Both burn.", Recommendation: "A: it smells good.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: res.Msg.GetQuestion().GetCode()}}
	enlighten := func(note string) *planv1.Question {
		t.Helper()
		res, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
			Question: ref, Note: note, WishId: wish,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}

	q := enlighten("How long does each burn?")
	if !Investigating(q) || q.GetRounds()[0].GetNote() != "How long does each burn?" || q.GetRounds()[0].GetActor() != actor {
		t.Fatalf("after enlighten = %v", q)
	}
	brief, err := BuildBrief(ctx, c.store, "", wish)
	if err != nil {
		t.Fatal(err)
	}
	text := brief.Text()
	if !strings.Contains(text, "## To investigate") || !strings.Contains(text, "**Q01** Which oil? (asked ") ||
		!strings.Contains(text, "): How long does each burn?") ||
		!strings.Contains(text, "the context in the shape every question keeps: the commands in one code block at the top") {
		t.Errorf("the brief lacks the question to investigate:\n%s", text)
	}
	if strings.Contains(text, "## Open questions") {
		t.Errorf("a question being investigated is not waiting for the developer:\n%s", text)
	}

	rev, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "Olive: 6 h. Paraffin: 9 h.", Recommendation: "B: it lasts.", History: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	q = rev.Msg.GetQuestion()
	last := q.GetRounds()[1]
	if Investigating(q) || q.GetRevision() != 1 || q.GetContext() != "Olive: 6 h. Paraffin: 9 h." ||
		q.GetRecommendation() != "B: it lasts." || len(q.GetOptions()) != 2 {
		t.Errorf("after revise = %v", q)
	}
	if last.GetKind() != planv1.RoundKind_ROUND_KIND_REVISE || last.GetContext() != "Both burn." ||
		last.GetRecommendation() != "A: it smells good." || len(last.GetOptions()) != 2 {
		t.Errorf("the revision's round keeps the former question: %v", last)
	}
	// A second round; the lead may revise again without a request.
	enlighten("")
	for range 2 {
		if _, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: ref, WishId: wish, Options: []string{"Olive", "Paraffin", "Both"},
		})); err != nil {
			t.Fatal(err)
		}
	}
	brief, _ = BuildBrief(ctx, c.store, "", wish)
	if text := brief.Text(); !strings.Contains(text, "  - Revised 3 times") || strings.Contains(text, "## To investigate") {
		t.Errorf("the brief after three revisions:\n%s", text)
	}

	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: ref, Choice: planv1.Choice_CHOICE_B, WishId: wish,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: ref, WishId: wish,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("enlighten a decision: %v, want failed_precondition", err)
	}
	if _, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "Too late.",
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("revise a decision: %v, want failed_precondition", err)
	}
}

// TestWorkerAttribution: a worker's question, revision and block name its task ($DJINN_TASK_ID on the command line),
// so the decision log and the page say "by W1", not "by the lead"; a task of another wish is refused.
func TestWorkerAttribution(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	task := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W1", Title: "Pick oil"}
	stranger := &planv1.Task{Id: store.NewID(), WishId: other, Code: "W1"}
	for _, task := range []*planv1.Task{task, stranger} {
		if err := c.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("test", "put", task); err != nil {
				return err
			}
			return tx.Put(task)
		}); err != nil {
			t.Fatal(err)
		}
	}
	asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", Options: []string{"Olive", "Paraffin"}, WishId: wish, TaskId: task.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if asked.Msg.GetQuestion().GetTaskId() != task.GetId() {
		t.Errorf("the question does not name the task that asked it: %v", asked.Msg.GetQuestion())
	}
	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}
	rev, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "Olive: 6 h.", TaskId: task.GetId(), History: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r := rev.Msg.GetQuestion().GetRounds(); len(r) != 1 || r[0].GetTaskId() != task.GetId() {
		t.Errorf("the revision does not name its task: %v", r)
	}
	block, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "decision", Title: "No CGO", TaskId: task.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	exp, _, err := collect(ctx, c.store, wish, allEvents)
	if err != nil {
		t.Fatal(err)
	}
	if d := render.Decisions(exp); len(d) != 1 || d[0].Block.GetId() != block.Msg.GetBlock().GetId() || d[0].By != "W1" {
		t.Errorf("decisions %+v, want the block by W1", d)
	}
	page, err := render.Page(render.Input{Export: exp})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Revised by W1") {
		t.Error("the page does not say who revised the question")
	}

	// Another wish's task is refused.
	if _, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "Paraffin: 9 h.", TaskId: stranger.GetId(),
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a revision by another wish's task: %v, want invalid_argument", err)
	}
	if _, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which wick?", WishId: wish, TaskId: stranger.GetId(),
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a question by another wish's task: %v, want invalid_argument", err)
	}
}

// TestRevisionHistoryAndContextFile: a revised question's context is its current version only (W114), what the
// caller pastes of an older history cut; each earlier version lives in its round, Tour 0 on the first. Revise reads
// --context-file, Markdown headers, arrows and parentheses as written, and gives back the rounds only with history.
func TestRevisionHistoryAndContextFile(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which transport?", Options: []string{"gRPC", "Connect"}, WishId: wish,
		Context: "Tour 0 initial context.", Recommendation: "B: Connect works over HTTP/1.1.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: asked.Msg.GetQuestion().GetCode()}}

	// Enlighten 1 records Tour 0 on rounds[0].
	enl1, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: ref, WishId: wish, Note: "Check browser streaming.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	r0 := enl1.Msg.GetQuestion().GetRounds()[0]
	if r0.GetContext() != "Tour 0 initial context." || r0.GetRecommendation() != "B: Connect works over HTTP/1.1." ||
		!slices.Equal(r0.GetOptions(), []string{"gRPC", "Connect"}) {
		t.Fatalf("Tour 0 not preserved on enlighten round: %v", r0)
	}

	// Revise 1 via ContextFile containing #, ->, and parentheses.
	ctxFile := t.TempDir() + "/rev1.md"
	rev1Body := "~~~\n# check streaming\ncurl https://example.com\n~~~\n\nWhy: browser -> server streaming (verified)."
	if err := writeTestFile(ctxFile, rev1Body+"\n"); err != nil {
		t.Fatal(err)
	}
	rev1, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, ContextFile: ctxFile, Recommendation: "B: Connect streams cleanly.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q1 := rev1.Msg.GetQuestion()
	if q1.GetContext() != rev1Body {
		t.Errorf("context after rev1 = %q, want %q", q1.GetContext(), rev1Body)
	}
	if len(q1.GetRounds()) != 0 || q1.GetRevision() != 1 {
		t.Errorf("revise without history gives back its rounds, or no revision: %v", q1)
	}

	// Enlighten 2 + Revise 2: a context pasted with an old history keeps its current part only.
	if _, err := c.questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: ref, WishId: wish, Note: "What about trailers?",
	})); err != nil {
		t.Fatal(err)
	}
	rev2, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish,
		Context:        "Tour 2 context.\n\n" + HistoryHeader + "\n\nstale copy",
		Recommendation: "B: trailers framed in body.",
		History:        true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	q2 := rev2.Msg.GetQuestion()
	if q2.GetContext() != "Tour 2 context." {
		t.Errorf("context after rev2 = %q, want the current version only", q2.GetContext())
	}
	var revised []string
	for _, r := range q2.GetRounds() {
		if r.GetKind() == planv1.RoundKind_ROUND_KIND_REVISE {
			revised = append(revised, r.GetContext())
		}
	}
	if !slices.Equal(revised, []string{"Tour 0 initial context.", rev1Body}) {
		t.Errorf("the revisions' rounds hold %q, want each earlier version", revised)
	}
	stored, err := store.Get[*planv1.Question](ctx, c.store, q2.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetContext() != "Tour 2 context." || len(stored.GetRounds()) != 4 {
		t.Errorf("stored = %v, want the current context and its four rounds", stored)
	}

	// Passing both context and context_file is rejected.
	if _, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "x", ContextFile: ctxFile,
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("both context and context_file: %v, want invalid_argument", err)
	}
}

// TestCleanVersions (W114): a question stored with the history Revise appended to its context, and a round that
// kept it, lose it at djinn up; a clean one is not written again.
func TestCleanVersions(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	old := c.ask(t, wish, "Olive", "Paraffin")
	old.Context = "Paraffin lasts.\n\n" + HistoryHeader + "\n\n### Tour 0 (initial)\n\nBoth burn."
	old.Rounds = []*planv1.Round{
		{Kind: planv1.RoundKind_ROUND_KIND_REVISE, Context: "Both burn."},
		{Kind: planv1.RoundKind_ROUND_KIND_REVISE, Context: "Olive smells.\n" + HistoryHeader + "\n\nBoth burn."},
	}
	clean := c.ask(t, wish, "Yes", "No")
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "put", old); err != nil {
			return err
		}
		return tx.Put(old)
	}); err != nil {
		t.Fatal(err)
	}
	journal := func() int {
		cmds, err := store.CommandsOf(ctx, c.store, methodCleanVersions, nil)
		if err != nil {
			t.Fatal(err)
		}
		return len(cmds)
	}
	for range 2 {
		if err := CleanVersions(ctx, c.store); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Get[*planv1.Question](ctx, c.store, old.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetContext() != "Paraffin lasts." || got.GetRounds()[0].GetContext() != "Both burn." ||
		got.GetRounds()[1].GetContext() != "Olive smells." {
		t.Errorf("cleaned = %v", got)
	}
	if n := journal(); n != 1 {
		t.Errorf("%d cleanings journaled, want 1: the second run and the clean question %s change nothing", n,
			clean.GetCode())
	}
}

// TestEnlightenOutlivesItsCall (W536): a request to investigate, once stored, is followed up even when its caller
// hangs up first, as the window's call did while djinn up hung: the harness's hook and the lead's event run on.
func TestEnlightenOutlivesItsCall(t *testing.T) {
	c := serve(t)
	wish := c.wish(t) // Of no project, as the incident's.
	asked := c.ask(t, wish, "Ask first", "Install anyway")
	ctx, hangUp := context.WithCancel(t.Context())
	defer hangUp()
	wishes := &Wishes{Store: c.store, Leads: &fakeLeads{}}
	var hookErr error
	questions := &Questions{Store: c.store, Enlightened: []func(context.Context, *planv1.Question, string){
		func(ctx context.Context, _ *planv1.Question, _ string) {
			hangUp() // The caller leaves while the harness tries to start the investigator, and fails to.
			hookErr = ctx.Err()
		},
		wishes.Enlightened,
	}}
	if _, err := questions.Enlighten(ctx, connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: asked.GetCode()}}, WishId: wish,
		Note: "are my specs enough first?",
	})); err != nil {
		t.Fatal(err)
	}
	if hookErr != nil {
		t.Errorf("the harness's hook ran on a context ended with its call: %v", hookErr)
	}
	events, err := store.List[*planv1.LeadEvent](t.Context(), c.store, store.Where{"wish_id": wish})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN {
		t.Fatalf("lead events: %v, want one to investigate", events)
	}
}
