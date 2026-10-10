package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Leads runs the terminal of a wish's lead and shows it in the window: djinn up provides it, on its terminals and its
// window.
type Leads interface {
	// Open runs line through the user's shell in the terminal called name, in dir, or attaches to that terminal if
	// it runs; an empty line runs the shell itself, and an empty dir the home folder. It refuses to start a program
	// while a terminal of another name runs a command line that holds exclusive. It returns the command line and
	// the folder of the terminal.
	Open(name, line, dir, exclusive string) (command []string, directory string, attached bool, err error)
	// Show brings the window to the front, on the wish and the terminal.
	Show(wishID, terminal string)
	// Running tells whether the terminal called name runs a program.
	Running(name string) bool
	// Exit reports the last lead terminal's exit, nil while it runs or has never started.
	Exit(name string) *planv1.LeadExit
	// Stop asks the program of the terminal called name to end, as a terminal that closes does, kills it if it is
	// still there after a grace, and returns once it ended. Nothing runs there: nothing to do.
	Stop(name string) error
	// Tell types text into the terminal called name as the developer would, then Enter, once nothing is being typed
	// there and no choice is on screen; it returns at once, with whether the text waits, ErrNoLead when the terminal
	// runs no program, or ErrNotLead when it runs another program than a lead's line (LeadLine). Texts told arrive
	// in order.
	Tell(name, text string) (planv1.TellWait, error)
	// Prompt is the choice the program of the terminal called name shows now, as read from its screen: an approval,
	// a folder to trust. Nil when it shows none, or runs nothing.
	Prompt(name string) *planv1.LeadPrompt
	// Choose picks the option index (from 0) of the choice the terminal called name shows, as its keys would. It
	// returns ErrNoChoice, typing nothing, when the screen no longer shows the choice of title and options.
	Choose(name, title string, lines, options []string, index int) error
	// Watch calls f each time a terminal starts or ends, and each time the choice a lead shows comes, changes or
	// goes; f never waits.
	Watch(f func())
	// Say types line into a running terminal. It is retained for terminal integrations that predate Tell.
	Say(name, line string) error
	// Close hangs up a running terminal. It is retained for terminal integrations that predate Stop.
	Close(name string)
}

// Errors of Leads.Tell.
var (
	// ErrNoLead: the lead's terminal runs no program.
	ErrNoLead = errors.New("the lead does not run")
	// ErrNotLead: the lead's terminal runs another program than its lead, a shell the window opened under its name:
	// a text typed there would run as a command.
	ErrNotLead = errors.New("the lead's terminal runs another program than the lead")
	// ErrNoChoice: the lead's terminal no longer shows the choice the window showed: answered there, or changed.
	ErrNoChoice = errors.New("the lead no longer shows this choice")
)

// LeadTerminal is the name of the terminal of a wish's lead.
func LeadTerminal(wishID string) string { return "lead-" + strings.ToLower(wishID) }

// WishProvider is the kind of agent of a wish: its lead runs it, and every task that names no other. A wish made
// before the choice runs Claude.
func WishProvider(wish *planv1.Wish) planv1.Provider {
	return ProviderFor(wish, planv1.Provider_PROVIDER_UNSPECIFIED)
}

// ProviderFor chooses a wish's provider with the project's provider as its fallback. An explicit wish provider wins;
// a recorded lead provider is the compatibility value for wishes made before Wish.Provider existed.
func ProviderFor(wish *planv1.Wish, project planv1.Provider) planv1.Provider {
	return cmp.Or(wish.GetProvider(), wish.GetLead().GetProvider(), project, planv1.Provider_PROVIDER_CLAUDE)
}

// leadProvider is the effective provider of a recorded lead. Older wishes left it unset and therefore mean Claude.
func leadProvider(lead *planv1.Lead) planv1.Provider {
	if p := lead.GetProvider(); p != planv1.Provider_PROVIDER_UNSPECIFIED {
		return p
	}
	return planv1.Provider_PROVIDER_CLAUDE
}

