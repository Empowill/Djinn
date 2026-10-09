package terminal

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxPending bounds an escape sequence kept between two reads: a longer one is dropped, not kept growing.
const maxPending = 4 << 10

// faintCell marks a cell drawn faint (SGR 2), above every character.
const faintCell rune = 1 << 30

// promptMarks start the row of an agent's prompt, before a space: Claude Code's ❯, Codex's ›.
var promptMarks = []rune{'❯', '›'}

// screen follows what the program shows now, cell by cell, as a terminal draws it: what Tell reads to know whether a
// choice is on screen. A choice drawn once and covered since, erased in place (Claude Code redraws by moving the
// cursor, not by clearing the screen) or scrolled away, is no longer there. It follows what moves text and the
// cursor, and of the colors only faintness, which tells an agent's placeholder from text typed at its prompt: the
// window's emulator is the one that shows.
type screen struct {
	cols, rows  int
	cells       [][]rune // rows of cols cells; 0 is blank, faintCell marks a faint character
	faint       bool     // what is drawn now is faint
	x, y        int
	wrap        bool // the last column was written: the next character goes to the next line
	top, bottom int  // the scroll region, rows included
	savedX      int
	savedY      int
	other       [][]rune // the main screen while the alternate one shows, nil otherwise
	pending     []byte   // a sequence or a character cut at the end of the last write
}

func newScreen(cols, rows int) *screen {
	s := &screen{}
	s.resize(cols, rows)
	return s
}

// resize sets the size; what fits stays where it was. The program redraws after.
func (s *screen) resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	s.cells = fit(s.cells, cols, rows)
	if s.other != nil {
		s.other = fit(s.other, cols, rows)
	}
	s.cols, s.rows = cols, rows
	s.x, s.y, s.wrap = min(s.x, cols-1), min(s.y, rows-1), false
	s.top, s.bottom = 0, rows-1
}

func fit(cells [][]rune, cols, rows int) [][]rune {
	out := make([][]rune, rows)
	for i := range out {
		out[i] = make([]rune, cols)
		if i < len(cells) {
			copy(out[i], cells[i])
		}
	}
	return out
}

