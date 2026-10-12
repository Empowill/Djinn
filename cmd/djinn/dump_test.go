//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a buffer two goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestDumpGoroutinesOnSignal: SIGUSR2 writes the goroutines of the process to the data folder and says where, and
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
	path := filepath.Join(home, dumpFile)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(say.String(), path) {
		if time.Now().After(deadline) {
			t.Fatalf("no dump said after SIGUSR2: %q", say.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), ".dumpGoroutines(") {
		t.Errorf("the dump lacks the goroutine that wrote it:\n%.2000s", b)
	}
}
