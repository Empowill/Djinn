package main

import (
	"slices"
	"time"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
)

// heldSince is, by task, when the latest gate or admission it holds now was granted, read in the intervals of the gates
// (gate.Gates.Intervals): those not released yet. The memory brake freezes the last admitted worker when only admitted
// ones grow (harness.Harness.GateGrants).
func heldSince(ivs []*machinev1.GateInterval) map[string]time.Time {
	out := map[string]time.Time{}
	for _, iv := range ivs {
		if iv.GetReleaseTime() != nil || iv.GetTaskId() == "" || iv.GetGrantTime() == nil {
			continue
		}
		if at := iv.GetGrantTime().AsTime(); at.After(out[iv.GetTaskId()]) {
			out[iv.GetTaskId()] = at
		}
	}
	return out
}

// heldAlone is the gates the task holds alone now, by name, in order, read in the intervals of the gates
// (gate.Gates.Intervals): those not released, not held shared. The developer may pause a worker that holds gates only
// shared, since no one waits for them (harness.Harness.HeldAlone, W29).
func heldAlone(ivs []*machinev1.GateInterval, taskID string) []string {
	var out []string
	for _, iv := range ivs {
		if iv.GetReleaseTime() != nil || iv.GetGrantTime() == nil || taskID == "" || iv.GetTaskId() != taskID ||
			iv.GetMode() == machinev1.GateMode_GATE_MODE_SHARED || slices.Contains(out, iv.GetGate()) {
			continue
		}
		out = append(out, iv.GetGate())
	}
	slices.Sort(out)
	return out
}
