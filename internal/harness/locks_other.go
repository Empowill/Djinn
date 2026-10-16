//go:build !linux

package harness

// busyIn is 0: no process is known to work in dir, where Djinn cannot read processes' working folders.
func busyIn(string) int { return 0 }
