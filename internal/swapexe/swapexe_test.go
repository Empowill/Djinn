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
	"time"

	"github.com/empowill/djinn/internal/testx"
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
	cleanup := testx.Guard()
	code := m.Run()
	cleanup()
	os.Exit(code)
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

// startEcho starts path with SWAPEXE_TEST_ECHO=1, retrying on Linux's transient ETXTBSY when a parallel subtest forks
// before close completes on the freshly written executable (golang/go#22315).
func startEcho(t *testing.T, path string) (*exec.Cmd, io.WriteCloser, io.ReadCloser) {
	t.Helper()
	var err error
	for range 20 {
		cmd := exec.Command(path)
		cmd.Env = append(os.Environ(), "SWAPEXE_TEST_ECHO=1")
		in, inErr := cmd.StdinPipe()
		if inErr != nil {
			t.Fatal(inErr)
		}
		out, outErr := cmd.StdoutPipe()
		if outErr != nil {
			t.Fatal(outErr)
		}
		err = cmd.Start()
		if err == nil {
			return cmd, in, out
		}
		_ = in.Close()
		_ = out.Close()
		if !strings.Contains(err.Error(), "text file busy") {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(err)
	return nil, nil, nil
}

// TestSwap: the new build takes the installed one's path, in one rename or moving the old one aside first, and a
// copy that runs never notices; a new build in another folder, or none, is refused.
func TestSwap(t *testing.T) {
	t.Parallel()
	for _, aside := range []bool{false, true} {
		t.Run(map[bool]string{false: "rename while running", true: "aside while running"}[aside], func(t *testing.T) {
			t.Parallel()
			if !aside && runtime.GOOS == "windows" {
				t.Skip("Windows refuses to rename over a running executable: Install moves it aside there")
			}
			swapWhileRunning(t, aside)
		})
	}
	t.Run("aside with nothing installed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fresh, installed := filepath.Join(dir, ".prog-new"), filepath.Join(dir, "prog")
		writeFile(t, fresh, "the new build")
		if err := Swap(fresh, installed, true); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(installed); err != nil || string(b) != "the new build" {
			t.Fatalf("installed: %q, %v", b, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%d files left, want the installed one alone", len(entries))
		}
	})
	for _, tc := range []struct {
		name  string
		paths func(t *testing.T) (src, dst string)
	}{
		{name: "another folder", paths: func(t *testing.T) (string, string) {
			src := filepath.Join(t.TempDir(), "new")
			writeFile(t, src, "")
			return src, filepath.Join(t.TempDir(), "prog")
		}},
		{name: "no new build", paths: func(t *testing.T) (string, string) {
			dir := t.TempDir()
			return filepath.Join(dir, "new"), filepath.Join(dir, "prog")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src, dst := tc.paths(t)
			if err := Swap(src, dst, false); err == nil {
				t.Fatal("Swap: no error")
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Errorf("Swap left something at %s: %v", dst, err)
			}
		})
	}
}

// swapWhileRunning installs over an executable that runs: the running copy never notices.
func swapWhileRunning(t *testing.T, aside bool) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "prog"+exe)
	copyFile(t, os.Args[0], installed)
	cmd, in, out := startEcho(t, installed)
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
	writeFile(t, fresh, "the new build")
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

	moved := movedAside(t, dir)
	if aside != (len(moved) == 1) {
		t.Fatalf("moved aside: %v", moved)
	}
	if !aside {
		return
	}
	// A later install removes what was moved aside, once nothing runs it (Linux lets it go at once; Windows once its
	// process ended).
	if runtime.GOOS == "windows" {
		in.Close()
		_ = cmd.Wait()
	}
	writeFile(t, fresh, "again")
	if err := Swap(fresh, installed, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, moved[0])); !os.IsNotExist(err) {
		t.Fatalf("the first old copy is still there: %v", err)
	}
}

// TestInstall: the new build takes the installed one's path, the old one moved aside on Windows only.
func TestInstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fresh, installed := filepath.Join(dir, ".prog-new"), filepath.Join(dir, "prog")
	writeFile(t, installed, "the old build")
	writeFile(t, fresh, "the new build")
	if err := Install(fresh, installed); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(installed); err != nil || string(b) != "the new build" {
		t.Fatalf("installed: %q, %v", b, err)
	}
	if moved := movedAside(t, dir); (len(moved) == 1) != (runtime.GOOS == "windows") {
		t.Errorf("moved aside: %v", moved)
	}
}

// movedAside lists the old executables moved aside in dir.
func movedAside(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var moved []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), AsidePrefix) {
			moved = append(moved, e.Name())
		}
	}
	return moved
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
