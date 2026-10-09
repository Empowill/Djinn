package ui

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Notification is a system notification of a question or of an inbox item.
type Notification struct {
	// ID names the notification: the question's or the item's identifier.
	ID    string
	Title string
	Body  string
	// WishID and QuestionID come back with what the user does with it; both are empty for an inbox item.
	WishID     string
	QuestionID string
	// Actions are the buttons that answer the question, in order.
	Actions []Action
}

// Action is a button of a notification. Its ID comes back in Response.Action.
type Action struct {
	ID    string
	Title string
}

// Response is what the user did with a notification.
type Response struct {
	WishID     string
	QuestionID string
	// Action is the button pressed: an Action.ID; empty for a click on the notification itself.
	Action string
}

// Notifier shows system notifications: the window's notification service, or a fake in tests.
type Notifier interface {
	Notify(Notification) error
}

// fresh is how old a question or an inbox item may be to be notified: an imported wish brings old questions, which
// are not news.
const fresh = time.Minute

// maxBody is the most of a question's or an item's text a notification shows, in characters; the window shows it
// whole.
const maxBody = 300

// choices are the buttons' identifiers and the choice each one answers.
var choices = map[string]planv1.Choice{
	"yes": planv1.Choice_CHOICE_YES,
	"a":   planv1.Choice_CHOICE_A,
	"b":   planv1.Choice_CHOICE_B,
	"c":   planv1.Choice_CHOICE_C,
	"d":   planv1.Choice_CHOICE_D,
}

// Notices tells the user of each question asked in an active wish, by a system notification: a click brings the
// window forward on the wish, a button answers the question. It tells of each new inbox item too: a click brings the
// window forward, where the flight plan shows the inbox; nothing answers the item from the notification. Without a
// notifier (a headless build, the browser) it shows nothing.
type Notices struct {
	Store *store.Store
	// Language of the notifications' texts.
	Language string
	// Show brings the window forward on a wish.
	Show func(wishID string)
	// Answer answers a question with a choice, as the window would.
	Answer func(ctx context.Context, questionID string, choice planv1.Choice) error

	mu       sync.Mutex
	notifier Notifier
	seen     map[string]bool    // questions and items notified
	asked    chan proto.Message // questions and inbox items to notify
}

// Use shows the notifications with notifier from now on; nil shows none.
func (n *Notices) Use(notifier Notifier) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notifier = notifier
}

// Run follows the questions and the inbox items stored, and notifies the new ones, until ctx ends.
func (n *Notices) Run(ctx context.Context) {
	asked := make(chan proto.Message, 64)
	n.mu.Lock()
	n.asked = asked
	n.mu.Unlock()
	n.Store.OnCommit(n.committed)
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-asked:
			switch m := m.(type) {
			case *planv1.Question:
				if err := n.notify(ctx, m); err != nil {
					log.Printf("djinn: notify question %s: %v", m.GetCode(), err)
				}
			case *planv1.InboxItem:
				if err := n.notifyItem(m); err != nil {
					log.Printf("djinn: notify inbox item %s: %v", m.GetId(), err)
				}
			}
		}
	}
}

// committed passes on the questions just asked and the inbox items just come, while a notifier shows them; it runs in
// the writer's goroutine, so it never waits.
func (n *Notices) committed(changes []proto.Message) {
	n.mu.Lock()
	off := n.notifier == nil
	n.mu.Unlock()
	if off {
		return
	}
	for _, m := range changes {
		switch m := m.(type) {
		case *planv1.Question:
			if m.GetAnswer() != nil || time.Since(m.GetCreateTime().AsTime()) > fresh {
				continue
			}
		case *planv1.InboxItem:
			if m.GetState() != planv1.InboxState_INBOX_STATE_NEW || time.Since(m.GetCreateTime().AsTime()) > fresh {
				continue
			}
		default:
			continue
		}
		select {
		case n.asked <- proto.Clone(m):
		default: // Far behind: the window shows it anyway.
		}
	}
}

// first is the notifier while id was not notified yet, and marks it notified; nil when it was, or shows none.
func (n *Notices) first(id string) Notifier {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.notifier == nil || n.seen[id] {
		return nil
	}
	if n.seen == nil {
		n.seen = map[string]bool{}
	}
	n.seen[id] = true
	return n.notifier
}

// notify shows a notification of q once, when its wish is active.
func (n *Notices) notify(ctx context.Context, q *planv1.Question) error {
	notifier := n.first(q.GetId())
	if notifier == nil {
		return nil
	}
	wish, err := store.Get[*planv1.Wish](ctx, n.Store, q.GetWishId())
	if err != nil {
		return err
	}
	if s := wish.GetState(); s != planv1.WishState_WISH_STATE_ACTIVE && s != planv1.WishState_WISH_STATE_UNSPECIFIED {
		return nil
	}
	return notifier.Notify(n.notification(wish, q))
}

// notification is the notification of q: the wish and the question's code as title, its text and options as body,
// and a button per answer.
func (n *Notices) notification(wish *planv1.Wish, q *planv1.Question) Notification {
	body := []string{clipText(q.GetText(), maxBody)}
	var actions []Action
	for i, option := range q.GetOptions() {
		letter := string(rune('A' + i))
		body = append(body, fmt.Sprintf("%s. %s", letter, clipText(option, maxBody/4)))
		actions = append(actions, Action{ID: strings.ToLower(letter), Title: letter})
	}
	if len(actions) == 0 {
		actions = []Action{{ID: "yes", Title: locales.T(n.Language, "notify.yes", nil)}}
	}
	return Notification{
		ID: q.GetId(), WishID: q.GetWishId(), QuestionID: q.GetId(), Actions: actions,
		Title: locales.T(n.Language, "notify.question_title", map[string]string{"code": q.GetCode(), "wish": wish.GetTitle()}),
		Body:  strings.Join(body, "\n"),
	}
}

// notifyItem shows a notification of a new inbox item once: its source as title, its text as body, no button.
func (n *Notices) notifyItem(item *planv1.InboxItem) error {
	notifier := n.first(item.GetId())
	if notifier == nil {
		return nil
	}
	return notifier.Notify(Notification{
		ID:    item.GetId(),
		Title: locales.T(n.Language, "notify.inbox_title", map[string]string{"source": item.GetSource()}),
		Body:  clipText(item.GetText(), maxBody),
	})
}

// Respond follows what the user did with a notification: a button answers its question; a click, or an answer
// that fails, brings the window forward on the wish, or as it is for an inbox item, which has none.
func (n *Notices) Respond(ctx context.Context, r Response) {
	choice, ok := choices[r.Action]
	if ok && n.Answer != nil {
		err := n.Answer(ctx, r.QuestionID, choice)
		if err == nil {
			return
		}
		log.Printf("djinn: answer from a notification: %v", err)
	}
	if n.Show != nil {
		n.Show(r.WishID)
	}
}

// clipText cuts s to most characters, an ellipsis marking the cut.
func clipText(s string, most int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= most {
		return s
	}
	return strings.TrimSpace(string(r[:most-1])) + "…"
}
