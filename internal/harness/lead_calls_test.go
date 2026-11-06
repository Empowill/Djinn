package harness

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

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
// azima works in the azima's project; a spawn that names none in a wish of several is refused with the command again,
// one per project, ready to run.
func TestLeadCallsProjects(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)

	// Refused on 06/11: "--project-id: must be a valid UUID".
	l.passes(t, "task", "spawn", l.dew, "--title", "Mockup of the accounts", "--project-id", "djinn", "--later")
	if got := l.task(t, l.dew, "W1").GetProjectId(); got != l.projects["djinn"] {
		t.Errorf("W1 works in %s, want djinn (%s)", got, l.projects["djinn"])
	}
	// The same spawn without --project-id: refused, naming the projects and the command for each.
	l.refused(t, []string{"task", "spawn", l.dew, "--title", "Mockup of the accounts", "--later"},
		"the wish has 3 projects: name one with --project-id (dolmen, terraform or djinn)",
		"djinn task spawn "+l.dew+" --title 'Mockup of the accounts' --project-id dolmen --later",
		"djinn task spawn "+l.dew+" --title 'Mockup of the accounts' --project-id djinn --later")
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
		"project dolmen is not one of the wish's projects (djinn)",
		"djinn task spawn "+l.other+" --title x --project-id djinn")
}

// TestLeadCallsCodes (W474): a task, a tilasm or a question given by code passes alone when it is unique, with
// --wish-id or --wish whichever the command spells; in several wishes, the refusal gives the command again with each.
// What Djinn writes in its comments event passes as it is.
func TestLeadCallsCodes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	l := newLeadCalls(t)
	l.passes(t, "task", "spawn", l.dew, "--title", "Accounts by priority", "--project-id", "djinn", "--later")
	l.passes(t, "task", "spawn", l.other, "--title", "Elsewhere", "--later")

	// Refused on 06/11: "unknown flag --wish-id", then "<task-id>: must be a valid UUID".
	l.refused(t, []string{"task", "done", "W1"},
		"2 wishes have a task W1: give its identifier, or the wish with --wish-id",
		"djinn task done W1 --wish-id "+l.dew+"  # Dew", "djinn task done W1 --wish-id "+l.other+"  # Other")
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
		"2 wishes have a tilasm L01: give its identifier, or the wish with --wish",
		"djinn tilasm comments L01 --wish "+l.dew+" --send 1  # Dew")
	l.refused(t, []string{"tilasm", "reply", "L01", "C1", "--text", "Done: ordered by drag and drop."},
		"djinn tilasm reply L01 C1 --text 'Done: ordered by drag and drop.' --wish "+l.dew+"  # Dew")
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
		"2 wishes have a question Q01", "djinn question get Q01 --wish-id "+l.dew+"  # Dew")
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
