package plan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"

// fakeLeads records what Resume asks of the terminals and the window.
type fakeLeads struct {
	running map[string][]string           // command of each running terminal, by name
	opened  []string                      // line of each program started
	shown   []string                      // wish/terminal of each show
	said    []string                      // terminal: line of each line typed
	closed  []string                      // name of each running terminal closed
	waiting bool                          // what SayText says of the texts said
	prompts map[string]*planv1.LeadPrompt // the choice each terminal shows, by name
	exits   map[string]*planv1.LeadExit   // how the program of each ended terminal ended, by name
	chosen  []string                      // terminal: option of each choice answered
	err     error
}

func (f *fakeLeads) Open(name, line, dir, exclusive string) ([]string, string, bool, error) {
	if f.err != nil {
		return nil, "", false, f.err
	}
	if cmd, ok := f.running[name]; ok {
		return cmd, dir, true, nil
	}
	cmd := []string{"/bin/sh", "-c", line}
	if line == "" {
		cmd = []string{"/bin/sh"}
	}
	if f.running == nil {
		f.running = map[string][]string{}
	}
	f.running[name] = cmd
	f.opened = append(f.opened, line+" in "+dir)
	return cmd, dir, false, nil
}

func (f *fakeLeads) Show(wishID, terminal string) { f.shown = append(f.shown, wishID+"/"+terminal) }

func (f *fakeLeads) Running(name string) bool {
	if f == nil || f.running == nil {
		return false
	}
	_, ok := f.running[name]
	return ok
}

func (f *fakeLeads) Command(name string) []string {
	if f == nil {
		return nil
	}
	return f.running[name]
}

func (f *fakeLeads) Close(name string) {
	if _, ok := f.running[name]; ok {
		delete(f.running, name)
		f.closed = append(f.closed, name)
	}
}

func (f *fakeLeads) Say(name, line string) error {
	if _, ok := f.running[name]; !ok {
		return errors.New("no such terminal")
	}
	f.said = append(f.said, name+": "+line)
	return nil
}

func (f *fakeLeads) SayText(name, text string) (bool, error) {
	if err := f.Say(name, text); err != nil {
		return false, err
	}
	return f.waiting, nil
}

func (f *fakeLeads) Prompt(name string) *planv1.LeadPrompt { return f.prompts[name] }

func (f *fakeLeads) Exit(name string) *planv1.LeadExit { return f.exits[name] }

func (f *fakeLeads) Choose(name, title string, lines, options []string, index int) error {
	p := f.prompts[name]
	if p == nil || p.GetTitle() != title || !slices.Equal(p.GetLines(), lines) ||
		!slices.Equal(p.GetOptions(), options) || index >= len(options) {
		return ErrNoChoice
	}
	f.chosen = append(f.chosen, name+": "+options[index])
	delete(f.prompts, name)
	return nil
}

func setLead(t *testing.T, c clients, req *planv1.LeadServiceSetRequest) (*planv1.Lead, error) {
	t.Helper()
	res, err := c.lead.Set(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetLead(), nil
}

func TestSetLead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := serve(t)
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}

	// By default Claude, in the folder of Djinn's first project.
	lead, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session})
	if err != nil {
		t.Fatal(err)
	}
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetSessionId() != session ||
		lead.GetDirectory() != project.Msg.GetProject().GetDirectory() {
		t.Errorf("lead = %v", lead)
	}
	// A folder of its own, made canonical; stored lead keeps it.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{
		SessionId: "thread-1", Provider: planv1.Provider_PROVIDER_CODEX, Directory: sub,
	}); err != nil {
		t.Fatal(err)
	}
	getRes, err := c.lead.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := getRes.Msg.GetLead(); got.GetProvider() != planv1.Provider_PROVIDER_CODEX ||
		got.GetSessionId() != "thread-1" || filepath.Base(got.GetDirectory()) != "sub" {
		t.Errorf("stored lead = %v", got)
	}

	// Antigravity with a session id.
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{
		SessionId: "c-1", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY, Directory: sub,
	}); err != nil {
		t.Fatal(err)
	}

	for name, req := range map[string]*planv1.LeadServiceSetRequest{
		"a provider without a terminal": {SessionId: session, Provider: planv1.Provider_PROVIDER_FAKE},
		"a session that is not a word":  {SessionId: "x; rm -rf ~"},
		"a missing folder":              {SessionId: session, Directory: filepath.Join(dir, "nowhere")},
		"claude without session":        {SessionId: "", Provider: planv1.Provider_PROVIDER_CLAUDE},
		"codex without session":         {SessionId: "", Provider: planv1.Provider_PROVIDER_CODEX},
		"antigravity without session":   {SessionId: "", Provider: planv1.Provider_PROVIDER_ANTIGRAVITY},
	} {
		if _, err := setLead(t, c, req); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", name, err)
		}
	}
	// Without a project folder needs one.
	emptyC := serve(t)
	if _, err := setLead(t, emptyC, &planv1.LeadServiceSetRequest{SessionId: session}); code(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "--directory") {
		t.Errorf("no folder: %v, want a hint to --directory", err)
	}
}

