package harness

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// seatBench is a driven lead whose Claude and Codex are two fake agents.
type seatBench struct {
	*leadBench
	codex *leadAgent
	lead  *plan.LeadService
	// asked are the seats the driver checked before offering them, after a limit.
	asked chan *planv1.LeadSeat
}

func driveSeats(t *testing.T, claude, codex func(n int) ([]Event, error), ready func(*planv1.LeadSeat) bool) *seatBench {
	t.Helper()
	s := &seatBench{codex: newLeadAgent(codex), asked: make(chan *planv1.LeadSeat, 20)}
	s.leadBench = driveLead(t, newLeadAgent(claude), func(d *LeadDriver) {
		d.Providers = map[planv1.Provider]Provider{
			planv1.Provider_PROVIDER_CLAUDE: d.Provider, planv1.Provider_PROVIDER_CODEX: s.codex,
		}
		d.Ready = func(_ context.Context, seat *planv1.LeadSeat, _ *planv1.Account) bool {
			s.asked <- seat
			return ready == nil || ready(seat)
		}
	})
	s.lead = &plan.LeadService{Wishes: s.wishes}
	return s
}

// codexStarted waits for the Codex session n to start, and returns its spec.
func (s *seatBench) codexStarted(t *testing.T, n int) Spec {
	t.Helper()
	select {
	case got := <-s.codex.started:
		if got != n {
			t.Fatalf("codex session %d started, want %d", got, n)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("codex session %d never started", n)
	}
	return s.codex.all()[n]
}

func (s *seatBench) addAccount(t *testing.T, name string, provider planv1.Provider) *planv1.Account {
	t.Helper()
	res, err := s.lead.AddAccount(t.Context(), connect.NewRequest(&planv1.LeadServiceAddAccountRequest{
		Name: name, Provider: provider, Label: strings.ToUpper(name), ConfigDirectory: filepath.Join(t.TempDir(), name),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetAccount()
}

func (s *seatBench) move(t *testing.T, provider planv1.Provider, model, account string) *planv1.LeadServiceMoveResponse {
	t.Helper()
	res, err := s.lead.Move(t.Context(), connect.NewRequest(&planv1.LeadServiceMoveRequest{
		Provider: provider, Model: model, Account: account,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg
}

// TestLeadMovesAtTheNextBatch: one move takes the lead to another agent, model and account; the batch that runs
// ends where it started, the next one starts on the new seat, with the lead's journal of the first, its account's
// folder in its environment, and the lead stays there.
func TestLeadMovesAtTheNextBatch(t *testing.T) {
	t.Parallel()
	s := driveSeats(t, func(int) ([]Event, error) { return leadTurn("djinn task spawn W4", "Spawned W4"), nil },
		func(int) ([]Event, error) { return leadTurn("djinn task list", "Read the tasks"), nil }, nil)
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended")
	s.started(t, 0)
	waitFor(t, "the first batch done", func() bool { return len(s.waiting(t)) == 0 })

	gpt := s.addAccount(t, "gpt", planv1.Provider_PROVIDER_CODEX)
	moved := s.move(t, planv1.Provider_PROVIDER_CODEX, "gpt-5.5", "gpt")
	if !strings.Contains(moved.GetNote(), "The next batch of the lead runs on Codex, model gpt-5.5, account GPT") {
		t.Errorf("note = %q", moved.GetNote())
	}
	if got := plan.ReadLeadSeat(s.home); got.GetProvider() != planv1.Provider_PROVIDER_CODEX || got.GetModel() != "gpt-5.5" ||
		got.GetAccount() != "gpt" {
		t.Errorf("seat kept = %v", got)
	}

	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_MERGED, "W4", "W4 merged")
	spec := s.codexStarted(t, 0)
	if spec.Model != "gpt-5.5" || !slices.Contains(spec.Env, "CODEX_HOME="+gpt.GetConfigDirectory()) ||
		spec.SystemPrompt == "" || !strings.Contains(spec.Prompt, "### W4 merged") ||
		!strings.Contains(spec.Prompt, "`djinn task spawn W4`") {
		t.Errorf("the codex batch: model %q, env %v, prompt:\n%s", spec.Model, spec.Env, spec.Prompt)
	}
	waitFor(t, "the codex batch done", func() bool { return len(s.waiting(t)) == 0 })
	batches := s.batches(t)
	if last := batches[len(batches)-1]; last.GetProvider() != planv1.Provider_PROVIDER_CODEX || last.GetAccount() != "gpt" {
		t.Errorf("the journal's last batch = %v", last)
	}

	// It stays there.
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W5", "W5 ended")
	spec = s.codexStarted(t, 1)
	if !slices.Contains(spec.Env, "CODEX_HOME="+gpt.GetConfigDirectory()) {
		t.Errorf("the next batch left the account: %v", spec.Env)
	}
	// Its journal says who led the batch before: the agent, its model as the agent said it, its account.
	if !strings.Contains(spec.Prompt, "· codex claude-opus-5-5 (account gpt), session") {
		t.Errorf("the journal lacks the seat:\n%s", spec.Prompt)
	}
	if n := len(s.agent.all()); n != 1 {
		t.Errorf("claude ran %d sessions; want the first only", n)
	}
}

// TestLeadLimitOffersMoves: a batch that hits the account's limit is not tried again: Djinn moves the lead nowhere by
// itself, says so beside Lead with the seats it may go to, the other account of its agent first, and its events
// wait, newer events too. One click (Move) sends them to the seat chosen at once.
func TestLeadLimitOffersMoves(t *testing.T) {
	t.Parallel()
	s := driveSeats(t, func(n int) ([]Event, error) {
		if n == 0 {
			limit := &Limit{What: "the account's session limit", Until: time.Now().Add(3 * time.Hour)}
			return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "rate limit rejected", Limit: limit}},
				errors.New("You've hit your session limit")
		}
		return leadTurn("djinn brief", "Took the batch over"), nil
	}, func(int) ([]Event, error) { return nil, nil }, func(seat *planv1.LeadSeat) bool {
		return seat.GetProvider() != planv1.Provider_PROVIDER_CODEX // Codex is signed out.
	})
	s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
	s.move(t, planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5", "")
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	s.started(t, 0)
	waitFor(t, "the limit reported", func() bool { return s.drive.LeadLimit() != nil })

	got, err := s.lead.Get(t.Context(), connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	limit := got.Msg.GetLimit()
	if !strings.Contains(limit.GetText(), "the account's session limit, resets at") || limit.GetUntil() == nil ||
		limit.GetSeat().GetAccount() != "" || !strings.Contains(got.Msg.GetStartError(), "Move the lead") {
		t.Errorf("limit = %v, start error %q", limit, got.Msg.GetStartError())
	}
	if moves := limit.GetMoves(); len(moves) != 1 || moves[0].GetAccount() != "work" ||
		moves[0].GetModel() != "claude-opus-5-5" {
		t.Errorf("moves = %v; want claude's work account, its model kept, codex left out", moves)
	}
	if len(got.Msg.GetAccounts()) != 1 || got.Msg.GetSeat().GetModel() != "claude-opus-5-5" {
		t.Errorf("Get = %v", got.Msg)
	}

	// No second session, even for a newer event.
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended")
	time.Sleep(150 * time.Millisecond)
	if n := len(s.agent.all()); n != 1 || len(s.waiting(t)) != 2 {
		t.Fatalf("%d sessions, %d events waiting after the limit", n, len(s.waiting(t)))
	}

	moved := s.move(t, planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5", "work")
	if !strings.Contains(moved.GetNote(), "The events that wait go to Claude Code, model claude-opus-5-5, account WORK now") {
		t.Errorf("note = %q", moved.GetNote())
	}
	spec := s.started(t, 1)
	if !strings.Contains(spec.Prompt, "### W2 ended") || !strings.Contains(spec.Prompt, "### W3 ended") ||
		!slices.ContainsFunc(spec.Env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") }) ||
		!slices.Contains(spec.Env, "ANTHROPIC_API_KEY=") {
		t.Errorf("the moved batch: env %v, prompt:\n%s", spec.Env, spec.Prompt)
	}
	waitFor(t, "the limit cleared", func() bool { return s.drive.LeadLimit() == nil && len(s.waiting(t)) == 0 })
}

// TestLeadLimitResets: with nowhere to move, the events wait for the reset, then go to the same seat.
func TestLeadLimitResets(t *testing.T) {
	t.Parallel()
	s := driveSeats(t, func(n int) ([]Event, error) {
		if n == 0 {
			return nil, errors.New("Claude AI usage limit reached|" + strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		}
		return leadTurn("djinn brief", ""), nil
	}, nil, func(*planv1.LeadSeat) bool { return false })
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	s.started(t, 0)
	waitFor(t, "the limit reported", func() bool { return s.drive.LeadLimit() != nil })
	if moves := s.drive.LeadLimit().GetMoves(); len(moves) != 0 {
		t.Errorf("moves = %v; want none", moves)
	}
	if spec := s.started(t, 1); !strings.Contains(spec.Prompt, "### W2 ended") {
		t.Errorf("the batch after the reset:\n%s", spec.Prompt)
	}
}

// TestLeadMoveRefused: Antigravity cannot lead, an account must exist and be its agent's, and the lead's account
// cannot be forgotten while it runs there; Djinn keeps no token, only the folder.
func TestLeadMoveRefused(t *testing.T) {
	t.Parallel()
	s := driveSeats(t, func(int) ([]Event, error) { return nil, nil }, nil, nil)
	work := s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
	for _, req := range []*planv1.LeadServiceMoveRequest{
		{Provider: planv1.Provider_PROVIDER_ANTIGRAVITY},
		{Provider: planv1.Provider_PROVIDER_CODEX, Account: "work"},
		{Account: "nobody"},
	} {
		if _, err := s.lead.Move(t.Context(), connect.NewRequest(req)); err == nil {
			t.Errorf("Move(%v) passed", req)
		}
	}
	if _, err := s.lead.AddAccount(t.Context(), connect.NewRequest(&planv1.LeadServiceAddAccountRequest{
		Name: "agy", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY, ConfigDirectory: t.TempDir(),
	})); err == nil {
		t.Error("an Antigravity account was named")
	}
	for _, dir := range []string{s.home, filepath.Join(s.home, "accounts"), work.GetConfigDirectory()} {
		if _, err := s.lead.AddAccount(t.Context(), connect.NewRequest(&planv1.LeadServiceAddAccountRequest{
			Name: "other", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: dir,
		})); err == nil {
			t.Errorf("an account in %s was named", dir)
		}
	}
	s.move(t, planv1.Provider_PROVIDER_CLAUDE, "", "work")
	if _, err := s.lead.RemoveAccount(t.Context(), connect.NewRequest(&planv1.LeadServiceRemoveAccountRequest{Name: "work"})); err == nil {
		t.Error("the lead's account was forgotten while it runs there")
	}
	s.move(t, planv1.Provider_PROVIDER_CLAUDE, "", "")
	if _, err := s.lead.RemoveAccount(t.Context(), connect.NewRequest(&planv1.LeadServiceRemoveAccountRequest{Name: "work"})); err != nil {
		t.Fatal(err)
	}
	res, err := s.lead.Accounts(t.Context(), connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.GetAccounts()) != 0 || !strings.Contains(res.Msg.GetText(), "No account") {
		t.Errorf("Accounts = %v", res.Msg)
	}
}
