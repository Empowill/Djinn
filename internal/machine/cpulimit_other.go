//go:build !linux

package machine

import (
	"context"
	"fmt"
)

// CPULimit caps a worker's CPU through a systemd user scope, on Linux only. macOS has no cgroups, and Windows would
// need a Job Object: there, workers run as they are.
func CPULimit(context.Context, int) ([]string, error) {
	return nil, fmt.Errorf("%w: they need systemd, on Linux", ErrNoCPULimit)
}