// text is what the screen shows, a line per row.
func (s *screen) text() string {
	var b strings.Builder
	for _, row := range s.cells {
		for _, r := range row {
			if r &^= faintCell; r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// prompt reads an agent's prompt off the screen: the lowest row that starts with its mark (promptMarks) and a space,
// where Claude Code and Codex take what is typed. It tells whether one shows, and whether it holds text: anything but
// blanks and the agent's placeholder, drawn faint ("Ask Codex to do anything").
func (s *screen) prompt() (shown, holds bool) {
	for y := s.rows - 1; y >= 0; y-- {
		row := s.cells[y]
		if len(row) < 2 || !slices.Contains(promptMarks, row[0]&^faintCell) ||
			!slices.Contains([]rune{0, ' ', '\u00a0'}, row[1]&^faintCell) {
			continue
		}
		for _, r := range row[2:] {
			if r&faintCell == 0 && r != 0 && !unicode.IsSpace(r) {
				return true, true
			}
		}
		return true, false
	}
	return false, false
}

// write draws the output b.
func (s *screen) write(b []byte) {
	if len(s.pending) > 0 {
		b = append(s.pending, b...)
		s.pending = nil
	}
	for i := 0; i < len(b); {
		n := s.step(b[i:])
		if n == 0 { // Cut: the rest comes with the next write.
			if len(b)-i <= maxPending {
				s.pending = append([]byte(nil), b[i:]...)
			}
			return
		}
		i += n
	}
}

// step draws what starts b, and returns how many bytes it took; 0 when b holds only the start of it.
func (s *screen) step(b []byte) int {
	c := b[0]
	switch {
	case c == 0x1b:
		return s.escape(b)
	case c == '\r':
		s.x, s.wrap = 0, false
	case c == '\n' || c == '\v' || c == '\f':
		s.index()
	case c == '\b':
		s.x, s.wrap = max(0, s.x-1), false
	case c == '\t':
		s.x, s.wrap = min(s.cols-1, (s.x/8+1)*8), false
	case c < 0x20 || c == 0x7f:
	case c < 0x80:
		s.put(rune(c))
	default:
		if !utf8.FullRune(b) {
			return 0
		}
		r, n := utf8.DecodeRune(b)
		if r != utf8.RuneError || n > 1 {
			s.put(r)
		}
		return n
	}
	return 1
}

// escape runs the escape sequence that starts b, and returns its length; 0 when b ends before it does.
func (s *screen) escape(b []byte) int {
	if len(b) < 2 {
		return 0
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
			i++
		}
		if i == len(b) {
			return 0
		}
		s.csi(string(b[2:i]), b[i])
		return i + 1
	case ']', 'P', 'X', '^', '_': // A string, up to BEL or ST: nothing drawn.
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b {
				if i+1 == len(b) {
					return 0
				}
				if b[i+1] == '\\' {
					return i + 2
				}
			}
		}
		return 0
	case '(', ')', '*', '+', '#', '%': // A character set: nothing drawn.
		if len(b) < 3 {
			return 0
		}
		return 3
	case '7':
		s.savedX, s.savedY = s.x, s.y
	case '8':
		s.restore()
	case 'D':
		s.index()
	case 'E':
		s.x = 0
		s.index()
	case 'M':
		s.wrap = false
		if s.y == s.top {
			s.scroll(-1)
		} else {
			s.y = max(0, s.y-1)
		}
	case 'c':
		s.other, s.faint = nil, false
		s.clear(0, 0, s.rows-1, s.cols-1)
		s.x, s.y, s.wrap, s.top, s.bottom = 0, 0, false, 0, s.rows-1
	}
	return 2
}

// csi runs a control sequence: its parameters, then its final byte.
func (s *screen) csi(params string, final byte) {
	private := params != "" && (params[0] < '0' || params[0] > ';')
	if private {
		params = params[1:]
	}
	var p []int
	for _, f := range strings.Split(params, ";") {
		f, _, _ = strings.Cut(f, ":")
		n, _ := strconv.Atoi(strings.TrimRight(f, " !\"#$%&'()*+,-./"))
		p = append(p, n)
	}
	arg := func(i, def int) int {
		if i < len(p) && p[i] > 0 {
			return p[i]
		}
		return def
	}
	if private {
		if final == 'h' || final == 'l' {
			for _, mode := range p {
				if mode == 1049 || mode == 1047 || mode == 47 {
					s.alternate(final == 'h', mode == 1049)
				}
			}
		}
		return
	}
	if final == 'm' { // Colors keep a line that reached its last column wrapping. Of them, faintness only.
		fields := strings.Split(params, ";")
		for i := 0; i < len(p); i++ {
			if strings.Contains(fields[i], ":") { // 38:5:n and the like: one field each.
				continue
			}
			switch p[i] {
			case 0:
				s.faint = false
			case 2:
				s.faint = true
			case 22:
				s.faint = false
			case 38, 48, 58: // A color: 5 and an index, or 2 and its red, green and blue.
				if i+1 < len(p) && p[i+1] == 5 {
					i += 2
				} else if i+1 < len(p) && p[i+1] == 2 {
					i += 4
				}
			}
		}
		return
	}
	n := arg(0, 1)
	s.wrap = false
	switch final {
	case 'A':
		s.y = max(0, s.y-n)
	case 'B', 'e':
		s.y = min(s.rows-1, s.y+n)
	case 'C', 'a':
		s.x = min(s.cols-1, s.x+n)
	case 'D':
		s.x = max(0, s.x-n)
	case 'E':
		s.x, s.y = 0, min(s.rows-1, s.y+n)
	case 'F':
		s.x, s.y = 0, max(0, s.y-n)
	case 'G', '`':
		s.x = min(s.cols-1, n-1)
	case 'd':
		s.y = min(s.rows-1, n-1)
	case 'H', 'f':
		s.y, s.x = min(s.rows-1, arg(0, 1)-1), min(s.cols-1, arg(1, 1)-1)
	case 'J':
		switch arg(0, 0) {
		case 0:
			s.clear(s.y, s.x, s.rows-1, s.cols-1)
		case 1:
			s.clear(0, 0, s.y, s.x)
		default:
			s.clear(0, 0, s.rows-1, s.cols-1)
		}
	case 'K':
		switch arg(0, 0) {
		case 0:
			s.clear(s.y, s.x, s.y, s.cols-1)
		case 1:
			s.clear(s.y, 0, s.y, s.x)
		default:
			s.clear(s.y, 0, s.y, s.cols-1)
		}
	case 'X':
		s.clear(s.y, s.x, s.y, min(s.cols-1, s.x+n-1))
	case 'P':
		row := s.cells[s.y]
		n = min(n, s.cols-s.x)
		copy(row[s.x:], row[s.x+n:])
		clear(row[s.cols-n:])
	case '@':
		row := s.cells[s.y]
		n = min(n, s.cols-s.x)
		copy(row[s.x+n:], row[s.x:])
		clear(row[s.x : s.x+n])
	case 'L', 'M':
		if s.y >= s.top && s.y <= s.bottom {
			top := s.top
			s.top = s.y
			if final == 'L' {
				s.scroll(-n)
			} else {
				s.scroll(n)
			}
			s.top = top
		}
	case 'S':
		s.scroll(n)
	case 'T':
		s.scroll(-n)
	case 'r':
		top, bottom := arg(0, 1)-1, arg(1, s.rows)-1
		if top < bottom && bottom < s.rows {
			s.top, s.bottom = top, bottom
		} else {
			s.top, s.bottom = 0, s.rows-1
		}
		s.x, s.y = 0, 0
	case 's':
		s.savedX, s.savedY = s.x, s.y
	case 'u':
		s.restore()
	}
}

// restore puts the cursor back where it was saved, within the screen: it may have shrunk since.
func (s *screen) restore() {
	s.x, s.y, s.wrap = min(s.savedX, s.cols-1), min(s.savedY, s.rows-1), false
}

// put writes r at the cursor, and moves it on.
func (s *screen) put(r rune) {
	w := width(r)
	if w == 0 {
		return
	}
	if s.wrap || s.x+w > s.cols {
		s.x = 0
		s.index()
	}
	if s.faint {
		r |= faintCell
	}
	s.cells[s.y][s.x] = r
	if w == 2 && s.x+1 < s.cols {
		s.cells[s.y][s.x+1] = 0
	}
	if s.x+w >= s.cols {
		s.x, s.wrap = s.cols-1, true
	} else {
		s.x += w
	}
}

// index moves the cursor down a line, scrolling at the bottom of the region.
func (s *screen) index() {
	s.wrap = false
	if s.y == s.bottom {
		s.scroll(1)
	} else if s.y < s.rows-1 {
		s.y++
	}
}

// scroll moves the rows of the region up n rows, down when n is negative; the rows uncovered are blank.
func (s *screen) scroll(n int) {
	region := s.cells[s.top : s.bottom+1]
	n = max(-len(region), min(len(region), n))
	if n > 0 {
		gone := append([][]rune(nil), region[:n]...)
		copy(region, region[n:])
		for i, row := range gone {
			clear(row)
			region[len(region)-n+i] = row
		}
	} else if n < 0 {
		n = -n
		gone := append([][]rune(nil), region[len(region)-n:]...)
		copy(region[n:], region[:len(region)-n])
		for i, row := range gone {
			clear(row)
			region[i] = row
		}
	}
}

// clear blanks from row y0, column x0 to row y1, column x1, both included, in reading order.
func (s *screen) clear(y0, x0, y1, x1 int) {
	for y := y0; y <= y1; y++ {
		from, to := 0, s.cols-1
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = x1
		}
		if from <= to {
			clear(s.cells[y][from : to+1])
		}
	}
}

// alternate shows the alternate screen, blank, or the main one again; with cursor, the cursor is saved and restored.
func (s *screen) alternate(on, cursor bool) {
	switch {
	case on && s.other == nil:
		if cursor {
			s.savedX, s.savedY = s.x, s.y
		}
		s.other, s.cells = s.cells, fit(nil, s.cols, s.rows)
	case !on && s.other != nil:
		s.cells, s.other = s.other, nil
		if cursor {
			s.restore()
		}
	}
	s.wrap = false
}

// width is how many cells r takes: 0 for a mark that combines with the character before, 2 for the wide ones of East
// Asian scripts and emoji, 1 otherwise. Close enough to read words off the screen.
func width(r rune) int {
	switch {
	case r >= 0x300 && r <= 0x36f, r >= 0x200b && r <= 0x200f, r >= 0xfe00 && r <= 0xfe0f:
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}
