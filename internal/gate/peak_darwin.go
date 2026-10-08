package gate

import (
	"os"
	"syscall"
)

// peakMemory is the peak resident memory of the command's largest process, its children included once it waited
// for them: macOS counts it in bytes.
func peakMemory(ps *os.ProcessState) uint64 {
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru.Maxrss > 0 {
		return uint64(ru.Maxrss)
	}
	return 0
}
