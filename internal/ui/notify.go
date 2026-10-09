package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Notification is a system notification: a question to answer, a task that waits or failed, a wish ready to review.
type Notification struct {
	// ID names the notification: one with the same ID replaces it.
	ID    string
	Title string
	Body  string
	// WishID and Target come back with a click: the wish to show, and the element of it to bring into view
	// (UiServiceWatchShowResponse.target).
	WishID string
	Target string
	// QuestionID is the question the buttons answer; empty without buttons.
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
	Target     string
	// Action is the button pressed: an Action.ID; empty for a click on the notification itself.
	Action string
}

// Notifier shows system notifications: the window's notification service, or a fake in tests.
type Notifier interface {
	Notify(Notification) error
}

// Permission is a notifier that needs the system's permission to show anything (macOS). A notifier without it
// shows them as soon as it runs.
type Permission interface {
	// Allowed tells whether the system shows the notifications.
	Allowed() (bool, error)
	// Allow asks the system to show them, and tells whether it does: on macOS its dialog shows the first time only.
	Allow() (bool, error)
}

// fresh is how old an event may be to be notified: an imported wish brings old questions, which are not news.
const fresh = time.Minute

// maxBody is the most of a question's text a notification shows, in characters; the window shows it whole.
const maxBody = 300

// choices are the buttons' identifiers and the choice each one answers.
var choices = map[string]planv1.Choice{
	"yes": planv1.Choice_CHOICE_YES,
	"a":   planv1.Choice_CHOICE_A,
	"b":   planv1.Choice_CHOICE_B,
	"c":   planv1.Choice_CHOICE_C,
	"d":   planv1.Choice_CHOICE_D,
}

// Notices tells the user, by a system notification, of what waits for them in an active wish: a question asked
// (the edit question of a task included), a task that waits for an answer or failed, a wish that became ready to
// review. A click brings the window forward on the wish, there; a button answers the question. Each event shows
// once, and none while the window is in front on its wish. Without a notifier (a headless build, the browser) it
// shows nothing.
type Notices struct {
	Store *store.Store
	// Language of the notifications' texts.
	Language string
	// Show brings the window forward on a wish, with target in view (UiServiceWatchShowResponse.target).
	Show func(wishID, target string)
	// Answer answers a question with a choice, as the window would.
	Answer func(ctx context.Context, questionID string, choice planv1.Choice) error

	mu       sync.Mutex
	notifier Notifier
	seen     map[string]bool // events notified, by key
	changes  chan proto.Message
	focused  bool   // the window is in front
	viewing  string // the wish the page shows

	readyMu sync.Mutex
	ready   map[string]bool // active wishes ready to review, as last seen

	leadMu      sync.Mutex
	leadCurrent map[string]*planv1.LeadPrompt // current prompts, including those waiting for notification permission
	leadShown   map[string]string             // the choice each wish's lead shows, as last notified
}

// Use shows the notifications with notifier from now on; nil shows none.
func (n *Notices) Use(notifier Notifier) {
	n.mu.Lock()
	n.notifier = notifier
	n.mu.Unlock()
	if notifier != nil && n.Store != nil {
		// What is ready already is not news: only a wish that becomes ready from now on is notified.
		if err := n.prime(context.Background()); err != nil {
			log.Printf("djinn: notices: %v", err)
		}
		n.replayLeadPrompts()
	}
}

// Focus says whether the window is in front.
func (n *Notices) Focus(focused bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.focused = focused
}

// View says which wish the page shows; empty for none.
func (n *Notices) View(wishID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.viewing = wishID
}

// Access tells whether the system shows the notifications.
func (n *Notices) Access() uiv1.NotificationAccess {
	return n.access(func(p Permission) (bool, error) { return p.Allowed() })
}

// Request asks the system to show the notifications, and tells whether it does.
func (n *Notices) Request() uiv1.NotificationAccess {
	access := n.access(func(p Permission) (bool, error) { return p.Allow() })
	if access == uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED {
		n.replayLeadPrompts()
	}
	return access
}

