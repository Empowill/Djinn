package plan

import (
	"bytes"
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"

// fakeLeads records what Resume asks of the terminals and the window.
type fakeLeads struct {
	running map[string][]string
	opened  []string
	shown   []string
	told    []string
	stopped []string
	waiting planv1.TellWait
	watch   func()
	prompts map[string]*planv1.LeadPrompt
	chosen  []string
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

func (f *fakeLeads) Running(name string) bool { _, ok := f.running[name]; return ok }

func (f *fakeLeads) Stop(name string) error {
	if f.running[name] != nil {
		f.stopped = append(f.stopped, name)
	}
	delete(f.running, name)
	return nil
}

func (f *fakeLeads) Tell(name, text string) (planv1.TellWait, error) {
	if !f.Running(name) {
		return planv1.TellWait_TELL_WAIT_UNSPECIFIED, ErrNoLead
	}
	if cmd := f.running[name]; !LeadLine(cmd[len(cmd)-1]) {
		return planv1.TellWait_TELL_WAIT_UNSPECIFIED, ErrNotLead
	}
	f.told = append(f.told, name+": "+text)
	return f.waiting, nil
}

func (f *fakeLeads) Watch(fn func()) { f.watch = fn }

func (f *fakeLeads) Prompt(name string) *planv1.LeadPrompt { return f.prompts[name] }

func (f *fakeLeads) Choose(name, title string, lines, options []string, index int) error {
	p := f.prompts[name]
	if !f.Running(name) {
		return ErrNoLead
	}
	if p == nil || p.GetTitle() != title || !slices.Equal(p.GetLines(), lines) || !slices.Equal(p.GetOptions(), options) || index >= len(options) {
		return ErrNoChoice
	}
	f.chosen = append(f.chosen, name+": "+options[index])
	delete(f.prompts, name)
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
	if res.GetAttached() || res.GetNote() != "" || strings.Join(res.GetCommand(), " ") != "/bin/sh -c claude "+
		leadFlags(runtime.GOOS, planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_NONE)+"--resume "+session {
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

// TestTell: what the developer writes reaches the lead's terminal while it runs; without a lead running, Tell says
// how to start it, and the list of the wishes says which leads run.
func TestTell(t *testing.T) {
	ctx := t.Context()
	if _, err := serve(t).wishes.Tell(ctx, connect.NewRequest(&planv1.WishServiceTellRequest{WishId: session, Text: "hi"})); code(err) != connect.CodeUnimplemented {
		t.Errorf("tell without terminals: %v, want unimplemented", err)
	}

	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	if leads.watch == nil {
		t.Fatal("the wishes do not follow the terminals")
	}
	id := c.wish(t)
	tell := func(text string) (*planv1.WishServiceTellResponse, error) {
		res, err := c.wishes.Tell(ctx, connect.NewRequest(&planv1.WishServiceTellRequest{WishId: id, Text: text}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	running := func() bool {
		t.Helper()
		res, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetWishes()[0].GetLeadRunning()
	}

	if _, err := tell("Hello"); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "djinn wish resume "+id) {
		t.Errorf("tell without a lead: %v, want a hint to resume", err)
	}
	if running() {
		t.Error("a lead runs, yet none was started")
	}
	// A terminal that starts or ends changes the wishes, for the window to read them again.
	s := watch(t, c.wishes, id)
	leads.running = map[string][]string{LeadTerminal(id): {"claude"}}
	leads.watch()
	if msg := next(t, s); !slices.Contains(msg.GetChanges(), planv1.Change_CHANGE_WISH) {
		t.Errorf("after a lead started: %v, want the wishes changed", msg)
	}
	if !running() {
		t.Error("the lead runs, yet the list says it does not")
	}
	res, err := tell("  First line\nsecond line \n")
	if err != nil || res.GetWaiting() {
		t.Fatalf("tell = %v, %v", res, err)
	}
	leads.waiting = planv1.TellWait_TELL_WAIT_TYPING
	if res, err = tell("Then"); err != nil || !res.GetWaiting() || res.GetWait() != planv1.TellWait_TELL_WAIT_TYPING {
		t.Errorf("tell while the developer types = %v, %v, want waiting", res, err)
	}
	if want := []string{"lead-" + id + ": First line\nsecond line", "lead-" + id + ": Then"}; strings.Join(leads.told, "|") != strings.Join(want, "|") {
		t.Errorf("told %q, want %q", leads.told, want)
	}
	// A shell under the lead's name: nothing is typed there, where it would run as a command.
	leads.running[LeadTerminal(id)] = []string{"/bin/sh"}
	if _, err := tell("rm -rf ."); code(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "another program than the lead") || len(leads.told) != 2 {
		t.Errorf("tell a shell: %v, told %q; want refused", err, leads.told)
	}
	if _, err := tell(" \n "); code(err) != connect.CodeInvalidArgument {
		t.Errorf("tell nothing: %v, want invalid", err)
	}
	if _, err := c.wishes.Tell(ctx, connect.NewRequest(&planv1.WishServiceTellRequest{WishId: session, Text: "hi"})); code(err) != connect.CodeNotFound {
		t.Errorf("tell an unknown wish: %v, want not found", err)
	}
}

// TestLeadLine: the lines a lead runs, as Resume starts them, and nothing else.
func TestLeadLine(t *testing.T) {
	lead := &planv1.Lead{SessionId: session}
	var lines []string
	for _, p := range []planv1.Provider{planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX} {
		lead.Provider = p
		line, err := resumeLine("linux", lead, planv1.Allowance_ALLOWANCE_AUTO)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	for _, p := range []planv1.Provider{
		planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX, planv1.Provider_PROVIDER_ANTIGRAVITY,
	} {
		lead.Provider = p
		line, err := briefLine("linux", lead, planv1.Allowance_ALLOWANCE_EDIT, "/data/wish", Brief{Stable: "rules", Moving: "now"})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	for _, line := range lines {
		if !LeadLine(line) {
			t.Errorf("%q is a lead's line", line)
		}
	}
	for _, line := range []string{"", "zsh", "/bin/sh -l", "sleep 30", "claudette", "echo claude"} {
		if LeadLine(line) {
			t.Errorf("%q is not a lead's line", line)
		}
	}
}

// TestLeadMode: the mode chosen with the wish, or allowed after, is the one its lead starts with, in the project it
// starts in; nothing more. Without one, the agent's own configuration decides.
func TestLeadMode(t *testing.T) {
	ctx := t.Context()
	c, leads := serveLeads(t, t.TempDir())
	dir := t.TempDir()
	project, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	pid := project.Msg.GetProject().GetId()
	resume := func(id string, provider planv1.Provider) string {
		t.Helper()
		if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{WishId: id, Provider: provider})); err != nil {
			t.Fatal(err)
		}
		delete(leads.running, LeadTerminal(id))
		return leads.opened[len(leads.opened)-1]
	}
	for _, tc := range []struct {
		provider planv1.Provider
		mode     planv1.Allowance
		has, not []string
	}{
		{planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_UNSPECIFIED,
			nil,
			[]string{"--allowedTools", "--permission-mode", "djinn wish grant", "djinn wish allow", "djinn question answer"}},
		{planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_EDIT, []string{"--permission-mode acceptEdits"}, []string{"auto"}},
		{planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_AUTO, []string{"--permission-mode auto"}, nil},
		{planv1.Provider_PROVIDER_CODEX, planv1.Allowance_ALLOWANCE_UNSPECIFIED, nil, []string{"--sandbox", "--ask-for-approval"}},
		{planv1.Provider_PROVIDER_CODEX, planv1.Allowance_ALLOWANCE_EDIT,
			[]string{"codex --sandbox workspace-write --ask-for-approval on-request '"}, []string{"--approve-for-me"}},
		{planv1.Provider_PROVIDER_CODEX, planv1.Allowance_ALLOWANCE_AUTO, []string{"--approve-for-me"}, nil},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, planv1.Allowance_ALLOWANCE_EDIT, []string{"agy -i --mode accept-edits"}, nil},
		{planv1.Provider_PROVIDER_ANTIGRAVITY, planv1.Allowance_ALLOWANCE_AUTO, []string{"agy -i --mode accept-edits"}, nil},
	} {
		made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
			Title: "Lead " + tc.provider.String() + " " + tc.mode.String(), ProjectIds: []string{pid},
			Provider: tc.provider, Allowance: tc.mode, Paused: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		wish := made.Msg.GetWish()
		if want := cmp.Or(tc.mode, planv1.Allowance_ALLOWANCE_NONE); AllowanceOf(wish, pid) != want {
			t.Errorf("%v: made with %v", tc.mode, wish.GetAllowances())
		}
		line := resume(wish.GetId(), tc.provider)
		for _, s := range tc.has {
			if !strings.Contains(line, s) {
				t.Errorf("%v %v: %q lacks %q", tc.provider, tc.mode, line, s)
			}
		}
		for _, s := range tc.not {
			if strings.Contains(line, s) {
				t.Errorf("%v %v: %q has %q", tc.provider, tc.mode, line, s)
			}
		}
		// The session resumed takes the mode allowed since.
		if tc.provider == planv1.Provider_PROVIDER_CLAUDE && tc.mode == planv1.Allowance_ALLOWANCE_EDIT {
			if _, err := c.wishes.Allow(ctx, connect.NewRequest(&planv1.WishServiceAllowRequest{
				WishId: wish.GetId(), Mode: planv1.Allowance_ALLOWANCE_AUTO,
			})); err != nil {
				t.Fatal(err)
			}
			if line := resume(wish.GetId(), tc.provider); !strings.Contains(line, "--permission-mode auto --resume ") {
				t.Errorf("resumed after allow: %q", line)
			}
		}
	}
	// A lead without a project keeps the provider’s permissions.
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "No project", Allowance: planv1.Allowance_ALLOWANCE_AUTO, Paused: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if line := resume(made.Msg.GetWish().GetId(), planv1.Provider_PROVIDER_CLAUDE); strings.Contains(line, "--permission-mode") ||
		len(made.Msg.GetWish().GetAllowances()) != 0 {
		t.Errorf("without a project: %q, %v", line, made.Msg.GetWish().GetAllowances())
	}
}

// TestChoose: the choice the lead shows comes with its wish, and the window's answer goes to the lead's terminal,
// only while that very choice shows.
func TestChoose(t *testing.T) {
	ctx := t.Context()
	choose := func(c clients, req *planv1.WishServiceChooseRequest) error {
		_, err := c.wishes.Choose(ctx, connect.NewRequest(req))
		return err
	}
	if err := choose(serve(t), &planv1.WishServiceChooseRequest{WishId: session, Option: 1}); code(err) != connect.CodeUnimplemented {
		t.Errorf("choose without terminals: %v, want unimplemented", err)
	}
	leads := &fakeLeads{}
	c := serve(t, WithLeads(leads))
	id := c.wish(t)
	name := LeadTerminal(id)
	prompt := &planv1.LeadPrompt{
		Title: "Would you like to run the following command?", Lines: []string{"$ djinn wish brief " + id},
		Options: []string{"Yes, proceed (y)", "Yes, and don't ask again for commands that start with `djinn` (p)", "No"},
	}
	leads.running = map[string][]string{name: {"/bin/sh", "-c", "codex resume s1"}}
	leads.prompts = map[string]*planv1.LeadPrompt{name: prompt}
	listed, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := listed.Msg.GetWishes()[0].GetLeadPrompt(); got.GetTitle() != prompt.GetTitle() || len(got.GetOptions()) != 3 {
		t.Fatalf("lead prompt = %v", got)
	}
	req := &planv1.WishServiceChooseRequest{WishId: id, Option: 2, Title: prompt.GetTitle(), Lines: prompt.GetLines(), Options: prompt.GetOptions()}
	other := &planv1.WishServiceChooseRequest{WishId: id, Option: 2, Title: "Do you want to proceed?", Lines: prompt.GetLines(), Options: prompt.GetOptions()}
	if err := choose(c, other); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("another choice: %v, want failed precondition", err)
	}
	changedCommand := &planv1.WishServiceChooseRequest{WishId: id, Option: 2, Title: prompt.GetTitle(), Options: prompt.GetOptions(), Lines: []string{"$ another command"}}
	if err := choose(c, changedCommand); code(err) != connect.CodeFailedPrecondition {
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
	if err := choose(c, &planv1.WishServiceChooseRequest{WishId: id, Option: 0}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("option 0: %v, want invalid argument", err)
	}
}

func TestWindowsLeadAllowsPowerShellCommands(t *testing.T) {
	flags := leadFlags("windows", planv1.Provider_PROVIDER_CLAUDE, planv1.Allowance_ALLOWANCE_EDIT)
	for _, tool := range []string{"Bash", "PowerShell"} {
		if !strings.Contains(flags, tool+"(djinn task spawn *)") {
			t.Errorf("missing %s orchestration rules: %s", tool, flags)
		}
	}
}
