//go:build !windows

// Not on Windows: it starts djinn up through up(), of up_test.go.

package main

import (
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
)

// TestAnswerFromANotification: a button of a notification answers its question through the server, guarded by
// its token like any client.
func TestAnswerFromANotification(t *testing.T) {
	home := t.TempDir()
	_, addr := up(t, home, environ(home, t.TempDir()))
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	wish, err := planv1connect.NewWishServiceClient(httpClient, base).Make(ctx,
		connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Ship it"}))
	if err != nil {
		t.Fatal(err)
	}
	questions := planv1connect.NewQuestionServiceClient(httpClient, base)
	asked, err := questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: wish.Msg.GetWish().GetId(), Text: "Which one?", Options: []string{"this", "that"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := asked.Msg.GetQuestion().GetId()
	if err := answer(ctx, addr, id, planv1.Choice_CHOICE_B); err != nil {
		t.Fatal(err)
	}
	list, err := questions.List(ctx, connect.NewRequest(&planv1.QuestionServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Msg.GetQuestions()[0].GetAnswer().GetChoice(); got != planv1.Choice_CHOICE_B {
		t.Errorf("answer = %v, want B", got)
	}
	// A choice the question does not offer is refused: the window shows the question instead.
	if err := answer(ctx, addr, id, planv1.Choice_CHOICE_D); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("answer D of two options: %v", err)
	}
}
