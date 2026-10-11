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
	// Running tells whether the terminal called name runs.
	Running(name string) bool
	// Close hangs up the running terminal called name, as closing it in the window does; none runs, nothing.
	Close(name string)
}

// LeadTerminal is the name of the terminal of the lead.
const LeadTerminal = "lead"

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

// LeadService implements planv1connect.LeadServiceHandler.
type LeadService struct {
	Wishes *Wishes
}

func (s *LeadService) Set(
	ctx context.Context, req *connect.Request[planv1.LeadServiceSetRequest],
) (*connect.Response[planv1.LeadServiceSetResponse], error) {
	lead := &planv1.Lead{Id: "lead", Provider: req.Msg.GetProvider(), SessionId: req.Msg.GetSessionId()}
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
	err := write(ctx, s.Wishes.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		if lead.Directory == "" {
			if old, err := store.Get[*planv1.Lead](ctx, tx, "lead"); err == nil && old.GetDirectory() != "" && isFolder(old.GetDirectory()) && !HoldsHome(old.GetDirectory()) {
				lead.Directory = old.GetDirectory()
			} else if lead.Directory, err = FirstProjectFolder(ctx, tx); err != nil && !errors.Is(err, ErrNoProject) {
				return err
			}
		}
		if lead.Directory == "" {
			return connect.NewError(connect.CodeInvalidArgument, errors.New(
				"none of Djinn's projects has a folder here: give the folder the session runs in with --directory"))
		}
		return tx.Put(lead)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.LeadServiceSetResponse{Lead: lead}), nil
}

func (s *LeadService) Resume(
	ctx context.Context, req *connect.Request[planv1.LeadServiceResumeRequest],
) (*connect.Response[planv1.LeadServiceResumeResponse], error) {
	if s.Wishes.Leads == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no terminal"))
	}
	lead, err := store.Get[*planv1.Lead](ctx, s.Wishes.Store, "lead")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, Status(err)
	}
	res := &planv1.LeadServiceResumeResponse{Lead: lead, Terminal: LeadTerminal}
	var line, dir, exclusive string
	var started *planv1.Lead // the lead a brief starts, recorded once its terminal runs
	// A session recorded in the home folder is not resumed: Djinn never runs a lead there, and claude finds a
	// session only from the folder it was made in. A new lead starts from the brief, in a project.
	atHome := hasLead(lead) && HoldsHome(lead.GetDirectory())
	// A lead of another agent than the recorded one cannot open its session: it starts from the brief, which says
	// how to take the wish over.
	other := req.Msg.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED &&
		req.Msg.GetProvider() != leadProvider(lead)
	if hasLead(lead) && !atHome && !other {
		if line, dir, err = sessionLine(lead); err != nil {
			return nil, err
		}
		exclusive = lead.GetSessionId()
	} else {
		if dir, err = FirstProjectFolder(ctx, s.Wishes.Store); err != nil {
			return nil, Status(err)
		}
		provider := req.Msg.GetProvider()
		if atHome && provider == planv1.Provider_PROVIDER_UNSPECIFIED {
			provider = lead.GetProvider()
		}
		var note string
		if line, dir, started, note, err = s.Wishes.newLead(ctx, provider, dir); err != nil {
			return nil, err
		}
		if atHome {
			leadDescr := "The lead's session " + lead.GetSessionId()
			if lead.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && lead.GetSessionId() == "" {
				leadDescr = "The lead"
			}
			note = fmt.Sprintf("%s was recorded in %s, the home folder, where Djinn never runs a "+
				"lead. ", leadDescr, lead.GetDirectory()) + note
		}
		res.Note, exclusive = note, started.GetSessionId()
	}
	res.Command, res.Directory, res.Attached, err = s.Wishes.Leads.Open(res.GetTerminal(), line, dir, exclusive)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	running := strings.Join(res.GetCommand(), " ")
	switch {
	case res.GetAttached() && started != nil:
		res.Note = fmt.Sprintf("The lead's terminal already runs %s: exit it there, then resume again to start a lead "+
			"from the brief.", running)
	case res.GetAttached() && line != "" && !strings.Contains(running, line) && (exclusive == "" || !strings.Contains(running, exclusive)):
		res.Note = fmt.Sprintf("The lead's terminal already runs %s, not %s: exit it there, then resume again.",
			running, line)
	case !res.GetAttached() && hasLead(started):
		// The new lead runs: its session is the lead from now on, for the next resume.
		if err := s.Wishes.recordLead(ctx, started); err != nil {
			return nil, err
		}
		res.Lead = started
	}
	if wishID := req.Msg.GetWishId(); wishID != "" {
		if wish, err := store.Get[*planv1.Wish](ctx, s.Wishes.Store, wishID); err == nil {
			line := fmt.Sprintf("Focus on the wish %s (%s): djinn wish brief %s", wish.GetTitle(), wish.GetId(), wish.GetId())
			_ = s.Wishes.Leads.Say(res.GetTerminal(), line)
		}
	}
	s.Wishes.Leads.Show(req.Msg.GetWishId(), res.GetTerminal())
	return connect.NewResponse(res), nil
}

func (s *LeadService) Get(
	ctx context.Context, req *connect.Request[planv1.LeadServiceGetRequest],
) (*connect.Response[planv1.LeadServiceGetResponse], error) {
	lead, err := store.Get[*planv1.Lead](ctx, s.Wishes.Store, "lead")
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewResponse(&planv1.LeadServiceGetResponse{}), nil
	}
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.LeadServiceGetResponse{Lead: lead}), nil
}

func (s *LeadService) Brief(
	ctx context.Context, req *connect.Request[planv1.LeadServiceBriefRequest],
) (*connect.Response[planv1.LeadServiceBriefResponse], error) {
	home := s.Wishes.home()
	brief, err := BuildGlobalBrief(ctx, s.Wishes.Store, home)
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.LeadServiceBriefResponse{Text: brief}), nil
}

// sessionLine is the command line that resumes the session of the lead, and the folder it runs in.
func sessionLine(lead *planv1.Lead) (line, dir string, err error) {
	if line, err = resumeLine(lead); err != nil {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, err)
	}
	dir = lead.GetDirectory()
	if !isFolder(dir) {
		if lead.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && lead.GetSessionId() == "" {
			return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the lead's folder %q is not on this machine: djinn lead set --provider antigravity --directory <folder> gives it",
				dir))
		}
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %q is not on this machine: djinn lead set %s --directory <folder> gives it",
			dir, lead.GetSessionId()))
	}
	if HoldsHome(dir) {
		return "", "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the lead's folder %s holds your home folder, where Djinn never runs a lead: djinn lead resume starts "+
				"a new one in a project", dir))
	}
	return line, dir, nil
}

// ErrNoProject says that no project has a folder on this machine: a lead has nowhere to start, and Djinn never starts
// one in the home folder.
var ErrNoProject = errors.New("no project has a folder on this machine, and Djinn never starts a lead in your home " +
	"folder: create a project first (djinn project add <folder>, or + beside Projects in the window)")

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
