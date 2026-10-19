package plan

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// sendFixture is a wish with a tilasm, L01, and what sends its comments.
type sendFixture struct {
	c      clients
	wish   string
	tilasm *planv1.Tilasm
}

func newSendFixture(t *testing.T, opts ...Option) sendFixture {
	t.Helper()
	c := serve(t, append([]Option{WithHome(t.TempDir())}, opts...)...)
	wish := c.wish(t)
	src := folder(t, map[string]string{"index.html": page("Notifications of the lead", "Notices group.")})
	tilasm := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish}).GetTilasm()
	return sendFixture{c: c, wish: wish, tilasm: tilasm}
}

func (f sendFixture) draft(t *testing.T, text string, anchor *planv1.TilasmAnchor) {
	t.Helper()
	_, err := f.c.tilasms.PutComment(t.Context(), connect.NewRequest(&planv1.TilasmServicePutCommentRequest{
		TilasmId: f.tilasm.GetId(), Text: text, Anchor: anchor,
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func (f sendFixture) send(t *testing.T, choice planv1.SendChoice, text string) (*planv1.TilasmServiceSendCommentsResponse, error) {
	t.Helper()
	res, err := f.c.tilasms.SendComments(t.Context(), connect.NewRequest(&planv1.TilasmServiceSendCommentsRequest{
		TilasmId: f.tilasm.GetId(), Choice: choice, Text: text,
	}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

// hold is how the window reads the tilasm's hold: with its drafts.
func (f sendFixture) hold(t *testing.T) *planv1.TilasmHold {
	t.Helper()
	res, err := f.c.tilasms.Drafts(t.Context(), connect.NewRequest(&planv1.TilasmServiceDraftsRequest{Wish: f.wish}))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Msg.GetHolds() {
		if h.GetTilasmId() == f.tilasm.GetId() {
			return h
		}
	}
	return nil
}

// ask asks a question of the wish that links the tilasm, recommending A.
func (f sendFixture) ask(t *testing.T) *planv1.Question {
	t.Helper()
	res, err := f.c.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: f.wish, Text: "Which flow?", Options: []string{"Drafts, one send", "A review"},
		Context: "The design: " + TilasmLink(f.tilasm.GetId()), Recommendation: "A: one send, one event.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetQuestion()
}

// taskOn stores a task of the wish with prompt and status, as a spawn would.
func (f sendFixture) taskOn(t *testing.T, code, prompt string, status planv1.TaskStatus) *planv1.Task {
	t.Helper()
	task := &planv1.Task{Id: store.NewID(), WishId: f.wish, Code: code, Title: "Task " + code, Prompt: prompt,
		Status: status, CreateTime: timestamppb.Now()}
	if strings.HasPrefix(code, "T") {
		task.Kind = planv1.TaskKind_TASK_KIND_AZIMA
	}
	err := f.c.store.Tx(t.Context(), func(tx *store.Tx) error {
		spawn := &planv1.TaskServiceSpawnRequest{WishId: f.wish, Title: task.GetTitle()}
		if err := tx.Journal(actor, planv1connect.TaskServiceSpawnProcedure, spawn); err != nil {
			return err
		}
		return tx.Put(task)
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// commentEvents are the comments events waiting for the lead.
func commentEvents(t *testing.T, s *store.Store) []*planv1.LeadEvent {
	t.Helper()
	events, err := waitingEvents(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.LeadEvent
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS {
			out = append(out, e)
		}
	}
	return out
}

// TestTilasmSendIterate: every draft and the comment written above the buttons leave in one send, one event whatever
// their number, with the choice; the digest keeps its line whole; the send holds the buttons until the lead reads it.
func TestTilasmSendIterate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSendFixture(t)
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, " "); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "L01 has no comment to iterate on") {
		t.Errorf("iterate without a comment = %v", err)
	}
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_UNSPECIFIED, "Hm."); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no choice = %v", err)
	}
	f.draft(t, "A paused wish: do its notices wait?", nil)
	f.draft(t, "Say two seconds.", &planv1.TilasmAnchor{Exact: "in less than two seconds", Label: "Grouping"})
	f.draft(t, "The arrow to the window is missing.", &planv1.TilasmAnchor{ElementId: "path", Label: "figure: The path"})
	if events := commentEvents(t, f.c.store); len(events) != 0 {
		t.Fatalf("drafts made events: %v", events)
	}
	if h := f.hold(t); h != nil {
		t.Fatalf("a tilasm with drafts only is held: %v", h)
	}

	res, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, "  And say what a granted wish does.  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commentCodes(res.GetComments()), " "); got != "C1 C2 C3 C4" {
		t.Errorf("sent = %s, want C1 C2 C3 C4", got)
	}
	for _, c := range res.GetComments() {
		if c.GetState() != planv1.CommentState_COMMENT_STATE_SENT || c.GetSend() != 1 || c.GetSendTime() == nil {
			t.Errorf("a comment sent = %v", c)
		}
	}
	if whole := res.GetComments()[3]; whole.GetText() != "And say what a granted wish does." || whole.GetAnchor() != nil {
		t.Errorf("the comment above the buttons = %v, want one on the whole tilasm", whole)
	}
	if s := res.GetSend(); s.GetNumber() != 1 || s.GetChoice() != planv1.SendChoice_SEND_CHOICE_ITERATE || s.GetQuestionId() != "" {
		t.Errorf("send = %v", s)
	}
	events := commentEvents(t, f.c.store)
	if len(events) != 1 {
		t.Fatalf("events = %v, want one for the send", events)
	}
	want := "Djinn: L01 v1, 4 comments (2 general, 1 on a passage, 1 on an element), iterate: its next version with " +
		"them: djinn tilasm comments L01 --send 1"
	if e := events[0]; e.GetText() != want || e.GetCode() != "L01" || e.GetWishId() != f.wish || !DigestKeeps(e) {
		t.Errorf("event = %v\nwant %q", e, want)
	}
	lead := &planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: t.TempDir()}
	if err := f.c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/lead", lead); err != nil {
			return err
		}
		return tx.Put(lead)
	}); err != nil {
		t.Fatal(err)
	}
	digest, err := f.c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)}))
	if err != nil {
		t.Fatal(err)
	}
	if line := "- L01 [Try Djinn on itself] comments: v1, 4 comments (2 general, 1 on a passage, 1 on an element), iterate"; !strings.Contains(digest.Msg.GetText(), line) {
		t.Errorf("djinn lead watch --timeout 0s:\n%s\nwant %q", digest.Msg.GetText(), line)
	}
	drafts, err := f.c.tilasms.Drafts(ctx, connect.NewRequest(&planv1.TilasmServiceDraftsRequest{Wish: f.wish}))
	if err != nil || len(drafts.Msg.GetComments()) != 0 {
		t.Errorf("drafts after the send = %v, %v", drafts.Msg.GetComments(), err)
	}
	read, err := f.c.tilasms.Comments(ctx, connect.NewRequest(&planv1.TilasmServiceCommentsRequest{Tilasm: ref("L01"), Send: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Msg.GetText(), " · 4 comments · iterate: put its next version with them\n") {
		t.Errorf("djinn tilasm comments --send 1:\n%s", read.Msg.GetText())
	}

	// Until the lead reads it, the send holds the buttons, not the comments.
	if h := f.hold(t); h.GetReason() != planv1.TilasmHoldReason_TILASM_HOLD_REASON_SENT || h.GetSend() != 1 {
		t.Errorf("hold while the lead has not read = %v", h)
	}
	f.draft(t, "One more.", nil)
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "send 1 of L01 waits for the Djinn to read it") {
		t.Errorf("a send while one waits = %v", err)
	}
	take(t, f.c.store)
	if h := f.hold(t); h != nil {
		t.Errorf("hold once read = %v", h)
	}
	res, err = f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, "")
	if err != nil || res.GetSend().GetNumber() != 2 || strings.Join(commentCodes(res.GetComments()), " ") != "C5" {
		t.Errorf("the next send = %v, %v; want send 2 with C5", res, err)
	}
}