func TestResume(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if _, err := serve(t).lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); code(err) != connect.CodeUnimplemented {
		t.Errorf("resume without terminals: %v, want unimplemented", err)
	}

	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	folder := project.Msg.GetProject().GetDirectory()
	resume := func() *planv1.LeadServiceResumeResponse {
		t.Helper()
		res, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	// Without a session nor a data folder for a new lead's first message: nothing starts, never an empty shell in
	// the lead's terminal, and the error says how to record a session.
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "no lead session") || !strings.Contains(err.Error(), "djinn lead set") {
		t.Errorf("resume without a session: %v, want failed_precondition", err)
	}
	if len(leads.opened) != 0 || leads.running[LeadTerminal] != nil {
		t.Errorf("opened %q without a session", leads.opened)
	}
	// With one, in a terminal that runs a shell: it is attached, and told.
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	leads.running = map[string][]string{LeadTerminal: {"/bin/sh"}}
	if res := resume(); !res.GetAttached() || !strings.Contains(res.GetNote(), "exit it there") || res.GetDirectory() != folder {
		t.Errorf("resume over a running shell = %v, want attached with a note", res)
	}
	delete(leads.running, LeadTerminal)
	res := resume()
	tools, _ := quoteArg(runtime.GOOS, claudeLeadTools)
	wantLine := "claude --resume " + session + " --allowedTools " + tools
	if res.GetAttached() || res.GetNote() != "" || strings.Join(res.GetCommand(), " ") != "/bin/sh -c "+wantLine {
		t.Errorf("resume = %v", res)
	}
	if want := wantLine + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Errorf("opened %q, want %q", leads.opened, want)
	}
	if res = resume(); !res.GetAttached() || res.GetNote() != "" || len(leads.opened) != 1 {
		t.Errorf("second resume = %v, opened %q; want attached, one program", res, leads.opened)
	}
	if len(leads.shown) != 3 || leads.shown[2] != "/"+LeadTerminal {
		t.Errorf("shown %q", leads.shown)
	}

	// A refusal of the terminals, as the guard of a session running elsewhere, comes back as is.
	delete(leads.running, LeadTerminal)
	leads.err = errors.New("already running in another terminal")
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "already running") {
		t.Errorf("refused: %v", err)
	}
	leads.err = nil
	// A lead folder this machine lacks is said, with the command that gives it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{})); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "lead set") {
		t.Errorf("missing folder: %v", err)
	}
}

