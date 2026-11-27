package harness

import (
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// TestQuestionsWakeWhenTheirConditionsHold: the scheduler's pass wakes an asleep question once every condition it
// sleeps until holds, and not before: a task over, an azima closed once its last part is done, a request merged by
// its babysit watcher, a time come, and several combined. Each one woken is journaled, its put-off date gone, and its
// lead hears of it once, with what held; an answered one is left asleep as it is.
func TestQuestionsWakeWhenTheirConditionsHold(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h := New(db, t.TempDir(), testProviders(), WithClock(clock))
	t.Cleanup(func() { h.Close(); db.Close() })
	queue := &leadQueue{}
	h.NotifyLead(queue.notify)
	h.watchSleepers()

	wish := &planv1.Wish{Id: store.NewID(), Title: "Light the lamp", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := func(code string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: store.NewID(), WishId: wish.GetId(), Code: code, Status: status,
			CreateTime: timestamppb.New(now)}
	}
	w1 := task("W1", planv1.TaskStatus_TASK_STATUS_PAUSED)
	azima := task("T2", planv1.TaskStatus_TASK_STATUS_DONE)
	azima.Kind = planv1.TaskKind_TASK_KIND_AZIMA
	part := task("W3", planv1.TaskStatus_TASK_STATUS_PAUSED)
	part.PartOf = azima.GetId()
	opener := task("W4", planv1.TaskStatus_TASK_STATUS_DONE)
	babysit := task("W5", planv1.TaskStatus_TASK_STATUS_PAUSED)
	babysit.Provider = planv1.Provider_PROVIDER_WATCH
	babysit.Babysit = &planv1.TaskBabysit{Url: "https://github.com/acme/lamp/pull/7", OpenedBy: "W4", DoneWhen: "MERGED"}
	on := func(t *planv1.Task) *planv1.SleepCondition {
		return &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TASK, TaskId: t.GetId(), Code: t.GetCode()}
	}
	merged := &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, TaskId: opener.GetId(), Code: "W4"}
	inAnHour := &planv1.SleepCondition{Kind: planv1.SleepKind_SLEEP_KIND_TIME, Time: timestamppb.New(now.Add(time.Hour))}
	n := 0
	question := func(conditions ...*planv1.SleepCondition) *planv1.Question {
		n++
		return &planv1.Question{
			Id: store.NewID(), WishId: wish.GetId(), Code: "Q0" + string(rune('0'+n)), Text: "Go on?",
			CreateTime: timestamppb.New(now), LaterTime: timestamppb.New(now),
			Sleep: &planv1.QuestionSleep{Conditions: conditions, CreateTime: timestamppb.New(now)},
		}
	}
	q1, q2, q3, q4 := question(on(w1)), question(on(azima)), question(merged), question(inAnHour)
	q5, q6 := question(on(w1), inAnHour, merged), question(on(w1))
	q6.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_YES}
	write := func(ms ...proto.Message) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("test", "test/put", wish); err != nil {
				return err
			}
			for _, m := range ms {
				if err := tx.Put(m); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(wish, w1, azima, part, opener, babysit, q1, q2, q3, q4, q5, q6)

	// awake passes once, then says the codes of the questions awake, among the open ones put to sleep.
	awake := func() string {
		t.Helper()
		h.schedule(ctx)
		var codes []string
		for _, q := range []*planv1.Question{q1, q2, q3, q4, q5, q6} {
			stored, err := store.Get[*planv1.Question](ctx, db, q.GetId())
			if err != nil {
				t.Fatal(err)
			}
			if !render.Asleep(stored) && stored.GetAnswer() == nil {
				codes = append(codes, stored.GetCode())
			}
		}
		return strings.Join(codes, " ")
	}
	set := func(t *planv1.Task, status planv1.TaskStatus) {
		t.Status = status
		write(t)
	}

	if got := awake(); got != "" {
		t.Fatalf("awake before anything held: %q, want none", got)
	}
	set(w1, planv1.TaskStatus_TASK_STATUS_DONE)
	if got := awake(); got != "Q01" {
		t.Errorf("awake once W1 is done: %q, want Q01", got)
	}
	woke, err := store.Get[*planv1.Question](ctx, db, q1.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !woke.GetSleep().GetWakeTime().AsTime().Equal(now) || woke.GetSleep().GetByHand() || woke.GetLaterTime() != nil ||
		!render.Woken(woke) {
		t.Errorf("Q01 woke as %v, put off %v: want woken now by its conditions, its put-off date gone",
			woke.GetSleep(), woke.GetLaterTime())
	}
	set(part, planv1.TaskStatus_TASK_STATUS_DONE)
	if got := awake(); got != "Q01 Q02" {
		t.Errorf("awake once the azima's last part is done: %q, want Q01 Q02", got)
	}
	babysit.Babysit.DoneTime = timestamppb.New(now)
	set(babysit, planv1.TaskStatus_TASK_STATUS_DONE)
	if got := awake(); got != "Q01 Q02 Q03" {
		t.Errorf("awake once the watcher saw the request merged: %q, want Q01 Q02 Q03", got)
	}
	mu.Lock()
	now = now.Add(time.Hour)
	mu.Unlock()
	if got := awake(); got != "Q01 Q02 Q03 Q04 Q05" {
		t.Errorf("awake an hour later: %q, want every open one", got)
	}
	if got := awake(); got != "Q01 Q02 Q03 Q04 Q05" {
		t.Errorf("awake after one more pass: %q", got)
	}
	var lines []string
	for _, e := range queue.all() {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_WOKE {
			lines = append(lines, e.GetCode()+" "+strings.SplitN(e.GetText(), ". ", 2)[0])
		}
	}
	want := []string{
		"Q01 Djinn: Q01 woke, its conditions hold: W1 over",
		"Q02 Djinn: Q02 woke, its conditions hold: T2 over",
		"Q03 Djinn: Q03 woke, its conditions hold: W4's request merged",
		"Q04 Djinn: Q04 woke, its conditions hold: " + now.Local().Format("2006-01-02 15:04"),
		"Q05 Djinn: Q05 woke, its conditions hold: W1 over, " + now.Local().Format("2006-01-02 15:04") + ", W4's request merged",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("the lead heard:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if h.sleepers.Load() {
		t.Error("the scheduler still reads the questions with none asleep")
	}
}

// TestQuestionsAsleepOnWhatCanNeverCome: a question asleep until a task's request is merged does not wake once that
// can never hold; it sleeps on, out of the developer's questions, and its lead hears once, at the scheduler's pass,
// which condition and why, with both commands that settle it: the task was cancelled (Q118 on W162), or it ended done
// and, plan.OpenedGrace later, Djinn knows no request it opened (Q139 on W233, its request opened outside Djinn). The
// lead decides: withdrawn, or woken by hand, the question is left so. Before that grace, or while a watcher babysits
// what it opened, it sleeps on untold; a task continued takes the mark off; a request merged wakes its question as
// before (W1048, Q670).
func TestQuestionsAsleepOnWhatCanNeverCome(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h := New(db, t.TempDir(), testProviders(), WithClock(clock))
	t.Cleanup(func() { h.Close(); db.Close() })
	queue := &leadQueue{}
	h.NotifyLead(queue.notify)
	h.watchSleepers()

	wish := &planv1.Wish{Id: store.NewID(), Title: "Dew", State: planv1.WishState_WISH_STATE_ACTIVE}
	task := func(code string) *planv1.Task {
		return &planv1.Task{Id: store.NewID(), WishId: wish.GetId(), Code: code, Status: planv1.TaskStatus_TASK_STATUS_PAUSED,
			CreateTime: timestamppb.New(now)}
	}
	w162, w233, w240, w250, w170 := task("W162"), task("W233"), task("W240"), task("W250"), task("W170")
	watcher := task("W241")
	watcher.Provider = planv1.Provider_PROVIDER_WATCH
	watcher.Babysit = &planv1.TaskBabysit{Url: "https://gitlab.com/acme/dew/-/merge_requests/8", OpenedBy: "W240", DoneWhen: "MERGED"}
	merging := task("W251")
	merging.Provider = planv1.Provider_PROVIDER_WATCH
	merging.Babysit = &planv1.TaskBabysit{Url: "https://gitlab.com/acme/dew/-/merge_requests/9", OpenedBy: "W250", DoneWhen: "MERGED"}
	question := func(code string, on *planv1.Task) *planv1.Question {
		return &planv1.Question{
			Id: store.NewID(), WishId: wish.GetId(), Code: code, Text: "Release now?", CreateTime: timestamppb.New(now),
			Sleep: &planv1.QuestionSleep{CreateTime: timestamppb.New(now), Conditions: []*planv1.SleepCondition{
				{Kind: planv1.SleepKind_SLEEP_KIND_MERGED, TaskId: on.GetId(), Code: on.GetCode()},
			}},
		}
	}
	q118, q139, q150, q160, q170 := question("Q118", w162), question("Q139", w233), question("Q150", w240),
		question("Q160", w250), question("Q170", w170)
	all := []*planv1.Question{q118, q139, q150, q160, q170}
	write := func(ms ...proto.Message) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("test", "test/put", wish); err != nil {
				return err
			}
			for _, m := range ms {
				if err := tx.Put(m); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(wish, w162, w233, w240, w250, w170, watcher, merging, q118, q139, q150, q160, q170)
	get := func(q *planv1.Question) *planv1.Question {
		t.Helper()
		stored, err := store.Get[*planv1.Question](ctx, db, q.GetId())
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	// pass runs the scheduler once, then says the codes of the questions the developer sees (neither asleep, nor
	// withdrawn), and those whose sleep carries a mark that it can never end.
	pass := func() (shown, never string) {
		t.Helper()
		h.schedule(ctx)
		var s, n []string
		for _, q := range all {
			stored := get(q)
			if !render.Asleep(stored) && stored.GetWithdrawal() == nil {
				s = append(s, stored.GetCode())
			}
			if stored.GetSleep().GetNever() != "" {
				n = append(n, stored.GetCode())
			}
		}
		return strings.Join(s, " "), strings.Join(n, " ")
	}
	// heard says the lead's events, a line each: their kind, code and first sentence.
	heard := func() []string {
		var lines []string
		for _, e := range queue.all() {
			kind := strings.TrimPrefix(e.GetKind().String(), "LEAD_EVENT_KIND_")
			lines = append(lines, kind+" "+e.GetCode()+" "+strings.SplitN(e.GetText(), ". ", 2)[0])
		}
		return lines
	}
	end := func(task *planv1.Task, status planv1.TaskStatus) {
		task.Status, task.EndTime = status, timestamppb.New(now)
		write(task)
	}
	check := func(when, shown, never string, events ...string) {
		t.Helper()
		gotShown, gotNever := pass()
		if gotShown != shown || gotNever != never {
			t.Errorf("%s: shown %q, never %q; want shown %q, never %q", when, gotShown, gotNever, shown, never)
		}
		if got := heard(); strings.Join(got, "\n") != strings.Join(events, "\n") {
			t.Errorf("%s: the lead heard:\n%s\nwant:\n%s", when, strings.Join(got, "\n"), strings.Join(events, "\n"))
		}
	}

	check("every task paused", "", "")
	end(w162, planv1.TaskStatus_TASK_STATUS_CANCELLED)
	end(w233, planv1.TaskStatus_TASK_STATUS_DONE)
	end(w240, planv1.TaskStatus_TASK_STATUS_DONE)
	never118 := "NEVER Q118 Djinn: Q118 sleeps on what can never come: W162's request merged can never hold, W162 was cancelled"
	check("W162 cancelled, W233 and W240 just done", "", "Q118", never118)
	if line := queue.all()[0].GetText(); !strings.Contains(line, "`djinn question withdraw Q118 --note \"why\"`") ||
		!strings.Contains(line, "`djinn question sleep Q118 --wake`") {
		t.Errorf("the lead is not given both commands: %q", line)
	}
	check("one more pass", "", "Q118", never118)

	mu.Lock()
	now = now.Add(plan.OpenedGrace)
	mu.Unlock()
	never139 := "NEVER Q139 Djinn: Q139 sleeps on what can never come: W233's request merged can never hold, W233 ended " +
		"done and Djinn knows no pull or merge request it opened"
	check(plan.OpenedGrace.String()+" after W233 ended with no request known", "", "Q118 Q139", never118, never139)

	merging.Babysit.DoneTime = timestamppb.New(now)
	end(merging, planv1.TaskStatus_TASK_STATUS_DONE)
	woke160 := "WOKE Q160 Djinn: Q160 woke, its conditions hold: W250's request merged"
	check("W250's request merged", "Q160", "Q118 Q139", never118, never139, woke160)

	end(w170, planv1.TaskStatus_TASK_STATUS_CANCELLED)
	never170 := "NEVER Q170 Djinn: Q170 sleeps on what can never come: W170's request merged can never hold, W170 was cancelled"
	check("W170 cancelled", "Q160", "Q118 Q139 Q170", never118, never139, woke160, never170)
	w170.Status, w170.EndTime = planv1.TaskStatus_TASK_STATUS_PAUSED, nil
	write(w170)
	check("W170 continued", "Q160", "Q118 Q139", never118, never139, woke160, never170)

	// The lead decides: Q118 matters still, it wakes it for the developer; Q139 no longer does, it withdraws it.
	woken := get(q118)
	plan.Wake(woken, now, true)
	withdrawn := get(q139)
	withdrawn.Withdrawal = &planv1.Withdrawal{Note: "released without W233's request", CreateTime: timestamppb.New(now)}
	write(woken, withdrawn)
	check("Q118 woken by the lead, Q139 withdrawn", "Q118 Q160", "Q118 Q139", never118, never139, woke160, never170)
	if line := plan.WokeLine(get(q118)); !strings.HasPrefix(line,
		"Djinn: Q118 woke, woken by hand, though W162's request merged can never hold, W162 was cancelled. ") {
		t.Errorf("the lead hears, once Q118 is woken by hand: %q", line)
	}
	if !h.sleepers.Load() {
		t.Error("the scheduler no longer reads Q150, asleep on W240's request")
	}
}
