package plan

// The order of the sign-ins (decision Q295 B, tilasm L49 v2). One list in settings.json (account_order) ranks the
// sign-ins of every agent, its own and its accounts, from the highest priority to the lowest: each new worker of an
// agent starts on the first free one of that agent in it, and so does each batch of the lead that follows the order
// (L2). The order never changes a worker's agent: a Claude worker reads the Claude rows only. A sign-in missing from
// the list (none written yet, or an account added since) comes after the ones it names, in the order by default:
// each agent's own sign-in, then its accounts as added.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

// signInAgents are the agents with a sign-in of their own, in the order by default.
var signInAgents = []planv1.Provider{
	planv1.Provider_PROVIDER_CLAUDE,
	planv1.Provider_PROVIDER_CODEX,
	planv1.Provider_PROVIDER_ANTIGRAVITY,
}

// AccountOrder is every sign-in of the agents on this machine in the developer's order, from settings.json in home.
func AccountOrder(home string) []SignIn {
	return orderedSignIns(readSettings(home))
}

// orderedSignIns are the sign-ins settings knows in its order: those account_order names first, as it names them,
// then the others in the order by default.
func orderedSignIns(settings *uiv1.Settings) []SignIn {
	var all []SignIn
	for _, p := range signInAgents {
		all = append(all, SignIn{Provider: p, Paused: slices.Contains(settings.GetPausedSignIns(), p)})
		for _, a := range settings.GetAccounts() {
			if a.GetProvider() == p {
				all = append(all, SignIn{Provider: p, Name: a.GetName(), Account: a, Paused: a.GetPaused()})
			}
		}
	}
	out := make([]SignIn, 0, len(all))
	taken := make([]bool, len(all))
	for _, ref := range settings.GetAccountOrder() {
		if i := slices.IndexFunc(all, func(s SignIn) bool { return s.Is(ref) }); i >= 0 && !taken[i] {
			out, taken[i] = append(out, all[i]), true
		}
	}
	for i, s := range all {
		if !taken[i] {
			out = append(out, s)
		}
	}
	for i := range out {
		switch {
		case slices.ContainsFunc(settings.GetUnlimitedForced(), out[i].Is):
			out[i].Unlimited = planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED
		case slices.ContainsFunc(settings.GetUnlimitedUnforced(), out[i].Is):
			out[i].Unlimited = planv1.UnlimitedSetting_UNLIMITED_SETTING_LIMITED
		}
	}
	return out
}

// Is tells whether ref names s.
func (s SignIn) Is(ref *uiv1.SignInRef) bool {
	return s.Provider == ref.GetProvider() && s.Name == ref.GetAccount()
}

// Ref names s in account_order.
func (s SignIn) Ref() *uiv1.SignInRef {
	return &uiv1.SignInRef{Provider: s.Provider, Account: s.Name}
}

