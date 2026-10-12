package terminal

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Prompt is a choice a program shows on its screen, waiting for keys: an agent's approval of a command, an edit, the
// network, or a folder to trust. Claude Code and Codex draw it alike: a question, what it is about, numbered options
// (the one picked marked ❯ or ›), and a hint of the keys (choosing).
//
//	Bash command                                     Would you like to run the following command?
//	  djinn task list --wish-id …
//	Do you want to proceed?                          $ djinn wish brief …
//	❯ 1. Yes                                         › 1. Yes, proceed (y)
//	  2. Yes, and don't ask again for …                2. Yes, and don't ask again for commands that start with `djinn` (p)
//	  3. No, and tell Claude what to do differently    3. No, and tell Codex what to do differently (esc)
//	Esc to cancel                                    Press enter to confirm or esc to cancel
type Prompt struct {
	// Title is the question, the line ending with "?" nearest the options; empty when none does.
	Title string
	// Lines are what the choice is about, a line each, as on screen.
	Lines []string
	// Options are the options in order, without their number; a long one wrapped on screen is joined.
	Options []string
	// Selected is the option the program marks as picked, from 0; -1 when none is marked.
	Selected int
}

// ErrNoPrompt: the screen shows no choice, or another one than the window showed.
var ErrNoPrompt = errors.New("the terminal shows no such choice now")

// keyGap is how long Choose waits between two keys: an agent that reads keys in one go would take them as a paste.
var keyGap = 80 * time.Millisecond

// optionRow is a numbered option: an optional marker, its number, then its text.
var optionRow = regexp.MustCompile(`^([❯›>▶→]\s*)?(\d{1,2})[.)]\s+(\S.*)$`)

// Prompt returns the choice the program shows now, or nil when it shows none: no hint of the keys on screen, or no
// numbered options.
func (t *Terminal) Prompt() *Prompt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.prompt()
}

// prompt reads the choice on screen; t.mu is held.
func (t *Terminal) prompt() *Prompt {
	if t.exited || !t.choosing() {
		return nil
	}
	return readPrompt(strings.Split(strings.TrimRight(t.screen.text(), "\n"), "\n"))
}

// option is an option found on screen.
type option struct {
	row    int
	number int // 0 when the options have no number
	col    int // where its text starts, in characters
	text   string
	marked bool
}

// readPrompt finds, in the rows of a screen, the options just above the hint of the keys, and the question above
// them. Options are numbered from 1 (approvals), or not (Claude Code's folder trust: "❯ No, exit", then "Yes, I trust
// this folder" at the same column).
func readPrompt(rows []string) *Prompt {
	h := -1
	for i := len(rows) - 1; i >= 0 && h < 0; i-- {
		if hint(unbox(rows[i])) {
			h = i
		}
	}
	// The options end a few rows above the hint, and start after a blank row or a rule.
	end := h - 1
	for end >= 0 && end >= h-3 && blankRow(rows[end]) {
		end--
	}
	if h < 0 || end < 0 || blankRow(rows[end]) {
		return nil
	}
	start := end
	for start > 0 && !blankRow(rows[start-1]) && !rule(rows[start-1]) {
		start--
	}
	opts := numbered(rows, start, end)
	if len(opts) < 2 {
		opts = marked(rows, start, end)
	}
	if len(opts) < 2 {
		return nil
	}
	return promptOf(rows, opts)
}

// numbered reads the options numbered from 1 in rows start to end; a long one goes on at its text's column.
func numbered(rows []string, start, end int) []option {
	var opts []option
	for i := start; i <= end; i++ {
		row := unbox(rows[i])
		m := optionRow.FindStringSubmatch(strings.TrimSpace(row))
		if m == nil {
			if len(opts) > 0 && indent(row) >= opts[len(opts)-1].col {
				opts[len(opts)-1].text += " " + strings.TrimSpace(row)
			}
			continue
		}
		n, _ := strconv.Atoi(m[2])
		opt := option{row: i, number: n, text: m[3], marked: m[1] != ""}
		opt.col = indent(row) + len([]rune(m[0])) - len([]rune(m[3]))
		switch {
		case n == 1:
			opts = []option{opt}
		case len(opts) > 0 && n == opts[len(opts)-1].number+1:
			opts = append(opts, opt)
		}
	}
	return opts
}

// markedRow is the option an agent marks as picked, without a number.
var markedRow = regexp.MustCompile(`^(\s*[❯›>▶→]\s+)(\S.*)$`)

// marked reads options without numbers in rows start to end: the one marked, and the rows at its text's column
// around it; a row further right goes on the option above.
func marked(rows []string, start, end int) []option {
	at, col := -1, 0
	for i := start; i <= end && at < 0; i++ {
		if m := markedRow.FindStringSubmatch(unbox(rows[i])); m != nil {
			at, col = i, len([]rune(m[1]))
		}
	}
	if at < 0 {
		return nil
	}
	var opts []option
	for i := start; i <= end; i++ {
		row := unbox(rows[i])
		text := strings.TrimSpace(row)
		switch {
		case i == at:
			text = markedRow.FindStringSubmatch(row)[2]
			opts = append(opts, option{row: i, col: col, text: strings.TrimSpace(text), marked: true})
		case indent(row) == col:
			opts = append(opts, option{row: i, col: col, text: text})
		case indent(row) > col && len(opts) > 0:
			opts[len(opts)-1].text += " " + text
		case i > at:
			return opts
		default:
			opts = nil // A line before the options: the question, or what it is about.
		}
	}
	return opts
}

