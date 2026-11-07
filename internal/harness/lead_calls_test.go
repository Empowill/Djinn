package harness

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// leadCalls is djinn up with its tilasms, two wishes sharing codes, and the command line on it: Dew on three projects
// (dolmen, terraform, djinn), Other on djinn.
type leadCalls struct {
	*env
	tilasms   planv1connect.TilasmServiceClient
	questions planv1connect.QuestionServiceClient
	url       string
	client    *http.Client
	projects  map[string]string // identifier by name
	dew       string
	other     string
}

func newLeadCalls(t *testing.T) *leadCalls {
	t.Helper()
	l := &leadCalls{env: up(t, t.TempDir()), projects: map[string]string{}}
	mux := http.NewServeMux()
	for prefix, handler := range plan.Handlers(l.db, plan.WithHome(l.home), plan.WithWorkers(l.h)) {
		mux.Handle(prefix, handler)
	}
	mux.Handle(Handler(l.h))
	mux.Handle(PlanHandler(l.h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	l.url, l.client = srv.URL, srv.Client()
	l.tilasms = planv1connect.NewTilasmServiceClient(srv.Client(), srv.URL)
	l.questions = planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL)
	root := t.TempDir()
	for _, name := range []string{"dolmen", "terraform", "djinn"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p, err := l.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		l.projects[p.Msg.GetProject().GetName()] = p.Msg.GetProject().GetId()
	}
	makeWish := func(title string, projects ...string) string {
		var ids []string
		for _, p := range projects {
			ids = append(ids, l.projects[p])
		}
		w, err := l.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title, ProjectIds: ids}))
		if err != nil {
			t.Fatal(err)
		}
		return w.Msg.GetWish().GetId()
	}
	l.dew = makeWish("Dew", "dolmen", "terraform", "djinn")
	l.other = makeWish("Other", "djinn")
	return l
}

// run runs a command line against djinn up, as the lead types it.
func (l *leadCalls) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = cli.Run(t.Context(), args, cli.Config{Version: "test", Addr: l.url, HTTP: l.client, Stdout: &out, Stderr: &errs,
		Getenv: func(string) string { return "" }})
	return code, out.String(), errs.String()
}

// passes runs a command line that must pass, and returns what it printed.
func (l *leadCalls) passes(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := l.run(t, args...)
	if code != 0 {
		t.Fatalf("djinn %s: exit %d\n%s", strings.Join(args, " "), code, errs)
	}
	return out
}

// refused runs a command line that must be refused, and checks that its refusal says each of want.
func (l *leadCalls) refused(t *testing.T, args []string, want ...string) {
	t.Helper()
	code, _, errs := l.run(t, args...)
	if code == 0 {
		t.Fatalf("djinn %s passed, want it refused", strings.Join(args, " "))
	}
	for _, w := range want {
		if !strings.Contains(errs, w) {
			t.Errorf("djinn %s refused with\n%s\nwant it to say %q", strings.Join(args, " "), errs, w)
		}
	}
}

// task is the task of wish with this code.
func (l *leadCalls) task(t *testing.T, wish, code string) *planv1.Task {
	t.Helper()
	found, err := store.List[*planv1.Task](t.Context(), l.db, store.Where{"wish_id": wish, "code": code})
	if err != nil || len(found) != 1 {
		t.Fatalf("task %s of wish %s: %v, %v", code, wish, found, err)
	}
	return found[0]
}