// resumeLine is the command line that resumes the lead's session, as its agent's command line takes it, with the
// options of mode (leadFlags) for the shell of goos. The session identifier was checked against a pattern without
// spaces or quotes: the line goes through the user's shell as it is.

func resumeLine(goos string, lead *planv1.Lead, mode planv1.Allowance) (string, error) {
	if lead == nil {
		return "", errors.New("lead session is missing")
	}
	switch lead.GetProvider() {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_UNSPECIFIED:
		return "claude " + leadFlags(goos, planv1.Provider_PROVIDER_CLAUDE, mode) + "--resume " + lead.GetSessionId(), nil
	case planv1.Provider_PROVIDER_CODEX:
		return "codex resume " + leadFlags(goos, planv1.Provider_PROVIDER_CODEX, mode) + lead.GetSessionId(), nil
	}
	return "", fmt.Errorf("a %s lead cannot be resumed in a terminal: only claude and codex",
		strings.ToLower(strings.TrimPrefix(lead.GetProvider().String(), "PROVIDER_")))
}

// leadCommands are the djinn commands a lead runs without asking the developer: reading and driving its wish, as its
// brief tells. What is the developer's word (grant, allow, answer, mark, make) or deletes is not among them: the
// agent asks before, as for any other command.
var leadCommands = []string{
	"djinn wish brief", "djinn wish sync", "djinn wish render", "djinn wish list",
	"djinn question ask", "djinn question list", "djinn question revise",
	"djinn task spawn", "djinn task list", "djinn task get", "djinn task watch", "djinn task send",
	"djinn task stop", "djinn task pause", "djinn task resume", "djinn task start",
	"djinn block put", "djinn block list", "djinn mark list",
	"djinn instruction list", "djinn instruction reflect", "djinn instruction assign", "djinn instruction complete",
}

// leadFlags are the options of a lead's agent for the mode the wish allows in the project it starts in, quoted for
// the shell of goos, each followed by a space (docs/providers.md, "The lead"). None grants more than mode: Claude
// gets leadCommands, and its edits accepted with edit, its auto mode with auto; Codex its workspace sandbox and
// approvals on request with edit, its automatic review of approvals with auto. Without a mode, the agent's own
// configuration decides, as for a worker.
func leadFlags(goos string, provider planv1.Provider, mode planv1.Allowance) string {
	if mode != planv1.Allowance_ALLOWANCE_EDIT && mode != planv1.Allowance_ALLOWANCE_AUTO {
		return "" // Keep the provider’s own permissions unless the developer chose an allowance.
	}
	var flags []string
	switch provider {
	case planv1.Provider_PROVIDER_CLAUDE:
		flags = append(flags, "--allowedTools")
		for _, c := range leadCommands {
			for _, tool := range []string{"Bash", "PowerShell"} {
				q, _ := quoteArg(goos, tool+"("+c+" *)") // No quote nor % in them.
				flags = append(flags, q)
			}
		}
		switch mode {
		case planv1.Allowance_ALLOWANCE_EDIT:
			flags = append(flags, "--permission-mode", "acceptEdits")
		case planv1.Allowance_ALLOWANCE_AUTO:
			flags = append(flags, "--permission-mode", "auto")
		}
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		flags = append(flags, "--mode", "accept-edits") // agy has no auto mode on its CLI.
	case planv1.Provider_PROVIDER_CODEX:
		switch mode {
		case planv1.Allowance_ALLOWANCE_EDIT:
			flags = append(flags, "--sandbox", "workspace-write", "--ask-for-approval", "on-request")
		case planv1.Allowance_ALLOWANCE_AUTO:
			// This option selects workspace-write and automatic approval review itself; Codex rejects it with
			// --sandbox or --ask-for-approval. --help exits before checking these conflicts.
			flags = append(flags, "--approve-for-me")
		}
	}
	if len(flags) == 0 {
		return ""
	}
	return strings.Join(flags, " ") + " "
}

