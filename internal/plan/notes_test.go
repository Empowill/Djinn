package plan_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/lead/notes"
)

// TestLeadNotesCommand (Q260): djinn lead notes prints the index, djinn lead notes <name> one note whole, from the
// notes built into djinn.
func TestLeadNotesCommand(t *testing.T) {
	t.Parallel()
	s, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	for prefix, h := range plan.Handlers(s) {
		mux.Handle(prefix, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := cli.Run(t.Context(), args, cli.Config{
			Version: "test", Addr: srv.URL, HTTP: srv.Client(), Stdout: &out, Stderr: &errs,
			Getenv: func(string) string { return "" },
		})
		return code, out.String(), errs.String()
	}

	index, err := notes.Index()
	if err != nil {
		t.Fatal(err)
	}
	if code, out, errs := run("lead", "notes"); code != 0 || !strings.Contains(out, index) {
		t.Errorf("djinn lead notes: code %d\nstdout:\n%s\nstderr:\n%s\nwant the index:\n%s", code, out, errs, index)
	}

	note, ok := notes.Find("question-context-heredoc")
	if !ok {
		t.Fatal("no note question-context-heredoc")
	}
	code, out, errs := run("lead", "notes", "question-context-heredoc")
	if code != 0 || !strings.Contains(out, note.Description) || !strings.Contains(out, note.Body) {
		t.Errorf("djinn lead notes question-context-heredoc: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	code, _, errs = run("lead", "notes", "no-such-note")
	if code != 1 || !strings.Contains(errs, `the lead has no note "no-such-note": djinn lead notes lists them`) {
		t.Errorf("djinn lead notes no-such-note: code %d, stderr %q", code, errs)
	}
}
