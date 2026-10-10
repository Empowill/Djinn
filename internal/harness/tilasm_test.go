package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
)

// TestSpawnTilasm: djinn task spawn --tilasm L01 opens the worker's first prompt with the tilasm's code, title, link,
// what it explains, the folder of its files and its text; a tilasm the wish does not have, a watcher and an azima are
// refused. djinn task get and list show each task the tilasms that cite it.
func TestSpawnTilasm(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	wishID, _ := e.wish(t, t.TempDir())
	spawn := func(req *planv1.TaskServiceSpawnRequest) (*planv1.Task, error) {
		req.WishId = wishID
		res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetTask(), nil
	}
	azima, err := spawn(&planv1.TaskServiceSpawnRequest{Title: "Tilasms", Kind: planv1.TaskKind_TASK_KIND_AZIMA})
	if err != nil {
		t.Fatal(err)
	}

	src := t.TempDir()
	index := "<html><head><title>The objects in the database</title></head><body><p>A wish holds tasks.</p>" +
		"<script>not text</script></body></html>"
	if err := os.WriteFile(filepath.Join(src, "index.html"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	tilasms := &plan.Tilasms{Store: e.db, Home: home}
	put, err := tilasms.Put(t.Context(), connect.NewRequest(&planv1.TilasmServicePutRequest{
		Path: src, Wish: wishID, Cites: []string{azima.GetCode()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	tilasm := put.Msg.GetTilasm()

	task, err := spawn(&planv1.TaskServiceSpawnRequest{
		Title: "Draw the model", Prompt: "Draw the model.", Provider: planv1.Provider_PROVIDER_FAKE, Later: true,
		Tilasm: []string{"l01," + tilasm.GetId()},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := firstPrompt(e.db, task.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Draw the model.\n\n# Tilasms given as context",
		"## L01 · The objects in the database",
		"- Link: djinn://tilasm/" + tilasm.GetId(),
		"- Explains: " + azima.GetCode(),
		"- Files, version 1: " + filepath.Join(home, "tilasms", tilasm.GetId(), "v1"),
		"(index.html)",
		"A wish holds tasks.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the first prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Count(prompt, "## L01") != 1 || strings.Contains(prompt, "not text") {
		t.Errorf("the tilasm is given once, without its scripts:\n%s", prompt)
	}

	if got := e.get(t, azima.GetId()).GetTilasms(); !slices.Equal(got, []string{"L01"}) {
		t.Errorf("task get %s: tilasms %v, want L01", azima.GetCode(), got)
	}
	if got := e.get(t, task.GetId()).GetTilasms(); len(got) != 0 {
		t.Errorf("task get %s: tilasms %v, want none: L01 does not cite it", task.GetCode(), got)
	}
	list, err := e.tasks.List(t.Context(), connect.NewRequest(&planv1.TaskServiceListRequest{WishId: wishID}))
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range list.Msg.GetTasks() {
		if want := listed.GetId() == azima.GetId(); want != slices.Equal(listed.GetTilasms(), []string{"L01"}) {
			t.Errorf("task list %s: tilasms %v", listed.GetCode(), listed.GetTilasms())
		}
	}

	for name, req := range map[string]*planv1.TaskServiceSpawnRequest{
		"a tilasm the wish does not have": {Title: "x", Provider: planv1.Provider_PROVIDER_FAKE, Later: true, Tilasm: []string{"L09"}},
		"a watcher":                       {Title: "x", Provider: planv1.Provider_PROVIDER_WATCH, Prompt: "true", Tilasm: []string{"L01"}},
		"an azima":                        {Title: "x", Kind: planv1.TaskKind_TASK_KIND_AZIMA, Tilasm: []string{"L01"}},
	} {
		if _, err := spawn(req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("--tilasm for %s: %v, want invalid argument", name, err)
		}
	}
}
