//go:build !linux && !darwin && !windows

package gate

import "os"

// peakMemory is unknown here.
func peakMemory(*os.ProcessState) uint64 { return 0 }
