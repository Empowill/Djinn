package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// configWatch is a project whose remote is a bare repository on the disk, and a harness to watch its configuration:
// every fetch is a real one, without network.
type configWatch struct {
	t            *testing.T
	remote, repo string
	home         string
	db           *store.Store
	project      *planv1.Project
	h            *Harness
}

// newConfigWatch is a project holding files on main, pushed to its bare remote and fetched; the watch not started.
func newConfigWatch(t *testing.T, files map[string]string) *configWatch {
	t.Helper()
	ctx := t.Context()
	w := &configWatch{t: t, remote: t.TempDir(), home: t.TempDir()}
	if _, err := git(ctx, w.remote, "init", "--quiet", "--bare"); err != nil {
		t.Fatal(err)
	}
	w.repo = gitRepoWith(t, files)
	for _, args := range [][]string{
		{"branch", "-M", "main"}, {"remote", "add", "origin", w.remote}, {"push", "--quiet", "origin", "main"},
		{"fetch", "--quiet", "origin"},
	} {
		if _, err := git(ctx, w.repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	w.db = db
	w.project = &planv1.Project{Id: store.NewID(), Name: "app", Directory: w.repo, Git: true}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/add", w.project); err != nil {
			return err
		}
		return tx.Put(w.project)
	}); err != nil {
		t.Fatal(err)
	}
	w.h = New(db, w.home, testProviders(), WithConfigTick(time.Hour))
	t.Cleanup(func() {
		w.h.Close()
		db.Close()
	})
	return w
}

// changes is the project's journal of changes, the newest first.
func (w *configWatch) changes() []*planv1.ConfigChange {
	w.t.Helper()
	c, err := plan.ProjectChanges(w.t.Context(), w.db, w.project.GetId())
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

// until waits for ok, what saying for what.
func (w *configWatch) until(what string, ok func() bool) {
	w.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatalf("waited for %s", what)
		}
	}
}

// mate is a teammate's clone of the remote, on main.
func (w *configWatch) mate() string {
	w.t.Helper()
	other := filepath.Join(w.t.TempDir(), "other")
	if _, err := git(w.t.Context(), filepath.Dir(other), "clone", "--quiet", "--branch", "main", w.remote, other); err != nil {
		w.t.Fatal(err)
	}
	appendFile(w.t, filepath.Join(other, ".git", "config"), "[user]\n\tname = Mate\n\temail = mate@example.com\n")
	return other
}

// gits runs each git command of commands in dir.
func (w *configWatch) gits(dir string, commands ...[]string) {
	w.t.Helper()
	for _, args := range commands {
		if _, err := git(w.t.Context(), dir, args...); err != nil {
			w.t.Fatal(err)
		}
	}
}

