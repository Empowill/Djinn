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
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
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
	return n.changes != nil
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
	w := makeWish(t, n.Store, "Livrer", planv1.WishState_WISH_STATE_UNSPECIFIED)
	long := strings.Repeat("mot ", 200)
	ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: long})
	got := notes.next(t)
	if got.Actions[0].Title != "Oui" || len([]rune(got.Body)) > maxBody {
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

func TestNoticesRespond(t *testing.T) {
	var shown []string
	type answer struct {
		id     string
		choice planv1.Choice
	}
	var answers []answer
	fail := false
	n := &Notices{
		Show: func(wishID, target string) { shown = append(shown, wishID+"#"+target) },
		Answer: func(_ context.Context, id string, c planv1.Choice) error {
			answers = append(answers, answer{id, c})
			if fail {
				return errors.New("already answered")
			}
			return nil
		},
	}
	n.Respond(t.Context(), Response{WishID: "w1", QuestionID: "q1", Target: "question-q1"})
	n.Respond(t.Context(), Response{WishID: "w1", QuestionID: "q1", Action: "b"})
	n.Respond(t.Context(), Response{WishID: "w2", QuestionID: "q2", Action: "yes"})
	fail = true
	n.Respond(t.Context(), Response{WishID: "w3", QuestionID: "q3", Action: "a"})
	n.Respond(t.Context(), Response{WishID: "w4", QuestionID: "q4", Action: "unknown"})
	if !slices.Equal(shown, []string{"w1#question-q1", "w3#", "w4#"}) {
		t.Errorf("shown = %v: want a click, a failed answer and an unknown button", shown)
	}
	want := []answer{{"q1", planv1.Choice_CHOICE_B}, {"q2", planv1.Choice_CHOICE_YES}, {"q3", planv1.Choice_CHOICE_A}}
	if !slices.Equal(answers, want) {
		t.Errorf("answers = %v, want %v", answers, want)
	}
}

// none checks that no notification comes.
func (f fakeNotifier) none(t *testing.T) {
	t.Helper()
	select {
	case extra := <-f:
		t.Errorf("unexpected notification %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func put(t *testing.T, db *store.Store, m proto.Message) {
	t.Helper()
	if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, m) }); err != nil {
		t.Fatal(err)
	}
}

// TestNoticesAskPermission: the edit question of a task (ASKING) says which task asks to change which project, with
// yes and no as buttons; when its worker ends read-only and waits, the notification of the question is replaced.
func TestNoticesAskPermission(t *testing.T) {
	n, notes := newNotices(t, "fr")
	db := n.Store
	w := makeWish(t, db, "Livrer", planv1.WishState_WISH_STATE_ACTIVE)
	project := &planv1.Project{Id: store.NewID(), Name: "site"}
	put(t, db, project)
	q := &planv1.Question{
		Id: store.NewID(), WishId: w.GetId(), CreateTime: timestamppb.Now(),
		Text: "May the worker of task W1 change the files of site?", Options: []string{"Yes: …", "No: …"},
	}
	task := &planv1.Task{
		Id: store.NewID(), WishId: w.GetId(), ProjectId: project.GetId(), Code: "W1", Title: "Fix the footer",
		Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Access: planv1.TaskAccess_TASK_ACCESS_ASKING,
		EditQuestionId: q.GetId(),
	}
	// One transaction, as the harness writes them (prepare).
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", "spawn", task), plan.Ask(t.Context(), tx, q), tx.Put(task))
	}); err != nil {
		t.Fatal(err)
	}
	got := notes.next(t)
	if got.Title != "Autorisation attendue · W1 · Livrer" || !strings.Contains(got.Body, "W1") ||
		!strings.Contains(got.Body, "site") || got.ID != q.GetId() || got.QuestionID != q.GetId() ||
		got.Target != "question-"+q.GetId() || got.WishID != w.GetId() {
		t.Errorf("permission = %+v", got)
	}
	if !slices.Equal(got.Actions, []Action{{"a", "Oui"}, {"b", "Non"}}) {
		t.Errorf("actions = %v", got.Actions)
	}

	task.Status, task.EndTime = planv1.TaskStatus_TASK_STATUS_WAITING, timestamppb.Now()
	put(t, db, task)
	got = notes.next(t)
	if got.Title != "W1 attend votre réponse · Livrer" || got.ID != q.GetId() || got.QuestionID != q.GetId() {
		t.Errorf("waiting = %+v", got)
	}
	// The same task written again, waiting still: the same event, not shown again.
	put(t, db, task)
	notes.none(t)
}

