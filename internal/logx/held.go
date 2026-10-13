package logx

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// HeldLong is how long a watched lock may be held before Watch logs its holder at warn (W549).
	HeldLong = 10 * time.Second
	// HeldFrozen is how long a watched lock may be held before Watch logs at error and dumps the goroutines: no work
	// holds a lock that long, the process froze.
	HeldFrozen = 3 * HeldLong
)

// Held knows since when, and from where, a lock is held, so that Watch names a holder that keeps it too long: the
// next freeze names itself. Mutex is one; a lock it does not own (the database's one connection) calls Take and Give.
type Held struct {
	holder atomic.Pointer[holding]
}

// holding is one hold of the lock.
type holding struct {
	since time.Time
	pcs   []uintptr // where it was taken
}

// Take records that the lock is held from now, by its caller.
func (h *Held) Take() { h.take(2) }

func (h *Held) take(skip int) {
	var pcs [24]uintptr
	n := runtime.Callers(skip+1, pcs[:])
	h.holder.Store(&holding{since: time.Now(), pcs: pcs[:n:n]})
}

// Give records that the lock is no longer held.
func (h *Held) Give() { h.holder.Store(nil) }

// Watch looks at the lock every second until ctx ends: held past HeldLong, it logs at warn where its holder took it;
// past HeldFrozen, once more at error, with a dump of every goroutine to dir (Dump), which shows what the holder
// waits for. Each hold is logged once at each level. dir "": no dump.
func (h *Held) Watch(ctx context.Context, name, dir string) {
	h.watch(ctx, name, dir, HeldLong, HeldFrozen, time.Second)
}

func (h *Held) watch(ctx context.Context, name, dir string, long, frozen, every time.Duration) {
	defer Crash("lock watch", "lock", name)
	tick := time.NewTicker(every)
	defer tick.Stop()
	var warned, dumped *holding
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		hold := h.holder.Load()
		if hold == nil {
			continue
		}
		held := time.Since(hold.since)
		if held >= long && hold != warned {
			warned = hold
			L().Warn("lock held long", "lock", name, "held", held.Round(time.Second), "taken at", frames(hold.pcs))
		}
		if held >= frozen && hold != dumped {
			dumped = hold
			args := []any{"lock", name, "held", held.Round(time.Second), "taken at", frames(hold.pcs)}
			if dir != "" {
				path, err := Dump(dir, "lock "+name+" held "+held.Round(time.Second).String())
				args = append(args, "dump", path)
				if err != nil {
					args = append(args, "err", err)
				}
			}
			L().Error("lock held: frozen", args...)
		}
	}
}

// frames are the functions at pcs, innermost first, with their lines.
func frames(pcs []uintptr) string {
	var b strings.Builder
	fs := runtime.CallersFrames(pcs)
	for {
		f, more := fs.Next()
		if b.Len() > 0 {
			b.WriteString(" < ")
		}
		file := f.File // Its folder and name: harness/schedule.go.
		if i := strings.LastIndex(file, "/"); i > 0 {
			if j := strings.LastIndex(file[:i], "/"); j >= 0 {
				file = file[j+1:]
			}
		}
		b.WriteString(f.Function[strings.LastIndex(f.Function, "/")+1:] + " " + file + ":" + strconv.Itoa(f.Line))
		if !more {
			return b.String()
		}
	}
}

// Mutex is a sync.Mutex that knows its holder: Watch logs one that keeps it past HeldLong.
type Mutex struct {
	mu   sync.Mutex
	held Held
}

// Lock locks m and records the caller as its holder.
func (m *Mutex) Lock() {
	m.mu.Lock()
	m.held.take(2)
}

// TryLock locks m if it is free, and says whether it did.
func (m *Mutex) TryLock() bool {
	if !m.mu.TryLock() {
		return false
	}
	m.held.take(2)
	return true
}

// Unlock unlocks m.
func (m *Mutex) Unlock() {
	m.held.Give()
	m.mu.Unlock()
}

// Watch watches m until ctx ends (Held.Watch).
func (m *Mutex) Watch(ctx context.Context, name, dir string) { m.held.Watch(ctx, name, dir) }
