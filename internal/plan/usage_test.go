package plan

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestAccountsUsage: djinn lead accounts says each sign-in's usage as the harness knows it, in a line each, and gives
// it whole with --json; without a harness, no usage.
func TestAccountsUsage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, err := store.Open(ctx, "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wishes := &Wishes{Store: st, Home: t.TempDir()}
	svc := &LeadService{Wishes: wishes, UserHome: t.TempDir()}
	res, err := svc.Accounts(ctx, connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Msg.GetText(), "Usage") || len(res.Msg.GetUsage()) != 0 {
		t.Errorf("no harness: %q, %v", res.Msg.GetText(), res.Msg.GetUsage())
	}

	read := time.Date(2026, 11, 5, 6, 10, 0, 0, time.Local)
	usage := []*planv1.AccountUsage{
		{Provider: planv1.Provider_PROVIDER_CLAUDE, ProbeError: "no usage limits told: signed out, or on an API key"},
		{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: "work", FiveHour: &planv1.UsageWindow{UsedPercent: 44},
			SevenDay: &planv1.UsageWindow{UsedPercent: 27, ResetsTime: timestamppb.New(time.Date(2026, 11, 11, 11, 0, 0, 0, time.Local))},
			ReadTime: timestamppb.New(read), Source: planv1.UsageSource_USAGE_SOURCE_PROBE, Next: true},
	}
	svc.Usage = func() []*planv1.AccountUsage { return usage }
	res, err = svc.Accounts(ctx, connect.NewRequest(&planv1.LeadServiceAccountsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- Claude Code's own sign-in: not read; probe failed: no usage limits told: signed out, or on an API key\n",
		"- work (Claude Code): 5 h 44 %, week 27 % (resets Wed 11 Nov 11:00), read at 06:10 by the probe; next\n",
	} {
		if !strings.Contains(res.Msg.GetText(), want) {
			t.Errorf("text lacks %q:\n%s", want, res.Msg.GetText())
		}
	}
	if len(res.Msg.GetUsage()) != 2 {
		t.Errorf("usage = %v", res.Msg.GetUsage())
	}
}
