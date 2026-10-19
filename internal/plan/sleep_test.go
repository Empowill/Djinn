package plan

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// TestSleepHolds: each kind of condition holds when what it waits for came, and only then: a task over (done, stopped,
// cancelled, deleted; an azima, closed), a request merged (by its babysit watcher, the task that opened it, its
// address, or an azima's pull request), a time come. A question wakes once all its conditions hold.
func TestSleepHolds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	task := func(id, code string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: id, WishId: "w", Code: code, Status: status}
	}
	azima := func(id string, state planv1.AzimaState, pr planv1.AzimaPrState) *planv1.Task {
		t := task(id, strings.ToUpper(id), planv1.TaskStatus_TASK_STATUS_DONE)
		t.Kind, t.Azima = planv1.TaskKind_TASK_KIND_AZIMA, &planv1.Azima{State: state, Pr: &planv1.AzimaPr{State: pr}}
		return t
	}
	watcher := func(id, url, openedBy string, merged bool) *planv1.Task {
		t := task(id, strings.ToUpper(id), planv1.TaskStatus_TASK_STATUS_RUNNING)
		t.Babysit = &planv1.TaskBabysit{Url: url, OpenedBy: openedBy, DoneWhen: "MERGED"}
		if merged {
			t.Babysit.DoneTime = timestamppb.New(now)
			t.Status = planv1.TaskStatus_TASK_STATUS_DONE
		}
		return t
	}
	const url = "https://gitlab.com/acme/lamp/-/merge_requests/3"
	tasks := []*planv1.Task{
		task("w1", "W1", planv1.TaskStatus_TASK_STATUS_RUNNING),
		task("w2", "W2", planv1.TaskStatus_TASK_STATUS_DONE),
		task("w3", "W3", planv1.TaskStatus_TASK_STATUS_STOPPED),
		task("w4", "W4", planv1.TaskStatus_TASK_STATUS_CANCELLED),
		task("w5", "W5", planv1.TaskStatus_TASK_STATUS_FAILED),
		azima("t6", planv1.AzimaState_AZIMA_STATE_IN_PROGRESS, planv1.AzimaPrState_AZIMA_PR_STATE_OPEN),
		azima("t7", planv1.AzimaState_AZIMA_STATE_DONE, planv1.AzimaPrState_AZIMA_PR_STATE_MERGED),
		watcher("w8", url, "W2", true),
		watcher("w9", "https://github.com/acme/lamp/pull/4", "W1", false),
		task("w10", "W10", planv1.TaskStatus_TASK_STATUS_DONE),
	}
	on := func(id string) *planv1.SleepCondition {
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TASK, TaskId: id}
	}
	merge := func(id, url string) *planv1.SleepCondition {
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, TaskId: id, Url: url}
	}
	at := func(d time.Duration) *planv1.SleepCondition {
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TIME, Time: timestamppb.New(now.Add(d))}
	}
	for _, c := range []struct {
		name string
		c    *planv1.SleepCondition
		want bool
	}{
		{"a task running", on("w1"), false},
		{"a task done", on("w2"), true},
		{"a task stopped", on("w3"), true},
		{"a task cancelled", on("w4"), true},
		{"a task failed", on("w5"), false},
		{"a task deleted", on("gone"), true},
		{"an azima under way", on("t6"), false},
		{"an azima closed", on("t7"), true},
		{"a request merged, by its watcher", merge("w8", ""), true},
		{"a request merged, by the task that opened it", merge("w2", ""), true},
		{"a request merged, by its address", merge("", url+"/"), true},
		{"a request open, by its watcher", merge("w9", ""), false},
		{"a request open, by the task that opened it", merge("w1", ""), false},
		{"a request open, by its address", merge("", "https://github.com/acme/lamp/pull/4"), false},
		{"no request of the task", merge("w10", ""), false},
		{"an azima's pull request open", merge("t6", ""), false},
		{"an azima's pull request merged", merge("t7", ""), true},
		{"a time past", at(-time.Minute), true},
		{"the time now", at(0), true},
		{"a time to come", at(time.Minute), false},
	} {
		if got := SleepHolds(c.c, tasks, now); got != c.want {
			t.Errorf("%s: holds %v, want %v", c.name, got, c.want)
		}
	}
	q := &planv1.Question{Sleep: &planv1.QuestionSleep{Conditions: []*planv1.SleepCondition{on("w2"), merge("w8", ""), at(time.Hour)}}}
	if SleepAllHold(q, tasks, now) {
		t.Error("all hold with a time to come, want not yet")
	}
	if !SleepAllHold(q, tasks, now.Add(time.Hour)) {
		t.Error("all hold once the time came, want so")
	}
	if SleepAllHold(&planv1.Question{Sleep: &planv1.QuestionSleep{}}, tasks, now) {
		t.Error("a sleep without conditions holds, want never")
	}
}

