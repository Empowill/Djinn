//go:build !linux && !darwin && !windows

package machine

import "runtime"

// Read gives the cores only on a system Djinn does not read further.
func Read() (Snapshot, error) { return Snapshot{Cores: runtime.NumCPU()}, nil }
