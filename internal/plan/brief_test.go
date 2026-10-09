package plan

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

func TestBrief(t *testing.T) {
	ctx := t.Context()
	c, wish, repo := source(t)
	home := t.TempDir()
	for _, f := range []string{"AGENTS.md", "contributing.md"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("# Rules\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// What a brief must never carry: a local path, credentials in a URL, a raw line, a tool's result.
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: wish.GetId(), Kind: "report", Title: "Where the code is",
		Content: "In " + repo + "/internal and " + filepath.Join(home, "wishes") + ", cloned from https://bob:hunter2@example.com/acme.git.",
	})); err != nil {
		t.Fatal(err)
	}
	failed := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), Code: "W2", Title: "Write the docs", Status: planv1.TaskStatus_TASK_STATUS_FAILED,
		Error: "create the worktree: " + repo + " is locked", CreateTime: timestamppb.Now(),
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", failed); err != nil {
			return err
		}
		return tx.Put(failed)
	}); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildBrief(ctx, c.store, home, wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	text := brief.Text()
	// The stable part first: Djinn's rules, then the projects' own; then the wish.
	order := []string{"# Leading a wish in Djinn", "## Commands", "## The projects' rules", "# The wish: Ship the API",
		"## Open questions", "## Latest decisions", "## Running", "## Waiting", "## Latest blocks"}
	last := -1
	for _, s := range order {
		i := strings.Index(text, s)
		if i <= last {
			t.Fatalf("%q at %d, after %d: the brief is out of order\n%s", s, i, last, text)
		}
		last = i
	}
	if !strings.HasPrefix(text, brief.Stable) || strings.Contains(brief.Stable, "# The wish") {
		t.Errorf("the stable part does not lead, or holds the wish:\n%s", text)
	}
	for _, want := range []string{
		"Read `AGENTS.md`, `contributing.md` at its root first", "**notes**, outside Git", "**api**, a Git repository",
		wish.GetId(), "**Q01** Which store? → A: SQLite", "**W1** Write the store (claude", "**W2** Write the docs: failed",
		"### No CGO (decision)", "In api/internal", "https://example.com/acme.git",
		"`djinn wish sync <wish>`", "republish that file as it is, in one call",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the brief lacks %q:\n%s", want, text)
		}
	}
	userHome, _ := os.UserHomeDir()
	for _, never := range []string{repo, home, userHome, "hunter2", "bob@", "s3cret", "session-42", "secretCode", "the code itself", `"raw"`} {
		if never != "" && strings.Contains(text, never) {
			t.Errorf("the brief holds %q:\n%s", never, text)
		}
	}

	// Another wish on the same projects reads the same stable part, byte for byte: one cache for both.
	other, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Another wish", ProjectIds: wish.GetProjectIds(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildBrief(ctx, c.store, home, other.Msg.GetWish().GetId())
	if err != nil {
		t.Fatal(err)
	}
	if second.Stable != brief.Stable || second.Moving == brief.Moving {
		t.Errorf("stable parts differ, or moving parts do not:\n%s\n---\n%s", brief.Stable, second.Stable)
	}
	// An empty section is hidden.
	if strings.Contains(second.Moving, "## Open questions") || strings.Contains(second.Moving, "## Running") {
		t.Errorf("empty sections shown:\n%s", second.Moving)
	}

	// djinn wish brief prints it.
	res, err := c.wishes.Brief(ctx, connect.NewRequest(&planv1.WishServiceBriefRequest{WishId: wish.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Msg.GetText(), brief.Stable) || !strings.Contains(res.Msg.GetText(), "# The wish: Ship the API") {
		t.Errorf("djinn wish brief = %q", res.Msg.GetText())
	}
}

// serveLeads serves the plan with fake terminals and pages in home, as djinn up does.
func serveLeads(t *testing.T, home string) (clients, *fakeLeads) {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	leads := &fakeLeads{}
	mux := http.NewServeMux()
	for prefix, h := range Handlers(s, WithLeads(leads), WithPages(NewPages(s, home, "v0-test"))) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return clients{
		projects:  planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:    planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		questions: planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL),
		blocks:    planv1connect.NewBlockServiceClient(srv.Client(), srv.URL),
		store:     s,
	}, leads
}

