package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// claudeNewlineHash is Claude Code's "newline-hash" check (2.1.300 to 2.1.302 at least): an argument holding a
// newline followed by #, after blanks or not, "can hide arguments from path validation". Claude refuses the call
// whatever its allow rules say; in don't-ask mode it reads "Permission to use Bash has been denied because Claude Code
// is running in don't ask mode".
var claudeNewlineHash = regexp.MustCompile(`\n\s*#`)

// claudeRefuses tells whether Claude Code refuses command by its newline-hash check.
func claudeRefuses(t *testing.T, command string) bool {
	t.Helper()
	words, err := splitWords(command)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(words, claudeNewlineHash.MatchString)
}

// w80Context is the context W80 of wish Dew 13 oct, the investigator of Q32, tried to revise its question with, on
// 16/10 at 20:01, in English: a # comment above each command of its code block, as QuestionShape asks.
const w80Context = "~~~\n # What W67 removes: breather.go, flags.go, .env, CI\ngit show 7b998f57f --stat\n" +
	" # Envoy routing: the oauth2 token route goes to hydra-public, not to breather\n" +
	"git show origin/ET-3510-old-role-types:envoy.yaml\n~~~\n\n" +
	"**Why**: HYDRA_PUBLIC_URL only fed a breather proxy, dead since July 2025.\n\n" +
	"- **Role of the variable** (verified): breather relayed /oauth2/token to Hydra and kept the answer 2 min in Redis.\n" +
	"- **Why it is dead** (verified): Hydra 2.3 does it alone, and envoy sends /oauth2/token straight to hydra-public.\n" +
	"- **Risk 1** (NOT verified): the production load balancer.\n\n" +
	"Recommendation: leave the draft, checking the production route before merging."

// specProbe hands over the spec of each worker it starts, then plays a fake that says it read.
type specProbe struct{ specs chan Spec }

func (p specProbe) Start(ctx context.Context, spec Spec) (Worker, error) {
	p.specs <- spec
	spec.Prompt = "text read"
	return Fake{}.Start(ctx, spec)
}