func TestLeadFolders(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if got := portableFolder(filepath.Join(string(filepath.Separator), "srv", "x")); got != "" {
		t.Errorf("an absolute folder outside the projects and the home folder travels: %q", got)
	}
	folders := map[string]string{"api": filepath.Join(home, "code", "api"), "web": ""}
	for in, want := range map[string]string{
		"~/code/x":   filepath.Join(home, "code", "x"),
		"~":          home,
		"API/cmd":    filepath.Join(home, "code", "api", "cmd"),
		`api\cmd`:    filepath.Join(home, "code", "api", "cmd"),
		"web/static": "",
		"other":      "",
		"":           "",
	} {
		if got := localFolder(in, folders); got != want {
			t.Errorf("localFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLeadStartsInAProject: a lead never starts in the home folder. A session recorded there is not resumed: a new
// lead starts from the brief in the first of Djinn's projects; with no project at all, nothing starts, and the
// error says to create one. It sets HOME: it runs alone.
func TestLeadStartsInAProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the home folder comes from USERPROFILE there; HoldsHome is the same code")
	}
	ctx := t.Context()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	c, leads := serveLeads(t, t.TempDir())
	resume := func() (*planv1.LeadServiceResumeResponse, error) {
		res, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	// No project: no lead, and the way out.
	if _, err := resume(); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "create a project first") {
		t.Errorf("resume without any project: %v, want failed_precondition asking for a project", err)
	}
	if _, err := FirstProjectFolder(ctx, c.store); !errors.Is(err, ErrNoProject) {
		t.Errorf("FirstProjectFolder without a project: %v", err)
	}
	if len(leads.opened) != 0 {
		t.Fatalf("opened %q without a project", leads.opened)
	}

	// Two projects, the home folder's own first (a project there never hosts a lead), then lamp, then shop.
	homeProject := &planv1.Project{Id: store.NewID(), Name: "home", Directory: home}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		return errors.Join(tx.Journal("test", planv1connect.ProjectServiceAddProcedure,
			&planv1.ProjectServiceAddRequest{Directory: home}), tx.Put(homeProject))
	}); err != nil {
		t.Fatal(err)
	}
	add := func(name string) *planv1.Project {
		t.Helper()
		dir := filepath.Join(home, "code", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		res, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetProject()
	}
	lamp, _ := add("lamp"), add("shop")
	if got, err := FirstProjectFolder(ctx, c.store); err != nil || got != lamp.GetDirectory() {
		t.Errorf("FirstProjectFolder = %q, %v; want lamp's", got, err)
	}

	// A lead starts in the first of Djinn's projects.
	res, err := resume()
	if err != nil {
		t.Fatal(err)
	}
	if res.GetDirectory() != lamp.GetDirectory() || res.GetLead().GetDirectory() != lamp.GetDirectory() ||
		len(leads.opened) != 1 || !strings.HasSuffix(leads.opened[0], " in "+lamp.GetDirectory()) {
		t.Errorf("a lead without a project: %v, opened %q", res, leads.opened)
	}

	// The home folder is refused as a lead's folder.
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session, Directory: home}); code(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "home folder") {
		t.Errorf("set-lead in the home folder: %v, want invalid_argument", err)
	}
	// A session recorded there all the same (by an older Djinn, an import of ~) is not resumed there, nor told.
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		lead := &planv1.Lead{Id: "lead", Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: home}
		return errors.Join(tx.Journal("test", planv1connect.LeadServiceSetProcedure,
			&planv1.LeadServiceSetRequest{SessionId: session, Directory: home}), tx.Put(lead))
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&Wishes{Store: c.store, Leads: leads}).Tell(ctx, "", "Djinn: hello"); err == nil || !strings.Contains(err.Error(), "home folder") {
		t.Errorf("tell a lead recorded in the home folder: %v", err)
	}
	if len(leads.opened) != 1 {
		t.Fatalf("opened %q in the home folder", leads.opened)
	}
	// Resume starts a new lead from the brief, in lamp, and records it.
	delete(leads.running, LeadTerminal)
	if res, err = resume(); err != nil {
		t.Fatal(err)
	}
	lead := res.GetLead()
	if len(leads.opened) != 2 || !strings.HasPrefix(leads.opened[1], "claude --session-id "+lead.GetSessionId()) ||
		!strings.HasSuffix(leads.opened[1], " in "+lamp.GetDirectory()) {
		t.Errorf("opened %q, want a new lead in lamp", leads.opened)
	}
	if lead.GetSessionId() == session || lead.GetDirectory() != lamp.GetDirectory() ||
		!strings.Contains(res.GetNote(), "home folder") || !strings.Contains(res.GetNote(), session) {
		t.Errorf("resume of a lead recorded in the home folder = %v", res)
	}
}

// TestHoldsHome sets HOME: it runs alone.
func TestHoldsHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the home folder comes from USERPROFILE there")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(home, "me"))
	for dir, want := range map[string]bool{
		filepath.Join(home, "me"):                true,
		filepath.Join(home, "me") + "/":          true,
		home:                                     true,
		"/":                                      true,
		filepath.Join(home, "me", "code"):        false,
		filepath.Join(home, "me-too"):            false,
		filepath.Join(home, "other", "..", "me"): true,
		"":                                       false,
	} {
		if got := HoldsHome(dir); got != want {
			t.Errorf("HoldsHome(%q) = %v, want %v", dir, got, want)
		}
	}
}

