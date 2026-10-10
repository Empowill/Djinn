//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInboxPlug: a project's skill declares an inbox source; djinn inbox sources lists it, not plugged in; djinn inbox
// plug runs it in djinn up, and its item comes; djinn inbox unplug stops it, having run it once.
func TestInboxPlug(t *testing.T) {
	home := t.TempDir()
	env := environ(home, t.TempDir())
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	skill := filepath.Join(folder, ".agents", "skills", "mentions")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	front := "---\nname: mentions\ndescription: Where I am mentioned.\nmetadata:\n  djinn:\n    source:\n" +
		"      watch: \"sh source.sh\"\n---\n"
	script := "echo run >> '" + runs + "'\necho 'carol mentioned you'\necho https://forge.example.com/acme/bell/issues/7\n"
	if err := errors.Join(
		os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(front), 0o644),
		os.WriteFile(filepath.Join(folder, "source.sh"), []byte(script), 0o644),
	); err != nil {
		t.Fatal(err)
	}
	up(t, home, env)
	djinn := func(args ...string) string {
		t.Helper()
		code, out, errs := runDjinn(t, env, args...)
		if code != 0 {
			t.Fatalf("djinn %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errs)
		}
		return out
	}
	djinn("project", "add", folder, "--name", "bell")

	// Declared, not plugged in: listed as such. That its command never starts is TestSourceUnplugged's.
	out := djinn("inbox", "sources")
	if !strings.Contains(out, "name: bell/mentions") || !strings.Contains(out, "watch: sh source.sh") ||
		strings.Contains(out, "plugged: true") {
		t.Errorf("sources:\n%s", out)
	}

	if out := djinn("inbox", "plug", "mentions"); !strings.Contains(out, "plugged: true") {
		t.Errorf("plug:\n%s", out)
	}
	// The plug wakes the sources at once: its item comes as soon as the command has printed it.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(djinn("inbox", "list"), "carol mentioned you") {
		if time.Now().After(deadline) {
			t.Fatal("the plugged source's item never came")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if out := djinn("inbox", "unplug", "bell/mentions"); !strings.Contains(out, "name: bell/mentions") ||
		strings.Contains(out, "plugged: true") {
		t.Errorf("unplug:\n%s", out)
	}
	if out := djinn("inbox", "sources"); strings.Contains(out, "plugged: true") {
		t.Errorf("sources after unplugging:\n%s", out)
	}
	if b, err := os.ReadFile(runs); err != nil || string(b) != "run\n" {
		t.Errorf("runs %q, %v; want one", b, err)
	}
	if code, _, errs := runDjinn(t, env, "inbox", "plug", "bell/bells"); code != 1 || !strings.Contains(errs, "no inbox source bell/bells") {
		t.Errorf("plug an unknown source: exit %d, %s", code, errs)
	}
}
