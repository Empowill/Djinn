package plan

import (
	"cmp"
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

// CircleRowOrder is the order of worker rows in the Summoning Circle (W400): Djinn (lead) first, then each kind of
// worker that calls a model.
var CircleRowOrder = []planv1.WorkerKind{
	planv1.WorkerKind_WORKER_KIND_LEAD,
	planv1.WorkerKind_WORKER_KIND_INVESTIGATION,
	planv1.WorkerKind_WORKER_KIND_MOCKUP,
	planv1.WorkerKind_WORKER_KIND_IFRIT,
	planv1.WorkerKind_WORKER_KIND_WORK,
	planv1.WorkerKind_WORKER_KIND_CORRECTION,
	planv1.WorkerKind_WORKER_KIND_REVIEW,
	planv1.WorkerKind_WORKER_KIND_QUESTION,
}

// circleModelAliases maps human-readable labels and short aliases to canonical model identifiers so Djinn always
// stores and sends model IDs, never labels with spaces.
var circleModelAliases = map[string]string{
	"claude sonnet 5.5":       "claude-sonnet-5-5",
	"sonnet":                  "claude-sonnet-5-5",
	"claude opus 5.8":         "claude-opus-5-8",
	"claude opus 5.5":         "claude-opus-5-5",
	"opus":                    "claude-opus-5-8",
	"claude haiku 5.5":        "claude-haiku-5-5",
	"haiku":                   "claude-haiku-5-5",
	"gpt-5.5":                 "gpt-5.5",
	"gpt 5.5":                 "gpt-5.5",
	"gpt-5.5 codex":           "gpt-5.5-codex",
	"gpt 5.5 codex":           "gpt-5.5-codex",
	"gpt-5.5 mini":            "gpt-5.5-mini",
	"gpt 5.5 mini":            "gpt-5.5-mini",
	"gemini 4 argon (high)":   "gemini-4-argon-high",
	"gemini 4 argon":          "gemini-4-argon-high",
	"argon":                   "gemini-4-argon-high",
	"gemini 3.8 flash (high)": "gemini-3.8-flash-high",
	"gemini 3.8 flash":        "gemini-3.8-flash-high",
	"gemini 3.8 pro (high)":   "gemini-3.8-pro-high",
	"gemini 3.8 pro":          "gemini-3.8-pro-high",
}

// NormalizeCircleModel maps a human-readable model label or short alias to its canonical model ID.
func NormalizeCircleModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if id, ok := circleModelAliases[strings.ToLower(trimmed)]; ok {
		return id
	}
	return trimmed
}

// DefaultCircleModel returns the default canonical model ID for a provider in the Summoning Circle.
func DefaultCircleModel(p planv1.Provider) string {
	switch p {
	case planv1.Provider_PROVIDER_CLAUDE:
		return "claude-sonnet-5-5"
	case planv1.Provider_PROVIDER_CODEX:
		return "gpt-5.5"
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		return "gemini-4-argon-high"
	default:
		return ""
	}
}

// ProviderForCircleModel infers the provider for a model ID or label; PROVIDER_UNSPECIFIED when unknown.
func ProviderForCircleModel(model string) planv1.Provider {
	m := NormalizeCircleModel(model)
	switch {
	case m == "":
		return planv1.Provider_PROVIDER_UNSPECIFIED
	case IsClaudeModel(m):
		return planv1.Provider_PROVIDER_CLAUDE
	case IsCodexModel(m):
		return planv1.Provider_PROVIDER_CODEX
	case IsAntigravityModel(m):
		return planv1.Provider_PROVIDER_ANTIGRAVITY
	default:
		return planv1.Provider_PROVIDER_UNSPECIFIED
	}
}

// setCircleRowSlice sets or replaces the row for seat.Kind in circle.
func setCircleRowSlice(circle []*planv1.WorkerSeat, seat *planv1.WorkerSeat) []*planv1.WorkerSeat {
	out := make([]*planv1.WorkerSeat, 0, len(circle)+1)
	replaced := false
	for _, r := range circle {
		if r.GetKind() == seat.GetKind() {
			out = append(out, proto.CloneOf(seat))
			replaced = true
		} else {
			out = append(out, proto.CloneOf(r))
		}
	}
	if !replaced {
		out = append(out, proto.CloneOf(seat))
	}
	return out
}

