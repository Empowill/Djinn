package terminal

import (
	"bytes"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/empowill/djinn/internal/logx"
)

// Djinn types a line into a terminal for the person: the lead of a wish learns so that the person answered one of
// its questions in the window. The line waits until the person stops typing, so that it never mixes with their keys,
// and goes only to the program's prompt: never into a choice on screen (a digit there picks an option, its Enter
// confirms one), never joined to a line the person left under way.

const (
	// Quiet is how long the person must not have typed in a terminal before Djinn types a line there.
	Quiet = 3 * time.Second
	// SayQueue is how many lines may wait in a terminal for the person to be quiet.
	SayQueue = 16
	// pasteOn and pasteOff turn bracketed paste on and off, from the program.
	pasteOn, pasteOff = "\x1b[?2004h", "\x1b[?2004l"
	// maxDraft is how much of what the person types Say keeps: enough to erase a word, bounded whatever is pasted.
	maxDraft = 4 << 10
)

// pace is how long Say, Choose and Hangup wait. A manager's terminals take its Config.pace: the tests shorten it.
type pace struct {
	// quiet is how long the person must not have typed before Djinn types a line: Quiet.
	quiet time.Duration
	// settle is how long the output of a program just started must pause before a line goes to it: it has drawn its
	// screen and reads its input.
	settle time.Duration
	// warmup is how long a program may draw before a line goes to it anyway.
	warmup time.Duration
	// enter is the pause between a line and its Enter: an agent's prompt reads a line and an Enter that come
	// together as a paste, and keeps the Enter in the text.
	enter time.Duration
	// afterEnter is the pause after Enter before the next line: it lets the agent's prompt accept the submission
	// and clear its prompt box before another line arrives.
	afterEnter time.Duration
	// hold is how often Say looks again while a choice on screen or a line under way holds its line.
	hold time.Duration
	// typingIdle is how long a line under way holds Say without a key, where the program shows no prompt Say reads
	// (underWay): there Say follows the keys, and loses track of an edit it does not know (a word erased by a key of
	// the person's own).
	typingIdle time.Duration
	// keyGap is how long Choose waits between two keys: an agent that reads keys in one go would take them as a
	// paste.
	keyGap time.Duration
	// grace is how long a program has to end after its terminal hangs up: Grace.
	grace time.Duration
}

// defaultPace is the pace of djinn up.
var defaultPace = pace{
	quiet: Quiet, settle: time.Second, warmup: 10 * time.Second, enter: 300 * time.Millisecond,
	afterEnter: 300 * time.Millisecond, hold: 200 * time.Millisecond, typingIdle: 30 * time.Second,
	keyGap: 80 * time.Millisecond, grace: Grace,
}

// ErrFull says that SayQueue lines already wait for the terminal.
var ErrFull = errors.New("too many lines wait for this terminal")

// voice is what Say needs to know of a terminal.
type voice struct {
	started   time.Time     // when the program started
	output    time.Time     // the last output of the program
	typed     time.Time     // the last key of the person
	lines     chan string   // the lines waiting, read by speak; nil until the first one
	hushed    chan struct{} // closed once speak returned; nil until the first line
	draft     []rune        // what the person typed at the prompt since it was last sent or cleared, a rune per character
	recalled  bool          // a line of the history may be at the prompt
	escaped   bool          // the last key was Esc alone
	lastTyped byte          // the last byte typed
	paste     bool          // the program takes bracketed paste
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

// SayText types text into the running terminal of name: see Terminal.SayText. ErrNotFound when none runs.
func (m *Manager) SayText(name, text string) (waiting bool, err error) {
	m.mu.Lock()
	t := m.byName[name]
	m.mu.Unlock()
	if t == nil || t.Exited() {
		return false, ErrNotFound
	}
	return t.SayText(text)
}

// Say types line into the program, then Enter, as the person would: once the person has not typed for Quiet, a
// program just started has drawn its screen, no choice is on screen and nothing is under way at the prompt (ready).
// A program that turned bracketed paste on gets the line as a paste, so that it takes it whole, then Enter on its
// own. Lines go out one at a time, in the order of the calls. Say returns at once: ErrExited when the program ended,
// ErrFull when SayQueue lines wait already. Line breaks and other control characters become spaces: Djinn types one
// line.
func (t *Terminal) Say(line string) error {
	_, err := t.queue(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, line))
	return err
}

// SayText is Say for a text the person wrote, which may hold line breaks: they stay line breaks in a program that
// turned bracketed paste on, inside the one paste, and become spaces in one that did not, where each would send a
// line. Tabs and other control characters become spaces. SayText tells whether the text waits for the person: they
// are typing in the terminal, have something under way at its prompt, or a choice is on screen.
func (t *Terminal) SayText(text string) (waiting bool, err error) {
	return t.queue(strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)))
}

