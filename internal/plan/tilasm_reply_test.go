package plan

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func (f sendFixture) reply(t *testing.T, m *planv1.TilasmServiceReplyRequest) (*planv1.TilasmServiceReplyResponse, error) {
	t.Helper()
	if m.GetTilasm() == nil {
		m.Tilasm = ref("L01")
	}
	res, err := f.c.tilasms.Reply(t.Context(), connect.NewRequest(m))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// TestTilasmReply: the Djinn replies to a sent comment with what it did and the tasks it made of it, which the
// comments' reading and the window's cards show; a draft, a comment or a task that is not there, and an empty reply are
// refused; a second reply replaces the first.
func TestTilasmReply(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSendFixture(t)
	f.draft(t, "A paused wish: do its notices wait?", nil)
	f.draft(t, "Say two seconds.", &planv1.TilasmAnchor{Exact: "in less than two seconds", Label: "Grouping"})
	if _, err := f.reply(t, &planv1.TilasmServiceReplyRequest{Comment: "C1", Text: "Done."}); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "L01 C1 is a draft: the developer has not sent it") {
		t.Errorf("a reply to a draft = %v", err)
	}
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, ""); err != nil {
		t.Fatal(err)
	}
	w60 := f.taskOn(t, "W60", "Put L01's next version: djinn tilasm put … --code L01", planv1.TaskStatus_TASK_STATUS_PENDING)
	t61 := f.taskOn(t, "T61", "An azima.", planv1.TaskStatus_TASK_STATUS_PENDING)
	for _, bad := range []struct {
		m    *planv1.TilasmServiceReplyRequest
		code connect.Code
		says string
	}{
		{&planv1.TilasmServiceReplyRequest{Comment: "C1", Text: "  "}, connect.CodeInvalidArgument, "a reply says"},
		{&planv1.TilasmServiceReplyRequest{Comment: "C9", Text: "Done."}, connect.CodeNotFound, "L01 has no comment C9"},
		{&planv1.TilasmServiceReplyRequest{Comment: "C1", Text: "Done.", Task: []string{"W99"}}, connect.CodeInvalidArgument, "W99"},
		{&planv1.TilasmServiceReplyRequest{Tilasm: ref("L02"), Comment: "C1", Text: "Done."}, connect.CodeNotFound, "no tilasm L02"},
	} {
		t.Run(bad.says, func(t *testing.T) {
			if _, err := f.reply(t, bad.m); connect.CodeOf(err) != bad.code || !strings.Contains(err.Error(), bad.says) {
				t.Errorf("reply %v = %v, want %v saying %q", bad.m, err, bad.code, bad.says)
			}
		})
	}

	res, err := f.reply(t, &planv1.TilasmServiceReplyRequest{
		Comment: "c1", Text: "W60 says it in the next version.", Task: []string{"w60", w60.GetId(), "T61"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.GetComment().GetReply()
	if r.GetText() != "W60 says it in the next version." || r.GetAuthor() != "lead" || r.GetCreateTime() == nil ||
		!slices.Equal(r.GetTaskIds(), []string{w60.GetId(), t61.GetId()}) {
		t.Errorf("reply = %v", r)
	}
	if res.GetComment().GetState() != planv1.CommentState_COMMENT_STATE_SENT || res.GetUnanswered() != 1 {
		t.Errorf("after C1's reply: %v, %d unanswered; want sent, 1", res.GetComment(), res.GetUnanswered())
	}
	read, err := f.c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("L01")}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  Reply from lead, tasks W60 T61: W60 says it in the next version.\n",
		"\n1 without a reply. Reply to each: djinn tilasm reply L01 <C> --text \"…\" [--task W60]\n",
	} {
		if !strings.Contains(read.Msg.GetText(), want) {
			t.Errorf("djinn tilasm comments lacks %q:\n%s", want, read.Msg.GetText())
		}
	}

	res, err = f.reply(t, &planv1.TilasmServiceReplyRequest{Comment: "C2", Text: "Two seconds, from the watch.", Author: "W60"})
	if err != nil || res.GetUnanswered() != 0 || res.GetComment().GetReply().GetAuthor() != "W60" {
		t.Errorf("C2's reply = %v, %v", res, err)
	}
	if read, err = f.c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("L01")})); err != nil ||
		strings.Contains(read.Msg.GetText(), "without a reply") {
		t.Errorf("every comment replied:\n%s\n%v", read.Msg.GetText(), err)
	}

	// A second reply replaces the first, its tasks too.
	if res, err = f.reply(t, &planv1.TilasmServiceReplyRequest{Comment: "C1", Text: "Not needed: a paused wish waits."}); err != nil ||
		res.GetComment().GetReply().GetText() != "Not needed: a paused wish waits." || len(res.GetComment().GetReply().GetTaskIds()) != 0 {
		t.Errorf("a second reply = %v, %v", res, err)
	}

	// The window reads the sent comments with their replies, beside the drafts.
	f.draft(t, "One more.", nil)
	drafts, err := f.c.tilasms.Drafts(ctx, connect.NewRequest(&planv1.TilasmServiceDraftsRequest{Wish: f.wish}))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commentCodes(drafts.Msg.GetComments()), " "); got != "C3" {
		t.Errorf("drafts = %s, want C3", got)
	}
	sent := drafts.Msg.GetSent()
	if got := strings.Join(commentCodes(sent), " "); got != "C1 C2" || sent[0].GetReply() == nil || sent[1].GetReply() == nil {
		t.Errorf("sent = %v, want C1 and C2 with their replies", sent)
	}
}

