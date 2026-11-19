package gate

import (
	"os"
	"testing"
)

// TestTreeFootprint reads the most footprint of a process tree from the kernel's process list: this test's own.
func TestTreeFootprint(t *testing.T) {
	t.Parallel()
	most, ok := treeFootprint(os.Getpid())
	if !ok || most == 0 {
		t.Errorf("treeFootprint = %d, %v", most, ok)
	}
	if _, ok := treeFootprint(1 << 30); ok {
		t.Error("treeFootprint of no process read")
	}
}