// queue puts text, without control characters but line breaks, on the lines that wait for the program, trimmed;
// waiting tells whether the person holds it.
func (t *Terminal) queue(text string) (waiting bool, err error) {
	text = strings.TrimSpace(text)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exited {
		return false, ErrExited
	}
	if t.voice.lines == nil {
		t.voice.lines, t.voice.hushed = make(chan string, SayQueue), make(chan struct{})
		go t.speak(t.voice.lines, t.voice.hushed)
	}
	select {
	case t.voice.lines <- text:
		return t.choosing() || t.underWay() || time.Since(t.voice.typed) < t.pace.quiet, nil
	default:
		return false, ErrFull
	}
}

// speak types the lines as they come, until the program ends; then it closes hushed.
func (t *Terminal) speak(lines <-chan string, hushed chan<- struct{}) {
	defer logx.Crash("terminal say", "terminal", t.Name)
	defer close(hushed)
	for {
		select {
		case <-t.done:
			return
		case line := <-lines:
			if !t.ready() {
				return
			}
			t.mu.Lock()
			paste := t.voice.paste
			t.mu.Unlock()
			if paste { // The program tells a paste from keys: the line is one, and the Enter after it submits.
				// A terminal pastes a line break as Enter, which the paste keeps from submitting.
				line = "\x1b[200~" + strings.ReplaceAll(line, "\n", "\r") + "\x1b[201~"
			} else {
				line = strings.ReplaceAll(line, "\n", " ")
			}
			if t.put([]byte(line)) != nil || !t.sleep(t.pace.enter) || t.put([]byte("\r")) != nil || !t.sleep(t.pace.afterEnter) {
				return
			}
		}
	}
}

