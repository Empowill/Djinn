package plan

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// next receives the next message of a Watch stream, or fails after a few seconds.
func next(t *testing.T, s *connect.ServerStreamForClient[planv1.WishServiceWatchResponse]) *planv1.WishServiceWatchResponse {
	t.Helper()
	got := make(chan *planv1.WishServiceWatchResponse, 1)
	go func() {
		if s.Receive() {
			got <- s.Msg()
		}
		close(got)
	}()
	select {
	case msg, ok := <-got:
		if !ok {
			t.Fatalf("the stream ended: %v", s.Err())
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
		return nil
	}
}

func watch(t *testing.T, c planv1connect.WishServiceClient, wishID string) *connect.ServerStreamForClient[planv1.WishServiceWatchResponse] {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := c.Watch(ctx, connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	if first := next(t, s); first.GetWishId() != "" || len(first.GetChanges()) != len(everything) {
		t.Fatalf("first message %v, want everything", first)
	}
	return s
}

func TestWatchSaysWhatChanged(t *testing.T) {
	c := serve(t)
	s := watch(t, c.wishes, "")
	wish := c.wish(t)
	if msg := next(t, s); msg.GetWishId() != wish || !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
		t.Fatalf("after make: %v", msg)
	}

	// A question changes the wish too: whether Djinn proposes to grant it follows its questions.
	if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: wish, Text: "Ship it?",
	})); err != nil {
		t.Fatal(err)
	}
	msg := next(t, s)
	if want := []planv1.Change{planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_QUESTION}; msg.GetWishId() != wish ||
		!slices.Equal(msg.GetChanges(), want) {
		t.Fatalf("after ask: %v, want %v", msg, want)
	}

	put, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish, Kind: "section", Title: "Notes", Content: "Hi",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("after put: %v", msg)
	}
	// A deleted block names its wish too.
	if _, err := c.blocks.Delete(t.Context(), connect.NewRequest(&planv1.BlockServiceDeleteRequest{
		Id: put.Msg.GetBlock().GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); msg.GetWishId() != wish || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_BLOCK}) {
		t.Fatalf("after delete: %v", msg)
	}

	if _, err := c.projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{
		Directory: t.TempDir(),
	})); err != nil {
		t.Fatal(err)
	}
	if msg := next(t, s); msg.GetWishId() != "" || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_PROJECT}) {
		t.Fatalf("after project add: %v", msg)
	}
}

func TestWatchOneWish(t *testing.T) {
	c := serve(t)
	mine, other := c.wish(t), c.wish(t)
	s := watch(t, c.wishes, mine)
	for _, wish := range []string{other, mine} {
		if _, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wish, Text: "Which one?",
		})); err != nil {
			t.Fatal(err)
		}
	}
	// The other wish's question never comes.
	if msg := next(t, s); msg.GetWishId() != mine {
		t.Fatalf("got %v, want only wish %s", msg, mine)
	}
}

func TestWatchCoalesces(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	s := watch(t, c.wishes, wish)
	// Many changes within one interval come as few messages, never one each.
	for range 20 {
		if _, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId: wish, Kind: "log", Title: "Line", Content: "x",
		})); err != nil {
			t.Fatal(err)
		}
	}
	received := 0
	deadline := time.After(2 * watchInterval)
	got := make(chan struct{}, 64)
	go func() {
		for s.Receive() {
			got <- struct{}{}
		}
	}()
	for {
		select {
		case <-got:
			received++
			continue
		case <-deadline:
		}
		break
	}
	if received == 0 || received >= 20 {
		t.Fatalf("%d messages for 20 changes", received)
	}
}

func TestWatchRefusesABadWish(t *testing.T) {
	c := serve(t)
	s, err := c.wishes.Watch(t.Context(), connect.NewRequest(&planv1.WishServiceWatchRequest{WishId: "not-a-uuid"}))
	if err == nil {
		for s.Receive() {
		}
		err = s.Err()
	}
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("got %v, want invalid argument", err)
	}
}
