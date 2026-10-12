package plan

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	running map[string][]string // command of each running terminal, by name
	opened  []string            // line of each program started
	shown   []string            // wish/terminal of each show
	said    []string            // terminal: line of each line typed
	closed  []string            // name of each running terminal closed
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

func setLead(t *testing.T, c clients, req *planv1.LeadServiceSetRequest) (*planv1.Lead, error) {
	t.Helper()
	res, err := c.lead.Set(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetLead(), nil
}

func TestSetLead(t *testing.T) {
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
	if res.GetAttached() || res.GetNote() != "" || strings.Join(res.GetCommand(), " ") != "/bin/sh -c claude --resume "+session {
		t.Errorf("resume = %v", res)
	}
	if want := "claude --resume " + session + " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
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
// error says to create one.
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
	if want := "agy -i '" + StartLine() + "' in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
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
	// Once it exits, the recorded agent resumes its own session with agy --continue.
	delete(leads.running, terminal)
	if res = resume(planv1.Provider_PROVIDER_UNSPECIFIED); len(leads.opened) != 2 || leads.opened[1] != "agy --continue in "+folder || !agyKept(res) {
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
		len(leads.opened) != 4 || leads.opened[3] != "claude --session-id "+lead.GetSessionId()+" '"+StartLine()+"' in "+folder {
		t.Errorf("claude over codex = %v, opened %q", res, leads.opened)
	}
}
