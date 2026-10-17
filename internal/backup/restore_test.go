package backup

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// TestRestore: an archive comes back as the data folder, the projects this machine lacks detached; the old folder
// goes aside, its worktrees kept at their paths, each move tried again when refused a moment. A running djinn, or an
// archive that is not a backup of Djinn, is refused.
func TestRestore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// One data folder backed up both ways, and one archive the cases below restore over a folder of their own.
	src, s, here := fullHome(t)
	archive := newArchive(t, "new")
	for _, ext := range []string{".tar.gz", ".zip"} {
		backup := filepath.Join(t.TempDir(), "backup"+ext)
		if _, err := Create(ctx, src, backup, "test", s.Snapshot); err != nil {
			t.Fatal(err)
		}
		t.Run("round trip "+ext, func(t *testing.T) {
			t.Parallel()
			restored := filepath.Join(t.TempDir(), "restored")
			out, err := Restore(ctx, restored, backup, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if out.Aside != "" || out.Files != 4 || !slices.Equal(out.Detached, []string{"gone"}) {
				t.Errorf("Restore = %+v, want 4 files, gone detached, nothing aside", out)
			}
			if got := readFile(t, filepath.Join(restored, "wishes", "w1", "page.html")); got != "<p>page</p>" {
				t.Errorf("page = %q", got)
			}
			if _, err := os.Stat(filepath.Join(restored, "projects", "p1", "worktrees")); !os.IsNotExist(err) {
				t.Errorf("a worktree was restored: %v", err)
			}
			r := openRestored(t, restored)
			projects, err := store.List[*planv1.Project](ctx, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			dirs := map[string]string{}
			for _, p := range projects {
				dirs[p.GetName()] = p.GetDirectory()
			}
			if dirs["here"] != here || dirs["gone"] != "" {
				t.Errorf("project folders = %v, want here kept and gone emptied", dirs)
			}
			journal, err := store.Commands(ctx, r, func(c store.Command) bool { return c.Method == methodRestore })
			if err != nil || len(journal) != 1 {
				t.Errorf("journal of the restore = %v, %v; want one entry", journal, err)
			}
		})
	}

	t.Run("old folder aside", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "djinn")
		writeFile(t, home, "state.json", "old")
		writeFile(t, home, "djinn.log", "old log")
		writeFile(t, home, "projects/p1/worktrees/t1/main.go", "work in progress")
		now := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)
		out, err := Restore(ctx, home, archive, now)
		if err != nil {
			t.Fatal(err)
		}
		if want := home + ".before-restore-2026-10-08-093000"; out.Aside != want {
			t.Errorf("aside = %s, want %s", out.Aside, want)
		}
		if got := readFile(t, filepath.Join(home, "state.json")); got != "new" {
			t.Errorf("state = %q, want the restored one", got)
		}
		if got := readFile(t, filepath.Join(out.Aside, "state.json")); got != "old" {
			t.Errorf("state aside = %q, want the old one", got)
		}
		if got := readFile(t, filepath.Join(home, "projects", "p1", "worktrees", "t1", "main.go")); got != "work in progress" {
			t.Errorf("worktree = %q, want it at its path", got)
		}
		// A second restore the same second finds a free name.
		out2, err := Restore(ctx, home, archive, now)
		if err != nil {
			t.Fatal(err)
		}
		if out2.Aside == out.Aside || !strings.HasPrefix(out2.Aside, out.Aside) {
			t.Errorf("second aside = %s", out2.Aside)
		}
	})

	// Each move of a restore (the old folder aside, the restored one in place, the worktrees) goes through though
	// refused a moment.
	t.Run("renames refused a moment", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "djinn")
		writeFile(t, home, "state.json", "old")
		writeFile(t, home, "projects/p1/worktrees/t1/main.go", "work in progress")
		r, refused := refusing()
		out, err := restore(ctx, home, archive, time.Now(), r)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{out.Aside, home, filepath.Join(home, "projects", "p1", "worktrees")}; !slices.Equal(*refused, want) {
			t.Errorf("refused %v, want the 3 moves once each: %v", *refused, want)
		}
		if got := readFile(t, filepath.Join(home, "state.json")); got != "new" {
			t.Errorf("state = %q, want the restored one", got)
		}
		if got := readFile(t, filepath.Join(out.Aside, "state.json")); got != "old" {
			t.Errorf("state aside = %q, want the old one", got)
		}
		if got := readFile(t, filepath.Join(home, "projects", "p1", "worktrees", "t1", "main.go")); got != "work in progress" {
			t.Errorf("worktree = %q, want it at its path", got)
		}
	})

	t.Run("running djinn", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "djinn")
		writeFile(t, home, "state.json", "running")
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.WriteAddr(home, "http://"+ln.Addr().String()+"/?token=x"); err != nil {
			t.Fatal(err)
		}
		_, err = Restore(ctx, home, archive, time.Now())
		if !errors.Is(err, ErrRunning) || !strings.Contains(err.Error(), "close its window") {
			t.Fatalf("Restore = %v, want ErrRunning saying how to stop it", err)
		}
		if got := readFile(t, filepath.Join(home, "state.json")); got != "running" {
			t.Errorf("the running folder changed: %q", got)
		}
		siblings, _ := os.ReadDir(filepath.Dir(home))
		if len(siblings) != 1 {
			t.Errorf("Restore left files next to the folder: %v", siblings)
		}
		// Once it stops, its address is stale: the restore goes on.
		ln.Close()
		if _, err := Restore(ctx, home, archive, time.Now()); err != nil {
			t.Fatal(err)
		}
	})

	dir := t.TempDir()
	manifest := `{"format": 1, "version": "test"}`
	for _, tc := range []struct {
		name  string
		files map[string]string // The entries of a zip archive; nil: a file that is no archive; empty: no file.
		want  string
	}{
		{name: "entry outside the folder", files: map[string]string{ManifestFile: manifest, "../evil": "x"}, want: "unsafe entry"},
		{name: "no manifest", files: map[string]string{"state.json": "{}"}, want: "no " + ManifestFile},
		{name: "future format", files: map[string]string{ManifestFile: `{"format": 2}`}, want: "format 2"},
		{name: "no database", files: map[string]string{ManifestFile: manifest}, want: "no " + store.File},
		{name: "not an archive", want: "not a backup archive"},
		{name: "no archive", files: map[string]string{}, want: "read "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".zip")
			switch {
			case tc.files == nil:
				archive = filepath.Join(dir, "plain.tar.gz")
				writeFile(t, dir, "plain.tar.gz", "not an archive")
			case len(tc.files) == 0:
				archive = filepath.Join(dir, "missing.zip")
			default:
				writeZip(t, archive, tc.files)
			}
			home := filepath.Join(t.TempDir(), "djinn")
			_, err := Restore(ctx, home, archive, time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Restore = %v, want %q", err, tc.want)
			}
			if exists(home) {
				t.Error("Restore created the folder")
			}
			if exists(filepath.Join(filepath.Dir(home), "evil")) {
				t.Error("an entry escaped the folder")
			}
		})
	}
}

