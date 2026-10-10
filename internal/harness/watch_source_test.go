package harness

import (
	"context"
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
	"github.com/empowill/djinn/internal/testx"
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

// sourceSkill writes in dir a skill named name whose inbox source runs command.
func sourceSkill(t *testing.T, dir, name, command string) {
	t.Helper()
	skill := filepath.Join(dir, ".agents", "skills", name)
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: " + name + "\ndescription: What is assigned to me.\nmetadata:\n  djinn:\n    source:\n      watch: " +
		strconv.Quote(command) + "\n---\n"
	if err := os.WriteFile(filepath.Join(skill, plan.SkillFile), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCount is how many times the fake command that writes to runs has started.
func runCount(t *testing.T, runs string) int {
	t.Helper()
	b, err := os.ReadFile(runs)
	if os.IsNotExist(err) {
		return 0
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

// sourcesUp runs the inbox sources of e with watchers fast enough for a test, their skills read again only when
// scan asks. said gets the text of each item a source printed, once the inbox has it.
func sourcesUp(e *env) (scan func() int, said <-chan string) {
	inbox := &plan.Inbox{Wishes: &plan.Wishes{Store: e.db, Language: "en"}}
	out := make(chan string, 16)
	scan = e.h.runSources(func(ctx context.Context, src *plan.Source, text string) (*planv1.InboxItem, error) {
		item, err := inbox.Receive(ctx, src, text)
		out <- text
		return item, err
	}, Watch{Quiet: 50 * time.Millisecond}, time.Hour)
	return scan, out
}

// nextSaid is the next item a source printed.
func nextSaid(t *testing.T, said <-chan string) string {
	t.Helper()
	select {
	case text := <-said:
		return text
	case <-time.After(10 * time.Second):
		t.Fatal("no item came")
		return ""
	}
}

// plugSource plugs in the source named name, through the API.
func plugSource(t *testing.T, client planv1connect.InboxServiceClient, name string) {
	t.Helper()
	if _, err := client.Plug(t.Context(), connect.NewRequest(&planv1.InboxServicePlugRequest{Source: name})); err != nil {
		t.Fatal(err)
	}
}

// TestSourceReadsOnly: a skill's source runs like a watcher, and each item it prints becomes an inbox item. Its
// command reads an empty input: Djinn writes nothing to it. Dismissing an item and routing one run no command: the
// source ran once, as declared, and the project's folder is as it was.
func TestSourceReadsOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	dir := t.TempDir()
	runs := filepath.Join(t.TempDir(), "runs")
	command := watchCommand("input:"+runs, "say:Babysit !12 · Fix the wick",
		"say:https://gitlab.example.com/acme/gong/-/merge_requests/12", "say:", "say:carol mentioned you")
	sourceSkill(t, dir, "assigned", command)
	before := files(t, dir)
	scan, said := sourcesUp(e)
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}
	client := planv1connect.NewInboxServiceClient(e.srv.Client(), e.srv.URL)
	plugSource(t, client, "assigned")
	scan()
	nextSaid(t, said)
	nextSaid(t, said)

	res, err := client.List(t.Context(), connect.NewRequest(&planv1.InboxServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	items := res.Msg.GetItems()
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
	// Two more passes: neither starts the command again, its watcher waiting its five minutes.
	if n := scan() + scan(); n != 2 {
		t.Errorf("%d sources running over two passes, want one each", n)
	}
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
// that is not never runs, plugged in or not.
func TestSourceRefused(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := up(t, home)
	dir := t.TempDir()
	runs := filepath.Join(t.TempDir(), "runs")
	sourceSkill(t, dir, "assigned", watchCommand("input:"+runs, "say:hello"))
	if err := os.WriteFile(filepath.Join(dir, PermissionsFile), []byte(`commands: "go test"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, _ := sourcesUp(e)
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}
	plugSource(t, planv1connect.NewInboxServiceClient(e.srv.Client(), e.srv.URL), "assigned")
	if n := scan(); n != 0 {
		t.Errorf("%d sources running, want none", n)
	}
	if _, err := os.Stat(runs); !os.IsNotExist(err) {
		t.Errorf("a source the project does not allow ran: %v", err)
	}
}

// TestSourceUnplugged: a source runs only once plugged in on this machine. Declared by a project's skill and not
// plugged in, its command never starts; plugged in, it starts once, and passes start no other; unplugged, its watcher
// stops, and plugging it in again starts a new one.
func TestSourceUnplugged(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	e := up(t, t.TempDir())
	dir := t.TempDir()
	runs := filepath.Join(t.TempDir(), "runs")
	sourceSkill(t, dir, "assigned", watchCommand("input:"+runs, "say:carol mentioned you", "wait"))
	scan, said := sourcesUp(e)
	if _, err := e.project.Add(t.Context(), connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir})); err != nil {
		t.Fatal(err)
	}
	if n := scan(); n != 0 {
		t.Fatalf("%d sources running before the plug, want none", n)
	}
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("a source not plugged in started %d commands, want 0", n)
	}

	client := planv1connect.NewInboxServiceClient(e.srv.Client(), e.srv.URL)
	plugSource(t, client, "assigned")
	if n := scan(); n != 1 {
		t.Fatalf("%d sources running once plugged in, want 1", n)
	}
	if text := nextSaid(t, said); text != "carol mentioned you" {
		t.Errorf("said %q", text)
	}
	if n := scan() + scan(); n != 2 || runCount(t, runs) != 1 {
		t.Fatalf("over two more passes: %d running, %d commands started; want 1 each time, 1 command", n, runCount(t, runs))
	}

	res, err := client.Unplug(t.Context(), connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: "assigned"}))
	if err != nil || res.Msg.GetSource().GetPlugged() {
		t.Fatalf("unplug: %v, %v", res, err)
	}
	if n := scan(); n != 0 {
		t.Fatalf("%d sources running once unplugged, want none", n)
	}
	plugSource(t, client, "assigned")
	if n := scan(); n != 1 {
		t.Fatalf("%d sources running once plugged in again, want 1", n)
	}
	nextSaid(t, said)
	if n := runCount(t, runs); n != 2 {
		t.Errorf("the source started %d commands, want 2", n)
	}
}
