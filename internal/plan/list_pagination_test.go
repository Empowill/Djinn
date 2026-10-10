package plan

import (
	"fmt"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestQuestionListPagination(t *testing.T) {
	c := serve(t)
	wishID := c.wish(t)

	var askedIDs []string
	for i := range 55 {
		q, err := c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text:   fmt.Sprintf("Question %02d?", i),
			WishId: wishID,
		}))
		if err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
		askedIDs = append(askedIDs, q.Msg.GetQuestion().GetId())
	}

	// First page (default size 50)
	res1, err := c.questions.List(t.Context(), connect.NewRequest(&planv1.QuestionServiceListRequest{
		WishId: wishID,
	}))
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(res1.Msg.GetQuestions()) != 50 {
		t.Fatalf("got %d questions, want 50", len(res1.Msg.GetQuestions()))
	}
	if res1.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res1.Msg.GetTotal())
	}
	if res1.Msg.GetNextPageToken() == "" {
		t.Fatal("expected non-empty next_page_token on page 1")
	}

	// Verify order of first page
	for i, q := range res1.Msg.GetQuestions() {
		if q.GetId() != askedIDs[i] {
			t.Errorf("item %d: got id %s, want %s", i, q.GetId(), askedIDs[i])
		}
	}

	// Second page
	res2, err := c.questions.List(t.Context(), connect.NewRequest(&planv1.QuestionServiceListRequest{
		WishId:    wishID,
		PageToken: res1.Msg.GetNextPageToken(),
	}))
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(res2.Msg.GetQuestions()) != 5 {
		t.Fatalf("got %d questions, want 5", len(res2.Msg.GetQuestions()))
	}
	if res2.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res2.Msg.GetTotal())
	}
	if res2.Msg.GetNextPageToken() != "" {
		t.Errorf("expected empty next_page_token on last page, got %q", res2.Msg.GetNextPageToken())
	}

	// Verify order of second page
	for i, q := range res2.Msg.GetQuestions() {
		if q.GetId() != askedIDs[50+i] {
			t.Errorf("item %d: got id %s, want %s", 50+i, q.GetId(), askedIDs[50+i])
		}
	}

	// Invalid page token
	_, err = c.questions.List(t.Context(), connect.NewRequest(&planv1.QuestionServiceListRequest{
		WishId:    wishID,
		PageToken: "invalid-token-!@#$",
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("invalid token: got %v, want CodeInvalidArgument", err)
	}
}

func TestBlockListPagination(t *testing.T) {
	c := serve(t)
	wishID := c.wish(t)

	var blockIDs []string
	for i := range 55 {
		b, err := c.blocks.Put(t.Context(), connect.NewRequest(&planv1.BlockServicePutRequest{
			WishId:  wishID,
			Kind:    "note",
			Title:   fmt.Sprintf("Block %02d", i),
			Content: "content",
		}))
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		blockIDs = append(blockIDs, b.Msg.GetBlock().GetId())
	}

	// First page (default size 50)
	res1, err := c.blocks.List(t.Context(), connect.NewRequest(&planv1.BlockServiceListRequest{
		WishId: wishID,
	}))
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(res1.Msg.GetBlocks()) != 50 {
		t.Fatalf("got %d blocks, want 50", len(res1.Msg.GetBlocks()))
	}
	if res1.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res1.Msg.GetTotal())
	}
	if res1.Msg.GetNextPageToken() == "" {
		t.Fatal("expected non-empty next_page_token on page 1")
	}

	// Verify order of first page
	for i, b := range res1.Msg.GetBlocks() {
		if b.GetId() != blockIDs[i] {
			t.Errorf("item %d: got id %s, want %s", i, b.GetId(), blockIDs[i])
		}
	}

	// Second page
	res2, err := c.blocks.List(t.Context(), connect.NewRequest(&planv1.BlockServiceListRequest{
		WishId:    wishID,
		PageToken: res1.Msg.GetNextPageToken(),
	}))
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(res2.Msg.GetBlocks()) != 5 {
		t.Fatalf("got %d blocks, want 5", len(res2.Msg.GetBlocks()))
	}
	if res2.Msg.GetTotal() != 55 {
		t.Errorf("got total %d, want 55", res2.Msg.GetTotal())
	}
	if res2.Msg.GetNextPageToken() != "" {
		t.Errorf("expected empty next_page_token on last page, got %q", res2.Msg.GetNextPageToken())
	}

	// Verify order of second page
	for i, b := range res2.Msg.GetBlocks() {
		if b.GetId() != blockIDs[50+i] {
			t.Errorf("item %d: got id %s, want %s", 50+i, b.GetId(), blockIDs[50+i])
		}
	}

	// Invalid page token
	_, err = c.blocks.List(t.Context(), connect.NewRequest(&planv1.BlockServiceListRequest{
		WishId:    wishID,
		PageToken: "invalid-token-!@#$",
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Errorf("invalid token: got %v, want CodeInvalidArgument", err)
	}
}
