package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
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

// putJournaled stores m, journaled as a test's command.
func putJournaled(tx *store.Tx, m proto.Message) error {
	if err := tx.Journal("test", "put", m); err != nil {
		return err
	}
	return tx.Put(m)
}

// permitted is a notifier that needs the system's permission.
type permitted struct {
	fakeNotifier
	allowed, asked bool
}

func (p *permitted) Allowed() (bool, error) { return p.allowed, nil }
func (p *permitted) Allow() (bool, error)   { p.asked = true; p.allowed = true; return true, nil }

// TestNoticesRun: Run follows the store and notifies what waits for the developer, once: a question of an active
// wish, an edit permission, a new inbox item, a wish ready to review; never a failed task, nor what the window shows.
func TestNoticesRun(t *testing.T) {
	t.Parallel()
	t.Run("shows the questions of active wishes", func(t *testing.T) {
		t.Parallel()
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
		put(t, db, q)
		ask(t, db, &planv1.Question{WishId: active.GetId(), Text: "Release now?"})
		if got := notes.next(t); got.Title != "Question Q04 · Ship the release" || !slices.Equal(got.Actions, []Action{{"yes", "Yes"}}) {
			t.Errorf("second notification = %+v", got)
		}
		notes.none(t)
	})
	t.Run("translates and clips", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "fr")
		w := makeWish(t, n.Store, "Ship", planv1.WishState_WISH_STATE_UNSPECIFIED)
		long := strings.Repeat("word ", 200)
		ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: long})
		got := notes.next(t)
		if got.Actions[0].Title != locales.T("fr", "notify.yes", nil) || got.Actions[0].Title == "Yes" || len([]rune(got.Body)) > maxBody {
			t.Errorf("notification = %+v (%d characters)", got, len([]rune(got.Body)))
		}
	})
	t.Run("shows nothing without a notifier", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		n.Use(nil)
		w := makeWish(t, n.Store, "Quiet", planv1.WishState_WISH_STATE_ACTIVE)
		ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: "Anyone?"})
		n.Use(notes)
		ask(t, n.Store, &planv1.Question{WishId: w.GetId(), Text: "Now?"})
		if got := notes.next(t); got.Body != "Now?" {
			t.Errorf("first notification shown = %+v", got)
		}
	})
	t.Run("shows new inbox items", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		db := n.Store
		receive := func(item *planv1.InboxItem) *planv1.InboxItem {
			t.Helper()
			item.Id, item.Key = store.NewID(), store.NewID()
			if item.CreateTime == nil {
				item.CreateTime = timestamppb.Now()
			}
			put(t, db, item)
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
		put(t, db, item)
		notes.none(t)
	})
	// The edit question of a task (ASKING) says which task asks to change which project, with yes and no as buttons;
	// when its worker ends read-only and waits, the notification of the question is replaced.
	t.Run("asks the permission of a task", func(t *testing.T) {
		t.Parallel()
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
		waiting := locales.T("fr", "notify.task_waiting_title", map[string]string{"code": "W1", "wish": "Livrer"})
		if got.Title != waiting || got.ID != q.GetId() || got.QuestionID != q.GetId() {
			t.Errorf("waiting = %+v", got)
		}
		// The same task written again, waiting still: the same event, not shown again.
		put(t, db, task)
		notes.none(t)
	})
	// A failed worker is the lead's, never the developer's (Q273, W96: W19, an investigator failed on refused commands,
	// told the developer it waited for them): no notification, as a worker that waits for its edit question has one.
	t.Run("shows no failed task", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		db := n.Store
		w := makeWish(t, db, "Ship it", planv1.WishState_WISH_STATE_ACTIVE)
		task := &planv1.Task{
			Id: store.NewID(), WishId: w.GetId(), Code: "W19", Title: "Q17: enlighten", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
			Role: planv1.TaskRole_TASK_ROLE_INVESTIGATOR, Question: "Q17",
		}
		put(t, db, task)
		notes.none(t)
		task.Status, task.EndTime = planv1.TaskStatus_TASK_STATUS_FAILED, timestamppb.Now()
		task.Error = "its command was refused: djinn task list --wish-id x | head"
		task.Refused = &planv1.TaskRefusals{Count: 2, Names: []string{"djinn"}}
		put(t, db, task)
		notes.none(t)
	})
	t.Run("shows a wish ready to review", func(t *testing.T) {
		t.Parallel()
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
	})
	// Nothing for the wish the window shows while it is in front; the same event is not shown later either.
	t.Run("quiet on the wish in view", func(t *testing.T) {
		t.Parallel()
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
	})
	// A lead asks with `djinn question ask`, a call to the server. The question reaches the Watch stream the window
	// follows, and a system notification.
	t.Run("shows a question asked through the server", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		mux := http.NewServeMux()
		for prefix, h := range plan.Handlers(n.Store) {
			mux.Handle(prefix, h)
		}
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		wishes := planv1connect.NewWishServiceClient(srv.Client(), srv.URL)
		made, err := wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Light the way"}))
		if err != nil {
			t.Fatal(err)
		}
		wishID := made.Msg.GetWish().GetId()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		watch, err := wishes.Watch(ctx, connect.NewRequest(&planv1.WishServiceWatchRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Close()
		if !watch.Receive() { // The first message names everything.
			t.Fatal(watch.Err())
		}

		questions := planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL)
		if _, err := questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			WishId: wishID, Text: "Which lamp first?", Options: []string{"Brass", "Glass"},
		})); err != nil {
			t.Fatal(err)
		}
		for watch.Receive() {
			if strings.EqualFold(watch.Msg().GetWishId(), wishID) &&
				slices.Contains(watch.Msg().GetChanges(), planv1.Change_CHANGE_QUESTION) {
				break
			}
		}
		if err := watch.Err(); err != nil {
			t.Fatalf("the watch never said the question: %v", err)
		}
		if got := notes.next(t); got.Title != "Question Q01 · Light the way" || got.WishID != wishID {
			t.Errorf("notification = %+v", got)
		}
	})
}