// leadMode is the mode the wish allows in the project its lead runs in: the one whose folder holds dir, the
// deepest. ALLOWANCE_NONE outside its projects.
func leadMode(ctx context.Context, r store.Reader, wish *planv1.Wish, dir string) (planv1.Allowance, error) {
	best, mode := "", planv1.Allowance_ALLOWANCE_NONE
	for _, id := range wish.GetProjectIds() {
		p, err := store.Get[*planv1.Project](ctx, r, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return mode, err
		}
		folder := p.GetDirectory()
		if folder == "" || dir == "" || len(folder) <= len(best) {
			continue
		}
		if rel, err := filepath.Rel(folder, dir); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			best, mode = folder, AllowanceOf(wish, p.GetId())
		}
	}
	return mode, nil
}

// LeadLine tells whether line is one a lead runs, as Resume starts it (resumeLine, briefLine) and a restart takes
// it back: its agent's program first.
func LeadLine(line string) bool {
	program, _, _ := strings.Cut(line, " ")
	return program == "claude" || program == "codex" || program == "agy"
}

// commandHasSession tells whether command is a Claude invocation for session. A rich brief can quote an older
// session identifier in its first message, so searching the whole shell command for the identifier would mistake an
// Antigravity or Codex terminal for the recorded Claude lead.
func commandHasSession(command []string, provider planv1.Provider, session string) bool {
	if provider != planv1.Provider_PROVIDER_CLAUDE || session == "" {
		return false
	}
	for i, arg := range command {
		text := strings.TrimSpace(arg)
		if text == "claude" {
			text = strings.Join(command[i:], " ")
		} else if !strings.HasPrefix(text, "claude ") {
			continue
		}
		for _, flag := range []string{"--session-id ", "--resume "} {
			if value, ok := strings.CutPrefix(text, "claude "); ok {
				text = value
			}
			if rest, ok := strings.CutPrefix(text, flag); ok && strings.HasPrefix(rest, session) {
				if len(rest) == len(session) || rest[len(session)] == ' ' {
					return true
				}
			}
		}
	}
	return false
}

func (w *Wishes) SetLead(
	ctx context.Context, req *connect.Request[planv1.WishServiceSetLeadRequest],
) (*connect.Response[planv1.WishServiceSetLeadResponse], error) {
	lead := &planv1.Lead{Provider: req.Msg.GetProvider(), SessionId: req.Msg.GetSessionId()}
	if lead.Provider != planv1.Provider_PROVIDER_UNSPECIFIED {
		if _, err := resumeLine(runtime.GOOS, lead, planv1.Allowance_ALLOWANCE_NONE); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if dir := req.Msg.GetDirectory(); dir != "" {
		var err error
		if lead.Directory, err = canonical(dir); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("directory: %w", err))
		}
		if HoldsHome(lead.Directory) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"directory %s holds your home folder: Djinn never runs a lead there", lead.Directory))
		}
	}
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		if lead.Provider == planv1.Provider_PROVIDER_UNSPECIFIED {
			// Without a kind given, the session is of the wish's agent.
			lead.Provider = WishProvider(wish)
			if _, err := resumeLine(runtime.GOOS, lead, planv1.Allowance_ALLOWANCE_NONE); err != nil {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%w: give --provider", err))
			}
		}
		if lead.Directory == "" {
			if lead.Directory, err = firstFolder(ctx, tx, wish); err != nil {
				return err
			}
		}
		if lead.Directory == "" {
			return connect.NewError(connect.CodeInvalidArgument, errors.New(
				"none of the wish's projects has a folder here: give the folder the session runs in with --directory"))
		}
		wish.Lead = lead
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceSetLeadResponse{Wish: wish}), nil
}