func (n *Notices) access(ask func(Permission) (bool, error)) uiv1.NotificationAccess {
	if n == nil {
		return uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE
	}
	n.mu.Lock()
	notifier := n.notifier
	n.mu.Unlock()
	if notifier == nil {
		return uiv1.NotificationAccess_NOTIFICATION_ACCESS_UNAVAILABLE
	}
	p, ok := notifier.(Permission)
	if !ok {
		return uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED
	}
	allowed, err := ask(p)
	if err != nil {
		log.Printf("djinn: permission to notify: %v", err)
	}
	if allowed {
		return uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED
	}
	return uiv1.NotificationAccess_NOTIFICATION_ACCESS_DENIED
}

// Run follows the questions and tasks stored, and notifies what is new, until ctx ends.
func (n *Notices) Run(ctx context.Context) {
	changes := make(chan proto.Message, 256)
	n.mu.Lock()
	n.changes = changes
	n.mu.Unlock()
	n.Store.OnCommit(n.committed)
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-changes:
			if err := n.follow(ctx, m); err != nil && ctx.Err() == nil {
				log.Printf("djinn: notify: %v", err)
			}
		}
	}
}

// committed passes on the questions and tasks just written, while a notifier shows them; it runs in the writer's
// goroutine, so it never waits.
func (n *Notices) committed(changes []proto.Message) {
	n.mu.Lock()
	off := n.notifier == nil
	n.mu.Unlock()
	if off {
		return
	}
	for _, m := range changes {
		switch m.(type) {
		case *planv1.Question, *planv1.Task, *planv1.Instruction:
		default:
			continue
		}
		select {
		case n.changes <- proto.Clone(m):
		default: // Far behind: the window shows it anyway.
		}
	}
}

// follow notifies what a question or a task just written tells, then whether its wish became ready.
func (n *Notices) follow(ctx context.Context, m proto.Message) error {
	var err error
	var wishID string
	switch m := m.(type) {
	case *planv1.Wish:
		n.leadMu.Lock()
		p := n.leadCurrent[m.GetId()]
		n.leadMu.Unlock()
		if p != nil {
			n.LeadPrompt(m.GetId(), p)
		}
	case *planv1.Instruction:
		wishID = m.GetWishId()
	case *planv1.Question:
		wishID = m.GetWishId()
		if m.GetAnswer() == nil && recent(m.GetCreateTime()) {
			err = n.question(ctx, m)
		}
	case *planv1.Task:
		wishID = m.GetWishId()
		switch m.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_WAITING, planv1.TaskStatus_TASK_STATUS_FAILED:
			if recent(m.GetEndTime()) {
				err = n.task(ctx, m)
			}
		}
	}
	return errors.Join(err, n.readiness(ctx, wishID))
}

// recent tells that an event of time t is news.
func recent(t *timestamppb.Timestamp) bool {
	return t != nil && time.Since(t.AsTime()) <= fresh
}

// question notifies q. The edit question of a task (docs/providers.md, ASKING) says which task asks, and its buttons
// say yes and no.
func (n *Notices) question(ctx context.Context, q *planv1.Question) error {
	wish, err := n.activeWish(ctx, q.GetWishId())
	if wish == nil || err != nil {
		return err
	}
	note := n.questionNote(wish, q)
	asking, err := store.List[*planv1.Task](ctx, n.Store, store.Where{"edit_question_id": q.GetId()})
	if err != nil {
		return err
	}
	if len(asking) > 0 {
		note = n.permissionNote(ctx, wish, asking[0], q.GetId())
	}
	return n.show("question:"+q.GetId(), note)
}

// task notifies a task that waits for the answer to its edit question, or failed.
func (n *Notices) task(ctx context.Context, t *planv1.Task) error {
	wish, err := n.activeWish(ctx, t.GetWishId())
	if wish == nil || err != nil {
		return err
	}
	key := fmt.Sprintf("task:%s:%d:%d", t.GetId(), t.GetStatus(), t.GetEndTime().AsTime().UnixNano())
	if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING {
		if t.GetEditQuestionId() == "" {
			return nil
		}
		// Its notification replaces the one of its question: the same event, now that the worker has read.
		note := n.permissionNote(ctx, wish, t, t.GetEditQuestionId())
		note.Title = n.text("notify.task_waiting_title", map[string]string{"code": t.GetCode(), "wish": wish.GetTitle()})
		return n.show(key, note)
	}
	body := []string{clipText(t.GetTitle(), maxBody/2)}
	if e := t.GetError(); e != "" {
		body = append(body, clipText(e, maxBody/2))
	}
	return n.show(key, Notification{
		ID: "task-" + t.GetId(), WishID: wish.GetId(), Target: "task-" + t.GetId(),
		Title: n.text("notify.task_failed_title", map[string]string{"code": t.GetCode(), "wish": wish.GetTitle()}),
		Body:  strings.Join(body, "\n"),
	})
}

