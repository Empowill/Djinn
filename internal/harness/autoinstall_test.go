package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestABuildOfTheRunningLineInstallsItself (W554): a build Djinn pushed past the push checks installs by itself when it
// is of the running Djinn's line, its branch or one that carries it on, and install_builds is on; a parallel branch's
// build, a project with no push check, install_builds: false, or the build installed already, are only offered; so is
// a build of the line that does not hold another line's install, newer than the running Djinn.
func TestABuildOfTheRunningLineInstallsItself(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	repo := gitRepo(t)
	e := up(t, t.TempDir())
	wishID, projectID := e.wish(t, repo)
	ctx := t.Context()
	project, err := store.Get[*planv1.Project](ctx, e.db, projectID)
	if err != nil {
		t.Fatal(err)
	}
	g := func(args ...string) string {
		t.Helper()
		out, err := git(ctx, repo, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	g("branch", "feat/robustness")
	g("branch", "feat/side")
	side, err := e.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Side", ProjectIds: []string{projectID}}))
	if err != nil {
		t.Fatal(err)
	}
	for wish, branch := range map[string]string{wishID: "feat/robustness", side.Msg.GetWish().GetId(): "feat/side"} {
		if _, err := e.wishes.SetIntegration(ctx, connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wish, ProjectId: projectID, Branch: branch,
		})); err != nil {
			t.Fatal(err)
		}
	}
	// merge commits a task's work and merges it into into, as the integration does, and returns the merge.
	merge := func(wID, code, into string) string {
		branch := code + "-work-0000000" + code[1:]
		g("switch", "--quiet", "-c", branch, into)
		writeFile(t, repo, "app/src/"+code+".txt", code+"\n")
		commitAll(t, repo, "Work of "+code)
		g("switch", "--quiet", into)
		g("merge", "--no-ff", "--quiet", "-m", "Merge branch '"+branch+"' into "+into, branch)
		if err := e.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
				return err
			}
			return tx.Put(&planv1.Task{
				Id: store.NewID(), WishId: wID, ProjectId: projectID, Code: code, Title: "Title of " + code,
				Branch: branch, Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now(),
			})
		}); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(g("rev-parse", "HEAD"))
	}
	running := merge(wishID, "W1", "feat/robustness")
	ahead := merge(wishID, "W2", "feat/robustness")
	parallel := merge(side.Msg.GetWish().GetId(), "W3", "feat/side")
	g("switch", "--quiet", "feat/robustness")
	e.h.version = "local-" + running[:7]

	settingsFile := filepath.Join("projects", projectID, "settings.txtpb")
	const checked = "install: \"install\"\nchecks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n"
	auto := func(text, branch, sha string) bool {
		t.Helper()
		writeFile(t, e.home, settingsFile, text)
		settings, err := plan.LoadSettings(e.home, project)
		if err != nil {
			t.Fatal(err)
		}
		b := Built{Branch: branch, Sha: sha}
		e.h.compare(ctx, project, &b)
		return e.h.installsItself(ctx, project, settings, b)
	}
	if !auto(checked, "feat/robustness", ahead) {
		t.Error("the running line's build, push checked: offered only")
	}
	if auto(checked, "feat/side", parallel) {
		t.Error("a parallel branch's build installs by itself")
	}
	if auto(checked+"install_builds: false\n", "feat/robustness", ahead) {
		t.Error("install_builds: false, and the line's build installs by itself")
	}
	if auto("install: \"install\"\nchecks { name: \"lint\" command: \"lint\" when: CHECK_WHEN_COMMIT }\n", "feat/robustness", ahead) {
		t.Error("no push check passed, and the build installs by itself")
	}
	// Installed already: not again. A newer build of the line holds it: installs.
	e.h.mu.Lock()
	e.h.installed[projectID] = ahead
	e.h.mu.Unlock()
	if auto(checked, "feat/robustness", ahead) {
		t.Error("the build installed already installs again")
	}
	newer := merge(wishID, "W4", "feat/robustness")
	if !auto(checked, "feat/robustness", newer) {
		t.Error("the line's newer build, holding the one installed: offered only")
	}
	// The other branch's build installed by hand, newer than the running Djinn: the line's build does not overwrite it.
	e.h.mu.Lock()
	e.h.installed[projectID] = parallel
	e.h.mu.Unlock()
	if auto(checked, "feat/robustness", newer) {
		t.Error("the line's build overwrites another branch's install")
	}
}

// TestInstallTellsItsLines: Install's commands tell each line they write to the window (InstallWith), the fake's as
// the real one's.
func TestInstallTellsItsLines(t *testing.T) {
	t.Parallel()
	var lines []string
	w := &lineWriter{line: func(l string) { lines = append(lines, l) }}
	for _, chunk := range []string{"task: [ui] npm ", "run build\r\nbuilt\n", "task: [install] go build"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.flush()
	if want := []string{"task: [ui] npm run build", "built", "task: [install] go build"}; !slices.Equal(lines, want) {
		t.Errorf("lines %q, want %q", lines, want)
	}

	lines = nil
	h := &Harness{commands: func(context.Context, string, []string) (string, int, error) { return "one\ntwo\n", 0, nil }}
	ctx := InstallWith(t.Context(), func(l string) { lines = append(lines, l) }, true)
	if out, _, err := h.command(ctx, installGate, "", "install", t.TempDir()); err != nil || !strings.HasPrefix(out, "one") {
		t.Fatalf("command: %q, %v", out, err)
	}
	if !slices.Equal(lines, []string{"one", "two"}) {
		t.Errorf("lines %q, want one, two", lines)
	}
}
