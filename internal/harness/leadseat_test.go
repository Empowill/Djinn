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
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
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

func driveSeats(
	t *testing.T, claude, codex func(n int) ([]Event, error), ready func(*planv1.LeadSeat) bool, set ...func(*LeadDriver),
) *seatBench {
	t.Helper()
	s := &seatBench{codex: newLeadAgent(codex), asked: make(chan *planv1.LeadSeat, 20)}
	s.leadBench = driveLead(t, newLeadAgent(claude), append([]func(*LeadDriver){func(d *LeadDriver) {
		d.Providers = map[planv1.Provider]Provider{
			planv1.Provider_PROVIDER_CLAUDE: d.Provider, planv1.Provider_PROVIDER_CODEX: s.codex,
		}
		d.Ready = func(_ context.Context, seat *planv1.LeadSeat, _ *planv1.Account) bool {
			s.asked <- seat
			return ready == nil || ready(seat)
		}
	}}, set...)...)
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

// followOrder makes the driver's lead follow the order of the sign-ins, as Djinn's does (L2): h, on the driver's
// home, keeps the limits and the quotas.
func followOrder(h *Harness) func(*LeadDriver) {
	return func(d *LeadDriver) {
		h.home = d.Home
		d.accounts = h
	}
}

// writeOrder writes the order of the sign-ins of claude: "" for its own.
func (s *seatBench) writeOrder(t *testing.T, names ...string) {
	t.Helper()
	var order []*uiv1.SignInRef
	for _, n := range names {
		order = append(order, &uiv1.SignInRef{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: n})
	}
	if _, err := plan.WriteAccountOrder(s.home, order); err != nil {
		t.Fatal(err)
	}
}

// TestLeadFollowsOrder: the lead's batches start on the first free sign-in of its agent in the order (L2), its model
// kept; a limit it hits holds that sign-in for the workers too and the batch goes on at once on the next one; a pause
// skips one; pinned, the lead stays where it is.
func TestLeadFollowsOrder(t *testing.T) {
	t.Parallel()
	until := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	h := &Harness{}
	s := driveSeats(t, func(n int) ([]Event, error) {
		if n == 0 {
			limit := &Limit{What: "the account's session limit", Until: until}
			return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "rate limit rejected", Limit: limit}},
				errors.New("You've hit your session limit")
		}
		return leadTurn("djinn brief", "Took the batch over"), nil
	}, nil, nil, followOrder(h))
	work := s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
	perso := s.addAccount(t, "perso", planv1.Provider_PROVIDER_CLAUDE)
	s.writeOrder(t, "work", "", "perso")
	s.move(t, planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5", "")

	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	if spec := s.started(t, 0); configDir(spec) != work.GetConfigDirectory() || spec.Model != "claude-opus-5-5" {
		t.Errorf("first batch on %q, model %q: want work's folder, the seat's model", configDir(spec), spec.Model)
	}
	spec := s.started(t, 1)
	if configDir(spec) != "" || !strings.Contains(spec.Prompt, "### W2 ended") {
		t.Errorf("after work's limit, on %q: want claude's own sign-in, the same events:\n%s", configDir(spec), spec.Prompt)
	}
	waitFor(t, "the batch done", func() bool { return len(s.waiting(t)) == 0 })
	if l := s.drive.LeadLimit(); l != nil {
		t.Errorf("a limit reported: %v", l)
	}
	if o := h.accountOptions(planv1.Provider_PROVIDER_CLAUDE); o[0].Name != "work" || !o[0].LimitedUntil.Equal(until) {
		t.Errorf("work not held for the workers: %+v", o)
	}
	batches := s.batches(t)
	if len(batches) != 2 || batches[0].GetAccount() != "work" || batches[1].GetAccount() != "" {
		t.Errorf("journal: %v", batches)
	}

	if _, err := s.lead.PauseAccount(t.Context(), connect.NewRequest(&planv1.LeadServicePauseAccountRequest{
		Provider: planv1.Provider_PROVIDER_CLAUDE,
	})); err != nil {
		t.Fatal(err)
	}
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended")
	if got := configDir(s.started(t, 2)); got != perso.GetConfigDirectory() {
		t.Errorf("its own paused, work limited: on %q, want perso's", got)
	}
	waitFor(t, "the batch done", func() bool { return len(s.waiting(t)) == 0 })

	s.move(t, planv1.Provider_PROVIDER_CLAUDE, "", "work")
	if !plan.LeadPinned(s.home) {
		t.Error("a move to an account does not pin the lead")
	}
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W4", "W4 ended")
	if got := configDir(s.started(t, 3)); got != work.GetConfigDirectory() {
		t.Errorf("pinned on work, limited: on %q, want work's, as the developer chose", got)
	}
}

