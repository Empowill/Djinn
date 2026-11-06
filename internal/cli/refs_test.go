package cli

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestResolve (W474): the command line sends a project named by its name as its identifier, and takes --wish for
// --wish-id where a request spells it so; a refusal that says what fits prints the command again with each value,
// a long text cut to "…", and djinn mcp names the field and the values.
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
	t.Run("the commands that fit", func(t *testing.T) {
		t.Parallel()
		_, run := serve(t)
		prompt := strings.Repeat("Order the accounts. ", 20)
		code, _, errs := run("task", "spawn", wishID, "--title", "Ship it", "--prompt", prompt)
		want := "error: invalid_argument: the wish has 2 projects: name one with --project-id\n" +
			"  run it again with what fits (… stands for the long text you gave):\n" +
			"  djinn task spawn " + wishID + " --title 'Ship it' --project-id api --prompt …\n" +
			"  djinn task spawn " + wishID + " --title 'Ship it' --project-id web --prompt …\n"
		if code != 1 || errs != want {
			t.Errorf("exit %d, stderr\n%s\nwant 1 and\n%s", code, errs, want)
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
	t.Run("a wish that is no UUID", func(t *testing.T) {
		t.Parallel()
		_, run := serve(t)
		code, _, errs := run("task", "done", "W5", "--wish-id", "Dew")
		if code != 2 || !strings.Contains(errs, `--wish-id: "Dew" is not a wish's UUID; expected a UUID`) {
			t.Errorf("exit %d, stderr\n%s", code, errs)
		}
	})
	t.Run("djinn mcp names what fits", func(t *testing.T) {
		t.Parallel()
		_, answers := mcpSession(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_spawn","arguments":{"wish_id":"`+
			wishID+`","title":"Ship"}}}`)
		res := result[mcpCallResult](t, answers["1"])
		if !res.IsError || len(res.Content) != 1 || !strings.HasSuffix(res.Content[0].Text,
			"\ncall it again with project_id set to one of: api; web") {
			t.Errorf("tool result %+v", res)
		}
	})
}