// TestNoticesCommitted: a task written again in the same status (its events, its resources) is not followed: what
// waits for the developer, and whether a wish is ready, change only with a task's status or a question's answer.
func TestNoticesCommitted(t *testing.T) {
	t.Parallel()
	n := &Notices{changes: make(chan proto.Message, 16)}
	n.Use(make(fakeNotifier))
	task := &planv1.Task{Id: "t1", WishId: "w1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}
	q := &planv1.Question{Id: "q1", WishId: "w1", CreateTime: timestamppb.Now()}
	n.committed([]proto.Message{task, q})
	task = proto.CloneOf(task)
	task.Resources = &planv1.Resources{Processes: 2}
	n.committed([]proto.Message{task, q})
	if got := len(n.changes); got != 2 {
		t.Fatalf("followed %d changes, want the task and the question once", got)
	}
	task = proto.CloneOf(task)
	task.Status = planv1.TaskStatus_TASK_STATUS_DONE
	q = proto.CloneOf(q)
	q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}
	n.committed([]proto.Message{task, q})
	n.committed([]proto.Message{task, q})
	if got := len(n.changes); got != 4 {
		t.Fatalf("followed %d changes, want the task done and the question answered once more", got)
	}
}

// TestNoticesAccess: notifications are unavailable without a notifier, allowed with one that needs no permission,
// denied until Request asks the system for it.
func TestNoticesAccess(t *testing.T) {
	t.Parallel()
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

// TestNoticesRespond: a click on a notification shows its target; a button answers its question, and shows the wish
// when the answer fails or the button is unknown.
func TestNoticesRespond(t *testing.T) {
	t.Parallel()
	type answer struct {
		id     string
		choice planv1.Choice
	}
	for _, c := range []struct {
		name    string
		r       Response
		fail    bool
		shown   []string
		answers []answer
	}{
		{"a click shows its target", Response{WishID: "w1", QuestionID: "q1", Target: "question-q1"}, false,
			[]string{"w1#question-q1"}, nil},
		{"a button answers", Response{WishID: "w1", QuestionID: "q1", Action: "b"}, false,
			nil, []answer{{"q1", planv1.Choice_CHOICE_B}}},
		{"yes answers yes", Response{WishID: "w2", QuestionID: "q2", Action: "yes"}, false,
			nil, []answer{{"q2", planv1.Choice_CHOICE_YES}}},
		{"a failed answer shows the wish", Response{WishID: "w3", QuestionID: "q3", Action: "a"}, true,
			[]string{"w3#"}, []answer{{"q3", planv1.Choice_CHOICE_A}}},
		{"an unknown button shows the wish", Response{WishID: "w4", QuestionID: "q4", Action: "unknown"}, false,
			[]string{"w4#"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var shown []string
			var answers []answer
			n := &Notices{
				Show: func(wishID, target string) { shown = append(shown, wishID+"#"+target) },
				Answer: func(_ context.Context, id string, choice planv1.Choice) error {
					answers = append(answers, answer{id, choice})
					if c.fail {
						return errors.New("already answered")
					}
					return nil
				},
			}
			n.Respond(t.Context(), c.r)
			if !slices.Equal(shown, c.shown) || !slices.Equal(answers, c.answers) {
				t.Errorf("shown %v, answers %v; want %v, %v", shown, answers, c.shown, c.answers)
			}
		})
	}
}

// TestNoticesLeadPrompt: the choice the lead's agent shows is notified once, again when its command changes or once
// it went and came back; one shown before the window can notify is notified once it can.
func TestNoticesLeadPrompt(t *testing.T) {
	t.Parallel()
	t.Run("once, again when it changes", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		p := &planv1.LeadPrompt{Title: "Run this command?", Lines: []string{"go tool task test"}, Options: []string{"Yes", "No"}}
		n.LeadPrompt(p)
		note := notes.next(t)
		if note.ID != "lead-prompt" || note.Title != "The Djinn needs your approval" ||
			note.Body != "Run this command?\ngo tool task test\n1. Yes\n2. No" || len(note.Actions) != 0 {
			t.Fatalf("notification: %+v", note)
		}
		n.LeadPrompt(p)
		notes.none(t) // The same choice again.
		p.Lines = []string{"go tool task build"}
		n.LeadPrompt(p)
		if note := notes.next(t); !strings.Contains(note.Body, "go tool task build") {
			t.Fatalf("changed command: %+v", note)
		}
		n.LeadPrompt(nil)
		n.LeadPrompt(p)
		notes.next(t)
		n.LeadPrompt(&planv1.LeadPrompt{})
		notes.none(t) // No choice.
	})
	// A choice the lead shows before the window can notify (its folder's trust at start) is notified once it can, and
	// only once.
	t.Run("once the window can notify", func(t *testing.T) {
		t.Parallel()
		n, notes := newNotices(t, "en")
		n.Use(nil)
		n.LeadPrompt(&planv1.LeadPrompt{Title: "Trust this folder?", Lines: []string{"/project"}, Options: []string{"No", "Yes"}})
		time.Sleep(50 * time.Millisecond)
		n.Use(notes)
		if got := notes.next(t); got.ID != "lead-prompt" || !strings.Contains(got.Body, "/project") {
			t.Fatalf("notification at start: %+v", got)
		}
		n.Use(notes)
		notes.none(t)
	})
}

