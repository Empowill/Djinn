package plan

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// quick is the timing of the lead's watch in these tests.
var quick = watchTiming{quiet: 50 * time.Millisecond, settleMax: 400 * time.Millisecond, grace: 150 * time.Millisecond, retype: time.Hour,
	remind: 50 * time.Millisecond}

// syncLeads is a fakeLeads safe for the reminder's goroutine.
type syncLeads struct {
	mu sync.Mutex
	f  fakeLeads
}

func (s *syncLeads) Open(name, line, dir, exclusive string) ([]string, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Open(name, line, dir, exclusive)
}
func (s *syncLeads) Show(wishID, terminal string) {}
func (s *syncLeads) Running(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Running(name)
}
func (s *syncLeads) Command(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.f.Command(name))
}
func (s *syncLeads) Close(name string) {}
func (s *syncLeads) Say(name, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Say(name, line)
}
func (s *syncLeads) SayText(name, text string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.SayText(name, text)
}
func (s *syncLeads) Prompt(name string) *planv1.LeadPrompt { return nil }
func (s *syncLeads) Exit(name string) *planv1.LeadExit     { return nil }
func (s *syncLeads) Choose(name, title string, lines, options []string, index int) error {
	return ErrNoChoice
}
func (s *syncLeads) said() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.f.said)
}

// leadWatchFixture is a store with a recorded lead of provider, and the lead service on it.
func leadWatchFixture(t *testing.T, provider planv1.Provider) (*Wishes, *LeadService, *syncLeads) {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	w := &Wishes{Store: s}
	w.leadWatch().timing = quick
	t.Cleanup(func() { w.leadWatch().stop(); s.Close(); leadWatches.Delete(s) })
	lead := &planv1.Lead{Id: "lead", Provider: provider, SessionId: session, Directory: t.TempDir()}
	if err := s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/lead", lead); err != nil {
			return err
		}
		return tx.Put(lead)
	}); err != nil {
		t.Fatal(err)
	}
	leads := &syncLeads{}
	w.Leads = leads
	return w, &LeadService{Wishes: w}, leads
}

func notify(t *testing.T, w *Wishes, kind planv1.LeadEventKind, code, text string) {
	t.Helper()
	if err := w.Notify(context.Background(), &planv1.LeadEvent{Kind: kind, Code: code, Text: text}); err != nil {
		t.Error(err)
	}
}

func leadWatchOnce(t *testing.T, s *LeadService, req *planv1.LeadServiceWatchRequest) string {
	t.Helper()
	res, err := s.Watch(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetText()
}

// cursorOf is the cursor a digest prints.
func cursorOf(t *testing.T, digest string) string {
	t.Helper()
	_, rest, ok := strings.Cut(digest, "Cursor ")
	if !ok {
		t.Fatalf("no cursor in the digest:\n%s", digest)
	}
	return strings.TrimSuffix(strings.Fields(rest)[0], ".")
}

// TestLeadWatchBatches: events that come close together come in one digest, a line each, with the cursor.
func TestLeadWatchBatches(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	// The events come 30 ms apart; the watch waits for 500 ms without one. With quick's 50 ms, a slow runner (Windows
	// CI) let the quiet pass between two of them: two events in the digest. A quiet of 1 ms reproduces it.
	timing := quick
	timing.quiet, timing.settleMax = 500*time.Millisecond, 10*time.Second
	w.leadWatch().timing = timing
	ended := make(chan struct{})
	defer func() { <-ended }() // A watch that failed early leaves no notify to a test that ended.
	go func() {
		defer close(ended)
		time.Sleep(30 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W1", "Djinn: W1 merged into main as 0123abcd")
		time.Sleep(30 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, "W2", "Djinn: W2 (Lamp) failed (claude): exit 1. The move is yours.")
		time.Sleep(30 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q3", `Djinn: Q3 answered B — "Oil".`)
	}()
	start := time.Now()
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{})
	for _, want := range []string{
		"3 events for the lead:\n",
		"- W1 merged: merged into main as 0123abcd\n",
		"- W2 failed: (Lamp) failed (claude): exit 1. The move is yours.\n",
		`- Q3 answer: answered B — "Oil".` + "\n",
		"Next: djinn lead watch --after ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest lacks %q:\n%s", want, got)
		}
	}
	if took := time.Since(start); took < 90*time.Millisecond+timing.quiet {
		t.Errorf("the watch returned after %s, before the events settled", took)
	}
}

