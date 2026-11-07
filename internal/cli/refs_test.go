package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestResolve (W474): the command line sends a project named by its name as its identifier, and takes --wish for
// --wish-id where a request spells it so; a refusal names in its text the flag and the values that fit, on the
// command line as in djinn mcp, and nothing is tried again.
func TestResolve(t *testing.T) {
	t.Parallel()
	t.Run("a project by its name", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		if code, out, errs := run("task", "spawn", wishID, "--title", "Ship", "--project-id", "WEB"); code != 0 || !strings.Contains(out, "project_id: "+wishID) {
			t.Fatalf("spawn --project-id WEB: exit %d\n%s%s", code, out, errs)
		}
		calls := f.seen()
		want := &planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Ship", ProjectId: wishID}
		if len(calls) != 2 || !proto.Equal(calls[1], want) {
			t.Errorf("the server received %v, want the projects listed, then %v", calls, want)
		}
	})
	t.Run("the refusal names what fits", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		code, _, errs := run("task", "spawn", wishID, "--title", "Ship it")
		want := "error: invalid_argument: the wish has 2 projects: run it again with --project-id api or --project-id web\n"
		if code != 1 || errs != want {
			t.Errorf("exit %d, stderr\n%s\nwant 1 and\n%s", code, errs, want)
		}
		if calls := f.seen(); len(calls) != 1 {
			t.Errorf("the server received %v, want the one call refused, not tried again", calls)
		}
	})
	t.Run("wish for wish-id", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		if code, _, errs := run("tilasm", "comments", "L01", "--wish-id", wishID); code != 0 {
			t.Fatalf("tilasm comments --wish-id: exit %d\n%s", code, errs)
		}
		want := &planv1.TilasmServiceCommentsRequest{Tilasm: &planv1.TilasmRef{Ref: &planv1.TilasmRef_Code{Code: "L01"}}, Wish: wishID}
		if calls := f.seen(); len(calls) != 1 || !proto.Equal(calls[0], want) {
			t.Errorf("the server received %v, want %v", calls, want)
		}
	})
	t.Run("a wish no title names", func(t *testing.T) {
		t.Parallel()
		_, run := serve(t)
		code, _, errs := run("task", "done", "W5", "--wish-id", "Dew")
		if code != 2 || !strings.Contains(errs, `--wish-id: no wish is titled "Dew", nor starts so; expected a wish's UUID, `+
			`its title, or the start of only one title (djinn wish list shows them)`) {
			t.Errorf("exit %d, stderr\n%s", code, errs)
		}
	})
	// W481: the lead names a wish by its title, as the brief and the events show it.
	t.Run("a wish by its title", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		if code, out, errs := run("task", "spawn", "ship it", "--title", "Ship", "--project-id", projectID); code != 0 {
			t.Fatalf("spawn in the wish by title: exit %d\n%s%s", code, out, errs)
		}
		calls := f.seen()
		// The granted Ship it is left out: the open one is meant.
		want := &planv1.TaskServiceSpawnRequest{WishId: wishID, Title: "Ship", ProjectId: projectID}
		if len(calls) != 2 || !proto.Equal(calls[1], want) {
			t.Errorf("the server received %v, want the wishes listed, then %v", calls, want)
		}
	})
	t.Run("the start of a title, as the command line's own --wish-id", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		if code, out, errs := run("task", "cancel", "W5", "--wish-id", "Ship the"); code != 0 {
			t.Fatalf("task cancel W5 --wish-id 'Ship the': exit %d\n%s%s", code, out, errs)
		}
		want := &planv1.TaskServiceGetRequest{TaskId: "W5", WishId: docsID}
		if calls := f.seen(); len(calls) < 2 || !proto.Equal(calls[1], want) {
			t.Errorf("the server received %v, want the wishes listed, then %v", calls, want)
		}
	})
	t.Run("the start of several titles", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		code, _, errs := run("task", "spawn", "Ship", "--title", "Ship", "--project-id", projectID)
		want := "error: <wish-id>: 2 wishes are titled \"Ship\" or start so: give one by its UUID, " +
			wishID + " (Ship it) or " + docsID + " (Ship the docs)\n" +
			"  run djinn task spawn --help for the arguments\n"
		if code != 2 || errs != want {
			t.Errorf("exit %d, stderr\n%s\nwant 2 and\n%s", code, errs, want)
		}
		if calls := f.seen(); len(calls) != 1 {
			t.Errorf("the server received %v, want the wishes listed only", calls)
		}
	})
	// W481: what the lead asks a worker goes in a file it writes with its Write tool, on every command that takes a
	// prompt, as on djinn task spawn.
	t.Run("a prompt from a file", func(t *testing.T) {
		t.Parallel()
		f, run := serve(t)
		file := filepath.Join(t.TempDir(), "prompt.md")
		prompt := "**Gain**: one call.\n\n# Scope\n\nThe command line."
		if err := os.WriteFile(file, []byte(prompt+"\n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, out, errs := run("task", "update", taskID, "--prompt-file", file); code != 0 {
			t.Fatalf("task update --prompt-file: exit %d\n%s%s", code, out, errs)
		}
		want := &planv1.TaskServiceUpdateRequest{TaskId: taskID, Prompt: proto.String(prompt)}
		if calls := f.seen(); len(calls) != 1 || !proto.Equal(calls[0], want) {
			t.Errorf("the server received %v, want %v", calls, want)
		}
		if code, _, errs := run("task", "update", taskID, "--prompt", "x", "--prompt-file", file); code != 2 ||
			!strings.Contains(errs, "give --prompt or --prompt-file, not both") {
			t.Errorf("--prompt beside --prompt-file: exit %d, stderr\n%s", code, errs)
		}
		if code, _, errs := run("task", "update", taskID, "--prompt-file", file+".gone"); code != 2 ||
			!strings.Contains(errs, "--prompt-file: open ") {
			t.Errorf("--prompt-file of no file: exit %d, stderr\n%s", code, errs)
		}
		if code, out, _ := run("task", "update", "--help"); code != 0 || !strings.Contains(out, "--prompt-file") {
			t.Errorf("task update --help: exit %d\n%s", code, out)
		}
	})
	t.Run("djinn mcp names what fits", func(t *testing.T) {
		t.Parallel()
		_, answers := mcpSession(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_spawn","arguments":{"wish_id":"`+
			wishID+`","title":"Ship"}}}`)
		res := result[mcpCallResult](t, answers["1"])
		if !res.IsError || len(res.Content) != 1 || !strings.HasSuffix(res.Content[0].Text,
			"the wish has 2 projects: run it again with --project-id api or --project-id web") {
			t.Errorf("tool result %+v", res)
		}
	})
}
