package logx

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// HungExit is the exit code of a djinn up whose stop outlived ExitAfter.
const HungExit = 3

// Shutdown follows the stop of djinn up step by step in the log, and keeps it from hanging: past DumpAfter it dumps
// the goroutines and logs the steps still running; past ExitAfter it logs them again, dumps again, runs BeforeExit and
// exits with HungExit.
type Shutdown struct {
	// Dir receives the dumps.
	Dir string
	// DumpAfter and ExitAfter count from Begin.
	DumpAfter, ExitAfter time.Duration
	// BeforeExit runs before a hung stop exits: a restart starts the newer djinn there. Nil does nothing.
	BeforeExit func()
	// Exit ends the process: os.Exit, unless a test replaces it.
	Exit func(code int)

	mu      sync.Mutex
	begun   time.Time
	timers  []*time.Timer
	running map[*step]bool
	ended   bool
}

type step struct {
	name  string
	start time.Time
}

// NewShutdown is a Shutdown that dumps to dir after 10 s and exits after 30 s.
func NewShutdown(dir string) *Shutdown {
	return &Shutdown{Dir: dir, DumpAfter: 10 * time.Second, ExitAfter: 30 * time.Second, Exit: os.Exit}
}

// Begin starts the stop, for why: the first call logs it and arms the watchdog; the others do nothing.
func (s *Shutdown) Begin(why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.begun.IsZero() {
		return
	}
	s.begun = time.Now()
	L().Info("shutdown begins", "why", why)
	s.timers = append(s.timers,
		time.AfterFunc(s.DumpAfter, func() { s.overdue(false) }),
		time.AfterFunc(s.ExitAfter, func() { s.overdue(true) }))
}

// Start logs that the step name begins, and begins the stop if it has not; the returned function logs that it ended.
func (s *Shutdown) Start(name string) (end func()) {
	s.Begin(name)
	st := &step{name: name, start: time.Now()}
	s.mu.Lock()
	if s.running == nil {
		s.running = map[*step]bool{}
	}
	s.running[st] = true
	s.mu.Unlock()
	L().Debug("shutdown waits", "for", name)
	return func() {
		s.mu.Lock()
		delete(s.running, st)
		s.mu.Unlock()
		Took(slog.LevelInfo, "shutdown step ended", st.start, "step", name)
	}
}

// Step runs f as the step name.
func (s *Shutdown) Step(name string, f func()) {
	defer s.Start(name)()
	f()
}

// End logs that the stop ended, and disarms the watchdog.
func (s *Shutdown) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.begun.IsZero() {
		return
	}
	s.ended = true
	for _, t := range s.timers {
		t.Stop()
	}
	Took(slog.LevelInfo, "shutdown ended", s.begun)
}

// Running names the steps still running, the oldest first, with how long each has run.
func (s *Shutdown) Running() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := make([]*step, 0, len(s.running))
	for st := range s.running {
		steps = append(steps, st)
	}
	slices.SortFunc(steps, func(a, b *step) int { return a.start.Compare(b.start) })
	out := make([]string, len(steps))
	for i, st := range steps {
		out[i] = fmt.Sprintf("%s (%s)", st.name, time.Since(st.start).Round(time.Millisecond))
	}
	return out
}

func (s *Shutdown) overdue(exit bool) {
	s.mu.Lock()
	ended, since := s.ended, time.Since(s.begun).Round(time.Second)
	s.mu.Unlock()
	if ended {
		return
	}
	waiting := strings.Join(s.Running(), ", ")
	if waiting == "" {
		waiting = "no step: the window or the server"
	}
	why := fmt.Sprintf("the stop has taken %s, still waiting for %s", since, waiting)
	if !exit {
		L().Warn("shutdown is slow", "took", since, "waiting", waiting)
		Dump(s.Dir, why) //nolint:errcheck // Dump logs where; a failed dump leaves the log line.
		return
	}
	L().Error("shutdown hangs: exiting", "took", since, "waiting", waiting, "code", HungExit)
	Dump(s.Dir, why) //nolint:errcheck // As above.
	if s.BeforeExit != nil {
		s.BeforeExit()
	}
	fmt.Fprintf(os.Stderr, "djinn: %s; exiting (see %s)\n", why, s.Dir)
	s.Exit(HungExit)
}
