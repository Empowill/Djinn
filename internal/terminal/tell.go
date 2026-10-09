package terminal

import (
	"bytes"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	// pasteOn and pasteOff turn bracketed paste on and off, from the program.
	pasteOn, pasteOff = "\x1b[?2004h", "\x1b[?2004l"
	// maxDraft is how much of what the user types Tell keeps: enough to erase a word, bounded whatever is pasted.
	maxDraft = 4 << 10
)

var (
	// settle is how long the user leaves the keyboard before Tell types: a key typed just before would mix with it.
	settle = time.Second
	// enterGap is how long Tell waits between the line and Enter: an agent that takes quick keys for a paste would
	// take an Enter that follows them for a line break.
	enterGap = 300 * time.Millisecond
	// tellPoll is how often Tell looks again while it waits on the keyboard.
	tellPoll = 200 * time.Millisecond
	// typingIdle is how long something under way at the prompt holds Tell without a key, where the program shows
	// no prompt Tell reads (underWay): there Tell follows the keys, and loses track of an edit it does not know (a
	// word erased by a key of the user's own).
	typingIdle = 30 * time.Second
)

// A choice that keys answer shows a hint: a key, "to" and what it does, that ends its line or a part of it between
// "·" (Claude Code adds "· Tab to amend" and more after its own). Claude Code's permission
// prompts, menus and folder trust ("Enter to confirm · Esc to cancel", or "Esc to exit"), its first run's
// onboarding ("Enter to confirm"), Codex's approvals ("Press enter to confirm or esc to cancel"). A line typed there
// would pick an option (a digit picks by number), and its Enter confirm one: trust a folder for the developer, or
// leave. Neither agent shows one at its prompt at rest (Claude Code 2.1.295 in auto mode, Codex 0.162), and prose
// seldom does: a sentence ends with a period.
var (
	choiceKeys  = []string{"esc", "escape", "enter", "return", "⏎", "↵"}
	choiceVerbs = []string{"cancel", "exit", "confirm", "continue"}
)

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

// Wait is why a text told waits before it is typed.
type Wait int

const (
	// NoWait: nothing holds the text.
	NoWait Wait = iota
	// WaitTyping: the user has a line under way at the prompt.
	WaitTyping
	// WaitChoice: the program shows a choice that keys answer.
	WaitChoice
)

// post is a text Post keeps to tell, and what to call if it fails.
type post struct {
	text   string
	failed func(error)
}