// TestLeadOverflow: with each sign-in of its agent full, the lead's batch starts in overflow on the one that may spend
// extra credits; refused there, then on the next, it waits for the first of their limits to reset, saying so.
func TestLeadOverflow(t *testing.T) {
	t.Parallel()
	now := time.Now()
	first, second := now.Add(2*time.Hour).Truncate(time.Second), now.Add(4*time.Hour).Truncate(time.Second)
	h := &Harness{}
	var overflowing []bool
	var d *LeadDriver
	s := driveSeats(t, func(n int) ([]Event, error) {
		overflowing = append(overflowing, d.Overflowing())
		reset := first
		if n == 1 {
			reset = second
		}
		limit := &Limit{What: "the account's session limit", Until: reset}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "rate limit rejected", Limit: limit}},
			errors.New("You've hit your session limit")
	}, nil, nil, followOrder(h), func(driver *LeadDriver) { d = driver })
	work := s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
	full := func(extra bool) Quota {
		return Quota{Read: now, Extra: &extra, FiveHour: &UsageWindow{Used: 1, ResetsAt: now.Add(time.Hour)}, SevenDay: &UsageWindow{Used: .5}}
	}
	h.noteQuota(planv1.Provider_PROVIDER_CLAUDE, "", full(false))
	h.noteQuota(planv1.Provider_PROVIDER_CLAUDE, "work", full(true))

	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	if got := configDir(s.started(t, 0)); got != work.GetConfigDirectory() {
		t.Errorf("first batch on %q, want work's: it may spend extra credits", got)
	}
	if got := configDir(s.started(t, 1)); got != "" {
		t.Errorf("refused on work, then on %q: want claude's own sign-in", got)
	}
	waitFor(t, "the limit reported", func() bool { return s.drive.LeadLimit() != nil })
	limit := s.drive.LeadLimit()
	if limit.GetUntil() == nil || !limit.GetUntil().AsTime().Equal(first) || !strings.Contains(s.drive.Failure(),
		"no other sign-in of its agent is free") {
		t.Errorf("limit until %v, why %q: want work's reset %v", limit.GetUntil().AsTime(), s.drive.Failure(), first)
	}
	if n := len(s.agent.all()); n != 2 || !slices.Equal(overflowing, []bool{true, true}) {
		t.Errorf("%d sessions, overflowing %v: want 2, both past their limits", n, overflowing)
	}
	if batches := s.batches(t); len(batches) != 2 || !batches[0].GetOverflow() || !batches[1].GetOverflow() {
		t.Errorf("journal: %v, want each batch marked in overflow", batches)
	}
}

