package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestBefore: a question says what its answer is needed before, or nothing and it can wait. A revision keeps the
// words when it gives none, changes them, or clears them when it gives them empty.
func TestBefore(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wishID := c.wish(t)

	asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", WishId: wishID, Before: "  before the merge ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := asked.Msg.GetQuestion()
	if q.GetBefore() != "before the merge" {
		t.Errorf("before = %q, want the words, trimmed", q.GetBefore())
	}
	if c.ask(t, wishID).GetBefore() != "" {
		t.Error("a question asked without --before can wait")
	}
	if _, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", WishId: wishID, Before: strings.Repeat("x", 101),
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("101 characters before: %v, want invalid argument", err)
	}

	revise := func(before *string) *planv1.Question {
		t.Helper()
		res, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}, WishId: wishID,
			Context: "More.", Before: before,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	if got := revise(nil).GetBefore(); got != "before the merge" {
		t.Errorf("a revision without --before keeps the words, got %q", got)
	}
	if got := revise(proto.String("before the demo")).GetBefore(); got != "before the demo" {
		t.Errorf("a revision with --before changes them, got %q", got)
	}
	if got := revise(proto.String("")).GetBefore(); got != "" {
		t.Errorf("a revision with --before \"\" clears them, got %q", got)
	}
}
