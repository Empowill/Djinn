package logx

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	testx.Main(m)
}

// syncBuffer is a buffer the log and the test share.
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

// capture makes the log a buffer, at debug, for the test. The log is the package's: a test that captures it never
// runs in parallel.
func capture(t *testing.T) *syncBuffer {
	var b syncBuffer
	t.Cleanup(Use(New(&b, slog.LevelDebug)))
	return &b
}

func TestStart(t *testing.T) {
	// Start writes logs/djinn.log at the level DJINN_LOG names, the log package's lines included.
	t.Run("writes the log folder", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv(Level, "info")
		stop, err := Start(home)
		if err != nil {
			t.Fatal(err)
		}
		L().Debug("hidden")
		L().Warn("shown", "n", 1)
		log.Printf("djinn: printed %d", 2)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		L().Warn("after stop") // Discarded again.
		b, err := os.ReadFile(filepath.Join(home, Dir, File))
		if err != nil {
			t.Fatal(err)
		}
		if s := string(b); strings.Contains(s, "hidden") || !strings.Contains(s, "level=WARN msg=shown n=1") ||
			strings.Contains(s, "after stop") || !strings.Contains(s, `level=INFO msg="djinn: printed 2"`) {
			t.Errorf("log:\n%s", s)
		}
	})
	t.Run("unknown level", func(t *testing.T) {
		t.Setenv(Level, "loud")
		if _, err := Start(t.TempDir()); err == nil {
			t.Error("DJINN_LOG=loud started")
		}
	})
}

// TestTook: what takes over Slow logs at warn, whatever its level.
func TestTook(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		start     time.Time
		args      []any
		want      string
	}{
		{"quick", "quick", time.Now(), nil, "level=DEBUG msg=quick"},
		{"slow", "slow", time.Now().Add(-2 * Slow), []any{"method", "/x.v1.S/M"},
			"level=WARN msg=slow took=2s method=/x.v1.S/M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := capture(t)
			Took(slog.LevelDebug, tc.msg, tc.start, tc.args...)
			if s := b.String(); !strings.Contains(s, tc.want) {
				t.Errorf("log:\n%s", s)
			}
		})
	}
}
