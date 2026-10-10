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
	// The checks Djinn runs on the project's work, which the lead and each worker learn.
	writeSettings(t, filepath.Join(repo, SettingsFile), "setup: \"npm ci\"\n"+
		"checks { name: \"lint\" command: \"go tool task lint\" when: CHECK_WHEN_COMMIT }\n"+
		"checks { name: \"test\" command: \"go tool task test\" when: CHECK_WHEN_PUSH }\n")
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

	// Asked last, needed before the merge: it comes before the question that can wait.
	if _, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which cache?", WishId: wish.GetId(), Before: "before the merge",
	})); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildBrief(ctx, c.store, home, wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	text := brief.Text()
	if i, j := strings.Index(text, "Which cache? (before the merge)\n"), strings.Index(text, "Which store? (can wait)\n"); i < 0 || j < i {
		t.Errorf("the open questions come before X, then can wait:\n%s", text)
	}
	// The wish first, where it stands; then how to lead it: Djinn's rules, then the projects' own.
	order := []string{"# The wish: Ship the API", "## Running", "## Waiting", "## Open questions", "## Latest decisions",
		"## Latest blocks", "# Leading a wish in Djinn", "## Commands", "## The projects' rules"}
	last := -1
	for _, s := range order {
		i := strings.Index(text, s)
		if i <= last {
			t.Fatalf("%q at %d, after %d: the brief is out of order\n%s", s, i, last, text)
		}
		last = i
	}
	if !strings.HasPrefix(text, brief.Moving) || !strings.HasSuffix(text, brief.Stable) || strings.Contains(brief.Stable, "# The wish") {
		t.Errorf("the wish does not lead, or the rules hold it:\n%s", text)
	}
	for _, want := range []string{
		"Read `AGENTS.md`, `contributing.md` at its root first", "**notes**, outside Git", "**api**, a Git repository",
		"  Djinn checks this project's work before it commits a task's work, with `djinn gate run lint -- go tool task lint`; " +
			"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
			"and fixes what they find. A fresh worktree needs `npm ci` first. Djinn says so in each worker's first prompt.\n",
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
		// Everything for the developer is a question; a resolved one is resolved; blocks are for agents.
		"Everything for the developer is a question", "with none when you really do not know what to think",
		"goes in its `--context`", "ask again, a new question, only on a real doubt", "**Blocks are for agents**",
		// An azima carries one clear goal, and new work finds its azima first.
		"**An azima carries one clear goal**", "rephrase its goal (its plan file's Goal and its title",
		"open a new azima only for a will no existing one carries",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the brief lacks %q:\n%s", want, text)
		}
	}
	userHome, _ := os.UserHomeDir()
	for _, never := range []string{repo, home, userHome, "hunter2", "bob@", "s3cret", "session-42", "secretCode", "the code itself", `"raw"`,
		"--kind report", "is a go"} {
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
	if !strings.HasPrefix(res.Msg.GetText(), "# The wish: Ship the API") || !strings.HasSuffix(res.Msg.GetText(), brief.Stable) {
		t.Errorf("djinn wish brief = %q", res.Msg.GetText())
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
	makeWish := func(title string) string {
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

	// Claude: the same first message as every agent, to run djinn wish brief; a session chosen and recorded.
	id := makeWish("Ship the API")
	res := resume(id, planv1.Provider_PROVIDER_UNSPECIFIED)
	lead := res.GetWish().GetLead()
	first, err := os.ReadFile(filepath.Join(home, PagesDir, id, LeadFirstFile))
	if err != nil || string(first) != StartLine(id) || !strings.Contains(string(first), "djinn wish brief "+id) {
		t.Fatalf("first message %q, %v", first, err)
	}
	want := "claude --session-id " + lead.GetSessionId() + " '" + StartLine(id) + "'"
	if len(leads.opened) != 1 || leads.opened[0] != want+" in "+folder {
		t.Fatalf("opened %q\nwant %q", leads.opened, want+" in "+folder)
	}
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetDirectory() != folder || len(lead.GetSessionId()) != 36 ||
		!strings.Contains(res.GetNote(), "new lead") || res.GetAttached() {
		t.Errorf("resume = %v", res)
	}
	// The shell gives the agent the message as written: the quotes hold.
	if sh, err := exec.LookPath("sh"); err == nil {
		q, _ := quoteArg("linux", StartLine(id)+"it's $HOME `x`")
		out, err := exec.Command(sh, "-c", "printf %s "+q).Output()
		if err != nil || string(out) != StartLine(id)+"it's $HOME `x`" {
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

	// Codex gets the same first message; Djinn cannot know its session.
	{
		id := makeWish("Lead with Codex")
		res := resume(id, planv1.Provider_PROVIDER_CODEX)
		got := leads.opened[len(leads.opened)-1]
		if got != "codex '"+StartLine(id)+"' in "+folder || res.GetWish().GetLead() != nil || res.GetNote() == "" {
			t.Errorf("codex: opened %q, resume %v", got, res)
		}
	}
	// Antigravity starts from the brief; its folder becomes the wish's lead, resumed with agy --continue.
	{
		id := makeWish("Lead with Antigravity")
		res := resume(id, planv1.Provider_PROVIDER_ANTIGRAVITY)
		got := leads.opened[len(leads.opened)-1]
		lead := res.GetWish().GetLead()
		if got != "agy -i '"+StartLine(id)+"' in "+folder || lead.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY ||
			lead.GetSessionId() != "" || lead.GetDirectory() != folder || res.GetNote() == "" {
			t.Errorf("antigravity: opened %q, resume %v", got, res)
		}
	}
	// The fake agent has no terminal.
	if _, err := c.wishes.Resume(ctx, connect.NewRequest(&planv1.WishServiceResumeRequest{
		WishId: makeWish("Fake lead"), Provider: planv1.Provider_PROVIDER_FAKE,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a fake lead: %v, want invalid_argument", err)
	}
}

func TestLeadLine(t *testing.T) {
	data := `C:\Users\Ann Lee\AppData\Roaming\djinn\wishes\w1`
	file := data + `\` + LeadFirstFile
	claude := &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: "s1"}
	start := StartLine("w1")

	// Every agent gets the same message, as one argument.
	for p, want := range map[planv1.Provider]string{
		planv1.Provider_PROVIDER_CLAUDE: `claude --session-id s1 "` + start + `"`,
		planv1.Provider_PROVIDER_CODEX:  `codex "` + start + `"`, planv1.Provider_PROVIDER_ANTIGRAVITY: `agy -i "` + start + `"`,
	} {
		lead := &planv1.Lead{Provider: p, SessionId: claude.GetSessionId()}
		if line, err := leadLine("windows", lead, data, start); err != nil || line != want {
			t.Errorf("%s: %q, %v; want %q", p, line, err, want)
		}
	}
	// cmd.exe carries no line break: a routed request first, the message names its file, and the folder is allowed.
	routed := "The developer's request: babysit !41\n\n" + start
	line, err := leadLine("windows", claude, data, routed)
	want := `claude --session-id s1 --add-dir "` + data + `" "Read ` + file + `, written by Djinn, and do what it says."`
	if err != nil || line != want {
		t.Errorf("windows line\n%s\nwant\n%s", line, want)
	}
	codex, err := leadLine("windows", &planv1.Lead{Provider: planv1.Provider_PROVIDER_CODEX}, data, routed)
	if err != nil || !strings.HasPrefix(codex, `codex "Read `+file) {
		t.Errorf("windows codex line %q, %v", codex, err)
	}
	// A data folder cmd.exe cannot quote is refused, with why.
	if _, err := leadLine("windows", claude, `C:\100%`, routed); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a %% in the data folder: %v", err)
	}
	// A message too long for one argument is read from its file, on every system.
	if line, err := leadLine("linux", claude, "/d", strings.Repeat("x", maxFirstArg+1)); err != nil ||
		!strings.HasSuffix(line, "--add-dir '/d' 'Read /d/lead-first.md, written by Djinn, and do what it says.'") {
		t.Errorf("a long message: %q, %v", line, err)
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
	_, finished, ok := strings.Cut(brief.Moving, "## Finished: 8, the latest\n\n")
	if !ok {
		t.Fatalf("no finished section:\n%s", brief.Moving)
	}
	want := "- **W7** Old task: done\n- **W3** Old task: done, closed by the developer: merged in Git\n- **W6** Old task: done\n" +
		"- **W5** Old task: done\n- **W4** Old task: done\n"
	if !strings.HasPrefix(finished, want) {
		t.Errorf("finished =\n%s\nwant\n%s", finished, want)
	}
	// What waits, by status: failed, waiting, then planned. W9, cut short and not resumed by Djinn, is history.
	waiting, _, _ := strings.Cut(brief.Moving, "## Finished")
	if strings.Contains(waiting, "**W9**") {
		t.Errorf("a task cut short for good waits:\n%s", brief.Moving)
	}
	last := -1
	for _, code := range []string{"**W12**", "**W11**", "**W10**"} {
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

// TestBriefWorkStands: the brief says where each finished task's work stands on its way into the wish's integration
// branch, and why a task waits for a dependency's work to be committed.
func TestBriefWorkStands(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Integrates"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	day := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	in := func(state planv1.IntegrationState, reason, by string) *planv1.TaskIntegration {
		return &planv1.TaskIntegration{State: state, Branch: "feat/x", Sha: "1a2b3c4d5e6f", Reason: reason, CorrectedBy: by}
	}
	var tasks []*planv1.Task
	for i, integration := range []*planv1.TaskIntegration{
		nil,
		in(planv1.IntegrationState_INTEGRATION_STATE_PENDING, "", ""),
		in(planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, "", ""),
		in(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, "W4 conflicts with feat/x in a.go", "W9"),
		in(planv1.IntegrationState_INTEGRATION_STATE_RED, "test exited 1", ""),
	} {
		tasks = append(tasks, &planv1.Task{
			Id: store.NewID(), WishId: wishID, Code: fmt.Sprintf("W%d", i+1), Title: "Work", Status: planv1.TaskStatus_TASK_STATUS_DONE,
			CreateTime: timestamppb.New(day), EndTime: timestamppb.New(day.Add(time.Duration(5-i) * time.Minute)), Integration: integration,
		})
	}
	tasks = append(tasks, &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W6", Title: "Next", Status: planv1.TaskStatus_TASK_STATUS_PENDING,
		Scheduled: true, DependsOn: []string{tasks[1].GetId()}, WaitReason: "waits for W2 to be committed", CreateTime: timestamppb.New(day)})
	tasks = append(tasks, &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W7", Title: "Work", Status: planv1.TaskStatus_TASK_STATUS_DONE,
		CreateTime: timestamppb.New(day), EndTime: timestamppb.New(day.Add(6 * time.Minute)), Integration: &planv1.TaskIntegration{
			State: planv1.IntegrationState_INTEGRATION_STATE_UNCOMMITTED, Branch: "feat/x", ReviewedBy: tasks[1].GetId(),
		}})
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
	for _, want := range []string{
		"- **W6** Next: planned, waits for W2 to be committed\n",
		"- **W7** Work: changes not committed, not merged, reviewed by W2\n" +
			"- **W1** Work: done\n" +
			"- **W2** Work: done, waiting to be committed\n" +
			"- **W3** Work: committed into feat/x as 1a2b3c4d\n" +
			"- **W4** Work: conflict, not committed: W4 conflicts with feat/x in a.go, corrected by W9\n",
	} {
		if !strings.Contains(brief.Moving, want) {
			t.Errorf("the brief lacks\n%s\nin\n%s", want, brief.Moving)
		}
	}
}

// TestDescribe: a wish's description is a few lines in place of what it had; the title stands for an empty one, so
// writing the title, or nothing, takes it back there.
func TestDescribe(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	id := c.wish(t)
	describe := func(text string) (*planv1.Wish, error) {
		res, err := c.wishes.Describe(ctx, connect.NewRequest(&planv1.WishServiceDescribeRequest{WishId: id, Text: text}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetWish(), nil
	}
	text := "Ship the API to the shop.\nScope: the store, not the web."
	if wish, err := describe("  " + text + "\n"); err != nil || wish.GetDescription() != text {
		t.Fatalf("describe = %v, %v", wish, err)
	}
	listed, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil || listed.Msg.GetWishes()[0].GetDescription() != text {
		t.Errorf("listed = %v, %v", listed, err)
	}
	title := listed.Msg.GetWishes()[0].GetTitle()
	for _, back := range []string{title, " ", ""} {
		if wish, err := describe(back); err != nil || wish.GetDescription() != "" {
			t.Errorf("describe(%q) = %v, %v; want the title back", back, wish, err)
		}
	}
	if _, err := c.wishes.Describe(ctx, connect.NewRequest(&planv1.WishServiceDescribeRequest{
		WishId: store.NewID(), Text: text,
	})); code(err) != connect.CodeNotFound {
		t.Errorf("describe an unknown wish: %v", err)
	}
}

// TestBriefOrder: djinn wish brief says where the wish stands on its own, in this order: the description, the azimas,
// what runs and waits, the open questions, the latest decisions and blocks, the last lead and when a lead last
// acted; then how to lead it.
func TestBriefOrder(t *testing.T) {
	ctx := t.Context()
	c, wish, _ := source(t)
	id := wish.GetId()
	description := "Ship the API to the shop.\nScope: the store, not the web."
	if _, err := c.wishes.Describe(ctx, connect.NewRequest(&planv1.WishServiceDescribeRequest{WishId: id, Text: description})); err != nil {
		t.Fatal(err)
	}
	azima := &planv1.Task{Id: store.NewID(), WishId: id, Code: "T01", Title: "The store", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now()}
	part := &planv1.Task{Id: store.NewID(), WishId: id, Code: "W2", Title: "Back the store up", PartOf: azima.GetId(),
		Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now()}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range []*planv1.Task{azima, part} {
			if err := tx.Journal("test", "test/put", task); err != nil {
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
	if _, err := setLead(t, c, &planv1.WishServiceSetLeadRequest{WishId: id, SessionId: session}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: id, Kind: "report", Title: "Where the store stands", Content: "Written, not backed up.",
	})); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildBrief(ctx, c.store, t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	text := brief.Text()
	last := -1
	for _, s := range []string{"# The wish: Ship the API\n\n" + description + "\n\n- Identifier:", "## Azimas", "**T01** The store: ready",
		"## Running", "**W1** Write the store", "## Waiting", "**W2** Back the store up (part of T01)", "## Open questions",
		"## Latest decisions", "## Latest blocks", "### Where the store stands (report)", "## The last lead",
		"- claude, session `" + session + "`, recorded ", "- A lead last acted ", "`djinn block put`",
		"# Leading a wish in Djinn", "**Start from the brief.**", "`djinn wish describe <wish> --text", "## The projects' rules"} {
		i := strings.Index(text, s)
		if i <= last {
			t.Fatalf("%q at %d, after %d: the brief is out of order\n%s", s, i, last, text)
		}
		last = i
	}
	// A worker's block is not the lead acting.
	tasks, err := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": id, "code": "W1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("W1: %v, %v", tasks, err)
	}
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{
		WishId: id, Kind: "report", Title: "From W1", Content: "Done.", TaskId: tasks[0].GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	if again, err := BuildBrief(ctx, c.store, t.TempDir(), id); err != nil || !strings.Contains(again.Moving, "`djinn block put`.\n") {
		t.Errorf("the lead's last act moved with a worker's block (%v):\n%s", err, again.Moving)
	}
	// A wish that never had a lead says nothing of one.
	if bare, err := BuildBrief(ctx, c.store, "", c.wish(t)); err != nil || strings.Contains(bare.Moving, "## The last lead") {
		t.Errorf("a wish without a lead (%v):\n%s", err, bare.Moving)
	}
}

// TestBriefRunningTaskModel: a running task's line includes the model when set.
func TestBriefRunningTaskModel(t *testing.T) {
	ctx := t.Context()
	c, wish, _ := source(t)
	tasks, err := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": wish.GetId(), "code": "W1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("W1: %v, %v", tasks, err)
	}
	task := tasks[0]
	task.Model = "claude-sonnet-4-5"
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/put", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, t.TempDir(), wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.Moving, "(claude · claude-sonnet-4-5, since ") {
		t.Errorf("brief = %s\nwant to contain (claude · claude-sonnet-4-5, since ", brief.Moving)
	}
}
