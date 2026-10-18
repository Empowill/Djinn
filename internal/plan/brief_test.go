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
	"github.com/empowill/djinn/lead/notes"
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
		"## Latest blocks", "# Leading in Djinn", "## Commands", "## The projects' rules"}
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
		"  Djinn checks this project's work before it merges a task's work, with `djinn gate run lint -- go tool task lint`; " +
			"before it pushes, with `djinn gate run test -- go tool task test`. A worker runs the commit checks before it ends, " +
			"and fixes what they find, then only the tests that cover what it changed; do not run the push checks (`djinn gate run test -- go tool task test`): " +
			"Djinn runs them before pushing. A fresh worktree needs `npm ci` first. Djinn says so in each worker's first prompt.\n",
		"**Workers start from a short prompt**, five to twenty lines of Markdown", "**Gain**, the outcome in one sentence",
		"When writing a worker's prompt, ask for the commit checks and the targeted tests, never \"run the whole test suite\"; the push checks are Djinn's.",
		wish.GetId(), "**Q01** Which store? → A: SQLite", "**W1** Write the store (claude", "**W2** Write the docs: failed",
		"### No CGO (decision)", "In api/internal", "https://example.com/acme.git",
		"`djinn wish sync <wish>`", "republish that file as it is, in one call",
		"**W3** Reopen the leads: resuming by itself, the account's session limit, resets at 07:20",
		"**W4** Pause a worker: resumed as W5",
		"**To follow up on a task, continue it**", "Fork it only to start a different task from its context",
		"`djinn task update <task>`",
		"`djinn wish route \"<request>\" --wish-id <wish> --ask`", "`--provider watch --prompt \"<command>\"`",
		"`--restart` starts again", "`metadata.djinn.wish`", "`djinn skill list`",
		// The decisions say who took them, and what they led to.
		"- 💬 **Q01** Which store? → A: SQLite", ", by the developer; led to W2\n", "- 📌 **No CGO** (block ", "), by the lead\n",
		"`--decision Q03`", "--icon 🔐",
		// Everything for the developer is a question; a resolved one is resolved; blocks are for agents.
		"Everything for the developer is a question", "with none when you really do not know what to think",
		"goes in its `--context`", "ask again, a new question, only on a real doubt", "**Blocks are for agents**",
		"Every move only the developer can make (push a tag, merge or open a pull request, create a release",
		"--move",
		"**Check every task that ends.**",
		"Read its final note (under its line in your watch's digest, or `djinn task watch <task> --last 5`)",
		// An azima carries one clear goal, and new work finds its azima first.
		"**An azima carries one clear goal**", "rephrase its goal (its plan file's Goal and its title",
		"open a new azima only for a will no existing one carries",
		// The lead owns the wish's DAG: it reads the open work and its overlaps before it spawns.
		"**You own the wish's DAG (W230).**", "`djinn task list --wish-id <wish> --open --short`",
		"`djinn task overlaps <wish>`", "never run beside it",
		"never spawn while the wish's integration points at another branch (a quarantine",
		"W399 brought the replay sink's node_modules link into feat/wails-go on 11/10",
		"A conflict or a red merge is a signal to re-sequence, not only to correct",
		// When Djinn misbehaves, the lead stops the damage alone and may revert a faulty merge.
		"**When Djinn misbehaves, stop the damage.**",
		"redirect the wish's integration to another branch, stop workers, withdraw useless questions",
		"revert a faulty merge on the integration branch, telling the developer at once",
		"every other commit on the integration branch waits for the developer's agreement, asked first (`djinn question ask`)",
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
	projectsDir := filepath.Join(home, "agy-projects")
	prev := AgyProjectsDir
	AgyProjectsDir = func() (string, error) { return projectsDir, nil }
	t.Cleanup(func() { AgyProjectsDir = prev })
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
		lead:      planv1connect.NewLeadServiceClient(srv.Client(), srv.URL),
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
	resume := func(p planv1.Provider) *planv1.LeadServiceResumeResponse {
		t.Helper()
		res, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{Provider: p}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	// Claude: the same first message as every agent, to run djinn brief; a session chosen and recorded.
	_ = makeWish("Ship the API")
	res := resume(planv1.Provider_PROVIDER_UNSPECIFIED)
	lead := res.GetLead()
	first, err := os.ReadFile(filepath.Join(home, "lead", LeadFirstFile))
	if err != nil || string(first) != StartLine() || !strings.Contains(string(first), "djinn brief") {
		t.Fatalf("first message %q, %v", first, err)
	}
	want := "claude --session-id " + lead.GetSessionId() + " --allowedTools '" + claudeLeadTools + "' '" + StartLine() + "'"
	if len(leads.opened) != 1 || leads.opened[0] != want+" in "+folder {
		t.Fatalf("opened %q\nwant %q", leads.opened, want+" in "+folder)
	}
	if lead.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || lead.GetDirectory() != folder || len(lead.GetSessionId()) != 36 ||
		!strings.Contains(res.GetNote(), "new lead") || res.GetAttached() {
		t.Errorf("resume = %v", res)
	}
	// The shell gives the agent the message as written: the quotes hold.
	if sh, err := exec.LookPath("sh"); err == nil {
		q, _ := quoteArg("linux", StartLine()+"it's $HOME `x`")
		out, err := exec.Command(sh, "-c", "printf %s "+q).Output()
		if err != nil || string(out) != StartLine()+"it's $HOME `x`" {
			t.Errorf("through sh: %q, %v", out, err)
		}
	}
	// The lead runs: a second resume attaches to it, with nothing to say.
	if res = resume(planv1.Provider_PROVIDER_UNSPECIFIED); !res.GetAttached() || res.GetNote() != "" || len(leads.opened) != 1 {
		t.Errorf("second resume = %v", res)
	}
	// Once it has exited, the recorded session is resumed.
	delete(leads.running, LeadTerminal)
	if res = resume(planv1.Provider_PROVIDER_UNSPECIFIED); leads.opened[1] != "claude --resume "+lead.GetSessionId()+" --allowedTools '"+claudeLeadTools+"' in "+folder {
		t.Errorf("resume after exit = %v, opened %q", res, leads.opened)
	}

	// Codex gets the same first message; Djinn cannot know its session.
	{
		delete(leads.running, LeadTerminal)
		res := resume(planv1.Provider_PROVIDER_CODEX)
		got := leads.opened[len(leads.opened)-1]
		if got != "codex '"+StartLine()+"' in "+folder || res.GetLead().GetSessionId() != lead.GetSessionId() || res.GetNote() == "" {
			t.Errorf("codex: opened %q, resume %v", got, res)
		}
	}
	// Antigravity starts from the brief; its folder becomes the lead, resumed with agy --continue.
	{
		delete(leads.running, LeadTerminal)
		res := resume(planv1.Provider_PROVIDER_ANTIGRAVITY)
		got := leads.opened[len(leads.opened)-1]
		lead := res.GetLead()
		if got != "agy -i '"+StartLine()+"' --project "+LeadAgyProjectID+" in "+folder || lead.GetProvider() != planv1.Provider_PROVIDER_ANTIGRAVITY ||
			lead.GetSessionId() != "" || lead.GetDirectory() != folder || res.GetNote() == "" {
			t.Errorf("antigravity: opened %q, resume %v", got, res)
		}
	}
	// The fake agent has no terminal.
	if _, err := c.lead.Resume(ctx, connect.NewRequest(&planv1.LeadServiceResumeRequest{
		Provider: planv1.Provider_PROVIDER_FAKE,
	})); code(err) != connect.CodeInvalidArgument {
		t.Errorf("a fake lead: %v, want invalid_argument", err)
	}
}

