package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	backupv1 "github.com/empowill/djinn/gen/go/backup/v1"
	"github.com/empowill/djinn/gen/go/backup/v1/backupv1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
)

// newHome opens a data folder with its database, closed at the end of the test.
func newHome(t *testing.T) (string, *store.Store) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "djinn")
	s, err := store.Open(context.Background(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return home, s
}

func addProject(t *testing.T, s *store.Store, name, dir string) {
	t.Helper()
	p := &planv1.Project{Id: store.NewID(), Name: name, Directory: dir}
	if err := s.Tx(context.Background(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "test/add", p); err != nil {
			return err
		}
		return tx.Put(p)
	}); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// entries lists the names of the files and folders of an archive.
func entries(t *testing.T, archive string) []string {
	t.Helper()
	var names []string
	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		return names
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

// openRestored opens the database of a restored data folder.
func openRestored(t *testing.T, home string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".tar.gz", ".zip"} {
		t.Run(ext, func(t *testing.T) {
			ctx := context.Background()
			home, s := newHome(t)
			here := t.TempDir()
			addProject(t, s, "here", here)
			addProject(t, s, "gone", filepath.Join(t.TempDir(), "missing"))
			writeFile(t, home, "state.json", `{"open":true}`)
			writeFile(t, home, "wishes/w1/page.html", "<p>page</p>")
			writeFile(t, home, "tasks/t2/notes.md", "notes")
			writeFile(t, home, "projects/p1/worktrees/t1/main.go", "package main")
			writeFile(t, home, "tasks/t2/.env", "TOKEN=x")
			writeFile(t, home, "tasks/t2/prod.env", "TOKEN=x")
			writeFile(t, home, ".envrc", "export TOKEN=x")
			writeFile(t, home, "djinn.log", "log")
			writeFile(t, home, "djinn-up.log", "log")
			writeFile(t, home, server.AddrFile, "http://127.0.0.1:1/?token=secret")

			archive := filepath.Join(t.TempDir(), "backup"+ext)
			res, err := Create(ctx, home, archive, "test", s.Snapshot)
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

			restored := filepath.Join(t.TempDir(), "restored")
			out, err := Restore(ctx, restored, archive, time.Now())
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
}

// TestConsistentWhileWriting backs up while a writer commits wishes, each with its journal entry: every backup
// holds as many entries as wishes.
func TestConsistentWhileWriting(t *testing.T) {
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
}

func TestRestoreRefusesARunningDjinn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src, s := newHome(t)
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if _, err := Create(ctx, src, archive, "test", s.Snapshot); err != nil {
		t.Fatal(err)
	}
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
}

func TestRestoreKeepsTheOldFolderAside(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src, s := newHome(t)
	writeFile(t, src, "state.json", "new")
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Create(ctx, src, archive, "test", s.Snapshot); err != nil {
		t.Fatal(err)
	}
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
}

func TestRestoreRefusesForeignArchives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name string, files map[string]string) string {
		p := filepath.Join(dir, name)
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
			io.WriteString(w, content)
		}
		if err := errors.Join(zw.Close(), f.Close()); err != nil {
			t.Fatal(err)
		}
		return p
	}
	manifest := `{"format": 1, "version": "test"}`
	cases := map[string]string{
		write("escape.zip", map[string]string{ManifestFile: manifest, "../evil": "x"}): "unsafe entry",
		write("bare.zip", map[string]string{"state.json": "{}"}):                       "no " + ManifestFile,
		write("future.zip", map[string]string{ManifestFile: `{"format": 2}`}):          "format 2",
		write("nodb.zip", map[string]string{ManifestFile: manifest}):                   "no " + store.File,
	}
	plain := filepath.Join(dir, "plain.tar.gz")
	writeFile(t, dir, "plain.tar.gz", "not an archive")
	cases[plain] = "not a backup archive"
	for archive, want := range cases {
		home := filepath.Join(t.TempDir(), "djinn")
		_, err := Restore(ctx, home, archive, time.Now())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Restore(%s) = %v, want %q", filepath.Base(archive), err, want)
		}
		if exists(home) {
			t.Errorf("Restore(%s) created the folder", filepath.Base(archive))
		}
	}
	if exists(filepath.Join(filepath.Dir(dir), "evil")) {
		t.Error("an entry escaped the folder")
	}
}

func TestService(t *testing.T) {
	t.Parallel()
	home, s := newHome(t)
	writeFile(t, home, "state.json", "{}")
	_, h := Handler(s, home, "test")
	mux := httptest.NewServer(h)
	defer mux.Close()
	client := backupv1connect.NewBackupServiceClient(mux.Client(), mux.URL)
	file := filepath.Join(t.TempDir(), "backup.zip")
	res, err := client.Create(context.Background(), connect.NewRequest(&backupv1.BackupServiceCreateRequest{File: file}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetFile() != file || res.Msg.GetFiles() != 3 {
		t.Errorf("Create = %v", res.Msg)
	}
	_, err = client.Create(context.Background(), connect.NewRequest(&backupv1.BackupServiceCreateRequest{File: file + ".rar"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("Create(.rar) = %v, want invalid argument", err)
	}
}

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
