package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// Say types line into the running terminal called name, then Enter, once the person is not typing there. It
	// returns at once; the lines go out in order.
	Say(name, line string) error
	// Close hangs up the running terminal called name, as closing it in the window does; none runs, nothing.
	Close(name string)
}

// LeadTerminal is the name of the terminal of a wish's lead.
func LeadTerminal(wishID string) string { return "lead-" + strings.ToLower(wishID) }

// leadProvider is the agent of a recorded lead: claude when the record names none.
func leadProvider(lead *planv1.Lead) planv1.Provider {
	if p := lead.GetProvider(); p != planv1.Provider_PROVIDER_UNSPECIFIED {
		return p
	}
	return planv1.Provider_PROVIDER_CLAUDE
}

// hasLead reports whether lead records a resumable lead session or workspace.
func hasLead(lead *planv1.Lead) bool {
	if lead == nil || lead.GetDirectory() == "" {
		return false
	}
	return lead.GetSessionId() != "" || lead.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY
}

// resumeLine is the command line that resumes the lead's session, as its agent's command line takes it. The session
// identifier was checked against a pattern without spaces or quotes: the line goes through the user's shell as it is.
func resumeLine(lead *planv1.Lead) (string, error) {
	switch lead.GetProvider() {
	case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_UNSPECIFIED:
		if lead.GetSessionId() == "" {
			return "", errors.New("a claude lead needs a session id")
		}
		return "claude --resume " + lead.GetSessionId(), nil
	case planv1.Provider_PROVIDER_CODEX:
		if lead.GetSessionId() == "" {
			return "", errors.New("a codex lead needs a session id")
		}
		return "codex resume " + lead.GetSessionId(), nil
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		if id := lead.GetSessionId(); id != "" {
			return "agy --conversation " + id, nil
		}
		return "agy --continue", nil
	}
	return "", fmt.Errorf("a %s lead cannot be resumed in a terminal: only claude, codex and antigravity",
		strings.ToLower(strings.TrimPrefix(lead.GetProvider().String(), "PROVIDER_")))
}

func (w *Wishes) SetLead(
	ctx context.Context, req *connect.Request[planv1.WishServiceSetLeadRequest],
) (*connect.Response[planv1.WishServiceSetLeadResponse], error) {
	lead := &planv1.Lead{Provider: req.Msg.GetProvider(), SessionId: req.Msg.GetSessionId()}
	if lead.Provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		lead.Provider = planv1.Provider_PROVIDER_CLAUDE
	}
	if _, err := resumeLine(lead); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if dir := req.Msg.GetDirectory(); dir != "" {
		var err error
		if lead.Directory, err = canonical(dir); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("directory: %w", err))
		}
		if HoldsHome(lead.Directory) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"directory: %s holds your home folder, where Djinn never runs a lead: start the session in a project", lead.Directory))
		}
	}
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
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

func (w *Wishes) Resume(
	ctx context.Context, req *connect.Request[planv1.WishServiceResumeRequest],
) (*connect.Response[planv1.WishServiceResumeResponse], error) {
	if w.Leads == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no terminal"))
	}
	wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	res := &planv1.WishServiceResumeResponse{Wish: wish, Terminal: LeadTerminal(wish.GetId())}
	var line, dir, exclusive string
	var started *planv1.Lead // the lead a brief starts, recorded once its terminal runs
	// A session recorded in the home folder is not resumed: Djinn never runs a lead there, and claude finds a
	// session only from the folder it was made in. A new lead starts from the brief, in a project.
	atHome := hasLead(wish.GetLead()) && HoldsHome(wish.GetLead().GetDirectory())
	// A lead of another agent than the recorded one cannot open its session: it starts from the brief, which says
	// how to take the wish over.
	other := req.Msg.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED &&
		req.Msg.GetProvider() != leadProvider(wish.GetLead())
	if hasLead(wish.GetLead()) && !atHome && !other {
		if line, dir, err = sessionLine(wish); err != nil {
			return nil, err
		}
		exclusive = wish.GetLead().GetSessionId()
	} else {
		if dir, err = startFolder(ctx, w.Store, wish); err != nil {
			return nil, Status(err)
		}
		provider := req.Msg.GetProvider()
		if atHome && provider == planv1.Provider_PROVIDER_UNSPECIFIED {
			provider = wish.GetLead().GetProvider()
		}
		var note string
		if line, dir, started, note, err = w.newLead(ctx, wish, provider, dir, ""); err != nil {
			return nil, err
		}
		if atHome {
			leadDescr := "The lead's session " + wish.GetLead().GetSessionId()
			if wish.GetLead().GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && wish.GetLead().GetSessionId() == "" {
				leadDescr = "The lead"
			}
			note = fmt.Sprintf("%s was recorded in %s, the home folder, where Djinn never runs a "+
				"lead. ", leadDescr, wish.GetLead().GetDirectory()) + note
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
	case res.GetAttached() && line != "" && !strings.Contains(running, line) && (exclusive == "" || !strings.Contains(running, exclusive)):
		res.Note = fmt.Sprintf("The lead's terminal already runs %s, not %s: exit it there, then resume again.",
			running, line)
	case !res.GetAttached() && hasLead(started):
		// The new lead runs: its session is the wish's lead from now on, for the next resume.
		if res.Wish, err = w.recordLead(ctx, wish.GetId(), started); err != nil {
			return nil, err
		}
	}
	w.Leads.Show(wish.GetId(), res.GetTerminal())
	return connect.NewResponse(res), nil
}