// TestWatchConfig (W151): the watch looks at once at each project's reference branch, fetches it alone from the
// remote, and, woken (as a push of Djinn wakes it), tells the change a teammate pushed there in the journal of changes.
func TestWatchConfig(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	w := newConfigWatch(t, map[string]string{".agents/djinn.yaml": teamYAML("setup: npm ci\n")})
	w.h.WatchConfig()
	w.until("the first look", func() bool { return len(w.changes()) == 1 })

	// A teammate pushes a permission on main.
	other := w.mate()
	if err := os.WriteFile(filepath.Join(other, ".agents", "permissions.txtpb"), []byte("commands: \"curl\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.gits(other, []string{"add", "."}, []string{"commit", "--quiet", "-m", "Allow curl"}, []string{"push", "--quiet", "origin", "main"})
	pushed, _ := git(ctx, other, "rev-parse", "HEAD")
	w.h.kickConfig()
	w.until("the change told", func() bool { return len(w.changes()) == 2 })
	c := w.changes()[0]
	if c.GetAuthor() != "Mate" || c.GetTitle() != "Allow curl" || c.GetCommit() != pushed || !plan.Waits(c) {
		t.Errorf("the change: %v", c)
	}
	if got := c.GetLines(); len(got) != 1 || got[0].GetText() != "Permissions: 1 more command the workers may run: curl." {
		t.Errorf("lines: %v", got)
	}
	if fetched, _ := git(ctx, w.repo, "rev-parse", "refs/remotes/origin/main"); fetched != pushed {
		t.Errorf("origin/main at %s, want %s", fetched, pushed)
	}
}

// TestWatchConfigBranch (W179, Q110): the developer's config_branch, followed by real fetches from a bare remote. Not
// on the remote yet: Djinn reads the configuration on main, a warning says so. A teammate pushes it: the next look
// fetches it, and Djinn reads its djinn.yaml there, the permissions staying main's, the journal telling no permission
// of the branch. Pushed again: the next look follows it. Merged and deleted from the remote: the next look drops its
// remote-tracking ref, and Djinn reads main again, the warning back; never an error for the work.
func TestWatchConfigBranch(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	w := newConfigWatch(t, map[string]string{
		".agents/djinn.yaml":        teamYAML("setup: echo main\n"),
		".agents/permissions.txtpb": "commands: \"make\"\n",
	})
	if err := plan.SaveDeveloperConfigBranch(w.home, w.project.GetId(), "djinn/gates"); err != nil {
		t.Fatal(err)
	}
	projects := &plan.Projects{Store: w.db, Home: w.home}
	setup := func() string {
		t.Helper()
		s, err := plan.LoadSettings(w.home, w.project)
		if err != nil {
			t.Fatalf("settings: %v", err) // the work reads them: never an error, branch there or not
		}
		return s.Setup
	}
	// reads checks where Djinn reads the configuration, the setup it gives, the permissions it applies (main's) and
	// the project's notice on the configuration branch.
	reads := func(when, ref, wantSetup string, wantPerms []string, kind planv1.ProjectNoticeKind, text string) {
		t.Helper()
		c := plan.ProjectConfig(w.home, w.project)
		if c.Ref != ref || c.ConfigBranch != "djinn/gates" || c.ConfigBranchMissing() != (ref == "origin/main") {
			t.Errorf("%s: configuration read on %q (missing %v), want %s", when, c.Ref, c.ConfigBranchMissing(), ref)
		}
		if got := setup(); got != wantSetup {
			t.Errorf("%s: setup %q, want %q", when, got, wantSetup)
		}
		perms, _, err := plan.ReadPermissions(plan.PermissionsDir(w.home, w.project))
		if err != nil || !slices.Equal(perms.GetCommands(), wantPerms) {
			t.Errorf("%s: permissions %v, %v; want main's, %v", when, perms.GetCommands(), err, wantPerms)
		}
		res, err := projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "app"}))
		if err != nil {
			t.Fatal(err)
		}
		var notice *planv1.ProjectNotice
		for _, n := range res.Msg.GetNotices() {
			if n.GetSubject() == "djinn/gates" {
				notice = n
			}
		}
		if notice.GetKind() != kind || !strings.Contains(notice.GetText(), text) ||
			notice.GetCommand() != "djinn project set-branch app --config --unset" {
			t.Errorf("%s: notice %v, want %v %q", when, notice, kind, text)
		}
		if watch := res.Msg.GetWatch(); watch.GetFetchError() != "" {
			t.Errorf("%s: the watch kept %q", when, watch.GetFetchError())
		}
	}
	gone := "config_branch djinn/gates is not on the remote (merged and deleted, or not fetched yet): Djinn reads the " +
		"configuration on origin/main."
	w.h.WatchConfig()
	w.until("the first look", func() bool { return len(w.changes()) == 1 })
	reads("not pushed yet", "origin/main", "echo main", []string{"make"}, planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_WARNING, gone)

	// A teammate pushes the branch, which allows curl too.
	other := w.mate()
	w.gits(other, []string{"checkout", "--quiet", "-b", "djinn/gates"})
	writeFile(t, other, ".agents/djinn.yaml", teamYAML("setup: echo gates\n"))
	writeFile(t, other, ".agents/permissions.txtpb", "commands: \"make\"\ncommands: \"curl\"\n")
	w.gits(other, []string{"add", "."}, []string{"commit", "--quiet", "-m", "Gates"},
		[]string{"push", "--quiet", "origin", "djinn/gates"})
	w.h.kickConfig()
	w.until("the branch fetched", func() bool { return plan.ProjectConfig(w.home, w.project).Ref == "origin/djinn/gates" })
	reads("pushed", "origin/djinn/gates", "echo gates", []string{"make"}, planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_INFORMATION,
		"Configuration read from origin/djinn/gates, not origin/main")
	w.until("the move told", func() bool { return len(w.changes()) == 2 })
	if got := w.changes()[0].GetLines(); len(got) != 2 ||
		got[0].GetText() != "Djinn now reads the configuration on origin/djinn/gates, not origin/main." ||
		got[1].GetTopic() == planv1.ProjectNoticeTopic_PROJECT_NOTICE_TOPIC_PERMISSIONS {
		t.Errorf("the move to the branch: %v", got)
	}

	// Pushed again.
	writeFile(t, other, ".agents/djinn.yaml", teamYAML("setup: echo gates again\n"))
	w.gits(other, []string{"commit", "--quiet", "-am", "Gates again"}, []string{"push", "--quiet", "origin", "djinn/gates"})
	w.h.kickConfig()
	w.until("the branch followed", func() bool {
		s, err := plan.LoadSettings(w.home, w.project)
		return err == nil && s.Setup == "echo gates again"
	})
	reads("pushed again", "origin/djinn/gates", "echo gates again", []string{"make"},
		planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_INFORMATION, "Configuration read from origin/djinn/gates")

	// Merged into main, then deleted from the remote.
	w.gits(other, []string{"checkout", "--quiet", "main"}, []string{"merge", "--quiet", "--no-ff", "-m", "Merge gates", "djinn/gates"},
		[]string{"push", "--quiet", "origin", "main", ":djinn/gates"})
	w.h.kickConfig()
	w.until("the branch dropped", func() bool { return plan.ProjectConfig(w.home, w.project).Ref == "origin/main" })
	if _, err := git(ctx, w.repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/djinn/gates"); err == nil {
		t.Errorf("origin/djinn/gates still in the copy")
	}
	w.until("main fetched", func() bool { return setup() == "echo gates again" })
	reads("deleted", "origin/main", "echo gates again", []string{"make", "curl"}, planv1.ProjectNoticeKind_PROJECT_NOTICE_KIND_WARNING, gone)
}
