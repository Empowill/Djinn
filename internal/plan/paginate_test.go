package plan

import (
	"encoding/base64"
	"testing"

	"connectrpc.com/connect"
)

func TestPaginate(t *testing.T) {
	items := make([]int, 125)
	for i := range items {
		items[i] = i
	}

	// Default page size (50)
	page1, token1, total1, err := Paginate(items, 0, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 50 || page1[0] != 0 || page1[49] != 49 {
		t.Fatalf("page 1 unexpected items: len %d, first %d, last %d", len(page1), page1[0], page1[len(page1)-1])
	}
	if total1 != 125 {
		t.Errorf("page 1 total: got %d, want 125", total1)
	}
	if token1 == "" {
		t.Fatal("page 1 expected non-empty token")
	}

	// Page 2
	page2, token2, total2, err := Paginate(items, 50, token1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 50 || page2[0] != 50 || page2[49] != 99 {
		t.Fatalf("page 2 unexpected items: len %d, first %d, last %d", len(page2), page2[0], page2[len(page2)-1])
	}
	if total2 != 125 {
		t.Errorf("page 2 total: got %d, want 125", total2)
	}
	if token2 == "" {
		t.Fatal("page 2 expected non-empty token")
	}

	// Page 3 (last page)
	page3, token3, total3, err := Paginate(items, 50, token2)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3) != 25 || page3[0] != 100 || page3[24] != 124 {
		t.Fatalf("page 3 unexpected items: len %d, first %d, last %d", len(page3), page3[0], page3[len(page3)-1])
	}
	if total3 != 125 {
		t.Errorf("page 3 total: got %d, want 125", total3)
	}
	if token3 != "" {
		t.Errorf("page 3 expected empty next_page_token, got %q", token3)
	}

	// Offset past end
	tokenPast := base64.RawURLEncoding.EncodeToString([]byte("125"))
	pagePast, tokenPastNext, totalPast, err := Paginate(items, 50, tokenPast)
	if err != nil {
		t.Fatalf("page past: %v", err)
	}
	if len(pagePast) != 0 {
		t.Errorf("expected 0 items past end, got %d", len(pagePast))
	}
	if tokenPastNext != "" {
		t.Errorf("expected empty token past end, got %q", tokenPastNext)
	}
	if totalPast != 125 {
		t.Errorf("expected total 125 past end, got %d", totalPast)
	}

	// Invalid page tokens
	invalidTokens := []string{
		"not-base64!@#$",
		base64.RawURLEncoding.EncodeToString([]byte("not-a-number")),
		base64.RawURLEncoding.EncodeToString([]byte("-10")),
	}
	for _, tok := range invalidTokens {
		_, _, _, err := Paginate(items, 50, tok)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("token %q: want CodeInvalidArgument, got %v", tok, err)
		}
	}
}
