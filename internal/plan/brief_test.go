package plan

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		Error: "create the worktree: " + repo + " is locked", CreateTime: timestamppb.Now(), Decision: "Q01",
	}
	// One Djinn resumes by itself, and one cut short that another task took over: neither is the lead's move.
	resuming := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), Code: "W3", Title: "Reopen the leads", Status: planv1.TaskStatus_TASK_STATUS_RESUMING,
		WaitReason: "the account's session limit, resets at 07:20", CreateTime: timestamppb.Now(),
	}
	cut := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), Code: "W4", Title: "Pause a worker", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
		Error: "djinn up ended while the worker ran", CreateTime: timestamppb.Now(), EndTime: timestamppb.Now(),
	}
	fork := &planv1.Task{
		Id: store.NewID(), WishId: wish.GetId(), Code: "W5", Title: "Pause a worker, again", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		ForkOf: "W4", CreateTime: timestamppb.Now(), EndTime: timestamppb.Now(),
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range []*planv1.Task{failed, resuming, cut, fork} {
			if err := tx.Journal(actor, "test/put", task); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
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
		"**W3** Reopen the leads: resuming by itself, the account's session limit, resets at 07:20",
		"**W4** Pause a worker: resumed as W5",
		"**To follow up on a task, continue it**", "Fork it only to start a different task from its context",
		"`djinn wish route \"<request>\" --wish-id <wish> --ask`", "`--provider watch --prompt \"<command>\"`",
		"`--restart` starts again", "`metadata.djinn.wish`", "`djinn skill list`",
		// The decisions say who took them, and what they led to.
		"- 💬 **Q01** Which store? → A: SQLite", ", by the developer; led to W2\n", "- 📌 **No CGO** (block ", "), by the lead\n",
		"`--decision Q03`", "--icon 🔒",
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

// A wish made for a request: its new lead reads the request once, on its first line, not again in the latest blocks.
// A request filed later, and the other blocks, stay.
func TestLeadBriefOnce(t *testing.T) {
	ctx := t.Context()
	c, wish, _ := source(t)
	request := "babysit https://gitlab.com/acme/shop/-/merge_requests/41"
	for _, b := range []*planv1.BlockServicePutRequest{
		{WishId: wish.GetId(), Kind: routeKindBlock, Title: "Request from “Ship the API”", Content: request + "\n"},
		{WishId: wish.GetId(), Kind: routeKindBlock, Title: "Request from “Ship the docs”", Content: "and the docs of !41"},
		{WishId: wish.GetId(), Kind: "report", Title: "The same words", Content: request},
	} {
		if _, err := c.blocks.Put(ctx, connect.NewRequest(b)); err != nil {
			t.Fatal(err)
		}
	}
	first := FirstLine("Ship the API", request)
	brief, err := LeadBrief(ctx, c.store, "", wish.GetId(), first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(brief.Moving, first+"\n\n# The wish: Ship the API") {
		t.Errorf("the request does not open the brief:\n%s", brief.Moving)
	}
	if n := strings.Count(brief.Moving, request); n != 2 || strings.Contains(brief.Moving, "Request from “Ship the API”") ||
		!strings.Contains(brief.Moving, "Request from “Ship the docs”") || !strings.Contains(brief.Moving, "### The same words") {
		t.Errorf("the request shows %d times, or a block went missing:\n%s", n, brief.Moving)
	}
	// djinn wish brief, later, still holds the request in its blocks.
	plain, err := BuildBrief(ctx, c.store, "", wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain.Moving, "Request from “Ship the API”") || plain.Stable != brief.Stable {
		t.Errorf("the plain brief lost the request:\n%s", plain.Moving)
	}
}

// serveLeads serves the plan with fake terminals and pages in home, as djinn up does.
func serveLeads(t *testing.T, home string, opts ...Option) (clients, *fakeLeads) {
	t.Helper()
	s, err := store.Open(t.Context(), "", Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	leads := &fakeLeads{}
	mux := http.NewServeMux()
	for prefix, h := range Handlers(s, append([]Option{WithLeads(leads), WithPages(NewPages(s, home, "v0-test"))}, opts...)...) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return clients{
		projects:  planv1connect.NewProjectServiceClient(srv.Client(), srv.URL),
		wishes:    planv1connect.NewWishServiceClient(srv.Client(), srv.URL),
		questions: planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL),
		blocks:    planv1connect.NewBlockServiceClient(srv.Client(), srv.URL),
		marks:     planv1connect.NewMarkServiceClient(srv.Client(), srv.URL),
		inbox:     planv1connect.NewInboxServiceClient(srv.Client(), srv.URL),
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

// TestBriefFinished: finished tasks leave the live lists; only the latest few show, the latest ended first, with who
// closed one by hand and why.
func TestBriefFinished(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Old work"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var tasks []*planv1.Task
	for i := range briefDone + 2 {
		tasks = append(tasks, &planv1.Task{
			Id: store.NewID(), WishId: wishID, Code: fmt.Sprintf("W%d", i+1), Title: "Old task", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.New(day), EndTime: timestamppb.New(day.Add(time.Duration(i) * time.Hour)),
		})
	}
	// W7 ended last; W3 was closed by hand, later than W6 ended.
	tasks[2].EndTime = timestamppb.New(day.Add(5*time.Hour + 30*time.Minute))
	tasks[2].Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: tasks[2].GetEndTime(), Note: "merged in Git"}
	for code, status := range map[string]planv1.TaskStatus{
		"W10": planv1.TaskStatus_TASK_STATUS_PENDING, "W11": planv1.TaskStatus_TASK_STATUS_WAITING,
		"W12": planv1.TaskStatus_TASK_STATUS_FAILED, "W9": planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
	} {
		tasks = append(tasks, &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: "Live", Status: status,
			CreateTime: timestamppb.New(day)})
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range tasks {
			if err := tx.Journal(actor, "test/put", task); err != nil {
				return err
			}
			if err := tx.Put(task); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, t.TempDir(), wishID)
	if err != nil {
		t.Fatal(err)
	}
	_, finished, ok := strings.Cut(brief.Moving, "## Finished: 7, the latest\n\n")
	if !ok {
		t.Fatalf("no finished section:\n%s", brief.Moving)
	}
	want := "- **W7** Old task: done\n- **W3** Old task: done, closed by the developer: merged in Git\n- **W6** Old task: done\n" +
		"- **W5** Old task: done\n- **W4** Old task: done\n"
	if !strings.HasPrefix(finished, want) {
		t.Errorf("finished =\n%s\nwant\n%s", finished, want)
	}
	// What waits, by status: cut short, failed, waiting, then planned.
	waiting, _, _ := strings.Cut(brief.Moving, "## Finished")
	last := -1
	for _, code := range []string{"**W9**", "**W12**", "**W11**", "**W10**"} {
		i := strings.Index(waiting, code)
		if i <= last {
			t.Fatalf("%s at %d, after %d: the waiting list is out of order\n%s", code, i, last, waiting)
		}
		last = i
	}
	if strings.Contains(waiting, "**W1** ") {
		t.Errorf("a finished task in the live list:\n%s", waiting)
	}
}