// MigrateCircle reads any legacy per-project provider/model settings from projects/*/settings.txtpb in home once,
// seeds the Summoning Circle in settings.json for rows not yet configured, and drops those fields from the
// per-project developer files.
func MigrateCircle(home string) {
	if home == "" {
		return
	}
	matches, err := filepath.Glob(filepath.Join(home, "projects", "*", "settings.txtpb"))
	if err != nil {
		return
	}
	for _, devFile := range matches {
		migrateDeveloperSettingsFile(home, devFile)
	}
}

// ReadCircle returns the Summoning Circle rows in CircleRowOrder from settings.json in home after migrating any
// legacy per-project settings. The lead row always carries a concrete provider and model ID; every other row carries
// PROVIDER_UNSPECIFIED and "" when set to "Same as lead".
func ReadCircle(home string) []*planv1.WorkerSeat {
	MigrateCircle(home)
	settings := readSettings(home)
	byKind := make(map[planv1.WorkerKind]*planv1.WorkerSeat, len(settings.GetCircle()))
	for _, r := range settings.GetCircle() {
		byKind[r.GetKind()] = r
	}
	leadSeat := ReadLeadSeat(home)
	leadProvider := leadSeat.GetProvider()
	if leadProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
		leadProvider = planv1.Provider_PROVIDER_CLAUDE
	}
	leadModel := NormalizeCircleModel(leadSeat.GetModel())
	if r := byKind[planv1.WorkerKind_WORKER_KIND_LEAD]; r != nil {
		if r.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED {
			leadProvider = r.GetProvider()
		}
		if r.GetModel() != "" {
			leadModel = NormalizeCircleModel(r.GetModel())
		}
	}
	if leadModel == "" {
		leadModel = DefaultCircleModel(leadProvider)
	}

	out := make([]*planv1.WorkerSeat, 0, len(CircleRowOrder))
	for _, kind := range CircleRowOrder {
		if kind == planv1.WorkerKind_WORKER_KIND_LEAD {
			out = append(out, &planv1.WorkerSeat{
				Kind:     planv1.WorkerKind_WORKER_KIND_LEAD,
				Provider: leadProvider,
				Model:    leadModel,
			})
			continue
		}
		r := byKind[kind]
		if r == nil || (r.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED && r.GetModel() == "") {
			out = append(out, &planv1.WorkerSeat{Kind: kind})
			continue
		}
		p := r.GetProvider()
		m := NormalizeCircleModel(r.GetModel())
		if p == planv1.Provider_PROVIDER_UNSPECIFIED {
			p = ProviderForCircleModel(m)
		}
		if m == "" && p != planv1.Provider_PROVIDER_UNSPECIFIED {
			m = DefaultCircleModel(p)
		}
		out = append(out, &planv1.WorkerSeat{
			Kind:     kind,
			Provider: p,
			Model:    m,
		})
	}
	return out
}

