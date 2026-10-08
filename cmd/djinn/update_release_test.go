//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// releaseCheckForTests is releaseCheck in a djinn of the tests that serves a fake release (TestMain).
const releaseCheckForTests = 150 * time.Millisecond

// trailerMark ends a copy of the test binary with the version it plays: two copies of one binary, two versions.
const trailerMark = "\ndjinn-test-version="

// withVersion is the test binary, playing version.
func withVersion(t *testing.T, version string) []byte {
	t.Helper()
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return append(b, trailerMark+version+"\n"...)
}

// trailerVersion is the version a copy of the test binary plays; empty for the test binary itself.
func trailerVersion(exe string) string {
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil || end < 64 {
		return ""
	}
	tail := make([]byte, 64)
	if _, err := f.ReadAt(tail, end-64); err != nil {
		return ""
	}
	i := bytes.LastIndex(tail, []byte(trailerMark))
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(string(tail[i+len(trailerMark):]))
}

// TestUpdateFromRelease runs a Djinn installed from a release while a fake release is served: it offers nothing while
// the release is its own version, offers a newer one without downloading it, refuses it on a click while its sum is
// wrong, then on djinn update downloads it, swaps it in and restarts on it.
func TestUpdateFromRelease(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	fake := newFakeRelease(t)
	v1, v2 := withVersion(t, "v1.0.0"), withVersion(t, "v2.0.0")
	dir := t.TempDir()
	djinnPath := filepath.Join(dir, "djinn")
	if err := os.WriteFile(djinnPath, v1, 0o755); err != nil {
		t.Fatal(err)
	}
	fake.publish(t, "v1.0.0", "djinn_test", v1, nil)
	env := environ(home, t.TempDir(), "DJINN_TEST_RELEASE_API="+fake.srv.URL, "DJINN_TEST_RELEASE_ASSET=djinn_test")

	old := exec.Command(djinnPath, "up")
	old.Env = env
	oldErrs := &buffer{}
	old.Stdout, old.Stderr = &buffer{}, oldErrs
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = old.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = old.Process.Signal(syscall.SIGINT)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = old.Process.Kill()
		}
	})
	ui, _ := clients(t, answering(t, home, oldErrs.String))
	alive := func(when string) {
		t.Helper()
		select {
		case <-exited:
			t.Fatalf("%s: djinn stopped:\n%s", when, oldErrs.String())
		default:
		}
		if b, err := os.ReadFile(djinnPath); err != nil || !bytes.Equal(b, v1) {
			t.Fatalf("%s: the installed binary changed (%v)", when, err)
		}
	}
	// waitChecks waits until djinn looked at the release n more times.
	waitChecks := func(n int) {
		t.Helper()
		from, _ := fake.counts()
		deadline := time.Now().Add(20 * time.Second)
		for checks, _ := fake.counts(); checks < from+n; checks, _ = fake.counts() {
			if time.Now().After(deadline) {
				t.Fatalf("djinn looked at the release %d times, want %d more:\n%s", checks-from, n, oldErrs.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	watch, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	updates, err := ui.WatchUpdate(watch, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer updates.Close()
	if !updates.Receive() || updates.Msg().GetCurrent() != "v1.0.0" {
		t.Fatalf("at start: %v, %v", updates.Msg(), updates.Err())
	}
	waitChecks(2)
	if r := updates.Msg().GetReady(); r != "" {
		t.Fatalf("the release of its own version is offered: %q", r)
	}

	// A newer release, whose sum is wrong: offered, and nothing downloads without the click.
	fake.publish(t, "v2.0.0", "djinn_test", v2, func(s string) string { return strings.Repeat("0", 64) + s[64:] })
	for updates.Msg().GetReady() != "v2.0.0" {
		if !updates.Receive() {
			t.Fatalf("v2.0.0 never offered: %v\n%s", updates.Err(), oldErrs.String())
		}
	}
	waitChecks(3)
	if _, downloads := fake.counts(); downloads != 0 {
		t.Fatalf("downloaded %d times without a click", downloads)
	}
	alive("offered")

	// The click: the sum does not match, the update fails and nothing changes.
	_, err = ui.Update(ctx, connect.NewRequest(&uiv1.UiServiceUpdateRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("update with a wrong sum: %v", err)
	}
	if _, downloads := fake.counts(); downloads != 1 {
		t.Fatalf("downloads after the click: %d, want 1", downloads)
	}
	time.Sleep(2 * restartDelay)
	alive("wrong sum")
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a refused release left files beside djinn: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(home, RestartFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused release noted a restart: %v", err)
	}

	// The right sum: djinn update downloads it, swaps it in, and Djinn restarts on it.
	fake.publish(t, "v2.0.0", "djinn_test", v2, nil)
	code, out, errs := runDjinn(t, env, "update", "--yes")
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("v1.0.0 still runs after djinn update:\n%s", oldErrs.String())
	}
	stopStarted(t, home, oldErrs.String())
	t.Logf("djinn update: exit %d\n%s%s", code, out, errs)
	if code != 0 || !strings.Contains(out, "restarting on v2.0.0") || !strings.Contains(out, "now running v2.0.0") {
		t.Fatalf("djinn update: exit %d\n%s%s\nv1.0.0:\n%s", code, out, errs, oldErrs.String())
	}
	if b, err := os.ReadFile(djinnPath); err != nil || !bytes.Equal(b, v2) {
		t.Fatalf("the installed binary is not the release (%v)", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("files beside djinn after the update: %v", entries)
	}
	ui, _ = clients(t, answering(t, home, func() string { b, _ := os.ReadFile(filepath.Join(home, LogFile)); return string(b) }))
	now, err := ui.WatchUpdate(ctx, connect.NewRequest(&uiv1.UiServiceWatchUpdateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer now.Close()
	if !now.Receive() || now.Msg().GetCurrent() != "v2.0.0" || now.Msg().GetReady() != "" {
		t.Fatalf("after the update: %v, %v", now.Msg(), now.Err())
	}
}

// TestProxySource: a Djinn installed with go install asks the module proxy GOPROXY names, and updates with go install
// of the newer version, with its own build tags, into its folder.
func TestProxySource(t *testing.T) {
	latest := "v1.2.0"
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+modulePath+"/@latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"Version":"`+latest+`","Time":"2026-10-01T00:00:00Z"}`)
	}))
	defer proxy.Close()

	// A fake go: go env GOPROXY prints $FAKE_GOPROXY; go install notes its arguments and writes $GOBIN/djinn.
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = env ]; then echo \"$FAKE_GOPROXY\"; exit 0; fi\n" +
		"echo \"$@\" >> " + calls + "\n" +
		"printf 'built %s' \"$*\" > \"$GOBIN/djinn\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	command := goCommand
	goCommand = filepath.Join(bin, "go")
	t.Cleanup(func() { goCommand = command })
	t.Setenv("FAKE_GOPROXY", proxy.URL+"/,direct")

	ctx := t.Context()
	src := proxySource{version: "v1.1.0"}
	r, err := src.latest(ctx)
	if err != nil || r == nil || r.Version != "v1.2.0" {
		t.Fatalf("a newer version: %+v, %v", r, err)
	}
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Fatalf("the offer ran go install: %s", b)
	}
	dir := t.TempDir()
	path, version, err := src.fetch(ctx, dir)
	if err != nil || version != "v1.2.0" || filepath.Dir(path) != dir {
		t.Fatalf("fetch: %q, %q, %v", path, version, err)
	}
	want := "install -tags headless " + modulePath + "/cmd/djinn@v1.2.0"
	if b, _ := os.ReadFile(path); string(b) != "built "+want {
		t.Fatalf("installed %q, want built %q", b, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left in the folder: %v", entries)
	}

	// Up to date, or no proxy to ask: nothing.
	latest = "v1.1.0"
	if r, err := src.latest(ctx); r != nil || err != nil {
		t.Fatalf("the same version: %v, %v", r, err)
	}
	t.Setenv("FAKE_GOPROXY", "off")
	if r, err := src.latest(ctx); r != nil || err != nil {
		t.Fatalf("GOPROXY=off: %v, %v", r, err)
	}
}
