package harness

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/usage"
	"github.com/empowill/djinn/lead/tones"
)

// leadAgent is a provider for the driven lead's tests: each session it starts plays the next step of play, and
// it keeps every spec it was given.
type leadAgent struct {
	mu    sync.Mutex
	specs []Spec
	// play is what the session n (from 0) says; release, when set for it, is waited on before it ends.
	play    func(n int) ([]Event, error)
	release map[int]chan struct{}
	started chan int
}

func newLeadAgent(play func(n int) ([]Event, error)) *leadAgent {
	return &leadAgent{play: play, release: map[int]chan struct{}{}, started: make(chan int, 10)}
}

// hold makes session n wait until its channel is closed.
func (a *leadAgent) hold(n int) chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := make(chan struct{})
	a.release[n] = c
	return c
}

func (a *leadAgent) all() []Spec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.specs)
}

func (a *leadAgent) Start(ctx context.Context, spec Spec) (Worker, error) {
	a.mu.Lock()
	n := len(a.specs)
	a.specs = append(a.specs, spec)
	held := a.release[n]
	a.mu.Unlock()
	events, err := a.play(n)
	w := &leadSession{events: make(chan Event), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer close(w.events)
		a.started <- n
		for _, ev := range events {
			select {
			case w.events <- ev:
			case <-ctx.Done():
				w.err = ctx.Err()
				return
			}
		}
		if held != nil {
			select {
			case <-held:
			case <-ctx.Done():
				w.err = ctx.Err()
				return
			}
		}
		w.err = err
	}()
	return w, nil
}

type leadSession struct {
	events chan Event
	done   chan struct{}
	err    error
}

func (s *leadSession) Events() <-chan Event { return s.events }
func (s *leadSession) Send(string) error    { return ErrClosed }
func (s *leadSession) Stop()                {}
func (s *leadSession) Wait() Result {
	<-s.done
	if s.err != nil {
		return Result{ExitCode: 1, Err: s.err}
	}
	return Result{}
}

// leadTurn is a session that ran a djinn command, said a line at the end of its turn, and spent a little.
func leadTurn(command, said string) []Event {
	return []Event{
		{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "session x", Model: "claude-opus-5-5"},
		{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: `Bash {"command":"` + command + `","description":"x"}`},
		{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "ok"},
		{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: said},
		{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, Usage: &planv1.Usage{InputTokens: 9, OutputTokens: 300,
			CacheReadTokens: 60000, CacheWriteTokens: 12000, CostUsd: 0.12}},
	}
}

// leadBench is a Djinn whose lead a driver drives with agent, on a wish.
type leadBench struct {
	home   string
	db     *store.Store
	wishes *plan.Wishes
	wish   *planv1.Wish
	agent  *leadAgent
	drive  *plan.LeadDrive
}

func driveLead(t *testing.T, agent *leadAgent, set ...func(*LeadDriver)) *leadBench {
	t.Helper()
	home := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	plan.SetLeadSettle(db, 20*time.Millisecond, 200*time.Millisecond)
	wish := &planv1.Wish{Id: store.NewID(), Title: "Faster checkout", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	}); err != nil {
		t.Fatal(err)
	}
	b := &leadBench{home: home, db: db, wishes: &plan.Wishes{Store: db, Home: home}, wish: wish, agent: agent}
	b.drive = plan.DriveLead(db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	// No test runs an agent's status command: no seat is offered after a limit, unless a test says so.
	d := &LeadDriver{Wishes: b.wishes, Provider: agent, Home: home, RetryAfter: time.Millisecond,
		Ready: func(context.Context, *planv1.LeadSeat, *planv1.Account) bool { return false }}
	for _, f := range set {
		f(d)
	}
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return b
}

