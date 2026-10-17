package render

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// at is a time on the morning of the questions below, m minutes past ten.
func at(m int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 10, 16, 10, m, 0, 0, time.UTC))
}

// woke is a sleep that ended m minutes past ten.
func woke(m int) *planv1.QuestionSleep {
	return &planv1.QuestionSleep{CreateTime: at(0), WakeTime: at(m)}
}

// TestByUrgency: a question a waiting task needs comes first, whatever its words; then developer moves before what
// they unblock; then those needed before something; then those that can wait (including install proposals and proof
// checks). Each level keeps the order they were asked in, a question put off for later or woken from its sleep in its
// place (ByPlace).
func TestByUrgency(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		questions []*planv1.Question
		tasks     []*planv1.Task
		want      string
	}{
		{
			name: "levels",
			questions: []*planv1.Question{
				{Id: "a", Code: "Q01"},
				{Id: "b", Code: "Q02", Before: "before the merge"},
				{Id: "c", Code: "Q03"},
				{Id: "d", Code: "Q04", Before: "before the demo"},
				{Id: "e", Code: "Q05", Before: "before the release"},
				{Id: "m", Code: "Q06", Move: true, Before: "before the release"},
				{Id: "i", Code: "Q07", Move: true},
			},
			tasks: []*planv1.Task{
				{Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_WAITING, EditQuestionId: "e"},
				{Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, EditQuestionId: "c"},
			},
			want: "Q05 Q06 Q02 Q04 Q01 Q03 Q07 ",
		},
		{
			// Within a level, a question put off for later comes after those asked before it was put off, and before
			// one asked after; once a level apart, urgency still comes first.
			name: "put off for later",
			questions: []*planv1.Question{
				{Id: "a", Code: "Q01", CreateTime: at(1), LaterTime: at(5)},
				{Id: "b", Code: "Q02", CreateTime: at(2)},
				{Id: "c", Code: "Q03", CreateTime: at(3), Before: "before the merge", LaterTime: at(5)},
				{Id: "d", Code: "Q04", CreateTime: at(4), Before: "before the merge"},
				{Id: "e", Code: "Q05", CreateTime: at(6)},
				// A date before its creation changes nothing.
				{Id: "f", Code: "Q06", CreateTime: at(7), LaterTime: at(0)},
			},
			want: "Q04 Q03 Q02 Q01 Q05 Q06 ",
		},
		{
			// A question that woke from its sleep comes first among the open questions, the first woken first; one put
			// off after it woke goes back to its place, put off.
			name: "woken first",
			questions: []*planv1.Question{
				{Id: "a", Code: "Q01", CreateTime: at(1)},
				{Id: "b", Code: "Q02", CreateTime: at(2), Sleep: woke(9)},
				{Id: "c", Code: "Q03", CreateTime: at(3), Sleep: woke(8)},
				{Id: "d", Code: "Q04", CreateTime: at(4), Sleep: woke(6), LaterTime: at(7)},
				{Id: "e", Code: "Q05", CreateTime: at(5)},
			},
			want: "Q03 Q02 Q01 Q05 Q04 ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ByUrgency(tc.questions, tc.tasks)
			got := ""
			for _, q := range tc.questions {
				got += q.GetCode() + " "
			}
			if got != tc.want {
				t.Errorf("order = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUrgencyOf: a question a waiting task needs is blocking, by its edit question or by the blocking question its
// worker asked (W260), and no longer once the task is continued; a move needed before something is the developer's
// move; then before X; an install or proof move without before can wait, as a plain question does.
func TestUrgencyOf(t *testing.T) {
	t.Parallel()
	waiting := planv1.TaskStatus_TASK_STATUS_WAITING
	for _, tc := range []struct {
		name string
		q    *planv1.Question
		task *planv1.Task
		want Urgency
	}{
		{"edit question of a waiting task", &planv1.Question{Id: "e", Before: "before the release"},
			&planv1.Task{Code: "W1", Status: waiting, EditQuestionId: "e"}, Blocking},
		{"edit question of a running task", &planv1.Question{Id: "c"},
			&planv1.Task{Code: "W2", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, EditQuestionId: "c"}, Later},
		{"worker's question of a waiting task", &planv1.Question{Id: "w"},
			&planv1.Task{Code: "W3", Status: waiting, WaitQuestionId: "w"}, Blocking},
		{"worker's question once continued", &planv1.Question{Id: "w"},
			&planv1.Task{Code: "W3", Status: planv1.TaskStatus_TASK_STATUS_RESUMING, WaitQuestionId: "w"}, Later},
		{"move needed before", &planv1.Question{Id: "m", Move: true, Before: "before the release"}, nil, Move},
		{"needed before", &planv1.Question{Id: "b", Before: "before the merge"}, nil, Before},
		{"plain", &planv1.Question{Id: "a"}, nil, Later},
		{"move without before", &planv1.Question{Id: "i", Move: true}, nil, Later},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tasks []*planv1.Task
			if tc.task != nil {
				tasks = append(tasks, tc.task)
			}
			if got := UrgencyOf(tc.q, Blocked(tasks)); got != tc.want {
				t.Errorf("urgency %d, want %d", got, tc.want)
			}
		})
	}
}

// TestPlace: a question stands where it was asked, or where it was put off for later when that came after.
func TestPlace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		q    *planv1.Question
		want *timestamppb.Timestamp
	}{
		{"never put off", &planv1.Question{CreateTime: at(4), Before: "before the merge"}, at(4)},
		{"put off after", &planv1.Question{CreateTime: at(1), LaterTime: at(5)}, at(5)},
		{"put off before its creation", &planv1.Question{CreateTime: at(7), LaterTime: at(0)}, at(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Place(tc.q); !got.Equal(tc.want.AsTime()) {
				t.Errorf("Place = %v, want %v", got, tc.want.AsTime())
			}
		})
	}
}

// TestAsleep: a question put to sleep is out of the open ones until it wakes; an answered one waits for nothing.
func TestAsleep(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		q             *planv1.Question
		asleep, woken bool
	}{
		{"put to sleep", &planv1.Question{Code: "Q06", Sleep: &planv1.QuestionSleep{CreateTime: at(1)}}, true, false},
		{"answered in its sleep", &planv1.Question{Code: "Q06", Sleep: &planv1.QuestionSleep{CreateTime: at(1)},
			Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}}, false, false},
		{"woken", &planv1.Question{Code: "Q03", CreateTime: at(3), Sleep: woke(8)}, false, true},
		{"put off after it woke", &planv1.Question{Code: "Q04", CreateTime: at(4), Sleep: woke(6), LaterTime: at(7)}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if Asleep(tc.q) != tc.asleep || Woken(tc.q) != tc.woken {
				t.Errorf("asleep %v, woken %v; want asleep %v, woken %v", Asleep(tc.q), Woken(tc.q), tc.asleep, tc.woken)
			}
		})
	}
}
