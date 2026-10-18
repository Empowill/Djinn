//go:build crossvet

package main

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// TestCrossVet checks that cmd/djinn, its tests included, vets cleanly on Linux for Windows (headless) and Darwin: a
// test file without a build tag using names of a file built only elsewhere fails here, before the Windows job of CI,
// which does not block. It runs only with the crossvet tag, in the Linux job of CI and by hand:
//
//	go test -tags headless,crossvet -run TestCrossVet ./cmd/djinn
//
// On a cold build cache it type-checks every dependency for both systems, 48 s (tools/slowtests).
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
