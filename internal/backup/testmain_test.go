package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	testx.Main(m)
}

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

// refusing is a rename that refuses the first try of each move, as Windows refuses one while an antivirus scans a
// file, and records the moves it refused, by the paths they went to. It pauses no time.
func refusing() (fsx.Renamer, *[]string) {
	var refused []string
	tried := map[string]bool{}
	rename := func(oldpath, newpath string) error {
		if move := oldpath + " -> " + newpath; !tried[move] {
			tried[move] = true
			refused = append(refused, newpath)
			return errors.New("Access is denied")
		}
		return os.Rename(oldpath, newpath)
	}
	return fsx.Renamer{Func: rename, Wait: time.Second, Sleep: func(time.Duration) {}}, &refused
}

// newArchive backs up, as a .tar.gz, a data folder whose state.json holds state.
func newArchive(t *testing.T, state string) string {
	t.Helper()
	src, s := newHome(t)
	writeFile(t, src, "state.json", state)
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Create(context.Background(), src, archive, "test", s.Snapshot); err != nil {
		t.Fatal(err)
	}
	return archive
}

// fullHome opens a data folder holding a file of each kind, those a backup leaves out too, and two projects: "here",
// whose folder is here, and "gone", whose folder is missing.
func fullHome(t *testing.T) (home string, s *store.Store, here string) {
	t.Helper()
	home, s = newHome(t)
	here = t.TempDir()
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
	writeFile(t, home, "logs/djinn.log.1", "log")
	writeFile(t, home, "logs/goroutines-20261012T000000.000000000Z.txt", "dump")
	writeFile(t, home, "run/4242.json", `{"pid":4242}`)
	writeFile(t, home, server.AddrFile, "http://127.0.0.1:1/?token=secret")
	return home, s, here
}