// TestQuestionSleep: a question put to sleep (djinn question sleep, ask and revise with --sleep-until) keeps its
// conditions typed, each task of its wish by code, and leaves the brief's open questions for its asleep ones; one that
// waits for nothing, or names what is not, is refused. Woken by hand, it comes first, says so, and the lead hears of it.
func TestQuestionSleep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t, WithLeads(&fakeLeads{}))
	wish := c.wish(t)
	running := testTask(wish, "W1", "Port the lamp", planv1.TaskStatus_TASK_STATUS_RUNNING)
	done := testTask(wish, "W2", "Polish the lamp", planv1.TaskStatus_TASK_STATUS_DONE)
	putTasks(t, c.store, running, done)
	ref := func(q *planv1.Question) *planv1.QuestionRef {
		return &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}
	}
	sleep := func(q *planv1.Question, until ...string) (*planv1.Question, error) {
		res, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
			Question: ref(q), WishId: wish, Until: until,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetQuestion(), nil
	}
	brief := func() string {
		t.Helper()
		b, err := BuildBrief(ctx, c.store, "", wish)
		if err != nil {
			t.Fatal(err)
		}
		return b.Text()
	}

	q1, q2 := c.ask(t, wish), c.ask(t, wish)
	asleep, err := sleep(q1, "w1, merged:W1", "https://github.com/acme/lamp/pull/12", "2026-10-20", "at:2026-10-20T09:30", "3d")
	if err != nil {
		t.Fatal(err)
	}
	conditions := asleep.GetSleep().GetConditions()
	kinds := ""
	for _, c := range conditions {
		kinds += strings.TrimPrefix(c.GetKind().String(), "SLEEP_KIND_") + ":" + c.GetCode() + " "
	}
	if want := "TASK:W1 MERGED:W1 MERGED: TIME: TIME: TIME: "; kinds != want {
		t.Errorf("conditions %q, want %q", kinds, want)
	}
	if conditions[0].GetTaskId() != running.GetId() || conditions[2].GetUrl() != "https://github.com/acme/lamp/pull/12" {
		t.Errorf("conditions %v: want W1 by identifier, the address kept", conditions)
	}
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.Local)
	if got := conditions[3].GetTime().AsTime(); !got.Equal(day) {
		t.Errorf("2026-10-20 = %v, want midnight on this machine's clock", got)
	}
	if got := conditions[4].GetTime().AsTime(); !got.Equal(day.Add(9*time.Hour + 30*time.Minute)) {
		t.Errorf("at:2026-10-20T09:30 = %v", got)
	}
	if got := time.Until(conditions[5].GetTime().AsTime()); got < 71*time.Hour || got > 72*time.Hour {
		t.Errorf("3d is %v from now, want 72h", got)
	}
	if !render.Asleep(asleep) || asleep.GetSleep().GetCreateTime() == nil {
		t.Errorf("sleep %v, want asleep since now", asleep.GetSleep())
	}
	text := brief()
	if strings.Contains(text, "**"+q1.GetCode()+"** Which store? (can wait)") ||
		!strings.Contains(text, "## Asleep questions") ||
		!strings.Contains(text, "- **"+q1.GetCode()+"** Which store? (until W1 over, W1's request merged, "+
			"https://github.com/acme/lamp/pull/12 merged, 2026-10-20 00:00, 2026-10-20 09:30") {
		t.Errorf("the brief lists %s asleep, not open:\n%s", q1.GetCode(), text)
	}

	for until, want := range map[string]connect.Code{
		"W9":            connect.CodeNotFound,
		"soon:W1":       connect.CodeInvalidArgument,
		"2026-13-45":    connect.CodeInvalidArgument,
		"-2h":           connect.CodeInvalidArgument,
		",":             connect.CodeInvalidArgument,
		"W2":            connect.CodeFailedPrecondition, // Done already: nothing to sleep until.
		"2020-01-01,W2": connect.CodeFailedPrecondition,
	} {
		if _, err := sleep(q2, until); code(err) != want {
			t.Errorf("sleep until %q: %v, want %v", until, err, want)
		}
	}
	if _, err := sleep(q2, "W2", "W1"); err != nil {
		t.Errorf("sleep until a task done and one running: %v, want asleep", err)
	}

	// Woken by hand: first among the open questions, the lead told to check it.
	res, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
		Question: ref(q2), WishId: wish, Wake: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	woken := res.Msg.GetQuestion()
	if render.Asleep(woken) || !render.Woken(woken) || !woken.GetSleep().GetByHand() {
		t.Errorf("sleep %v once woken by hand", woken.GetSleep())
	}
	c.ask(t, wish)
	if _, err := sleep(q1, "W1"); err != nil {
		t.Fatal(err)
	}
	text = brief()
	if i := strings.Index(text, "## Open questions\n\n- **"+q2.GetCode()+"** Which store? (can wait)"); i < 0 ||
		!strings.Contains(text, "  - Woke ") {
		t.Errorf("the brief lists %s first, woken:\n%s", q2.GetCode(), text)
	}
	events, err := store.List[*planv1.LeadEvent](ctx, c.store, store.Where{"wish_id": wish})
	if err != nil {
		t.Fatal(err)
	}
	var woke []string
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_WOKE {
			woke = append(woke, e.GetCode()+": "+e.GetText())
		}
	}
	if len(woke) != 1 || !strings.HasPrefix(woke[0], q2.GetCode()+": Djinn: "+q2.GetCode()+
		" woke, the developer woke it before W2 over, W1 over. Check that its facts still hold") {
		t.Errorf("the lead heard %q, want that %s woke", woke, q2.GetCode())
	}
	if _, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
		Question: ref(q2), WishId: wish, Wake: true,
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("wake a question awake: %v, want FailedPrecondition", err)
	}
	if _, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
		Question: ref(q2), WishId: wish,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("sleep without until nor wake: %v, want InvalidArgument", err)
	}
}

