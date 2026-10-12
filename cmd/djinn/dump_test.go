package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/logx"
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

// awaitDump waits until say names a dump of the log folder of home, and returns what it holds.
func awaitDump(t *testing.T, home string, say *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if dumps := logx.Dumps(logx.Folder(home)); len(dumps) > 0 && strings.Contains(say.String(), dumps[0]) {
			b, err := os.ReadFile(dumps[0])
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no dump said: %q", say.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDumpGoroutinesOnRequestFile: logx.DumpRequest in the log folder writes the goroutines of the process there and
// says where, on every system, and the process goes on (W511).
func TestDumpGoroutinesOnRequestFile(t *testing.T) {
	defer func(d time.Duration) { dumpPoll = d }(dumpPoll)
	dumpPoll = 10 * time.Millisecond
	home := t.TempDir()
	var say syncBuffer
	go dumpGoroutines(t.Context(), home, nil, &say)
	if err := os.MkdirAll(logx.Folder(home), 0o700); err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(logx.Folder(home), logx.DumpRequest)
	if err := os.WriteFile(request, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if b := awaitDump(t, home, &say); !strings.Contains(b, ".dumpGoroutines(") || !strings.Contains(b, "asked by dump.request") {
		t.Errorf("the dump lacks the goroutine that wrote it:\n%.2000s", b)
	}
	if _, err := os.Stat(request); err == nil {
		t.Error("the request is still there: it would dump again")
	}
}