// TestLeadWatchSettleIsCapped: events that never pause are returned after quick.settleMax anyway. It measures an upper
// bound of time: it runs alone, out of the parallel tests' contention.
func TestLeadWatchSettleIsCapped(t *testing.T) {
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	// The ticks end before the test does: one notified after it ended, the store closed by its cleanup, failed a test
	// that had completed, a panic that ended the package's test binary (W530, Windows CI).
	stop, ended := make(chan struct{}), make(chan struct{})
	defer func() { close(stop); <-ended }()
	go func() {
		defer close(ended)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "", "Djinn: tick")
			}
		}
	}()
	start := time.Now()
	leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{})
	if took := time.Since(start); took > quick.settleMax+300*time.Millisecond {
		t.Errorf("the watch took %s with events that never pause, want about %s", took, quick.settleMax)
	}
}

// TestLeadWatchAcknowledgesByCursor: --after acknowledges every event up to the cursor for the session, and only
// those; the next watch returns the rest.
func TestLeadWatchAcknowledgesByCursor(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W1", "Djinn: W1 merged")
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W2", "Djinn: W2 merged")
	first := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{})
	cursor := cursorOf(t, first)
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, "W3", "Djinn: W3 failed")
	second := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursor})
	if !strings.Contains(second, "1 event for the lead:\n- W3 failed") || strings.Contains(second, "W1") {
		t.Errorf("after the cursor, want W3 alone:\n%s", second)
	}
	events, err := store.List[*planv1.LeadEvent](t.Context(), w.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		acked := e.GetCode() != "W3"
		if (e.GetAckSessionId() == session) != acked || (e.GetAckTime() != nil) != acked {
			t.Errorf("%s: acknowledged by %q at %v, want acknowledged %v", e.GetCode(), e.GetAckSessionId(), e.GetAckTime(), acked)
		}
	}
	// A timeout of 0 returns at once, with nothing waiting past the cursor once W3 is acknowledged.
	empty := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, second), Timeout: durationpb.New(0)})
	if !strings.HasPrefix(empty, "No event for the lead in 0s. Run djinn lead watch --after ") {
		t.Errorf("empty digest:\n%s", empty)
	}
}

// TestLeadWatchRedelivers: a digest whose cursor never came back (the lead crashed, its session was lost) is
// printed again by the next watch: at least once, never lost.
func TestLeadWatchRedelivers(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q1", "Djinn: Q1 answered yes.")
	lost := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{})
	// The session is lost: the next lead watches without the cursor.
	again := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(time.Second)})
	if !strings.Contains(again, "- Q1 answer: answered yes.") || cursorOf(t, again) != cursorOf(t, lost) {
		t.Errorf("the lost event did not come back:\n%s", again)
	}
	// A watch that was cut before it printed anything (its client went away) acknowledges nothing either.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{})); err == nil {
		t.Error("a watch whose client went away returned no error")
	}
	if events, _ := waitingEvents(t.Context(), w.Store); len(events) != 1 {
		t.Errorf("%d events wait, want Q1 still", len(events))
	}
}

// TestLeadWatchRefusesOthers: the events are the lead's: another session and a worker are refused.
func TestLeadWatchRefusesOthers(t *testing.T) {
	t.Parallel()
	_, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	_, err := s.Watch(t.Context(), connect.NewRequest(&planv1.LeadServiceWatchRequest{Session: "someone-else"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "djinn lead set") {
		t.Errorf("another session: %v", err)
	}
	ctx := WithCaller(t.Context(), &WorkerCaller{TaskID: "t1", WishID: "w1"})
	if _, err := s.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a worker: %v", err)
	}
	// The lead's own session, named, is served.
	leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Session: session, Timeout: durationpb.New(0)})
}

