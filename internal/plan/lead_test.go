package plan

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"

// fakeLeads records what Resume asks of the terminals and the window.
type fakeLeads struct {
	running map[string][]string // command of each running terminal, by name
	opened  []string            // line of each program started
	shown   []string            // wish/terminal of each show
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
	// Without --provider, the session is of the wish's agent; an antigravity wish has none to record.
	for chosen, want := range map[planv1.Provider]connect.Code{
		planv1.Provider_PROVIDER_CODEX: 0, planv1.Provider_PROVIDER_ANTIGRAVITY: connect.CodeInvalidArgument,
	} {
		made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Lead with " + chosen.String(), ProjectIds: []string{project.Msg.GetProject().GetId()}, Provider: chosen,
			Paused: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		wish, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: made.Msg.GetWish().GetId(), SessionId: session})
		if code(err) != want || (err == nil && wish.GetLead().GetProvider() != chosen) {
			t.Errorf("%s: lead %v, %v", chosen, wish.GetLead(), err)
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