func TestLeadLine(t *testing.T) {
	data := `C:\Users\Ann Lee\AppData\Roaming\djinn\lead`
	file := data + `\` + LeadFirstFile
	claude := &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: "s1"}
	start := StartLine()

	// Every agent gets the same message, as one argument.
	for p, want := range map[planv1.Provider]string{
		planv1.Provider_PROVIDER_CLAUDE:      `claude --session-id s1 --allowedTools "` + claudeLeadTools + `" "` + start + `"`,
		planv1.Provider_PROVIDER_CODEX:       `codex "` + start + `"`,
		planv1.Provider_PROVIDER_ANTIGRAVITY: `agy -i "` + start + `" --project ` + LeadAgyProjectID,
	} {
		lead := &planv1.Lead{Provider: p, SessionId: claude.GetSessionId()}
		if line, err := leadLine("windows", lead, data, start); err != nil || line != want {
			t.Errorf("%s: %q, %v; want %q", p, line, err, want)
		}
	}
	// cmd.exe carries no line break: a routed request first, the message names its file, and the folder is allowed.
	routed := "The developer's request: babysit !41\n\n" + start
	line, err := leadLine("windows", claude, data, routed)
	want := `claude --session-id s1 --allowedTools "` + claudeLeadTools + `" --add-dir "` + data + `" "Read ` + file + `, written by Djinn, and do what it says."`
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

// TestBriefCancelled: the brief lists a cancelled task in its finished list with who and why.
func TestBriefCancelled(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Cancelled work"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	task := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Useless task",
		Status:     planv1.TaskStatus_TASK_STATUS_CANCELLED,
		CreateTime: timestamppb.New(day), EndTime: timestamppb.New(day.Add(time.Hour)),
		Cancelled: &planv1.Cancellation{Actor: planv1.Closer_CLOSER_LEAD, CreateTime: timestamppb.New(day.Add(time.Hour)), Note: "no longer needed"},
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, t.TempDir(), wishID)
	if err != nil {
		t.Fatal(err)
	}
	want := "- **W1** Useless task: cancelled, by the lead: no longer needed\n"
	if !strings.Contains(brief.Moving, want) {
		t.Errorf("brief does not list cancelled task:\n%s\nwant:\n%s", brief.Moving, want)
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
			"- **W2** Work: waiting to be committed\n" +
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

// TestWishIcon: a wish's icon is a name of the bank, set in place of what it had and taken back to its mark by an empty
// one; a name outside the bank is refused. Until it has one, its brief asks the Djinn to choose it.
func TestWishIcon(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	id := c.wish(t)
	brief := func() string {
		res, err := c.wishes.Brief(ctx, connect.NewRequest(&planv1.WishServiceBriefRequest{WishId: id}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetText()
	}
	ask := "This wish has no icon in the window's menu yet"
	if !strings.Contains(brief(), ask) || !strings.Contains(brief(), "djinn wish icon "+id+" --name <name>") {
		t.Errorf("the brief of a wish without an icon does not ask for one:\n%s", brief())
	}
	icon := func(name string) (*planv1.Wish, error) {
		res, err := c.wishes.Icon(ctx, connect.NewRequest(&planv1.WishServiceIconRequest{WishId: id, Name: name}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetWish(), nil
	}
	if wish, err := icon(" Lamp "); err != nil || wish.GetIcon() != "lamp" {
		t.Fatalf("icon = %v, %v", wish, err)
	}
	listed, err := c.wishes.List(ctx, connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil || listed.Msg.GetWishes()[0].GetIcon() != "lamp" {
		t.Errorf("listed = %v, %v", listed, err)
	}
	if strings.Contains(brief(), ask) {
		t.Error("the brief asks for an icon the wish has")
	}
	if _, err := icon("hand"); code(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "djinn wish icons") {
		t.Errorf("an icon outside the bank: %v", err)
	}
	if wish, err := icon(""); err != nil || wish.GetIcon() != "" {
		t.Errorf("icon(\"\") = %v, %v; want the mark back", wish, err)
	}
	res, err := c.wishes.Icons(ctx, connect.NewRequest(&planv1.WishServiceIconsRequest{Search: "theme"}))
	if err != nil || len(res.Msg.GetIcons()) != 1 || res.Msg.GetIcons()[0].GetName() != "palette" ||
		res.Msg.GetIcons()[0].GetWordsFr() == "" {
		t.Errorf("icons --search theme = %v, %v", res, err)
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
		"## Latest decisions", "## Latest blocks", "### Where the store stands (report)",
		"# Leading in Djinn", "**Start from the brief.**", "`djinn wish describe <wish> --text", "## The projects' rules"} {
		i := strings.Index(text, s)
		if i <= last {
			t.Fatalf("%q at %d, after %d: the brief is out of order\n%s", s, i, last, text)
		}
		last = i
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

// TestBriefInstallQuestionsRule: the lead's rule in briefRules reads the install_questions setting.
func TestBriefInstallQuestionsRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		iq   planv1.InstallQuestions
		want string
	}{
		{
			name: "always",
			iq:   planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS,
			want: "grant a right, install and restart (djinn: always)",
		},
		{
			name: "when_needed",
			iq:   planv1.InstallQuestions_INSTALL_QUESTIONS_WHEN_NEEDED,
			want: "grant a right, install and restart (djinn: when_needed)",
		},
		{
			name: "never",
			iq:   planv1.InstallQuestions_INSTALL_QUESTIONS_NEVER,
			want: "grant a right, install and restart (djinn: never)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			repo := t.TempDir()
			writeSettings(t, filepath.Join(repo, SettingsFile), fmt.Sprintf("install_questions: %s\n", InstallQuestionsWord(tc.iq)))
			proj := &planv1.Project{Name: "djinn", Directory: repo}
			rules := briefRules([]*planv1.Project{proj}, home)
			if !strings.Contains(rules, tc.want) {
				t.Errorf("briefRules(%v) lacks %q:\n%s", tc.iq, tc.want, rules)
			}
		})
	}

	// Integration test with project settings
	ctx := t.Context()
	c, wish, repo := source(t)
	home := t.TempDir()
	writeSettings(t, filepath.Join(repo, SettingsFile), "install_questions: when_needed\n")

	brief, err := BuildBrief(ctx, c.store, home, wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	wantRule := "grant a right, install and restart (api: when_needed, notes: always)"
	if !strings.Contains(brief.Stable, wantRule) {
		t.Errorf("brief.Stable lacks %q with when_needed settings:\n%s", wantRule, brief.Stable)
	}
}