// writeZip writes a zip archive holding files, by their names.
func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(zw.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}

// TestFinish: a restore a stop cut is ended at the next start, and what restores cut sooner left goes.
func TestFinish(t *testing.T) {
	t.Parallel()
	// A stop after the old data folder went aside and before the restored one took its place leaves no data folder;
	// Finish, as djinn up starts, puts the restored folder in place, its worktrees back, and removes what a restore cut
	// sooner left.
	t.Run("cut between the renames", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		archive := newArchive(t, "new")
		home := filepath.Join(t.TempDir(), "djinn")
		writeFile(t, home, "state.json", "old")
		writeFile(t, home, "projects/p1/worktrees/t1/main.go", "work in progress")
		// The second rename never returns: the restore stands where a kill would stop it, its deferred calls not run.
		cut, release := make(chan struct{}), make(chan struct{})
		moves := 0
		r := fsx.Renamer{Func: func(oldpath, newpath string) error {
			if moves++; moves == 2 {
				close(cut)
				<-release
				return errors.New("killed")
			}
			return os.Rename(oldpath, newpath)
		}}
		ended := make(chan struct{})
		go func() {
			defer close(ended)
			_, _ = restore(ctx, home, archive, time.Now(), r)
		}()
		<-cut
		if exists(home) {
			close(release)
			<-ended
			t.Fatal("the data folder is there between the renames")
		}
		// What a restore cut while it extracted leaves, and a folder of another data folder's restore.
		sooner := filepath.Join(filepath.Dir(home), ".djinn.restore-1")
		writeFile(t, sooner, "djinn.db", "half")
		other := filepath.Join(filepath.Dir(home), ".djinn-dev.restore-1")
		writeFile(t, other, "djinn.db", "theirs")
		did, err := Finish(home)
		close(release)
		<-ended
		if err != nil {
			t.Fatal(err)
		}
		if len(did) != 3 {
			t.Errorf("Finish did %q, want the folder in place, its worktrees back and the cut extraction removed", did)
		}
		if got := readFile(t, filepath.Join(home, "state.json")); got != "new" {
			t.Errorf("state = %q, want the restored one", got)
		}
		if got := readFile(t, filepath.Join(home, "projects", "p1", "worktrees", "t1", "main.go")); got != "work in progress" {
			t.Errorf("worktree = %q, want it back at its path", got)
		}
		if exists(filepath.Join(home, restoreMark)) {
			t.Error("the mark stays in the restored folder")
		}
		if exists(sooner) {
			t.Error("the folder of a restore cut while it extracted stays")
		}
		if !exists(other) {
			t.Error("the folder of another data folder's restore was removed")
		}
		if did, err := Finish(home); err != nil || len(did) != 0 {
			t.Errorf("Finish again = %q, %v; want nothing to do", did, err)
		}
	})

	t.Run("no parent folder", func(t *testing.T) {
		t.Parallel()
		if did, err := Finish(filepath.Join(t.TempDir(), "missing", "djinn")); err != nil || did != nil {
			t.Errorf("Finish = %q, %v; want nothing to do", did, err)
		}
	})

	// A stop before the data folder moves leaves it whole; the extracted folder, marked, goes at the next start.
	t.Run("cut before the renames", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "djinn")
		writeFile(t, home, store.File, "the database")
		writeFile(t, home, "state.json", "old")
		marked := filepath.Join(filepath.Dir(home), ".djinn.restore-2")
		writeFile(t, marked, "state.json", "new")
		writeFile(t, marked, restoreMark, home+".before-restore-x")
		if _, err := Finish(home); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(home, "state.json")); got != "old" {
			t.Errorf("state = %q, want the data folder as it was", got)
		}
		if exists(marked) {
			t.Error("the extracted folder stays")
		}
	})
}
