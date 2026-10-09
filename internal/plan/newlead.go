package plan

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// The files of a new lead's brief, in the wish's folder of Djinn's data folder, next to its page: the stable part,
// that claude appends to its system prompt, and the part that moves.
const (
	LeadRulesFile = "lead-rules.md"
	LeadBriefFile = "lead-brief.md"
)

// maxBriefArg is the most of a brief that goes on the command line as the first message; a longer one is read from
// its file. Linux takes 128 KiB in one argument.
const maxBriefArg = 64 << 10

// newLead prepares a new lead for a wish that has none to resume: the command line that starts provider's agent in
// dir (the wish's first project; without one, the folder of every wish's own folder) on the wish's brief, and the lead
// it starts.
// The lead holds a session only when Djinn chooses it ahead: claude's. A server without data folder keeps the shell.
func (w *Wishes) newLead(
	ctx context.Context, wish *planv1.Wish, provider planv1.Provider, dir string,
) (line, folder string, lead *planv1.Lead, note string, err error) {
	home := w.home()
	if home == "" {
		return "", dir, nil, "This wish has no lead session to resume: the lead's terminal runs a shell. " +
			"djinn wish set-lead records the session of the lead.", nil
	}
	provider = cmp.Or(provider, WishProvider(wish))
	brief, err := BuildBrief(ctx, w.Store, home, wish.GetId())
	if err != nil {
		return "", "", nil, "", Status(err)
	}
	own := filepath.Join(home, PagesDir, strings.ToLower(wish.GetId()))
	if err := os.MkdirAll(own, 0o700); err != nil {
		return "", "", nil, "", connect.NewError(connect.CodeInternal, err)
	}
	rules, moving := filepath.Join(own, LeadRulesFile), filepath.Join(own, LeadBriefFile)
	for file, text := range map[string]string{rules: brief.Stable, moving: brief.Moving} {
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			return "", "", nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("write the brief: %w", err))
		}
	}
	if dir == "" {
		// One folder for every lead without a project, not the wish's own: Claude Code asks to trust the folder it
		// starts in, and its trust covers the subfolders, so the question comes once, not at each wish
		// (docs/providers.md).
		dir = filepath.Dir(own)
	}
	lead = &planv1.Lead{Provider: provider, Directory: dir}
	if provider == planv1.Provider_PROVIDER_CLAUDE {
		lead.SessionId = store.NewID()
	}
	if line, err = briefLine(runtime.GOOS, lead, own, brief); err != nil {
		return "", "", nil, "", err
	}
	switch provider {
	case planv1.Provider_PROVIDER_CLAUDE:
		note = "A new lead started from the wish's brief (djinn wish brief). Its session is the wish's lead now: " +
			"djinn wish resume takes it back."
	case planv1.Provider_PROVIDER_CODEX:
		note = "A new codex lead started from the wish's brief (djinn wish brief). Djinn does not know its session: " +
			"djinn wish set-lead " + wish.GetId() + " <session> --provider codex records it."
	default:
		note = "A new antigravity lead started from the wish's brief (djinn wish brief). An antigravity lead cannot be " +
			"resumed in a terminal: the next resume starts a new one."
	}
	return line, dir, lead, note, nil
}

// briefLine is the command line that starts a lead's agent on brief, through the user's shell on goos; folder holds
// the brief's files. Claude appends the stable part to its system prompt, from its file, and gets the part that moves as its first message:
// the prefix it shares with every lead is read from the cache. Codex and Antigravity take no system prompt at the
// command line: the whole brief is their first message, stable part first. A message the shell cannot carry
// (cmd.exe and a line break, or one too long) is replaced by the request to read its file.
func briefLine(goos string, lead *planv1.Lead, folder string, brief Brief) (string, error) {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	rules, moving := folder+sep+LeadRulesFile, folder+sep+LeadBriefFile
	quote := func(s string) (string, error) {
		q, ok := quoteArg(goos, s)
		if !ok {
			return "", connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%q cannot go through the shell: move Djinn's data folder to a path without quotes nor %%", s))
		}
		return q, nil
	}
	first := func(text, read string) (msg string, byFile bool, err error) {
		if q, ok := quoteArg(goos, text); ok && len(text) <= maxBriefArg {
			return q, false, nil
		}
		msg, err = quote(read)
		return msg, true, err
	}
	switch lead.GetProvider() {
	case planv1.Provider_PROVIDER_CLAUDE:
		rulesArg, err := quote(rules)
		if err != nil {
			return "", err
		}
		msg, byFile, err := first(brief.Moving, "Read "+moving+": where the wish stands now, written by Djinn. Then lead it.")
		if err != nil {
			return "", err
		}
		line := "claude --session-id " + lead.GetSessionId() + " --append-system-prompt-file " + rulesArg
		if byFile {
			dir, err := quote(folder)
			if err != nil {
				return "", err
			}
			line += " --add-dir " + dir
		}
		return line + " " + msg, nil
	case planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_ANTIGRAVITY:
		msg, _, err := first(brief.Text(), "Read "+rules+", then "+moving+": how to lead this wish, and where it stands "+
			"now, written by Djinn. Then lead it.")
		if err != nil {
			return "", err
		}
		if lead.GetProvider() == planv1.Provider_PROVIDER_CODEX {
			return "codex " + msg, nil
		}
		return "agy -i " + msg, nil
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

// recordLead makes lead the lead of the wish, journaled as djinn wish set-lead would be.
func (w *Wishes) recordLead(ctx context.Context, wishID string, lead *planv1.Lead) (*planv1.Wish, error) {
	req := &planv1.WishServiceSetLeadRequest{
		WishId: wishID, SessionId: lead.GetSessionId(), Provider: lead.GetProvider(), Directory: lead.GetDirectory(),
	}
	var wish *planv1.Wish
	err := Status(w.Store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.WishServiceSetLeadProcedure, req); err != nil {
			return err
		}
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, wishID); err != nil {
			return err
		}
		wish.Lead = lead
		return tx.Put(wish)
	}))
	if err != nil {
		return nil, err
	}
	return wish, Status(fill(ctx, w.Store, wish))
}