// ResolveWorkerSeat returns the effective provider and canonical model ID for kind from the Summoning Circle in home.
// When the row is set to "Same as lead", it follows the lead's provider and model ID. explicit is true when the
// circle or lead seat has been configured in settings.json (or migrated from a per-project developer file).
func ResolveWorkerSeat(home string, kind planv1.WorkerKind) (provider planv1.Provider, model string, explicit bool) {
	if home == "" {
		return planv1.Provider_PROVIDER_CLAUDE, "", false
	}
	MigrateCircle(home)
	settings := readSettings(home)
	for _, r := range settings.GetCircle() {
		if r.GetKind() == kind && (r.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || r.GetModel() != "") {
			p := r.GetProvider()
			m := NormalizeCircleModel(r.GetModel())
			if p == planv1.Provider_PROVIDER_UNSPECIFIED {
				p = ProviderForCircleModel(m)
			}
			if p == planv1.Provider_PROVIDER_UNSPECIFIED {
				p = planv1.Provider_PROVIDER_CLAUDE
			}
			if m == "" {
				m = DefaultCircleModel(p)
			}
			return p, m, true
		}
	}
	// Row is "Same as lead" (or WORKER_KIND_LEAD): follow the lead's choice if the circle or lead seat is configured.
	followLead := func(p planv1.Provider, m string) (planv1.Provider, string, bool) {
		if kind == planv1.WorkerKind_WORKER_KIND_IFRIT && !CanIfrit(p) {
			return planv1.Provider_PROVIDER_CLAUDE, DefaultCircleModel(planv1.Provider_PROVIDER_CLAUDE), true
		}
		if kind == planv1.WorkerKind_WORKER_KIND_QUESTION && !CanReadOnly(p) {
			return planv1.Provider_PROVIDER_CLAUDE, DefaultCircleModel(planv1.Provider_PROVIDER_CLAUDE), true
		}
		return p, m, true
	}
	for _, r := range settings.GetCircle() {
		if r.GetKind() == planv1.WorkerKind_WORKER_KIND_LEAD && (r.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || r.GetModel() != "") {
			p := r.GetProvider()
			m := NormalizeCircleModel(r.GetModel())
			if p == planv1.Provider_PROVIDER_UNSPECIFIED {
				p = cmp.Or(settings.GetLead().GetProvider(), ProviderForCircleModel(m), planv1.Provider_PROVIDER_CLAUDE)
			}
			if m == "" {
				m = DefaultCircleModel(p)
			}
			return followLead(p, m)
		}
	}
	if lead := settings.GetLead(); lead != nil && (lead.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || lead.GetModel() != "") {
		p := lead.GetProvider()
		m := NormalizeCircleModel(lead.GetModel())
		if p == planv1.Provider_PROVIDER_UNSPECIFIED {
			p = cmp.Or(ProviderForCircleModel(m), planv1.Provider_PROVIDER_CLAUDE)
		}
		if m == "" {
			m = DefaultCircleModel(p)
		}
		return followLead(p, m)
	}
	if len(settings.GetCircle()) > 0 {
		return planv1.Provider_PROVIDER_CLAUDE, DefaultCircleModel(planv1.Provider_PROVIDER_CLAUDE), true
	}
	return planv1.Provider_PROVIDER_CLAUDE, "", false
}