// TestResumeAnotherAgent: the developer picks another agent than the recorded lead's. A new lead of that agent starts
// from the brief in the lead's terminal; an antigravity or codex one leaves the claude record as it is, a claude one
// over a codex record takes its place. While the terminal runs a lead, nothing starts, and the note says so.
func TestResumeAnotherAgent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the lines are checked for the shells of Unix; TestLeadLine checks cmd.exe")
	}
	ctx := t.Context()
	c, leads := serveLeads(t, t.TempDir())
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	folder := project.Msg.GetProject().GetDirectory()
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	terminal := LeadTerminal
	resume := func(p planv1.Provider) *planv1.LeadServiceResumeResponse {
		t.Helper()
		res, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{Provider: p}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	agyKept := func(res *planv1.LeadServiceResumeResponse) bool {
		lead := res.GetLead()
		return lead.GetProvider() == planv1.Provider_PROVIDER_ANTIGRAVITY && lead.GetDirectory() == folder
	}

	// Antigravity on a claude-led djinn: agy -i on the start line, in the project; becomes the lead.
	res := resume(planv1.Provider_PROVIDER_ANTIGRAVITY)
	if want := "agy -i '" + StartLine() + "' --project " + LeadAgyProjectID + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Fatalf("opened %q, want %q", leads.opened, want)
	}
	if res.GetAttached() || res.GetTerminal() != terminal || !agyKept(res) ||
		!strings.Contains(res.GetNote(), "The folder's most recent conversation is the lead now") {
		t.Errorf("antigravity over claude = %v", res)
	}
	// While it runs, neither another agent nor the recorded lead starts: the note says to exit it there.
	for _, p := range []planv1.Provider{planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_UNSPECIFIED} {
		res = resume(p)
		if !res.GetAttached() || !strings.Contains(res.GetNote(), "already runs") || !strings.Contains(res.GetNote(), "exit it there") ||
			!agyKept(res) || len(leads.opened) != 1 {
			t.Errorf("%s over a running agy = %v, opened %q", p, res, leads.opened)
		}
	}
	// Once it exits, the recorded agent resumes its own session with agy --continue --project djinn-lead.
	delete(leads.running, terminal)
	if res = resume(planv1.Provider_PROVIDER_UNSPECIFIED); len(leads.opened) != 2 || leads.opened[1] != "agy --continue --project "+LeadAgyProjectID+" in "+folder || !agyKept(res) {
		t.Errorf("resumed agy = %v, opened %q", res, leads.opened)
	}
	// Codex: its session is not known, the antigravity record stays until set-lead gives it.
	delete(leads.running, terminal)
	res = resume(planv1.Provider_PROVIDER_CODEX)
	if len(leads.opened) != 3 || leads.opened[2] != "codex '"+StartLine()+"' in "+folder || !agyKept(res) ||
		!strings.Contains(res.GetNote(), "lead set") || !strings.Contains(res.GetNote(), "stays the antigravity conversation") {
		t.Errorf("codex = %v, opened %q", res, leads.opened)
	}
	// A codex record, then claude: a new claude lead, whose session becomes the lead.
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{
		SessionId: "thread-1", Provider: planv1.Provider_PROVIDER_CODEX,
	}); err != nil {
		t.Fatal(err)
	}
	delete(leads.running, terminal)
	res = resume(planv1.Provider_PROVIDER_CLAUDE)
	lead := res.GetLead()
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetSessionId() == "thread-1" ||
		len(leads.opened) != 4 || leads.opened[3] != "claude --session-id "+lead.GetSessionId()+" --allowedTools '"+claudeLeadTools+"' '"+StartLine()+"' in "+folder {
		t.Errorf("claude over codex = %v, opened %q", res, leads.opened)
	}
}

