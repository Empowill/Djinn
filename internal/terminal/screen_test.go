package terminal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rows is what s shows, its blank rows and the spaces that end rows left out.
func rows(s *screen) []string {
	var out []string
	for _, row := range strings.Split(s.text(), "\n") {
		if row = strings.TrimRight(row, " "); row != "" {
			out = append(out, row)
		}
	}
	return out
}

// TestScreenReadsClaudeCodeTrust: Claude Code's folder trust as recorded (claude 2.1.295, 120 columns) draws
// word by word with cursor moves; cut anywhere between two writes, it reads as on screen, a choice.
func TestScreenReadsClaudeCodeTrust(t *testing.T) {
	recorded, err := os.ReadFile(filepath.Join("testdata", "claude-trust.out"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{1, 2, 3, 7, 64, len(recorded)} {
		term := &Terminal{screen: newScreen(120, 40)}
		for i := 0; i < len(recorded); i += cut {
			term.screen.write(recorded[i:min(len(recorded), i+cut)])
		}
		got := rows(term.screen)
		for _, want := range []string{" ❯ No, exit", "   Yes, I trust this folder", " Enter to confirm · Esc to cancel"} {
			if !slices.Contains(got, want) {
				t.Fatalf("cut every %d bytes: %q not a row of:\n%s", cut, want, strings.Join(got, "\n"))
			}
		}
		if !term.choosing() {
			t.Fatalf("cut every %d bytes: the trust is not a choice", cut)
		}
	}
}

// TestScreenFollowsRedraws: what an agent erases in place, scrolls away or draws on the alternate screen and leaves
// is gone; what it shows now stays.
func TestScreenFollowsRedraws(t *testing.T) {
	for name, c := range map[string]struct {
		cols int
		out  string
		want []string
	}{
		"erased in place": {30,
			"a\r\nDo you want to proceed?\r\n❯ 1. Yes\r\nEsc to cancel\r\n" + strings.Repeat("\x1b[1A\x1b[2K", 3) + "b\r\n",
			[]string{"a", "b"}},
		"scrolled away":     {20, "Esc to cancel\r\n" + strings.Repeat("x\r\n", 5) + "y", []string{"x", "x", "x", "x", "y"}},
		"alternate screen":  {20, "main\r\n\x1b[?1049hEsc to cancel\x1b[?1049l", []string{"main"}},
		"overwritten":       {20, "Esc to cancel\r\x1b[KEnter to go\x1b[3G\x1b[1P", []string{"Ener to go"}},
		"colors at the end": {10, "0123456789\x1b[31m\x1b[0mabc", []string{"0123456789", "abc"}},
		"wide":              {10, "界界界界界x", []string{"界 界 界 界 界", "x"}},
		"region":            {10, "\x1b[2;4rtop\x1b[4;1Hq\r\nr", []string{"top", "q", "r"}},
	} {
		s := newScreen(c.cols, 5)
		s.write([]byte(c.out))
		if got := rows(s); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: rows %q, want %q", name, got, c.want)
		}
	}
	s := newScreen(20, 3)
	s.write([]byte("\x1b[3;15H\x1b7\x1b[HEsc to cancel"))
	s.resize(5, 2)
	if got := rows(s); strings.Join(got, "|") != "Esc t" {
		t.Errorf("resized: rows %q, want what fits", got)
	}
	s.write([]byte("\x1b8x\x1b[?1049h\x1b[?1049ly"))             // The cursor saved before stays within the screen.
	if got := rows(s); strings.Join(got, "|") != "Esc t|    y" { // y over x: the cursor came back from the alternate screen.
		t.Errorf("restored: rows %q", got)
	}
}

// TestChoosingReadsHints: a choice shows a key, "to" and what it does, at the end of a row or of a part of it between
// "·"; a sentence that says so does not, nor an agent's prompt at rest.
func TestChoosingReadsHints(t *testing.T) {
	for out, want := range map[string]bool{
		"  Esc to cancel":                                  true,
		"Enter to confirm · Esc to exit":                   true,
		"Press enter to confirm or esc to cancel":          true,
		"Press Enter to continue…":                         true,
		"Esc to cancel · Tab to amend · ctrl+e to explain": true,
		"1. Yes  2. No  (Esc to cancel)":                   true,
		"Press Esc to cancel.":                             false,
		"press Esc to cancel it":                           false,
		"Press Ctrl-C again to exit":                       false,
		"  ⏵⏵ auto mode on (shift+tab to cycle)":           false,
		"  ← for agents · ? for shortcuts":                 false,
		"✻ Thinking… (12s · esc to interrupt)":             false,
	} {
		term := &Terminal{screen: newScreen(80, 5)}
		term.screen.write([]byte("❯ \r\n" + out + "\r\n"))
		if got := term.choosing(); got != want {
			t.Errorf("%q: choosing %v, want %v", out, got, want)
		}
	}
}

// FuzzScreen: whatever a program writes, at whatever size, the screen never fails: djinn up reads every terminal
// through it.
func FuzzScreen(f *testing.F) {
	f.Add([]byte("\x1b7\x1b[99;99H\x1b8x\x1b[2;3r\x1b[L\x1b[M\x1b[5P\x1b[5@\x1b[9X界\r\n"), uint8(3), uint8(2))
	f.Add([]byte("\x1b[?1049h\x1b[S\x1b[T\x1bM\x1bD\x1bE\x1bc\x1b]0;t\a\x1b[?1049l"), uint8(1), uint8(1))
	f.Fuzz(func(t *testing.T, out []byte, cols, rows uint8) {
		s := newScreen(20, 5)
		half := len(out) / 2
		s.write(out[:half])
		s.resize(int(cols), int(rows))
		s.write(out[half:])
		_ = s.text()
	})
}
