package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// redOutput is a push check's output longer than the 10 lines Djinn keeps of it: the failing test is named at its top,
// a spec in its middle, and slowtests' "Failed:" line at its end.
func redOutput() string {
	var b strings.Builder
	b.WriteString("=== RUN   TestLogin\n    login_test.go:12: want 200, got 500\n--- FAIL: TestLogin (0.01s)\n")
	for i := range 20 {
		fmt.Fprintf(&b, "ok  \texample.com/app/internal/p%d\t0.1s\n", i)
	}
	b.WriteString("  1) [chromium] › docs.spec.ts:32:3 › the Grimoire opens ─────────\n")
	for i := range 20 {
		fmt.Fprintf(&b, "  ✓  %d [chromium] › other.spec.ts:%d:1 › passes (1.0s)\n", i+2, i+10)
	}
	b.WriteString("\nFailed: example.com/app/internal/auth TestLogin\n")
	return b.String()
}

// TestKeepCheckOutput (W245, L15 step 4): a red push or commit check keeps its whole output in a file under Djinn's
// data folder, with what it names failing; the integration's events link it, and the correction's prompt names it.
func TestKeepCheckOutput(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// Red once while other work may still land, the push is held; due again on the very same tree, its push checks do
	// not run again: the correction starts at once and reads the kept output file.
	t.Run("push check red once per tree", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, lintAtCommit+"checks { name: \"test\" command: \"test\" when: CHECK_WHEN_PUSH }\n", map[string]int{"lint": 0})
		out := redOutput()
		in.testCode, in.testOut = 1, out
		bare := in.remote(t)
		old := in.tip(t)

		var (
			mu     sync.Mutex
			prompt string
			read   string
		)
		in.correctWith(func(dir, p string) string {
			if !strings.HasPrefix(p, "The push checks of ") {
				return ""
			}
			mu.Lock()
			defer mu.Unlock()
			prompt = p
			if m := regexp.MustCompile(`whole output in (\S+\.log)`).FindStringSubmatch(p); m != nil {
				b, _ := os.ReadFile(m[1])
				read = string(b)
			}
			return "text read the whole output"
		})
		other := in.mustSpawn(t, in.wishID, "Other work", "wait\ntext other", nil) // Work left: the first red waits for it.
		in.until(t, other.GetId(), isStatus(planv1.TaskStatus_TASK_STATUS_RUNNING))
		azima := in.azima(t, in.wishID, "Login")
		w1 := in.finished(t, "W9", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
		in.ran()

		// Red once, other work running: held, no correction yet. The push checks wait until the pass is over: the pass's own
		// look at the push finds them running.
		wait := make(chan struct{})
		in.mu.Lock()
		in.testWait = wait
		in.mu.Unlock()
		in.passApart(t, 0)
		in.mu.Lock()
		in.testWait = nil
		in.mu.Unlock()
		close(wait)
		in.h.waitPushes()
		if got := in.remoteTip(t, bare); got != old {
			t.Fatalf("pushed red: origin at %s", got)
		}
		if runs, _ := in.ran(); !slices.Contains(runs, "test") {
			t.Fatalf("runs %q; want the push checks", runs)
		}
		state := in.pushState(t)
		if state.GetHeldRuns() != 1 || state.GetCorrectedBy() != "" {
			t.Fatalf("the push %v; want held once, no correction while other work runs", state)
		}
		f := state.GetFailure()
		file := f.GetOutputFile()
		if dir := filepath.Join(in.home, "projects", in.projectID, "checks"); filepath.Dir(file) != dir {
			t.Fatalf("output file %q; want one in %s", file, dir)
		}
		if b, err := os.ReadFile(file); err != nil || !strings.HasSuffix(string(b), out) || !strings.HasPrefix(string(b), "# test (test) at ") {
			t.Fatalf("output file %s = %q, %v; want a heading line, then the whole output", file, b, err)
		}
		if want := []string{"auth TestLogin", "docs.spec.ts:32"}; !slices.Equal(f.GetFailed(), want) {
			t.Errorf("failing %q; want %q", f.GetFailed(), want)
		}
		if strings.Contains(f.GetOutput(), "TestLogin (0.01s)") {
			t.Errorf("the failure's output %q; want its end only, the whole in the file", f.GetOutput())
		}
		link := "the whole output of test is in " + file + "; failing: auth TestLogin, docs.spec.ts:32"
		if _, texts := in.integration(t, w1); !slices.Contains(texts, link) {
			t.Errorf("W9's events %q lack %q", texts, link)
		}

		// Due again on the same tree: the push checks do not run, the correction starts at once.
		tip := in.tip(t)
		in.pass(t, time.Minute)
		if runs, _ := in.ran(); slices.Contains(runs, "test") {
			t.Fatalf("runs %q; want the push checks not run again on a tree they were red on", runs)
		}
		state = in.pushState(t)
		if state.GetCorrectedBy() == "" || state.GetAttempts() != 1 || state.GetHeldRuns() != 1 || state.GetFailedSha() != tip {
			t.Fatalf("the push %v; want held, run once, corrected at once", state)
		}
		again := fmt.Sprintf("the push checks of %s at %s do not run again: they were red on this very tree at %s (test: test exited 1); "+
			"the whole output is in %s", in.branch, short8(tip), short8(tip), file)
		if _, texts := in.integration(t, w1); !slices.Contains(texts, again) {
			t.Errorf("W9's events %q lack %q", texts, again)
		}
		c := in.ended(t, state.GetCorrectedBy())
		if c.GetTitle() != "Merge: push checks — auth TestLogin, docs.spec.ts:32" {
			t.Errorf("correction title %q; want what failed", c.GetTitle())
		}
		mu.Lock()
		defer mu.Unlock()
		for _, s := range []string{"Djinn kept the check's whole output in " + file, "It names as failing: auth TestLogin, docs.spec.ts:32."} {
			if !strings.Contains(prompt, s) {
				t.Errorf("the correction's prompt lacks %q:\n%s", s, prompt)
			}
		}
		if !strings.HasSuffix(read, out) {
			t.Errorf("the correction read %q; want the whole output", read)
		}
	})

	// A red commit check keeps its whole output too, linked from the task's events and named in its correction's prompt.
	t.Run("commit check red", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.checking(t, "checks { name: \"test\" command: \"test\" when: CHECK_WHEN_COMMIT }\n", nil)
		in.testCode, in.testOut = 1, redOutput()
		prompts := make(chan string, 1)
		in.correctWith(func(dir, p string) string {
			select {
			case prompts <- p:
			default:
			}
			return "text corrected"
		})
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})

		in.pass(t, 0)
		got, texts := in.integration(t, w1)
		file := got.GetFailure().GetOutputFile()
		if got.GetState() != planv1.IntegrationState_INTEGRATION_STATE_RED || file == "" {
			t.Fatalf("W1's integration %v; want red, its whole output kept", got)
		}
		if b, err := os.ReadFile(file); err != nil || !strings.HasSuffix(string(b), redOutput()) {
			t.Fatalf("output file %s = %q, %v", file, b, err)
		}
		if link := "the whole output of test is in " + file + "; failing: auth TestLogin, docs.spec.ts:32"; !slices.Contains(texts, link) {
			t.Errorf("W1's events %q lack %q", texts, link)
		}
		select {
		case p := <-prompts:
			if !strings.Contains(p, "Djinn kept the check's whole output in "+file) {
				t.Errorf("the correction's prompt lacks the output file:\n%s", p)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no correction started")
		}
	})
}