// readiness notifies the wish of id when it just became ready to review (plan.Ready).
func (n *Notices) readiness(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	wish, err := n.activeWish(ctx, id)
	if err != nil {
		return err
	}
	ready := false
	if wish != nil {
		if ready, err = n.isReady(ctx, id); err != nil {
			return err
		}
	}
	n.readyMu.Lock()
	was := n.ready[id]
	if n.ready == nil {
		n.ready = map[string]bool{}
	}
	n.ready[id] = ready
	n.readyMu.Unlock()
	if !ready || was {
		return nil
	}
	// The change from not ready to ready is the event: a wish ready again after more work is notified again.
	return n.show("", Notification{
		ID: "ready-" + id, WishID: id, Target: "grant-" + id,
		Title: n.text("notify.wish_ready_title", map[string]string{"wish": wish.GetTitle()}),
		Body:  n.text("notify.wish_ready_body", nil),
	})
}

// prime notes which active wishes are ready now.
func (n *Notices) prime(ctx context.Context) error {
	wishes, err := plan.ActiveWishes(ctx, n.Store)
	if err != nil {
		return err
	}
	ready := map[string]bool{}
	for _, w := range wishes {
		if ready[w.GetId()], err = n.isReady(ctx, w.GetId()); err != nil {
			return err
		}
	}
	n.readyMu.Lock()
	n.ready = ready
	n.readyMu.Unlock()
	return nil
}

func (n *Notices) isReady(ctx context.Context, wishID string) (bool, error) {
	where := store.Where{"wish_id": wishID}
	tasks, err := store.List[*planv1.Task](ctx, n.Store, where)
	if err != nil {
		return false, err
	}
	questions, err := store.List[*planv1.Question](ctx, n.Store, where)
	if err != nil {
		return false, err
	}
	instructions, err := store.List[*planv1.Instruction](ctx, n.Store, where)
	if err != nil {
		return false, err
	}
	return plan.Ready(tasks, questions, instructions...), nil
}

