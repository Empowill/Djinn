package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/store"
)

// LeadFirstFile holds the first message of a new lead, in the wish's folder of Djinn's data folder, next to its page:
// the lead reads it there when the shell cannot carry it.
const LeadFirstFile = "lead-first.md"

// maxFirstArg is the most of a first message that goes on the command line; a longer one is read from its file.
// Linux takes 128 KiB in one argument.
const maxFirstArg = 64 << 10

// StartLine is how every new lead starts, whatever its agent: from the brief, which Djinn computes from the plan, each
// djinn command run alone so that none waits for the developer's approval, every question, doubt or choice in a
// question since the developer does not read its terminal.
func StartLine() string {
	return "You lead the active wishes of this Djinn. Run `djinn brief`: where the wishes stand and how to " +
		"lead them, computed by Djinn from its plan. Then continue the wishes from what it says. Everything for the " +
		"developer, reports included, is a question through `djinn question ask`. " + UnreadTerminalRule + " The " +
		"work goes to tasks. " + DirectCommandsRule + " If your tool cuts the brief, run `djinn brief --wishes`."
}

// newLead prepares a new lead: the command line that starts provider's agent in dir (FirstProjectFolder: a
// project's folder, never the home folder) on StartLine, the same for every agent, and the lead it starts.
// The lead holds a session only when Djinn chooses it ahead: claude's. An antigravity lead resumes without a
// session (the folder's most recent conversation). A server without data folder starts none: the lead's terminal
// never holds an empty shell.
func (w *Wishes) newLead(
	ctx context.Context, provider planv1.Provider, dir string,
) (line, folder string, lead *planv1.Lead, note string, err error) {
	if dir == "" || HoldsHome(dir) {
		return "", "", nil, "", Status(ErrNoProject)
	}
	home := w.home()
	if home == "" {
		return "", "", nil, "", connect.NewError(connect.CodeFailedPrecondition, errors.New(
			"there is no lead session to resume, and this server has no data folder to start a new lead: "+
				"djinn lead set records the session of a lead"))
	}
	if provider == planv1.Provider_PROVIDER_UNSPECIFIED {
		provider = planv1.Provider_PROVIDER_CLAUDE
	}
	msg := StartLine()
	own := filepath.Join(home, "lead")
	if err := os.MkdirAll(own, 0o700); err != nil {
		return "", "", nil, "", connect.NewError(connect.CodeInternal, err)
	}
	if err := fsx.WriteAtomic(filepath.Join(own, LeadFirstFile), []byte(msg), 0o600); err != nil {
		return "", "", nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("write the first message: %w", err))
	}
	lead = &planv1.Lead{Id: "lead", Provider: provider, Directory: dir}
	if provider == planv1.Provider_PROVIDER_CLAUDE {
		lead.SessionId = store.NewID()
	}
	if provider == planv1.Provider_PROVIDER_ANTIGRAVITY {
		if err := writeLeadAgyProject(dir, home); err != nil {
			return "", "", nil, "", connect.NewError(connect.CodeInternal, err)
		}
	}
	if line, err = leadLine(runtime.GOOS, lead, own, msg); err != nil {
		return "", "", nil, "", err
	}
	// The lead recorded before, which stays the lead unless the new one is claude's or antigravity's.
	kept := ""
	if old, err := store.Get[*planv1.Lead](ctx, w.Store, "lead"); err == nil && hasLead(old) && !HoldsHome(old.GetDirectory()) {
		if old.GetSessionId() != "" {
			kept = providerName(old.GetProvider()) + " session " + old.GetSessionId()
		} else {
			kept = providerName(old.GetProvider()) + " conversation"
		}
	}
	switch provider {
	case planv1.Provider_PROVIDER_CLAUDE:
		note = "A new lead started from the brief (djinn brief). Its session is the lead now: " +
			"djinn lead resume takes it back."
	case planv1.Provider_PROVIDER_CODEX:
		note = "A new codex lead started from the brief (djinn brief). Djinn does not know its session: " +
			"djinn lead set <session> --provider codex records it."
		if kept != "" {
			note += " Until then, the lead stays the " + kept + "."
		}
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		note = "A new antigravity lead started from the brief (djinn brief). The folder's most recent " +
			"conversation is the lead now: djinn lead resume takes it back."
	default:
		note = fmt.Sprintf("A new %s lead started from the brief (djinn brief). A %s lead cannot be "+
			"resumed in a terminal: the next resume starts a new one.", providerName(provider), providerName(provider))
		if kept != "" {
			note = fmt.Sprintf("A new %s lead started from the brief (djinn brief). A %s lead cannot "+
				"be resumed in a terminal: the lead stays the %s, which the next resume takes back "+
				"once this one exits.", providerName(provider), providerName(provider), kept)
		}
	}
	return line, dir, lead, note, nil
}

// leadLine is the command line that starts a lead's agent on msg, its first message, through the user's shell on
// goos; folder holds the message's file. Every agent gets the same message. One the shell cannot carry (cmd.exe and
// a line break, or one too long) is replaced by the request to read its file.
func leadLine(goos string, lead *planv1.Lead, folder, msg string) (string, error) {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	quote := func(s string) (string, error) {
		q, ok := quoteArg(goos, s)
		if !ok {
			return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%q cannot go through the shell: move Djinn's data folder to a path without quotes nor %%", s))
		}
		return q, nil
	}
	var arg string
	byFile := false
	if q, ok := quoteArg(goos, msg); ok && len(msg) <= maxFirstArg {
		arg = q
	} else {
		var err error
		if arg, err = quote("Read " + folder + sep + LeadFirstFile + ", written by Djinn, and do what it says."); err != nil {
			return "", err
		}
		byFile = true
	}
	switch lead.GetProvider() {
	case planv1.Provider_PROVIDER_CLAUDE:
		tools, err := quote(claudeLeadTools)
		if err != nil {
			return "", err
		}
		line := "claude --session-id " + lead.GetSessionId() + " --allowedTools " + tools
		if byFile {
			dir, err := quote(folder)
			if err != nil {
				return "", err
			}
			line += " --add-dir " + dir
		}
		return line + " " + arg, nil
	case planv1.Provider_PROVIDER_CODEX:
		return "codex " + arg, nil
	case planv1.Provider_PROVIDER_ANTIGRAVITY:
		return "agy -i " + arg + " --project " + LeadAgyProjectID, nil
	}
	return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
		"a lead runs claude, codex or antigravity, not %s", providerName(lead.GetProvider())))
}

// quoteArg quotes s as one argument for the shell that runs a terminal's line on goos: single quotes for the shells
// of Unix (sh, bash, zsh and fish read them alike), double quotes for cmd.exe, which cannot carry a quote, a %, a !
// nor a line break. It says false when s cannot go through.
func quoteArg(goos, s string) (string, bool) {
	if goos == "windows" {
		if s == "" || strings.ContainsAny(s, "\"%!^\r\n") {
			return "", false
		}
		return `"` + s + `"`, true
	}
	if strings.ContainsRune(s, 0) {
		return "", false
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", true
}

// recordLead makes lead the lead, journaled as djinn lead set would be.
func (w *Wishes) recordLead(ctx context.Context, lead *planv1.Lead) error {
	lead.Id = "lead"
	req := &planv1.LeadServiceSetRequest{
		SessionId: lead.GetSessionId(), Provider: lead.GetProvider(), Directory: lead.GetDirectory(),
	}
	return Status(w.Store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.LeadServiceSetProcedure, req); err != nil {
			return err
		}
		return tx.Put(lead)
	}))
}