// sessionLine is the command line that resumes the session of the wish's lead, and the folder it runs in.
func sessionLine(wish *planv1.Wish) (line, dir string, err error) {
	lead := wish.GetLead()
	if line, err = resumeLine(lead); err != nil {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	dir = lead.GetDirectory()
	if !isFolder(dir) {
		if lead.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && lead.GetSessionId() == "" {
			return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the lead's folder %q is not on this machine: djinn wish set-lead %s --provider antigravity --directory <folder> gives it",
				dir, wish.GetId()))
		}
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %q is not on this machine: djinn wish set-lead %s %s --directory <folder> gives it",
			dir, wish.GetId(), lead.GetSessionId()))
	}
	if HoldsHome(dir) {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %s holds your home folder, where Djinn never runs a lead: djinn wish resume %s starts "+
				"a new one in a project", dir, wish.GetId()))
	}
	return line, dir, nil
}

// ErrNoProject says that no project has a folder on this machine: a lead has nowhere to start, and Djinn never starts
// one in the home folder.
var ErrNoProject = errors.New("no project has a folder on this machine, and Djinn never starts a lead in your home " +
	"folder: create a project first (djinn project add <folder>, or + beside Projects in the window)")

// startFolder is the folder a new lead of wish starts in: the wish's first project that has one on this machine,
// else the first of Djinn's projects, in the order the window lists them. ErrNoProject when no project has one.
func startFolder(ctx context.Context, r store.Reader, wish *planv1.Wish) (string, error) {
	dir, err := firstFolder(ctx, r, wish)
	if dir != "" || err != nil {
		return dir, err
	}
	return FirstProjectFolder(ctx, r)
}

// FirstProjectFolder is the folder of the first of Djinn's projects, in the order the window lists them, that has
// one on this machine, outside the home folder's own. ErrNoProject when none has: where the window's terminal opens
// by default, and a new lead without a project of its own.
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

// firstFolder is the folder of the first project of the wish that has one on this machine, outside the home
// folder's own; empty when none has.
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

// isFolder reports whether dir is an absolute path to a folder of this machine.
func isFolder(dir string) bool {
	info, err := os.Stat(dir)
	return dir != "" && filepath.IsAbs(dir) && err == nil && info.IsDir()
}

// HoldsHome reports whether dir is the user's home folder or a folder above it (/, /home, C:\): Djinn never runs a
// lead there. An agent started there asks whether to trust the whole home folder, and reaches all of it.
func HoldsHome(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir {
		return true // A root.
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	rel, err := filepath.Rel(resolve(dir), resolve(home))
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// portableFolder is the lead's folder as an export carries it, with forward slashes whatever the system: the
// scrubber has replaced a project's folder by its name and the home folder by ~; a path still absolute names this
// machine only, and is left out.
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
	var base string
	if first == "~" {
		base, _ = os.UserHomeDir()
	} else {
		base = folders[strings.ToLower(first)]
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(rest))
}
