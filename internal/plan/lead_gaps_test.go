package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The gaps of the lead's command line its session of 16/10/2026 measured (decision Q31 A, study L10): one answer's
// note read through a list of 113 KB, task codes that two wishes shared, notes cut at 160 characters, and a task
// watch that followed a running task. The digest's own (TestDigest*) are beside it, in leadwatch_test.go.

// TestQuestionGet: djinn question get prints one question as it stands, its answer and note whole, without what its
// revisions replaced; a code two wishes share needs the wish.
func TestQuestionGet(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	w1, w2 := c.wish(t), c.wish(t)
	former := strings.Repeat("what the question said before. ", 500)
	note := "Take B, and keep the old page a week.\n\nThen drop it."
	q := &planv1.Question{
		Id: store.NewID(), Code: "Q01", WishId: w1, Text: "Which page?", Options: []string{"Old", "New"},
		Context: "The context now.", Recommendation: "B: the new one.",
		Rounds: []*planv1.Round{
			{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, Note: "Which one loads faster?"},
			{Kind: planv1.RoundKind_ROUND_KIND_REVISE, Context: former, Options: []string{"Old", "New", "Both"},
				Recommendation: "C: both."},
		},
		Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_B, Note: note},
	}
	other := &planv1.Question{Id: store.NewID(), Code: "Q01", WishId: w2, Text: "Another one?"}
	c.put(t, q, other)

	get := func(req *planv1.QuestionServiceGetRequest) (*planv1.Question, error) {
		res, err := c.questions.Get(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetQuestion(), nil
	}
	byCode := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q01"}}
	if _, err := get(&planv1.QuestionServiceGetRequest{Question: byCode}); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "--wish-id") {
		t.Errorf("a code of two wishes, no wish = %v, want failed_precondition naming --wish-id", err)
	}
	got, err := get(&planv1.QuestionServiceGetRequest{Question: byCode, WishId: w1})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetId() != q.GetId() || got.GetAnswer().GetNote() != note || got.GetContext() != "The context now." ||
		got.GetRecommendation() != "B: the new one." || len(got.GetOptions()) != 2 {
		t.Errorf("question get = %v, want Q01 of the first wish as it stands, its note whole", got)
	}
	if len(got.GetRounds()) != 2 || got.GetRounds()[0].GetNote() != "Which one loads faster?" {
		t.Fatalf("rounds = %v, want both, the request's note kept", got.GetRounds())
	}
	if r := got.GetRounds()[1]; r.GetContext() != "" || len(r.GetOptions()) != 0 || r.GetRecommendation() != "" {
		t.Errorf("a revision keeps what it replaced: %v", r)
	}
	stored, err := store.Get[*planv1.Question](ctx, c.store, q.GetId())
	if err != nil || stored.GetRounds()[1].GetContext() != former {
		t.Errorf("question get changed the stored rounds: %v", err)
	}
	if got, err := get(&planv1.QuestionServiceGetRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: other.GetId()}},
	}); err != nil || got.GetWishId() != w2 {
		t.Errorf("question get by identifier = %v, %v", got, err)
	}
	if _, err := get(&planv1.QuestionServiceGetRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q09"}}, WishId: w1,
	}); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown code = %v, want not_found", err)
	}
}

// putWishes writes wishes of these titles in w's store, and returns their identifiers.
// TestPacketCarriesTheNote: a driven lead's batch carries an event's note whole, apart from its line, once.
func TestPacketCarriesTheNote(t *testing.T) {
	t.Parallel()
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, Code: "Q03", WishId: "w1",
		Text: `Djinn: Q03 answered A. Note: "Yes, but…".`, Note: "Yes, but\nonly on Linux."}
	got := eventEntry(e, map[string]string{"w1": "Djinn QoL"})
	if !strings.Contains(got, "The note, whole:\n\n> Yes, but\n> only on Linux.\n") {
		t.Errorf("entry = %s", got)
	}
	e.Note = "Yes, but"
	if got := eventEntry(e, nil); strings.Contains(got, "The note, whole") {
		t.Errorf("a note its text holds comes twice: %s", got)
	}
}
