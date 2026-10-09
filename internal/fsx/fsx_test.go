package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenameRetrying: a rename that fails for a moment, as Windows' does over a file a browser holds open, is tried
// again until it passes; one that keeps failing gives its error once the wait is over, and no wait means one try.
func TestRenameRetrying(t *testing.T) {
	held := errors.New("Access is denied")
	failing := func(times int) (func(string, string) error, *int) {
		tries := 0
		return func(string, string) error {
			tries++
			if tries <= times {
				return held
			}
			return nil
		}, &tries
	}
	var slept time.Duration
	sleep := func(d time.Duration) { slept += d }

	rename, tries := failing(3)
	if err := (Renamer{Func: rename, Wait: 2 * time.Second, Sleep: sleep}).Rename("a", "b"); err != nil || *tries != 4 {
		t.Errorf("held 3 times: %v after %d tries, want nil after 4", err, *tries)
	}
	if slept != 3*retryEvery {
		t.Errorf("held 3 times: slept %v, want %v", slept, 3*retryEvery)
	}

	rename, tries = failing(1 << 30)
	slept = 0
	if err := (Renamer{Func: rename, Wait: 100 * time.Millisecond, Sleep: sleep}).Rename("a", "b"); !errors.Is(err, held) || *tries != 6 {
		t.Errorf("held for good: %v after %d tries, want the error after 6", err, *tries)
	}
	if slept != 100*time.Millisecond {
		t.Errorf("held for good: gave up after %v, want 100ms", slept)
	}

	rename, tries = failing(1)
	slept = 0
	if err := (Renamer{Func: rename, Sleep: sleep}).Rename("a", "b"); !errors.Is(err, held) || *tries != 1 || slept != 0 {
		t.Errorf("no wait: %v after %d tries and %v, want the error after 1 and no sleep", err, *tries, slept)
	}
}

// TestRenameWhileOpen: a file a reader holds open is replaced once the reader lets it go. Elsewhere than on Windows
// the rename passes at once; on Windows it fails while the file is open, and OS tries again after the reader closed
// it, in the pause between two tries.
func TestRenameWhileOpen(t *testing.T) {
	dir := t.TempDir()
	file, tmp := filepath.Join(dir, "settings.json"), filepath.Join(dir, ".settings.json.tmp")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	r := OS()
	r.Sleep = func(time.Duration) { reader.Close() }
	if err := r.Rename(tmp, file); err != nil {
		t.Fatalf("rename while a reader holds the file: %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != "new" {
		t.Errorf("file = %q, want the new content", got)
	}
}