// TestNoticesIfritWarning: an ifrit about to go out is notified once per out time, even with the window in front on
// its wish, which says nothing of it; its Keep lit button keeps it lit, and brings no window forward.
func TestNoticesIfritWarning(t *testing.T) {
	t.Parallel()
	n, notes := newNotices(t, "en")
	var kept, shown int
	n.KeepLit = func() { kept++ }
	n.Show = func(string, string) { shown++ }
	w := makeWish(t, n.Store, "Front", planv1.WishState_WISH_STATE_ACTIVE)
	n.View(w.GetId())
	n.Focus(true)
	out := time.Date(2026, 10, 16, 10, 30, 0, 0, time.Local)
	ifrit := &planv1.Task{Id: store.NewID(), WishId: w.GetId(), Code: "W12", Kind: planv1.TaskKind_TASK_KIND_IFRIT,
		Ifrit: &planv1.TaskIfrit{State: planv1.IfritState_IFRIT_STATE_LISTENING, OutTime: timestamppb.New(out)}}
	n.IfritWarning(ifrit)
	got := notes.next(t)
	if got.Title != "W12 goes out in 5 min · Front" || !strings.Contains(got.Body, "W12 goes out at 10:30") ||
		got.Target != "task-"+ifrit.GetId() || len(got.Actions) != 1 || got.Actions[0].Title != "Keep lit" {
		t.Errorf("warning = %+v", got)
	}
	n.IfritWarning(ifrit)
	notes.none(t)

	n.Respond(t.Context(), Response{WishID: w.GetId(), Target: got.Target, Action: got.Actions[0].ID})
	if kept != 1 || shown != 0 {
		t.Errorf("Keep lit: kept %d, shown %d; want kept once, no window", kept, shown)
	}
}

// TestNoticesWindow: what djinn up found about the window's page shows each time, even while the window is in front
// on a wish, under one ID that replaces the last; with no notifier, nothing shows and nothing fails.
func TestNoticesWindow(t *testing.T) {
	t.Parallel()
	n, notes := newNotices(t, "en")
	n.View("w")
	n.Focus(true)
	for range 2 {
		if err := n.Window("Djinn's window stayed black", "Djinn opened it again."); err != nil {
			t.Fatal(err)
		}
		if got := notes.next(t); got.ID != "djinn-window" || got.Title != "Djinn's window stayed black" ||
			got.Body != "Djinn opened it again." || got.WishID != "" {
			t.Errorf("notification = %+v", got)
		}
	}
	n.Use(nil)
	if err := n.Window("t", "b"); err != nil {
		t.Errorf("without a notifier: %v", err)
	}
	notes.none(t)
}
