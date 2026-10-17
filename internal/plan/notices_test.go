package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// refPusher finds the reference branch main, or fails with err.
type refPusher struct {
	testPusher
	err error
}

func (p *refPusher) Reference(ctx context.Context, project *planv1.Project, name string) (string, string, error) {
	if p.err != nil {
		return "", "", p.err
	}
	return p.testPusher.Reference(ctx, project, name)
}

// noticeLines are notices, one line each: kind, topic, subject, action and command.
func noticeLines(notices []*planv1.ProjectNotice) []string {
	var out []string
	for _, n := range notices {
		line := fmt.Sprintf("%s %s %s %s",
			strings.TrimPrefix(n.GetKind().String(), "PROJECT_NOTICE_KIND_"),
			strings.TrimPrefix(n.GetTopic().String(), "PROJECT_NOTICE_TOPIC_"), n.GetSubject(),
			strings.TrimPrefix(n.GetAction().String(), "PROJECT_NOTICE_ACTION_"))
		if n.GetCommand() != "" {
			line += " | " + n.GetCommand()
		}
		out = append(out, line)
	}
	return out
}

// TestProjectNotices (W146): djinn project show lists what Djinn finds in a project's configuration, things to fix
// first, each a sentence and one action: settings and permissions it cannot read or ignores in part, the setup's
// failed last run, the skills whose template, source or babysit it cannot use, a summoned skill it no longer finds,
// and a source this machine has not decided on. The projects list counts the things to fix.
func TestProjectNotices(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	dir := t.TempDir()
	add := func(dir string) *planv1.Project {
		t.Helper()
		res, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetProject()
	}
	project := add(dir)
	show := func() *planv1.ProjectServiceShowResponse {
		t.Helper()
		res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: project.GetName()}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	if res := show(); len(res.GetNotices()) > 0 || res.GetProject().GetToFix() != 0 {
		t.Fatalf("a bare folder: %v, to fix %d; want nothing", noticeLines(res.GetNotices()), res.GetProject().GetToFix())
	}

	devFile := DeveloperSettingsFile(home, project.GetId())
	writeSettings(t, filepath.Join(dir, SettingsFile), "model: \"opus\"\nfuture_setting: true\n")
	writeSettings(t, devFile, "setup: \"npm ci\"\n")
	writeSettings(t, filepath.Join(dir, PermissionsFile), "edit: true\nfuture_permission: true\n")
	agentSkill(t, dir, "bad-djinn", "metadata:\n  djinn: [1, 2]\n")
	agentSkill(t, dir, "bad-template", "metadata:\n  djinn:\n    wish:\n      title: Fix it\n")
	agentSkill(t, dir, "bad-source", "metadata:\n  djinn:\n    source:\n      every: 5m\n")
	agentSkill(t, dir, "bad-babysit", "metadata:\n  djinn:\n    babysit:\n      watch: sh watch.sh\n")
	agentSkill(t, dir, "mentions", "metadata:\n  djinn:\n    source:\n      watch: mentions --new\n")

	// A skill summoned from a project, then gone from it.
	other := t.TempDir()
	agentSkill(t, other, "shared", "")
	holder := add(other)
	if _, err := c.skills.Summon(ctx, connect.NewRequest(&planv1.SkillServiceSummonRequest{
		Skill: holder.GetName() + "/shared", Into: project.GetName(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(other, SkillFolders[0], "shared")); err != nil {
		t.Fatal(err)
	}

	// The setup failed with the command it still is; a passed run, or one of another command, is no notice.
	if err := c.store.Tx(ctx, func(tx *store.Tx) error {
		p, err := store.Get[*planv1.Project](ctx, tx, project.GetId())
		if err != nil {
			return err
		}
		p.CheckRuns = []*planv1.CheckRun{{Name: "setup", Setup: true, Command: "npm ci", ExitCode: 1, Reason: "npm: not found"}}
		if err := tx.Journal("test", "test/check", p); err != nil {
			return err
		}
		return tx.Put(p)
	}); err != nil {
		t.Fatal(err)
	}

	res := show()
	source := project.GetName() + "/mentions"
	want := []string{
		"TO_FIX SETUP npm ci FIX_FILE",
		"TO_FIX SKILL bad-babysit FIX_FILE",
		"TO_FIX SKILL bad-djinn FIX_FILE",
		"TO_FIX SKILL bad-source FIX_FILE",
		"TO_FIX SKILL bad-template FIX_FILE",
		"TO_FIX SKILL " + holder.GetName() + "/shared UNSUMMON_SKILL | djinn skill unsummon " + holder.GetName() +
			"/shared --from " + project.GetName(),
		"TO_FIX SOURCE " + source + " PLUG_SOURCE | djinn inbox plug " + source,
		"INFORMATION SETTINGS .agents/settings.txtpb UNSPECIFIED",
		"INFORMATION PERMISSIONS .agents/permissions.txtpb UNSPECIFIED",
	}
	if got := noticeLines(res.GetNotices()); !slices.Equal(got, want) {
		t.Errorf("notices:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, n := range res.GetNotices() {
		if n.GetText() == "" || strings.Count(n.GetText(), ". ") > 1 {
			t.Errorf("%s: %q, want one sentence", n.GetSubject(), n.GetText())
		}
		switch n.GetSubject() {
		case "npm ci":
			if n.GetFile() != devFile || n.GetDetail() != "npm: not found" {
				t.Errorf("the setup: file %s, detail %q; want %s, its reason", n.GetFile(), n.GetDetail(), devFile)
			}
		case "bad-djinn":
			// The project's folder, as Add resolved it: /private/var on macOS, the long name of RUNNER~1 on Windows.
			if n.GetFile() != filepath.Join(project.GetDirectory(), SkillFolders[0], "bad-djinn", SkillFile) ||
				!strings.HasPrefix(n.GetDetail(), "SKILL.md: metadata.djinn: ") {
				t.Errorf("an unreadable skill: file %s, detail %q", n.GetFile(), n.GetDetail())
			}
		case ".agents/settings.txtpb":
			if !strings.Contains(n.GetDetail(), "future_setting") {
				t.Errorf("an unknown setting: detail %q", n.GetDetail())
			}
		}
	}
	if res.GetProject().GetToFix() != 7 {
		t.Errorf("to fix: %d, want 7", res.GetProject().GetToFix())
	}
	list, err := c.projects.List(ctx, connect.NewRequest(&planv1.ProjectServiceListRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Msg.GetProjects() {
		if p.GetId() == project.GetId() && p.GetToFix() != 7 {
			t.Errorf("listed, to fix: %d, want 7", p.GetToFix())
		}
	}

	// A source decided on, plugged in or said no to, waits for nothing; plugged in again, the no is forgotten.
	undecided := func() bool {
		return slices.ContainsFunc(show().GetNotices(), func(n *planv1.ProjectNotice) bool { return n.GetSubject() == source })
	}
	if _, err := c.inbox.Plug(ctx, connect.NewRequest(&planv1.InboxServicePlugRequest{Source: source})); err != nil {
		t.Fatal(err)
	}
	if undecided() {
		t.Error("plugged in: still undecided")
	}
	unplug, err := c.inbox.Unplug(ctx, connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: source}))
	if err != nil || !unplug.Msg.GetSource().GetDeclined() || undecided() {
		t.Errorf("unplugged: %v, %v; want declined, decided", unplug, err)
	}
	plug, err := c.inbox.Plug(ctx, connect.NewRequest(&planv1.InboxServicePlugRequest{Source: source}))
	if err != nil || plug.Msg.GetSource().GetDeclined() {
		t.Errorf("plugged in again: %v, %v; want the no forgotten", plug, err)
	}
	if _, err := c.inbox.Unplug(ctx, connect.NewRequest(&planv1.InboxServiceUnplugRequest{Source: source})); err != nil {
		t.Fatal(err)
	}

	// A file Djinn cannot read: its notice says the file, and Djinn reads no setting of it.
	writeSettings(t, devFile, "model: sonnet\n")
	writeSettings(t, filepath.Join(dir, PermissionsFile), "edit: maybe\n")
	got := noticeLines(show().GetNotices())
	if !slices.Contains(got, "TO_FIX SETTINGS "+devFile+" FIX_FILE") ||
		!slices.Contains(got, "TO_FIX PERMISSIONS .agents/permissions.txtpb FIX_FILE") ||
		slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, " SETUP ") }) {
		t.Errorf("unreadable files: %s", strings.Join(got, "\n"))
	}
}

// TestProjectNoticesBranch (W146): a reference branch Djinn does not find is a thing to fix, named with
// main_branch; a copy with another branch checked out is an information.
func TestProjectNoticesBranch(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	pusher := &refPusher{}
	c := serve(t, WithHome(home), WithPusher(pusher))
	dir := gitRepo(t, "repo", "https://example.com/repo.git")
	if out, err := exec.Command("git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/feature").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
	if err != nil {
		t.Fatal(err)
	}
	project := add.Msg.GetProject()
	show := func() []*planv1.ProjectNotice {
		t.Helper()
		res, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: project.GetName()}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetNotices()
	}
	n := show()
	if got := noticeLines(n); !slices.Equal(got, []string{"INFORMATION BRANCH feature UNSPECIFIED"}) ||
		!strings.Contains(n[0].GetText(), "not the reference branch origin/main") {
		t.Errorf("on feature: %v", n)
	}

	if out, err := exec.Command("git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if n := show(); len(n) > 0 {
		t.Errorf("on main: %v, want nothing", noticeLines(n))
	}

	writeSettings(t, filepath.Join(dir, SettingsFile), "main_branch: \"dev\"\n")
	pusher.err = errors.New("the repository holds no origin/dev: fetch it, or name another branch")
	n = show()
	if got := noticeLines(n); !slices.Equal(got, []string{"TO_FIX BRANCH dev NAME_BRANCH"}) ||
		n[0].GetFile() != filepath.Join(dir, SettingsFile) || !strings.Contains(n[0].GetDetail(), "origin/dev") {
		t.Errorf("dev missing: %v", n)
	}
}
