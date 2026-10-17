package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/store"
)

// Where the lead Djinn drives runs (W132, decision Q31): its seat, an agent, a model and an account, chosen by the
// developer in one gesture (djinn lead move, the lead's menu) and kept until changed. The accounts are folders of the
// agents' own, each signed in once with the agent's own command: Claude Code keeps its settings, sessions and login
// under CLAUDE_CONFIG_DIR (its macOS Keychain entry keyed to that folder too), Codex under CODEX_HOME (its keyring
// entry keyed to that folder too). Djinn names the folder and passes it on; it never reads nor stores a token. Both
// live in settings.json of Djinn's data folder, on this machine only.

// settingsFile is the window's settings that Djinn acts on (ui.v1.Settings), in Djinn's data folder.
const settingsFile = "settings.json"

// settingsMu keeps two changes of settings.json made by this process from losing one another.
var settingsMu sync.Mutex

// readSettings reads settings.json in home; empty when it is missing or unreadable.
func readSettings(home string) *uiv1.Settings {
	settings := &uiv1.Settings{}
	if home == "" {
		return settings
	}
	if data, err := os.ReadFile(filepath.Join(home, settingsFile)); err == nil {
		_ = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, settings)
	}
	return settings
}

// changeSettings applies change to settings.json in home, keeping the other settings, and writes it at once.
func changeSettings(home string, change func(*uiv1.Settings) error) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settings := readSettings(home)
	if err := change(settings); err != nil {
		return err
	}
	data, err := (protojson.MarshalOptions{Multiline: true}).Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return fsx.WriteAtomic(filepath.Join(home, settingsFile), data, 0o600)
}

// ReadLeadSeat is where the lead Djinn drives runs, from settings.json in home: Claude, its default model and
// sign-in, unless the developer moved it.
func ReadLeadSeat(home string) *planv1.LeadSeat {
	seat := proto.CloneOf(readSettings(home).GetLead())
	if seat == nil {
		seat = &planv1.LeadSeat{}
	}
	if seat.Provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		seat.Provider = planv1.Provider_PROVIDER_CLAUDE
	}
	return seat
}

// ReadAccounts are the accounts of the agents on this machine, from settings.json in home.
func ReadAccounts(home string) []*planv1.Account {
	return readSettings(home).GetAccounts()
}

// FindAccount is the account called name among accounts; nil when none is.
func FindAccount(accounts []*planv1.Account, name string) *planv1.Account {
	for _, a := range accounts {
		if a.GetName() == name {
			return a
		}
	}
	return nil
}

// accountVars are the variables that point each agent to a folder of its own, by its documentation: Claude Code's
// "Each directory has its own settings, session history, and claude.ai login or API key"
// (code.claude.com/docs/en/authentication), Codex's "Codex stores its local state under CODEX_HOME" with its
// auth.json (developers.openai.com/codex/config-advanced). Antigravity documents none (antigravity.google/docs).
var accountVars = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE: "CLAUDE_CONFIG_DIR",
	planv1.Provider_PROVIDER_CODEX:  "CODEX_HOME",
}

// accountDefaults are the agents' own folders, which an account never takes, and the prefix of an account's folder
// in the home folder: ~/.claude-work beside ~/.claude, as Claude Code's documentation names it.
var accountDefaults = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE: ".claude",
	planv1.Provider_PROVIDER_CODEX:  ".codex",
}

// claudeKeys are the variables that come before the login of a Claude Code folder (code.claude.com/docs/en/
// authentication, Authentication precedence): a session on an account clears them, so that the account's login is
// the one it uses.
var claudeKeys = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

// AccountEnv is what a session on account a adds to its environment: the agent's folder variable, and for Claude the
// variables that would win over its login, cleared. Nil for no account.
func AccountEnv(a *planv1.Account) []string {
	name := accountVars[a.GetProvider()]
	if a == nil || name == "" {
		return nil
	}
	env := []string{name + "=" + a.GetConfigDirectory()}
	if a.GetProvider() == planv1.Provider_PROVIDER_CLAUDE {
		for _, key := range claudeKeys {
			env = append(env, key+"=")
		}
	}
	return env
}

