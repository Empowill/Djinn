package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestQuestionLater: putting an open question off stores when, and the brief lists it below the questions asked
// before, above one asked after; the date is read again from the store. Clearing it gives the question its place
// back. A decided or withdrawn question is not put off.
func TestQuestionLater(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	ask := func(text string) *planv1.Question {
		t.Helper()
		res, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{Text: text, WishId: wish}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetQuestion()
	}
	ref := func(q *planv1.Question) *planv1.QuestionRef {
		return &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}
	}
	later := func(q *planv1.Question, back bool) (*planv1.Question, error) {
		res, err := c.questions.Later(ctx, connect.NewRequest(&planv1.QuestionServiceLaterRequest{
			Question: ref(q), WishId: wish, Clear: back,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetQuestion(), nil
	}
	// order is the codes of the brief's open questions, as it lists them.
	order := func() string {
		t.Helper()
		brief, err := BuildBrief(ctx, c.store, "", wish)
		if err != nil {
			t.Fatal(err)
		}
		var codes []string
		for line := range strings.SplitSeq(brief.Text(), "\n") {
			if code, ok := strings.CutPrefix(line, "- **Q"); ok && strings.HasSuffix(line, "(can wait)") {
				codes = append(codes, "Q"+code[:2])
			}
		}
		return strings.Join(codes, " ")
	}
	stored := func(q *planv1.Question) *planv1.Question {
		t.Helper()
		res, err := c.questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{WishId: wish}))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range res.Msg.GetQuestions() {
			if s.GetId() == q.GetId() {
				return s
			}
		}
		t.Fatalf("%s not listed", q.GetCode())
		return nil
	}

	q1, q2, q3 := ask("Which oil?"), ask("Which wick?"), ask("Which lamp?")
	if got := order(); got != "Q01 Q02 Q03" {
		t.Fatalf("brief order = %q, want Q01 Q02 Q03", got)
	}
	put, err := later(q1, false)
	if err != nil {
		t.Fatal(err)
	}
	if put.GetLaterTime() == nil || !put.GetLaterTime().AsTime().After(q3.GetCreateTime().AsTime()) {
		t.Fatalf("later_time = %v, want after %v", put.GetLaterTime(), q3.GetCreateTime())
	}
	if got := stored(q1).GetLaterTime(); got == nil || !got.AsTime().Equal(put.GetLaterTime().AsTime()) {
		t.Errorf("stored later_time = %v, want %v", got, put.GetLaterTime())
	}
	ask("Which flame?")
	if got := order(); got != "Q02 Q03 Q01 Q04" {
		t.Errorf("brief order once Q01 is put off and Q04 asked = %q, want Q02 Q03 Q01 Q04", got)
	}
	brief, err := BuildBrief(ctx, c.store, "", wish)
	if err != nil {
		t.Fatal(err)
	}
	if text := brief.Text(); !strings.Contains(text, "**Q01** Which oil? (can wait)\n  - Put off for later") ||
		strings.Contains(text, "**Q02** Which wick? (can wait)\n  - Put off") {
		t.Errorf("the brief says which question is put off:\n%s", text)
	}

	back, err := later(q1, true)
	if err != nil {
		t.Fatal(err)
	}
	if back.GetLaterTime() != nil || stored(q1).GetLaterTime() != nil {
		t.Errorf("cleared later_time = %v, stored %v; want none", back.GetLaterTime(), stored(q1).GetLaterTime())
	}
	if got := order(); got != "Q01 Q02 Q03 Q04" {
		t.Errorf("brief order once Q01 is back = %q, want Q01 Q02 Q03 Q04", got)
	}

	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: ref(q2), WishId: wish, Choice: planv1.Choice_CHOICE_YES,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := later(q2, false); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("later on a decided question = %v, want FailedPrecondition", err)
	}
	if _, err := c.questions.Withdraw(ctx, connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
		Question: ref(q3), WishId: wish,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := later(q3, false); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("later on a withdrawn question = %v, want FailedPrecondition", err)
	}
}
