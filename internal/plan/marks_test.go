package plan

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func TestRecommended(t *testing.T) {
	two := []string{"Olive — the classic", "Paraffin — brighter"}
	cases := []struct {
		options []string
		rec     string
		want    planv1.Choice
		ok      bool
	}{
		{nil, "", planv1.Choice_CHOICE_YES, true},
		{two, "**A**, for the smell.", planv1.Choice_CHOICE_A, true},
		{two, "B: brighter.", planv1.Choice_CHOICE_B, true},
		{two, "Option B — it lasts.", planv1.Choice_CHOICE_B, true},
		{two, "B", planv1.Choice_CHOICE_B, true},
		{two, "Paraffin, it lasts.", planv1.Choice_CHOICE_B, true},
		// A letter beyond the options, an article, or nothing names no option.
		{two, "C: neither.", 0, false},
		{two, "A good question: both work.", 0, false},
		{two, "", 0, false},
		{[]string{"Yes, pour it", "Yes, but later"}, "Yes.", 0, false},
	}
	for _, c := range cases {
		got, ok := Recommended(&planv1.Question{Options: c.options, Recommendation: c.rec})
		if got != c.want || ok != c.ok {
			t.Errorf("Recommended(%q, %q) = %v, %v; want %v, %v", c.options, c.rec, got, ok, c.want, c.ok)
		}
	}
}

// TestMarks: a question is marked read, the mark replaced or taken off, and approving an open question answers it
// with its recommendation, as an answer does. A block takes no mark: blocks are for agents. The brief lists the marks.
func TestMarks(t *testing.T) {
	ctx := t.Context()
	var answered []string
	c := serve(t, WithAnswered(func(_ context.Context, q *planv1.Question) string {
		answered = append(answered, q.GetCode())
		return ""
	}))
	wish := c.wish(t)
	res, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", Options: []string{"Olive", "Paraffin"}, WishId: wish, Recommendation: "B: brighter.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := res.Msg.GetQuestion()
	vague := c.ask(t, wish, "sqlite", "files")
	block, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "decision", Title: "Ship on Friday", Content: "Unless it rains.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	put := func(target *planv1.MarkTarget, kind planv1.MarkKind, remove bool) (*planv1.Marked, error) {
		res, err := c.marks.Put(ctx, connect.NewRequest(&planv1.MarkServicePutRequest{
			Target: target, Kind: kind, Remove: remove, WishId: wish,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetMarked(), nil
	}
	byCode := func(code string) *planv1.MarkTarget {
		return &planv1.MarkTarget{Ref: &planv1.MarkTarget_Code{Code: code}}
	}
	byID := func(id string) *planv1.MarkTarget { return &planv1.MarkTarget{Ref: &planv1.MarkTarget_Id{Id: id}} }

	// Approving the open question answers it with B, and wakes what waits for an answer.
	got, err := put(byCode("Q01"), planv1.MarkKind_MARK_KIND_APPROVED, false)
	if err != nil || got.GetQuestionId() != q.GetId() || got.GetLabel() != "Q01" || got.GetMark().GetActor() != actor {
		t.Fatalf("approve Q01 = %v, %v", got, err)
	}
	list, err := c.questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	if a := list.Msg.GetQuestions()[0].GetAnswer(); a.GetChoice() != planv1.Choice_CHOICE_B {
		t.Errorf("Q01's answer after approval = %v, want B", a)
	}
	if strings.Join(answered, ",") != "Q01" {
		t.Errorf("answered hooks = %v, want Q01", answered)
	}
	// A recommendation that names no option cannot be approved in one click.
	if _, err := put(byID(vague.GetId()), planv1.MarkKind_MARK_KIND_APPROVED, false); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("approve a vague recommendation: %v, want failed_precondition", err)
	}
	// Read twice is one mark; then the read mark comes off Q01, and Q02 is read.
	for range 2 {
		if _, err := put(byCode("Q01"), planv1.MarkKind_MARK_KIND_READ, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := put(byCode("Q01"), planv1.MarkKind_MARK_KIND_READ, true); err != nil {
		t.Fatal(err)
	}
	if _, err := put(byID(vague.GetId()), planv1.MarkKind_MARK_KIND_READ, false); err != nil {
		t.Fatal(err)
	}
	// A block is neither read nor approved, and keeps no mark.
	blockID := block.Msg.GetBlock().GetId()
	for _, kind := range []planv1.MarkKind{planv1.MarkKind_MARK_KIND_READ, planv1.MarkKind_MARK_KIND_APPROVED} {
		if _, err := put(byID(blockID), kind, false); code(err) != connect.CodeFailedPrecondition {
			t.Errorf("mark a block %s: %v, want failed_precondition", markWord(kind), err)
		}
	}
	if b, err := store.Get[*planv1.Block](ctx, c.store, blockID); err != nil || len(b.GetMarks()) > 0 {
		t.Errorf("the block's marks = %v, %v; want none", b.GetMarks(), err)
	}
	if _, err := put(byID(wish), planv1.MarkKind_MARK_KIND_READ, false); code(err) != connect.CodeNotFound {
		t.Errorf("mark a wish's id: %v, want not_found", err)
	}

	marks, err := c.marks.List(ctx, connect.NewRequest(&planv1.MarkServiceListRequest{WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, m := range marks.Msg.GetMarks() {
		seen = append(seen, m.GetLabel()+" "+markWord(m.GetMark().GetKind()))
	}
	if want := "Q01 approved," + vague.GetCode() + " read"; strings.Join(seen, ",") != want {
		t.Errorf("marks = %v, want %s", seen, want)
	}

	brief, err := BuildBrief(ctx, c.store, "", wish)
	if err != nil {
		t.Fatal(err)
	}
	text := brief.Text()
	for _, want := range []string{
		"## Marked by the developer", "- **approved** Q01 Which oil?", "- **read** " + vague.GetCode() + " ",
		"`djinn mark list <wish>`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("brief lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "block Ship on Friday") {
		t.Errorf("the brief lists a mark on a block:\n%s", text)
	}
	// The latest first.
	if strings.Index(text, "- **read** "+vague.GetCode()) > strings.Index(text, "- **approved** Q01") {
		t.Errorf("the brief's marks are not the latest first:\n%s", text)
	}
}