// accountLogins are the commands that sign each agent in, from its documentation: claude auth login
// (code.claude.com/docs/en/cli-reference), codex login (developers.openai.com/codex/cli/reference).
var accountLogins = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE: "claude auth login",
	planv1.Provider_PROVIDER_CODEX:  "codex login",
}

// AccountLogin is the command line that signs account a in, in its folder, for the user's shell on goos: cmd.exe on
// Windows, a POSIX shell elsewhere.
func AccountLogin(a *planv1.Account, goos string) string {
	name, login := accountVars[a.GetProvider()], accountLogins[a.GetProvider()]
	if name == "" {
		return ""
	}
	if goos == "windows" {
		return fmt.Sprintf(`set "%s=%s" && %s`, name, a.GetConfigDirectory(), login)
	}
	return fmt.Sprintf("%s=%s %s", name, shellQuote(a.GetConfigDirectory()), login)
}

// AccountStatus are the arguments of the command that tells whether an agent is signed in, exiting 0 when it is and
// 1 when not; nil for an agent that documents none.
func AccountStatus(provider planv1.Provider) []string {
	switch provider {
	case planv1.Provider_PROVIDER_CLAUDE:
		return []string{"auth", "status"}
	case planv1.Provider_PROVIDER_CODEX:
		return []string{"login", "status"}
	}
	return nil
}

// shellQuote quotes s for a POSIX shell, unless it needs none.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@~", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// agentNames are the agents a seat names, as the window does.
var agentNames = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE:      "Claude Code",
	planv1.Provider_PROVIDER_CODEX:       "Codex",
	planv1.Provider_PROVIDER_ANTIGRAVITY: "Antigravity",
	planv1.Provider_PROVIDER_FAKE:        "the fake agent",
}

// SeatText names seat for a reader: "Codex, model gpt-5.5, account Work".
func SeatText(seat *planv1.LeadSeat, accounts []*planv1.Account) string {
	provider := seat.GetProvider()
	if provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		provider = planv1.Provider_PROVIDER_CLAUDE
	}
	text := agentNames[provider]
	if m := seat.GetModel(); m != "" {
		text += ", model " + m
	}
	if name := seat.GetAccount(); name != "" {
		label := name
		if a := FindAccount(accounts, name); a != nil {
			label = accountLabel(a)
		}
		text += ", account " + label
	}
	return text
}

// SameSeat tells whether a and b run the lead on the same agent and account, whatever the model.
func SameSeat(a, b *planv1.LeadSeat) bool {
	pa, pb := a.GetProvider(), b.GetProvider()
	if pa == planv1.Provider_PROVIDER_UNSPECIFIED {
		pa = planv1.Provider_PROVIDER_CLAUDE
	}
	if pb == planv1.Provider_PROVIDER_UNSPECIFIED {
		pb = planv1.Provider_PROVIDER_CLAUDE
	}
	return pa == pb && a.GetAccount() == b.GetAccount()
}

// LeadAgent says why the driven lead cannot run on provider; nil when it can. Claude and Codex take its rules as
// their own instructions and a folder to write in; Antigravity takes neither, and refuses a session that may not edit
// everything (docs/providers.md). The fake agent plays a script, for tests and trials.
func LeadAgent(provider planv1.Provider) error {
	switch provider {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_FAKE:
		return nil
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		return errors.New("antigravity cannot lead yet: agy takes no instructions of Djinn's own beside the prompt, " +
			"and refuses a session that may not edit every file of its folder; move the lead to claude or codex")
	}
	return fmt.Errorf("%s is not an agent that can lead: move the lead to claude or codex", provider)
}

// LeadLimit is the usage limit the lead Djinn drives hit, and where it may move, until a batch ends its turn or it
// moves; nil when none.
func (d *LeadDrive) LeadLimit() *planv1.LeadLimit {
	d.mu.Lock()
	defer d.mu.Unlock()
	return proto.CloneOf(d.limit)
}

