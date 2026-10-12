package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Notification is a system notification: a question to answer, a task or wish that waits, a new inbox item, or the
// lead's choice.
type Notification struct {
	// ID names the notification: one with the same ID replaces it.
	ID    string
	Title string
	Body  string
	// WishID and Target come back with a click: the wish to show, and the element of it to bring into view
	// (UiServiceWatchShowResponse.target). Both are empty for an inbox item.
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

// Notices tells the user, by a system notification, of what waits for them in an active wish: a question asked
// (the edit question of a task included), a task that waits for an answer or failed, or a wish that became ready to
// review. A click brings the window forward on the wish, there; a button answers the question. It tells of each new
// inbox item too, and of the choice the lead shows: a click brings the window forward, where the flying carpet shows
// the inbox and the attention bar the lead's choice. Each event shows once, and none of a wish while the window is in
// front on it. Without a notifier (a headless build, the browser) it shows nothing.
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
	seen     map[string]bool    // events notified, by key
	states   map[string]string  // each task's status and each question's answer, as last passed on (state)
	changes  chan proto.Message // questions, tasks, inbox items and the lead's choices to follow
	focused  bool               // the window is in front
	viewing  string             // the wish the page shows
	prompt   *planv1.LeadPrompt // the choice the lead shows, as last seen; nil for none
	prompted string             // the choice of the lead last notified (leadPromptKey)

	readyMu sync.Mutex
	ready   map[string]bool // active wishes ready to review, as last seen
}

// Use shows the notifications with notifier from now on; nil shows none. A choice the lead showed before, and still
// shows, is notified then: the lead may ask to trust its folder before the window can notify.
func (n *Notices) Use(notifier Notifier) {
	n.mu.Lock()
	n.notifier = notifier
	n.mu.Unlock()
	if notifier == nil {
		return
	}
	if n.Store != nil {
		// What is ready already is not news: only a wish that becomes ready from now on is notified.
		if err := n.prime(context.Background()); err != nil {
			logx.L().Error("read the wishes ready to review", "loop", "notices", "err", err)
		}
	}
	n.replayLeadPrompt()
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

// Request asks the system to show the notifications, and tells whether it does. Once it does, the choice the lead
// shows is notified again: the system may have refused it before.
func (n *Notices) Request() uiv1.NotificationAccess {
	access := n.access(func(p Permission) (bool, error) { return p.Allow() })
	if access == uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED {
		n.mu.Lock()
		n.prompted = ""
		n.mu.Unlock()
		n.replayLeadPrompt()
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
		logx.L().Warn("ask permission to notify", "err", err)
	}
	if allowed {
		return uiv1.NotificationAccess_NOTIFICATION_ACCESS_ALLOWED
	}
	return uiv1.NotificationAccess_NOTIFICATION_ACCESS_DENIED
}

// Run follows the questions, tasks and inbox items stored, and notifies what is new, until ctx ends.
func (n *Notices) Run(ctx context.Context) {
	defer logx.Crash("notices")
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
			if p, ok := m.(*planv1.LeadPrompt); ok {
				if err := n.notifyLeadPrompt(p); err != nil {
					logx.L().Warn("notify the lead's choice", "loop", "notices", "err", err)
				}
				continue
			}
			if err := n.follow(ctx, m); err != nil && ctx.Err() == nil {
				logx.L().Error("notify a change", "loop", "notices", "err", err)
			}
		}
	}
}

// committed passes on what may be news, while a notifier shows them: a question asked or answered, a task whose
// status changed, a new inbox item. A task written again in the same status (its events, its resources) is not
// passed on: what waits, and whether a wish is ready, change only with a status. It runs in the writer's goroutine,
// so it never waits.
func (n *Notices) committed(changes []proto.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.notifier == nil {
		return
	}
	for _, m := range changes {
		switch m := m.(type) {
		case *planv1.Question:
			if !n.moved(m.GetId(), fmt.Sprint(m.GetAnswer() != nil)) {
				continue
			}
		case *planv1.Task:
			if !n.moved(m.GetId(), m.GetStatus().String()) {
				continue
			}
		case *planv1.InboxItem:
			if m.GetState() != planv1.InboxState_INBOX_STATE_NEW || !recent(m.GetCreateTime()) {
				continue
			}
		default:
			continue
		}
		select {
		case n.changes <- proto.Clone(m):
		default: // Far behind: the window shows it anyway.
		}
	}
}

