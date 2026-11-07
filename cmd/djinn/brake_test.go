package main

import (
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
