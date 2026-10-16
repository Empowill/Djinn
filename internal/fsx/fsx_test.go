package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// TestWriteAtomicRetriesARefusedRename: a rename refused for a moment, as Windows refuses one while an antivirus
// scans settings.json, is tried again, and the new content lands.
func TestWriteAtomicRetriesARefusedRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tries := 0
	refused := func(oldpath, newpath string) error {
		if tries++; tries <= 2 {
			return errors.New("Access is denied")
		}
		return os.Rename(oldpath, newpath)
	}
	r := Renamer{Func: refused, Wait: time.Second, Sleep: func(time.Duration) {}}
	if err := r.WriteAtomic(path, []byte("new"), 0o600); err != nil || tries != 3 {
		t.Fatalf("WriteAtomic = %v after %d tries, want nil after 3", err, tries)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("file = %q, want the new content", got)
	}
}

// TestWriteAtomicCutBeforeRename: a write a kill cuts before its rename leaves the old file whole, and its temporary
// file next to it, which the sweep removes, leaving every other file alone.
func TestWriteAtomicCutBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.txtpb")
	if err := os.WriteFile(path, []byte("old settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	cut, renamed := errors.New("killed"), false
	r := Renamer{Func: func(oldpath, newpath string) error {
		// Where a kill would stop: the temporary file is written whole, the old file not touched yet. A kill runs no
		// deferred removal: the test leaves a copy of it, as a kill would.
		renamed = true
		b, err := os.ReadFile(oldpath)
		if err != nil {
			return err
		}
		if string(b) != "new settings" {
			t.Errorf("temporary file = %q before the rename, want the new content whole", b)
		}
		return errors.Join(cut, os.WriteFile(filepath.Join(dir, "."+filepath.Base(newpath)+".djinn-123.tmp"), b, 0o600))
	}}
	if err := r.WriteAtomic(path, []byte("new settings"), 0o600); !errors.Is(err, cut) {
		t.Fatalf("WriteAtomic = %v, want the cut", err)
	}
	if !renamed {
		t.Fatal("WriteAtomic never renamed")
	}
	if got, _ := os.ReadFile(path); string(got) != "old settings" {
		t.Errorf("file = %q after a cut write, want the old content whole", got)
	}
	others := []string{
		"settings.txtpb", ".settings.txtpb.tmp", ".settings.txtpb.123.tmp", ".settings.txtpb.djinn-12a.tmp",
		"settings.txtpb.djinn-123.tmp", ".settings.txtpb.djinn-123.tmp.bak", "notes.tmp",
	}
	for _, name := range others[1:] {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A folder of that name is not Djinn's file either.
	if err := os.Mkdir(filepath.Join(dir, ".plan.djinn-7.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	removed, err := Sweep(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, ".settings.txtpb.djinn-123.tmp")}; !slices.Equal(removed, want) {
		t.Errorf("Sweep removed %v, want %v", removed, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, e := range entries {
		kept = append(kept, e.Name())
	}
	if want := append([]string{".plan.djinn-7.tmp"}, others...); !sameNames(kept, want) {
		t.Errorf("left %v, want %v", kept, want)
	}
	if removed, err := Sweep(filepath.Join(dir, "missing"), 0); err != nil || removed != nil {
		t.Errorf("Sweep of a missing folder = %v, %v; want nothing", removed, err)
	}
}

// TestWriteAtomicNamesItsTemporaryFile: the temporary file of a write is one Sweep removes, and nothing else is left
// once the write is done; the file gets the permissions asked.
func TestWriteAtomicNamesItsTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	var tmp string
	r := Renamer{Func: func(oldpath, newpath string) error {
		tmp = oldpath
		return os.Rename(oldpath, newpath)
	}}
	if err := r.WriteAtomic(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tempName.MatchString(filepath.Base(tmp)) {
		t.Errorf("temporary file %s: Sweep would not know it", filepath.Base(tmp))
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	if err := WriteAtomicNoSync(path, []byte("again"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "again" {
		t.Errorf("file = %q, want the last content", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left, want the file alone", len(entries))
	}
}

func sameNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// TestSweepSparesWritesOnTheirWay: in a folder another Djinn writes in, a temporary file younger than the age given
// is a write on its way, and stays; an older one goes.
func TestSweepSparesWritesOnTheirWay(t *testing.T) {
	dir := t.TempDir()
	young, old := filepath.Join(dir, ".a.json.djinn-1.tmp"), filepath.Join(dir, ".b.json.djinn-2.tmp")
	for _, path := range []string{young, old} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, long, long); err != nil {
		t.Fatal(err)
	}
	removed, err := Sweep(dir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{old}) {
		t.Errorf("Sweep removed %v, want the old file alone", removed)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("the young file: %v", err)
	}
}