// moved notes state as the one of id, and tells whether it is another than the one noted before. n.mu is held.
func (n *Notices) moved(id, state string) bool {
	if n.states == nil {
		n.states = map[string]string{}
	}
	was, ok := n.states[id]
	n.states[id] = state
	return !ok || was != state
}

// follow notifies what a question, a task or an inbox item just written tells, then whether its wish became ready.
func (n *Notices) follow(ctx context.Context, m proto.Message) error {
	var err error
	var wishID string
	switch m := m.(type) {
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
	case *planv1.InboxItem:
		err = n.notifyItem(m)
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
	return plan.Ready(tasks, questions), nil
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
	inView := note.WishID != "" && n.focused && n.viewing == note.WishID
	n.mu.Unlock()
	if inView {
		return nil // The user sees it already.
	}
	return notifier.Notify(note)
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

// notifyItem shows a new inbox item once: its source as title, its text as body, no button. An item has no wish: a
// click brings the window forward as it is.
func (n *Notices) notifyItem(item *planv1.InboxItem) error {
	return n.show("inbox:"+item.GetId(), Notification{
		ID:    item.GetId(),
		Title: n.text("notify.inbox_title", map[string]string{"source": item.GetSource()}),
		Body:  clipText(item.GetText(), maxBody),
	})
}

// LeadPrompt notifies the choice p the lead's agent shows in its terminal (an approval, a folder to trust): once per
// choice, again when another comes; nil says it went. A click brings the window forward, where the attention bar
// answers it. It never waits: it runs in the terminal's reader.
func (n *Notices) LeadPrompt(p *planv1.LeadPrompt) {
	if p == nil {
		p = &planv1.LeadPrompt{}
	}
	n.mu.Lock()
	changes := n.changes
	n.mu.Unlock()
	if changes == nil {
		return
	}
	select {
	case changes <- proto.Clone(p):
	default: // Far behind: the window shows it anyway.
	}
}

// replayLeadPrompt passes on again the choice the lead shows, if any: one the notifier could not show yet.
func (n *Notices) replayLeadPrompt() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.prompt != nil && n.changes != nil {
		select {
		case n.changes <- n.prompt:
		default:
		}
	}
}

// leadPromptKey names a choice of the lead, to tell one from another: its question, what it is about and its options.
// Empty for none.
func leadPromptKey(p *planv1.LeadPrompt) string {
	if len(p.GetOptions()) == 0 {
		return ""
	}
	return p.GetTitle() + "\x00" + strings.Join(p.GetLines(), "\x00") + "\x00" + strings.Join(p.GetOptions(), "\x00")
}

// notifyLeadPrompt shows a notification of the lead's choice p, unless it is the one shown last: its question as
// title, what it is about and its options as body, no button (the window answers it).
func (n *Notices) notifyLeadPrompt(p *planv1.LeadPrompt) error {
	key := leadPromptKey(p)
	n.mu.Lock()
	n.prompt = nil
	if key != "" {
		n.prompt = p
	}
	notifier := n.notifier
	same := key == n.prompted
	if notifier != nil || key == "" {
		n.prompted = key
	}
	n.mu.Unlock()
	if same || key == "" || notifier == nil {
		return nil
	}
	var body []string
	for _, line := range append([]string{p.GetTitle()}, p.GetLines()...) {
		if line != "" {
			body = append(body, clipText(line, maxBody/2))
		}
	}
	for i, option := range p.GetOptions() {
		body = append(body, fmt.Sprintf("%d. %s", i+1, clipText(option, maxBody/4)))
	}
	return notifier.Notify(Notification{
		ID:    "lead-prompt",
		Title: locales.T(n.Language, "notify.lead_prompt_title", nil),
		Body:  clipText(strings.Join(body, "\n"), 2*maxBody),
	})
}

// Respond follows what the user did with a notification: a button answers its question; a click, or an answer
// that fails, brings the window forward on the wish, there, or as it is for an inbox item or the lead's choice,
// which have none.
func (n *Notices) Respond(ctx context.Context, r Response) {
	choice, ok := choices[r.Action]
	if ok && n.Answer != nil {
		err := n.Answer(ctx, r.QuestionID, choice)
		if err == nil {
			return
		}
		logx.L().Warn("answer from a notification", "wish", r.WishID, "question", r.QuestionID, "err", err)
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
