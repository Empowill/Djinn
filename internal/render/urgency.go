package render

import (
	"cmp"
	"slices"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// Urgency is how much an open question holds up, the most urgent first.
type Urgency int

const (
	// Blocking: a waiting task needs the answer. Djinn computes it from the tasks.
	Blocking Urgency = iota
	// Move: a move only the developer can make (push a tag, merge a PR, install, etc.).
	Move
	// Before: the asker said what the answer is needed before (Question.before).
	Before
	// Later: nothing waits for it; it can wait.
	Later
)

// Blocked maps a question's id to the codes of the waiting tasks that need its answer: an edit question, or the
// blocking question a worker asked (Task.wait_question_id).
func Blocked(tasks []*planv1.Task) map[string][]string {
	blocked := map[string][]string{}
	for _, t := range tasks {
		if id := WaitsOn(t); id != "" {
			blocked[id] = append(blocked[id], t.GetCode())
		}
	}
	return blocked
}

// WaitsOn is the question the task waits WAITING on, by identifier: its edit question, or the blocking question its
// worker asked; "" for a task that does not wait.
func WaitsOn(t *planv1.Task) string {
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_WAITING {
		return ""
	}
	return cmp.Or(t.GetWaitQuestionId(), t.GetEditQuestionId())
}

// UrgencyOf is the urgency of an open question, given what Blocked returned.
func UrgencyOf(q *planv1.Question, blocked map[string][]string) Urgency {
	switch {
	case len(blocked[q.GetId()]) > 0:
		return Blocking
	case q.GetMove() && q.GetBefore() != "":
		return Move
	case q.GetBefore() != "":
		return Before
	default:
		return Later
	}
}

// Place is where an open question stands among the others: when it was asked, or when it was put off for later if
// that came after (Question.later_time). The window orders them the same way (byAsked, src/data/flight.ts).
func Place(q *planv1.Question) time.Time {
	at := q.GetCreateTime().AsTime()
	if later := q.GetLaterTime(); later != nil && later.AsTime().After(at) {
		return later.AsTime()
	}
	return at
}

// Asleep tells an open question put to sleep that has not woken yet (Question.sleep): it waits for its conditions,
// not for the developer, and is out of the open questions.
func Asleep(q *planv1.Question) bool {
	s := q.GetSleep()
	return q.GetAnswer() == nil && q.GetWithdrawal() == nil && s != nil && s.GetWakeTime() == nil
}

// Woken tells an open question that woke from its sleep and was not put off since (Later): it comes at the top of its
// wish's open questions.
func Woken(q *planv1.Question) bool {
	woke := q.GetSleep().GetWakeTime()
	if q.GetAnswer() != nil || q.GetWithdrawal() != nil || woke == nil {
		return false
	}
	return q.GetLaterTime() == nil || q.GetLaterTime().AsTime().Before(woke.AsTime())
}

// ByPlace orders open questions: the woken ones first (Woken), by when they woke, then the others by their Place, the
// oldest first; those at the same place keep their order. The window orders them the same way (byAsked).
func ByPlace(a, b *planv1.Question) int {
	wa, wb := Woken(a), Woken(b)
	switch {
	case wa && wb:
		return a.GetSleep().GetWakeTime().AsTime().Compare(b.GetSleep().GetWakeTime().AsTime())
	case wa:
		return -1
	case wb:
		return 1
	}
	return Place(a).Compare(Place(b))
}

// ByUrgency orders questions in place: blocking, then before X, then can wait; each level by Place, the oldest first.
func ByUrgency(questions []*planv1.Question, tasks []*planv1.Task) {
	blocked := Blocked(tasks)
	slices.SortStableFunc(questions, ByPlace)
	slices.SortStableFunc(questions, func(a, b *planv1.Question) int {
		return cmp.Compare(UrgencyOf(a, blocked), UrgencyOf(b, blocked))
	})
}
