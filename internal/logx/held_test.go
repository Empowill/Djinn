package logx

import (
	"strings"
	"testing"
	"time"
)

// TestHeldNamesItsHolder: a lock held past its limit logs at warn where its holder took it, once; past the freeze
// limit, once at error, with a dump of the goroutines; a lock taken and given back quickly logs nothing (W549).
func TestHeldNamesItsHolder(t *testing.T) {
	b := capture(t)
	dir := t.TempDir()
	var m Mutex
	go m.held.watch(t.Context(), "test", dir, 50*time.Millisecond, 150*time.Millisecond, 5*time.Millisecond)

	for range 100 {
		m.Lock()
		m.Unlock() //nolint:staticcheck // An empty critical section: the watch must not see it.
	}
	time.Sleep(100 * time.Millisecond)
	if s := b.String(); strings.Contains(s, "lock held") {
		t.Fatalf("a quick lock was logged:\n%s", s)
	}

	takeAndKeep(&m)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(b.String(), "lock held: frozen") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // More ticks: each level is logged once.
	m.Unlock()
	s := b.String()
	if n := strings.Count(s, "level=WARN msg=\"lock held long\" lock=test"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, s)
	}
	if n := strings.Count(s, "level=ERROR msg=\"lock held: frozen\" lock=test"); n != 1 {
		t.Errorf("%d errors, want 1:\n%s", n, s)
	}
	if !strings.Contains(s, "logx.takeAndKeep logx/held_test.go:") {
		t.Errorf("the holder is not named:\n%s", s)
	}
	if dumps := Dumps(dir); len(dumps) != 1 || !strings.Contains(s, "dump="+dumps[0]) {
		t.Errorf("dumps %v, log:\n%s", dumps, s)
	}
}

// takeAndKeep locks m and keeps it: the holder the watch names.
func takeAndKeep(m *Mutex) { m.Lock() }
