package plan

// The order of the sign-ins (decision Q295 B, tilasm L49 v2). One list in settings.json (account_order) ranks the
// sign-ins of every agent, its own and its accounts, from the highest priority to the lowest: each new worker of an
// agent starts on the first free one of that agent in it, and so does each batch of the lead that follows the order
// (L2). The order never changes a worker's agent: a Claude worker reads the Claude rows only. A sign-in missing from
// the list (none written yet, or an account added since) comes after the ones it names, in the order by default:
// each agent's own sign-in, then its accounts as added.

import (
	"fmt"
	"slices"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
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