func TestNoticesShowAFailedTaskOnce(t *testing.T) {
	n, notes := newNotices(t, "en")
	db := n.Store
	w := makeWish(t, db, "Ship it", planv1.WishState_WISH_STATE_ACTIVE)
	// Failed long ago, written again: not news.
	old := &planv1.Task{
		Id: store.NewID(), WishId: w.GetId(), Code: "W1", Title: "Old", Status: planv1.TaskStatus_TASK_STATUS_FAILED,
		EndTime: timestamppb.New(time.Now().Add(-time.Hour)),
	}
	put(t, db, old)
	task := &planv1.Task{
		Id: store.NewID(), WishId: w.GetId(), Code: "W2", Title: "Build", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
	}
	put(t, db, task)
	notes.none(t)
	task.Status, task.Error, task.EndTime = planv1.TaskStatus_TASK_STATUS_FAILED, "exit code 2", timestamppb.Now()
	put(t, db, task)
	got := notes.next(t)
	if got.Title != "W2 failed · Ship it" || got.Body != "Build\nexit code 2" || got.Target != "task-"+task.GetId() ||
		got.QuestionID != "" || len(got.Actions) != 0 {
		t.Errorf("failed = %+v", got)
	}
	put(t, db, task)
	notes.none(t)
}

func TestNoticesShowAWishReadyToReview(t *testing.T) {
	n, notes := newNotices(t, "en")
	db := n.Store
	// Ready before the window starts: not news.
	n.Use(nil)
	before := makeWish(t, db, "Before", planv1.WishState_WISH_STATE_ACTIVE)
	put(t, db, &planv1.Task{Id: store.NewID(), WishId: before.GetId(), Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE})
	n.Use(notes) // As the window does once it starts: it notes what is ready now.

	w := makeWish(t, db, "Ship it", planv1.WishState_WISH_STATE_ACTIVE)
	task := &planv1.Task{Id: store.NewID(), WishId: w.GetId(), Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}
	put(t, db, task)
	put(t, db, &planv1.Task{Id: store.NewID(), WishId: before.GetId(), Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_DONE})
	notes.none(t)
	task.Status = planv1.TaskStatus_TASK_STATUS_DONE
	put(t, db, task)
	got := notes.next(t)
	if got.Title != "Ready to review · Ship it" || got.WishID != w.GetId() || got.Target != "grant-"+w.GetId() {
		t.Errorf("ready = %+v", got)
	}
	put(t, db, task)
	notes.none(t)
}

// TestNoticesQuietOnTheWishInView: nothing for the wish the window shows while it is in front; the same event is
// not shown later either.
func TestNoticesQuietOnTheWishInView(t *testing.T) {
	n, notes := newNotices(t, "en")
	db := n.Store
	w := makeWish(t, db, "Ship it", planv1.WishState_WISH_STATE_ACTIVE)
	other := makeWish(t, db, "Other", planv1.WishState_WISH_STATE_ACTIVE)
	n.View(w.GetId())
	n.Focus(true)
	ask(t, db, &planv1.Question{WishId: w.GetId(), Text: "Seen?"})
	ask(t, db, &planv1.Question{WishId: other.GetId(), Text: "Elsewhere?"})
	if got := notes.next(t); got.Body != "Elsewhere?" {
		t.Errorf("notification = %+v, want the other wish's", got)
	}
	n.Focus(false)
	ask(t, db, &planv1.Question{WishId: w.GetId(), Text: "Away?"})
	if got := notes.next(t); got.Body != "Away?" {
		t.Errorf("notification = %+v, want the one asked while the window is behind", got)
	}
	notes.none(t)
}

// permitted is a notifier that needs the system's permission.
type permitted struct {
	fakeNotifier
	allowed, asked bool
}

func (p *permitted) Allowed() (bool, error) { return p.allowed, nil }
func (p *permitted) Allow() (bool, error)   { p.asked = true; p.allowed = true; return true, nil }

func TestNoticesAccess(t *testing.T) {
	var n *Notices
	if got := n.Access(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE {
		t.Errorf("no notices: %v", got)
	}
	n = &Notices{}
	if got := n.Access(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE {
		t.Errorf("no notifier: %v", got)
	}
	n.Use(make(fakeNotifier))
	if got := n.Access(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED {
		t.Errorf("a notifier without permission: %v", got)
	}
	p := &permitted{}
	n.Use(p)
	if got := n.Access(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_DENIED || p.asked {
		t.Errorf("not allowed yet: %v (asked %v)", got, p.asked)
	}
	if got := n.Request(); got != uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED || !p.asked {
		t.Errorf("request: %v (asked %v)", got, p.asked)
	}
}

// putJournaled stores m, journaled as a test's command.
func putJournaled(tx *store.Tx, m proto.Message) error {
	if err := tx.Journal("test", "put", m); err != nil {
		return err
	}
	return tx.Put(m)
}
