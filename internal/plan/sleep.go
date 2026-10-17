package plan

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// requestAddress matches the web address of a pull request (…/pull/12) or of a merge request (…/-/merge_requests/12).
var requestAddress = regexp.MustCompile(`^https?://\S+/(?:pull|-/merge_requests)/\d+/?$`)

// days matches a delay in days, which time.ParseDuration does not read: 3d, 1d12h.
var days = regexp.MustCompile(`^(\d+)d(.*)$`)

// ParseSleep reads the conditions a question of the wish wishID sleeps until, as djinn question sleep --until takes
// them (commas separate several in one text), at now: tasks by code or identifier, merged: requests, web addresses of
// requests, times and delays. Each names a task of the wish, or a time to come.
func ParseSleep(ctx context.Context, tx *store.Tx, wishID string, texts []string, now time.Time) ([]*planv1.SleepCondition, error) {
	var out []*planv1.SleepCondition
	for _, text := range texts {
		for part := range strings.SplitSeq(text, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			c, err := parseCondition(ctx, tx, wishID, part, now)
			if err != nil {
				code := connect.CodeInvalidArgument // A task not found keeps its own code.
				if cerr := new(connect.Error); errors.As(err, &cerr) {
					code, err = cerr.Code(), errors.New(cerr.Message())
				}
				return nil, connect.NewError(code, fmt.Errorf("sleep until %q: %w", part, err))
			}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"sleep until what? Give a task (W12), a request merged (merged:W45), or a time (2026-10-20, 3d)"))
	}
	return out, nil
}

// parseCondition reads one condition, text, of ParseSleep.
func parseCondition(ctx context.Context, tx *store.Tx, wishID, text string, now time.Time) (*planv1.SleepCondition, error) {
	kind, ref, typed := strings.Cut(text, ":")
	if !typed || strings.HasPrefix(text, "http") || isTimeText(text) {
		kind, ref = "", text
	}
	switch strings.ToLower(kind) {
	case "merged":
		if requestAddress.MatchString(ref) {
			return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, Url: ref}, nil
		}
		t, err := sleepTask(ctx, tx, wishID, ref)
		if err != nil {
			return nil, err
		}
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, TaskId: t.GetId(), Code: t.GetCode()}, nil
	case "done", "closed", "task":
		return taskCondition(ctx, tx, wishID, ref)
	case "at", "time", "date":
		return timeCondition(ref, now)
	case "":
	default:
		return nil, fmt.Errorf("no condition %s: say done, merged or at, before a colon", kind)
	}
	switch {
	case requestAddress.MatchString(ref):
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, Url: ref}, nil
	case isTimeText(ref):
		return timeCondition(ref, now)
	}
	return taskCondition(ctx, tx, wishID, ref)
}

// isTimeText tells a text that says a time or a delay, rather than a task: it starts with a figure.
func isTimeText(text string) bool {
	return text != "" && text[0] >= '0' && text[0] <= '9'
}

// taskCondition is the condition that the task ref names is over.
func taskCondition(ctx context.Context, tx *store.Tx, wishID, ref string) (*planv1.SleepCondition, error) {
	t, err := sleepTask(ctx, tx, wishID, ref)
	if err != nil {
		return nil, err
	}
	return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TASK, TaskId: t.GetId(), Code: t.GetCode()}, nil
}

// sleepTask is the task of the wish wishID that ref names, by code or identifier.
func sleepTask(ctx context.Context, tx *store.Tx, wishID, ref string) (*planv1.Task, error) {
	if ref == "" {
		return nil, errors.New("name a task")
	}
	id, err := aboutTask(ctx, tx, wishID, ref)
	if err != nil {
		return nil, err
	}
	return store.Get[*planv1.Task](ctx, tx, id)
}

// timeCondition is the condition that the time text says has come: a day (midnight), a day and an hour, both on this
// machine's clock, an RFC 3339 time, or a delay from now.
func timeCondition(text string, now time.Time) (*planv1.SleepCondition, error) {
	at, err := parseWhen(text, now)
	if err != nil {
		return nil, err
	}
	return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TIME, Time: timestamppb.New(at)}, nil
}