// blankRow tells whether row shows nothing but a frame's sides.
func blankRow(row string) bool { return strings.TrimSpace(unbox(row)) == "" }

// promptOf is the prompt of the options opts: the question nearest above them, and what lies between, or above it
// when nothing does (Claude Code shows what it is about first, then asks).
func promptOf(rows []string, opts []option) *Prompt {
	p := &Prompt{Selected: -1}
	for i, o := range opts {
		p.Options = append(p.Options, strings.TrimSpace(o.text))
		if o.marked {
			p.Selected = i
		}
	}
	// What lies above the options, up to a rule, two blank rows, or the top.
	var above []string
	blank := 0
	for i := opts[0].row - 1; i >= 0; i-- {
		row := strings.TrimSpace(unbox(rows[i]))
		if rule(rows[i]) {
			break
		}
		if row == "" {
			if blank++; blank == 2 {
				break
			}
			continue
		}
		blank = 0
		above = append(above, row)
	}
	slices.Reverse(above)
	// The question is the nearest line that asks one as the agents do; else the nearest line ending with "?": a
	// reason Codex shows under its question may end with one too.
	title := -1
	for i := len(above) - 1; i >= 0; i-- {
		if strings.HasSuffix(above[i], "?") && (title < 0 || asks(above[i])) {
			if title = i; asks(above[i]) {
				break
			}
		}
	}
	lines := above
	if title >= 0 {
		p.Title = above[title]
		if lines = above[title+1:]; len(lines) == 0 {
			lines = above[:title]
		}
	}
	p.Lines = slices.Clone(lines)
	return p
}

// asks tells whether line asks as the agents' choices do: "Do you want to proceed?", "Would you like to run the
// following command?", "Do you trust the files in this folder?".
func asks(line string) bool {
	for _, start := range []string{"Do you ", "Would you ", "Is this ", "Allow "} {
		if strings.HasPrefix(line, start) {
			return true
		}
	}
	return false
}

// unbox is row without the frame Claude Code may draw around a choice: its sides become spaces.
func unbox(row string) string {
	return strings.Map(func(r rune) rune {
		if r == '│' || r == '┃' {
			return ' '
		}
		return r
	}, row)
}

// rule tells whether row is a frame's edge or a rule across: only lines and corners.
func rule(row string) bool {
	row = strings.TrimSpace(row)
	return row != "" && strings.Trim(row, "─━═╭╮╰╯┌┐└┘╌┄ ") == ""
}

// hint tells whether row is the hint of the keys, which ends a list of options.
func hint(row string) bool {
	for _, part := range strings.Split(row, "·") {
		w := strings.Fields(strings.ToLower(strings.TrimRight(part, " …")))
		if n := len(w); n >= 3 && w[n-2] == "to" && slices.Contains(choiceVerbs, strings.Trim(w[n-1], "()[]")) {
			return true
		}
	}
	return false
}

// indent is how many characters of blank start row.
func indent(row string) int {
	n := 0
	for _, r := range row {
		if !unicode.IsSpace(r) {
			break
		}
		n++
	}
	return n
}

// promptKey names a prompt, to tell one from another: its question, command/reason and options. Empty for none.
func promptKey(p *Prompt) string {
	if p == nil {
		return ""
	}
	return p.Title + "\x00" + strings.Join(p.Lines, "\x00") + "\x00" + strings.Join(p.Options, "\x00")
}

// Choose picks the option index (from 0) of the choice on screen, as the user would with the keys: the arrows from
// the option marked to index, then Enter; a digit when none is marked. The screen must still show the choice of
// title and options, or Choose returns ErrNoPrompt and types nothing.
func (t *Terminal) Choose(title string, lines, options []string, index int) error {
	if t.Exited() {
		return ErrExited
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.mu.Lock()
	p := t.prompt()
	if p == nil || p.Title != title || !slices.Equal(p.Lines, lines) || !slices.Equal(p.Options, options) || index < 0 || index >= len(p.Options) || t.answered == promptKey(p) {
		t.mu.Unlock()
		return ErrNoPrompt
	}
	t.answered = promptKey(p)
	t.mu.Unlock()
	var keys []string
	switch {
	case p.Selected < 0:
		keys = []string{strconv.Itoa(index + 1)}
	case index < p.Selected:
		keys = slices.Repeat([]string{"\x1b[A"}, p.Selected-index)
	default:
		keys = slices.Repeat([]string{"\x1b[B"}, index-p.Selected)
	}
	if p.Selected >= 0 {
		keys = append(keys, "\r")
	}
	for i, key := range keys {
		if i > 0 {
			select {
			case <-t.done:
				return ErrExited
			case <-time.After(keyGap):
			}
		}
		// A command can change while the keys settle: never confirm a different request.
		t.mu.Lock()
		same := promptKey(t.prompt()) == promptKey(p)
		t.mu.Unlock()
		if !same {
			return ErrNoPrompt
		}
		if _, err := t.p.Write([]byte(key)); err != nil {
			return err
		}
	}
	return nil
}