// TestBriefConflictsRule: the lead owns the wish's DAG (W230): before a spawn it reads the open work and the files it
// touches; work on the same files is bundled or ordered, never side by side; big work is a chain, not a fan.
func TestBriefConflictsRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**You own the wish's DAG (W230).** Whether two workers collide is your plan's doing, not Djinn's.",
		"`djinn task list --wish-id <wish> --open --short`", "`djinn task overlaps <wish>`",
		"Work on files an open task touches is never run beside it: send it to that task (`djinn task update " +
			"<task> --prompt` while planned, `djinn task send <task> \"…\"` while it runs), or spawn it `--after` that task.",
		"Give `--write-scopes` when you know the paths",
		"Big work is a chain of small steps, each `--after` the one before",
		"never chain tasks in a naive sequential line (`W1 -> W2 -> W3 -> W4`) just to be safe",
		"W174 `--after W160`",
		"A task starts from the integrated branch: never spawn while the wish's integration points at another branch (a quarantine",
		"W399 brought the replay sink's node_modules link into feat/wails-go on 11/10",
		"A conflict or a red merge is a signal to re-sequence, not only to correct",
		"`--open` for what runs and what comes",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
	if strings.Contains(rules, "Keep the graph free of conflicts") {
		t.Error("briefRules still holds the rule W230 replaced")
	}
}

// TestBriefOperationalDecisionsRule: Djinn's operational decisions go to the lead as events, not questions.
func TestBriefOperationalDecisionsRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**Operational decisions are yours (Decision Q196).**",
		"Djinn's operational questions",
		"go to you as an event, never as a question to the developer",
		"retry a correction, drop the work (`djinn task done <task> --drop` or `djinn task cancel`), re-sequence the graph (`djinn task depend`), or leave it waiting",
		"Ask the developer only for moves only a person can make (a tag, a PR, a release, a right) or a real choice",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefDjinnRule (Q259 C): the lead is the Djinn, first among the rules; workers never bear the name.
func TestBriefDjinnRule(t *testing.T) {
	rules := briefRules(nil, "")
	want := "- **You are the Djinn.** In Djinn, the lead is called the Djinn, and you alone bear that name: the one of " +
		"your kind who lives in the lamp, the developer's single voice for every wish. Workers are your hands, never " +
		"Djinns; the app is \"Djinn\", you are \"the Djinn\". Speak of yourself that way in questions and blocks " +
		"(\"the Djinn recommends…\")."
	if i := strings.Index(rules, "\n- "); i < 0 || !strings.HasPrefix(rules[i+1:], want) {
		t.Errorf("briefRules does not start its rules with %q:\n%s", want, rules)
	}
}

