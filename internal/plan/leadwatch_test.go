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
var quick = watchTiming{quiet: 50 * time.Millisecond, settleMax: 400 * time.Millisecond, grace: 150 * time.Millisecond, retype: time.Hour}

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
func (s *syncLeads) Close(name string) {}
func (s *syncLeads) Say(name, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Say(name, line)
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
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	go func() {
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
	if took := time.Since(start); took < 90*time.Millisecond+quick.quiet {
		t.Errorf("the watch returned after %s, before the events settled", took)
	}
}

// TestLeadWatchSettleIsCapped: events that never pause are returned after quick.settleMax anyway.
func TestLeadWatchSettleIsCapped(t *testing.T) {
	w, s, _ := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
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
// nothing; a codex lead, which no finished command wakes up, gets the line even then.
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
		w, s, leads := leadWatchFixture(t, planv1.Provider_PROVIDER_CLAUDE)
		done := make(chan string)
		go func() { done <- leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{}) }()
		time.Sleep(20 * time.Millisecond)
		notify(t, w, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q1", "Djinn: Q1 answered yes.")
		got := <-done
		if !strings.Contains(got, "Q1 answer") {
			t.Errorf("digest:\n%s", got)
		}
		// The lead runs its next watch within the grace: still nothing typed.
		go func() {
			done <- leadWatchOnce(t, s, &planv1.LeadServiceWatchRequest{After: cursorOf(t, got), Timeout: durationpb.New(time.Second)})
		}()
		time.Sleep(quick.grace + 3*quick.quiet)
		if said := leads.said(); len(said) != 0 {
			t.Errorf("typed %q while a watch waited", said)
		}
		<-done
	})

	t.Run("the watch does not come back", func(t *testing.T) {
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

	t.Run("codex", func(t *testing.T) {
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
