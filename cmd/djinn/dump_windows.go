package main

import "os"

// notifyDump sends nothing on Windows, which has no user signal: DJINN_PPROF serves the goroutines there.
func notifyDump(chan<- os.Signal) (stop func()) { return func() {} }
