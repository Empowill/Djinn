package render

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestByUrgency: a question a waiting task needs comes first, whatever its words; then developer moves before
// what they unblock; then those needed before something; then those that can wait (including install proposals and
// proof checks). Each level keeps the order they were asked in.
func TestByUrgency(t *testing.T) {
	questions := []*planv1.Question{
		{Id: "a", Code: "Q01"},
		{Id: "b", Code: "Q02", Before: "before the merge"},
		{Id: "c", Code: "Q03"},
		{Id: "d", Code: "Q04", Before: "before the demo"},
		{Id: "e", Code: "Q05", Before: "before the release"},
		{Id: "m", Code: "Q06", Move: true, Before: "before the release"},
		{Id: "i", Code: "Q07", Move: true},
	}
	tasks := []*planv1.Task{
		{Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_WAITING, EditQuestionId: "e"},
		{Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, EditQuestionId: "c"},
	}
	ByUrgency(questions, tasks)
	got := ""
	for _, q := range questions {
		got += q.GetCode() + " "
	}
	if want := "Q05 Q06 Q02 Q04 Q01 Q03 Q07 "; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
	blocked := Blocked(tasks)
	for _, c := range []struct {
		q    *planv1.Question
		want Urgency
	}{
		{questions[0], Blocking}, // Q05
		{questions[1], Move},     // Q06
		{questions[2], Before},   // Q02
		{questions[4], Later},    // Q01
		{questions[6], Later},    // Q07 (install/proof move without before)
	} {
		if got := UrgencyOf(c.q, blocked); got != c.want {
			t.Errorf("%s: urgency %d, want %d", c.q.GetCode(), got, c.want)
		}
	}
}
