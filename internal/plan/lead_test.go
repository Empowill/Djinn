package plan

import (
	"bytes"
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

func setLead(t *testing.T, c clients, req *planv1.WishServiceSetLeadRequest) (*planv1.Wish, error) {
	t.Helper()
	res, err := c.wishes.SetLead(t.Context(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg.GetWish(), nil
}

func TestSetLead(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Resume me", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()

	// By default Claude, in the folder of the wish's first project.
	wish, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session})
	if err != nil {
		t.Fatal(err)
	}
	lead := wish.GetLead()
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetSessionId() != session ||
		lead.GetDirectory() != project.Msg.GetProject().GetDirectory() {
		t.Errorf("lead = %v", lead)
	}
	// A folder of its own, made canonical; the wish keeps it.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{
		WishId: id, SessionId: "thread-1", Provider: planv1.Provider_PROVIDER_CODEX, Directory: sub,
	}); err != nil {
		t.Fatal(err)
	}
	listed, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := listed.Msg.GetWishes()[0].GetLead(); got.GetProvider() != planv1.Provider_PROVIDER_CODEX ||
		got.GetSessionId() != "thread-1" || filepath.Base(got.GetDirectory()) != "sub" {
		t.Errorf("stored lead = %v", got)
	}

	for name, req := range map[string]*planv1.WishServiceSetLeadRequest{
		"a provider without a terminal": {WishId: id, SessionId: session, Provider: planv1.Provider_PROVIDER_FAKE},
		"a session that is not a word":  {WishId: id, SessionId: "x; rm -rf ~"},
		"a missing folder":              {WishId: id, SessionId: session, Directory: filepath.Join(dir, "nowhere")},
	} {
		if _, err := setLead(t, c, req); code(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want invalid_argument", name, err)
		}
	}
	// A wish without a project folder needs one.
	bare := c.wish(t)
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: bare, SessionId: session}); code(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "--directory") {
		t.Errorf("no folder: %v, want a hint to --directory", err)
	}
}

func TestResume(t *testing.T) {
	ctx := t.Context()
	if _, err := serve(t).wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: session})); code(err) != connect.CodeUnimplemented {
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
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Resume me", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	resume := func() *planv1.WishServiceResumeResponse {
		t.Helper()
		res, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	// Without a session: a shell in the first project, and a note that says so.
	res := resume()
	if res.GetTerminal() != "lead-"+id || res.GetAttached() || !strings.Contains(res.GetNote(), "no lead session") ||
		res.GetDirectory() != folder {
		t.Errorf("resume without a session = %v", res)
	}
	if want := " in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Errorf("opened %q, want %q", leads.opened, want)
	}
	// With one, in a new terminal: the shell of the first resume still runs, so it is attached, and told.
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	if res = resume(); !res.GetAttached() || !strings.Contains(res.GetNote(), "exit it there") {
		t.Errorf("resume over a running shell = %v, want attached with a note", res)
	}
	delete(leads.running, "lead-"+id)
	res = resume()
	if res.GetAttached() || res.GetNote() != "" || strings.Join(res.GetCommand(), " ") != "/bin/sh -c claude --resume "+session {
		t.Errorf("resume = %v", res)
	}
	if res = resume(); !res.GetAttached() || res.GetNote() != "" || len(leads.opened) != 2 {
		t.Errorf("second resume = %v, opened %q; want attached, one program", res, leads.opened)
	}
	if len(leads.shown) != 4 || leads.shown[3] != id+"/lead-"+id {
		t.Errorf("shown %q", leads.shown)
	}

	// A refusal of the terminals, as the guard of a session running elsewhere, comes back as is.
	delete(leads.running, "lead-"+id)
	leads.err = errors.New("already running in another terminal")
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id})); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "already running") {
		t.Errorf("refused: %v", err)
	}
	leads.err = nil
	// A lead folder this machine lacks is said, with the command that gives it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id})); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "set-lead") {
		t.Errorf("missing folder: %v", err)
	}
}

