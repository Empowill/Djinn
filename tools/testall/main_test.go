package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestRun runs three fake parts: the green ones print as they end, the red one at the end, and the last line names it.
func TestRun(t *testing.T) {
	var out strings.Builder
	start := func(_ context.Context, part string, log io.Writer) error {
		fmt.Fprintf(log, "%s says hello\n", part)
		if part == "e2e" {
			fmt.Fprint(log, "  1 failed\n    docs.spec.ts:32 no newline at the end")
			return errors.New("exit status 1")
		}
		return nil
	}
	code := run(context.Background(), &out, t.TempDir(), []string{"test-go", "test-ui", "e2e"}, start)
	if code != 1 {
		t.Fatalf("code %d, want 1:\n%s", code, out.String())
	}
	s := out.String()
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "Failed: e2e" {
		t.Errorf("last line %q, want the failed part:\n%s", last, s)
	}
	for _, want := range []string{"test-go says hello", "test-ui says hello", "e2e says hello", "docs.spec.ts:32"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	// The red part's output comes after every green one's.
	if red := strings.Index(s, "e2e says hello"); red < strings.Index(s, "test-go says hello") || red < strings.Index(s, "test-ui says hello") {
		t.Errorf("the red part's output is not last:\n%s", s)
	}
	if strings.Contains(s, "end\nFailed") {
		t.Errorf("a log without a final newline runs into the next line:\n%s", s)
	}
}

// TestRunGreen ends with the parts' times when every part passes.
func TestRunGreen(t *testing.T) {
	var out strings.Builder
	start := func(_ context.Context, part string, log io.Writer) error {
		fmt.Fprintf(log, "%s ok\n", part)
		return nil
	}
	code := run(context.Background(), &out, t.TempDir(), []string{"a", "b"}, start)
	if code != 0 {
		t.Fatalf("code %d, want 0:\n%s", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "every part green") || !strings.Contains(s, "a ok") || !strings.Contains(s, "b ok") {
		t.Errorf("output:\n%s", s)
	}
	if strings.Contains(s, "Failed:") {
		t.Errorf("a green run names a failure:\n%s", s)
	}
}
