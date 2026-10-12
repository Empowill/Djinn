package main

import "os"

// notifyDump sends nothing on Windows, which has no user signal: djinn debug dump asks there (W511).
func notifyDump(chan<- os.Signal) (stop func()) { return func() {} }
