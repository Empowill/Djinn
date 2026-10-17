package harness

import (
	"path/filepath"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// TestMergeTitleThroughCorrections: a correction of a correction (W422 of W419 of W401) is titled from W401, the work
// it brings in the end, in words, its code after them, then what failed; one line.
func TestMergeTitleThroughCorrections(t *testing.T) {
	t.Parallel()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), store.File), plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	// Closed before TempDir's removal: Windows refuses to delete an open djinn.db.
	t.Cleanup(func() { db.Close() })
	w401 := &planv1.Task{Id: "w401", Code: "W401", Title: "Bring W333's documentation, W369's lead marking\nand W370's guards into the agent protocol"}
	w402 := &planv1.Task{Id: "w402", Code: "W402", Title: "Antigravity workers may read web pages"}
	red := &planv1.IntegrationFailure{State: planv1.IntegrationState_INTEGRATION_STATE_RED, TaskIds: []string{"w401", "w402"}}
	w419 := &planv1.Task{Id: "w419", Code: "W419", Title: "Merge: old title", Correction: &planv1.TaskCorrection{Failure: red}}
	conflict := &planv1.IntegrationFailure{
		State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, TaskIds: []string{"w419", "w402"},
		Files: []string{"docs/agent-protocol.md"},
	}
	w422 := &planv1.Task{Id: "w422", Code: "W422", Correction: &planv1.TaskCorrection{Failure: conflict}}
	review := &planv1.Task{Id: "w430", Code: "W430", Review: &planv1.TaskReview{TaskIds: []string{"w422"}}}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		for _, m := range []*planv1.Task{w401, w402, w419, w422} {
			if err := tx.Journal("test", "put", m); err != nil {
				return err
			}
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	served := servedTasks(t.Context(), db, []*planv1.Task{w419, w402})
	if got := codes(served); got != "W401, W402" {
		t.Fatalf("W422 serves %s; want W401, W402 once each", got)
	}
	got := mergeTitle(served, failureWhat(conflict))
	want := "Merge: Bring W333's documentation, W369's lead marking and W370's... (W401, W402) — conflict in docs/agent-protocol.md"
	if got != want || strings.Contains(got, "\n") {
		t.Errorf("W422's title\n%q; want\n%q", got, want)
	}
	if got := mergeTitle(servedTasks(t.Context(), db, []*planv1.Task{review}), "work left not committed"); got !=
		"Merge: Bring W333's documentation, W369's lead marking and W370's... (W401, W402) — work left not committed" {
		t.Errorf("a review of W422: %q", got)
	}
	if got := mergeTitle([]*planv1.Task{w402}, failureWhat(red)); got != "Merge: Antigravity workers may read web pages (W402) — tests" {
		t.Errorf("a red correction of W402: %q", got)
	}

	// A task that cannot be read stands for itself: the title still names what it can.
	lost := &planv1.Task{Id: "w9", Code: "W9", Title: "Lost work", Correction: &planv1.TaskCorrection{Failure: &planv1.IntegrationFailure{TaskIds: []string{"gone"}}}}
	if got := codes(servedTasks(t.Context(), db, []*planv1.Task{lost})); got != "W9" {
		t.Errorf("a correction of a task gone serves %s; want W9", got)
	}
}

// TestFailedChecks: a push-check correction names what failed, out of the check's output.
func TestFailedChecks(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ out, reason, want string }{
		{"--- FAIL: TestNoFrench (0.01s)\n    locales_test.go:40: ...\nFAIL\nFAIL\tgithub.com/empowill/djinn/locales\t0.3s\n", "test: exit status 1", "TestNoFrench"},
		{"ok  \tx\nFAIL\tgithub.com/empowill/djinn/internal/harness\t12.1s\nFAIL\n", "test: exit status 1", "harness"},
		{"  1) [chromium] › e2e/wish-render.spec.ts:40:3 › renders\n  1 failed\n", "e2e: exit status 1", "wish-render.spec.ts"},
		{"something else\n", "lint: exit status 2\nmore", "lint"},
	} {
		got := failedChecks(&planv1.IntegrationFailure{Output: c.out, Reason: c.reason})
		if got != c.want {
			t.Errorf("failedChecks(%q) = %q; want %q", c.out, got, c.want)
		}
	}
	if got := mergeLine("push checks", "TestNoFrench"); got != "Merge: push checks — TestNoFrench" {
		t.Errorf("push-check title %q", got)
	}
	if got := failureWhat(&planv1.IntegrationFailure{Files: []string{"a", "b", "c"}}); got != "conflict in a and 2 more files" {
		t.Errorf("three files: %q", got)
	}
}
