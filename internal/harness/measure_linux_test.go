package harness

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
)

// TestMeasureWorker reads a real worker from /proc: a fake claude, measured as djinn up measures it.
func TestMeasureWorker(t *testing.T) {
	e, task := measuredClaude(t, machine.ReadWorker)
	var got *planv1.Resources
	waitFor(t, "a reading of the worker", func() bool {
		got = e.get(t, task.GetId()).GetResources()
		return got.GetProcesses() > 0
	})
	if got.GetMemoryBytes() == 0 || got.GetPeakMemoryBytes() < got.GetMemoryBytes() || got.GetReadTime() == nil {
		t.Errorf("resources = %v, want its memory and its peak", got)
	}
	if uses := e.h.Uses(); len(uses) != 1 || uses[0].GetTaskId() != task.GetId() {
		t.Errorf("uses = %v", uses)
	}
}
