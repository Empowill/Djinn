//go:build !windows

// Not on Windows: the djinn a release brings is a shell script.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/ui"
)

// scriptSource is a release source whose newest release is a script that says its version, as djinn version does.
type scriptSource struct {
	version string
	mu      sync.Mutex
	fetches int
}

func (s *scriptSource) latest(context.Context) (*release, error) {
	return &release{Version: s.version}, nil
}

func (s *scriptSource) fetch(_ context.Context, dir string) (string, string, error) {
	s.mu.Lock()
	s.fetches++
	s.mu.Unlock()
	f, err := os.CreateTemp(dir, ".djinn-new-*")
	if err != nil {
		return "", "", err
	}
	_, err = f.WriteString("#!/bin/sh\necho djinn " + s.version + "\n")
	if err := errors.Join(err, f.Close(), os.Chmod(f.Name(), 0o755)); err != nil {
		return "", "", err
	}
	return f.Name(), s.version, nil
}

// TestALocalBuildFollowsItsCheckout: a Djinn built from a checkout finds a release and does what its checkout says
// (harness.Release, tested on real repositories in internal/harness): on main, the release installs by itself at the
// path of the running djinn, once, the restart left to the click; offered, it waits for the click; a release the build
// holds, or one a branch merges instead, is not offered, and nothing downloads.
func TestALocalBuildFollowsItsCheckout(t *testing.T) {
	const version = "local-1a2b3c4"
	for _, c := range []struct {
		fit       harness.ReleaseFit
		offered   bool
		installed bool
	}{
		{harness.ReleaseInstall, true, true},
		{harness.ReleaseOffer, true, false},
		{harness.ReleaseHeld, false, false},
		{harness.ReleaseMerge, false, false},
	} {
		exe := filepath.Join(t.TempDir(), "djinn")
		if err := os.WriteFile(exe, []byte("#!/bin/sh\necho djinn "+version+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := ui.New(version)
		if err != nil {
			t.Fatal(err)
		}
		src := &scriptSource{version: "v0.2.0"}
		var asked []string
		u := &updater{
			exe: exe, version: version, ui: svc, releases: src, started: info, seen: info,
			fit: func(_ context.Context, tag string) (harness.ReleaseFit, error) {
				asked = append(asked, tag)
				return c.fit, nil
			},
		}
		r, _ := src.latest(t.Context())
		u.consider(t.Context(), r)
		u.consider(t.Context(), r) // Found again, six hours later.
		if len(asked) != 2 || asked[0] != "v0.2.0" {
			t.Errorf("fit %d: the checkout was asked about %v", c.fit, asked)
		}
		if got := u.offered(); got != c.offered {
			t.Errorf("fit %d: offered %v; want %v", c.fit, got, c.offered)
		}
		installed, err := binaryVersion(t.Context(), exe)
		if err != nil {
			t.Fatal(err)
		}
		if got := installed == "v0.2.0"; got != c.installed || c.installed && u.check(t.Context()) != "v0.2.0" {
			t.Errorf("fit %d: the djinn at the path is %s, the update ready %q", c.fit, installed, u.check(t.Context()))
		}
		if want := map[bool]int{true: 1, false: 0}[c.installed]; src.fetches != want {
			t.Errorf("fit %d: %d downloads; want %d", c.fit, src.fetches, want)
		}
		if u.Restarting() {
			t.Errorf("fit %d: restarting without a click", c.fit)
		}
	}
}
