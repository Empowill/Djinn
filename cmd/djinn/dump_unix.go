//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// dumpSignal is the signal that asks djinn up for its goroutines: kill -USR2 <pid>. SIGUSR1 is WebKit's, which
// takes it over in the window's process.
var dumpSignal os.Signal = syscall.SIGUSR2

// notifyDump sends dumpSignal to ch, and returns what stops it.
func notifyDump(ch chan<- os.Signal) (stop func()) {
	signal.Notify(ch, dumpSignal)
	return func() { signal.Stop(ch) }
}