func TestResumeFromBrief(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix; TestBriefLine checks cmd.exe")
	}
	ctx := t.Context()
	home := t.TempDir()
	c, leads := serveLeads(t, home)
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	folder := project.Msg.GetProject().GetDirectory()
	last := ""
	make := func(title string) string {
		t.Helper()
		if last != "" { // Three wishes are active at most: each case makes one.
			if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: last})); err != nil {
				t.Fatal(err)
			}
		}
		w, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: title, ProjectIds: []string{project.Msg.GetProject().GetId()},
		}))
		if err != nil {
			t.Fatal(err)
		}
		last = w.Msg.GetWish().GetId()
		return last
	}
	resume := func(id string, p planv1.Provider) *planv1.WishServiceResumeResponse {
		t.Helper()
		res, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id, Provider: p}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	// Claude: the stable part from its file, the moving part as the first message, a session chosen and recorded.
	id := make("Ship the API")
	res := resume(id, planv1.Provider_PROVIDER_UNSPECIFIED)
	lead := res.GetWish().GetLead()
	rules := filepath.Join(home, PagesDir, id, LeadRulesFile)
	moving, err := os.ReadFile(filepath.Join(home, PagesDir, id, LeadBriefFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "claude --session-id " + lead.GetSessionId() + " --append-system-prompt-file '" + rules + "' '" + string(moving) + "'"
	if len(leads.opened) != 1 || leads.opened[0] != want+" in "+folder {
		t.Fatalf("opened %q\nwant %q", leads.opened, want+" in "+folder)
	}
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetDirectory() != folder || len(lead.GetSessionId()) != 36 ||
		!strings.Contains(res.GetNote(), "new lead") || res.GetAttached() {
		t.Errorf("resume = %v", res)
	}
	stable, err := os.ReadFile(rules)
	if err != nil || !strings.HasPrefix(string(stable), "# Leading a wish in Djinn") || !strings.HasPrefix(string(moving), "# The wish: Ship the API") {
		t.Errorf("brief files: %q / %q (%v)", stable, moving, err)
	}
	// The shell gives the agent the brief as written: the quotes hold.
	if sh, err := exec.LookPath("sh"); err == nil {
		q, _ := quoteArg("linux", string(moving)+"it's $HOME `x`")
		out, err := exec.Command(sh, "-c", "printf %s "+q).Output()
		if err != nil || string(out) != string(moving)+"it's $HOME `x`" {
			t.Errorf("through sh: %q, %v", out, err)
		}
	}
	// The lead runs: a second resume attaches to it, with nothing to say.
	if res = resume(id, planv1.Provider_PROVIDER_UNSPECIFIED); !res.GetAttached() || res.GetNote() != "" || len(leads.opened) != 1 {
		t.Errorf("second resume = %v", res)
	}
	// Once it has exited, the recorded session is resumed.
	delete(leads.running, "lead-"+id)
	if res = resume(id, planv1.Provider_PROVIDER_UNSPECIFIED); leads.opened[1] != "claude --resume "+lead.GetSessionId()+" in "+folder {
		t.Errorf("resume after exit = %v, opened %q", res, leads.opened)
	}

	// Codex and Antigravity take the whole brief as their first message; Djinn cannot know their session.
	for p, prefix := range map[planv1.Provider]string{
		planv1.Provider_PROVIDER_CODEX: "codex '# Leading a wish", planv1.Provider_PROVIDER_ANTIGRAVITY: "agy -i '# Leading a wish",
	} {
		id := make("Lead with " + p.String())
		res := resume(id, p)
		got := leads.opened[len(leads.opened)-1]
		if !strings.HasPrefix(got, prefix) || !strings.Contains(got, "# The wish: Lead with") || res.GetWish().GetLead() != nil ||
			res.GetNote() == "" {
			t.Errorf("%s: opened %q, resume %v", p, got, res)
		}
	}
	// Without a project, every lead starts in one folder, the parent of each wish's own: Claude Code's trust covers
	// the subfolders of the folder trusted, so it asks once. The brief stays in the wish's own folder.
	for _, p := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX} {
		if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: last})); err != nil {
			t.Fatal(err)
		}
		w, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Alone with " + p.String()}))
		if err != nil {
			t.Fatal(err)
		}
		last = w.Msg.GetWish().GetId()
		res := resume(last, p)
		got, want := leads.opened[len(leads.opened)-1], " in "+filepath.Join(home, PagesDir)
		if !strings.HasSuffix(got, want) || (p == planv1.Provider_PROVIDER_CLAUDE &&
			res.GetWish().GetLead().GetDirectory() != filepath.Join(home, PagesDir)) {
			t.Errorf("%s without a project: opened %q, lead %v, want%s", p, got, res.GetWish().GetLead(), want)
		}
		if _, err := os.Stat(filepath.Join(home, PagesDir, last, LeadBriefFile)); err != nil {
			t.Errorf("%s without a project: %v", p, err)
		}
	}
	// The fake agent has no terminal.
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{
		WishId: make("Fake lead"), Provider: planv1.Provider_PROVIDER_FAKE,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a fake lead: %v, want invalid_argument", err)
	}
}