func TestTilasmSendLaunch(t *testing.T) {
	t.Parallel()
	// launch without a comment answers the open question that links the tilasm with
	// its recommended option, as its approval does, once: no comments event, and a second launch is refused.
	t.Run("without_comment", func(t *testing.T) {
		t.Parallel()
		var answered []string
		f := newSendFixture(t, WithAnswered(func(_ context.Context, q *planv1.Question) string {
			answered = append(answered, q.GetCode()+" "+q.GetAnswer().GetChoice().String())
			return ""
		}))
		other := f.c.ask(t, f.wish, "Yes", "No") // Links no tilasm.
		q := f.ask(t)
		res, err := f.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.GetAnswered() != q.GetCode() || len(res.GetComments()) != 0 || res.GetSend().GetQuestionId() != q.GetId() {
			t.Errorf("launch = %v, want %s answered", res, q.GetCode())
		}
		if strings.Join(answered, ",") != q.GetCode()+" CHOICE_A" {
			t.Errorf("answered = %v, want %s A once", answered, q.GetCode())
		}
		got, err := store.Get[*planv1.Question](t.Context(), f.c.store, q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if got.GetAnswer().GetChoice() != planv1.Choice_CHOICE_A || got.GetAnswer().GetActor() != planv1.Closer_CLOSER_DEVELOPER ||
			!hasMark(got, planv1.MarkKind_MARK_KIND_APPROVED) {
			t.Errorf("the question = %v, want approved as A", got)
		}
		if o, _ := store.Get[*planv1.Question](t.Context(), f.c.store, other.GetId()); o.GetAnswer() != nil {
			t.Errorf("a question that links no tilasm was answered: %v", o)
		}
		if events := commentEvents(t, f.c.store); len(events) != 0 {
			t.Errorf("a launch that answered makes no comments event: %v", events)
		}
		if h := f.hold(t); h.GetReason() != planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED || h.GetSend() != 1 || h.GetCode() != q.GetCode() {
			t.Errorf("hold = %v, want launched by send 1", h)
		}
		f.draft(t, "Too late?", nil)
		for _, choice := range []planv1.SendChoice{planv1.SendChoice_SEND_CHOICE_LAUNCH, planv1.SendChoice_SEND_CHOICE_ITERATE} {
			if _, err := f.send(t, choice, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
				!strings.Contains(err.Error(), "L01 was launched already, by send 1") {
				t.Errorf("%s after the launch = %v", choice, err)
			}
		}
		if len(answered) != 1 {
			t.Errorf("answered = %v, want once", answered)
		}
	})

	// launch with comments is one event that names the question, left open for the
	// lead; launch never goes twice, even two at once.
	t.Run("with_comments", func(t *testing.T) {
		t.Parallel()
		f := newSendFixture(t)
		q := f.ask(t)
		f.draft(t, "Keep the count in the button.", nil)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() { _, errs[i] = f.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH, "") })
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("two launches at once = %v, want one refused", errs)
		}
		for _, err := range errs {
			if err != nil && connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Errorf("the second launch = %v", err)
			}
		}
		events := commentEvents(t, f.c.store)
		want := "Djinn: L01 v1, 1 comment (1 general), launch " + q.GetCode() + " with them: djinn tilasm comments L01 --send 1"
		if len(events) != 1 || events[0].GetText() != want {
			t.Errorf("events = %v, want one: %q", events, want)
		}
		if got, _ := store.Get[*planv1.Question](t.Context(), f.c.store, q.GetId()); got.GetAnswer() != nil {
			t.Errorf("a launch with comments leaves the question to the lead: %v", got.GetAnswer())
		}
		take(t, f.c.store)
		if h := f.hold(t); h.GetReason() != planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED || h.GetCode() != q.GetCode() {
			t.Errorf("hold = %v, want launched", h)
		}
	})

	// a launch without a comment and without a question to answer tells the lead so.
	t.Run("no_question", func(t *testing.T) {
		t.Parallel()
		f := newSendFixture(t)
		res, err := f.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH, "")
		if err != nil || res.GetAnswered() != "" {
			t.Fatalf("launch = %v, %v", res, err)
		}
		want := "Djinn: L01 v1, launch its work: no comment, and no open question links it (djinn tilasm get L01)."
		if events := commentEvents(t, f.c.store); len(events) != 1 || events[0].GetText() != want {
			t.Errorf("events = %v, want %q", events, want)
		}
	})
}

