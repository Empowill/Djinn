package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	knownGOOS = map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true,
		"freebsd": true, "illumos": true, "ios": true, "js": true,
		"linux": true, "netbsd": true, "openbsd": true, "plan9": true,
		"solaris": true, "wasip1": true, "windows": true,
	}
	knownGOARCH = map[string]bool{
		"386": true, "amd64": true, "arm": true, "arm64": true,
		"loong64": true, "mips": true, "mips64": true, "mips64le": true,
		"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
		"s390x": true, "wasm": true,
	}
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod not found)")
		}
		dir = parent
	}
}

// TestNoMisnamedTestFiles ensures that no file in the repository follows the *_test_<goos>.go or
// *_test_<goarch>.go naming pattern. Go only recognizes files ending in _test.go (such as
// foo_windows_test.go or foo_amd64_test.go) as test files; naming them *_test_<target>.go causes Go
// to treat <target> as a build constraint and compile the file into non-test builds.
func TestNoMisnamedTestFiles(t *testing.T) {
	root := repoRoot(t)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin", ".release", ".app":
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}

		base := strings.TrimSuffix(name, ".go")
		idx := strings.Index(base, "_test_")
		if idx == -1 {
			return nil
		}

		suffix := base[idx+len("_test_"):]
		parts := strings.Split(suffix, "_")
		allTargetTokens := true
		for _, part := range parts {
			if !knownGOOS[part] && !knownGOARCH[part] {
				allTargetTokens = false
				break
			}
		}
		if allTargetTokens && len(parts) > 0 {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			t.Errorf("misnamed test file %s: Go test files must end with _test.go (e.g. name_<goos>_test.go); Go compiles *_test_<goos>.go into regular binaries", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repo: %v", err)
	}
}

// TestCrossVet checks that cmd/djinn vets cleanly on Linux for Windows (headless) and Darwin.
func TestCrossVet(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cross-vet checks only run on Linux")
	}
	root := repoRoot(t)

	t.Run("windows", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "go", "vet", "-tags", "headless", "./cmd/djinn")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS=windows", "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOOS=windows go vet -tags headless ./cmd/djinn failed:\n%s", out)
		}
	})

	t.Run("darwin", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "go", "vet", "./cmd/djinn")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS=darwin", "CGO_ENABLED=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOOS=darwin go vet ./cmd/djinn failed:\n%s", out)
		}
	})
}
