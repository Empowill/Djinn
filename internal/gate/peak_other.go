//go:build !linux && !darwin

package gate

import "os"

// peakMemory is unknown here. Windows gives a process's peak memory only while its handle is open, and the
// command's children only through a Job Object: neither is held yet.
func peakMemory(*os.ProcessState) uint64 { return 0 }
