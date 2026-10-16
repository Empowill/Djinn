package plan

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// putCommentProcedure is what the window will journal a comment by; no command writes one yet.
const putCommentProcedure = "/plan.v1.TilasmService/PutComment"

// putComments stores comments as the window will, each with a new identifier.
func putComments(t *testing.T, s *store.Store, comments ...*planv1.TilasmComment) {
	t.Helper()
	err := s.Tx(t.Context(), func(tx *store.Tx) error {
		for _, c := range comments {
			if c.GetId() == "" {
				c.Id = store.NewID()
			}
			if err := tx.Journal(actor, putCommentProcedure, c); err != nil {
				return err
			}
			if err := tx.Put(c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTilasmCommentRoundTrip: a comment comes back from the store as it went in, its anchor and its reply whole, after
// Djinn opens its database again.
func TestTilasmCommentRoundTrip(t *testing.T) {
	ctx := t.Context()
	file := filepath.Join(t.TempDir(), store.File)
	s, err := store.Open(ctx, file, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	at := timestamppb.New(time.Date(2026, 10, 16, 18, 42, 0, 0, time.UTC))
	want := &planv1.TilasmComment{
		TilasmId: store.NewID(), WishId: store.NewID(), Code: "C2", Text: "Two seconds is the watch's value: say so.",
		Anchor: &planv1.TilasmAnchor{
			Page: "index.html", Exact: "in less than two seconds leave together", Prefix: "Notices that come ",
			Suffix: ", in one line of the digest.", Start: 1200, End: 1239, ElementId: "grouping", Path: "body>section:nth-of-type(2)>p",
			Fingerprint: "Notices that come in less", Label: "Grouping", Status: planv1.AnchorStatus_ANCHOR_STATUS_CHANGED,
			StatusVersion: 4,
		},
		Version: 3, State: planv1.CommentState_COMMENT_STATE_SENT, Send: 1, CreateTime: at, UpdateTime: at, SendTime: at,
		Reply:   &planv1.CommentReply{Text: "Done.", Author: "lead", CreateTime: at, TaskIds: []string{store.NewID()}},
		Follows: "C1",
	}
	putComments(t, s, want)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = store.Open(ctx, file, Entities()...); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := store.Get[*planv1.TilasmComment](ctx, s, want.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("comment read again = %v\nwant %v", got, want)
	}
	listed, err := store.List[*planv1.TilasmComment](ctx, s, store.Where{"tilasm_id": want.GetTilasmId()})
	if err != nil || len(listed) != 1 || !proto.Equal(listed[0], want) {
		t.Errorf("by tilasm = %v, %v; want the comment", listed, err)
	}

	// Two comments of a tilasm do not share a code.
	err = s.Tx(ctx, func(tx *store.Tx) error {
		again := &planv1.TilasmComment{Id: store.NewID(), TilasmId: want.GetTilasmId(), WishId: want.GetWishId(), Code: "c2"}
		if err := tx.Journal(actor, putCommentProcedure, again); err != nil {
			return err
		}
		return tx.Put(again)
	})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("a second C2 = %v, want %v", err, store.ErrDuplicate)
	}
}

// TestTilasmComments: djinn tilasm comments reads the comments sent, send by send, each with what it is on; a draft
// never shows, in the text nor in the list.
func TestTilasmComments(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	w60 := tasks(t, c, wish, "W60")["W60"]
	src := folder(t, map[string]string{"index.html": page("Notifications of the lead", "Notices group.")})
	put := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish})
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish, Code: "L01"}).GetTilasm()
	if tilasm.GetId() != put.GetTilasm().GetId() || len(tilasm.GetVersions()) != 2 {
		t.Fatalf("second put = %v", tilasm)
	}

	comments := func(send int32) (*planv1.TilasmServiceCommentsResponse, error) {
		res, err := c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("l01"), Send: send}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	none, err := comments(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "L01 · Notifications of the lead · version 2\nNo comment sent yet.\n"; none.GetText() != want || len(none.GetComments()) != 0 {
		t.Errorf("before any comment: %q, %v; want %q", none.GetText(), none.GetComments(), want)
	}

	first := timestamppb.New(time.Date(2026, 10, 16, 18, 42, 0, 0, time.UTC))
	second := timestamppb.New(time.Date(2026, 10, 16, 18, 50, 0, 0, time.UTC))
	sent := planv1.CommentState_COMMENT_STATE_SENT
	on := func(code string, send int32, version int32, text string) *planv1.TilasmComment {
		at := first
		if send == 2 {
			at = second
		}
		return &planv1.TilasmComment{
			TilasmId: tilasm.GetId(), WishId: wish, Code: code, Text: text, Version: version, State: sent, Send: send,
			CreateTime: first, SendTime: at,
		}
	}
	c1 := on("C1", 1, 2, "A paused wish: do its notices wait?")
	c2 := on("C2", 1, 1, "Two seconds is the watch's value:\nsay so.")
	c2.Anchor = &planv1.TilasmAnchor{
		Page: "index.html", Exact: "in less than two seconds leave together", Label: "Grouping",
		Status: planv1.AnchorStatus_ANCHOR_STATUS_CHANGED, StatusVersion: 2,
	}
	c2.Reply = &planv1.CommentReply{Text: "Done: two seconds, from the watch.", Author: "lead", TaskIds: []string{w60.GetId()}}
	c3 := on("C3", 2, 2, "The arrow to the window is missing.")
	c3.Anchor = &planv1.TilasmAnchor{Path: "body>figure", Fingerprint: "The path of a notice", Label: "figure: The path of a notice"}
	c3.Follows = "C2"
	c10 := on("C10", 2, 2, "And the window?")
	c10.Anchor = &planv1.TilasmAnchor{Page: "other.html", ElementId: "digest-table", Status: planv1.AnchorStatus_ANCHOR_STATUS_LOST, StatusVersion: 2}
	draft := &planv1.TilasmComment{
		TilasmId: tilasm.GetId(), WishId: wish, Code: "C4", Text: "A draft no agent reads.", Version: 2,
		State: planv1.CommentState_COMMENT_STATE_DRAFT, Anchor: &planv1.TilasmAnchor{Exact: "Notices group."},
	}
	// Stored out of order: the reading sorts by send, then by the number of the code (C10 after C3).
	putComments(t, c.store, c10, draft, c3, c2, c1)

	all, err := comments(0)
	if err != nil {
		t.Fatal(err)
	}
	want := `L01 · Notifications of the lead · version 2

send 1 · 2026-10-16 18:42 UTC · 2 comments

C1 on the whole tilasm
  A paused wish: do its notices wait?

C2 on a passage, Grouping, written on version 1, changed in version 2
  “in less than two seconds leave together”
  Two seconds is the watch's value:
  say so.
  Reply from lead, tasks W60: Done: two seconds, from the watch.

send 2 · 2026-10-16 18:50 UTC · 2 comments

C3 on an element, figure: The path of a notice, following up C2
  “The path of a notice”
  The arrow to the window is missing.

C10 on an element, #digest-table, page other.html, gone from version 2
  And the window?
`
	if all.GetText() != want {
		t.Errorf("text:\n%s\nwant:\n%s", all.GetText(), want)
	}
	if got := commentCodes(all.GetComments()); !slices.Equal(got, []string{"C1", "C2", "C3", "C10"}) {
		t.Errorf("comments = %v, want the four sent, by send then code", got)
	}
	if strings.Contains(all.GetText(), "C4") || strings.Contains(all.GetText(), draft.GetText()) {
		t.Errorf("the draft shows:\n%s", all.GetText())
	}

	one, err := comments(2)
	if err != nil {
		t.Fatal(err)
	}
	if got := commentCodes(one.GetComments()); !slices.Equal(got, []string{"C3", "C10"}) || strings.Contains(one.GetText(), "send 1") ||
		!strings.Contains(one.GetText(), "send 2 · 2026-10-16 18:50 UTC · 2 comments\n") {
		t.Errorf("--send 2: %v\n%s", got, one.GetText())
	}
	if _, err := comments(3); connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "L01 has no send 3: it has 1 to 2") {
		t.Errorf("--send 3 = %v, want not found, naming the sends", err)
	}

	// A wish deleted takes its comments along.
	if _, err := c.wishes.Delete(ctx, connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: wish})); err != nil {
		t.Fatal(err)
	}
	if left, err := store.List[*planv1.TilasmComment](ctx, c.store, store.Where{"wish_id": wish}); err != nil || len(left) != 0 {
		t.Errorf("after the wish's delete: %v, %v", left, err)
	}
}