// ready waits until a line may go to the program: no choice shows on its screen and nothing is under way at its
// prompt, the person has not typed for quiet, and a program just started has paused its output for settle, or drawn
// for warmup. False when the program ended first.
func (t *Terminal) ready() bool {
	for {
		t.mu.Lock()
		v, changed := t.voice, t.changed
		held := t.choosing() || t.underWay()
		t.mu.Unlock()
		if held {
			// A choice goes with the output that erases it; a line under way, with a key, the output that shows the
			// prompt cleared, or typingIdle. Look again at each output, and every hold.
			if !t.await(changed, t.pace.hold) {
				return false
			}
			continue
		}
		now := time.Now()
		p := t.pace
		at, step := v.typed.Add(p.quiet), time.Duration(0)
		if now.Sub(v.started) < p.warmup {
			drawn := v.started.Add(p.warmup)
			if !v.output.IsZero() && v.output.Add(p.settle).Before(drawn) {
				drawn = v.output.Add(p.settle)
			}
			if drawn.After(at) {
				at, step = drawn, p.settle // The output may come meanwhile: look again.
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
func (t *Terminal) sleep(d time.Duration) bool { return t.await(nil, d) }

// await waits for d, or until changed is closed; false when the program ended first.
func (t *Terminal) await(changed <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.done:
		return false
	case <-changed:
		return true
	case <-timer.C:
		return true
	}
}

// underWay tells whether the person has something under way at the prompt that a line typed would join. Where the
// agent shows its prompt (Claude Code, Codex: screen.prompt), the screen says it: text there that is not its
// placeholder, however long ago it was typed, and whatever edit brought it there. A word left at the prompt while the
// person thinks holds the line until it is sent or cleared: the line would be glued to it. Elsewhere the keys say it:
// typed and not sent nor cleared, or a line recalled from the history, with a key less than typingIdle ago. t.mu is
// held.
func (t *Terminal) underWay() bool {
	if shown, holds := t.screen.prompt(); shown {
		return holds
	}
	v := &t.voice
	return (len(v.draft) > 0 || v.recalled) && time.Since(v.typed) < t.pace.typingIdle
}

// keys follows what the person types at the program's prompt: when they last typed, and whether something is under
// way there that a line typed would join. Printable keys add; Backspace takes a character off, Ctrl+W and
// Alt+Backspace a word; Enter (not after a backslash, which makes it a line break), Ctrl+C, Ctrl+U and Esc twice
// clear; Up and Down at an empty prompt recall a line of the history. Keys that answer a choice on screen type
// nothing. What the terminal sends on its own (reports) is no key. t.mu is held.
func (t *Terminal) keys(b []byte) {
	if b = withoutReports(b); len(b) == 0 {
		return
	}
	v := &t.voice
	v.typed = time.Now()
	if t.choosing() {
		v.escaped = false
		return
	}
	reset := func() { v.draft, v.recalled, v.lastTyped = v.draft[:0], false, 0 }
	paste := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		escaped := v.escaped
		v.escaped = false
		switch {
		case c == 0x1b:
			if bytes.HasPrefix(b[i:], []byte("\x1b[200~")) {
				paste, i = true, i+5
				continue
			}
			if bytes.HasPrefix(b[i:], []byte("\x1b[201~")) {
				paste, i = false, i+5
				continue
			}
			v.lastTyped = 0
			switch {
			case i+1 == len(b): // Esc alone: twice clears the prompt.
				if escaped {
					reset()
				} else {
					v.escaped = true
				}
			case b[i+1] == 0x1b:
				reset()
				i++
			case b[i+1] == 0x7f || b[i+1] == 0x08: // Alt+Backspace
				t.eraseWord()
				i++
			case b[i+1] == '[' || b[i+1] == 'O':
				// A key that moves or edits: it adds nothing. Its sequence ends at its final byte.
				i += 2
				for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
				if i < len(b) && (b[i] == 'A' || b[i] == 'B') && len(v.draft) == 0 {
					v.recalled = true // Up or Down at an empty prompt: a line of the history, maybe.
				}
			default: // Alt and a key.
				i++
			}
		case c == '\r' && !paste && v.lastTyped != '\\':
			reset()
		case c == 0x03 || c == 0x15:
			reset()
		case c == 0x17:
			t.eraseWord()
		case c == 0x7f || c == 0x08:
			v.draft, v.lastTyped = v.draft[:max(0, len(v.draft)-1)], 0
		case c >= 0x20 || c == '\r' || c == '\n' || c == '\t':
			if c < 0x80 || c >= 0xc0 { // One character: a lead byte, not a continuation one.
				if len(v.draft) == maxDraft {
					v.draft = append(v.draft[:0], v.draft[maxDraft/2:]...)
				}
				v.draft = append(v.draft, rune(c))
			}
			v.lastTyped = c
		}
	}
}

// withoutReports is b without what a terminal sends on its own, which the person did not type: answers to the
// program's queries and reports of the mouse and the focus. Codex asks the colors at start (OSC 10 and 11) and
// xterm.js answers them as keys would come, ESC ] 10;rgb:…, which read as Alt+] and text would leave a line under way
// that no Enter of the person's ever clears. Codex and Claude Code follow the mouse (mode 1003): a pointer moving
// over the terminal is no typing either. A key itself never ends so: a string (OSC, DCS, APC, PM, SOS) up to BEL or
// ST, a mouse report (CSI < … M or m, or CSI M and three bytes), focus (CSI I, CSI O), the cursor's position (CSI …
// R), the device's attributes (CSI ? … c, CSI > … c), a mode (CSI ? … $ y) or the keyboard's flags (CSI ? … u).
func withoutReports(b []byte) []byte {
	if !bytes.Contains(b, []byte{0x1b}) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] != 0x1b || i+1 == len(b) {
			out = append(out, b[i])
			i++
			continue
		}
		switch b[i+1] {
		case ']', 'P', '_', '^', 'X':
			j := i + 2
			for j < len(b) && b[j] != 0x07 && (b[j] != 0x1b || j+1 == len(b) || b[j+1] != '\\') {
				j++
			}
			if j < len(b) && b[j] == 0x1b {
				j++
			}
			i = min(len(b), j+1)
			continue
		case '[':
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j == len(b) {
				break
			}
			params, final := b[i+2:j], b[j]
			report := false
			switch {
			case final == 'M' && len(params) == 0 && j+3 < len(b): // X10 mouse: three bytes follow.
				i = j + 4
				continue
			case (final == 'M' || final == 'm') && bytes.HasPrefix(params, []byte("<")),
				(final == 'I' || final == 'O') && len(params) == 0,
				final == 'R',
				final == 'c' && len(params) > 0 && (params[0] == '?' || params[0] == '>'),
				final == 'y' && bytes.HasSuffix(params, []byte("$")),
				final == 'u' && bytes.HasPrefix(params, []byte("?")):
				report = true
			}
			if report {
				i = j + 1
				continue
			}
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// eraseWord follows Ctrl+W: the spaces before the cursor go, then the word before them. t.mu is held.
func (t *Terminal) eraseWord() {
	v := &t.voice
	n := len(v.draft)
	for n > 0 && unicode.IsSpace(v.draft[n-1]) {
		n--
	}
	for n > 0 && !unicode.IsSpace(v.draft[n-1]) {
		n--
	}
	v.draft, v.lastTyped = v.draft[:n], 0
}

// pasteMode follows whether the program turned bracketed paste on, in the output from offset from of t.buf; t.mu
// is held.
func (t *Terminal) pasteMode(from int) {
	seen := t.buf[max(0, from-len(pasteOn)+1):]
	on, off := bytes.LastIndex(seen, []byte(pasteOn)), bytes.LastIndex(seen, []byte(pasteOff))
	if on > off {
		t.voice.paste = true
	} else if off > on {
		t.voice.paste = false
	}
}
