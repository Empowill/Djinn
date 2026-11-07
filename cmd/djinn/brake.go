package main

import (
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
