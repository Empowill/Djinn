// Package logx is the log of djinn up, to debug a freeze or a quit that hangs (W511): levelled lines (log/slog) in
// logs/djinn.log of the data folder, rolled over in the process on every system, and goroutine dumps beside them.
// It never logs a secret, a prompt or what a person wrote: names, durations, exit codes and errors.
package logx

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Dir is the folder of the logs and the dumps, in the data folder.
	Dir = "logs"
	// File is the log of djinn up, in Dir.
	File = "djinn.log"
	// Console is what a djinn up started in the background writes on its output (the window's toolkit, panics), in Dir.
	Console = "console.log"
	// MaxSize is the size at which File rolls over.
	MaxSize = 10 << 20
	// Keep is how many rolled-over files File keeps: File and they hold at most (Keep+1) × MaxSize, 50 MB.
	Keep = 4
	// Level is the variable that sets the lowest level logged: debug, info (the default), warn or error.
	Level = "DJINN_LOG"
	// Slow is the time past which a call, a pass or a wait logs at warn.
	Slow = time.Second
)

// Folder is the folder of the logs of the data folder home.
func Folder(home string) string { return filepath.Join(home, Dir) }

var current atomic.Pointer[slog.Logger]

func init() { current.Store(slog.New(slog.DiscardHandler)) }

// L is the log: it discards everything until Start, as in tests.
func L() *slog.Logger { return current.Load() }

// Use makes l the log until the returned function restores the one before; tests capture the log with it.
func Use(l *slog.Logger) (restore func()) {
	before := current.Swap(l)
	return func() { current.Store(before) }
}

// Start opens the log of the data folder home, rolled over at MaxSize, at the level DJINN_LOG names, and makes it the
// log, and the log package's, until stop. A panic or a fatal error that ends the process lands in CrashFile beside it;
// the one the last djinn up left is kept under its time, and logged.
func Start(home string) (stop func() error, err error) {
	level, err := ParseLevel(os.Getenv(Level))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Level, err)
	}
	if err := os.MkdirAll(Folder(home), 0o700); err != nil {
		return nil, err
	}
	f, err := OpenRotating(filepath.Join(Folder(home), File), MaxSize, Keep)
	if err != nil {
		return nil, err
	}
	l := New(f, level)
	restore := Use(l)
	// What Djinn still writes with the log package goes to the same file, at info.
	before, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(l)
	stopCrash, err := crashOutput(Folder(home))
	if err != nil {
		l.Warn("no crash file: a crash goes to the standard error only", "err", err)
		stopCrash = func() {}
	}
	return func() error {
		stopCrash()
		slog.SetDefault(before)
		log.SetOutput(out)
		log.SetFlags(flags)
		restore()
		return f.Close()
	}, nil
}

// New is a log written to w as text lines (key=value), from level up.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// ParseLevel reads debug, info, warn or error; empty is info.
func ParseLevel(s string) (slog.Level, error) {
	var l slog.Level
	if strings.TrimSpace(s) == "" {
		return slog.LevelInfo, nil
	}
	if err := l.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return 0, fmt.Errorf("%q: expected debug, info, warn or error", s)
	}
	return l, nil
}

// Took logs msg with how long since start, at warn past Slow, at level otherwise.
func Took(level slog.Level, msg string, start time.Time, args ...any) {
	took := time.Since(start)
	if took > Slow && level < slog.LevelWarn {
		level = slog.LevelWarn
	}
	L().Log(context.Background(), level, msg, append([]any{"took", took.Round(time.Millisecond)}, args...)...)
}

// Lock locks mu, and logs at warn a wait past Slow for the lock name: who holds it held up the others.
func Lock(mu *sync.Mutex, name string) {
	if mu.TryLock() {
		return
	}
	start := time.Now()
	mu.Lock()
	if took := time.Since(start); took > Slow {
		L().Warn("lock waited", "lock", name, "took", took.Round(time.Millisecond))
	}
}

// Cut is s on one line, cut at n bytes.
func Cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