// TestInvestigatorRevisesWithACommentLine: W80's revision of Q32, refused three times by Claude Code whatever its
// quoting, goes through (W153). The context of an investigator on Claude goes in a file of its own folder, the one
// place it may write; its djinn question revise passes that file, on one line that Claude's check lets through.
func TestInvestigatorRevisesWithACommentLine(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	probe := specProbe{make(chan Spec, 4)}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = probe
	e := upWith(t, home, providers, WithEnlightenWorkers())
	wishID, _ := e.wish(t, fakeProject(t, "question_provider: PROVIDER_CLAUDE\n"))
	q := e.asker(wishID)
	asked := q.ask(t, "One-line why.")
	q.enlighten(t, asked.GetCode(), "Is the variable still read?")
	var spec Spec
	select {
	case spec = <-probe.specs:
	case <-time.After(20 * time.Second):
		t.Fatal("no investigator started")
	}
	inv := e.roles(t, wishID, planv1.TaskRole_TASK_ROLE_INVESTIGATOR)[0]

	// W80's command: Djinn's rules allowed it, Claude refused it, with double quotes as with single ones.
	for _, quote := range []string{`"`, `'`} {
		w80 := "djinn question revise Q32 --wish-id 01a1351c-6d48-795d-8054-0066eb45d8e5 --context " + quote +
			w80Context + quote
		if !commandAllowed(spec.Permissions, w80) || !claudeRefuses(t, w80) {
			t.Errorf("W80's command quoted with %s: allowed by Djinn %v, refused by Claude %v: want both", quote,
				commandAllowed(spec.Permissions, w80), claudeRefuses(t, w80))
		}
	}

	// Its own folder is the one it may write in, and its prompt says to write the context there.
	file := questionContextFile(home, inv.GetId())
	if !slices.Equal(spec.WriteDirs, []string{filepath.Dir(file)}) {
		t.Errorf("write folders %q, want %q", spec.WriteDirs, filepath.Dir(file))
	}
	if info, err := os.Stat(filepath.Dir(file)); err != nil || !info.IsDir() {
		t.Errorf("its folder: %v, want made", err)
	}
	if want := "--context-file \"" + file + "\""; !strings.Contains(spec.Prompt, want) {
		t.Errorf("prompt does not say %s:\n%s", want, spec.Prompt)
	}
	if strings.Contains(spec.Prompt, "leading space") {
		t.Errorf("prompt still says an indented # passes:\n%s", spec.Prompt)
	}

	// Claude's settings let it write there and nowhere else, and run djinn question revise: nothing broader.
	args := Claude{}.args(spec)
	var settings struct {
		Permissions struct {
			Allow, Deny []string
			DefaultMode string
		}
	}
	if i := slices.Index(args, "--settings"); i < 0 || json.Unmarshal([]byte(args[i+1]), &settings) != nil {
		t.Fatalf("no settings in %q", args)
	}
	p := settings.Permissions
	dir := claudeAbsolute(filepath.Dir(file))
	var writes []string
	for _, rule := range p.Allow {
		if strings.HasPrefix(rule, "Edit") || strings.HasPrefix(rule, "Write") || strings.HasPrefix(rule, "NotebookEdit") {
			writes = append(writes, rule)
		}
	}
	if want := []string{"Edit(" + dir + "/**)", "Write(" + dir + "/**)"}; !slices.Equal(writes, want) ||
		!slices.Contains(p.Allow, "Bash(djinn question revise *)") || p.DefaultMode != "dontAsk" {
		t.Errorf("settings %+v: want writes %q only, djinn question revise, dontAsk", p, want)
	}

	// The command its prompt says, once it wrote W80's context with its Write tool: allowed, on one line, and
	// Claude lets it through. It revises the question with the context as written.
	if err := os.WriteFile(file, []byte(w80Context+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	revise := "djinn question revise " + asked.GetCode() + " --wish-id " + wishID + " --context-file \"" + file +
		"\" --recommendation \"Check the production route, then leave the draft.\""
	if !commandAllowed(spec.Permissions, revise) || claudeRefuses(t, revise) || strings.Contains(revise, "\n") {
		t.Fatalf("%s: allowed %v, refused by Claude %v: want one line, allowed, let through", revise,
			commandAllowed(spec.Permissions, revise), claudeRefuses(t, revise))
	}
	words, err := splitWords(revise)
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Run as the developer would: this test's own environment may name a worker.
	if code := cli.Run(ctx, words[1:], cli.Config{Version: "test", Addr: e.srv.URL, HTTP: e.srv.Client(), Stdout: &out,
		Stderr: &errs, Getenv: func(string) string { return "" }}); code != 0 {
		t.Fatalf("%s: code %d\n%s\n%s", revise, code, out.String(), errs.String())
	}
	got, err := store.Get[*planv1.Question](t.Context(), e.db, asked.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetContext() != w80Context || got.GetRevision() != 1 {
		t.Errorf("revised to revision %d, context:\n%s\nwant revision 1, context:\n%s", got.GetRevision(),
			got.GetContext(), w80Context)
	}
}

// TestCodexInvestigatorWritesNowhere: an investigator on Codex gets no folder to write in, so no context file: Codex
// would open its workspace-write sandbox on its workspace, the project, where Claude's rules open the folder alone.
func TestCodexInvestigatorWritesNowhere(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	home := t.TempDir()
	probe := specProbe{make(chan Spec, 4)}
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CODEX] = probe
	e := upWith(t, home, providers, WithEnlightenWorkers())
	wishID, _ := e.wish(t, fakeProject(t, "question_provider: PROVIDER_CODEX\n"))
	q := e.asker(wishID)
	asked := q.ask(t, "One-line why.")
	q.enlighten(t, asked.GetCode(), "Is the variable still read?")
	var spec Spec
	select {
	case spec = <-probe.specs:
	case <-time.After(20 * time.Second):
		t.Fatal("no investigator started")
	}
	if len(spec.WriteDirs) > 0 || strings.Contains(spec.Prompt, "--context-file") {
		t.Errorf("write folders %q, prompt:\n%s\nwant none, no --context-file", spec.WriteDirs, spec.Prompt)
	}
	if sandbox, _, _, _, _ := codexAccess(spec); sandbox == "workspace-write" {
		t.Errorf("sandbox %s, want no writing", sandbox)
	}
}
