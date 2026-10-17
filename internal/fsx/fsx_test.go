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

// TestRenamer_Rename: a rename that fails for a moment, as Windows' does over a file a browser holds open, is tried
// again until it passes; one that keeps failing gives its error once the wait is over, and no wait means one try.
func TestRenamer_Rename(t *testing.T) {
	t.Parallel()
	held := errors.New("Access is denied")
	for _, tc := range []struct {
		name      string
		held      int // Tries refused before one passes.
		wait      time.Duration
		wantErr   error
		wantTries int
		wantSlept time.Duration
	}{
		{name: "held for a moment", held: 3, wait: 2 * time.Second, wantTries: 4, wantSlept: 3 * retryEvery},
		{name: "held for good", held: 1 << 30, wait: 100 * time.Millisecond, wantErr: held, wantTries: 6,
			wantSlept: 100 * time.Millisecond},
		{name: "no wait", held: 1, wantErr: held, wantTries: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tries := 0
			var slept time.Duration
			r := Renamer{
				Func: func(string, string) error {
					if tries++; tries <= tc.held {
						return held
					}
					return nil
				},
				Wait:  tc.wait,
				Sleep: func(d time.Duration) { slept += d },
			}
			if err := r.Rename("a", "b"); !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) ||
				tries != tc.wantTries {
				t.Errorf("Rename = %v after %d tries, want %v after %d", err, tries, tc.wantErr, tc.wantTries)
			}
			if slept != tc.wantSlept {
				t.Errorf("slept %v, want %v", slept, tc.wantSlept)
			}
		})
	}

	// A file a reader holds open is replaced once the reader lets it go. Elsewhere than on Windows the rename passes
	// at once; on Windows it fails while the file is open, and OS tries again after the reader closed it, in the pause
	// between two tries.
	t.Run("file held open", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file, tmp := filepath.Join(dir, "settings.json"), filepath.Join(dir, ".settings.json.tmp")
		writeFile(t, file, "old")
		writeFile(t, tmp, "new")
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
	})
}

// TestRenamer_WriteAtomic: the write goes through a temporary file Sweep knows, renamed with the Renamer: a refused
// rename is tried again, a cut one leaves the old file whole.
func TestRenamer_WriteAtomic(t *testing.T) {
	t.Parallel()
	// A rename refused for a moment, as Windows refuses one while an antivirus scans settings.json, is tried again,
	// and the new content lands.
	t.Run("rename refused a moment", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "settings.json")
		writeFile(t, path, "old")
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
	})

	// A write a kill cuts before its rename leaves the old file whole; the temporary file is whole by then.
	t.Run("cut before the rename", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "settings.txtpb")
		writeFile(t, path, "old settings")
		cut, renamed := errors.New("killed"), false
		r := Renamer{Func: func(oldpath, _ string) error {
			// Where a kill would stop: the temporary file is written whole, the old file not touched yet.
			renamed = true
			if b, err := os.ReadFile(oldpath); err != nil || string(b) != "new settings" {
				t.Errorf("temporary file = %q, %v before the rename, want the new content whole", b, err)
			}
			return cut
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
	})

	// The temporary file of a write is one Sweep removes; the file gets the permissions asked.
	t.Run("temporary file sweep knows", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "a.md")
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
	})
}

// TestWriteAtomic: the write makes the file's folder, and nothing but the file is left once it is done; a folder that
// cannot be made fails it.
func TestWriteAtomic(t *testing.T) {
	t.Parallel()
	t.Run("new folder", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "tilasm")
		path := filepath.Join(dir, "index.html")
		if err := WriteAtomic(path, []byte("page"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != "page" {
			t.Errorf("file = %q, want the content", got)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%d files left, want the file alone", len(entries))
		}
	})
	t.Run("folder is a file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "file")
		writeFile(t, file, "")
		if err := WriteAtomic(filepath.Join(file, "a.md"), nil, 0o600); err == nil {
			t.Error("WriteAtomic under a file: no error")
		}
	})
}

// TestWriteAtomicNoSync: the file holds the last content written, and nothing else is left.
func TestWriteAtomicNoSync(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	writeFile(t, path, "body")
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

// TestSyncDir: a folder syncs; a missing one fails, but on Windows, where SyncDir does nothing.
func TestSyncDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{name: "folder", dir: dir},
		{name: "missing folder", dir: filepath.Join(dir, "missing"), wantErr: runtime.GOOS != "windows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := SyncDir(tc.dir); (err != nil) != tc.wantErr {
				t.Errorf("SyncDir = %v, want an error: %v", err, tc.wantErr)
			}
		})
	}
}

// TestSweep: the temporary files of cut writes go, every other file stays; in a folder another Djinn writes in, one
// younger than the age given is a write on its way, and stays.
func TestSweep(t *testing.T) {
	t.Parallel()
	t.Run("cut writes among other files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		others := []string{
			"settings.txtpb", ".settings.txtpb.tmp", ".settings.txtpb.123.tmp", ".settings.txtpb.djinn-12a.tmp",
			"settings.txtpb.djinn-123.tmp", ".settings.txtpb.djinn-123.tmp.bak", "notes.tmp",
		}
		for _, name := range append([]string{".settings.txtpb.djinn-123.tmp"}, others...) {
			writeFile(t, filepath.Join(dir, name), "")
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
		want := append([]string{".plan.djinn-7.tmp"}, others...)
		slices.Sort(want)
		if !slices.Equal(kept, want) {
			t.Errorf("left %v, want %v", kept, want)
		}
	})
	t.Run("missing folder", func(t *testing.T) {
		t.Parallel()
		if removed, err := Sweep(filepath.Join(t.TempDir(), "missing"), 0); err != nil || removed != nil {
			t.Errorf("Sweep of a missing folder = %v, %v; want nothing", removed, err)
		}
	})
	t.Run("not a folder", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("Windows names a file read as a folder a missing path: Sweep finds nothing there")
		}
		file := filepath.Join(t.TempDir(), "file")
		writeFile(t, file, "")
		if _, err := Sweep(file, 0); err == nil {
			t.Error("Sweep of a file: no error")
		}
	})
	t.Run("writes on their way", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		young, old := filepath.Join(dir, ".a.json.djinn-1.tmp"), filepath.Join(dir, ".b.json.djinn-2.tmp")
		writeFile(t, young, "")
		writeFile(t, old, "")
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
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