// TestTilasmHolds: the server computes why a tilasm takes no send, from the tasks that run or are planned on it and
// the questions that link it; its put (--code) holds before its link.
func TestTilasmHolds(t *testing.T) {
	t.Parallel()
	t.Run("tasks", func(t *testing.T) {
		t.Parallel()
		f := newSendFixture(t)
		link := TilasmLink(f.tilasm.GetId())
		given := "Do it.\n\n" + tilasmContextHead + "\n## L01 · Notes\n\n- Link: " + link + "\n"
		reason := func() string {
			h := f.hold(t)
			if h == nil {
				return "free"
			}
			return strings.TrimPrefix(h.GetReason().String(), "TILASM_HOLD_REASON_") + " " + h.GetCode()
		}
		// Tasks that ended, an azima, and a task whose tilasm only quotes the put of L01: none holds it.
		f.taskOn(t, "W1", given, planv1.TaskStatus_TASK_STATUS_DONE)
		f.taskOn(t, "W2", "Put it: djinn tilasm put --code L01", planv1.TaskStatus_TASK_STATUS_CANCELLED)
		f.taskOn(t, "W3", "Other.\n\n"+tilasmContextHead+"\n## L02\n\n- Link: djinn://tilasm/x\n\ndjinn tilasm put --code L01\n",
			planv1.TaskStatus_TASK_STATUS_RUNNING)
		f.taskOn(t, "T4", given, planv1.TaskStatus_TASK_STATUS_PENDING)
		if got := reason(); got != "free" {
			t.Fatalf("hold = %s, want free", got)
		}
		f.taskOn(t, "W5", given, planv1.TaskStatus_TASK_STATUS_PENDING)
		if got := reason(); got != "WORK W5" {
			t.Errorf("hold = %s, want WORK W5", got)
		}
		f.taskOn(t, "W6", "Its next version, as L01 C2 says: djinn tilasm put <folder> --code l01."+given[len("Do it."):],
			planv1.TaskStatus_TASK_STATUS_RUNNING)
		if got := reason(); got != "REWORK W6" {
			t.Errorf("hold = %s, want REWORK W6", got)
		}
		f.draft(t, "Faster.", nil)
		if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_LAUNCH, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), "W6 puts the next version of L01") {
			t.Errorf("a launch while W6 reworks = %v", err)
		}
	})

	// a question that links the tilasm answered, and none open, launched it.
	t.Run("answered", func(t *testing.T) {
		t.Parallel()
		f := newSendFixture(t)
		q := f.ask(t)
		if h := f.hold(t); h != nil {
			t.Fatalf("an open question holds nothing: %v", h)
		}
		_, err := f.c.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: planv1.Choice_CHOICE_B,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if h := f.hold(t); h.GetReason() != planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED || h.GetCode() != q.GetCode() {
			t.Errorf("hold = %v, want launched by %s", h, q.GetCode())
		}
		again := f.ask(t)
		if h := f.hold(t); h != nil {
			t.Errorf("a new open question %s frees it: %v", again.GetCode(), h)
		}
	})
}

