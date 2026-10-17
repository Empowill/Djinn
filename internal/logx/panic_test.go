package logx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// crashHome is the variable that makes the test binary a djinn up whose loop panics, in that data folder.
const crashHome = "LOGX_CRASH_HOME"

// TestCrash: a background loop that panics ends the process, after its stack and a last line reach
// the log; the runtime writes the panic to the crash file, which the next start names by its time and logs (W525).
func TestCrash(t *testing.T) {
	if home := os.Getenv(crashHome); home != "" {
		crashingLoop(home)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestCrash$")
	cmd.Env = append(os.Environ(), crashHome+"="+home, Level+"=info")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the loop's panic did not end the process:\n%s", out)
	}
	log, err := os.ReadFile(filepath.Join(Folder(home), File))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`level=ERROR msg="panic: djinn up stops" in=schedule task=W7 panic="no worker for W7"`,
		`stack="goroutine `,
		"crashingLoop",
	} {
		if !strings.Contains(string(log), want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	crash, err := os.ReadFile(filepath.Join(Folder(home), CrashFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(crash), "panic: no worker for W7") || !strings.Contains(string(crash), "crashingLoop") {
		t.Errorf("crash file:\n%s", crash)
	}

	// The next start keeps it under its time, and tells it in the log.
	stop, err := Start(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	crashes := Crashes(Folder(home))
	if len(crashes) != 1 {
		t.Fatalf("crash files: %v", crashes)
	}
	if b, _ := os.ReadFile(crashes[0]); string(b) != string(crash) {
		t.Errorf("%s:\n%s", crashes[0], b)
	}
	if _, err := os.Stat(filepath.Join(Folder(home), CrashFile)); !os.IsNotExist(err) {
		t.Errorf("a clean stop left %s: %v", CrashFile, err)
	}
	log, _ = os.ReadFile(filepath.Join(Folder(home), File))
	if want := `level=ERROR msg="the last djinn up crashed" file=` + crashes[0] +
		` panic="panic: no worker for W7`; !strings.Contains(string(log), want) {
		t.Errorf("the log lacks %q:\n%s", want, log)
	}
}

// crashingLoop is a djinn up in home whose scheduler loop panics.
func crashingLoop(home string) {
	if _, err := Start(home); err != nil {
		panic(err)
	}
	go func() {
		defer Crash("schedule", "task", "W7")
		panic("no worker for W7")
	}()
	time.Sleep(time.Minute)
}

// TestKeepCrash: a start names the crash file left by its time, and keeps the latest CrashesKept; an empty
// one, left by a djinn up killed, goes.
func TestKeepCrash(t *testing.T) {
	capture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, CrashFile)
	for i := range CrashesKept + 2 {
		if err := os.WriteFile(path, []byte("goroutine 1\npanic: crash\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 12, 10, i, 0, 0, time.UTC)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		keepCrash(dir, path)
	}
	crashes := Crashes(dir)
	if len(crashes) != CrashesKept || !strings.HasSuffix(crashes[0], "crash-20261012T100200.000000000Z.txt") {
		t.Errorf("crash files: %v", crashes)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	keepCrash(dir, path)
	if _, err := os.Stat(path); !os.IsNotExist(err) || len(Crashes(dir)) != CrashesKept {
		t.Errorf("an empty crash file: %v, %v", err, Crashes(dir))
	}
}