// TestLeadTell: what the developer writes reaches the lead's terminal as their own words, its line breaks kept,
// about a wish when they name one, and whether it waits for them; never a terminal that runs no lead, nor a shell.
func TestLeadTell(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	tellReq := func(text, wishID string) *connect.Request[planv1.LeadServiceTellRequest] {
		return connect.NewRequest(&planv1.LeadServiceTellRequest{Text: text, WishId: wishID})
	}
	if _, err := serve(t).lead.Tell(ctx, tellReq("hi", "")); code(err) != connect.CodeUnimplemented {
		t.Errorf("tell without terminals: %v, want unimplemented", err)
	}

	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	tell := func(text, wishID string) (*planv1.LeadServiceTellResponse, error) {
		res, err := c.lead.Tell(ctx, tellReq(text, wishID))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	if _, err := tell("Hello", ""); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "djinn lead resume") {
		t.Errorf("tell without a lead: %v, want a hint to resume", err)
	}
	if _, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	if _, err := tell("Hello", ""); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "djinn lead resume") {
		t.Errorf("tell while the lead does not run: %v, want a hint to resume", err)
	}
	leads.running = map[string][]string{LeadTerminal: {"/bin/sh"}}
	if _, err := tell("rm -rf ~", ""); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("tell while the lead's terminal runs a shell: %v, want a refusal", err)
	}
	if len(leads.said) != 0 || len(leads.opened) != 0 {
		t.Fatalf("opened %q, said %q: nothing starts, nothing goes to a shell", leads.opened, leads.said)
	}

	leads.running[LeadTerminal] = []string{"/bin/sh", "-c", "claude --resume " + session}
	res, err := tell("  First line\nsecond line \n", "")
	if err != nil || res.GetWaiting() {
		t.Fatalf("tell = %v, %v", res, err)
	}
	leads.waiting = true
	id := c.wish(t)
	if res, err = tell("Then", id); err != nil || !res.GetWaiting() {
		t.Errorf("tell while the developer types = %v, %v, want waiting", res, err)
	}
	want := []string{
		LeadTerminal + ": First line\nsecond line",
		LeadTerminal + ": About wish Try Djinn on itself (" + id + "): Then",
	}
	if !slices.Equal(leads.said, want) {
		t.Errorf("said %q, want %q", leads.said, want)
	}
	if _, err := tell(" \n ", ""); code(err) != connect.CodeInvalidArgument {
		t.Errorf("tell nothing: %v, want invalid", err)
	}
	if _, err := tell("hi", session); code(err) != connect.CodeNotFound {
		t.Errorf("tell about an unknown wish: %v, want not found", err)
	}
	if len(leads.opened) != 0 || len(leads.shown) != 0 {
		t.Errorf("opened %q, shown %q: telling the lead starts nothing, raises nothing", leads.opened, leads.shown)
	}
}

// TestLeadStartFailureVisibleUntilRetry: why the lead could not start, and how it ended, stay on LeadService.Get, for
// the window to show beside Lead, until Lead starts it again.
func TestLeadStartFailureVisibleUntilRetry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	leads := &fakeLeads{err: errors.New("agent executable unavailable")}
	c := serve(t, WithLeads(leads))
	if _, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.LeadServiceSetRequest{SessionId: session}); err != nil {
		t.Fatal(err)
	}
	watcher := watch(t, c.wishes, "")
	get := func() *planv1.LeadServiceGetResponse {
		t.Helper()
		res, err := c.lead.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	resume := connect.NewRequest(&planv1.LeadServiceResumeRequest{})
	if _, err := c.lead.Resume(ctx, resume); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("start: %v", err)
	}
	if msg := next(t, watcher); !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
		t.Fatalf("the failure did not reach the window: %v", msg)
	}
	if got := get(); got.GetStartError() != "agent executable unavailable" || got.GetExit() != nil {
		t.Fatalf("failure not visible: %v", got)
	}
	if got := get(); got.GetStartError() == "" {
		t.Fatal("the failure went once read")
	}

	leads.err = nil
	if _, err := c.lead.Resume(ctx, resume); err != nil {
		t.Fatal(err)
	}
	if got := get(); got.GetStartError() != "" || got.GetExit() != nil {
		t.Fatalf("the retry did not clear the failure: %v", got)
	}
	// The lead ends at once: how stays shown, until Lead starts it again.
	delete(leads.running, LeadTerminal)
	leads.exits = map[string]*planv1.LeadExit{LeadTerminal: {Code: 2}}
	if got := get(); got.GetExit() == nil || got.GetExit().GetCode() != 2 {
		t.Fatalf("exit not visible: %v", got)
	}
}