// Tell writes to the wish's lead in its terminal, as the developer would type it there.
func (w *Wishes) Tell(
	ctx context.Context, req *connect.Request[planv1.WishServiceTellRequest],
) (*connect.Response[planv1.WishServiceTellResponse], error) {
	if w.Leads == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no terminal"))
	}
	text := strings.TrimSpace(req.Msg.GetText())
	if text == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("text: nothing to say"))
	}
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	wait, err := w.Leads.Tell(LeadTerminal(wish.GetId()), text)
	if errors.Is(err, ErrNoLead) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%w: djinn wish resume %s starts it", err, wish.GetId()))
	}
	if errors.Is(err, ErrNotLead) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%w: nothing written. Close that terminal, then djinn wish resume %s", err, wish.GetId()))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&planv1.WishServiceTellResponse{
		Waiting: wait != planv1.TellWait_TELL_WAIT_UNSPECIFIED, Wait: wait,
	}), nil
}

// Choose answers the choice the lead shows in its terminal, as its keys would: the window's answer to an approval.
func (w *Wishes) Choose(
	ctx context.Context, req *connect.Request[planv1.WishServiceChooseRequest],
) (*connect.Response[planv1.WishServiceChooseResponse], error) {
	if w.Leads == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no terminal"))
	}
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	err = w.Leads.Choose(LeadTerminal(wish.GetId()), req.Msg.GetTitle(), req.Msg.GetLines(), req.Msg.GetOptions(), int(req.Msg.GetOption())-1)
	switch {
	case errors.Is(err, ErrNoLead), errors.Is(err, ErrNotLead), errors.Is(err, ErrNoChoice):
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&planv1.WishServiceChooseResponse{}), nil
}

func (w *Wishes) Resume(
	ctx context.Context, req *connect.Request[planv1.WishServiceResumeRequest],
) (*connect.Response[planv1.WishServiceResumeResponse], error) {
	if w.Leads == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no terminal"))
	}
	res, err := w.resume(ctx, req.Msg.GetWishId(), req.Msg.GetProvider())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// resume shows the wish and resumes its lead, or starts one of provider from the brief when it has no session.
func (w *Wishes) resume(
	ctx context.Context, wishID string, provider planv1.Provider,
) (result *planv1.WishServiceResumeResponse, resultErr error) {
	defer func() {
		key := strings.ToLower(wishID)
		if resultErr != nil {
			text := resultErr.Error()
			var ce *connect.Error
			if errors.As(resultErr, &ce) {
				text = ce.Message()
			}
			w.leadErrors.Store(key, text)
		} else {
			w.leadErrors.Delete(key)
		}
		w.watch.notify([]watched{{wish: wishID, kind: planv1.Change_CHANGE_WISH}})
	}()
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, wishID)
	if err != nil {
		return nil, Status(err)
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	res := &planv1.WishServiceResumeResponse{Wish: wish, Terminal: LeadTerminal(wish.GetId())}
	var line, dir, exclusive string
	var started *planv1.Lead // the lead a brief starts, recorded once its terminal runs
	lead := wish.GetLead()
	atHome := lead.GetSessionId() != "" && HoldsHome(lead.GetDirectory())
	other := provider != planv1.Provider_PROVIDER_UNSPECIFIED && provider != leadProvider(lead)
	if lead.GetSessionId() != "" && !atHome && !other {
		dir, exclusive = lead.GetDirectory(), lead.GetSessionId()
		mode, modeErr := leadMode(ctx, w.Store, wish, dir)
		if modeErr != nil {
			return nil, Status(modeErr)
		}
		if line, err = resumeLine(runtime.GOOS, lead, mode); err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if !isFolder(dir) || HoldsHome(dir) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the lead's folder %q is not on this machine: djinn wish set-lead %s %s --directory <folder> gives it",
				dir, wish.GetId(), lead.GetSessionId()))
		}
	} else {
		if dir, err = startFolder(ctx, w.Store, wish); err != nil {
			return nil, Status(err)
		}
		startProvider := provider
		if atHome && startProvider == planv1.Provider_PROVIDER_UNSPECIFIED {
			startProvider = leadProvider(lead)
		}
		var note string
		if line, dir, started, note, err = w.newLead(ctx, wish, startProvider, dir); err != nil {
			return nil, err
		}
		if atHome {
			note = fmt.Sprintf("The lead's session %s was recorded in %s, the home folder, where Djinn never runs a lead. ",
				lead.GetSessionId(), lead.GetDirectory()) + note
		}
		res.Note, exclusive = note, started.GetSessionId()
	}
	res.Command, res.Directory, res.Attached, err = w.Leads.Open(res.GetTerminal(), line, dir, exclusive)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	running := strings.Join(res.GetCommand(), " ")
	switch {
	case res.GetAttached() && started != nil:
		res.Note = fmt.Sprintf("The lead's terminal already runs %s: exit it there, then resume again to start a lead "+
			"from the wish's brief.", running)
	case res.GetAttached() && line != "" && !strings.Contains(running, line) &&
		!commandHasSession(res.GetCommand(), leadProvider(lead), exclusive):
		res.Note = fmt.Sprintf("The lead's terminal already runs %s, not %s: exit it there, then resume again.",
			running, line)
	case !res.GetAttached() && started.GetSessionId() != "":
		// The new lead runs: its session is the wish's lead from now on, for the next resume.
		if res.Wish, err = w.recordLead(ctx, wish.GetId(), started); err != nil {
			return nil, err
		}
	}
	w.Leads.Show(wish.GetId(), res.GetTerminal())
	return res, nil
}

