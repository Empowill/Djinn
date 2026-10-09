package terminal

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// Djinn types a line into a terminal for the person: the lead of a wish learns so that the person answered one of
// its questions in the window. The line waits until the person stops typing, so that it never mixes with their keys.

const (
	// Quiet is how long the person must not have typed in a terminal before Djinn types a line there.
	Quiet = 3 * time.Second
	// SayQueue is how many lines may wait in a terminal for the person to be quiet.
	SayQueue = 16
)

var (
	// quiet is Quiet, shortened by the tests.
	quiet = Quiet
	// settle is how long the output of a program just started must pause before a line goes to it: it has drawn its
	// screen and reads its input.
	settle = time.Second
	// warmup is how long a program may draw before a line goes to it anyway.
	warmup = 10 * time.Second
	// enter is the pause between a line and its Enter: an agent's prompt reads a line and an Enter that come
	// together as a paste, and keeps the Enter in the text.
	enter = 300 * time.Millisecond
)

// ErrFull says that SayQueue lines already wait for the terminal.
var ErrFull = errors.New("too many lines wait for this terminal")

// voice is what Say needs to know of a terminal.
type voice struct {
	started time.Time   // when the program started
	output  time.Time   // the last output of the program
	typed   time.Time   // the last key of the person
	lines   chan string // the lines waiting, read by speak; nil until the first one
}

// Say types line into the running terminal of name: see Terminal.Say. ErrNotFound when none runs.
func (m *Manager) Say(name, line string) error {
	m.mu.Lock()
	t := m.byName[name]
	m.mu.Unlock()
	if t == nil || t.Exited() {
		return ErrNotFound
	}
	return t.Say(line)
}

// Say types line into the program, then Enter, as the person would: once the person has not typed for Quiet, and a
// program just started has drawn its screen. Lines go out one at a time, in the order of the calls. Say returns at
// once: ErrExited when the program ended, ErrFull when SayQueue lines wait already. Line breaks and other control
// characters become spaces: Djinn types one line.
func (t *Terminal) Say(line string) error {
	line = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, line))
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exited {
		return ErrExited
	}
	if t.voice.lines == nil {
		t.voice.lines = make(chan string, SayQueue)
		go t.speak(t.voice.lines)
	}
	select {
	case t.voice.lines <- line:
		return nil
	default:
		return ErrFull
	}
}

// speak types the lines as they come, until the program ends.
func (t *Terminal) speak(lines <-chan string) {
	for {
		select {
		case <-t.done:
			return
		case line := <-lines:
			if !t.ready() || t.put([]byte(line)) != nil || !t.sleep(enter) || t.put([]byte("\r")) != nil {
				return
			}
		}
	}
}

// ready waits until a line may go to the program: the person has not typed for quiet, and a program just started
// has paused its output for settle, or drawn for warmup. False when the program ended first.
func (t *Terminal) ready() bool {
	for {
		t.mu.Lock()
		v := t.voice
		t.mu.Unlock()
		now := time.Now()
		at, step := v.typed.Add(quiet), time.Duration(0)
		if now.Sub(v.started) < warmup {
			drawn := v.started.Add(warmup)
			if !v.output.IsZero() && v.output.Add(settle).Before(drawn) {
				drawn = v.output.Add(settle)
			}
			if drawn.After(at) {
				at, step = drawn, settle // The output may come meanwhile: look again.
			}
		}
		wait := at.Sub(now)
		if wait <= 0 {
			return true
		}
		if step > 0 && step < wait {
			wait = step
		}
		if !t.sleep(wait) {
			return false
		}
	}
}

// sleep waits for d; false when the program ended first.
func (t *Terminal) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.done:
		return false
	case <-timer.C:
		return true
	}
}