// TestBriefUnansweredComments: the wish's brief has a line for a tilasm whose sent comments wait for a reply, saying
// how many and which, gone once each has one; a draft never shows in it. The rules tell the lead to reply to each.
func TestBriefUnansweredComments(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	f := newSendFixture(t, WithHome(home))
	brief := func() string {
		t.Helper()
		b, err := BuildBrief(t.Context(), f.c.store, home, f.wish)
		if err != nil {
			t.Fatal(err)
		}
		return b.Moving
	}
	f.draft(t, "A secret draft of the developer.", nil)
	if b := brief(); strings.Contains(b, "comment") || strings.Contains(b, "secret draft") {
		t.Errorf("a draft shows in the brief:\n%s", b)
	}
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, "Say two seconds."); err != nil {
		t.Fatal(err)
	}
	f.draft(t, "Another draft, kept.", nil)
	line := func(want string) {
		t.Helper()
		b := brief()
		if want == "" {
			if strings.Contains(b, "without a reply") {
				t.Errorf("the brief still names comments without a reply:\n%s", b)
			}
		} else if !strings.Contains(b, want) {
			t.Errorf("the brief lacks %q:\n%s", want, b)
		}
		if strings.Contains(b, "draft") {
			t.Errorf("the brief names a draft:\n%s", b)
		}
	}
	line("\n- L01: 2 comments sent, 2 without a reply (C1, C2): reply to each, `djinn tilasm reply L01 <C> --text \"…\"`.\n")
	if _, err := f.reply(t, &planv1.TilasmServiceReplyRequest{Comment: "C2", Text: "Said."}); err != nil {
		t.Fatal(err)
	}
	line("\n- L01: 2 comments sent, 1 without a reply (C1): reply to each")
	if _, err := f.reply(t, &planv1.TilasmServiceReplyRequest{Comment: "C1", Text: "A paused wish waits."}); err != nil {
		t.Fatal(err)
	}
	line("")

	rules := StableBrief(home, nil)
	for _, want := range []string{
		"Then reply to every comment, `djinn tilasm reply L01 C3 --text \"…\" --task W60`", "*Launch*: turn the comments into the planned work",
		"*Iterate*: a task that puts the tilasm's next version", "never a guess", "`djinn tilasm comments <code> --send <n>`",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("the rules lack %q", want)
		}
	}
}
