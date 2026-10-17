package render

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

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

// TestByUrgencyPlace: within a level, a question put off for later comes after those asked before it was put off,
// and before one asked after; once a level apart, urgency still comes first.
func TestByUrgencyPlace(t *testing.T) {
	at := func(m int) *timestamppb.Timestamp {
		return timestamppb.New(time.Date(2026, 10, 16, 10, m, 0, 0, time.UTC))
	}
	questions := []*planv1.Question{
		{Id: "a", Code: "Q01", CreateTime: at(1), LaterTime: at(5)},
		{Id: "b", Code: "Q02", CreateTime: at(2)},
		{Id: "c", Code: "Q03", CreateTime: at(3), Before: "before the merge", LaterTime: at(5)},
		{Id: "d", Code: "Q04", CreateTime: at(4), Before: "before the merge"},
		{Id: "e", Code: "Q05", CreateTime: at(6)},
		// A date before its creation changes nothing.
		{Id: "f", Code: "Q06", CreateTime: at(7), LaterTime: at(0)},
	}
	ByUrgency(questions, nil)
	got := ""
	for _, q := range questions {
		got += q.GetCode() + " "
	}
	if want := "Q04 Q03 Q02 Q01 Q05 Q06 "; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
	if got := Place(questions[0]); !got.Equal(at(4).AsTime()) {
		t.Errorf("Place(Q04) = %v, want its creation", got)
	}
}

// TestByPlaceWoken: a question that woke from its sleep comes first among the open questions, the first woken first;
// one put off after it woke goes back to its place, put off; one asleep is out of the open ones (Asleep).
func TestByPlaceWoken(t *testing.T) {
	at := func(m int) *timestamppb.Timestamp {
		return timestamppb.New(time.Date(2026, 10, 16, 10, m, 0, 0, time.UTC))
	}
	woke := func(m int) *planv1.QuestionSleep { return &planv1.QuestionSleep{CreateTime: at(0), WakeTime: at(m)} }
	questions := []*planv1.Question{
		{Id: "a", Code: "Q01", CreateTime: at(1)},
		{Id: "b", Code: "Q02", CreateTime: at(2), Sleep: woke(9)},
		{Id: "c", Code: "Q03", CreateTime: at(3), Sleep: woke(8)},
		{Id: "d", Code: "Q04", CreateTime: at(4), Sleep: woke(6), LaterTime: at(7)},
		{Id: "e", Code: "Q05", CreateTime: at(5)},
	}
	ByUrgency(questions, nil)
	got := ""
	for _, q := range questions {
		got += q.GetCode() + " "
	}
	if want := "Q03 Q02 Q01 Q05 Q04 "; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
	asleep := &planv1.Question{Code: "Q06", Sleep: &planv1.QuestionSleep{CreateTime: at(1)}}
	if !Asleep(asleep) || Woken(asleep) {
		t.Errorf("a question put to sleep: asleep %v, woken %v; want asleep", Asleep(asleep), Woken(asleep))
	}
	asleep.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}
	if Asleep(asleep) {
		t.Error("an answered question is asleep, want awake: it waits for nothing")
	}
}