// TestLeadWatchFallbackLine: without a watch, Djinn types one short line once the events settle; with one waiting,
// nothing; a codex lead, which no finished command wakes up, gets the line even then. Its cases measure upper bounds of
// time: they run beside each other only, out of the other tests' contention.
func TestLeadWatchFallbackLine(t *testing.T) {
	waitSaid := func(leads *syncLeads, n int) []string {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && len(leads.said()) < n {
			time.Sleep(10 * time.Millisecond)
		}
		return leads.said()
	}

	t.Run("no watch", func(t *testing.T) {
		t.Parallel()
		w, _, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W1", "Djinn: W1 merged")
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W2", "Djinn: W2 merged")
		if said := waitSaid(leads, 1); !slices.Equal(said, []string{"lead: Djinn: 2 events waiting, run djinn lead watch"}) {
			t.Errorf("typed %q, want one line for both events", said)
		}
		// More events while the lead has not watched yet: the line is not typed again.
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W3", "Djinn: W3 merged")
		time.Sleep(4 * quick.quiet)
		if said := leads.said(); len(said) != 1 {
			t.Errorf("typed %q, want the line once", said)
		}
	})

	t.Run("a watch waits", func(t *testing.T) {
		t.Parallel()
		w, s, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
		done := make(chan string)
		go func() { done <- leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{}) }()
		time.Sleep(20 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q1", "Djinn: Q1 answered yes.")
		got := <-done
		if !strings.Contains(got, "Q1 answer") {
			t.Errorf("digest:\n%s", got)
		}
		// The lead runs its next watch within the grace: still nothing typed. The check done, the watch is cut rather
		// than waited for.
		ctx, cancel := context.WithCancel(t.Context())
		after := cursorOf(t, got)
		go func() {
			_, _ = s.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{After: after, Timeout: durationpb.New(time.Minute)}))
			close(done)
		}()
		time.Sleep(quick.grace + 3*quick.quiet)
		if said := leads.said(); len(said) != 0 {
			t.Errorf("typed %q while a watch waited", said)
		}
		cancel()
		<-done
	})

	t.Run("the watch does not come back", func(t *testing.T) {
		t.Parallel()
		w, s, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q1", "Djinn: Q1 answered yes.")
		leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{}) // The lead read it, then crashed before its next watch.
		start := time.Now()
		if said := waitSaid(leads, 1); !slices.Equal(said, []string{"lead: Djinn: 1 event waiting, run djinn lead watch"}) {
			t.Errorf("typed %q", said)
		}
		if took := time.Since(start); took < quick.grace-20*time.Millisecond {
			t.Errorf("typed after %s, within the grace %s", took, quick.grace)
		}
	})

	t.Run("a watch that returns at once promises none", func(t *testing.T) {
		t.Parallel()
		w, s, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CODEX)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W1", "Djinn: W1 merged")
		if said := waitSaid(leads, 1); len(said) != 1 {
			t.Fatalf("typed %q", said)
		}
		got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})
		leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(0)})
		// The lead watched: the next event gets its line, at once, without a grace.
		start := time.Now()
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W2", "Djinn: W2 merged")
		if said := waitSaid(leads, 2); len(said) != 2 || time.Since(start) > quick.grace {
			t.Errorf("typed %q after %s", said, time.Since(start))
		}
	})

	t.Run("codex", func(t *testing.T) {
		t.Parallel()
		w, s, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CODEX)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			_, _ = s.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{Timeout: durationpb.New(time.Minute)}))
		}()
		time.Sleep(20 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, "W1", "Djinn: W1 failed")
		if said := waitSaid(leads, 1); len(said) != 1 {
			t.Errorf("typed %q to a codex lead, want the line", said)
		}
	})
}

// TestLeadWatchAcknowledgedAnswer: an answer that waited for a lead stops waiting once the lead acknowledges it.
func TestLeadWatchAcknowledgedAnswer(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	q := &planv1.Question{Id: store.NewID(), WishId: store.NewID(), Code: "Q7", WaitingLead: true}
	if err := w.Store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/question", q); err != nil {
			return err
		}
		return tx.Put(q)
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Notify(t.Context(), &planv1.LeadEvent{
		Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, WishId: q.GetWishId(), Code: "Q7", Text: "Djinn: Q7 answered yes.",
	}); err != nil {
		t.Fatal(err)
	}
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{})
	if q, _ := store.Get[*planv1.Question](t.Context(), w.Store, q.GetId()); !q.GetWaitingLead() {
		t.Error("the answer stopped waiting before the lead acknowledged it")
	}
	leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(0)})
	if q, _ := store.Get[*planv1.Question](t.Context(), w.Store, q.GetId()); q.GetWaitingLead() {
		t.Error("the answer still waits for a lead after the lead acknowledged it")
	}
}