// TestLeadPinAndUnpin: djinn lead pin keeps each batch of the lead on the sign-in named, whatever the order; --unpin
// takes the pin off, and the next batch starts again on the first free sign-in of its agent in the order (Q317).
func TestLeadPinAndUnpin(t *testing.T) {
	t.Parallel()
	h := &Harness{}
	s := driveSeats(t, func(int) ([]Event, error) { return leadTurn("djinn brief", "Took the batch over"), nil },
		nil, nil, followOrder(h))
	work := s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
	perso := s.addAccount(t, "perso", planv1.Provider_PROVIDER_CLAUDE)
	s.writeOrder(t, "work", "", "perso")
	pin := func(account string, unpin bool) *planv1.LeadServicePinResponse {
		t.Helper()
		res, err := s.lead.Pin(t.Context(), connect.NewRequest(&planv1.LeadServicePinRequest{Account: account, Unpin: unpin}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	if res := pin("perso", false); !res.GetPinned() || res.GetSeat().GetAccount() != "perso" || !plan.LeadPinned(s.home) {
		t.Errorf("pinned on perso: %v", res)
	}
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W2", "W2 ended")
	if got := configDir(s.started(t, 0)); got != perso.GetConfigDirectory() {
		t.Errorf("pinned on perso: on %q, want perso's, whatever the order", got)
	}
	waitFor(t, "the batch done", func() bool { return len(s.waiting(t)) == 0 })

	res := pin("", true)
	if res.GetPinned() || res.GetSeat().GetAccount() != "" || plan.LeadPinned(s.home) ||
		!strings.Contains(res.GetNote(), "follows the order again") {
		t.Errorf("unpinned: %v", res)
	}
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W3", "W3 ended")
	if got := configDir(s.started(t, 1)); got != work.GetConfigDirectory() {
		t.Errorf("unpinned: on %q, want work's, the first in the order", got)
	}
	waitFor(t, "the batch done", func() bool { return len(s.waiting(t)) == 0 })

	if res := pin("", false); !res.GetPinned() || res.GetSeat().GetAccount() != "" {
		t.Errorf("pinned on its own sign-in: %v", res)
	}
	s.notify(t, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, "W4", "W4 ended")
	if got := configDir(s.started(t, 2)); got != "" {
		t.Errorf("pinned on claude's own sign-in: on %q, want its default folder", got)
	}

	for _, bad := range []*planv1.LeadServicePinRequest{{Account: "work", Unpin: true}, {Account: "nobody"}} {
		if _, err := s.lead.Pin(t.Context(), connect.NewRequest(bad)); err == nil {
			t.Errorf("pin %v accepted", bad)
		}
	}
	s.addAccount(t, "gpt", planv1.Provider_PROVIDER_CODEX)
	if _, err := s.lead.Pin(t.Context(), connect.NewRequest(&planv1.LeadServicePinRequest{Account: "gpt"})); err == nil ||
		!strings.Contains(err.Error(), "djinn lead move --provider codex --account gpt") {
		t.Errorf("pin on codex's account while claude leads: %v", err)
	}
}

// TestLeadSeat tests moving the lead between providers, models, and accounts, and handling account limits.
func TestLeadSeat(t *testing.T) {
	t.Parallel()

	t.Run("moves_at_next_batch", func(t *testing.T) {
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
	})

	t.Run("limit_offers_moves", func(t *testing.T) {
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
		time.Sleep(80 * time.Millisecond)
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
	})

	t.Run("limit_resets", func(t *testing.T) {
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
	})

	t.Run("move_refused", func(t *testing.T) {
		t.Parallel()
		s := driveSeats(t, func(int) ([]Event, error) { return nil, nil }, nil, nil)
		work := s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
		for _, req := range []*planv1.LeadServiceMoveRequest{
			{Provider: planv1.Provider_PROVIDER_ANTIGRAVITY, Account: "work"},
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
		movedAgy := s.move(t, planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-4-argon-high", "")
		if !strings.Contains(movedAgy.GetNote(), "The next batch of the lead runs on Antigravity, model gemini-4-argon-high") {
			t.Errorf("agy move note = %q", movedAgy.GetNote())
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
	})

	// The developer pauses an account and an agent's own sign-in, then resumes them (T33): the workers' sign-ins
	// follow, the lead's seat stays.
	t.Run("pause", func(t *testing.T) {
		t.Parallel()
		s := driveSeats(t, func(int) ([]Event, error) { return nil, nil }, nil, nil)
		s.addAccount(t, "work", planv1.Provider_PROVIDER_CLAUDE)
		s.move(t, planv1.Provider_PROVIDER_CLAUDE, "", "work")
		paused, err := s.lead.PauseAccount(t.Context(), connect.NewRequest(&planv1.LeadServicePauseAccountRequest{Name: "work"}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(paused.Msg.GetNote(), "The account work (Claude Code) paused: no worker starts there") {
			t.Errorf("pause note = %q", paused.Msg.GetNote())
		}
		if _, err := s.lead.PauseAccount(t.Context(), connect.NewRequest(&planv1.LeadServicePauseAccountRequest{
			Provider: planv1.Provider_PROVIDER_ANTIGRAVITY,
		})); err != nil {
			t.Fatal(err)
		}
		agy := plan.SignIns(s.home, planv1.Provider_PROVIDER_ANTIGRAVITY)
		claude := plan.SignIns(s.home, planv1.Provider_PROVIDER_CLAUDE)
		if len(agy) != 1 || !agy[0].Paused || len(claude) != 2 || claude[0].Paused || claude[1].Name != "work" || !claude[1].Paused {
			t.Errorf("sign-ins: antigravity %v, claude %v", agy, claude)
		}
		if seat := plan.ReadLeadSeat(s.home); seat.GetAccount() != "work" {
			t.Errorf("the lead moved to %v", seat)
		}
		res, err := s.lead.Accounts(t.Context(), connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Antigravity's own sign-in is paused", "Claude Code, paused)"} {
			if !strings.Contains(res.Msg.GetText(), want) {
				t.Errorf("Accounts text lacks %q:\n%s", want, res.Msg.GetText())
			}
		}
		for _, req := range []*planv1.LeadServicePauseAccountRequest{
			{}, {Name: "nobody"}, {Name: "work", Provider: planv1.Provider_PROVIDER_CODEX}, {Provider: planv1.Provider_PROVIDER_FAKE},
		} {
			if _, err := s.lead.PauseAccount(t.Context(), connect.NewRequest(req)); err == nil {
				t.Errorf("PauseAccount(%v) passed", req)
			}
		}
		for _, req := range []*planv1.LeadServiceResumeAccountRequest{{Name: "work"}, {Provider: planv1.Provider_PROVIDER_ANTIGRAVITY}} {
			if _, err := s.lead.ResumeAccount(t.Context(), connect.NewRequest(req)); err != nil {
				t.Fatal(err)
			}
		}
		agy, claude = plan.SignIns(s.home, planv1.Provider_PROVIDER_ANTIGRAVITY), plan.SignIns(s.home, planv1.Provider_PROVIDER_CLAUDE)
		if agy[0].Paused || claude[1].Paused {
			t.Errorf("still paused: antigravity %v, claude %v", agy, claude)
		}
	})
}