// TestBriefUnreadTerminalRule (W234): the developer does not read the lead's terminal, second among the rules, and
// says why: every question, doubt or choice goes into a question in the window, statements included, withdrawn once a
// task or Djinn does what it asked; the terminal reply stays one short line at most. The brief says it at its start
// and at its end, and the first message of every lead too.
func TestBriefUnreadTerminalRule(t *testing.T) {
	rules := briefRules(nil, "")
	bullets := strings.Split(rules, "\n- ")
	if len(bullets) < 3 || !strings.HasPrefix(bullets[2], "**The developer does not read your terminal.**") {
		t.Errorf("the second rule is not the terminal's:\n%s", rules)
	}
	for _, want := range []string{
		"costs output tokens for nothing",
		"Every question, doubt or choice you have goes into a question (`djinn question ask`) in the wish concerned, " +
			"never only into your text; a fact or a decision for the next leads and workers goes into a block.",
		"Your terminal reply stays one short line at most, what you did and what you wait for: no recap, no summary, no \"done\"",
		"Everything for the developer goes through questions in the window",
		"statements included",
		"\"OK, nothing to do\" as an option",
		"A real wait on the developer is a `--move` question that says so plainly",
		"Never two questions for one gesture",
		"withdraw a question (`djinn question withdraw`) the moment a task or Djinn does what it asked",
		"A line the developer types in your terminal is a request like any other: answer it in the window, never in the terminal",
		"Withdraw the open questions its work made needless",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
	for _, gone := range []string{"Say nothing in your terminal, ever", "A turn ends with no text at all", "naming the questions you asked"} {
		if strings.Contains(rules, gone) {
			t.Errorf("briefRules still holds %q:\n%s", gone, rules)
		}
	}
	if !strings.Contains(StartLine(), UnreadTerminalRule) || strings.Contains(StartLine(), "write nothing in this terminal") {
		t.Errorf("StartLine lacks the rule on the terminal: %s", StartLine())
	}
}

// TestDrivenRulesUnreadText (W234): the driven lead's first rule says the developer reads none of its text: every
// question, doubt or choice goes into a question, its last text one short line for the journal.
func TestDrivenRulesUnreadText(t *testing.T) {
	rules := DrivenRules(nil, "")
	bullets := strings.Split(rules, "\n- ")
	if len(bullets) < 2 || !strings.HasPrefix(bullets[1], "**The developer reads none of your text.**") {
		t.Fatalf("the driven lead's first rule is not the one on its text:\n%s", rules)
	}
	for _, want := range []string{
		"No terminal shows this session",
		"Every question, doubt or choice you have goes into a question (`djinn question ask`) in the wish concerned, " +
			"never only into your text; a fact or a decision for the next leads and workers goes into a block.",
		"End your turn with one short line for the journal",
	} {
		if !strings.Contains(bullets[1], want) {
			t.Errorf("the driven lead's first rule lacks %q:\n%s", want, bullets[1])
		}
	}
}

// TestBriefQuestionEmojiRule: every question's title starts with the emoji of its subject, the same as its --icon.
func TestBriefQuestionEmojiRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**Every question's title starts with its emoji.** One emoji that tells its subject at a glance, then a space and the title",
		"🚀 a merge or a release, 🔎 a review, 🔐 a right or a permission, 🧪 a check or a test, 📦 a build, " +
			"🌿 a branch, 🧭 an organisation choice, 🪵 logs, ⚠️ an incident",
		"The same emoji goes in `--icon`",
		"`djinn question ask \"🔐 <question>\" <wish>",
		"--icon 🔐` (the emoji its title starts with)",
		"its verb first after its emoji (\"🔎 Review MR !123",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefMovesRule: the lead keeps the developer's moves, each a call to action with the links where they act;
// a pull or merge request a task opens is a review move at once.
func TestBriefMovesRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**You keep the developer's moves.**",
		"Each such move is one `--move` question whose title is a call to action, its verb first after its emoji",
		"clickable links to where they act: the PR or MR, the CI run, the ticket, the file and line",
		"every command to run in a code block, to copy as it is",
		"When a task opens a pull or merge request, ask \"Review it and take it out of draft\" with its link at once",
		"marking it ready is the developer's",
		"No move is left implied in a note, a block or your terminal",
		"withdraw a move the moment it is done or superseded",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefQuestionShapeRule (W575): the lead reads the shape a question's context keeps, and where its example is.
func TestBriefQuestionShapeRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"- **A question reads at a glance (W575).** " + QuestionShape,
		"every command the developer must run, in one fenced code block, in run order",
		"No command, flag, path or hash inside a sentence",
		"`djinn lead notes readable-questions`",
		"- `readable-questions`: ",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefNoCheckAskedRule (Q246): a check is never asked of the developer: a task or a watcher runs it; a question
// is for a decision or a person's move only, its command in a code block at the top of the context.
func TestBriefNoCheckAskedRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**Never ask the developer to check (Q246).**",
		"A check, a measure, a log to read, a CI result to look at, a command to run is never asked of the developer",
		"Spawn a task that runs it and investigates what it finds, or let a watcher report it",
		"a CI result comes from the PR's or MR's babysit watcher",
		"When a worker's final note says what remains to verify, spawn that task or watcher",
		"A question to the developer is only for a decision or a move only a person can make",
		"the exact command goes in the code block at the top of its `--context`, never in a sentence",
		"what it leaves to verify (something it did not verify, a failing test it calls unrelated) goes to a task",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefUnderstandAnswerRule:a lead starts no work from an answer it does not fully understand; it asks again.
func TestBriefUnderstandAnswerRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**Understand an answer before you act on it.**",
		"never start work or spawn a task from an answer while any part of it (the choice, its note, how they fit together) is unclear to you",
		"Ask again, a new question that quotes what you did not understand, even on a question they answered",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

// TestBriefCancelRule: the lead cancels a task no longer useful with djinn task cancel, never marks it done.
func TestBriefCancelRule(t *testing.T) {
	rules := briefRules(nil, "")
	want := "a task no longer useful is cancelled, never marked done: `djinn task cancel <task> --note \"…\"` " +
		"stops its worker, removes its worktree (its branch stays), drops its work from integration and takes it out of " +
		"the window's lists"
	if !strings.Contains(rules, want) {
		t.Errorf("briefRules lacks %q:\n%s", want, rules)
	}
	// W370: a task ended on purpose is cancelled, never left stopped; stop is only to continue at once.
	want = "a task you end on purpose (a watcher no longer needed, a task made useless) is cancelled, never left stopped or " +
		"paused, which the window lists as waiting: `djinn task stop` only for a worker you continue at once"
	if !strings.Contains(rules, want) {
		t.Errorf("briefRules lacks %q:\n%s", want, rules)
	}
}

// TestBriefRefusedCallsRule (Q273): a worker's refused calls reach the lead alone, who decides; a new right is asked
// of the developer explicitly, in a --move question.
func TestBriefRefusedCallsRule(t *testing.T) {
	rules := briefRules(nil, "")
	for _, want := range []string{
		"**Refused calls are yours, rights stay explicit (Q273).**",
		"names the calls its permissions refused and the right that would allow them; the developer never sees them",
		"nor a failed worker: the window never counts one as waiting for them, it is yours alone.",
		"ask for it in one `--move` question naming exactly that right, the command that grants it, and why.",
		"continue the task otherwise (`djinn task continue`)",
		"close it as seen (`djinn task done <task> --note \"…\"`)",
		"spawn it again where a right the wish or project grants already covers them",
		"a done task left with refused calls holds its wish's grant",
		"Never take a broad right by default",
		"ask for it in one `--move` question naming exactly that right, the command that grants it, and why",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q:\n%s", want, rules)
		}
	}
}

