package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

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
		!strings.Contains(text, "): How long does each burn?") {
		t.Errorf("the brief lacks the question to investigate:\n%s", text)
	}
	if strings.Contains(text, "## Open questions") {
		t.Errorf("a question being investigated is not waiting for the developer:\n%s", text)
	}

	rev, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "Olive: 6 h. Paraffin: 9 h.", Recommendation: "B: it lasts.",
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
