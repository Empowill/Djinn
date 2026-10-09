package swapexe

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// The test binary turns into a program that answers each line it reads, for as long as it runs.
	if os.Getenv("SWAPEXE_TEST_ECHO") == "1" {
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() {
			_, _ = os.Stdout.WriteString("alive " + s.Text() + "\n")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// exe ends the name of an executable: Windows runs only a file named so.
var exe = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestSwapWhileRunning installs over an executable that runs, both ways: the running copy never notices.
func TestSwapWhileRunning(t *testing.T) {
	for _, aside := range []bool{false, true} {
		t.Run(map[bool]string{false: "rename", true: "aside"}[aside], func(t *testing.T) {
			if !aside && runtime.GOOS == "windows" {
				t.Skip("Windows refuses to rename over a running executable: Install moves it aside there")
			}
			dir := t.TempDir()
			installed := filepath.Join(dir, "prog"+exe)
			copyFile(t, os.Args[0], installed)
			cmd := exec.Command(installed)
			cmd.Env = append(os.Environ(), "SWAPEXE_TEST_ECHO=1")
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { in.Close(); _ = cmd.Wait() })
			lines := bufio.NewReader(out)
			ask := func(word string) {
				t.Helper()
				if _, err := io.WriteString(in, word+"\n"); err != nil {
					t.Fatal(err)
				}
				got, err := lines.ReadString('\n')
				if err != nil || got != "alive "+word+"\n" {
					t.Fatalf("the running copy answered %q, %v", got, err)
				}
			}
			ask("before")

			fresh := filepath.Join(dir, ".prog-new")
			if err := os.WriteFile(fresh, []byte("the new build"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := Swap(fresh, installed, aside); err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(installed); err != nil || string(b) != "the new build" {
				t.Fatalf("installed: %q, %v", b, err)
			}
			if _, err := os.Stat(fresh); !os.IsNotExist(err) {
				t.Fatalf("the new build is still at %s: %v", fresh, err)
			}
			ask("after")

			entries, _ := os.ReadDir(dir)
			var moved []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), AsidePrefix) {
					moved = append(moved, e.Name())
				}
			}
			if aside != (len(moved) == 1) {
				t.Fatalf("moved aside: %v", moved)
			}
			if !aside {
				return
			}
			// A later install removes what was moved aside, once nothing runs it (Linux lets it go at once; Windows
			// once its process ended).
			if runtime.GOOS == "windows" {
				in.Close()
				_ = cmd.Wait()
			}
			if err := os.WriteFile(fresh, []byte("again"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := Swap(fresh, installed, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, moved[0])); !os.IsNotExist(err) {
				t.Fatalf("the first old copy is still there: %v", err)
			}
		})
	}
}

func TestSwapRefusesAnotherFolder(t *testing.T) {
	src := filepath.Join(t.TempDir(), "new")
	if err := os.WriteFile(src, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Swap(src, filepath.Join(t.TempDir(), "prog"), false); err == nil {
		t.Fatal("swap across folders: no error")
	}
}