// TestSendLineFits: the line of a send stays whole in a digest, whatever the numbers.
func TestSendLineFits(t *testing.T) {
	t.Parallel()
	tilasm := &planv1.Tilasm{Code: "L123", Versions: []*planv1.TilasmVersion{{Number: 120}}}
	var comments []*planv1.TilasmComment
	for i := range 3000 {
		var a *planv1.TilasmAnchor
		switch i % 3 {
		case 1:
			a = &planv1.TilasmAnchor{Exact: "x"}
		case 2:
			a = &planv1.TilasmAnchor{Path: "p"}
		}
		comments = append(comments, &planv1.TilasmComment{Anchor: a})
	}
	q := &planv1.Question{Code: "Q999"}
	for _, choice := range []planv1.SendChoice{planv1.SendChoice_SEND_CHOICE_LAUNCH, planv1.SendChoice_SEND_CHOICE_ITERATE} {
		for _, question := range []*planv1.Question{q, nil} {
			for _, n := range []int{0, 1, 3000} {
				send := &planv1.TilasmSend{Number: 999, Choice: choice}
				e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS, Code: "L123",
					Text: SendLine(tilasm, send, comments[:n], question)}
				line := DigestLine(e, "")
				if !DigestKeeps(e) || utf8.RuneCountInString(strings.TrimPrefix(line, "L123 comments: ")) > digestLineMax {
					t.Errorf("%d comments, %s: the digest cuts %q", n, choice, line)
				}
			}
		}
	}
}