func TestLeadTravels(t *testing.T) {
	ctx := t.Context()
	src, wish, repo := source(t)
	sub := filepath.Join(repo, "cmd")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := setLead(t, src, &planv1.WishServiceSetLeadRequest{WishId: wish.GetId(), SessionId: session, Directory: sub}); err != nil {
		t.Fatal(err)
	}
	data := export(t, src, wish.GetId(), filepath.Join(t.TempDir(), "wish.djinn"))
	if bytes.Contains(data, []byte(repo)) {
		t.Error("the export holds the folder of the lead")
	}
	exp, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if lead := exp.GetWish().GetLead(); lead.GetSessionId() != session || lead.GetDirectory() != "api/cmd" ||
		lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE {
		t.Errorf("exported lead = %v, want the session in api/cmd", lead)
	}

	// Where the project is here, the lead's folder comes back under it.
	dst := serve(t)
	clone := gitRepo(t, "elsewhere", "git@github.com:acme/api.git")
	if _, err := dst.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: clone})); err != nil {
		t.Fatal(err)
	}
	res, err := dst.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Msg.GetWish().GetLead().GetDirectory(); got != filepath.Join(res.Msg.GetProjects()[0].GetProject().GetDirectory(), "cmd") {
		t.Errorf("imported lead folder = %q, want cmd under %s", got, clone)
	}
	// Where it is not, the folder waits for set-lead; the session stays.
	empty := serve(t)
	res, err = empty.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
	if err != nil {
		t.Fatal(err)
	}
	if lead := res.Msg.GetWish().GetLead(); lead.GetDirectory() != "" || lead.GetSessionId() != session {
		t.Errorf("lead imported without its project = %v", lead)
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
// lead starts from the brief in the wish's project; a wish without a project starts in the first of Djinn's projects;
// with no project at all, nothing starts, and the error says to create one.
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
	resume := func(id string) (*planv1.WishServiceResumeResponse, error) {
		res, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	// No project: no lead, and the way out.
	bare := c.wish(t)
	if _, err := resume(bare); code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "create a project first") {
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
	lamp, shop := add("lamp"), add("shop")
	if got, err := FirstProjectFolder(ctx, c.store); err != nil || got != lamp.GetDirectory() {
		t.Errorf("FirstProjectFolder = %q, %v; want lamp's", got, err)
	}

	// A wish without a project starts its lead in the first of Djinn's projects.
	res, err := resume(bare)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetDirectory() != lamp.GetDirectory() || res.GetWish().GetLead().GetDirectory() != lamp.GetDirectory() ||
		len(leads.opened) != 1 || !strings.HasSuffix(leads.opened[0], " in "+lamp.GetDirectory()) {
		t.Errorf("a wish without a project: %v, opened %q", res, leads.opened)
	}

	// The home folder is refused as a lead's folder.
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Stock the shop", ProjectIds: []string{shop.GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session, Directory: home}); code(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "home folder") {
		t.Errorf("set-lead in the home folder: %v, want invalid_argument", err)
	}
	// A session recorded there all the same (by an older Djinn, an import of ~) is not resumed there, nor told.
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		wish, err := store.Get[*planv1.Wish](ctx, tx, id)
		if err != nil {
			return err
		}
		wish.Lead = &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: session, Directory: home}
		return errors.Join(tx.Journal("test", planv1connect.WishServiceSetLeadProcedure,
			&planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session, Directory: home}), tx.Put(wish))
	}); err != nil {
		t.Fatal(err)
	}
	if err := (&Wishes{Store: c.store, Leads: leads}).Tell(ctx, id, "Djinn: hello"); err == nil || !strings.Contains(err.Error(), "home folder") {
		t.Errorf("tell a lead recorded in the home folder: %v", err)
	}
	if len(leads.opened) != 1 {
		t.Fatalf("opened %q in the home folder", leads.opened[1:])
	}
	// Resume starts a new lead from the brief, in the wish's project, and records it.
	if res, err = resume(id); err != nil {
		t.Fatal(err)
	}
	lead := res.GetWish().GetLead()
	if len(leads.opened) != 2 || !strings.HasPrefix(leads.opened[1], "claude --session-id "+lead.GetSessionId()) ||
		!strings.HasSuffix(leads.opened[1], " in "+shop.GetDirectory()) {
		t.Errorf("opened %q, want a new lead in shop", leads.opened[1:])
	}
	if lead.GetSessionId() == session || lead.GetDirectory() != shop.GetDirectory() ||
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
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Take it over", ProjectIds: []string{project.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := made.Msg.GetWish().GetId()
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	terminal := LeadTerminal(id)
	resume := func(p planv1.Provider) *planv1.WishServiceResumeResponse {
		t.Helper()
		res, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id, Provider: p}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	claudeKept := func(res *planv1.WishServiceResumeResponse) bool {
		lead := res.GetWish().GetLead()
		return lead.GetProvider() == planv1.Provider_PROVIDER_CLAUDE && lead.GetSessionId() == session
	}

	// Antigravity on a claude-led wish: agy -i on the start line, in the project; the claude record stays.
	res := resume(planv1.Provider_PROVIDER_ANTIGRAVITY)
	if want := "agy -i '" + StartLine(id) + "' in " + folder; len(leads.opened) != 1 || leads.opened[0] != want {
		t.Fatalf("opened %q, want %q", leads.opened, want)
	}
	if res.GetAttached() || res.GetTerminal() != terminal || !claudeKept(res) ||
		!strings.Contains(res.GetNote(), "stays the claude session "+session) {
		t.Errorf("antigravity over claude = %v", res)
	}
	// While it runs, neither another agent nor the recorded lead starts: the note says to exit it there.
	for _, p := range []planv1.Provider{planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_UNSPECIFIED} {
		res = resume(p)
		if !res.GetAttached() || !strings.Contains(res.GetNote(), "already runs") || !strings.Contains(res.GetNote(), "exit it there") ||
			!claudeKept(res) || len(leads.opened) != 1 {
			t.Errorf("%s over a running agy = %v, opened %q", p, res, leads.opened)
		}
	}
	// Once it exits, the recorded agent resumes its own session.
	delete(leads.running, terminal)
	if res = resume(planv1.Provider_PROVIDER_CLAUDE); leads.opened[1] != "claude --resume "+session+" in "+folder || !claudeKept(res) {
		t.Errorf("claude = %v, opened %q", res, leads.opened)
	}
	// Codex: its session is not known, the claude record stays until set-lead gives it.
	delete(leads.running, terminal)
	res = resume(planv1.Provider_PROVIDER_CODEX)
	if leads.opened[2] != "codex '"+StartLine(id)+"' in "+folder || !claudeKept(res) ||
		!strings.Contains(res.GetNote(), "set-lead") || !strings.Contains(res.GetNote(), "stays the claude session") {
		t.Errorf("codex = %v, opened %q", res, leads.opened)
	}
	// A codex record, then claude: a new claude lead, whose session becomes the wish's lead.
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{
		WishId: id, SessionId: "thread-1", Provider: planv1.Provider_PROVIDER_CODEX,
	}); err != nil {
		t.Fatal(err)
	}
	delete(leads.running, terminal)
	res = resume(planv1.Provider_PROVIDER_CLAUDE)
	lead := res.GetWish().GetLead()
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetSessionId() == "thread-1" ||
		leads.opened[3] != "claude --session-id "+lead.GetSessionId()+" '"+StartLine(id)+"' in "+folder {
		t.Errorf("claude over codex = %v, opened %q", res, leads.opened)
	}
}
