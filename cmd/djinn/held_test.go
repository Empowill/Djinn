package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestHeldStart: the Hourglass's last reading at or above the limit holds the start first, naming it; then a stop that
// was not clean; then --paused. A reading under the limit, none, one the store cannot read, or a limit of 0 holds
// nothing by itself.
func TestHeldStart(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 21, 3, 16, 0, 0, time.UTC)
	reading := func(memory uint64, err error) func(context.Context) (store.Sample, bool, error) {
		return func(context.Context) (store.Sample, bool, error) {
			return store.Sample{Series: "machine", At: at, Memory: memory}, memory > 0, err
		}
	}
	for _, c := range []struct {
		name            string
		paused, unclean bool
		limit           int
		last            func(context.Context) (store.Sample, bool, error)
		want            uiv1.HeldStart
		says            string
	}{
		{"95 %", false, false, 90, reading(95, nil), uiv1.HeldStart_HELD_START_MEMORY,
			"the Hourglass's last reading of the last djinn up, at 2026-10-21 03:16:00, had the machine's memory at 95 % (--paused-memory 90): "},
		{"90 %, at the limit", false, false, 90, reading(90, nil), uiv1.HeldStart_HELD_START_MEMORY, "at 90 % (--paused-memory 90)"},
		{"95 %, a crash too", true, true, 90, reading(95, nil), uiv1.HeldStart_HELD_START_MEMORY,
			"(--paused-memory 90), and that djinn up did not stop cleanly: "},
		{"89 %", false, false, 90, reading(89, nil), uiv1.HeldStart_HELD_START_UNSPECIFIED, ""},
		{"no reading", false, false, 90, reading(0, nil), uiv1.HeldStart_HELD_START_UNSPECIFIED, ""},
		{"unread", false, false, 90, reading(99, errors.New("locked")), uiv1.HeldStart_HELD_START_UNSPECIFIED, ""},
		{"limit 0", false, false, 0, reading(99, nil), uiv1.HeldStart_HELD_START_UNSPECIFIED, ""},
		{"a crash", true, true, 90, reading(50, nil), uiv1.HeldStart_HELD_START_UNCLEAN, "the last djinn up did not stop cleanly"},
		{"--paused", true, false, 90, reading(50, nil), uiv1.HeldStart_HELD_START_PAUSED, "djinn up --paused asked it"},
	} {
		got := heldStart(t.Context(), c.paused, c.unclean, c.limit, 100, c.last)
		if got.why != c.want || !strings.Contains(got.lead, c.says) || (c.want == uiv1.HeldStart_HELD_START_UNSPECIFIED) != (got.lead == "") {
			t.Errorf("%s: %v %q, want %v saying %q", c.name, got.why, got.lead, c.want, c.says)
		}
	}
}

// TestWithoutPaused: a restart on a newer djinn keeps the flags of djinn up but --paused, in any of its spellings.
func TestWithoutPaused(t *testing.T) {
	t.Parallel()
	got := withoutPaused([]string{"--paused", "--browser", "-paused=true", "--paused-memory", "80", "--terminal", "paused"})
	if want := []string{"--browser", "--paused-memory", "80", "--terminal", "paused"}; !slices.Equal(got, want) {
		t.Errorf("withoutPaused = %q, want %q", got, want)
	}
}