// TestLeadCallsProjects (W474): the lead names a project by its name, as djinn project list shows it; a part of an
// azima works in the azima's project; a spawn that names none in a wish of several is refused with --project-id and
// each project's name.
func TestLeadCallsProjects(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)

	// Refused on 06/11: "--project-id: must be a valid UUID".
	l.passes(t, "task", "spawn", l.dew, "--title", "Mockup of the accounts", "--project-id", "djinn", "--later")
	if got := l.task(t, l.dew, "W1").GetProjectId(); got != l.projects["djinn"] {
		t.Errorf("W1 works in %s, want djinn (%s)", got, l.projects["djinn"])
	}
	// The same spawn without --project-id: refused, naming the flag for each project.
	l.refused(t, []string{"task", "spawn", l.dew, "--title", "Mockup of the accounts", "--later"},
		"the wish has 3 projects: run it again with --project-id dolmen, --project-id terraform or --project-id djinn")
	// A project no one has: the names it could be.
	l.refused(t, []string{"task", "spawn", l.dew, "--title", "x", "--project-id", "dolmne"},
		"--project-id: no project is named dolmne; expected one of dolmen, terraform, djinn, or a project's UUID")

	// A part of an azima works in the azima's project.
	l.passes(t, "task", "spawn", l.dew, "--title", "Patch the dead letters", "--kind", "azima", "--project-id", "terraform")
	l.passes(t, "task", "spawn", l.dew, "--title", "The subscriber's role", "--part-of", "T1", "--later")
	if got := l.task(t, l.dew, "W2").GetProjectId(); got != l.projects["terraform"] {
		t.Errorf("a part of T1 works in %s, want T1's terraform (%s)", got, l.projects["terraform"])
	}

	// A project the wish lacks: refused, naming the wish's.
	l.refused(t, []string{"task", "spawn", l.other, "--title", "x", "--project-id", "dolmen"},
		"project dolmen is not one of the wish's projects (djinn)", "or run it again with --project-id djinn")
}

// TestLeadCallsCodes (W474): a task, a tilasm or a question given by code passes alone when it is unique, with
// --wish-id or --wish whichever the command spells; in several wishes, the refusal gives the flag with each wish.
// What Djinn writes in its comments event passes as it is.
func TestLeadCallsCodes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)
	l.passes(t, "task", "spawn", l.dew, "--title", "Accounts by priority", "--project-id", "djinn", "--later")
	l.passes(t, "task", "spawn", l.other, "--title", "Elsewhere", "--later")

	// Refused on 06/11: "unknown flag --wish-id", then "<task-id>: must be a valid UUID".
	l.refused(t, []string{"task", "done", "W1"},
		"2 wishes have a task W1: run it again with --wish-id "+l.dew+" (Dew) or --wish-id "+l.other+" (Other)")
	l.passes(t, "task", "done", "W1", "--wish-id", l.dew, "--note", "merged by hand")
	if got := l.task(t, l.dew, "W1").GetStatus(); got != planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("Dew's W1 is %v, want done", got)
	}
	if got := l.task(t, l.other, "W1").GetStatus(); got == planv1.TaskStatus_TASK_STATUS_DONE {
		t.Errorf("Other's W1 is done too")
	}
	if out := l.passes(t, "task", "get", "W1", "--wish", l.other); !strings.Contains(out, "title: Elsewhere") {
		t.Errorf("task get W1 --wish <other> printed\n%s", out)
	}

	// A tilasm L01 in each wish; the developer sends comments on Dew's.
	var dewL01 *planv1.Tilasm
	for _, wish := range []string{l.dew, l.other} {
		dir := t.TempDir()
		writeFile(t, dir, "index.html", "<!doctype html><html><head><title>Accounts</title></head><body><p>By priority.</p></body></html>")
		res, err := l.tilasms.Put(t.Context(), connect.NewRequest(&planv1.TilasmServicePutRequest{Path: dir, Wish: wish}))
		if err != nil {
			t.Fatal(err)
		}
		if wish == l.dew {
			dewL01 = res.Msg.GetTilasm()
		}
	}
	if _, err := l.tilasms.PutComment(t.Context(), connect.NewRequest(&planv1.TilasmServicePutCommentRequest{
		TilasmId: dewL01.GetId(), Text: "Drag and drop to order them.",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := l.tilasms.SendComments(t.Context(), connect.NewRequest(&planv1.TilasmServiceSendCommentsRequest{
		TilasmId: dewL01.GetId(), Choice: planv1.SendChoice_SEND_CHOICE_ITERATE,
	})); err != nil {
		t.Fatal(err)
	}
	events, err := store.List[*planv1.LeadEvent](t.Context(), l.db, store.Where{"wish_id": l.dew})
	if err != nil {
		t.Fatal(err)
	}
	var command string
	for _, e := range events {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS {
			_, command, _ = strings.Cut(e.GetText(), ": djinn ")
		}
	}
	if command == "" {
		t.Fatalf("no comments event names a command: %v", events)
	}
	// Refused on 06/11 without --wish: the event's command, replayed as it is, passes.
	if out := l.passes(t, strings.Fields(command)...); !strings.Contains(out, "Drag and drop to order them.") {
		t.Errorf("djinn %s printed\n%s\nwant the comment sent", command, out)
	}
	l.refused(t, []string{"tilasm", "comments", "L01", "--send", "1"},
		"2 wishes have a tilasm L01: run it again with --wish "+l.dew+" (Dew) or --wish "+l.other+" (Other)")
	l.refused(t, []string{"tilasm", "reply", "L01", "C1", "--text", "Done: ordered by drag and drop."},
		"--wish "+l.dew+" (Dew)")
	l.passes(t, "tilasm", "reply", "L01", "C1", "--text", "Done: ordered by drag and drop.", "--wish-id", l.dew)

	// A question Q01 in each wish.
	for _, wish := range []string{l.dew, l.other} {
		if _, err := l.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Which order?", WishId: wish,
		})); err != nil {
			t.Fatal(err)
		}
	}
	l.refused(t, []string{"question", "get", "Q01"},
		"2 wishes have a question Q01", "--wish-id "+l.dew+" (Dew)")
}

