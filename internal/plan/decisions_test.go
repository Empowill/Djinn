package plan

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestIcon: a question and a block carry the emoji their writer gives, one emoji only; a block keeps its icon when
// another field changes.
func TestIcon(t *testing.T) {
	c := serve(t)
	wishID := c.wish(t)
	q, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which store?", WishId: wishID, Icon: "🔒",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Msg.GetQuestion().GetIcon(); got != "🔒" {
		t.Errorf("the question's icon is %q", got)
	}
	b, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wishID, Kind: "decision", Title: "SQLite", Content: "One file.", Icon: "🧱",
	}))
	if err != nil {
		t.Fatal(err)
	}
	b, err = c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wishID, Id: b.Msg.GetBlock().GetId(), Title: "SQLite, in WAL mode",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Msg.GetBlock().GetIcon(); got != "🧱" {
		t.Errorf("the block's icon is %q after a change of title", got)
	}
	for _, icon := range []string{"⚠️", "👩‍💻", "👍🏽", "🇫🇷", "🏴‍☠️"} {
		if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Which one?", WishId: wishID, Icon: icon,
		})); err != nil {
			t.Errorf("%q is one emoji: %v", icon, err)
		}
	}
	for _, icon := range []string{"a", "🔒🔒", "🔒 ", "1", "🇫", "→", "!", "́", "🔒🔒🔒🔒🔒🔒🔒🔒🔒"} {
		if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Which one?", WishId: wishID, Icon: icon,
		})); code(err) != connect.CodeInvalidArgument {
			t.Errorf("question icon %q: %v, want invalid argument", icon, err)
		}
		if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId: wishID, Kind: "decision", Icon: icon,
		})); code(err) != connect.CodeInvalidArgument {
			t.Errorf("block icon %q: %v, want invalid argument", icon, err)
		}
	}
}

// TestDecision: a task names the decision it comes from, an answered question by its code or id, or a decision block
// by its id; anything else is refused, and says why.
func TestDecision(t *testing.T) {
	c := serve(t)
	wishID, other := c.wish(t), c.wish(t)
	open := c.ask(t, wishID)
	q := c.ask(t, wishID)
	if _, err := c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: planv1.Choice_CHOICE_YES,
	})); err != nil {
		t.Fatal(err)
	}
	put := func(wish, kind string) string {
		res, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{WishId: wish, Kind: kind, Title: kind}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetBlock().GetId()
	}
	decision, section, elsewhere := put(wishID, "Decision"), put(wishID, "section"), put(other, "decision")
	for _, tt := range []struct{ ref, want string }{
		{q.GetCode(), q.GetCode()},
		{"q02", q.GetCode()},
		{q.GetId(), q.GetCode()},
		{decision, decision},
	} {
		got, err := Decision(t.Context(), c.store, wishID, tt.ref)
		if err != nil || got != tt.want {
			t.Errorf("Decision(%s) = %q, %v; want %q", tt.ref, got, err, tt.want)
		}
	}
	for _, tt := range []struct {
		ref  string
		code connect.Code
	}{
		{open.GetCode(), connect.CodeFailedPrecondition},
		{"Q99", connect.CodeInvalidArgument},
		{section, connect.CodeInvalidArgument},
		{elsewhere, connect.CodeInvalidArgument},
		{"01890000-0000-7000-8000-000000000000", connect.CodeInvalidArgument},
	} {
		if _, err := Decision(t.Context(), c.store, wishID, tt.ref); code(err) != tt.code {
			t.Errorf("Decision(%s): %v, want %v", tt.ref, err, tt.code)
		}
	}
}