// WriteAccountOrder writes the whole order of the sign-ins to settings.json in home, from the highest priority to the
// lowest (the gesture of the window, a drag or Alt+Up and Alt+Down, sends the whole list). Each sign-in it names must
// be known: an agent's own, or an account of that agent; none twice. One it leaves out keeps its place after them, in
// the order by default. Nothing stops or moves: the order counts at each next start of a worker and batch of the lead.
// It returns the order written, whole.
func WriteAccountOrder(home string, order []*uiv1.SignInRef) ([]SignIn, error) {
	var written []SignIn
	err := changeSettings(home, func(settings *uiv1.Settings) error {
		known := orderedSignIns(&uiv1.Settings{Accounts: settings.GetAccounts(), PausedSignIns: settings.GetPausedSignIns()})
		for i, ref := range order {
			if !slices.ContainsFunc(known, func(s SignIn) bool { return s.Is(ref) }) {
				return fmt.Errorf("no sign-in %s: djinn lead accounts lists them", refText(ref))
			}
			if slices.ContainsFunc(order[:i], func(o *uiv1.SignInRef) bool {
				return o.GetProvider() == ref.GetProvider() && o.GetAccount() == ref.GetAccount()
			}) {
				return fmt.Errorf("%s comes twice in the order", refText(ref))
			}
		}
		settings.AccountOrder = order
		written = orderedSignIns(settings)
		settings.AccountOrder = make([]*uiv1.SignInRef, len(written))
		for i, s := range written {
			settings.AccountOrder[i] = s.Ref()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// refText names ref for a reader: "the account work of Claude Code", "Codex's own sign-in".
func refText(ref *uiv1.SignInRef) string {
	name := agentNames[ref.GetProvider()]
	if name == "" {
		name = ref.GetProvider().String()
	}
	if ref.GetAccount() == "" {
		return name + "'s own sign-in"
	}
	return "the account " + ref.GetAccount() + " of " + name
}

// LeadPinned tells whether the lead stays on its seat's account whatever the order (settings.json in home,
// lead_pinned); false: each of its batches follows the order (L2).
func LeadPinned(home string) bool {
	return readSettings(home).GetLeadPinned()
}

// PinLead pins the lead on its seat's account, or lets it follow the order again, in settings.json in home.
func PinLead(home string, pinned bool) error {
	return changeSettings(home, func(settings *uiv1.Settings) error {
		settings.LeadPinned = &pinned
		return nil
	})
}

// SignInWord names s as djinn lead order takes it: its agent (claude) for its own sign-in, agent/name (claude/work)
// for one of its accounts.
func SignInWord(s SignIn) string {
	if s.Name == "" {
		return providerWord(s.Provider)
	}
	return providerWord(s.Provider) + "/" + s.Name
}

// parseSignIn is the sign-in word names (SignInWord), as account_order keeps it.
func parseSignIn(word string) (*uiv1.SignInRef, error) {
	agent, name, _ := strings.Cut(word, "/")
	p, ok := planv1.Provider_value["PROVIDER_"+strings.ToUpper(agent)]
	if !ok || !slices.Contains(signInAgents, planv1.Provider(p)) {
		return nil, fmt.Errorf("%q names no agent: claude, codex or antigravity, then /name for an account", word)
	}
	return &uiv1.SignInRef{Provider: planv1.Provider(p), Account: name}, nil
}

// signInLine names s in a line of the order: "the account work (Work subscription) of Claude Code".
func signInLine(s SignIn) string {
	if s.Account == nil {
		return agentNames[s.Provider] + "'s own sign-in"
	}
	return s.Name + " (" + accountLabel(s.Account) + ", " + agentNames[s.Provider] + ")"
}

// Order implements LeadService.Order: the whole order of the sign-ins, written to settings.json, then said back.
func (s *LeadService) Order(
	ctx context.Context, req *connect.Request[planv1.LeadServiceOrderRequest],
) (*connect.Response[planv1.LeadServiceOrderResponse], error) {
	refs := make([]*uiv1.SignInRef, 0, len(req.Msg.GetSignIn()))
	for _, word := range req.Msg.GetSignIn() {
		ref, err := parseSignIn(word)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		refs = append(refs, ref)
	}
	var written []SignIn
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		var err error
		written, err = WriteAccountOrder(s.Wishes.home(), refs)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := &planv1.LeadServiceOrderResponse{}
	var b strings.Builder
	b.WriteString("The order of the sign-ins, from the highest priority: each new worker, and each batch of the lead " +
		"unless it is pinned, starts on the first free one of its agent.\n")
	for i, si := range written {
		fmt.Fprintf(&b, "%d. %s\n", i+1, signInLine(si))
		res.SignIn = append(res.SignIn, SignInWord(si))
	}
	res.Text = b.String()
	s.Wishes.ChangeLead()
	return connect.NewResponse(res), nil
}

// Pin implements LeadService.Pin: the lead pinned on a sign-in of its agent, or with unpin following the order again;
// the events that wait after a failed batch or a limit go to its next batch at once.
func (s *LeadService) Pin(
	ctx context.Context, req *connect.Request[planv1.LeadServicePinRequest],
) (*connect.Response[planv1.LeadServicePinResponse], error) {
	home := s.Wishes.home()
	name, unpin := req.Msg.GetAccount(), req.Msg.GetUnpin()
	if unpin && name != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"--unpin takes no account: the lead follows the order of the sign-ins of its agent again"))
	}
	accounts := ReadAccounts(home)
	seat := ReadLeadSeat(home)
	if name != "" {
		a := FindAccount(accounts, name)
		switch {
		case a == nil:
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf(
				"no account is called %s: djinn lead accounts lists them", name))
		case a.GetProvider() != seat.GetProvider():
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"the account %s is %s's, and the lead runs %s: djinn lead move --provider %s --account %s moves it there",
				name, agentNames[a.GetProvider()], agentNames[seat.GetProvider()], providerWord(a.GetProvider()), name))
		}
	}
	seat.Account = name // Unpinned, the order picks each batch's: the seat names none.
	pinned := !unpin
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		return changeSettings(home, func(settings *uiv1.Settings) error {
			settings.Lead, settings.LeadPinned = seat, &pinned
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	note := "The lead is pinned on " + SeatText(seat, accounts) + ": each batch starts there whatever the order, " +
		"until you unpin it (djinn lead pin --unpin)."
	if unpin {
		note = "The lead follows the order again: each batch starts on the first free sign-in of " +
			agentNames[seat.GetProvider()] + " in the order of the accounts."
	}
	if d := s.Wishes.drive(); d != nil && (d.Failure() != "" || d.LeadLimit() != nil) {
		d.SetLimit(nil, "")
		d.askRetry()
		note += " The events that wait go to its next batch now."
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServicePinResponse{Seat: seat, Pinned: pinned, Note: note}), nil
}

// AccountModeOf is how a sign-in is picked on this machine (settings.json in home, account_mode): dynamic unless the
// developer chose the manual order (decision Q339 C).
func AccountModeOf(home string) planv1.AccountMode {
	if readSettings(home).GetAccountMode() == planv1.AccountMode_ACCOUNT_MODE_MANUAL {
		return planv1.AccountMode_ACCOUNT_MODE_MANUAL
	}
	return planv1.AccountMode_ACCOUNT_MODE_DYNAMIC
}

// WriteUnlimited writes the developer's word on whether the sign-in ref names is pseudo-unlimited to settings.json in
// home: forced, limited, or auto (none kept, Djinn's detection decides). The sign-in must be known.
func WriteUnlimited(home string, ref *uiv1.SignInRef, set planv1.UnlimitedSetting) error {
	return changeSettings(home, func(settings *uiv1.Settings) error {
		known := orderedSignIns(&uiv1.Settings{Accounts: settings.GetAccounts()})
		if !slices.ContainsFunc(known, func(s SignIn) bool { return s.Is(ref) }) {
			return fmt.Errorf("no sign-in %s: djinn lead accounts lists them", refText(ref))
		}
		same := func(r *uiv1.SignInRef) bool {
			return r.GetProvider() == ref.GetProvider() && r.GetAccount() == ref.GetAccount()
		}
		settings.UnlimitedForced = slices.DeleteFunc(settings.UnlimitedForced, same)
		settings.UnlimitedUnforced = slices.DeleteFunc(settings.UnlimitedUnforced, same)
		switch set {
		case planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED:
			settings.UnlimitedForced = append(settings.UnlimitedForced, ref)
		case planv1.UnlimitedSetting_UNLIMITED_SETTING_LIMITED:
			settings.UnlimitedUnforced = append(settings.UnlimitedUnforced, ref)
		}
		return nil
	})
}

// AccountMode implements LeadService.AccountMode: how a sign-in is picked, written to settings.json.
func (s *LeadService) AccountMode(
	ctx context.Context, req *connect.Request[planv1.LeadServiceAccountModeRequest],
) (*connect.Response[planv1.LeadServiceAccountModeResponse], error) {
	mode := req.Msg.GetMode()
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		return changeSettings(s.Wishes.home(), func(settings *uiv1.Settings) error {
			settings.AccountMode = mode
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	note := "Dynamic: each new worker, and each batch of the lead unless it is pinned, starts on the free sign-in of " +
		"its agent whose week ends soonest, then on one whose quota is not known, then on a pseudo-unlimited one, " +
		"each in the order."
	if mode == planv1.AccountMode_ACCOUNT_MODE_MANUAL {
		note = "Manual: each new worker, and each batch of the lead unless it is pinned, starts on the first free " +
			"sign-in of its agent in the order (djinn lead order)."
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceAccountModeResponse{Mode: mode, Note: note}), nil
}

// Unlimited implements LeadService.Unlimited: the developer's word on whether a sign-in is pseudo-unlimited.
func (s *LeadService) Unlimited(
	ctx context.Context, req *connect.Request[planv1.LeadServiceUnlimitedRequest],
) (*connect.Response[planv1.LeadServiceUnlimitedResponse], error) {
	ref, err := parseSignIn(req.Msg.GetSignIn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	set := req.Msg.GetUnlimited()
	err = write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		if err := WriteUnlimited(s.Wishes.home(), ref, set); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var note string
	switch set {
	case planv1.UnlimitedSetting_UNLIMITED_SETTING_FORCED:
		note = refText(ref) + " is pseudo-unlimited: in the dynamic mode it comes after the sign-ins of its agent " +
			"with a quota, and its quota never holds it back."
	case planv1.UnlimitedSetting_UNLIMITED_SETTING_LIMITED:
		note = refText(ref) + " counts with a quota, even when Djinn reads none."
	default:
		note = "Djinn's detection decides whether " + refText(ref) + " is pseudo-unlimited: an agent that tells no " +
			"quota, or a Claude sign-in on an API key, is."
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceUnlimitedResponse{Unlimited: set, Note: note}), nil
}
