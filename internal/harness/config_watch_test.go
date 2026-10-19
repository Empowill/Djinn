package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestWatchConfig (W151): the watch looks at once at each project's reference branch, fetches it alone from the
// remote, and, woken (as a push of Djinn wakes it), tells the change a teammate pushed there in the journal of changes.
func TestWatchConfig(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	ctx := t.Context()
	remote := t.TempDir()
	if _, err := git(ctx, remote, "init", "--quiet", "--bare"); err != nil {
		t.Fatal(err)
	}
	repo := gitRepoWith(t, map[string]string{".agents/settings.txtpb": "setup: \"npm ci\"\n"})
	for _, args := range [][]string{
		{"branch", "-M", "main"}, {"remote", "add", "origin", remote}, {"push", "--quiet", "origin", "main"},
		{"fetch", "--quiet", "origin"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	project := &planv1.Project{Id: store.NewID(), Name: "app", Directory: repo, Git: true}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/add", project); err != nil {
			return err
		}
		return tx.Put(project)
	}); err != nil {
		t.Fatal(err)
	}
	h := New(db, t.TempDir(), testProviders(), WithConfigTick(time.Hour))
	t.Cleanup(func() {
		h.Close()
		db.Close()
	})
	h.WatchConfig()
	changes := func() []*planv1.ConfigChange {
		t.Helper()
		c, err := plan.ProjectChanges(ctx, db, project.GetId())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("waited for %s", what)
			}
		}
	}
	until("the first look", func() bool { return len(changes()) == 1 })

	// A teammate pushes a permission on main.
	other := filepath.Join(t.TempDir(), "other")
	if _, err := git(ctx, filepath.Dir(other), "clone", "--quiet", "--branch", "main", remote, other); err != nil {
		t.Fatal(err)
	}
	appendFile(t, filepath.Join(other, ".git", "config"), "[user]\n\tname = Mate\n\temail = mate@example.com\n")
	if err := os.WriteFile(filepath.Join(other, ".agents", "permissions.txtpb"), []byte("commands: \"curl\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", "Allow curl"}, {"push", "--quiet", "origin", "main"}} {
		if _, err := git(ctx, other, args...); err != nil {
			t.Fatal(err)
		}
	}
	pushed, _ := git(ctx, other, "rev-parse", "HEAD")
	h.kickConfig()
	until("the change told", func() bool { return len(changes()) == 2 })
	c := changes()[0]
	if c.GetAuthor() != "Mate" || c.GetTitle() != "Allow curl" || c.GetCommit() != pushed || !plan.Waits(c) {
		t.Errorf("the change: %v", c)
	}
	if got := c.GetLines(); len(got) != 1 || got[0].GetText() != "Permissions: 1 more command the workers may run: curl." {
		t.Errorf("lines: %v", got)
	}
	if fetched, _ := git(ctx, repo, "rev-parse", "refs/remotes/origin/main"); fetched != pushed {
		t.Errorf("origin/main at %s, want %s", fetched, pushed)
	}
}