// TestLeadCallsContextFile (W474): djinn question ask reads its context from a file, as djinn question revise does:
// the lead writes a context of several lines with its Write tool, never inline.
func TestLeadCallsContextFile(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)
	text := "~~~\n# prints the push\ndjinn project show djinn\n~~~\n\nWhy: the push waits."
	file := filepath.Join(t.TempDir(), "context.md")
	if err := os.WriteFile(file, []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l.passes(t, "question", "ask", "Push now?", l.dew, "--context-file", file)
	found, err := store.List[*planv1.Question](t.Context(), l.db, store.Where{"wish_id": l.dew})
	if err != nil || len(found) != 1 {
		t.Fatalf("questions of Dew: %v, %v", found, err)
	}
	if got := found[0].GetContext(); got != text {
		t.Errorf("context = %q, want the file's %q", got, text)
	}
	l.refused(t, []string{"question", "ask", "Push now?", l.dew, "--context", "x", "--context-file", file},
		"give context or context_file, not both")
}

// TestLeadCallsGates (W592): the calls the leads of 07/11 00:28 and 00:29 typed seven times in a wish of three
// projects, on a binary without W474: each passes, or its refusal gives the command that passes, which does.
func TestLeadCallsGates(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)
	gates, err := l.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{
		Directory: gitRepo(t), Name: "dolmen-git",
	}))
	if err != nil {
		t.Fatal(err)
	}
	gitID := gates.Msg.GetProject().GetId()
	w, err := l.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Djinn gates", ProjectIds: []string{gitID, l.projects["terraform"], l.projects["djinn"]},
	}))
	if err != nil {
		t.Fatal(err)
	}
	wish := w.Msg.GetWish().GetId()

	// 00:29: "--project-id: must be a valid UUID".
	l.passes(t, "task", "spawn", wish, "--kind", "azima", "--title", "Remote branches have a PR", "--project-id", "djinn")
	if got := l.task(t, wish, "T1").GetProjectId(); got != l.projects["djinn"] {
		t.Errorf("T1 works in %s, want djinn", got)
	}
	// 00:29: "the wish has 3 projects: name one with --project-id", with nothing to run.
	l.refused(t, []string{"task", "spawn", wish, "--kind", "azima", "--title", "Remote branches have a PR"},
		"the wish has 3 projects: name one with --project-id (dolmen-git, terraform or djinn)",
		"djinn task spawn "+wish+" --title 'Remote branches have a PR' --project-id djinn --kind azima")

	// W588's state at 00:29: done, merged into its azima's branch, which removed its worktree.
	l.passes(t, "task", "spawn", wish, "--kind", "azima", "--title", "The job boarder has time", "--project-id", "dolmen-git")
	t2 := l.task(t, wish, "T2")
	done := &planv1.Task{
		Id: store.NewID(), WishId: wish, Code: "W1", Title: "!3014: drop the test that pins no local timeout",
		ProjectId: gitID, PartOf: t2.GetId(), Provider: planv1.Provider_PROVIDER_FAKE, Status: planv1.TaskStatus_TASK_STATUS_DONE,
		SessionId: "s", Scheduled: true, WriteScopes: []string{"go/agentci"}, CreateTime: timestamppb.Now(),
		StartTime: timestamppb.Now(), EndTime: timestamppb.Now(), Branch: "w1-3014",
		Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED,
			Branch: "djinn/T2-the-job-boarder-has-time"},
	}
	putTask(t, l.db, done, "text dropped")

	// 00:28: "the wish has 3 projects: name one with --project-id"; the part works in its azima's project.
	l.passes(t, "task", "spawn", wish, "--title", "!3014: answer the thread", "--part-of", "T2", "--after", "W1",
		"--write-scopes", "gitlab-mr-3014-discussions", "--prompt", "text answered", "--later")
	if got := l.task(t, wish, "W2").GetProjectId(); got != gitID {
		t.Errorf("a part of T2 works in %s, want T2's dolmen-git (%s)", got, gitID)
	}
	// An azima part of another one takes its project too.
	l.passes(t, "task", "spawn", wish, "--kind", "azima", "--title", "The review threads", "--part-of", "T2")
	if got := l.task(t, wish, "T3").GetProjectId(); got != gitID {
		t.Errorf("an azima part of T2 works in %s, want T2's dolmen-git (%s)", got, gitID)
	}

	// 00:28: "unknown flag --wish-id", "<task-id>: must be a valid UUID", then "has no worktree any more" with a
	// --fork alone, which names no project in a wish of three. Each says why, and the spawn that goes on.
	prompt := "Djinn pushed your branch: answer the thread, resolve it."
	fork := "djinn task spawn " + wish + " --title '!3014: drop the test that pins no local timeout' --part-of T2 " +
		"--fork W1 --after W1 --write-scopes go/agentci --prompt 'Djinn pushed your branch: answer the thread, resolve it.'"
	for _, args := range [][]string{
		{"task", "continue", "W1", "--wish-id", wish, "--prompt", prompt},
		{"task", "continue", "W1", "--wish", "Djinn gates", "--prompt", prompt},
		{"task", "continue", done.GetId(), "--prompt", prompt},
	} {
		l.refused(t, args, "task W1 has no worktree any more: its work merged into djinn/T2-the-job-boarder-has-time, "+
			"which removed it. djinn task continue resumes a task in its own worktree only", fork)
	}
	// A prompt of several lines goes through a file.
	l.refused(t, []string{"task", "continue", "W1", "--wish-id", wish, "--prompt", "Answer.\n\nResolve."},
		"--fork W1 --after W1 --write-scopes go/agentci --prompt-file <file>",
		"<file> holds your prompt: write it with your Write tool first.")

	// The command given passes, as the shell reads it: a part of T2, forked from W1's session, after it.
	l.passes(t, "task", "spawn", wish, "--title", "!3014: drop the test that pins no local timeout", "--part-of", "T2",
		"--fork", "W1", "--after", "W1", "--write-scopes", "go/agentci", "--prompt", prompt, "--later")
	got := l.task(t, wish, "W3")
	if got.GetPartOf() != t2.GetId() || got.GetProjectId() != gitID || got.GetForkOf() != "W1" ||
		!slices.Equal(got.GetDependsOn(), []string{done.GetId()}) {
		t.Errorf("the fork W3: %v", got)
	}
}