func TestGlobalBrief(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	home := t.TempDir()

	repo := filepath.Join(t.TempDir(), "djinn")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, filepath.Join(repo, SettingsFile), "install_questions: never\n")
	if _, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: repo})); err != nil {
		t.Fatal(err)
	}

	made1, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Active Wish 1"}))
	if err != nil {
		t.Fatal(err)
	}
	wish1ID := made1.Msg.GetWish().GetId()
	if _, err := c.wishes.Describe(ctx, connect.NewRequest(&planv1.WishServiceDescribeRequest{WishId: wish1ID, Text: "First active wish"})); err != nil {
		t.Fatal(err)
	}

	made2, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Active Wish 2"}))
	if err != nil {
		t.Fatal(err)
	}
	wish2ID := made2.Msg.GetWish().GetId()
	if _, err := c.wishes.Describe(ctx, connect.NewRequest(&planv1.WishServiceDescribeRequest{WishId: wish2ID, Text: "Second active wish"})); err != nil {
		t.Fatal(err)
	}

	made3, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Paused Wish"}))
	if err != nil {
		t.Fatal(err)
	}
	wish3ID := made3.Msg.GetWish().GetId()
	if _, err := c.wishes.Pause(ctx, connect.NewRequest(&planv1.WishServicePauseRequest{WishId: wish3ID})); err != nil {
		t.Fatal(err)
	}

	made4, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Granted Wish"}))
	if err != nil {
		t.Fatal(err)
	}
	wish4ID := made4.Msg.GetWish().GetId()
	if _, err := c.wishes.Grant(ctx, connect.NewRequest(&planv1.WishServiceGrantRequest{WishId: wish4ID})); err != nil {
		t.Fatal(err)
	}

	if _, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which oil?", Options: []string{"Olive", "Paraffin"}, WishId: wish1ID,
	})); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildGlobalBrief(ctx, c.store, home)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(brief, "# The wish: Active Wish 1") {
		t.Errorf("expected Active Wish 1 in global brief:\n%s", brief)
	}
	if !strings.Contains(brief, "- Identifier: `"+wish1ID+"`") {
		t.Errorf("expected wish1ID in global brief:\n%s", brief)
	}
	if !strings.Contains(brief, "## Open questions") || !strings.Contains(brief, "Which oil?") {
		t.Errorf("expected open question in global brief:\n%s", brief)
	}
	if !strings.Contains(brief, "# The wish: Active Wish 2") {
		t.Errorf("expected Active Wish 2 in global brief:\n%s", brief)
	}
	if !strings.Contains(brief, "- Identifier: `"+wish2ID+"`") {
		t.Errorf("expected wish2ID in global brief:\n%s", brief)
	}

	wantPaused := fmt.Sprintf("- **Paused Wish** (`%s`): paused: `djinn wish activate %s` makes it active again.", wish3ID, wish3ID)
	if !strings.Contains(brief, wantPaused) {
		t.Errorf("expected paused line %q in global brief:\n%s", wantPaused, brief)
	}
	wantGranted := fmt.Sprintf("- **Granted Wish** (`%s`):", wish4ID)
	if !strings.Contains(brief, wantGranted) {
		t.Errorf("expected granted line starting with %q in global brief:\n%s", wantGranted, brief)
	}

	if !strings.Contains(brief, "# Leading in Djinn") {
		t.Errorf("expected # Leading in Djinn in global brief:\n%s", brief)
	}
	if !strings.Contains(brief, "(djinn: never)") {
		t.Errorf("expected (djinn: never) in global brief:\n%s", brief)
	}

	resp, err := c.lead.Brief(ctx, connect.NewRequest(&planv1.LeadServiceBriefRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetText() != brief {
		t.Errorf("c.lead.Brief text mismatch: got %d bytes, want %d bytes", len(resp.Msg.GetText()), len(brief))
	}

	// The incident of 15/10/2026: Antigravity's run_command kept only the end of the brief, and the lead piped it
	// through head and sed. The rule on direct commands opens it; its end names every active wish and the rule again.
	if !strings.HasPrefix(brief, DirectCommandsRule) {
		t.Errorf("the brief does not open with the rule on direct commands:\n%.300s", brief)
	}
	// W234: the rule on the terminal no one reads comes next, and at its end too.
	if first := strings.SplitN(brief, "\n\n", 3); len(first) < 3 || first[1] != UnreadTerminalRule {
		t.Errorf("the brief's second paragraph is not the rule on the terminal:\n%.600s", brief)
	}
	lines := strings.Split(strings.TrimRight(brief, "\n"), "\n")
	tail := strings.Join(lines[max(0, len(lines)-12):], "\n")
	for _, want := range []string{
		"- **Active Wish 1** (`" + wish1ID + "`): 0 running, 0 waiting, 1 open questions.",
		"- **Active Wish 2** (`" + wish2ID + "`): 0 running, 0 waiting, 0 open questions.",
		"`djinn brief --wishes`", DirectCommandsRule, DirectFlags, UnreadTerminalRule,
	} {
		if !strings.Contains(tail, want) {
			t.Errorf("the brief's last lines lack %q:\n%s", want, tail)
		}
	}
	if strings.Contains(tail, "Paused Wish") || strings.Contains(tail, "Granted Wish") {
		t.Errorf("the brief's last lines name a wish that is not active:\n%s", tail)
	}

	// djinn brief --wishes: where the wishes stand, without the rules.
	wishes, err := c.lead.Brief(ctx, connect.NewRequest(&planv1.LeadServiceBriefRequest{Wishes: true}))
	if err != nil {
		t.Fatal(err)
	}
	short := wishes.Msg.GetText()
	for _, want := range []string{DirectCommandsRule, "# The wish: Active Wish 1", "Which oil?", "# The wish: Active Wish 2", wantPaused, wantGranted} {
		if !strings.Contains(short, want) {
			t.Errorf("djinn brief --wishes lacks %q:\n%s", want, short)
		}
	}
	for _, gone := range []string{"# Leading in Djinn", "## Commands", "## The projects' rules", "at a glance"} {
		if strings.Contains(short, gone) {
			t.Errorf("djinn brief --wishes holds %q:\n%s", gone, short)
		}
	}
	if !strings.HasPrefix(brief, strings.TrimRight(short, "\n")) {
		t.Errorf("djinn brief --wishes is not the start of the brief:\n%s", short)
	}
}

// TestStartLineRunsCommandsAlone: the first message of every lead forbids pipes, chains, loops, redirections and
// substitutions, and names the brief that no tool cuts; cmd.exe still carries it as one argument.
func TestStartLineRunsCommandsAlone(t *testing.T) {
	start := StartLine()
	for _, want := range []string{"never a pipe (`|`)", "`&&`, `||`, `;`", "a loop (`for`)", "(`>`, `<<`)", "(`$(...)`)", "`djinn brief --wishes`"} {
		if !strings.Contains(start, want) {
			t.Errorf("StartLine lacks %q: %s", want, start)
		}
	}
	for _, goos := range []string{"linux", "windows"} {
		if _, ok := quoteArg(goos, start); !ok {
			t.Errorf("StartLine does not go through the shell of %s: %s", goos, start)
		}
	}
	rules := briefRules(nil, "")
	for _, want := range []string{DirectCommandsRule, DirectFlags, "`djinn brief --wishes`", "`djinn lead notes --all`",
		"`djinn task list --wish-id <wish> --short`", "`djinn task watch <task>` follows a task until it ends; `--last 5`", "`djinn question get <question> --wish-id <wish>`"} {
		if !strings.Contains(rules, want) {
			t.Errorf("briefRules lacks %q", want)
		}
	}
}

// TestLeadNotesAtOnce: djinn lead notes prints several notes, or all of them, in one command, never in a shell loop.
func TestLeadNotesAtOnce(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	read := func(req *planv1.LeadServiceNotesRequest) (string, error) {
		res, err := c.lead.Notes(ctx, connect.NewRequest(req))
		if err != nil {
			return "", err
		}
		return res.Msg.GetText(), nil
	}
	all, err := notes.Notes()
	if err != nil || len(all) < 2 {
		t.Fatalf("notes: %d, %v", len(all), err)
	}
	a, b := all[0], all[1]

	if got, err := read(&planv1.LeadServiceNotesRequest{}); err != nil || !strings.HasPrefix(got, "- `"+a.Name+"`: ") {
		t.Errorf("the index: %q, %v", got, err)
	}
	if got, err := read(&planv1.LeadServiceNotesRequest{Name: a.Name}); err != nil || got != a.Text() {
		t.Errorf("one note: %q, %v", got, err)
	}
	if got, err := read(&planv1.LeadServiceNotesRequest{Name: " " + b.Name + " , " + strings.ToUpper(a.Name) + ","}); err != nil ||
		got != b.Text()+"\n"+a.Text() {
		t.Errorf("two notes: %q, %v", got, err)
	}
	got, err := read(&planv1.LeadServiceNotesRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		if strings.Count(got, n.Text()) != 1 {
			t.Errorf("--all lacks %s once", n.Name)
		}
	}
	if _, err := read(&planv1.LeadServiceNotesRequest{Name: a.Name + ",nope,nada"}); code(err) != connect.CodeNotFound ||
		!strings.Contains(err.Error(), `"nope", "nada"`) {
		t.Errorf("unknown notes: %v", err)
	}
	if _, err := read(&planv1.LeadServiceNotesRequest{Name: a.Name, All: true}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("--all with a name: %v", err)
	}
}

func TestBriefHeldPush(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	dir1 := t.TempDir()
	p1, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir1}))
	if err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	p2, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir2}))
	if err != nil {
		t.Fatal(err)
	}

	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Held push", ProjectIds: []string{p1.Msg.GetProject().GetId(), p2.Msg.GetProject().GetId()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	wish, err := store.Get[*planv1.Wish](ctx, c.store, wishID)
	if err != nil {
		t.Fatal(err)
	}
	corr := &planv1.Task{
		Id: store.NewID(), WishId: wishID, Code: "W10", Title: "Fix push", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		CreateTime: timestamppb.Now(),
	}
	wish.Pushes = []*planv1.WishPush{
		{
			ProjectId:   p1.Msg.GetProject().GetId(),
			Held:        "push checks failed: test exited 1",
			CorrectedBy: corr.GetId(),
		},
	}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/put", corr); err != nil {
			return err
		}
		if err := tx.Put(corr); err != nil {
			return err
		}
		if err := tx.Journal(actor, "test/put", wish); err != nil {
			return err
		}
		return tx.Put(wish)
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(ctx, c.store, t.TempDir(), wishID)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("- Push held in %s: push checks failed: test exited 1, corrected by W10.\n", p1.Msg.GetProject().GetName())
	if !strings.Contains(brief.Moving, want) {
		t.Errorf("brief lacks %q:\n%s", want, brief.Moving)
	}
}

