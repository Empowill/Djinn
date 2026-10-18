package plan

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestWatchMethods verifies that every WishService and QuestionService mutation publishes its change to the watch stream.
func TestWatchMethods(t *testing.T) {
	restore := SetWatchInterval(time.Millisecond)
	t.Cleanup(restore)

	t.Run("wish", func(t *testing.T) {
		c := serve(t)
		sAll := watch(t, c.wishes, "")

		// 1. Make
		makeRes, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Wish Alpha"}))
		if err != nil {
			t.Fatal(err)
		}
		wishID := makeRes.Msg.GetWish().GetId()
		msg := next(t, sAll)
		if msg.GetWishId() != wishID || !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Make: got %v, want CHANGE_WISH for %s", msg, wishID)
		}

		sWish := watch(t, c.wishes, wishID)

		// 2. Rename
		if _, err := c.wishes.Rename(t.Context(), connect.NewRequest(&planv1.WishServiceRenameRequest{
			WishId: wishID, Title: "Wish Alpha Renamed",
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Rename: got %v, want CHANGE_WISH", msg)
		}

		// 3. Describe
		if _, err := c.wishes.Describe(t.Context(), connect.NewRequest(&planv1.WishServiceDescribeRequest{
			WishId: wishID, Text: "A detailed description",
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Describe: got %v, want CHANGE_WISH", msg)
		}

		// 4. SetIntegration
		if _, err := c.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId:       wishID,
			PushStrategy: planv1.PushStrategy_PUSH_STRATEGY_AZIMA.Enum(),
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("SetIntegration: got %v, want CHANGE_WISH", msg)
		}

		// 5. PushStrategy
		if _, err := c.wishes.PushStrategy(t.Context(), connect.NewRequest(&planv1.WishServicePushStrategyRequest{
			WishId:   wishID,
			Strategy: planv1.PushStrategy_PUSH_STRATEGY_WISH.Enum(),
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("PushStrategy: got %v, want CHANGE_WISH", msg)
		}

		// 6. Pause
		if _, err := c.wishes.Pause(t.Context(), connect.NewRequest(&planv1.WishServicePauseRequest{
			WishId: wishID,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Pause: got %v, want CHANGE_WISH", msg)
		}

		// 7. Activate
		if _, err := c.wishes.Activate(t.Context(), connect.NewRequest(&planv1.WishServiceActivateRequest{
			WishId: wishID,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sWish)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Activate: got %v, want CHANGE_WISH", msg)
		}

		// 8. Move
		makeRes2, err := c.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Wish Beta"}))
		if err != nil {
			t.Fatal(err)
		}
		wishID2 := makeRes2.Msg.GetWish().GetId()
		_ = next(t, sAll) // consume make for wish2

		if _, err := c.wishes.Move(t.Context(), connect.NewRequest(&planv1.WishServiceMoveRequest{
			WishId: wishID2, To: 1,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, sAll)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
			t.Fatalf("Move: got %v, want CHANGE_WISH", msg)
		}
	})

	t.Run("question", func(t *testing.T) {
		c := serve(t)
		wish1 := c.wish(t)
		wish2 := c.wish(t)

		s1 := watch(t, c.wishes, wish1)
		s2 := watch(t, c.wishes, wish2)

		// 1. Ask
		askRes, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wish1, Text: "Should we proceed?", Options: []string{"Yes", "No"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		q1 := askRes.Msg.GetQuestion()
		msg := next(t, s1)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Ask: got %v, want CHANGE_QUESTION", msg)
		}
		if got := msg.GetChanged().GetQuestions(); len(got) != 1 || got[0].GetId() != q1.GetId() {
			t.Fatalf("Ask: changed questions %v, want %s", got, q1.GetId())
		}

		// 2. Enlighten
		ref1 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q1.GetId()}}
		if _, err := c.questions.Enlighten(t.Context(), connect.NewRequest(&planv1.QuestionServiceEnlightenRequest{
			Question: ref1, Note: "Need more details", WishId: wish1,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, s1)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Enlighten: got %v, want CHANGE_QUESTION", msg)
		}

		// 3. Revise
		if _, err := c.questions.Revise(t.Context(), connect.NewRequest(&planv1.QuestionServiceReviseRequest{
			Question: ref1, WishId: wish1, Context: "Here are details", Recommendation: "A: Yes",
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, s1)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Revise: got %v, want CHANGE_QUESTION", msg)
		}

		// 4. Answer
		if _, err := c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: ref1, Choice: planv1.Choice_CHOICE_A,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, s1)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Answer: got %v, want CHANGE_QUESTION", msg)
		}

		// 5. Withdraw
		askRes2, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wish1, Text: "Cancel this?", Options: []string{"A", "B"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		q2 := askRes2.Msg.GetQuestion()
		_ = next(t, s1) // consume ask

		ref2 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q2.GetId()}}
		if _, err := c.questions.Withdraw(t.Context(), connect.NewRequest(&planv1.QuestionServiceWithdrawRequest{
			Question: ref2,
		})); err != nil {
			t.Fatal(err)
		}
		msg = next(t, s1)
		if !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Withdraw: got %v, want CHANGE_QUESTION", msg)
		}

		// 6. Move
		askRes3, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wish1, Text: "Move me to wish2?", Options: []string{"X", "Y"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		q3 := askRes3.Msg.GetQuestion()
		_ = next(t, s1) // consume ask

		ref3 := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q3.GetId()}}
		if _, err := c.questions.Move(t.Context(), connect.NewRequest(&planv1.QuestionServiceMoveRequest{
			Question: ref3, WishId: wish1, Wish: wish2,
		})); err != nil {
			t.Fatal(err)
		}

		// Wish 1 receives deletion
		msg1 := next(t, s1)
		if !slices.Contains(msg1.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Move source: got %v, want CHANGE_QUESTION", msg1)
		}
		if !slices.Contains(msg1.GetChanged().GetDeleted(), q3.GetId()) {
			t.Fatalf("Move source: deleted %v, want %s", msg1.GetChanged().GetDeleted(), q3.GetId())
		}

		// Wish 2 receives addition
		msg2 := next(t, s2)
		if !slices.Contains(msg2.GetChanges(), planv1.Change_CHANGE_QUESTION) {
			t.Fatalf("Move target: got %v, want CHANGE_QUESTION", msg2)
		}
		if got := msg2.GetChanged().GetQuestions(); len(got) == 0 || got[0].GetText() != "Move me to wish2?" {
			t.Fatalf("Move target: changed questions %v, want moved question", got)
		}
	})
}