// TestQuestionAskedAsleep: djinn question ask --sleep-until asks it asleep; djinn question revise --sleep-until puts a
// question to sleep again, revised, its put-off date gone, and a decided one is not put to sleep.
func TestQuestionAskedAsleep(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	putTasks(t, c.store, testTask(wish, "W1", "Port the lamp", planv1.TaskStatus_TASK_STATUS_RUNNING))
	res, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Merge it now?", WishId: wish, SleepUntil: []string{"W1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := res.Msg.GetQuestion()
	if !render.Asleep(q) || q.GetSleep().GetConditions()[0].GetCode() != "W1" {
		t.Errorf("asked asleep: sleep %v, want until W1", q.GetSleep())
	}
	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}}
	if _, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
		Question: ref, WishId: wish, Wake: true,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.questions.Later(ctx, connect.NewRequest(&planv1.QuestionServiceLaterRequest{Question: ref, WishId: wish})); err != nil {
		t.Fatal(err)
	}
	revised, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: "W1 still runs.", SleepUntil: []string{"2h"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q = revised.Msg.GetQuestion()
	if !render.Asleep(q) || q.GetSleep().GetConditions()[0].GetKind() != planv1.SleepKind_SLEEP_KIND_TIME ||
		q.GetLaterTime() != nil || q.GetRevision() != 1 || q.GetContext() != "W1 still runs." {
		t.Errorf("revised asleep: %v", q)
	}
	if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: ref, WishId: wish, Choice: planv1.Choice_CHOICE_YES,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.questions.Sleep(ctx, connect.NewRequest(&planv1.QuestionServiceSleepRequest{
		Question: ref, WishId: wish, Until: []string{"2h"},
	})); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("sleep a decided question: %v, want FailedPrecondition", err)
	}
}