// ReadLeadSeat is where the lead Djinn drives runs, from settings.json in home: Claude, its default model and
// sign-in, unless the developer moved it.
func ReadLeadSeat(home string) *planv1.LeadSeat {
	settings := readSettings(home)
	seat := proto.CloneOf(settings.GetLead())
	if seat == nil {
		seat = &planv1.LeadSeat{}
	}
	for _, r := range settings.GetCircle() {
		if r.GetKind() == planv1.WorkerKind_WORKER_KIND_LEAD {
			if seat.Provider == planv1.Provider_PROVIDER_UNSPECIFIED && r.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED {
				seat.Provider = r.GetProvider()
			}
			if seat.Model == "" && r.GetModel() != "" {
				seat.Model = NormalizeCircleModel(r.GetModel())
			}
			break
		}
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
			if len(settings.Circle) > 0 {
				settings.Circle = setCircleRowSlice(settings.Circle, &planv1.WorkerSeat{
					Kind:     planv1.WorkerKind_WORKER_KIND_LEAD,
					Provider: seat.GetProvider(),
					Model:    NormalizeCircleModel(seat.GetModel()),
				})
			}
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

// GetCircle implements LeadService.GetCircle: returns the global Summoning Circle rows for all worker kinds.
func (s *LeadService) GetCircle(
	_ context.Context, _ *connect.Request[planv1.LeadServiceGetCircleRequest],
) (*connect.Response[planv1.LeadServiceGetCircleResponse], error) {
	return connect.NewResponse(&planv1.LeadServiceGetCircleResponse{
		Rows: ReadCircle(s.Wishes.home()),
	}), nil
}

// SetCircle implements LeadService.SetCircle: validates and stores one row of the global Summoning Circle in
// settings.json, keeping the lead seat in sync when WORKER_KIND_LEAD changes.
func (s *LeadService) SetCircle(
	ctx context.Context, req *connect.Request[planv1.LeadServiceSetCircleRequest],
) (*connect.Response[planv1.LeadServiceSetCircleResponse], error) {
	home := s.Wishes.home()
	MigrateCircle(home)
	kind := req.Msg.GetKind()
	if !slices.Contains(CircleRowOrder, kind) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown worker kind %v", kind))
	}
	rawModel := req.Msg.GetModel()
	leadMoved := false
	if kind == planv1.WorkerKind_WORKER_KIND_LEAD {
		p := req.Msg.GetProvider()
		if p == planv1.Provider_PROVIDER_UNSPECIFIED {
			if rawModel != "" {
				p = ProviderForCircleModel(rawModel)
			}
			if p == planv1.Provider_PROVIDER_UNSPECIFIED {
				p = ReadLeadSeat(home).GetProvider()
			}
		}
		if err := LeadAgent(p); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if p != planv1.Provider_PROVIDER_FAKE {
			if err := ValidateProviderModel(p, rawModel); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
		}
		leadMoved = true
	} else if req.Msg.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || rawModel != "" {
		p := req.Msg.GetProvider()
		if p == planv1.Provider_PROVIDER_UNSPECIFIED {
			p = ProviderForCircleModel(rawModel)
		}
		if kind == planv1.WorkerKind_WORKER_KIND_QUESTION && !CanReadOnly(p) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"provider %s cannot run a question worker (read-only): choose claude or codex", providerName(p)))
		}
		if kind == planv1.WorkerKind_WORKER_KIND_IFRIT && !CanIfrit(p) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"provider %s cannot run an ifrit: choose claude or antigravity", providerName(p)))
		}
		if err := ValidateProviderModel(p, rawModel); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(*store.Tx) error {
		return changeSettings(home, func(settings *uiv1.Settings) error {
			if kind == planv1.WorkerKind_WORKER_KIND_LEAD {
				p := req.Msg.GetProvider()
				m := NormalizeCircleModel(req.Msg.GetModel())
				if p == planv1.Provider_PROVIDER_UNSPECIFIED {
					p = cmp.Or(ProviderForCircleModel(m), settings.GetLead().GetProvider(), planv1.Provider_PROVIDER_CLAUDE)
				}
				if m == "" {
					m = DefaultCircleModel(p)
				}
				account := settings.GetLead().GetAccount()
				if account != "" && settings.GetLead().GetProvider() != p {
					account = ""
				}
				settings.Lead = &planv1.LeadSeat{Provider: p, Model: m, Account: account}
				settings.Circle = setCircleRowSlice(settings.Circle, &planv1.WorkerSeat{
					Kind:     planv1.WorkerKind_WORKER_KIND_LEAD,
					Provider: p,
					Model:    m,
				})
				return nil
			}
			if req.Msg.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED && req.Msg.GetModel() == "" {
				settings.Circle = setCircleRowSlice(settings.Circle, &planv1.WorkerSeat{Kind: kind})
				return nil
			}
			p := req.Msg.GetProvider()
			m := NormalizeCircleModel(req.Msg.GetModel())
			if p == planv1.Provider_PROVIDER_UNSPECIFIED {
				p = ProviderForCircleModel(m)
			}
			if m == "" {
				m = DefaultCircleModel(p)
			}
			settings.Circle = setCircleRowSlice(settings.Circle, &planv1.WorkerSeat{
				Kind:     kind,
				Provider: p,
				Model:    m,
			})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if leadMoved {
		if d := s.Wishes.drive(); d != nil && (d.Failure() != "" || d.LeadLimit() != nil) {
			d.SetLimit(nil, "")
			d.askRetry()
		}
	}
	s.Wishes.ChangeLead()
	return connect.NewResponse(&planv1.LeadServiceSetCircleResponse{Rows: ReadCircle(home)}), nil
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
