package machine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The djinn command of the workers runs this executable, and a start replaces the one an older djinn left there,
// whole: nothing else stays in the folder.
func TestLinkSelfReplacesAnOlderOne(t *testing.T) {
	home := t.TempDir()
	name := "djinn"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := SelfDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("an older djinn"), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second start finds it in place
		got, err := LinkSelf(home)
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Fatalf("LinkSelf = %s; want %s", got, dir)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, errA := os.Stat(filepath.Join(dir, name))
	b, errB := os.Stat(exe)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Errorf("%s is not this executable, %s (%v, %v)", filepath.Join(dir, name), exe, errA, errB)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%s holds %d files; want the djinn command alone", dir, len(entries))
	}
}

// PathFirst puts the folder first, then Djinn's own PATH as it is, each folder once.
func TestPathFirst(t *testing.T) {
	sep := string(os.PathListSeparator)
	dir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("PATH", "/a"+sep+dir+sep+"/b")
	got := PathFirst(dir)
	if want := "PATH=" + strings.Join([]string{dir, "/a", "/b"}, sep); got != want {
		t.Errorf("PathFirst = %q; want %q", got, want)
	}
	if PathFirst("") != "" {
		t.Error("PathFirst of no folder is not empty")
	}
}
