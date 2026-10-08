package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestBackupCommand backs up a data folder no djinn runs on, then restores it over itself.
func TestBackupCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), "h")
	t.Setenv("DJINN_HOME", home)
	s, err := store.Open(context.Background(), filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	archive := filepath.Join(t.TempDir(), "b.zip")
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := runBackup(args, &out, &out)
		return code, out.String()
	}
	if code, out := run("--file", archive); code != 0 || !strings.Contains(out, "wrote "+archive+" (2 files") {
		t.Fatalf("djinn backup = %d, %s", code, out)
	}
	if code, out := run("restore", archive); code != 0 || !strings.Contains(out, "1 file restored") ||
		!strings.Contains(out, "kept in "+home+".before-restore-") {
		t.Fatalf("djinn backup restore = %d, %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, store.File)); err != nil {
		t.Error(err)
	}
	for _, args := range [][]string{{"restore"}, {"--bogus"}, {"restore", "a", "b"}} {
		if code, out := run(args...); code != 2 || !strings.Contains(out, "Usage: djinn backup") {
			t.Errorf("djinn backup %v = %d, %s; want the usage and 2", args, code, out)
		}
	}
}
