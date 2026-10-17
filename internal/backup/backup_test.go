package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// TestDefaultFile: a new archive goes in the Downloads folder, named after the time; a name taken gets a number.
func TestDefaultFile(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(user, ".config"))
	if err := os.Mkdir(filepath.Join(user, "Downloads"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)
	first, err := DefaultFile(now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != filepath.Join(user, "Downloads") || !strings.HasPrefix(filepath.Base(first), "djinn-backup-2026-10-08-093000.") {
		t.Errorf("DefaultFile = %s", first)
	}
	writeFile(t, filepath.Dir(first), filepath.Base(first), "taken")
	second, err := DefaultFile(now)
	if err != nil || second == first || !strings.Contains(second, "093000-2.") {
		t.Errorf("DefaultFile when taken = %s, %v", second, err)
	}
}

// TestCreate: the archive holds the manifest, the database and the files of the data folder, but what a backup
// leaves out; it is put in place though its rename is refused a moment, and is consistent while a writer commits.
func TestCreate(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".tar.gz", ".zip"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			home, s, _ := fullHome(t)
			archive := filepath.Join(t.TempDir(), "backup"+ext)
			res, err := Create(context.Background(), home, archive, "test", s.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if res.GetFile() != archive || res.GetSize() == 0 || res.GetFiles() != 5 {
				t.Errorf("Create = %v, want %s with 5 files", res, archive)
			}
			names := entries(t, archive)
			for _, want := range []string{ManifestFile, store.File, "state.json", "wishes/w1/page.html", "tasks/t2/notes.md"} {
				if !slices.Contains(names, want) {
					t.Errorf("the archive lacks %s: %v", want, names)
				}
			}
			for _, name := range names {
				if strings.Contains(name, "worktrees") || strings.Contains(name, "env") || strings.HasSuffix(name, ".log") ||
					name == server.AddrFile || strings.HasPrefix(name, store.File+"-") {
					t.Errorf("the archive holds %s", name)
				}
			}
		})
	}

	t.Run("rename refused a moment", func(t *testing.T) {
		t.Parallel()
		home, s := newHome(t)
		writeFile(t, home, "state.json", "state")
		archive := filepath.Join(t.TempDir(), "backup.tar.gz")
		r, refused := refusing()
		if _, err := create(context.Background(), home, archive, "test", s.Snapshot, r); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(*refused, []string{archive}) {
			t.Errorf("refused %v, want the archive's rename once", *refused)
		}
		if !slices.Contains(entries(t, archive), "state.json") {
			t.Errorf("the archive lacks state.json")
		}
	})

	// Backups while a writer commits wishes, each with its journal entry: every backup holds as many entries as
	// wishes.
	t.Run("while a writer commits", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		home, s := newHome(t)
		var stop atomic.Bool
		var wg sync.WaitGroup
		var written atomic.Int64
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				w := &planv1.Wish{Id: store.NewID(), Title: strings.Repeat("a wish ", 50)}
				if err := s.Tx(ctx, func(tx *store.Tx) error {
					if err := tx.Journal("test", "test/make", w); err != nil {
						return err
					}
					return tx.Put(w)
				}); err != nil {
					t.Error(err)
					return
				}
				written.Add(1)
			}
		}()
		snapshots := map[string]Snapshot{
			"service": s.Snapshot,
			"file": func(ctx context.Context, dst string) error {
				return store.SnapshotFile(ctx, filepath.Join(home, store.File), dst)
			},
		}
		var archives []string
		for i := range 4 { // Two of each kind.
			for written.Load() < int64(10*(i+1)) {
				time.Sleep(time.Millisecond)
			}
			kind := []string{"service", "file"}[i%2]
			archive := filepath.Join(t.TempDir(), "backup.tar.gz")
			if _, err := Create(ctx, home, archive, "test", snapshots[kind]); err != nil {
				stop.Store(true)
				wg.Wait()
				t.Fatal(kind, err)
			}
			archives = append(archives, archive)
		}
		stop.Store(true)
		wg.Wait()
		for i, archive := range archives {
			restored := filepath.Join(t.TempDir(), "restored")
			if _, err := Restore(ctx, restored, archive, time.Now()); err != nil {
				t.Fatal(err)
			}
			r := openRestored(t, restored)
			wishes, err := store.List[*planv1.Wish](ctx, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			journal, err := store.Commands(ctx, r, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(wishes) < 10*(i+1) || len(journal) != len(wishes) {
				t.Errorf("backup %d: %d wishes, %d journal entries; want as many, at least %d", i, len(wishes), len(journal), 10*(i+1))
			}
		}
	})

	failed := errors.New("disk full")
	for _, tc := range []struct {
		name     string
		home     func(t *testing.T) string
		file     string
		snapshot Snapshot
		want     string
	}{
		{name: "unknown archive kind", file: "backup.rar", want: "expected a name ending in"},
		{name: "no data folder", home: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") },
			file: "backup.zip", want: "no data folder"},
		{name: "snapshot fails", file: "backup.zip", snapshot: func(context.Context, string) error { return failed },
			want: failed.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if tc.home != nil {
				home = tc.home(t)
			}
			dir := t.TempDir()
			_, err := Create(context.Background(), home, filepath.Join(dir, tc.file), "test", tc.snapshot)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Create = %v, want %q", err, tc.want)
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Errorf("Create left %v", left)
			}
		})
	}
}
