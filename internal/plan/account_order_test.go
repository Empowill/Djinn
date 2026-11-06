package plan

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

// orderText names each sign-in of order: "claude", "claude/work".
func orderText(order []SignIn) []string {
	var out []string
	for _, s := range order {
		out = append(out, SignInWord(s))
	}
	return out
}

// TestAccountOrder: without an order written, each agent's own sign-in comes first, then its accounts as added; a
// whole order written is kept, an account added since goes at the bottom, a forgotten one leaves it; an order that
// names an unknown sign-in, or one twice, is refused; SignIns reads the order of one agent.
func TestAccountOrder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settings := &uiv1.Settings{
		Accounts: []*planv1.Account{
			{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE},
			{Name: "gpt", Provider: planv1.Provider_PROVIDER_CODEX},
			{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, Paused: true},
		},
		PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CODEX},
	}
	data, err := protojson.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, settingsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := orderText(AccountOrder(home)), []string{"claude", "claude/work", "claude/perso", "codex", "codex/gpt",
		"antigravity"}; !slices.Equal(got, want) {
		t.Errorf("order by default = %v, want %v", got, want)
	}

	claude := func(name string) *uiv1.SignInRef {
		return &uiv1.SignInRef{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: name}
	}
	written, err := WriteAccountOrder(home, []*uiv1.SignInRef{claude("perso"), {Provider: planv1.Provider_PROVIDER_CODEX}, claude("")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude/perso", "codex", "claude", "claude/work", "codex/gpt", "antigravity"}
	if got := orderText(written); !slices.Equal(got, want) {
		t.Errorf("written = %v, want %v", got, want)
	}
	if got := orderText(AccountOrder(home)); !slices.Equal(got, want) {
		t.Errorf("read back = %v, want %v", got, want)
	}
	signIns := SignIns(home, planv1.Provider_PROVIDER_CLAUDE)
	if got := orderText(signIns); !slices.Equal(got, []string{"claude/perso", "claude", "claude/work"}) || !signIns[0].Paused {
		t.Errorf("claude's sign-ins = %v, %+v", got, signIns)
	}
	if codex := SignIns(home, planv1.Provider_PROVIDER_CODEX); !codex[0].Paused || codex[0].Name != "" {
		t.Errorf("codex's own pause lost: %+v", codex)
	}

	if err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Accounts = append(s.Accounts, &planv1.Account{Name: "late", Provider: planv1.Provider_PROVIDER_CLAUDE})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := orderText(AccountOrder(home)); got[len(got)-1] != "claude/late" {
		t.Errorf("an account added since: %v, want it at the bottom", got)
	}

	for _, bad := range [][]*uiv1.SignInRef{
		{claude("nobody")},
		{{Provider: planv1.Provider_PROVIDER_CODEX, Account: "work"}},
		{claude("work"), claude("work")},
	} {
		if _, err := WriteAccountOrder(home, bad); err == nil {
			t.Errorf("order %v written", bad)
		}
	}
	if got := orderText(AccountOrder(home)); got[0] != "claude/perso" {
		t.Errorf("a refused order changed it: %v", got)
	}
}

// TestLeadServiceOrder: djinn lead order writes the whole order, a sign-in left out after the ones named, and says it
// back; djinn lead accounts gives it, with the lead's pin; an unknown sign-in is refused, the order left as it was.
func TestLeadServiceOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, err := store.Open(ctx, "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := &LeadService{Wishes: &Wishes{Store: st, Home: t.TempDir()}, UserHome: t.TempDir()}
	home := svc.Wishes.home()
	if err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Accounts = []*planv1.Account{
			{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, Label: "Work"},
			{Name: "gpt", Provider: planv1.Provider_PROVIDER_CODEX},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Order(ctx, connect.NewRequest(&planv1.LeadServiceOrderRequest{SignIn: []string{"claude/work", "codex", "claude"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude/work", "codex", "claude", "codex/gpt", "antigravity"}
	if got := res.Msg.GetSignIn(); !slices.Equal(got, want) {
		t.Errorf("order written = %v, want %v", got, want)
	}
	if text := res.Msg.GetText(); !strings.Contains(text, "1. work (Work, Claude Code)\n2. Codex's own sign-in\n") {
		t.Errorf("text:\n%s", text)
	}

	for _, bad := range [][]string{{"claude/nobody"}, {"codex/work"}, {"claude", "claude"}} {
		_, err := svc.Order(ctx, connect.NewRequest(&planv1.LeadServiceOrderRequest{SignIn: bad}))
		if ce := new(connect.Error); !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument {
			t.Errorf("order %v: %v, want invalid_argument", bad, err)
		}
	}

	accounts, err := svc.Accounts(ctx, connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := accounts.Msg.GetOrder(); !slices.Equal(got, want) || accounts.Msg.GetPinned() {
		t.Errorf("accounts: order %v, pinned %v; want %v, not pinned", got, accounts.Msg.GetPinned(), want)
	}
	if err := PinLead(home, true); err != nil {
		t.Fatal(err)
	}
	if accounts, err = svc.Accounts(ctx, connect.NewRequest(&planv1.LeadServiceAccountsRequest{})); err != nil ||
		!accounts.Msg.GetPinned() {
		t.Errorf("accounts once pinned: %v, %v", accounts.Msg.GetPinned(), err)
	}
}

// TestLeadOverflowing: while the lead's batch runs past its limits, its sign-in's usage says so, with a line of its
// own when Djinn read nothing there; a batch that ended, or that runs under its limits, marks none.
func TestLeadOverflowing(t *testing.T) {
	t.Parallel()
	read := []*planv1.AccountUsage{{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: "work"}}
	running := &planv1.LeadBatch{Account: "work", Overflow: true, State: planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING}
	if got := leadOverflowing(read, running); len(got) != 1 || !got[0].GetOverflowing() {
		t.Errorf("work in overflow: %v", got)
	}
	if !strings.Contains(UsageText(read[0]), "in overflow") {
		t.Errorf("line: %q", UsageText(read[0]))
	}
	own := &planv1.LeadBatch{Overflow: true, State: planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING}
	if got := leadOverflowing(nil, own); len(got) != 1 || got[0].GetAccount() != "" || !got[0].GetOverflowing() ||
		got[0].GetProvider() != planv1.Provider_PROVIDER_CLAUDE {
		t.Errorf("own sign-in in overflow, nothing read: %v", got)
	}
	for _, b := range []*planv1.LeadBatch{
		{Account: "work", Overflow: true, State: planv1.LeadBatchState_LEAD_BATCH_STATE_DONE},
		{Account: "work", State: planv1.LeadBatchState_LEAD_BATCH_STATE_RUNNING},
	} {
		if got := leadOverflowing(nil, b); len(got) != 0 {
			t.Errorf("batch %v: %v", b, got)
		}
	}
}

// TestLeadServiceAccountMode: djinn lead account-mode writes the mode, dynamic by default, and djinn lead accounts
// says it; djinn lead unlimited writes the developer's word on a sign-in, read back on its SignIn, auto taking it off;
// an unknown sign-in is refused; forgetting an account forgets its word.
func TestLeadServiceAccountMode(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, err := store.Open(ctx, "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := &LeadService{Wishes: &Wishes{Store: st, Home: t.TempDir()}, UserHome: t.TempDir()}
	home := svc.Wishes.home()
	if err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Accounts = []*planv1.Account{{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := AccountModeOf(home); got != planv1.AccountMode_ACCOUNT_MODE_DYNAMIC {
		t.Errorf("mode by default %v, want dynamic", got)
	}
	if _, err := svc.AccountMode(ctx, connect.NewRequest(&planv1.LeadServiceAccountModeRequest{Mode: planv1.AccountMode_ACCOUNT_MODE_MANUAL})); err != nil {
		t.Fatal(err)
	}
	accounts, err := svc.Accounts(ctx, connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if accounts.Msg.GetMode() != planv1.AccountMode_ACCOUNT_MODE_MANUAL || !strings.Contains(accounts.Msg.GetText(), "Manual mode") {
		t.Errorf("accounts: mode %v, text:\n%s", accounts.Msg.GetMode(), accounts.Msg.GetText())
	}

	setting := func(provider planv1.Provider, name string) planv1.UnlimitedSetting {
		for _, s := range SignIns(home, provider) {
			if s.Name == name {
				return s.Unlimited
			}
		}
		return -1
	}
	for _, c := range []struct {
		signIn string
		set    planv1.UnlimitedSetting
	}{
		{"claude/work", planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED},
		{"codex", planv1.UnlimitedSetting_UNLIMITED_SETTING_LIMITED},
	} {
		if _, err := svc.Unlimited(ctx, connect.NewRequest(&planv1.LeadServiceUnlimitedRequest{SignIn: c.signIn, Unlimited: c.set})); err != nil {
			t.Fatal(err)
		}
	}
	if got := setting(planv1.Provider_PROVIDER_CLAUDE, "work"); got != planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED {
		t.Errorf("work: %v, want forced", got)
	}
	if got := setting(planv1.Provider_PROVIDER_CODEX, ""); got != planv1.UnlimitedSetting_UNLIMITED_SETTING_LIMITED {
		t.Errorf("codex: %v, want limited", got)
	}
	if _, err := svc.Unlimited(ctx, connect.NewRequest(&planv1.LeadServiceUnlimitedRequest{SignIn: "codex", Unlimited: planv1.UnlimitedSetting_UNLIMITED_SETTING_AUTO})); err != nil {
		t.Fatal(err)
	}
	if got := setting(planv1.Provider_PROVIDER_CODEX, ""); got != planv1.UnlimitedSetting_UNLIMITED_SETTING_UNSPECIFIED {
		t.Errorf("codex back to auto: %v", got)
	}
	_, err = svc.Unlimited(ctx, connect.NewRequest(&planv1.LeadServiceUnlimitedRequest{SignIn: "claude/nobody", Unlimited: planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED}))
	if ce := new(connect.Error); !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument {
		t.Errorf("unknown sign-in: %v, want invalid_argument", err)
	}
	if _, err := svc.RemoveAccount(ctx, connect.NewRequest(&planv1.LeadServiceRemoveAccountRequest{Name: "work"})); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(home).GetUnlimitedForced(); len(got) != 0 {
		t.Errorf("forced after forgetting work: %v", got)
	}
}