// SetProvider changes the wish's agent, then stops its lead and starts one of the new agent from the brief.
func (w *Wishes) SetProvider(
	ctx context.Context, req *connect.Request[planv1.WishServiceSetProviderRequest],
) (*connect.Response[planv1.WishServiceSetProviderResponse], error) {
	provider := req.Msg.GetProvider()
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	from := WishProvider(wish)
	res := &planv1.WishServiceSetProviderResponse{Wish: wish}
	if from == provider {
		// Nothing to journal: the brief reads a change of agent from the journal.
		if err := fill(ctx, w.Store, wish); err != nil {
			return nil, Status(err)
		}
		res.Note = "The wish already runs " + providerName(provider) + ": nothing changed."
		return connect.NewResponse(res), nil
	}
	var old *planv1.Lead // the lead set aside, its session of the old agent
	err = write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		from = WishProvider(wish)
		wish.Provider = provider
		// A session of another agent cannot lead any more: the next resume starts the new one from the brief. One of
		// the new agent, recorded by djinn wish set-lead, is kept: it is resumed.
		if lead := wish.GetLead(); lead != nil && cmp.Or(lead.GetProvider(), planv1.Provider_PROVIDER_CLAUDE) != provider {
			old, wish.Lead = lead, nil
		}
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	res.Wish = wish
	notes := []string{fmt.Sprintf("The wish runs %s now, instead of %s: its lead, and every task to come that "+
		"names no other agent. The tasks that run go on with theirs. The conversation of the %s lead does not pass to "+
		"the new one, which starts from the wish's brief.", providerName(provider), providerName(from), providerName(from))}
	if old.GetSessionId() != "" {
		if line, err := resumeLine(runtime.GOOS, old, planv1.Allowance_ALLOWANCE_NONE); err == nil {
			notes = append(notes, "The session of the old lead stays with "+providerName(old.GetProvider())+": "+line+
				" in its folder reads it again.")
		}
	}
	if w.Leads == nil {
		if err := fill(ctx, w.Store, wish); err != nil {
			return nil, Status(err)
		}
		res.Note = strings.Join(append(notes, "djinn wish resume "+wish.GetId()+" starts the new lead."), " ")
		return connect.NewResponse(res), nil
	}
	terminal := LeadTerminal(wish.GetId())
	if err := w.Leads.Stop(terminal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf(
			"the wish runs %s now, but its lead did not stop: %w. Exit it in its terminal, then djinn wish resume %s",
			providerName(provider), err, wish.GetId()))
	}
	resumed, err := w.resume(ctx, wish.GetId(), provider)
	if err != nil {
		return nil, err
	}
	res.Wish, res.Terminal, res.Command = resumed.GetWish(), resumed.GetTerminal(), resumed.GetCommand()
	if n := resumed.GetNote(); n != "" {
		notes = append(notes, n)
	}
	res.Note = strings.Join(notes, " ")
	return connect.NewResponse(res), nil
}