// TestLeadChoose: the choice the lead's agent shows comes with the lead, and the window's answer goes to the lead's
// terminal, only while that very choice shows: the same options on another command need a new decision.
func TestLeadChoose(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	choose := func(c clients, req *planv1.LeadServiceChooseRequest) error {
		_, err := c.lead.Choose(ctx, connect.NewRequest(req))
		return err
	}
	if err := choose(serve(t), &planv1.LeadServiceChooseRequest{Option: 1}); code(err) != connect.CodeUnimplemented {
		t.Errorf("choose without terminals: %v, want unimplemented", err)
	}
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	prompt := &planv1.LeadPrompt{
		Title: "Would you like to run the following command?", Lines: []string{"$ djinn wish brief"},
		Options: []string{"Yes, proceed (y)", "Yes, and don't ask again for commands that start with `djinn` (p)", "No"},
	}
	leads.prompts = map[string]*planv1.LeadPrompt{LeadTerminal: prompt}
	req := &planv1.LeadServiceChooseRequest{Option: 2, Title: prompt.GetTitle(), Lines: prompt.GetLines(), Options: prompt.GetOptions()}
	if err := choose(c, req); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "djinn lead resume") {
		t.Errorf("choose while the lead does not run: %v, want a hint to resume", err)
	}
	got, err := c.lead.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil || got.Msg.GetPrompt() != nil {
		t.Fatalf("the prompt of a lead that does not run: %v, %v", got.Msg.GetPrompt(), err)
	}
	leads.running = map[string][]string{LeadTerminal: {"/bin/sh", "-c", "codex resume s1"}}
	got, err = c.lead.Get(ctx, connect.NewRequest(&planv1.LeadServiceGetRequest{}))
	if err != nil || got.Msg.GetPrompt().GetTitle() != prompt.GetTitle() || len(got.Msg.GetPrompt().GetOptions()) != 3 {
		t.Fatalf("lead prompt = %v, %v", got.Msg.GetPrompt(), err)
	}
	other := &planv1.LeadServiceChooseRequest{Option: 2, Title: "Do you want to proceed?", Lines: prompt.GetLines(), Options: prompt.GetOptions()}
	if err := choose(c, other); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("another choice: %v, want failed precondition", err)
	}
	changed := &planv1.LeadServiceChooseRequest{Option: 2, Title: prompt.GetTitle(), Options: prompt.GetOptions(), Lines: []string{"$ another command"}}
	if err := choose(c, changed); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("same options on another command: %v, want failed precondition", err)
	}
	if err := choose(c, req); err != nil {
		t.Fatal(err)
	}
	if len(leads.chosen) != 1 || !strings.HasSuffix(leads.chosen[0], "start with `djinn` (p)") {
		t.Errorf("chosen %q", leads.chosen)
	}
	if err := choose(c, req); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("answered twice: %v, want failed precondition", err)
	}
	if err := choose(c, &planv1.LeadServiceChooseRequest{Option: 0}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("option 0: %v, want invalid argument", err)
	}
}

// TestLeadAgyProject points AgyProjectsDir, the package's, at its own folder: it runs alone.
func TestLeadAgyProject(t *testing.T) {
	projectsDir := t.TempDir()
	prev := AgyProjectsDir
	AgyProjectsDir = func() (string, error) { return projectsDir, nil }
	t.Cleanup(func() { AgyProjectsDir = prev })

	dir := t.TempDir()
	djinnHome := t.TempDir()
	if err := writeLeadAgyProject(dir, djinnHome); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(projectsDir, LeadAgyProjectID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var got leadAgyProjectFile
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != LeadAgyProjectID {
		t.Errorf("djinn-lead.json id = %q, want %q", got.ID, LeadAgyProjectID)
	}
	allow := got.PermissionGrants.PermissionGrants.Allow
	for _, want := range []string{
		"read_file(" + filepath.Clean(dir) + ")",
		"read_file(" + filepath.Clean(djinnHome) + ")",
		"command(djinn)",
		"command(git status)",
		"command(git log)",
		"command(git diff)",
		"command(git show)",
		"command(git branch)",
	} {
		if !slices.Contains(allow, want) {
			t.Errorf("djinn-lead.json lacks %q:\n%s", want, b)
		}
	}
}
