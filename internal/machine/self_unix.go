//go:build linux || darwin

package machine

import (
	"os"
	"syscall"
	"time"
)

// ReadSelf reads what Djinn's own process uses, without any process it started (its window, leads, workers, gate
// holders): its CPU time, from getrusage, and its resident memory.
func ReadSelf() (Group, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return Group{}, err
	}
	g := Group{Processes: 1, CPU: time.Duration(ru.Utime.Nano() + ru.Stime.Nano())}
	listing.mu.Lock()
	defer listing.mu.Unlock()
	if now := time.Now(); now.Sub(listing.at) >= time.Second {
		listing.procs, listing.err = listProcs()
		listing.at = now
	}
	for _, p := range listing.procs {
		if p.pid == os.Getpid() {
			g.Memory = p.memory
		}
	}
	return g, listing.err
}