// parseWhen reads the time text says, at now: see timeCondition.
func parseWhen(text string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return t, nil
		}
	}
	d, rest := time.Duration(0), text
	if m := days.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, err
		}
		d, rest = time.Duration(n)*24*time.Hour, m[2]
	}
	if rest != "" {
		more, err := time.ParseDuration(rest)
		if err != nil {
			return time.Time{}, errors.New("not a time: say 2026-10-20, 2026-10-20T09:00, an RFC 3339 time, or a delay like 3d or 2h")
		}
		d += more
	}
	if d <= 0 {
		return time.Time{}, errors.New("a delay to come, like 3d or 2h")
	}
	return now.Add(d), nil
}

// SleepHolds tells whether the condition c holds at now, by tasks: every task of the question's wish, Task.azima set
// on the azimas (WithAzimas). A task gone counts as over: nothing is left to wait for.
func SleepHolds(c *planv1.SleepCondition, tasks []*planv1.Task, now time.Time) bool {
	switch c.GetKind() {
	case planv1.SleepKind_SLEEP_KIND_TIME:
		return !now.Before(c.GetTime().AsTime())
	case planv1.SleepKind_SLEEP_KIND_TASK:
		for _, t := range tasks {
			if t.GetId() == c.GetTaskId() {
				return over(t)
			}
		}
		return true
	case planv1.SleepKind_SLEEP_KIND_MERGED:
		return merged(c, tasks)
	}
	return false
}

// SleepAllHold tells whether every condition the question q sleeps until holds at now (SleepHolds).
func SleepAllHold(q *planv1.Question, tasks []*planv1.Task, now time.Time) bool {
	conditions := q.GetSleep().GetConditions()
	for _, c := range conditions {
		if !SleepHolds(c, tasks, now) {
			return false
		}
	}
	return len(conditions) > 0
}

// over tells whether t is over, for a question asleep until it is: done (an azima, closed), stopped or cancelled.
func over(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE:
		return !IsAzima(t) || t.GetAzima().GetState() == planv1.AzimaState_AZIMA_STATE_DONE
	case planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
		return true
	}
	return false
}

// merged tells whether the request of c is merged, as Djinn knows it: a watcher that babysits it printed its done
// line, the watcher named, the one that babysits what the task named opened, or the one on its web address; or the
// azima named has its pull request merged.
func merged(c *planv1.SleepCondition, tasks []*planv1.Task) bool {
	var named *planv1.Task
	for _, t := range tasks {
		if t.GetId() == c.GetTaskId() {
			named = t
		}
	}
	if named.GetAzima().GetPr().GetState() == planv1.AzimaPrState_AZIMA_PR_STATE_MERGED {
		return true
	}
	for _, t := range tasks {
		b := t.GetBabysit()
		if b == nil || b.GetDoneTime() == nil {
			continue
		}
		switch {
		case c.GetUrl() != "" && strings.EqualFold(strings.TrimSuffix(b.GetUrl(), "/"), strings.TrimSuffix(c.GetUrl(), "/")),
			named != nil && t.GetId() == named.GetId(),
			named != nil && b.GetOpenedBy() != "" && b.GetOpenedBy() == named.GetCode() && t.GetWishId() == named.GetWishId():
			return true
		}
	}
	return false
}

// SleepText says a condition in a few words, for the brief and the lead's events: W12 over, W45's request merged,
// 2026-10-20 09:00.
func SleepText(c *planv1.SleepCondition) string {
	switch c.GetKind() {
	case planv1.SleepKind_SLEEP_KIND_TASK:
		return c.GetCode() + " over"
	case planv1.SleepKind_SLEEP_KIND_MERGED:
		if c.GetUrl() != "" {
			return c.GetUrl() + " merged"
		}
		return c.GetCode() + "'s request merged"
	case planv1.SleepKind_SLEEP_KIND_TIME:
		return c.GetTime().AsTime().Local().Format("2006-01-02 15:04")
	}
	return "?"
}

// SleepTexts says every condition of q's sleep, joined.
func SleepTexts(q *planv1.Question) string {
	var texts []string
	for _, c := range q.GetSleep().GetConditions() {
		texts = append(texts, SleepText(c))
	}
	return strings.Join(texts, ", ")
}

