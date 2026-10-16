package harness

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestStartSweepsCutWrites: djinn up removes, as it starts, the temporary files writes a kill cut left in Djinn's own
// folders, the plan folder of a project and the tilasms' folder, and nothing else.
func TestStartSweepsCutWrites(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	put := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	project := filepath.Join(home, "projects", "p1")
	cut := []string{
		put(filepath.Join(home, runDir, ".41.json.djinn-1.tmp")),
		put(filepath.Join(home, ".restart.json.djinn-2.tmp")),
		put(filepath.Join(project, ".settings.txtpb.djinn-3.tmp")),
		put(filepath.Join(project, ".installed.djinn-4.tmp")),
		put(filepath.Join(project, "worktrees", ".t1.setup.djinn-5.tmp")),
		put(filepath.Join(repo, "plan", ".a01.md.djinn-6.tmp")),
		put(filepath.Join(repo, "plan", "wish", ".a02.md.djinn-7.tmp")),
		put(filepath.Join(home, "tilasms", ".put-123", "index.html")),
	}
	kept := []string{
		put(filepath.Join(project, "settings.txtpb")),
		put(filepath.Join(project, "worktrees", "t1", ".a.djinn-8.tmp")), // In a worktree: the worker's own.
		put(filepath.Join(repo, "plan", "a01.md")),
		put(filepath.Join(repo, "plan", ".notes.tmp")),
		put(filepath.Join(repo, ".a.djinn-9.tmp")), // Outside the plan folder.
		put(filepath.Join(home, "tilasms", "01a1", "v1", "index.html")),
	}
	// In a plan folder, which another Djinn may share, one written a moment ago is a write on its way.
	onItsWay := put(filepath.Join(repo, "plan", ".a03.md.djinn-10.tmp"))
	kept = append(kept, onItsWay)
	long := time.Now().Add(-time.Hour)
	for _, path := range cut[5:7] {
		if err := os.Chtimes(path, long, long); err != nil {
			t.Fatal(err)
		}
	}
	// Recover sweeps what is in Djinn's folders; the plan folder of a project it knows once added.
	e := up(t, home)
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		p := &planv1.Project{Id: store.NewID(), Name: "repo", Directory: repo}
		if err := tx.Journal("test", "test/add", p); err != nil {
			return err
		}
		return tx.Put(p)
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.h.sweepTemp(t.Context()), cut[5]) {
		t.Errorf("the plan folder of a project was not swept")
	}
	for _, path := range cut {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s stays", path)
		}
	}
	for _, path := range kept {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed: %v", path, err)
		}
	}
}
