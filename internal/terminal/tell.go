package terminal

import (
	"bytes"
	"strings"
	"time"
	"unicode"
)

const (
	// choiceTail is how much of the last output tells whether the program shows a choice: a choice drawn longer
	// ago has been covered since.
	choiceTail = 8 << 10
	// pasteOn and pasteOff turn bracketed paste on and off, from the program.
	pasteOn, pasteOff = "\x1b[?2004h", "\x1b[?2004l"
)

var (
	// settle is how long the user leaves the keyboard before Tell types: a key typed just before would mix with it.
	settle = time.Second
	// enterGap is how long Tell waits between the line and Enter: an agent that takes quick keys for a paste would
	// take an Enter that follows them for a line break.
	enterGap = 300 * time.Millisecond
	// tellPoll is how often Tell looks again while it waits on the keyboard.
	tellPoll = 200 * time.Millisecond
)

// choiceMarks are what an agent shows under a choice that keys answer, letters and spaces removed: Claude Code's
// permission prompts and menus ("Esc to cancel"), Codex's approvals ("Press enter to confirm or esc to cancel").
// A line typed there would pick an option, and its Enter confirm one.
var choiceMarks = []string{"tocancel"}

// Tell types line into the program as if the user had, then Enter, once the user has nothing under way at the
// prompt and no choice is on screen. An agent that is busy answering takes the line into its queue, as Claude Code
// and Codex do with what is typed meanwhile. Lines told one after the other arrive in order. Control characters of
// line are dropped: it is one line. Tell returns once the line is typed, or with ErrExited when the program ended
// first.
func (t *Terminal) Tell(line string) error {
	return t.typeIn(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, line), false)
}

// TellText is Tell for a text the user wrote, which may hold line breaks: they stay line breaks in a program that
// takes bracketed paste, as in a paste, and become spaces in one that does not, where each would send a line.
func (t *Terminal) TellText(text string) error {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	return t.typeIn(strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, text), true)
}

// post is a text Post keeps to tell, and what to call if it fails.
type post struct {
	text   string
	failed func(error)
}

// Post tells text as TellText does, without waiting: the texts posted arrive in the order posted, and failed, if not
// nil, gets the error of one that could not be typed. Post tells whether the text waits for the user: a line under
// way at the prompt, or a choice on screen.
func (t *Terminal) Post(text string, failed func(error)) (waits bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.posted = append(t.posted, post{text, failed})
	if !t.posting {
		t.posting = true
		go t.drain()
	}
	return t.composing > 0 || t.choosing()
}

// drain tells what is posted, one text after the other, until none is left.
func (t *Terminal) drain() {
	for {
		t.mu.Lock()
		if len(t.posted) == 0 {
			t.posting = false
			t.mu.Unlock()
			return
		}
		next := t.posted[0]
		t.posted = t.posted[1:]
		t.mu.Unlock()
		if err := t.TellText(next.text); err != nil && next.failed != nil {
			next.failed(err)
		}
	}
}

// typeIn types text as Tell does; with lines, a line break in it stays one if the program takes bracketed paste.
func (t *Terminal) typeIn(line string, lines bool) error {
	t.tell.Lock()
	defer t.tell.Unlock()
	for {
		t.mu.Lock()
		composing, choosing, quiet := t.composing > 0, t.choosing(), time.Since(t.lastKey)
		changed := t.changed
		t.mu.Unlock()
		if !composing && !choosing && quiet >= settle {
			break
		}
		var poll <-chan time.Time
		if !composing && !choosing {
			poll = time.After(settle - quiet)
		} else if composing {
			poll = time.After(tellPoll) // Keys change nothing on screen we watch.
		}
		select {
		case <-t.done:
			return ErrExited
		case <-changed:
		case <-poll:
		}
	}
	if t.Exited() {
		return ErrExited
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.mu.Lock()
	paste := t.paste
	t.mu.Unlock()
	text := line
	if lines && !paste {
		text = strings.ReplaceAll(text, "\n", " ")
	}
	if paste { // The program tells a paste from keys: the line is one, and Enter after it submits.
		// A terminal pastes a line break as Enter, which a paste keeps from sending.
		text = "\x1b[200~" + strings.ReplaceAll(text, "\n", "\r") + "\x1b[201~"
	}
	if _, err := t.p.Write([]byte(text)); err != nil {
		return err
	}
	select {
	case <-t.done:
		return ErrExited
	case <-time.After(enterGap):
	}
	_, err := t.p.Write([]byte("\r"))
	return err
}

// choosing tells whether the program shows a choice that keys answer; t.mu is held.
func (t *Terminal) choosing() bool {
	tail := t.buf[max(0, len(t.buf)-choiceTail):]
	plain := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, string(stripEscapes(tail))))
	for _, mark := range choiceMarks {
		if strings.Contains(plain, mark) {
			return true
		}
	}
	return false
}

// stripEscapes removes the escape sequences of output: CSI, OSC and the two-byte ones.
func stripEscapes(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != 0x1b {
			out = append(out, b[i])
			continue
		}
		if i+1 >= len(b) {
			break
		}
		switch b[i+1] {
		case '[': // CSI: parameters, then a final byte from @ to ~.
			i += 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
		case ']': // OSC: up to BEL or ST.
			i += 2
			for i < len(b) && b[i] != 0x07 && !(b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\') {
				i++
			}
			if i < len(b) && b[i] == 0x1b {
				i++
			}
		default:
			i++
		}
	}
	return out
}

// typed follows what the user types at the program's prompt: whether something is under way there, that a line
// told would join. Printable keys add, Backspace takes off, Enter (not after a backslash, which makes it a line
// break), Ctrl+C and Ctrl+U clear. Keys that answer a choice on screen type nothing. t.mu is held.
func (t *Terminal) typed(b []byte) {
	t.lastKey = time.Now()
	if t.choosing() {
		return
	}
	paste := false
	for i := 0; i < len(b); i++ {
		c := b[i]
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
			// A key that moves or edits: it adds nothing. Its sequence ends at its final byte.
			if i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
				i += 2
				for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
			} else {
				i++
			}
			t.lastTyped = 0
		case c == '\r' && !paste && t.lastTyped != '\\':
			t.composing, t.lastTyped = 0, 0
		case c == 0x03 || c == 0x15:
			t.composing, t.lastTyped = 0, 0
		case c == 0x7f || c == 0x08:
			t.composing, t.lastTyped = max(0, t.composing-1), 0
		case c >= 0x20 || c == '\r' || c == '\n' || c == '\t':
			if c < 0x80 || c >= 0xc0 { // One character: a lead byte, not a continuation one.
				t.composing++
			}
			t.lastTyped = c
		}
	}
}

// pasteMode follows whether the program turned bracketed paste on, in the output from offset from of t.buf; t.mu
// is held.
func (t *Terminal) pasteMode(from int) {
	seen := t.buf[max(0, from-len(pasteOn)+1):]
	on, off := bytes.LastIndex(seen, []byte(pasteOn)), bytes.LastIndex(seen, []byte(pasteOff))
	if on > off {
		t.paste = true
	} else if off > on {
		t.paste = false
	}
}