// TestDigestCapsLines: a digest prints digestMax events at most, its cursor on the last one printed.
func TestDigestCapsLines(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	for range digestMax + 5 {
		if err := QueueLeadEvent(t.Context(), w.Store, &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED,
			Text: "Djinn: merged"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})
	if strings.Count(got, "\n- ") != digestMax || !strings.Contains(got, "5 more wait: the next watch returns them at once.") {
		t.Errorf("digest:\n%s", got)
	}
	next := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(0)})
	if strings.Count(next, "\n- ") != 5 {
		t.Errorf("next digest:\n%s", next)
	}
}

// TestApprovalComesBackInLeadWatch: the developer's approval of a decision the lead or a worker took comes back in
// djinn lead watch, once. Approving an open question is its answer, and approving the developer's own decision tells
// nothing (decision Q173).
func TestApprovalComesBackInLeadWatch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Approvals", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	leads.running = map[string][]string{LeadTerminal: {"/bin/sh", "-c", "claude --resume " + session}}
	task := store.NewID()
	c.put(t, &planv1.Task{Id: task, WishId: wish, Code: "W01", Status: planv1.TaskStatus_TASK_STATUS_RUNNING})
	answer := func(q *planv1.Question, req *planv1.QuestionServiceAnswerRequest) {
		t.Helper()
		req.Question, req.Choice = &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: q.GetId()}}, planv1.Choice_CHOICE_B
		if _, err := c.questions.Answer(ctx, connect.NewRequest(req)); err != nil {
			t.Fatal(err)
		}
	}
	approve := func(q *planv1.Question) {
		t.Helper()
		if _, err := c.marks.Put(ctx, connect.NewRequest(&planv1.MarkServicePutRequest{
			Target: &planv1.MarkTarget{Ref: &planv1.MarkTarget_Id{Id: q.GetId()}}, Kind: planv1.MarkKind_MARK_KIND_APPROVED,
			WishId: wish,
		})); err != nil {
			t.Fatal(err)
		}
	}
	byLead := c.ask(t, wish, "Olive", "Paraffin")
	answer(byLead, &planv1.QuestionServiceAnswerRequest{Lead: wish})
	byWorker := c.ask(t, wish, "Olive", "Paraffin")
	answer(byWorker, &planv1.QuestionServiceAnswerRequest{TaskId: task})
	byYou := c.ask(t, wish, "Olive", "Paraffin")
	answer(byYou, &planv1.QuestionServiceAnswerRequest{By: planv1.Closer_CLOSER_DEVELOPER})
	take(t, c.store)

	// The developer approves in the window each decision, the lead's and the worker's twice.
	for _, q := range []*planv1.Question{byLead, byLead, byWorker, byWorker, byYou} {
		approve(q)
	}
	want := []string{
		LeadTerminal + `: Djinn: Q01 approved by the developer, as you answered it: B — "Paraffin". The decision is theirs now.`,
		LeadTerminal + `: Djinn: Q02 approved by the developer, as W01 answered it: B — "Paraffin". The decision is theirs now.`,
	}
	if got := told(t, c.store); !slices.Equal(got, want) {
		t.Fatalf("queued %q, want %q", got, want)
	}
	res, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)}))
	if err != nil {
		t.Fatal(err)
	}
	digest := res.Msg.GetText()
	for _, line := range []string{
		`- Q01 [Approvals] approved: approved by the developer, as you answered it: B — "Paraffin". The decision is theirs now.`,
		`- Q02 [Approvals] approved: approved by the developer, as W01 answered it: B — "Paraffin". The decision is theirs now.`,
	} {
		if !strings.Contains(digest, line+"\n") {
			t.Errorf("the digest lacks %q:\n%s", line, digest)
		}
	}
	if _, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{
		After: cursorOf(t, digest), Timeout: durationpb.New(0),
	})); err != nil {
		t.Fatal(err)
	}

	// An open question approved is answered: the lead hears the answer, not an approval.
	open, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", Options: []string{"Olive", "Paraffin"}, WishId: wish, Recommendation: "B: brighter.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	approve(open.Msg.GetQuestion())
	events, err := waitingEvents(ctx, c.store)
	if err != nil || len(events) != 1 || events[0].GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER {
		t.Errorf("approving an open question queued %v, %v; want its answer only", events, err)
	}
}

