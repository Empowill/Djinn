package render

import (
	"cmp"
	"slices"

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

// Blocked maps a question's id to the codes of the waiting tasks that need its answer.
func Blocked(tasks []*planv1.Task) map[string][]string {
	blocked := map[string][]string{}
	for _, t := range tasks {
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING && t.GetEditQuestionId() != "" {
			blocked[t.GetEditQuestionId()] = append(blocked[t.GetEditQuestionId()], t.GetCode())
		}
	}
	return blocked
}

// UrgencyOf is the urgency of an open question, given what Blocked returned.
func UrgencyOf(q *planv1.Question, blocked map[string][]string) Urgency {
	switch {
	case len(blocked[q.GetId()]) > 0:
		return Blocking
	case q.GetMove():
		return Move
	case q.GetBefore() != "":
		return Before
	default:
		return Later
	}
}

// ByUrgency orders questions in place: blocking, then before X, then can wait; each level keeps its order.
func ByUrgency(questions []*planv1.Question, tasks []*planv1.Task) {
	blocked := Blocked(tasks)
	slices.SortStableFunc(questions, func(a, b *planv1.Question) int {
		return cmp.Compare(UrgencyOf(a, blocked), UrgencyOf(b, blocked))
	})
}