// TestTilasmSendChangesTheTilasms: a send, and a task on a tilasm, change the wish's tilasms, which the window reads
// again for their holds.
func TestTilasmSendChangesTheTilasms(t *testing.T) {
	t.Parallel()
	f := newSendFixture(t)
	s := watch(t, f.c.wishes, f.wish)
	f.taskOn(t, "W1", "Read "+TilasmLink(f.tilasm.GetId()), planv1.TaskStatus_TASK_STATUS_RUNNING)
	for msg := next(t, s); !hasChange(msg, planv1.Change_CHANGE_TILASM); msg = next(t, s) {
	}
}

func hasChange(msg *planv1.WishServiceWatchResponse, kind planv1.Change) bool {
	for _, c := range msg.GetChanges() {
		if c == kind {
			return true
		}
	}
	return false
}

// TestTilasmSendReads: the window reads when the Djinn read each send, from its comments event acknowledged; a send
// that waits is not read, and one whose event Djinn no longer keeps stays read, without a time.
func TestTilasmSendReads(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSendFixture(t)
	reads := func() []*planv1.TilasmSendRead {
		t.Helper()
		res, err := f.c.tilasms.Drafts(ctx, connect.NewRequest(&planv1.TilasmServiceDraftsRequest{Wish: f.wish}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetReads()
	}
	f.draft(t, "A paused wish: do its notices wait?", nil)
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, ""); err != nil {
		t.Fatal(err)
	}
	if got := reads(); len(got) != 0 {
		t.Fatalf("reads before the lead read = %v", got)
	}
	at := timestamppb.New(time.Date(2026, 10, 16, 18, 43, 0, 0, time.UTC))
	first := commentEvents(t, f.c.store)
	if err := f.c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/ack", &planv1.LeadServiceWatchRequest{}); err != nil {
			return err
		}
		for _, e := range first {
			e.AckSessionId, e.AckTime = session, at
			if err := tx.Put(e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := reads()
	if len(got) != 1 || got[0].GetTilasmId() != f.tilasm.GetId() || got[0].GetSend() != 1 ||
		!got[0].GetReadTime().AsTime().Equal(at.AsTime()) {
		t.Fatalf("reads once the lead read send 1 = %v", got)
	}

	f.draft(t, "One more.", nil)
	if _, err := f.send(t, planv1.SendChoice_SEND_CHOICE_ITERATE, ""); err != nil {
		t.Fatal(err)
	}
	if got := reads(); len(got) != 1 || got[0].GetSend() != 1 {
		t.Errorf("reads while send 2 waits = %v, want send 1 only", got)
	}
	// A week after it was read, Djinn drops the event: the send stays read.
	if err := f.c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/drop", &planv1.LeadServiceWatchRequest{}); err != nil {
			return err
		}
		return tx.Delete(first[0])
	}); err != nil {
		t.Fatal(err)
	}
	if got := reads(); len(got) != 1 || got[0].GetSend() != 1 || got[0].GetReadTime() != nil {
		t.Errorf("reads once the event of send 1 is gone = %v, want send 1 without a time", got)
	}
}