func (b *leadBench) notify(t *testing.T, kind planv1.LeadEventKind, code, text string) *planv1.LeadEvent {
	t.Helper()
	e := &planv1.LeadEvent{Kind: kind, WishId: b.wish.GetId(), Code: code, Text: text}
	if err := b.wishes.Notify(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

// started waits for session n to start, and returns its spec.
func (b *leadBench) started(t *testing.T, n int) Spec {
	t.Helper()
	select {
	case got := <-b.agent.started:
		if got != n {
			t.Fatalf("session %d started, want %d", got, n)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("session %d never started", n)
	}
	return b.agent.all()[n]
}

func (b *leadBench) batches(t *testing.T) []*planv1.LeadBatch {
	t.Helper()
	all, err := plan.LeadJournal(t.Context(), b.db, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func (b *leadBench) waiting(t *testing.T) []*planv1.LeadEvent {
	t.Helper()
	events, err := store.List[*planv1.LeadEvent](t.Context(), b.db, store.Where{"ack_session_id": ""})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// sentText is every text of the messages a fake agent read, recorded in its input file as stream-json lines.
func sentText(t *testing.T, input string) string {
	t.Helper()
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			texts = append(texts, v)
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	for line := range strings.Lines(string(data)) {
		var v any
		if json.Unmarshal([]byte(line), &v) == nil {
			walk(v)
		}
	}
	return strings.Join(texts, "\n")
}

// TestLeadDriver verifies how Djinn drives the lead across batches, retries, cuts, and recorded streams.
func TestLeadDriver(t *testing.T) {
	t.Parallel()

	// A batch's session starts fresh in the lead's folder, on the short rules in its system prompt and a message that
	// holds the events whole, the wish's brief and an empty journal; it may run djinn and nothing else, as the lead.
	t.Run("batch_prompt", func(t *testing.T) {
		t.Parallel()
		b := driveLead(t, newLeadAgent(func(int) ([]Event, error) {
			return leadTurn("djinn task spawn W4", "Spawned W4 for Q2's answer; waits for it"), nil
		}))
		long := "W3 ended: " + strings.Repeat("the parser reads every line of the file, ", 8) + "and leaves one case to verify."
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", long)
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ANSWER, "Q2", "Q2 answered: A, keep the cache")
		spec := b.started(t, 0)

		for _, want := range []string{
			"# A batch for the Djinn", "2 event(s)", "### W3 ended · wish Faster checkout (`" + b.wish.GetId() + "`)",
			long, "### Q2 answer", "Q2 answered: A, keep the cache", "## The lead's journal", "No batch before this one",
			"# The wish: Faster checkout", "## The active wishes",
		} {
			if !strings.Contains(spec.Prompt, want) {
				t.Errorf("the prompt lacks %q:\n%s", want, spec.Prompt)
			}
		}
		for _, want := range []string{"driven lead, rules " + plan.DrivenRulesVersion, "The developer reads none of your text",
			"Every question, doubt or choice you have goes into a question (`djinn question ask`) in the wish concerned, never only into your text",
			"A message is the developer's words", "Never run `djinn lead watch`"} {
			if !strings.Contains(spec.SystemPrompt, want) {
				t.Errorf("the rules lack %q", want)
			}
		}
		if strings.Contains(spec.Prompt, "## Your tone") {
			t.Errorf("no page said its theme, yet the batch has a tone:\n%s", spec.Prompt)
		}
		if strings.Contains(spec.SystemPrompt, "djinn lead watch --after") || strings.Contains(spec.Prompt, "djinn lead watch --after") {
			t.Errorf("a driven lead is told to watch")
		}
		if spec.Resume != "" || spec.TaskID == "" || spec.Dir != plan.LeadFolder(b.home) || spec.MaxTurns != LeadMaxTurns ||
			!slices.Contains(spec.Env, "DJINN_LEAD=1") || !slices.Contains(spec.Env, "DJINN_TASK_ID=") || !slices.Contains(spec.Permissions.GetCommands(), "djinn") ||
			spec.Permissions.GetEdit() || !slices.Equal(spec.WriteDirs, []string{spec.Dir}) {
			t.Errorf("spec = %+v", spec)
		}

		waitFor(t, "the events acknowledged", func() bool { return len(b.waiting(t)) == 0 })
		batches := b.batches(t)
		if len(batches) != 1 {
			t.Fatalf("batches = %v", batches)
		}
		got := batches[0]
		if got.GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_DONE || got.GetSessionId() != spec.TaskID ||
			got.GetModel() != "claude-opus-5-5" || got.GetAttempt() != 1 || len(got.GetEventIds()) != 2 ||
			!slices.Equal(got.GetCommands(), []string{"djinn task spawn W4"}) ||
			got.GetNote() != "Spawned W4 for Q2's answer; waits for it" || got.GetUsage().GetCostUsd() != 0.12 ||
			got.GetPromptChars() != int32(len(spec.Prompt)) {
			t.Errorf("batch = %v", got)
		}
		events, _ := store.List[*planv1.LeadEvent](t.Context(), b.db, nil)
		for _, e := range events {
			if e.GetAckSessionId() != spec.TaskID {
				t.Errorf("event %s acknowledged by %q", e.GetCode(), e.GetAckSessionId())
			}
		}
		if b.drive.Batch().GetId() != got.GetId() {
			t.Errorf("the window's batch = %v", b.drive.Batch())
		}
	})

	// The next batch is a new session, never a resumed one, and its message carries the journal of the first:
	// who led it, what it ran and what it said; the first batch's events are not in it again.
	t.Run("second_batch_starts_fresh", func(t *testing.T) {
		t.Parallel()
		b := driveLead(t, newLeadAgent(func(n int) ([]Event, error) {
			if n == 0 {
				return leadTurn("djinn question ask Q5", "Asked Q5 about the cache"), nil
			}
			return leadTurn("djinn task spawn W6", ""), nil
		}))
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended: the parser works")
		first := b.started(t, 0)
		waitFor(t, "the first batch done", func() bool { return len(b.waiting(t)) == 0 })
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W4", "W4 merged into feat/cache")
		second := b.started(t, 1)

		if second.TaskID == first.TaskID || second.Resume != "" || second.Fork {
			t.Errorf("the second batch is not a fresh session: %+v", second)
		}
		events, journal, ok := strings.Cut(second.Prompt, "## The lead's journal")
		if !ok {
			t.Fatalf("no journal:\n%s", second.Prompt)
		}
		for _, want := range []string{"session `" + first.TaskID + "`", "claude claude-opus-5-5", "attempt 1 · done",
			"$0.12", "- W3 [Faster checkout] ended: ended: the parser works", "`djinn question ask Q5`", "Said at the end: Asked Q5 about the cache"} {
			if !strings.Contains(journal, want) {
				t.Errorf("the journal lacks %q:\n%s", want, journal)
			}
		}
		if !strings.Contains(events, "### W4 merged") || strings.Contains(events, "### W3") || !strings.Contains(events, "1 event(s)") {
			t.Errorf("the second batch's events:\n%s", events)
		}
	})

	// What the developer writes to the Djinn while a batch runs is an event of the queue, whole: the running session
	// never gets it, the next batch does, and the first batch acknowledges only its own events.
	t.Run("message_waits_for_next_batch", func(t *testing.T) {
		t.Parallel()
		agent := newLeadAgent(func(int) ([]Event, error) { return leadTurn("djinn brief", ""), nil })
		release := agent.hold(0)
		b := driveLead(t, agent)
		lead := &plan.LeadService{Wishes: b.wishes}
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_FAILED, "W8", "W8 failed: tests red")
		first := b.started(t, 0)
		text := "Stop W8 for now,\nand look at the cache first."
		if _, err := lead.Tell(t.Context(), connect.NewRequest(&planv1.LeadServiceTellRequest{
			Text: text, WishId: b.wish.GetId(),
		})); err != nil {
			t.Fatal(err)
		}
		if _, err := lead.Tell(t.Context(), connect.NewRequest(&planv1.LeadServiceTellRequest{Text: "And the docs?"})); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond) // More than the settling: a batch would have started by now.
		if n := len(agent.all()); n != 1 {
			t.Fatalf("%d sessions while the first runs", n)
		}
		close(release)
		second := b.started(t, 1)
		if strings.Contains(first.Prompt, "look at the cache") {
			t.Errorf("the running batch got the message")
		}
		if !strings.Contains(second.Prompt, "### message · wish Faster checkout") || !strings.Contains(second.Prompt, text) ||
			!strings.Contains(second.Prompt, "And the docs?") || strings.Contains(second.Prompt, "### W8 failed") {
			t.Errorf("the second batch's prompt:\n%s", second.Prompt)
		}
		waitFor(t, "every event acknowledged", func() bool { return len(b.waiting(t)) == 0 })
		batches := b.batches(t)
		if len(batches) != 2 || len(batches[0].GetEventIds()) != 1 || len(batches[1].GetEventIds()) != 2 {
			t.Errorf("batches = %v", batches)
		}
	})

	// A batch whose session fails gets one new session; failed again, it is reported beside Lead, its events wait, and
	// no session starts until a new event comes, which goes with them.
	t.Run("failed_batch_retried_once", func(t *testing.T) {
		t.Parallel()
		b := driveLead(t, newLeadAgent(func(n int) ([]Event, error) {
			if n < 2 {
				return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: "Bash djinn task stop W9"}},
					errors.New("API Error: 529 overloaded")
			}
			return leadTurn("djinn task list", ""), nil
		}))
		lead := &plan.LeadService{Wishes: b.wishes}
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, "W9", "W9 fails to integrate")
		first := b.started(t, 0)
		second := b.started(t, 1)
		if second.TaskID == first.TaskID {
			t.Errorf("the retry resumed the failed session")
		}
		for _, want := range []string{"The first session of this batch failed", "attempt 1 · failed",
			"Failed: API Error: 529 overloaded", "`djinn task stop W9`", "### W9 move"} {
			if !strings.Contains(second.Prompt, want) {
				t.Errorf("the retry's prompt lacks %q:\n%s", want, second.Prompt)
			}
		}
		waitFor(t, "the failure reported", func() bool { return b.drive.Failure() != "" })
		got, err := lead.Get(t.Context(), connect.NewRequest(&planv1.LeadServiceGetRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Msg.GetDriven() || !strings.Contains(got.Msg.GetStartError(), "failed twice: API Error: 529 overloaded") ||
			got.Msg.GetBatch().GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED || got.Msg.GetLead() == nil {
			t.Errorf("Get = %v", got.Msg)
		}
		time.Sleep(60 * time.Millisecond)
		if n := len(b.agent.all()); n != 2 || len(b.waiting(t)) != 1 {
			t.Fatalf("%d sessions, %d events waiting after the report", n, len(b.waiting(t)))
		}
		batches := b.batches(t)
		if len(batches) != 2 || batches[1].GetAttempt() != 2 || batches[1].GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED {
			t.Errorf("batches = %v", batches)
		}

		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W10", "W10 ended")
		third := b.started(t, 2)
		if !strings.Contains(third.Prompt, "### W9 move") || !strings.Contains(third.Prompt, "### W10 ended") {
			t.Errorf("the next batch lacks the waiting events:\n%s", third.Prompt)
		}
		waitFor(t, "the report cleared", func() bool { return b.drive.Failure() == "" && len(b.waiting(t)) == 0 })
	})

	// After a batch that failed twice or hit a reported limit, Relancer (LeadService.Resume) clears the reported
	// failure, sends the waiting events to a new session at once on the same seat, and opens no terminal.
	t.Run("retried_by_lead", func(t *testing.T) {
		t.Parallel()
		var lead *plan.LeadService
		var resumeOnFail atomic.Bool
		var callbackNote atomic.Value
		b := driveLead(t, newLeadAgent(func(n int) ([]Event, error) {
			switch n {
			case 0, 1:
				return nil, errors.New("API Error: 529 overloaded")
			case 2:
				return nil, errors.New("API Error: 500 transient")
			case 4, 5:
				return nil, errors.New("API Error: 502 bad gateway")
			case 6:
				return nil, errors.New("You've hit your session limit")
			default:
				return leadTurn("djinn brief", ""), nil
			}
		}), func(d *LeadDriver) {
			d.RetryAfter = 40 * time.Millisecond
			d.Changed = func() {
				if resumeOnFail.Load() && d.Wishes.Drive().Failure() != "" && resumeOnFail.CompareAndSwap(true, false) {
					res, err := lead.Resume(t.Context(), connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
					if err == nil {
						callbackNote.Store(res.Msg.GetNote())
					}
				}
			}
		})
		lead = &plan.LeadService{Wishes: b.wishes}

		// 1. Twice-failed batch: attempt 1 fails with empty StartError, attempt 2 fails and sets StartError,
		// then LeadService.Resume clears StartError and starts a new batch (whose own attempt 1 failure keeps
		// StartError empty while waiting for its automatic second attempt).
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
		b.started(t, 0)
		waitFor(t, "attempt 1 failed", func() bool {
			return b.drive.Batch().GetAttempt() == 1 && b.drive.Batch().GetState() == planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED
		})
		if errText := b.drive.Failure(); errText != "" {
			t.Fatalf("Failure() after first attempt = %q, want empty until second failure", errText)
		}
		b.started(t, 1)
		waitFor(t, "twice-failed batch reported", func() bool { return b.drive.Failure() != "" })
		got, err := lead.Get(t.Context(), connect.NewRequest(&planv1.LeadServiceGetRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if got.Msg.GetBatch().GetAttempt() != 2 || got.Msg.GetBatch().GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED ||
			!strings.Contains(got.Msg.GetStartError(), "failed twice: API Error: 529 overloaded") {
			t.Fatalf("Get after 2nd failure = %v", got.Msg)
		}
		res, err := lead.Resume(t.Context(), connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		wantRetryNote := "Djinn drives the lead: the events that wait go to a new session now. The side journal in " +
			"the window shows what each batch did."
		if res.Msg.GetTerminal() != "" || res.Msg.GetNote() != wantRetryNote {
			t.Errorf("Resume after 2nd failure = %v, want note %q", res.Msg, wantRetryNote)
		}
		if errText := b.drive.Failure(); errText != "" {
			t.Errorf("Failure() right after Resume = %q, want cleared", errText)
		}
		// Session 2 (attempt 1 of the resumed batch) fails once; StartError must stay empty while waiting for session 3.
		b.started(t, 2)
		waitFor(t, "resumed batch attempt 1 failed", func() bool {
			return b.drive.Batch().GetAttempt() == 1 && b.drive.Batch().GetState() == planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED
		})
		if errText := b.drive.Failure(); errText != "" {
			t.Errorf("Failure() after resumed batch attempt 1 = %q, want empty before attempt 2", errText)
		}
		if again := b.started(t, 3); !strings.Contains(again.Prompt, "### W2 ended") {
			t.Errorf("resumed batch attempt 2:\n%s", again.Prompt)
		}
		waitFor(t, "W2 acknowledged", func() bool { return len(b.waiting(t)) == 0 })

		// 2. Resume called synchronously as soon as drive.Fail notifies Changed must not be lost.
		resumeOnFail.Store(true)
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended")
		b.started(t, 4)
		b.started(t, 5)
		if again := b.started(t, 6); !strings.Contains(again.Prompt, "### W3 ended") {
			t.Errorf("batch resumed from Changed callback:\n%s", again.Prompt)
		}
		if gotNote, _ := callbackNote.Load().(string); gotNote != wantRetryNote {
			t.Errorf("callback Resume note = %q, want %q", gotNote, wantRetryNote)
		}

		// 3. Session 6 hits a usage limit on attempt 1: Resume clears the limit and runs session 7 at once.
		waitFor(t, "the limit reported", func() bool { return b.drive.LeadLimit() != nil && b.drive.Failure() != "" })
		res, err = lead.Resume(t.Context(), connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		if res.Msg.GetTerminal() != "" || res.Msg.GetNote() != wantRetryNote {
			t.Errorf("Resume after limit = %v, want note %q", res.Msg, wantRetryNote)
		}
		if b.drive.Failure() != "" || b.drive.LeadLimit() != nil {
			t.Errorf("Failure=%q Limit=%v after Resume, want cleared", b.drive.Failure(), b.drive.LeadLimit())
		}
		if again := b.started(t, 7); !strings.Contains(again.Prompt, "### W3 ended") {
			t.Errorf("the retried batch after limit:\n%s", again.Prompt)
		}
		waitFor(t, "W3 acknowledged", func() bool { return len(b.waiting(t)) == 0 })

		// 4. Resume while idle (no failure or limit) returns the side-journal note without starting a session.
		res, err = lead.Resume(t.Context(), connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		wantIdleNote := "Djinn drives the lead: each batch of its events, and what you write to the Djinn, goes to a " +
			"fresh session that ends with its turn. The side journal in the window shows what each batch did."
		if res.Msg.GetNote() != wantIdleNote {
			t.Errorf("idle Resume note = %q, want %q", res.Msg.GetNote(), wantIdleNote)
		}
	})

	// While Djinn drives the lead, no session watches its events.
	t.Run("refuses_watch", func(t *testing.T) {
		t.Parallel()
		b := driveLead(t, newLeadAgent(func(int) ([]Event, error) { return nil, nil }))
		if err := b.db.Tx(t.Context(), func(tx *store.Tx) error {
			lead := &planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: "s1", Directory: b.home}
			if err := tx.Journal("test", "test/put", lead); err != nil {
				return err
			}
			return tx.Put(lead)
		}); err != nil {
			t.Fatal(err)
		}
		_, err := (&plan.LeadService{Wishes: b.wishes}).Watch(t.Context(),
			connect.NewRequest(&planv1.LeadServiceWatchRequest{}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "the lead is driven by Djinn") {
			t.Errorf("Watch = %v", err)
		}
	})

	// A batch left running by a stop is failed at the next start, and its events go to a new session.
	t.Run("ends_cut_batch", func(t *testing.T) {
		t.Parallel()
		agent := newLeadAgent(func(int) ([]Event, error) { return leadTurn("djinn brief", ""), nil })
		agent.hold(0)
		b := driveLead(t, agent)
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_RESTART, "", "Djinn restarted on v1.2.3")
		b.started(t, 0)
		cut := b.batches(t)[0]
		if cut.GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING {
			t.Fatalf("batch = %v", cut)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		next := newLeadAgent(func(int) ([]Event, error) { return leadTurn("djinn brief", ""), nil })
		go (&LeadDriver{Wishes: b.wishes, Provider: next, Home: b.home}).restore(ctx, plan.DriveLead(b.db, nil))
		waitFor(t, "the cut batch failed", func() bool {
			return b.batches(t)[0].GetState() == planv1.LeadBatchState_LEAD_BATCH_STATE_FAILED
		})
		if got := b.batches(t)[0]; !strings.Contains(got.GetError(), "cut") || got.GetEndTime() == nil {
			t.Errorf("cut batch = %v", got)
		}
	})

	// Measures what a batch costs on the stream of a real one (testdata/claude/lead-batch.jsonl, W131), replayed by the
	// test binary through the real process path, never a model.
	t.Run("batch_cost_recorded", func(t *testing.T) {
		t.Parallel()
		env, _, _ := fake{provider: "claude", fixture: "lead-batch"}.env(t)
		b := driveLead(t, nil, func(d *LeadDriver) { d.Provider, d.Env = Claude{Command: os.Args[0]}, env })
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_MESSAGE, "", "Withdraw the license question, I will decide later.")
		waitFor(t, "the batch done", func() bool { return len(b.waiting(t)) == 0 })
		batch := b.batches(t)[0]
		u := batch.GetUsage()
		if batch.GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_DONE || batch.GetModel() != "claude-opus-5-5" ||
			u.GetCostUsd() != 0.1599468 || u.GetCacheReadTokens() != 47074 || u.GetCacheWriteTokens() != 18102 ||
			len(batch.GetCommands()) != 1 || !strings.HasPrefix(batch.GetCommands()[0], "djinn question withdraw Q01") {
			t.Fatalf("batch = %v", batch)
		}
		if cost := u.GetCostUsd(); cost < 0.12 || cost > 0.21 {
			t.Errorf("$%.3f a batch, out of L10's estimate of $0.12 to $0.21", cost)
		}
		table, err := usage.DefaultTable()
		if err != nil {
			t.Fatal(err)
		}
		estimate, ok, err := table.Estimate(u, batch.GetModel(), planv1.Provider_PROVIDER_CLAUDE)
		if err != nil || !ok {
			t.Fatalf("estimate: %v, %v", ok, err)
		}
		if math.Abs(estimate-u.GetCostUsd()) > 1e-6 {
			t.Errorf("estimated $%.7f; claude counted $%.7f", estimate, u.GetCostUsd())
		}
		t.Logf("a batch: $%.4f as claude counts it, $%.4f estimated by the price table; %d tokens read from the cache, %d written",
			u.GetCostUsd(), estimate, u.GetCacheReadTokens(), u.GetCacheWriteTokens())
	})

	// Each batch speaks in the tone of the theme the window showed as it started (decision Q36, W156), on the stream of
	// a real batch replayed through the real process path.
	t.Run("tone_follows_theme_recorded", func(t *testing.T) {
		t.Parallel()
		env, args, input := fake{provider: "claude", fixture: "lead-batch"}.env(t)
		b := driveLead(t, nil, func(d *LeadDriver) { d.Provider, d.Env = Claude{Command: os.Args[0]}, env })
		var rules string
		for n, step := range []struct {
			theme     uiv1.ShownTheme
			want, not string
		}{
			{uiv1.ShownTheme_SHOWN_THEME_LIGHT, tones.Light(), tones.Dark()},
			{uiv1.ShownTheme_SHOWN_THEME_DARK, tones.Dark(), tones.Light()},
		} {
			if err := plan.SetShownTheme(b.home, step.theme); err != nil {
				t.Fatal(err)
			}
			b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_MESSAGE, "", "Withdraw the license question, I will decide later.")
			waitFor(t, "the batch done", func() bool { return len(b.batches(t)) == n+1 && len(b.waiting(t)) == 0 })
			message := sentText(t, input)
			if !strings.Contains(message, "## Your tone") || !strings.Contains(message, step.want) || strings.Contains(message, step.not) {
				t.Errorf("%v: the batch's message lacks its tone, or has the other:\n%s", step.theme, message)
			}
			got, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			// Windows caps a process's whole command line at 32,767 UTF-16 code units (CreateProcessW): the rules go in
			// a file, never on the command line (W436).
			if len(got) > 4096 {
				t.Errorf("the command line is %d bytes: the rules belong in a file", len(got))
			}
			_, file, _ := strings.Cut(string(got), "--append-system-prompt-file\n")
			file, _, _ = strings.Cut(file, "\n--exclude-dynamic-system-prompt-sections")
			systemBytes, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			system := string(systemBytes)
			if !strings.Contains(system, "Your tone follows the window's theme") || strings.Contains(system, step.want) {
				t.Errorf("%v: the rules lack the tone's limits, or carry a tone:\n%s", step.theme, system)
			}
			if rules == "" {
				rules = system
			} else if system != rules {
				t.Errorf("the rules changed with the theme: the cache of the last session no longer serves")
			}
		}
	})

	// On a real batch (testdata/claude/lead-comments.jsonl, W120), the lead that gets a send of four comments reads
	// them in one command, acts on them in the same batch, and replies to every one with djinn tilasm reply.
	t.Run("replies_to_comments_recorded", func(t *testing.T) {
		t.Parallel()
		env, _, _ := fake{provider: "claude", fixture: "lead-comments"}.env(t)
		b := driveLead(t, nil, func(d *LeadDriver) { d.Provider, d.Env = Claude{Command: os.Args[0]}, env })
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS, "L01",
			"Djinn: L01 v1, 4 comments (2 general, 1 on a passage, 1 on an element), iterate: its next version with them: "+
				"djinn tilasm comments L01 --send 1")
		waitFor(t, "the batch done", func() bool { return len(b.waiting(t)) == 0 })
		batch := b.batches(t)[0]
		if batch.GetState() != planv1.LeadBatchState_LEAD_BATCH_STATE_DONE || batch.GetUsage().GetCostUsd() != 0.6187598000000001 {
			t.Fatalf("batch = %v", batch)
		}
		commands := batch.GetCommands()
		read := slices.IndexFunc(commands, func(c string) bool { return strings.HasPrefix(c, "djinn tilasm comments L01") })
		if read != 0 {
			t.Errorf("the comments are read at %d, want first: %q", read, commands)
		}
		if n := slices.IndexFunc(commands, func(c string) bool { return strings.HasPrefix(c, "djinn tilasm put ") }); n < 0 ||
			!strings.Contains(commands[n], "--code L01") {
			t.Errorf("no next version of L01 put: %q", commands)
		}
		for _, code := range []string{"C1", "C2", "C3", "C4"} {
			var at []int
			for i, c := range commands {
				if strings.HasPrefix(c, "djinn tilasm reply L01 "+code+" --text ") {
					at = append(at, i)
				}
			}
			if len(at) != 1 || at[0] < read {
				t.Errorf("%s replied at %v, want once after the read: %q", code, at, commands)
			}
		}
		// W453: the first djinn question revise call in the recorded stream failed (--wish is not a valid flag) and is
		// marked "failed: " in the batch's journal; the second succeeded and is not.
		if !slices.ContainsFunc(commands, func(c string) bool {
			return strings.HasPrefix(c, "failed: djinn question revise Q01 --wish ")
		}) {
			t.Errorf("failed tool call not marked in commands: %q", commands)
		}
		if !slices.ContainsFunc(commands, func(c string) bool {
			return strings.HasPrefix(c, "djinn question revise 01a1481f-")
		}) {
			t.Errorf("succeeded tool call missing from commands: %q", commands)
		}
	})

	// A command whose tool result fails (ToolError or an error/declined/exit prefix) is marked "failed: …" in the
	// batch's journal and in the next batch's prompt (W453).
	t.Run("failed_tool_call_marked_in_journal", func(t *testing.T) {
		t.Parallel()
		agent := newLeadAgent(func(n int) ([]Event, error) {
			if n == 0 {
				return []Event{
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: `Bash {"command":"djinn task spawn w1 --prompt \"# Gain\""}`},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "error: Permission to use Bash has been denied", ToolError: true},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: `Bash {"command":"djinn task spawn w1 --prompt \"**Gain**\""}`},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "W1 spawned"},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: `run_command {"CommandLine":"djinn wish list --short"}`},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "W1"},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, Text: `write_to_file {"TargetFile":"/home/d/lead/plan.md"}`},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, Text: "ok"},
					{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "Spawned W1 after retry."},
				}, nil
			}
			return leadTurn("djinn task list --short", "Checked W1."), nil
		})
		b := driveLead(t, agent)
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W1", "Done.")
		waitFor(t, "first batch done", func() bool { return len(b.batches(t)) == 1 && len(b.waiting(t)) == 0 })
		first := b.batches(t)[0]
		want := []string{
			`failed: djinn task spawn w1 --prompt "# Gain"`,
			`djinn task spawn w1 --prompt "**Gain**"`,
			`djinn wish list --short`,
			`write_to_file plan.md`,
		}
		if !slices.Equal(first.GetCommands(), want) {
			t.Fatalf("first batch commands = %q, want %q", first.GetCommands(), want)
		}
		b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W1", "Done again.")
		waitFor(t, "second batch done", func() bool { return len(b.batches(t)) == 2 && len(b.waiting(t)) == 0 })
		secondPrompt := agent.all()[1].Prompt
		if !strings.Contains(secondPrompt, "- `failed: djinn task spawn w1 --prompt \"# Gain\"`") ||
			!strings.Contains(secondPrompt, "- `djinn task spawn w1 --prompt \"**Gain**\"`") {
			t.Errorf("second batch prompt lacks failed/succeeded commands in journal:\n%s", secondPrompt)
		}
	})
}