// activeWish is the wish of id when it is active, else nil.
func (n *Notices) activeWish(ctx context.Context, id string) (*planv1.Wish, error) {
	wish, err := store.Get[*planv1.Wish](ctx, n.Store, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil || !plan.Active(wish) {
		return nil, err
	}
	return wish, nil
}

// show shows note once for key (always for an empty key), unless the window is in front on its wish.
func (n *Notices) show(key string, note Notification) error {
	n.mu.Lock()
	notifier := n.notifier
	if notifier == nil || (key != "" && n.seen[key]) {
		n.mu.Unlock()
		return nil
	}
	if key != "" {
		if n.seen == nil {
			n.seen = map[string]bool{}
		}
		n.seen[key] = true
	}
	inView := n.focused && n.viewing == note.WishID
	n.mu.Unlock()
	if inView {
		return nil // The user sees it already.
	}
	return notifier.Notify(note)
}

// LeadPrompt notifies the choice p the lead of wishID shows in its terminal (an approval, a folder to trust): once
// per choice, again when another comes; nil says it went. A click shows it in the window, which answers it.
func (n *Notices) LeadPrompt(wishID string, p *planv1.LeadPrompt) {
	key := ""
	if p != nil {
		key = p.GetTitle() + "\x00" + strings.Join(p.GetLines(), "\x00") + "\x00" + strings.Join(p.GetOptions(), "\x00")
	}
	n.leadMu.Lock()
	if n.leadCurrent == nil {
		n.leadCurrent = map[string]*planv1.LeadPrompt{}
	}
	if p == nil {
		delete(n.leadCurrent, wishID)
		delete(n.leadShown, wishID)
	} else {
		n.leadCurrent[wishID] = proto.Clone(p).(*planv1.LeadPrompt)
	}
	same := n.leadShown[wishID] == key
	n.leadMu.Unlock()
	if same || key == "" || n.Store == nil || n.Access() != uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED {
		return
	}
	wish, err := n.activeWish(context.Background(), wishID)
	if wish == nil || err != nil {
		return
	}
	n.leadMu.Lock()
	if n.leadShown == nil {
		n.leadShown = map[string]string{}
	}
	if n.leadShown[wishID] == key {
		n.leadMu.Unlock()
		return
	}
	n.leadShown[wishID] = key
	n.leadMu.Unlock()
	body := []string{}
	for _, line := range append([]string{p.GetTitle()}, p.GetLines()...) {
		if line != "" {
			body = append(body, clipText(line, maxBody/2))
		}
	}
	for i, option := range p.GetOptions() {
		body = append(body, fmt.Sprintf("%d. %s", i+1, clipText(option, maxBody/4)))
	}
	note := Notification{
		ID: "lead-prompt-" + wishID, WishID: wishID, Target: "lead-prompt-" + wishID,
		Title: n.text("notify.lead_prompt_title", map[string]string{"wish": wish.GetTitle()}),
		Body:  clipText(strings.Join(body, "\n"), 2*maxBody),
	}
	if err := n.show("", note); err != nil {
		n.leadMu.Lock()
		delete(n.leadShown, wishID)
		n.leadMu.Unlock()
		log.Printf("djinn: notices: %v", err)
	}
}

// replayLeadPrompts delivers choices seen before the notifier or its permission became available.
func (n *Notices) replayLeadPrompts() {
	n.leadMu.Lock()
	pending := make(map[string]*planv1.LeadPrompt, len(n.leadCurrent))
	for id, p := range n.leadCurrent {
		pending[id] = p
	}
	n.leadMu.Unlock()
	for id, p := range pending {
		n.LeadPrompt(id, p)
	}
}

// questionNote is the notification of q: the wish and the question's code as title, its text and options as body,
// and a button per answer.
func (n *Notices) questionNote(wish *planv1.Wish, q *planv1.Question) Notification {
	body := []string{clipText(q.GetText(), maxBody)}
	var actions []Action
	for i, option := range q.GetOptions() {
		letter := string(rune('A' + i))
		body = append(body, fmt.Sprintf("%s. %s", letter, clipText(option, maxBody/4)))
		actions = append(actions, Action{ID: strings.ToLower(letter), Title: letter})
	}
	if len(actions) == 0 {
		actions = []Action{{ID: "yes", Title: n.text("notify.yes", nil)}}
	}
	return Notification{
		ID: q.GetId(), WishID: q.GetWishId(), QuestionID: q.GetId(), Target: "question-" + q.GetId(), Actions: actions,
		Title: n.text("notify.question_title", map[string]string{"code": q.GetCode(), "wish": wish.GetTitle()}),
		Body:  strings.Join(body, "\n"),
	}
}

// permissionNote is the notification of the edit question questionID of task t: may its worker change the files?
// A asks yes, B no, as the question's options.
func (n *Notices) permissionNote(ctx context.Context, wish *planv1.Wish, t *planv1.Task, questionID string) Notification {
	project := t.GetProjectId()
	if p, err := store.Get[*planv1.Project](ctx, n.Store, project); err == nil {
		project = p.GetName()
	}
	return Notification{
		ID: questionID, WishID: wish.GetId(), QuestionID: questionID, Target: "question-" + questionID,
		Title: n.text("notify.permission_title", map[string]string{"code": t.GetCode(), "wish": wish.GetTitle()}),
		Body:  n.text("notify.permission_body", map[string]string{"code": t.GetCode(), "project": project}),
		Actions: []Action{
			{ID: "a", Title: n.text("notify.yes", nil)},
			{ID: "b", Title: n.text("notify.no", nil)},
		},
	}
}

func (n *Notices) text(key string, vars map[string]string) string {
	return locales.T(n.Language, key, vars)
}

// Respond follows what the user did with a notification: a button answers its question; a click, or an answer
// that fails, brings the window forward on the wish, there.
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
		n.Show(r.WishID, r.Target)
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