// WokeLine is the line that tells a lead the question q woke: its conditions held, or the developer woke it.
func WokeLine(q *planv1.Question) string {
	why := "its conditions hold: " + SleepTexts(q)
	if q.GetSleep().GetByHand() {
		why = "the developer woke it before " + SleepTexts(q)
	}
	return fmt.Sprintf("Djinn: %s woke, %s. Check that its facts still hold (costs, the state of tasks and requests) "+
		"and revise it if they changed (`djinn question revise %s`): it shows at the top of the developer's questions.",
		q.GetCode(), why, q.GetCode())
}

// sleep puts question to sleep until conditions, at now; it takes it out of the open questions until they hold. Its
// put-off date goes: it wakes at the top.
func sleep(question *planv1.Question, conditions []*planv1.SleepCondition, now time.Time) error {
	if question.GetAnswer() != nil {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s is decided", question.GetCode()))
	}
	if question.GetWithdrawal() != nil {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s was withdrawn", question.GetCode()))
	}
	question.Sleep = &planv1.QuestionSleep{Conditions: conditions, CreateTime: timestamppb.New(now)}
	question.LaterTime = nil
	return nil
}

// Wake wakes question, asleep, at now: by hand, or because its conditions hold. Its put-off date goes, so that it
// shows at the top of its wish's open questions (render.Woken).
func Wake(question *planv1.Question, now time.Time, byHand bool) {
	question.Sleep = proto.CloneOf(question.GetSleep())
	question.Sleep.WakeTime, question.Sleep.ByHand = timestamppb.New(now), byHand
	question.LaterTime = nil
}

// refuseHeld refuses conditions that all hold already, by the tasks of wishID at now: the question would wake at once.
func refuseHeld(ctx context.Context, tx *store.Tx, wishID string, conditions []*planv1.SleepCondition, now time.Time) error {
	tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wishID})
	if err != nil {
		return err
	}
	q := &planv1.Question{Sleep: &planv1.QuestionSleep{Conditions: conditions}}
	if SleepAllHold(q, WithAzimas(tasks), now) {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s hold already: nothing to sleep until", SleepTexts(q)))
	}
	return nil
}

// sleepUntil puts question to sleep until the conditions texts say, read at now, in tx: refused when they all hold
// already.
func sleepUntil(ctx context.Context, tx *store.Tx, question *planv1.Question, texts []string, now time.Time) error {
	conditions, err := ParseSleep(ctx, tx, question.GetWishId(), texts, now)
	if err != nil {
		return err
	}
	if err := refuseHeld(ctx, tx, question.GetWishId(), conditions, now); err != nil {
		return err
	}
	return sleep(question, conditions, now)
}

// Sleep puts an open question to sleep until its conditions hold, or wakes it now: the lead hears of it (Woke).
func (q *Questions) Sleep(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceSleepRequest],
) (*connect.Response[planv1.QuestionServiceSleepResponse], error) {
	m := req.Msg
	if m.GetWake() == (len(m.GetUntil()) > 0) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("give --until, or --wake"))
	}
	var question *planv1.Question
	woke := false
	err := write(ctx, q.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if question, err = find(ctx, tx, m.GetQuestion(), m.GetWishId()); err != nil {
			return err
		}
		now := time.Now()
		if !m.GetWake() {
			if err := sleepUntil(ctx, tx, question, m.GetUntil(), now); err != nil {
				return err
			}
			return tx.Put(question)
		}
		if !render.Asleep(question) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("question %s is not asleep", question.GetCode()))
		}
		Wake(question, now, true)
		woke = true
		return tx.Put(question)
	})
	if err != nil {
		return nil, err
	}
	if woke {
		ctx = context.WithoutCancel(ctx)
		for _, f := range q.Woke {
			f(ctx, proto.CloneOf(question))
		}
	}
	return connect.NewResponse(&planv1.QuestionServiceSleepResponse{Question: question}), nil
}

// Woke tells the lead of q's wish that q woke (WokeLine).
func (w *Wishes) Woke(ctx context.Context, q *planv1.Question) {
	w.notify(ctx, q, planv1.LeadEventKind_LEAD_EVENT_KIND_WOKE, WokeLine(q), "")
}
