package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// TestLeadBatchPrompt: a batch's session starts fresh in the lead's folder, on the short rules in its system prompt
// and a message that holds the events whole, the wish's brief and an empty journal; it may run djinn and nothing
// else, as the lead. Once it ends its turn, its events are acknowledged by its session, and the journal keeps what
// it ran, what it said and what it cost.
func TestLeadBatchPrompt(t *testing.T) {
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
}

// TestLeadSecondBatchStartsFresh: the next batch is a new session, never a resumed one, and its message carries the
// journal of the first: who led it, what it ran and what it said; the first batch's events are not in it again.
func TestLeadSecondBatchStartsFresh(t *testing.T) {
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
}

// TestLeadMessageWaitsForNextBatch: what the developer writes to the Djinn while a batch runs is an event of the
// queue, whole: the running session never gets it, the next batch does, and the first batch acknowledges only its
// own events.
func TestLeadMessageWaitsForNextBatch(t *testing.T) {
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
	time.Sleep(100 * time.Millisecond) // More than the settling: a batch would have started by now.
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
}

// TestLeadFailedBatchRetriedOnceThenReported: a batch whose session fails gets one new session; failed again, it is
// reported beside Lead, its events wait, and no session starts until a new event comes, which goes with them.
func TestLeadFailedBatchRetriedOnceThenReported(t *testing.T) {
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
	time.Sleep(100 * time.Millisecond)
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
}

// TestLeadRetriedByLead: after a reported limit, which no second session tries, Lead (LeadService.Resume) sends the
// waiting events to a new session at once, on the same seat, and opens no terminal.
func TestLeadRetriedByLead(t *testing.T) {
	t.Parallel()
	b := driveLead(t, newLeadAgent(func(n int) ([]Event, error) {
		if n < 1 {
			return nil, errors.New("You've hit your session limit")
		}
		return leadTurn("djinn brief", ""), nil
	}))
	b.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	b.started(t, 0)
	waitFor(t, "the failure reported", func() bool { return b.drive.Failure() != "" })
	res, err := (&plan.LeadService{Wishes: b.wishes}).Resume(t.Context(),
		connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetTerminal() != "" || !strings.Contains(res.Msg.GetNote(), "Djinn drives the lead") {
		t.Errorf("Resume = %v", res.Msg)
	}
	if again := b.started(t, 1); !strings.Contains(again.Prompt, "### W2 ended") {
		t.Errorf("the retried batch:\n%s", again.Prompt)
	}
}

// TestDrivenLeadRefusesWatch: while Djinn drives the lead, no session watches its events.
func TestDrivenLeadRefusesWatch(t *testing.T) {
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
}

// TestLeadDriverEndsACutBatch: a batch left running by a stop is failed at the next start, and its events go to a
// new session.
func TestLeadDriverEndsACutBatch(t *testing.T) {
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
	// A second driver on the same store, as the next djinn up: the first one's batch is cut.
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
}

// TestLeadBatchCostOnRecordedStream measures what a batch costs on the stream of a real one (testdata/claude/
// lead-batch.jsonl, W131), replayed by the test binary through the real process path, never a model: the journal
// keeps claude's own figure, $0.16, at the top of L10's estimate of $0.12 to $0.21 a batch; the price table's estimate
// is lower, since it prices every cache write at 5 minutes and claude -p writes them for an hour.
func TestLeadBatchCostOnRecordedStream(t *testing.T) {
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
	// The hour's cache writes at 8 $ a million tokens instead of 5: claude's figure.
	if hour := estimate + float64(u.GetCacheWriteTokens())*3/1e6; hour < 0.159 || hour > 0.161 {
		t.Errorf("estimated $%.4f, $%.4f with the hour's writes; claude counted $%.4f", estimate, hour, u.GetCostUsd())
	}
	t.Logf("a batch: $%.4f as claude counts it, $%.4f estimated by the price table; %d tokens read from the cache, %d written",
		u.GetCostUsd(), estimate, u.GetCacheReadTokens(), u.GetCacheWriteTokens())
}

// TestLeadToneFollowsTheThemeOnRecordedStream: each batch speaks in the tone of the theme the window showed as it
// started (decision Q36, W156), on the stream of a real batch replayed through the real process path, never a model:
// in light its message carries the solar Djinn's tone, in dark the demon's, never both; the rules that bound every
// tone are in the system prompt, the same text either way.
func TestLeadToneFollowsTheThemeOnRecordedStream(t *testing.T) {
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
		_, system, _ := strings.Cut(string(got), "--append-system-prompt\n")
		system, _, _ = strings.Cut(system, "\n--exclude-dynamic-system-prompt-sections")
		if !strings.Contains(system, "Your tone follows the window's theme") || strings.Contains(system, step.want) {
			t.Errorf("%v: the rules lack the tone's limits, or carry a tone:\n%s", step.theme, system)
		}
		if rules == "" {
			rules = system
		} else if system != rules {
			t.Errorf("the rules changed with the theme: the cache of the last session no longer serves")
		}
	}
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

// TestLeadRepliesToEachCommentOnRecordedStream: on a real batch (testdata/claude/lead-comments.jsonl, W120), the lead
// that gets a send of four comments reads them in one command, acts on them in the same batch, and replies to every
// one with djinn tilasm reply, each once, after reading them.
func TestLeadRepliesToEachCommentOnRecordedStream(t *testing.T) {
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
}