// TestTilasmCommentsOnlyDrafts: a tilasm whose comments are all drafts reads as one with none sent, and --send names no
// send.
func TestTilasmCommentsOnlyDrafts(t *testing.T) {
	ctx := t.Context()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	src := folder(t, map[string]string{"index.html": page("Notes", "Text.")})
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish}).GetTilasm()
	putComments(t, c.store, &planv1.TilasmComment{
		TilasmId: tilasm.GetId(), WishId: wish, Code: "C1", Text: "Mine for now.", Version: 1,
		State: planv1.CommentState_COMMENT_STATE_DRAFT,
	})
	res, err := c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("L01")}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "L01 · Notes · version 1\nNo comment sent yet.\n"; res.Msg.GetText() != want || len(res.Msg.GetComments()) != 0 {
		t.Errorf("drafts only: %q, %v; want %q", res.Msg.GetText(), res.Msg.GetComments(), want)
	}
	_, err = c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("L01"), Send: 1}))
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "L01 has no comment sent") {
		t.Errorf("--send 1 = %v, want not found", err)
	}
}

// TestTilasmCommentChangesTheTilasms: a comment written or sent changes the wish's tilasms, which the window reads
// again.
func TestTilasmCommentChangesTheTilasms(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	s := watch(t, c.wishes, wish)
	putComments(t, c.store, &planv1.TilasmComment{
		TilasmId: store.NewID(), WishId: wish, Code: "C1", State: planv1.CommentState_COMMENT_STATE_DRAFT,
	})
	if msg := next(t, s); msg.GetWishId() != wish || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_TILASM}) {
		t.Fatalf("after a comment: %v", msg)
	}
}

func commentCodes(comments []*planv1.TilasmComment) []string {
	out := make([]string, len(comments))
	for i, c := range comments {
		out[i] = c.GetCode()
	}
	return out
}