func putWishes(t *testing.T, w *Wishes, titles ...string) []string {
	t.Helper()
	var ids []string
	err := w.Store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/wishes", &planv1.WishServiceListRequest{}); err != nil {
			return err
		}
		for _, title := range titles {
			wish := &planv1.Wish{Id: store.NewID(), Title: title, State: planv1.WishState_WISH_STATE_ACTIVE}
			ids = append(ids, wish.GetId())
			if err := tx.Put(wish); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestDigestNamesTheWish: every line of a digest names its wish beside its code, one wish or several, since codes
// repeat across wishes (W74 was in two of them), and the digest ends with the wishes' identifiers.
func TestDigestNamesTheWish(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	ids := putWishes(t, w, "Djinn QoL", "Release")
	for _, id := range ids {
		if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED,
			WishId: id, Code: "W74", Text: "Djinn: W74 done: it works."}); err != nil {
			t.Fatal(err)
		}
	}
	notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART, "", "Djinn: restarted on v1.")
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})
	for _, want := range []string{
		"\n- W74 [Djinn QoL] ended: done: it works.\n", "\n- W74 [Release] ended: done: it works.\n",
		"\n- restart: restarted on v1.\n",
		"\nWishes: [Djinn QoL] " + ids[0] + ", [Release] " + ids[1] + ".\n",
		"djinn question get <question> --wish-id <wish>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the digest lacks %q:\n%s", want, got)
		}
	}

	if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED,
		WishId: ids[0], Code: "W76", Text: "Djinn: W76 merged."}); err != nil {
		t.Fatal(err)
	}
	one := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(0)})
	if !strings.Contains(one, "\n- W76 [Djinn QoL] merged: merged.\n") {
		t.Errorf("a digest of one wish does not name it:\n%s", one)
	}
}

// TestDigestCarriesTheNote: the final note of a task that ended, or the note of an answer, comes whole under its
// line, where the line keeps 160 characters; past a screen or two, the digest names the command that prints it all,
// and that command does.
func TestDigestCarriesTheNote(t *testing.T) {
	t.Parallel()
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	wish := putWishes(t, w, "Djinn QoL")[0]
	note := "Added djinn question get.\n\nLeft to verify:\n- the Windows build"
	queue := func(code, text, note string) {
		t.Helper()
		if err := w.Notify(t.Context(), &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED,
			WishId: wish, Code: code, Text: text, Note: note}); err != nil {
			t.Fatal(err)
		}
	}
	queue("W01", "Djinn: W01 done: Added djinn question get.", note)
	queue("W02", "Djinn: W02 done: fine.", "fine.")
	long := strings.Repeat("A line of a long final note, one of many.\n", 100)
	queue("W03", "Djinn: W03 done: A line of a long final note, one of many.", long)
	got := leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{Timeout: durationpb.New(0)})

	whole := "- W01 [Djinn QoL] ended: done: Added djinn question get.\n  Note:\n    Added djinn question get.\n" +
		"    Left to verify:\n    - the Windows build\n- W02"
	if !strings.Contains(got, whole) {
		t.Errorf("the digest lacks the whole note under its line:\n%s", got)
	}
	if !strings.Contains(got, "- W02 [Djinn QoL] ended: done: fine.\n- W03") {
		t.Errorf("a note the line holds comes again:\n%s", got)
	}
	_, w03, _ := strings.Cut(got, "- W03")
	if n := len([]rune(w03)); n > digestNoteMax+800 {
		t.Errorf("the long note takes %d characters of the digest:\n%s", n, got)
	}
	all, err := store.List[*planv1.LeadEvent](t.Context(), w.Store, nil)
	events := slices.DeleteFunc(all, func(e *planv1.LeadEvent) bool { return e.GetCode() != "W03" })
	if err != nil || len(events) != 1 {
		t.Fatalf("events of W03 = %v, %v", events, err)
	}
	command := "djinn lead event " + events[0].GetId() + " prints it whole."
	if !strings.Contains(w03, command) || !strings.Contains(w03, "The note goes on for ") {
		t.Errorf("the cut note does not name %q:\n%s", command, w03)
	}
	res, err := s.Event(t.Context(), connect.NewRequest(&planv1.LeadServiceEventRequest{Id: events[0].GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Msg.GetText(); strings.Count(text, "> A line of a long final note, one of many.") != 100 ||
		!strings.Contains(text, "### W03 ended · wish Djinn QoL (`"+wish+"`)") {
		t.Errorf("djinn lead event = %s", text)
	}
	if _, err := s.Event(context.Background(), connect.NewRequest(&planv1.LeadServiceEventRequest{Id: store.NewID()})); code(err) != connect.CodeNotFound {
		t.Errorf("an unknown event = %v, want not_found", err)
	}
}

func (s *syncLeads) opened() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.f.opened)
}

