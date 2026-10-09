package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// fakeNotifier records the notifications instead of showing them.
type fakeNotifier chan Notification

func (f fakeNotifier) Notify(n Notification) error { f <- n; return nil }

// next is the next notification shown, or fails after a while.
func (f fakeNotifier) next(t *testing.T) Notification {
	t.Helper()
	select {
	case n := <-f:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return Notification{}
	}
}

func newNotices(t *testing.T, language string) (*Notices, fakeNotifier) {
	t.Helper()
	db, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	n := &Notices{Store: db, Language: language}
	notes := make(fakeNotifier, 8)
	n.Use(notes)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	// Run listens to the store once it runs: a first write waits for it.
	for !n.listening() {
		time.Sleep(time.Millisecond)
	}
	return n, notes
}

// listening tells that Run follows the store.
func (n *Notices) listening() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.asked != nil
}

func makeWish(t *testing.T, db *store.Store, title string, state planv1.WishState) *planv1.Wish {
	t.Helper()
	w := &planv1.Wish{Id: store.NewID(), Title: title, State: state}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, w) }); err != nil {
		t.Fatal(err)
	}
	return w
}

func ask(t *testing.T, db *store.Store, q *planv1.Question) *planv1.Question {
	t.Helper()
	if q.Id == "" {
		q.Id = store.NewID()
	}
	if q.CreateTime == nil {
		q.CreateTime = timestamppb.Now()
	}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", "ask", q), plan.Ask(t.Context(), tx, q))
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestNoticesShowTheQuestionsOfActiveWishes(t *testing.T) {
	n, notes := newNotices(t, "en")
	db := n.Store
	active := makeWish(t, db, "Ship the release", planv1.WishState_WISH_STATE_ACTIVE)
	paused := makeWish(t, db, "Later", planv1.WishState_WISH_STATE_PAUSED)

	// Not news: a question of a paused wish, one imported with its old date, and a decision.
	ask(t, db, &planv1.Question{WishId: paused.GetId(), Text: "Paused?"})
	ask(t, db, &planv1.Question{WishId: active.GetId(), Text: "Imported?", CreateTime: timestamppb.New(time.Now().Add(-time.Hour))})
	ask(t, db, &planv1.Question{WishId: active.GetId(), Text: "Decided?", Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}})

	q := ask(t, db, &planv1.Question{WishId: active.GetId(), Text: "Which database?", Options: []string{"SQLite", "Postgres"}})
	got := notes.next(t)
	if got.Title != "Question Q03 · Ship the release" || got.Body != "Which database?\nA. SQLite\nB. Postgres" ||
		got.WishID != active.GetId() || got.QuestionID != q.GetId() || got.ID != q.GetId() {
		t.Errorf("notification = %+v", got)
	}
	if !slices.Equal(got.Actions, []Action{{"a", "A"}, {"b", "B"}}) {
		t.Errorf("actions = %v", got.Actions)
	}

	// Answered, the question is stored again: no second notification. The next question has one.
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_A}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, q) }); err != nil {
		t.Fatal(err)
	}
	ask(t, db, &planv1.Question{WishId: active.GetId(), Text: "Release now?"})
	if got := notes.next(t); got.Title != "Question Q04 · Ship the release" || !slices.Equal(got.Actions, []Action{{"yes", "Yes"}}) {
		t.Errorf("second notification = %+v", got)
	}
	select {
	case extra := <-notes:
		t.Errorf("unexpected notification %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNoticesTranslateAndClip(t *testing.T) {
	n, notes := newNotices(t, "fr")
	w := makeWish(t, n.Store, "Ship", planv1.WishState_WISH_STATE_UNSPECIFIED)
	long := strings.Repeat("word ", 200)
	ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: long})
	got := notes.next(t)
	if got.Actions[0].Title != locales.T("fr", "notify.yes", nil) || got.Actions[0].Title == "Yes" || len([]rune(got.Body)) > maxBody {
		t.Errorf("notification = %+v (%d characters)", got, len([]rune(got.Body)))
	}
}

