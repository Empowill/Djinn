package main

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
)

// TestHeldSince: a task is admitted last at the latest grant of what it holds now; what it gave back, or a holder
// outside any task, does not count.
func TestHeldSince(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)
	ivs := []*machinev1.GateInterval{
		{Gate: "lint", TaskId: "a", GrantTime: timestamppb.New(at)},
		{Gate: "commands", TaskId: "a", GrantTime: timestamppb.New(at.Add(time.Minute))},
		{Gate: "commands", TaskId: "a", GrantTime: timestamppb.New(at.Add(2 * time.Minute)),
			ReleaseTime: timestamppb.New(at.Add(3 * time.Minute))},
		{Gate: "test", TaskId: "b", GrantTime: timestamppb.New(at.Add(30 * time.Second))},
		{Gate: "test", GrantTime: timestamppb.New(at.Add(time.Hour))},
	}
	got := heldSince(ivs)
	if len(got) != 2 || !got["a"].Equal(at.Add(time.Minute)) || !got["b"].Equal(at.Add(30*time.Second)) {
		t.Errorf("heldSince = %v", got)
	}
}

// TestHeldAlone: a task holds alone the gates it holds now not shared; a shared one, one given back, or another task's
// does not count.
func TestHeldAlone(t *testing.T) {
	t.Parallel()
	at := timestamppb.New(time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC))
	shared, alone := machinev1.GateMode_GATE_MODE_SHARED, machinev1.GateMode_GATE_MODE_EXCLUSIVE
	ivs := []*machinev1.GateInterval{
		{Gate: "lint", TaskId: "a", Mode: shared, GrantTime: at},
		{Gate: "commands", TaskId: "a", Mode: shared, GrantTime: at},
		{Gate: "test", TaskId: "a", Mode: alone, GrantTime: at, ReleaseTime: at},
		{Gate: "test", TaskId: "b", Mode: alone, GrantTime: at},
		{Gate: "stack", TaskId: "b", Mode: alone},
	}
	if got := heldAlone(ivs, "a"); len(got) != 0 {
		t.Errorf("heldAlone(a) = %v, want none", got)
	}
	ivs = append(ivs, &machinev1.GateInterval{Gate: "e2e", TaskId: "a", Mode: alone, GrantTime: at})
	if got := heldAlone(ivs, "a"); !slices.Equal(got, []string{"e2e"}) {
		t.Errorf("heldAlone(a) = %v, want [e2e]", got)
	}
	if got := heldAlone(ivs, "b"); !slices.Equal(got, []string{"test"}) {
		t.Errorf("heldAlone(b) = %v, want [test]", got)
	}
}