// TestAnswerWakesLeadWatchAcrossWishes: an answer while no watch waits queues the event and types one short waiting
// line in the lead's terminal once settled, never the answer itself; the lead's watch prints the answer with a cursor;
// a watch waiting with --after receives an answer from another wish at once without typing another line.
func TestAnswerWakesLeadWatchAcrossWishes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	leads := &syncLeads{}
	c := serve(t, WithLeads(leads))
	v, _ := leadWatches.Load(c.store)
	lw := v.(*leadWatch)
	lw.timing = watchTiming{
		quiet:     80 * time.Millisecond,
		settleMax: 400 * time.Millisecond,
		grace:     5 * time.Second,
		retype:    time.Hour,
		remind:    20 * time.Millisecond,
	}

	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	projectID := project.Msg.GetProject().GetId()
	makeWish := func(title string) string {
		t.Helper()
		res, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: title, ProjectIds: []string{projectID},
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetWish().GetId()
	}
	askAndAnswer := func(wishID, first, second string) string {
		t.Helper()
		asked, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text:    "Which lamp first, " + first + " or " + second + "?",
			Options: []string{first, second}, WishId: wishID,
		}))
		if err != nil {
			t.Fatal(err)
		}
		q := asked.Msg.GetQuestion()
		if _, err := c.questions.Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
			Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: q.GetCode()}},
			WishId:   wishID, Choice: planv1.Choice_CHOICE_B, Note: "lighter",
		})); err != nil {
			t.Fatal(err)
		}
		return q.GetCode()
	}

	wishID := makeWish("Tell the lead")
	other := makeWish("Tell the same lead")
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	first := askAndAnswer(wishID, "Brass", "Glass")
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(leads.said()) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	wantSaid := []string{LeadTerminal + ": " + WaitingLine(1)}
	if got := leads.said(); !slices.Equal(got, wantSaid) {
		t.Fatalf("said %q, want %q (never the answer itself)", got, wantSaid)
	}

	watched, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{
		Session: session, Timeout: durationpb.New(0),
	}))
	if err != nil {
		t.Fatal(err)
	}
	digest := watched.Msg.GetText()
	if want := "- " + first + ` [Tell the lead] answer: answered B — "Glass"`; !strings.Contains(digest, want) {
		t.Errorf("first digest lacks %q:\n%s", want, digest)
	}
	cursor := cursorOf(t, digest)

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{
			Session: session, After: cursor, Timeout: durationpb.New(30 * time.Second),
		}))
		if err != nil {
			errCh <- err
			return
		}
		done <- res.Msg.GetText()
	}()
	for time.Now().Before(deadline) {
		lw.mu.Lock()
		watching := lw.watching
		lw.mu.Unlock()
		if watching == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	second := askAndAnswer(other, "Copper", "Tin")
	var nextDigest string
	select {
	case err := <-errCh:
		t.Fatal(err)
	case nextDigest = <-done:
	}
	if want := "- " + second + ` [Tell the same lead] answer: answered B — "Tin"`; !strings.Contains(nextDigest, want) {
		t.Errorf("second digest lacks %q:\n%s", want, nextDigest)
	}
	if strings.Contains(nextDigest, "Glass") {
		t.Errorf("second digest still holds the acknowledged first answer:\n%s", nextDigest)
	}
	if got := leads.said(); !slices.Equal(got, wantSaid) {
		t.Errorf("said %q after second answer, want nothing more typed", got)
	}
	if got := leads.opened(); len(got) != 1 {
		t.Errorf("opened %q, want the lead started once", got)
	}

	if _, err := c.lead.Watch(ctx, connect.NewRequest(&planv1.LeadServiceWatchRequest{
		Session: session, After: cursorOf(t, nextDigest), Timeout: durationpb.New(0),
	})); err != nil {
		t.Fatal(err)
	}
	if events, err := waitingEvents(ctx, c.store); err != nil || len(events) != 0 {
		t.Errorf("waiting events after acknowledging = %v, %v; want none", events, err)
	}
}