func TestNoticesWithoutNotifier(t *testing.T) {
	n, notes := newNotices(t, "en")
	n.Use(nil)
	w := makeWish(t, n.Store, "Quiet", planv1.WishState_WISH_STATE_ACTIVE)
	ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: "Anyone?"})
	n.Use(notes)
	ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: "Now?"})
	if got := notes.next(t); got.Body != "Now?" {
		t.Errorf("first notification shown = %+v", got)
	}
}

func TestNoticesShowNewInboxItems(t *testing.T) {
	n, notes := newNotices(t, "en")
	db := n.Store
	receive := func(item *planv1.InboxItem) *planv1.InboxItem {
		t.Helper()
		item.Id, item.Key = store.NewID(), store.NewID()
		if item.CreateTime == nil {
			item.CreateTime = timestamppb.Now()
		}
		if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, item) }); err != nil {
			t.Fatal(err)
		}
		return item
	}

	// Not news: an item dismissed, one routed, and one of an hour ago.
	receive(&planv1.InboxItem{Source: "babysit-mr", Text: "Dismissed", State: planv1.InboxState_INBOX_STATE_DISMISSED})
	receive(&planv1.InboxItem{Source: "babysit-mr", Text: "Routed", State: planv1.InboxState_INBOX_STATE_ROUTED})
	receive(&planv1.InboxItem{Source: "babysit-mr", Text: "Old", State: planv1.InboxState_INBOX_STATE_NEW,
		CreateTime: timestamppb.New(time.Now().Add(-time.Hour))})

	item := receive(&planv1.InboxItem{
		Source: "babysit-mr", State: planv1.InboxState_INBOX_STATE_NEW,
		Text: "Babysit !12 · Fix the wick\nhttps://gitlab.example.com/acme/gong/-/merge_requests/12",
	})
	got := notes.next(t)
	if got.Title != "Inbox · babysit-mr" || got.Body != item.GetText() || got.ID != item.GetId() ||
		got.WishID != "" || got.QuestionID != "" || len(got.Actions) != 0 {
		t.Errorf("notification = %+v", got)
	}

	// Dismissed, the item is stored again: no second notification.
	item.State = planv1.InboxState_INBOX_STATE_DISMISSED
	if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, item) }); err != nil {
		t.Fatal(err)
	}
	select {
	case extra := <-notes:
		t.Errorf("unexpected notification %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNoticesRespond(t *testing.T) {
	var shown []string
	type answer struct {
		id     string
		choice planv1.Choice
	}
	var answers []answer
	fail := false
	n := &Notices{
		Show: func(wishID string) { shown = append(shown, wishID) },
		Answer: func(_ context.Context, id string, c planv1.Choice) error {
			answers = append(answers, answer{id, c})
			if fail {
				return errors.New("already answered")
			}
			return nil
		},
	}
	n.Respond(t.Context(), Response{WishID: "w1", QuestionID: "q1"})
	n.Respond(t.Context(), Response{WishID: "w1", QuestionID: "q1", Action: "b"})
	n.Respond(t.Context(), Response{WishID: "w2", QuestionID: "q2", Action: "yes"})
	fail = true
	n.Respond(t.Context(), Response{WishID: "w3", QuestionID: "q3", Action: "a"})
	n.Respond(t.Context(), Response{WishID: "w4", QuestionID: "q4", Action: "unknown"})
	if !slices.Equal(shown, []string{"w1", "w3", "w4"}) {
		t.Errorf("shown = %v: want a click, a failed answer and an unknown button", shown)
	}
	want := []answer{{"q1", planv1.Choice_CHOICE_B}, {"q2", planv1.Choice_CHOICE_YES}, {"q3", planv1.Choice_CHOICE_A}}
	if !slices.Equal(answers, want) {
		t.Errorf("answers = %v, want %v", answers, want)
	}
}

// putJournaled stores m, journaled as a test's command.
func putJournaled(tx *store.Tx, m proto.Message) error {
	if err := tx.Journal("test", "put", m); err != nil {
		return err
	}
	return tx.Put(m)
}
