package harness

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
)

// files are the files under dir, with their contents.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSourceReadsOnly: a skill's source runs like a watcher, and each item it prints becomes an inbox item. Its
// command reads an empty input: Djinn writes nothing to it. Dismissing an item and routing one run no command: the
// source ran once, as declared, and the project's folder is as it was.
func TestSourceReadsOnly(t *testing.T) {
	home := t.TempDir()
	e := up(t, home)
	dir := t.TempDir()
	runs := filepath.Join(t.TempDir(), "runs")
	command := watchCommand("input:"+runs, "say:Babysit !12 · Fix the wick",
		"say:https://gitlab.example.com/acme/gong/-/merge_requests/12", "say:", "say:carol mentioned you")
	skill := filepath.Join(dir, ".agents", "skills", "assigned")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: assigned\ndescription: What is assigned to me.\nmetadata:\n  djinn:\n    source:\n      watch: " +
		strconv.Quote(command) + "\n---\n"
	if err := os.WriteFile(filepath.Join(skill, plan.SkillFile), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	before := files(t, dir)
	inbox := &plan.Inbox{Wishes: &plan.Wishes{Store: e.db, Language: "en"}}
	e.h.runSources(inbox.Receive, Watch{Quiet: 50 * time.Millisecond}, 50*time.Millisecond)
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}

	client := planv1connect.NewInboxServiceClient(e.srv.Client(), e.srv.URL)
	var items []*planv1.InboxItem
	deadline := time.Now().Add(20 * time.Second)
	for len(items) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		res, err := client.List(t.Context(), connect.NewRequest(&planv1.InboxServiceListRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		items = res.Msg.GetItems()
	}
	var texts []string
	for _, i := range items {
		texts = append(texts, i.GetSource()+": "+i.GetText())
	}
	if want := []string{"assigned: carol mentioned you",
		"assigned: Babysit !12 · Fix the wick\nhttps://gitlab.example.com/acme/gong/-/merge_requests/12"}; !slices.Equal(texts, want) {
		t.Fatalf("items %q, want %q", texts, want)
	}

	if _, err := client.Dismiss(t.Context(), connect.NewRequest(&planv1.InboxServiceDismissRequest{ItemId: items[0].GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Route(t.Context(), connect.NewRequest(&planv1.InboxServiceRouteRequest{
		ItemId: items[1].GetId(), Choice: planv1.Choice_CHOICE_A,
	})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // Several rescans: none starts the command again.
	b, err := os.ReadFile(runs)
	if err != nil || strings.Split(strings.TrimSpace(string(b)), "\n")[0] != "read 0 bytes" || strings.Count(string(b), "\n") != 1 {
		t.Errorf("the source's runs: %q, %v; want one run that read nothing", b, err)
	}
	if after := files(t, dir); len(after) != len(before) {
		t.Errorf("the project's folder changed: %v", after)
	}
	wishes, err := e.wishes.List(t.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
	if err != nil || len(wishes.Msg.GetWishes()) != 1 || wishes.Msg.GetWishes()[0].GetTitle() != "Babysit !12 · Fix the wick" {
		t.Errorf("wishes = %v, %v", wishes.Msg.GetWishes(), err)
	}
}

// TestSourceRefused: where a project lists the commands its workers may run, a source must be one of them; one
// that is not never runs.
func TestSourceRefused(t *testing.T) {
	home := t.TempDir()
	e := up(t, home)
	dir := t.TempDir()
	runs := filepath.Join(t.TempDir(), "runs")
	skill := filepath.Join(dir, ".agents", "skills", "assigned")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: assigned\ndescription: x\nmetadata:\n  djinn:\n    source:\n      watch: " +
		strconv.Quote(watchCommand("input:"+runs, "say:hello")) + "\n---\n"
	if err := os.WriteFile(filepath.Join(skill, plan.SkillFile), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, PermissionsFile), []byte(`commands: "go test"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inbox := &plan.Inbox{Wishes: &plan.Wishes{Store: e.db, Language: "en"}}
	e.h.runSources(inbox.Receive, Watch{Quiet: 50 * time.Millisecond}, 50*time.Millisecond)
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Errorf("a source the project does not allow ran: %v", err)
	}
}