// TestBriefComments: diff review comments on tasks appear in the brief with file, lines, and resolution status.
func TestBriefComments(t *testing.T) {
	ctx := t.Context()
	c, wish, _ := source(t)
	tasks, err := store.List[*planv1.Task](ctx, c.store, store.Where{"wish_id": wish.GetId(), "code": "W1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("W1: %v, %v", tasks, err)
	}
	task := tasks[0]
	task.Comments = []*planv1.TaskComment{
		{
			Id:       store.NewID(),
			File:     "src/main.go",
			Line:     10,
			EndLine:  10,
			Text:     "Check boundary condition",
			Resolved: false,
		},
		{
			Id:       store.NewID(),
			File:     "src/utils.go",
			Line:     20,
			EndLine:  25,
			Text:     "Refactored helper function",
			Resolved: true,
		},
	}
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
	if !strings.Contains(brief.Moving, "- Comment on src/main.go, line 10 (unresolved): Check boundary condition") {
		t.Errorf("brief lacks unresolved comment:\n%s", brief.Moving)
	}
	if !strings.Contains(brief.Moving, "- Comment on src/utils.go, lines 20-25 (resolved): Refactored helper function") {
		t.Errorf("brief lacks resolved comment:\n%s", brief.Moving)
	}
}

// TestBriefListsNotes (Q260): every brief gives the lead's notes as an index, a line per note with its name and
// description, never a note's text; the rule says to open one before acting in its area, and to change them by a task
// that edits lead/notes, never at run time, and (Q262 B) to update or remove a note in the same breath as the task
// that fixes what it describes.
func TestBriefListsNotes(t *testing.T) {
	all, err := notes.Notes()
	if err != nil {
		t.Fatal(err)
	}
	c := serve(t)
	res, err := c.lead.Brief(t.Context(), connect.NewRequest(&planv1.LeadServiceBriefRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	brief := res.Msg.GetText()
	for _, want := range []string{
		"**Read the lead's notes before you act in their area.**",
		"**Run direct `djinn` commands and native file tools.**",
		"Run each `djinn` command alone, as one direct command: never a pipe (`|`), a chain (`&&`, `||`, `;`), a loop (`for`)",
		"a backtick between double quotes is a substitution too",
		"(`Read`, `Write`, `view_file`, `write_to_file`)",
		"**Delegate.**",
		"Never read or search source code yourself (no `Read`, `Grep`, `Glob`, or `view_file` in the lead session to inspect code before spawning a task)",
		"even finding out where a feature lives or how a flag works goes immediately to a Djinn task",
		"open it: `djinn lead notes <name>`",
		"spawns a task there that edits `lead/notes/`",
		"nothing writes them at run time",
		"When a task fixes what a note describes, update or remove that note in the same breath",
		"that task, or one spawned with it, edits `lead/notes/`; no cleanup waits for later",
		"\n## The lead's notes\n\n",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
	for _, n := range all {
		if line := "- `" + n.Name + "`: " + n.Description + "\n"; !strings.Contains(brief, line) {
			t.Errorf("the brief lacks the index line %q", line)
		}
		for para := range strings.SplitSeq(n.Body, "\n\n") {
			if strings.Contains(brief, strings.TrimSpace(para)) {
				t.Errorf("the brief holds the text of note %s: %q", n.Name, para)
			}
		}
	}
}

// TestBriefSaysWhoWaitsForTheIfrit: the lead's briefs, of the wish and of all of them, say who waits for the gates a
// lit ifrit holds, so that the lead offers the developer to end it (Q11); an ifrit nobody waits for has no such line.
func TestBriefSaysWhoWaitsForTheIfrit(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Front"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	start := timestamppb.New(time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC))
	ifrit := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W12", Title: "Front of dolmen", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{State: planv1.IfritState_IFRIT_STATE_LISTENING}, StartTime: start}
	other := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W13", Title: "Back of dolmen", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{State: planv1.IfritState_IFRIT_STATE_LISTENING}, StartTime: start}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range []*planv1.Task{ifrit, other} {
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
	home := t.TempDir()
	waits := map[string][]string{ifrit.GetId(): {"W15 (Cards): go tool task e2e, for stack", "W16 (Lists), for stack"}}
	want := "  - Waiting for the gates this ifrit holds until the developer leaves Djinn: " +
		"W15 (Cards): go tool task e2e, for stack; W16 (Lists), for stack. " +
		"Offer the developer to end it (`djinn task done W12`) or stop it (`djinn task stop W12`).\n"

	brief, err := buildBrief(ctx, c.store, home, wishID, waits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.Moving, "Front of dolmen (claude, ifrit, listening, since 2026-10-16 15:00 UTC)\n"+want) {
		t.Errorf("the wish's brief does not say who waits for W12:\n%s", brief.Moving)
	}
	if strings.Count(brief.Moving, "Waiting for the gates this ifrit holds") != 1 {
		t.Errorf("W13, which nobody waits for, has its line:\n%s", brief.Moving)
	}
	global, err := globalBrief(ctx, c.store, home, true, waits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(global, "Front of dolmen (claude, ifrit, listening, since 2026-10-16 15:00 UTC)\n"+want) {
		t.Errorf("the brief of the wishes does not say who waits for W12:\n%s", global)
	}
	if plain, err := BuildBrief(ctx, c.store, home, wishID); err != nil || strings.Contains(plain.Moving, "Waiting for the gates") {
		t.Errorf("no gates known, the brief says who waits (%v):\n%s", err, plain.Moving)
	}
}

// TestBriefLeadsAnIfrit (T51, Q11): the lead's rules say when to light an ifrit and how to lead one, its commands
// name --kind ifrit and --about; the wish's brief says which running task is an ifrit and where it stands, and that
// one put out is lit again only at the developer's word.
func TestBriefLeadsAnIfrit(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Front"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	start := timestamppb.New(time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC))
	working := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W12", Title: "Front of dolmen", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{State: planv1.IfritState_IFRIT_STATE_WORKING}, StartTime: start}
	out := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W13", Title: "Stack of dolmen", Status: planv1.TaskStatus_TASK_STATUS_STOPPED,
		Kind: planv1.TaskKind_TASK_KIND_IFRIT, Ifrit: &planv1.TaskIfrit{State: planv1.IfritState_IFRIT_STATE_OUT},
		Error: "out: you left Djinn alone at 15:12", StartTime: start, EndTime: timestamppb.New(start.AsTime().Add(time.Hour))}
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W14", Title: "Cards", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: start}
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		for _, task := range []*planv1.Task{working, out, task} {
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
		"Front of dolmen (claude, ifrit, working, since",
		"**W13** Stack of dolmen: an ifrit out: you left Djinn alone at 15:12, lit again by a message only at the developer's word",
	} {
		if !strings.Contains(brief.Moving, want) {
			t.Errorf("the wish's brief lacks %q:\n%s", want, brief.Moving)
		}
	}
	if !strings.Contains(brief.Moving, "**W14** Cards (claude, since") {
		t.Errorf("a task that is no ifrit is called one:\n%s", brief.Moving)
	}
	for _, want := range []string{
		IfritRule,
		"never for work a task with its checks does alone. One lit per wish.",
		"`djinn task send <ifrit> \"…\"`, instead of spawning tasks",
		"`--about <ifrit>`",
		"do not repeat its reply",
		"When a task waits for a gate it holds, offer the developer to stop it",
		"never send it one without the developer's word",
		"`djinn task done <ifrit> --note \"…\"`: its work integrates",
		"`--kind ifrit` with `--idle-minutes`",
		"`--about W12` names the task it is about",
		"to an ifrit out, it lights it again",
		"- `ifrit`: ",
	} {
		if !strings.Contains(brief.Stable, want) {
			t.Errorf("the lead's rules lack %q", want)
		}
	}
}

// TestBriefMarksInvestigations (W213): the lead's brief marks an investigation's line, running, waiting or finished,
// and its rules say when to spawn one, and when a task is urgent work for feedback instead.
func TestBriefMarksInvestigations(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Studies"}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	start := timestamppb.New(time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC))
	inv := func(code, title string, status planv1.TaskStatus) *planv1.Task {
		return &planv1.Task{Id: store.NewID(), WishId: wishID, Code: code, Title: title, Status: status, StartTime: start,
			Kind: planv1.TaskKind_TASK_KIND_INVESTIGATION, Scheduled: true}
	}
	tasks := []*planv1.Task{inv("W1", "Study the flakes", planv1.TaskStatus_TASK_STATUS_RUNNING),
		inv("W2", "Audit the gates", planv1.TaskStatus_TASK_STATUS_PENDING), inv("W3", "Audit the merges", planv1.TaskStatus_TASK_STATUS_DONE)}
	tasks[1].StartTime, tasks[2].EndTime = nil, start
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
	for _, want := range []string{"**W1** Study the flakes [investigation] (claude", "**W2** Audit the gates [investigation]:",
		"**W3** Audit the merges [investigation]: done"} {
		if !strings.Contains(brief.Moving, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief.Moving)
		}
	}
	rules := briefRules(nil, "")
	for _, want := range []string{"--kind investigation", "--feedback", "a mockup", "is a repair, always first"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the lead's rules lack %q", want)
		}
	}
}