// Post tells text as TellText does, without waiting: the texts posted arrive in the order posted, and failed, if not
// nil, gets the error of one that could not be typed. Post tells whether the text waits for the user, and why: a line
// under way at the prompt, or a choice on screen.
func (t *Terminal) Post(text string, failed func(error)) Wait {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.posted = append(t.posted, post{text, failed})
	if !t.posting {
		t.posting = true
		go t.drain()
	}
	switch {
	case t.choosing():
		return WaitChoice
	case t.underWay():
		return WaitTyping
	}
	return NoWait
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
		composing, choosing, quiet := t.underWay(), t.choosing(), time.Since(t.lastKey)
		changed := t.changed
		t.mu.Unlock()
		if !composing && !choosing && quiet >= settle {
			break
		}
		var poll <-chan time.Time
		if !composing && !choosing {
			poll = time.After(settle - quiet)
		} else if composing {
			poll = time.After(tellPoll) // Keys change nothing on screen we watch, and what is under way expires.
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

// choosing tells whether the program shows a choice that keys answer, on its screen now: a choice it covered since,
// and what scrolled away, hold nothing. t.mu is held.
func (t *Terminal) choosing() bool {
	for _, row := range strings.Split(t.screen.text(), "\n") {
		for _, part := range strings.Split(row, "·") {
			w := strings.Fields(strings.ToLower(strings.TrimRight(part, " …")))
			for i := range w {
				w[i] = strings.Trim(w[i], "()[]")
			}
			if n := len(w); n >= 3 && w[n-2] == "to" && slices.Contains(choiceVerbs, w[n-1]) &&
				slices.Contains(choiceKeys, w[n-3]) {
				return true
			}
		}
	}
	return false
}

// underWay tells whether the user has something under way at the prompt that a line told would join. Where the
// agent shows its prompt (Claude Code, Codex), the screen says it: text there that is not its placeholder, however
// long ago it was typed, and whatever edit brought it there. A word left at the prompt while the developer thinks
// holds the line until it is sent or cleared: a line told would be glued to it. Elsewhere the keys say it: typed and
// not sent nor cleared, or a line recalled from the history, with a key less than typingIdle ago. t.mu is held.
func (t *Terminal) underWay() bool {
	if shown, holds := t.screen.prompt(); shown {
		return holds
	}
	return (len(t.draft) > 0 || t.recalled) && time.Since(t.lastKey) < typingIdle
}

// typed follows what the user types at the program's prompt: whether something is under way there, that a line
// told would join. Printable keys add; Backspace takes a character off, Ctrl+W and Alt+Backspace a word; Enter (not
// after a backslash, which makes it a line break), Ctrl+C, Ctrl+U and Esc twice clear; Up and Down at an empty prompt
// recall a line of the history. Keys that answer a choice on screen type nothing. What the terminal sends on its own
// (reports) is no key. t.mu is held.
func (t *Terminal) typed(b []byte) {
	if b = withoutReports(b); len(b) == 0 {
		return
	}
	t.lastKey = time.Now()
	if t.choosing() {
		t.escaped = false
		return
	}
	reset := func() { t.draft, t.recalled, t.lastTyped = t.draft[:0], false, 0 }
	paste := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		escaped := t.escaped
		t.escaped = false
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
			t.lastTyped = 0
			switch {
			case i+1 == len(b): // Esc alone: twice clears the prompt.
				if escaped {
					reset()
				} else {
					t.escaped = true
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
				if i < len(b) && (b[i] == 'A' || b[i] == 'B') && len(t.draft) == 0 {
					t.recalled = true // Up or Down at an empty prompt: a line of the history, maybe.
				}
			default: // Alt and a key.
				i++
			}
		case c == '\r' && !paste && t.lastTyped != '\\':
			reset()
		case c == 0x03 || c == 0x15:
			reset()
		case c == 0x17:
			t.eraseWord()
		case c == 0x7f || c == 0x08:
			t.draft, t.lastTyped = t.draft[:max(0, len(t.draft)-1)], 0
		case c >= 0x20 || c == '\r' || c == '\n' || c == '\t':
			if c < 0x80 || c >= 0xc0 { // One character: a lead byte, not a continuation one.
				if len(t.draft) == maxDraft {
					t.draft = append(t.draft[:0], t.draft[maxDraft/2:]...)
				}
				t.draft = append(t.draft, rune(c))
			}
			t.lastTyped = c
		}
	}
}

// withoutReports is b without what a terminal sends on its own, which the user did not type: answers to the
// program's queries and reports of the mouse and the focus. Codex asks the colors at start (OSC 10 and 11) and
// xterm.js answers them as keys would come, ESC ] 10;rgb:…, which read as Alt+] and text would leave a line under way
// that no Enter of the user's ever clears. Codex and Claude Code follow the mouse (mode 1003): a pointer moving over
// the terminal is no typing either. A key itself never ends so: a string (OSC, DCS, APC, PM, SOS) up to BEL or ST, a
// mouse report (CSI < … M or m, or CSI M and three bytes), focus (CSI I, CSI O), the cursor's position (CSI … R), the
// device's attributes (CSI ? … c, CSI > … c), a mode (CSI ? … $ y) or the keyboard's flags (CSI ? … u).
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
	n := len(t.draft)
	for n > 0 && unicode.IsSpace(t.draft[n-1]) {
		n--
	}
	for n > 0 && !unicode.IsSpace(t.draft[n-1]) {
		n--
	}
	t.draft, t.lastTyped = t.draft[:n], 0
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
