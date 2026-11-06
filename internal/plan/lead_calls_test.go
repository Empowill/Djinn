package plan_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// leadCalls is the plan services on a store in memory, the command line on them, and what a launch received: the
// calls W474 left refused (W481), replayed as the lead types them.
type leadCalls struct {
	wishes    planv1connect.WishServiceClient
	questions planv1connect.QuestionServiceClient
	projects  map[string]string // identifier by name
	url       string
	client    *http.Client
	mu        sync.Mutex
	launched  []*planv1.PlannedTask
}

func newLeadCalls(t *testing.T) *leadCalls {
	t.Helper()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	l := &leadCalls{projects: map[string]string{}}
	launch := func(_ context.Context, q *planv1.Question, p *planv1.PlannedTask) (*planv1.Task, error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.launched = append(l.launched, p)
		return &planv1.Task{Id: "0199c0a1-7b2e-7c3d-8e4f-000000000009", WishId: q.GetWishId(), Code: "W1", Title: p.GetTitle()}, nil
	}
	mux := http.NewServeMux()
	for prefix, h := range plan.Handlers(s, plan.WithLaunch(launch)) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	l.url, l.client = srv.URL, srv.Client()
	l.wishes = planv1connect.NewWishServiceClient(srv.Client(), srv.URL)
	l.questions = planv1connect.NewQuestionServiceClient(srv.Client(), srv.URL)
	projects := planv1connect.NewProjectServiceClient(srv.Client(), srv.URL)
	root := t.TempDir()
	for _, name := range []string{"dolmen", "djinn"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p, err := projects.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		l.projects[name] = p.Msg.GetProject().GetId()
	}
	return l
}

// wish makes a wish titled so, on dolmen.
func (l *leadCalls) wish(t *testing.T, title string) string {
	t.Helper()
	w, err := l.wishes.Make(t.Context(), connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: title, ProjectIds: []string{l.projects["dolmen"]},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return w.Msg.GetWish().GetId()
}

// run runs a command line against the services, as the lead types it.
func (l *leadCalls) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = cli.Run(t.Context(), args, cli.Config{
		Version: "test", Addr: l.url, HTTP: l.client, Stdout: &out, Stderr: &errs, Getenv: func(string) string { return "" },
	})
	return code, out.String(), errs.String()
}