// SetLimit records the limit the lead hit, with why it failed: the window offers its moves beside Lead.
func (d *LeadDrive) SetLimit(l *planv1.LeadLimit, why string) {
	d.mu.Lock()
	d.limit, d.failure = proto.CloneOf(l), why
	changed := d.changed
	d.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// Move implements LeadService.Move: the seat is written to settings.json, the command to the journal. The waiting
// events, if a batch failed or hit a limit, go to the new seat at once.
func (s *LeadService) Move(
	ctx context.Context, req *connect.Request[planv1.LeadServiceMoveRequest],
) (*connect.Response[planv1.LeadServiceMoveResponse], error) {
	home := s.Wishes.home()
	m := req.Msg
	accounts := ReadAccounts(home)
	seat := &planv1.LeadSeat{Provider: m.GetProvider(), Model: m.GetModel(), Account: m.GetAccount()}
	if seat.Provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		seat.Provider = ReadLeadSeat(home).GetProvider()
	}
	if err := LeadAgent(seat.GetProvider()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if name := seat.GetAccount(); name != "" {
		a := FindAccount(accounts, name)
		switch {
		case a == nil:
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf(
				"no account is called %s: djinn lead accounts lists them, djinn lead add-account names one", name))
		case a.GetProvider() != seat.GetProvider():
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"the account %s is %s's, not %s's: give --provider %s too", name, agentNames[a.GetProvider()],
				agentNames[seat.GetProvider()], providerWord(a.GetProvider())))
		}
	}
	err := write(ctx, s.Wishes.Store, req.Spec(), m, func(*store.Tx) error {
		return changeSettings(home, func(settings *uiv1.Settings) error {
			settings.Lead = seat
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	note := "The next batch of the lead runs on " + SeatText(seat, accounts) + ", with the lead's journal; the lead " +
		"stays there until you move it."
	if d := s.Wishes.drive(); d == nil {
		note = "Djinn does not drive the lead on this machine (settings.json, lead_driven): the lead runs on " +
			SeatText(seat, accounts) + " once it does."
	} else if d.Failure() != "" || d.LeadLimit() != nil {
		d.SetLimit(nil, "")
		d.askRetry()
		note = "The events that wait go to " + SeatText(seat, accounts) + " now, with the lead's journal; the lead " +
			"stays there until you move it."
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceMoveResponse{Seat: seat, Note: note}), nil
}

// providerWord is how a command names an agent: claude, codex.
func providerWord(p planv1.Provider) string {
	return strings.ToLower(strings.TrimPrefix(p.String(), "PROVIDER_"))
}

// Accounts implements LeadService.Accounts.
func (s *LeadService) Accounts(
	_ context.Context, _ *connect.Request[planv1.LeadServiceAccountsRequest],
) (*connect.Response[planv1.LeadServiceAccountsResponse], error) {
	home := s.Wishes.home()
	accounts, seat := ReadAccounts(home), ReadLeadSeat(home)
	var b strings.Builder
	fmt.Fprintf(&b, "The lead runs on %s.\n\n", SeatText(seat, accounts))
	if len(accounts) == 0 {
		b.WriteString("No account: each agent runs on its own sign-in. djinn lead add-account <name> --provider " +
			"claude names one, in a folder of its own.\n")
	}
	for _, a := range accounts {
		fmt.Fprintf(&b, "- **%s** (%s, %s): %s\n  Sign in once: `%s`\n", a.GetName(), accountLabel(a), agentNames[a.GetProvider()],
			a.GetConfigDirectory(), AccountLogin(a, runtime.GOOS))
	}
	return connect.NewResponse(&planv1.LeadServiceAccountsResponse{Text: b.String(), Accounts: accounts, Seat: seat}), nil
}

// accountLabel is what the window calls a: its label, else its name.
func accountLabel(a *planv1.Account) string {
	if a.GetLabel() != "" {
		return a.GetLabel()
	}
	return a.GetName()
}

// AddAccount implements LeadService.AddAccount: the account is written to settings.json and its folder made; the
// agent signs in there itself, by the command it returns.
func (s *LeadService) AddAccount(
	ctx context.Context, req *connect.Request[planv1.LeadServiceAddAccountRequest],
) (*connect.Response[planv1.LeadServiceAddAccountResponse], error) {
	home := s.Wishes.home()
	m := req.Msg
	if accountVars[m.GetProvider()] == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%s documents no folder of its own per sign-in: only claude and codex have accounts", agentNames[m.GetProvider()]))
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	dir := m.GetConfigDirectory()
	if dir == "" {
		dir = filepath.Join(userHome, accountDefaults[m.GetProvider()]+"-"+m.GetName())
	}
	if !filepath.IsAbs(dir) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("config_directory: %q is not an absolute path", dir))
	}
	dir = filepath.Clean(dir)
	if err := accountFolder(dir, home, userHome, m.GetProvider()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	a := &planv1.Account{Name: m.GetName(), Provider: m.GetProvider(), Label: strings.TrimSpace(m.GetLabel()), ConfigDirectory: dir}
	err = write(ctx, s.Wishes.Store, req.Spec(), m, func(*store.Tx) error {
		return changeSettings(home, func(settings *uiv1.Settings) error {
			for _, old := range settings.GetAccounts() {
				switch {
				case old.GetName() == a.GetName():
					return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("an account is called %s already", a.GetName()))
				case filepath.Clean(old.GetConfigDirectory()) == dir:
					return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("the account %s has %s already", old.GetName(), dir))
				}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return connect.NewError(connect.CodeFailedPrecondition, err)
			}
			settings.Accounts = append(settings.Accounts, a)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceAddAccountResponse{Account: a, LoginCommand: AccountLogin(a, runtime.GOOS)}), nil
}

// accountFolder says why dir cannot be an account's folder: Djinn's data folder or a folder in it (a login would be
// kept in Djinn's), the home folder, or the agent's default folder (that is no account but its own sign-in).
func accountFolder(dir, djinnHome, userHome string, provider planv1.Provider) error {
	inside := func(parent string) bool {
		rel, err := filepath.Rel(filepath.Clean(parent), dir)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	switch {
	case djinnHome != "" && inside(djinnHome):
		return fmt.Errorf("%s is in Djinn's data folder, which keeps no login: give a folder of the agent's own", dir)
	case dir == filepath.Clean(userHome):
		return fmt.Errorf("%s is your home folder: give a folder of the agent's own", dir)
	case dir == filepath.Join(userHome, accountDefaults[provider]):
		return fmt.Errorf("%s is %s's own folder, its default sign-in: an account takes another one", dir, agentNames[provider])
	}
	return nil
}

// RemoveAccount implements LeadService.RemoveAccount: Djinn forgets the account; its folder stays.
func (s *LeadService) RemoveAccount(
	ctx context.Context, req *connect.Request[planv1.LeadServiceRemoveAccountRequest],
) (*connect.Response[planv1.LeadServiceRemoveAccountResponse], error) {
	home := s.Wishes.home()
	name := req.Msg.GetName()
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		return changeSettings(home, func(settings *uiv1.Settings) error {
			if settings.GetLead().GetAccount() == name {
				return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
					"the lead runs on the account %s: move it first (djinn lead move)", name))
			}
			n := len(settings.Accounts)
			settings.Accounts = slices.DeleteFunc(settings.Accounts, func(a *planv1.Account) bool { return a.GetName() == name })
			if len(settings.Accounts) == n {
				return connect.NewError(connect.CodeNotFound, fmt.Errorf("no account is called %s", name))
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceRemoveAccountResponse{}), nil
}

// LimitWait is how long the events of a lead that hit a limit wait before the same seat tries again, when the agent
// did not say when the limit resets.
const LimitWait = 15 * time.Minute