// sessionLine is the command and folder used to resume a recorded lead after checking that the folder is local and
// outside the home folder. It is kept separate so routed starts can choose a new provider without touching a saved
// session.
func sessionLine(wish *planv1.Wish) (line, dir string, err error) {
	lead := wish.GetLead()
	if line, err = resumeLine(runtime.GOOS, lead, planv1.Allowance_ALLOWANCE_NONE); err != nil {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	dir = lead.GetDirectory()
	if !isFolder(dir) {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %q is not on this machine: djinn wish set-lead %s %s --directory <folder> gives it",
			dir, wish.GetId(), lead.GetSessionId()))
	}
	if HoldsHome(dir) {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %s holds your home folder, where Djinn never runs a lead: djinn wish resume %s starts a new one in a project",
			dir, wish.GetId()))
	}
	return line, dir, nil
}

// ErrNoProject says that no project has a local folder from which a lead may start.
var ErrNoProject = errors.New("no project has a folder on this machine, and Djinn never starts a lead in your home folder: create a project first (djinn project add <folder>, or + beside Projects in the window)")

// startFolder prefers a wish project and then the first local project, while excluding the home folder and its parents.
func startFolder(ctx context.Context, r store.Reader, wish *planv1.Wish) (string, error) {
	dir, err := firstFolder(ctx, r, wish)
	if dir != "" || err != nil {
		return dir, err
	}
	return FirstProjectFolder(ctx, r)
}

// FirstProjectFolder returns the first local project outside the home folder.
func FirstProjectFolder(ctx context.Context, r store.Reader) (string, error) {
	projects, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if dir := p.GetDirectory(); isFolder(dir) && !HoldsHome(dir) {
			return dir, nil
		}
	}
	return "", ErrNoProject
}

func isFolder(dir string) bool {
	info, err := os.Stat(dir)
	return dir != "" && filepath.IsAbs(dir) && err == nil && info.IsDir()
}

// HoldsHome reports whether dir is the home folder, a parent of it, or a filesystem root.
func HoldsHome(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(resolve(dir), resolve(home))
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// firstFolder is the folder of the first project of the wish that has one on this machine; empty when none has.
func firstFolder(ctx context.Context, r store.Reader, wish *planv1.Wish) (string, error) {
	for _, id := range wish.GetProjectIds() {
		p, err := store.Get[*planv1.Project](ctx, r, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if dir := p.GetDirectory(); isFolder(dir) && !HoldsHome(dir) {
			return dir, nil
		}
	}
	return "", nil
}

// portableFolder is the lead's folder as an export carries it: the scrubber has replaced a project's folder by its
// name and the home folder by ~; a path still absolute names this machine only, and is left out.
func portableFolder(dir string) string {
	if filepath.IsAbs(dir) || strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, `\`) {
		return ""
	}
	return strings.ReplaceAll(dir, `\`, "/")
}

// localFolder puts back this machine's folder in a lead's folder from an export: ~ is the home folder, and a path
// that starts with the name of a project of the export starts from that project's folder here. A folder this
// machine does not have yet is left empty, for djinn wish set-lead to give.
func localFolder(dir string, folders map[string]string) string {
	if dir == "" || filepath.IsAbs(dir) {
		return dir
	}
	first, rest, _ := strings.Cut(strings.ReplaceAll(dir, `\`, "/"), "/")
	base := folders[strings.ToLower(first)]
	if first == "~" {
		base, _ = os.UserHomeDir()
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(rest))
}
