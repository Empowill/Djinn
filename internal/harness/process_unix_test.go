//go:build !windows

package harness

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/empowill/djinn/internal/testx"
)

func TestProcessUnixSetPriorityTree(t *testing.T) {
	testx.Portable(t)

	// Save original hook functions.
	origSetPriority := setPriorityFn
	origSetCgroupWeight := setCgroupWeightFn
	origWorkerPIDs := workerPIDsFn
	defer func() {
		setPriorityFn = origSetPriority
		setCgroupWeightFn = origSetCgroupWeight
		workerPIDsFn = origWorkerPIDs
	}()

	var priorityCalls []struct {
		pid  int
		nice int
	}
	setPriorityFn = func(which, who, prio int) error {
		if which == syscall.PRIO_PROCESS {
			priorityCalls = append(priorityCalls, struct {
				pid  int
				nice int
			}{pid: who, nice: prio})
		}
		return nil
	}

	var weightCalls []struct {
		cgroup string
		weight int
	}
	setCgroupWeightFn = func(dir string, weight int) error {
		weightCalls = append(weightCalls, struct {
			cgroup string
			weight int
		}{cgroup: dir, weight: weight})
		return nil
	}

	workerPIDsFn = func(pid int, cgroup string) ([]int, error) {
		return []int{pid, pid + 1, pid + 2}, nil
	}

	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()

	proc := &process{
		cmd:    cmd,
		cgroup: "/sys/fs/cgroup/djinn/worker-w1.scope",
	}

	// 1. Lower priority (low = true)
	if err := setPriority(proc, true); err != nil {
		t.Fatalf("setPriority(low=true): %v", err)
	}

	if len(weightCalls) != 1 || weightCalls[0].weight != 10 {
		t.Errorf("expected weight 10, got %v", weightCalls)
	}

	wantPids := []int{cmd.Process.Pid, cmd.Process.Pid + 1, cmd.Process.Pid + 2}
	if len(priorityCalls) != 3 {
		t.Fatalf("expected 3 priority calls, got %d", len(priorityCalls))
	}
	for i, call := range priorityCalls {
		if call.pid != wantPids[i] || call.nice != 10 {
			t.Errorf("call %d: got pid=%d nice=%d, want pid=%d nice=10", i, call.pid, call.nice, wantPids[i])
		}
	}

	// 2. Raise priority (low = false)
	weightCalls = nil
	priorityCalls = nil

	if err := setPriority(proc, false); err != nil {
		t.Fatalf("setPriority(low=false): %v", err)
	}

	if len(weightCalls) != 1 || weightCalls[0].weight != 100 {
		t.Errorf("expected weight 100, got %v", weightCalls)
	}
	if len(priorityCalls) != 3 {
		t.Fatalf("expected 3 priority calls, got %d", len(priorityCalls))
	}
	for i, call := range priorityCalls {
		if call.pid != wantPids[i] || call.nice != 0 {
			t.Errorf("call %d: got pid=%d nice=%d, want pid=%d nice=0", i, call.pid, call.nice, wantPids[i])
		}
	}
}
