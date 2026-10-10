//go:build !linux

package machine

import (
	"context"
	"fmt"
)

// ProbeScopes runs workers in a systemd user scope, on Linux only. macOS has no cgroups, and Windows would need a Job
// Object: there, workers run in their process group.
func ProbeScopes(context.Context, int, uint64) (*Scopes, []string, error) {
	return nil, nil, fmt.Errorf("%w: scopes need systemd, on Linux", ErrNoScope)
}
