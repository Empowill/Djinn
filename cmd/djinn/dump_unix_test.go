//go:build !windows

package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestDumpGoroutinesOnSignal: SIGUSR2 writes the goroutines of the process to the log folder and says where, and
// the process goes on (W504).
func TestDumpGoroutinesOnSignal(t *testing.T) {
	home := t.TempDir()
	dump := make(chan os.Signal, 1)
	defer notifyDump(dump)()
	var say syncBuffer
	go dumpGoroutines(t.Context(), home, dump, &say)
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	if b := awaitDump(t, home, &say); !strings.Contains(b, ".dumpGoroutines(") {
		t.Errorf("the dump lacks the goroutine that wrote it:\n%.2000s", b)
	}
}