// TestFailingTests: what a red check's output names failing, whatever reports it.
func TestFailingTests(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, out string
		want      []string
	}{
		{"slowtests", "--- FAIL: TestA (0.1s)\nFAIL\texample.com/m/internal/a\t0.2s\n\nFailed: example.com/m/internal/a TestA, example.com/m/internal/a TestA/sub, example.com/m/cmd/x (build failed)\n",
			[]string{"a TestA", "a TestA/sub", "x (build failed)"}},
		{"go test", "--- FAIL: TestB (0.1s)\n    --- FAIL: TestB/one (0.0s)\nFAIL\texample.com/m/b\t0.2s\n", []string{"TestB", "TestB/one"}},
		{"go package", "panic: boom\nFAIL\texample.com/m/b\t0.2s\n", []string{"example.com/m/b"}},
		{"playwright", "  1) [chromium] › docs.spec.ts:32:3 › the Grimoire ──\n  2) [chromium] › theme.spec.ts:58:1 › theme ──\n  1 failed\n    [chromium] › docs.spec.ts:32:3 › the Grimoire\n",
			[]string{"docs.spec.ts:32", "theme.spec.ts:58"}},
		{"node tap", "TAP version 13\nnot ok 1 - tests/styles.test.cjs\n  ---\n    not ok 2 - no unused class\nok 3 - fine\nnot ok 4 - later # SKIP\n",
			[]string{"tests/styles.test.cjs", "no unused class"}},
		{"node spec", "✖ no unused class (1.2ms)\nℹ fail 1\n✖ failing tests:\n\n✖ no unused class (1.2ms)\n", []string{"no unused class"}},
		{"none", "lint exited 1: 3 issues\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := failingTests(c.out); !slices.Equal(got, c.want) {
				t.Errorf("failingTests = %q; want %q", got, c.want)
			}
		})
	}
}

// TestPruneOutputs: a project keeps its last keptOutputs red outputs, the oldest go.
func TestPruneOutputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range keptOutputs + 3 {
		writeFile(t, dir, fmt.Sprintf("20261017T%06d.000Z-test-1a2b3c4d.log", i), "out\n")
	}
	pruneOutputs(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != keptOutputs || entries[0].Name() != "20261017T000003.000Z-test-1a2b3c4d.log" {
		t.Errorf("%d files left, the first %s; want %d, the 3 oldest gone", len(entries), entries[0].Name(), keptOutputs)
	}
}
