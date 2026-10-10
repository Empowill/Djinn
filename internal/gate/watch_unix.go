//go:build !windows

package gate

import (
	"os"
	"time"
)

// watch measures the command through its resource usage, which counts its children once it waited for them.
func watch(*os.Process) func(*os.ProcessState) (cpu time.Duration, peak uint64) {
	return func(ps *os.ProcessState) (time.Duration, uint64) {
		return ps.UserTime() + ps.SystemTime(), peakMemory(ps)
	}
}