// TestTilasmSends: the server computes what an iterate send and a launch send without a comment do on each tilasm of a
// wish, from the one open question that links it, its mockup option within its rounds and its recommended option.
func TestTilasmSends(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newSendFixture(t)
	sends := func() *planv1.TilasmSends {
		t.Helper()
		res, err := f.c.tilasms.Drafts(ctx, connect.NewRequest(&planv1.TilasmServiceDraftsRequest{Wish: f.wish}))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range res.Msg.GetSends() {
			if s.GetTilasmId() == f.tilasm.GetId() {
				return s
			}
		}
		return nil
	}
	if got := sends(); got != nil {
		t.Fatalf("without an open question = %v, want nil", got)
	}
	asked, err := f.c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		WishId: f.wish, Text: "Do the typed options suit you?",
		Options:        []string{"Iterate the mockup", "Approve and build", "Other"},
		Context:        "The mockup: " + TilasmLink(f.tilasm.GetId()),
		Recommendation: "B: build it.",
		Plan: []string{
			"option: A\nkind: mockup\ntitle: Mockup v2\ntilasm: " + f.tilasm.GetCode() + "\nrounds: 3\n\nIterate.",
			"option: B\ntitle: Typed options\n\nBuild.",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := asked.Msg.GetQuestion()
	want := &planv1.TilasmSends{
		TilasmId: f.tilasm.GetId(), Question: q.GetCode(), IterateOption: "A", Round: 1, Rounds: 3,
		LaunchOption: "B", LaunchKind: "work", LaunchTitle: "Typed options",
	}
	if got := sends(); !proto.Equal(got, want) {
		t.Fatalf("with an open question = %v\nwant %v", got, want)
	}

	// At its last round, iterate goes to the lead; launch still answers with B.
	if err := f.c.store.Tx(ctx, func(tx *store.Tx) error {
		stored, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
		if err != nil {
			return err
		}
		stored.Rounds = []*planv1.Round{
			{Kind: planv1.RoundKind_ROUND_KIND_ITERATE},
			{Kind: planv1.RoundKind_ROUND_KIND_ITERATE},
		}
		if err := tx.Journal(actor, "test/rounds", stored); err != nil {
			return err
		}
		return tx.Put(stored)
	}); err != nil {
		t.Fatal(err)
	}
	wantLast := &planv1.TilasmSends{
		TilasmId: f.tilasm.GetId(), Question: q.GetCode(),
		LaunchOption: "B", LaunchKind: "work", LaunchTitle: "Typed options",
	}
	if got := sends(); !proto.Equal(got, wantLast) {
		t.Errorf("at the last round = %v\nwant %v", got, wantLast)
	}

	// Answered: no open question links the tilasm.
	if _, err := f.c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, Choice: planv1.Choice_CHOICE_B,
	})); err != nil {
		t.Fatal(err)
	}
	if got := sends(); got != nil {
		t.Errorf("after the question is answered = %v, want nil", got)
	}
}

// TestNoteMaxMatchesWindow: the 8000-character limit on notes and comments is the same in the proto, the digest and
// the window.
func TestNoteMaxMatchesWindow(t *testing.T) {
	t.Parallel()
	const want = 8000
	if digestNoteMax != want {
		t.Errorf("digestNoteMax = %d, want %d", digestNoteMax, want)
	}
	for _, field := range []struct {
		msg  protoreflect.Message
		name protoreflect.Name
	}{
		{(&planv1.Withdrawal{}).ProtoReflect(), "note"},
		{(&planv1.QuestionServiceEnlightenRequest{}).ProtoReflect(), "note"},
		{(&planv1.QuestionServiceAnswerRequest{}).ProtoReflect(), "note"},
		{(&planv1.TilasmComment{}).ProtoReflect(), "text"},
		{(&planv1.TilasmServiceSendCommentsRequest{}).ProtoReflect(), "text"},
		{(&planv1.TilasmServicePutCommentRequest{}).ProtoReflect(), "text"},
	} {
		fd := field.msg.Descriptor().Fields().ByName(field.name)
		rules, _ := proto.GetExtension(fd.Options(), validate.E_Field).(*validate.FieldRules)
		if got := rules.GetString().GetMaxLen(); got != want {
			t.Errorf("%s.%s max_len = %d, want %d", field.msg.Descriptor().Name(), field.name, got, want)
		}
	}
	src, err := os.ReadFile("../../src/data/format.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^export const NOTE_MAX = 8000;$`).Match(src) {
		t.Errorf("src/data/format.ts lacks export const NOTE_MAX = %d", want)
	}
}