// passes runs args and fails the test unless the command passed; it returns what it printed.
func (l *leadCalls) passes(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := l.run(t, args...)
	if code != 0 {
		t.Fatalf("djinn %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

// refused runs args and fails the test unless the command was refused with code, saying each of want.
func (l *leadCalls) refused(t *testing.T, code int, args []string, want ...string) {
	t.Helper()
	got, out, errs := l.run(t, args...)
	if got != code {
		t.Errorf("djinn %s: exit %d, want %d\n%s%s", strings.Join(args, " "), got, code, out, errs)
	}
	for _, w := range want {
		if !strings.Contains(errs, w) {
			t.Errorf("djinn %s: stderr\n%s\nwant it to say %q", strings.Join(args, " "), errs, w)
		}
	}
}

// TestLeadCallsWishTitle (W481): a wish is named by its exact title, case ignored, or by the start of only one title,
// as a positional <wish-id> and as --wish-id; a start of several titles is refused with the command again for each.
func TestLeadCallsWishTitle(t *testing.T) {
	t.Parallel()
	l := newLeadCalls(t)
	calls := l.wish(t, "Ship the lead calls")
	docs := l.wish(t, "Ship the docs")

	out := l.passes(t, "question", "ask", "Push now?", "Ship the lead calls")
	if !strings.Contains(out, "code: Q01") {
		t.Errorf("question ask in the wish by title printed\n%s", out)
	}
	if out := l.passes(t, "question", "list", "--wish-id", "ship the l"); !strings.Contains(out, "Push now?") {
		t.Errorf("question list --wish-id by the start of a title printed\n%s", out)
	}
	if out := l.passes(t, "question", "list", "--wish-id", "Ship the docs"); strings.Contains(out, "Push now?") {
		t.Errorf("question list of the other wish printed its question\n%s", out)
	}
	if out := l.passes(t, "question", "get", "Q01", "--wish", "SHIP THE LEAD CALLS"); !strings.Contains(out, "wish_id: "+calls) {
		t.Errorf("question get --wish by title printed\n%s", out)
	}

	l.refused(t, 2, []string{"question", "ask", "Push?", "Ship the"},
		`<wish-id>: 2 wishes are titled "Ship the" or start so: give one by its UUID`,
		"djinn question ask 'Push?' "+calls+"  # Ship the lead calls",
		"djinn question ask 'Push?' "+docs+"  # Ship the docs")
	l.refused(t, 2, []string{"question", "list", "--wish-id", "Lamp"},
		`--wish-id: no wish is titled "Lamp", nor starts so; expected a wish's UUID, its title, or the start of only one title`)
}

// TestLeadCallsAddProject (W481): djinn wish add-project <wish> djinn adds the project without a flag, the wish by
// its title; --project-id still works.
func TestLeadCallsAddProject(t *testing.T) {
	t.Parallel()
	l := newLeadCalls(t)
	calls := l.wish(t, "Ship the lead calls")
	docs := l.wish(t, "Ship the docs")

	l.passes(t, "wish", "add-project", "Ship the lead calls", "djinn")
	l.passes(t, "wish", "add-project", docs, "--project-id", "djinn")
	list, err := l.wishes.List(t.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range list.Msg.GetWishes() {
		if !slices.Contains([]string{calls, docs}, w.GetId()) || !slices.Contains(w.GetProjectIds(), l.projects["djinn"]) {
			t.Errorf("wish %s works in %v, want djinn among them", w.GetTitle(), w.GetProjectIds())
		}
	}
	l.refused(t, 1, []string{"wish", "add-project", "Ship the docs", "djinn"}, "already_exists")
}

// TestLeadCallsQuestionCode (W481): a question code in several wishes is the only open question of them; when none or
// several are open, the refusal gives the commands. djinn question launch takes the only answered one, and its prompt
// from --prompt-file.
func TestLeadCallsQuestionCode(t *testing.T) {
	t.Parallel()
	l := newLeadCalls(t)
	calls := l.wish(t, "Ship the lead calls")
	docs := l.wish(t, "Ship the docs")
	ids := map[string]string{}
	for _, wish := range []string{calls, docs} {
		q, err := l.questions.Ask(t.Context(), connect.NewRequest(&planv1.QuestionServiceAskRequest{
			Text: "Light it?", WishId: wish, Plan: []string{"title: Light it\n\nThe plan's prompt."},
		}))
		if err != nil {
			t.Fatal(err)
		}
		ids[wish] = q.Msg.GetQuestion().GetId()
	}

	// Both open: refused, the commands given.
	l.refused(t, 1, []string{"question", "get", "Q01"}, "2 wishes have a question Q01",
		"djinn question get Q01 --wish-id "+calls+"  # Ship the lead calls")

	// The docs' answered: Q01 is the lead calls' one, the only one open.
	if _, err := l.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: ids[docs]}}, Choice: planv1.Choice_CHOICE_YES,
		Note: "Light it, with the new prompt.",
	})); err != nil {
		t.Fatal(err)
	}
	if out := l.passes(t, "question", "get", "Q01"); !strings.Contains(out, "id: "+ids[calls]) {
		t.Errorf("question get Q01 printed\n%s\nwant the open one, %s", out, ids[calls])
	}

	// Launched: the answered one, its prompt read from the file.
	file := filepath.Join(t.TempDir(), "prompt.md")
	prompt := "**Gain**: light it.\n\n# Scope\n\nThe lamp only."
	if err := os.WriteFile(file, []byte(prompt+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l.passes(t, "question", "launch", "Q01", "--prompt-file", file)
	l.mu.Lock()
	launched := slices.Clone(l.launched)
	l.mu.Unlock()
	if len(launched) != 1 || launched[0].GetPrompt() != prompt {
		t.Errorf("launched %v, want one task with the file's prompt %q", launched, prompt)
	}
	l.refused(t, 2, []string{"question", "launch", "Q01", "--prompt", "x", "--prompt-file", file},
		"give --prompt or --prompt-file, not both")

	// Both answered: refused again.
	if _, err := l.questions.Answer(t.Context(), connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: ids[calls]}}, Choice: planv1.Choice_CHOICE_YES,
		Note: "Later.",
	})); err != nil {
		t.Fatal(err)
	}
	l.refused(t, 1, []string{"question", "get", "Q01"}, "2 wishes have a question Q01",
		"djinn question get Q01 --wish-id "+docs+"  # Ship the docs")
}