func TestBriefLine(t *testing.T) {
	brief := Brief{Stable: "# Rules\n", Moving: "# The wish: 100% done\n"}
	data := `C:\Users\Ann Lee\AppData\Roaming\djinn\wishes\w1`
	rules, moving := data+`\`+LeadRulesFile, data+`\`+LeadBriefFile
	claude := &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: "s1"}

	// cmd.exe carries no line break: the first message names the brief's file, and the folder is allowed.
	line, err := briefLine("windows", claude, data, brief)
	if err != nil {
		t.Fatal(err)
	}
	want := `claude --session-id s1 --append-system-prompt-file "` + rules + `" --add-dir "` + data + `" "Read ` + moving +
		`: where the wish stands now, written by Djinn. Then lead it."`
	if line != want {
		t.Errorf("windows line\n%s\nwant\n%s", line, want)
	}
	codex, err := briefLine("windows", &planv1.Lead{Provider: planv1.Provider_PROVIDER_CODEX}, data, brief)
	if err != nil || !strings.HasPrefix(codex, `codex "Read `+rules+", then "+moving) {
		t.Errorf("windows codex line %q, %v", codex, err)
	}
	// A data folder cmd.exe cannot quote is refused, with why.
	if _, err := briefLine("windows", claude, `C:\100%`, brief); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a %% in the data folder: %v", err)
	}
	// A brief too long for one argument is read from its file, on every system.
	long := Brief{Stable: "# Rules\n", Moving: strings.Repeat("x", maxBriefArg+1)}
	if line, err := briefLine("linux", claude, "/d", long); err != nil ||
		!strings.HasSuffix(line, "--add-dir '/d' 'Read /d/lead-brief.md: where the wish stands now, written by Djinn. Then lead it.'") {
		t.Errorf("a long brief: %q, %v", line, err)
	}
	if q, ok := quoteArg("darwin", "it's"); !ok || q != `'it'\''s'` {
		t.Errorf("quote = %q", q)
	}
}

// TestWishProvider: the agent chosen when a wish is made is its lead's, said in its brief; a wish made before the
// choice leads with Claude.
func TestWishProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix; TestBriefLine checks cmd.exe")
	}
	ctx := t.Context()
	home := t.TempDir()
	c, leads := serveLeads(t, home)
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		chosen planv1.Provider
		name   string
		line   string
	}{
		{planv1.Provider_PROVIDER_UNSPECIFIED, "claude", "claude --session-id "},
		{planv1.Provider_PROVIDER_CLAUDE, "claude", "claude --session-id "},
		{planv1.Provider_PROVIDER_CODEX, "codex", "codex '# Leading a wish"},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, "antigravity", "agy -i '# Leading a wish"},
	} {
		made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Lead with " + tc.name, ProjectIds: []string{project.Msg.GetProject().GetId()}, Provider: tc.chosen, Paused: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		wish := made.Msg.GetWish()
		if wish.GetProvider() != tc.chosen || WishProvider(wish).String() != "PROVIDER_"+strings.ToUpper(tc.name) {
			t.Errorf("%s: made %v", tc.chosen, wish)
		}
		brief, err := BuildBrief(ctx, c.store, home, wish.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(brief.Moving, "- Agent: "+tc.name+", chosen with the wish") {
			t.Errorf("%s: brief = %q", tc.chosen, brief.Moving)
		}
		// Resume without a kind of agent starts the wish's.
		if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: wish.GetId()})); err != nil {
			t.Fatal(err)
		}
		if got := leads.opened[len(leads.opened)-1]; !strings.HasPrefix(got, tc.line) {
			t.Errorf("%s: opened %q, want %q…", tc.chosen, got, tc.line)
		}
	}
}
