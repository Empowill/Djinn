//go:build !windows

package terminal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rawterm "golang.org/x/term"
)

// fakeLead runs, in a terminal called lead, a program that behaves as an agent's prompt does (TestLeadProcess),
// with Tell's delays shortened.
func fakeLead(t *testing.T) (*Manager, *Terminal) {
	t.Helper()
	oldSettle, oldGap, oldPoll, oldIdle := settle, enterGap, tellPoll, typingIdle
	settle, enterGap, tellPoll, typingIdle = 100*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond, 3*time.Second
	t.Cleanup(func() { settle, enterGap, tellPoll, typingIdle = oldSettle, oldGap, oldPoll, oldIdle })
	t.Setenv(helperEnv, "lead")
	trust, err := filepath.Abs(filepath.Join("testdata", "claude-trust.out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(trustEnv, trust)
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	term, _, err := m.Open("lead", []string{os.Args[0], "-test.run=^TestLeadProcess$"}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	output(t, term, 0, contains("ready"))
	return m, term
}

// tell runs Tell on term in the background; the channel gets its error.
func tell(term *Terminal, line string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- term.Tell(line) }()
	return done
}

// none checks that the output of term from offset from shows nothing more for a while.
func none(t *testing.T, term *Terminal, from uint64, what string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	var out bytes.Buffer
	stop := make(chan struct{})
	close(stop) // What is there, without waiting for more.
	_ = term.Read(from, stop, func(o Output) error {
		out.Write(o.Data)
		return nil
	})
	if strings.Contains(out.String(), "line") {
		t.Fatalf("%s, yet the program got:\n%q", what, out.String())
	}
}

// size is the length of the output of term so far.
func size(term *Terminal) uint64 {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.base + uint64(len(term.buf))
}

// TestTellTypesALineThenEnter: at an idle prompt, the line arrives as one paste, the program having asked for
// bracketed paste, then Enter on its own: the agent submits it, it does not take Enter for a line break.
func TestTellTypesALineThenEnter(t *testing.T) {
	m, term := fakeLead(t)
	if m.Lookup("lead") != term || m.Lookup("other") != nil {
		t.Fatal("Lookup finds the running lead only")
	}
	from := size(term)
	if err := <-tell(term, "Q02 answered: A.\r\nContinue.\x1b[2J"); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`line "Q02 answered: A.Continue.[2J" pasted, enter apart`))
}

// TestTellWaitsForWhatTheUserTypes: a line the user is typing is never mixed with the one told, which waits until
// the user sends theirs, or clears it.
func TestTellWaitsForWhatTheUserTypes(t *testing.T) {
	_, term := fakeLead(t)
	from := size(term)
	if err := term.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	done := tell(term, "Q01 answered: B. Continue.")
	none(t, term, from, "the user is typing")
	// Backspace, arrows and a paste are still typing: the line is not empty.
	if err := term.Write([]byte("\x7f\x1b[D\x1b[200~p\r\x1b[201~")); err != nil {
		t.Fatal(err)
	}
	none(t, term, from, "the user is still typing")
	if err := term.Write([]byte("lo\r")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	out := output(t, term, from, contains(`line "Q01 answered: B. Continue."`))
	if i, j := strings.Index(out, `line "he`), strings.Index(out, `line "Q01`); i < 0 || i > j {
		t.Fatalf("the user's line must go first, alone:\n%q", out)
	}

	// Typed, then erased: nothing is under way any more.
	from = size(term)
	if err := term.Write([]byte("ab\x7f\x7f")); err != nil {
		t.Fatal(err)
	}
	if err := <-tell(term, "Q03 answered: yes. Continue."); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`line "Q03 answered: yes. Continue." pasted`))
}

// TestTellWaitsForAChoice: while the agent shows a choice that keys answer (a permission prompt), the line waits:
// it would pick an option. The keys that answer the choice are not a line under way.
func TestTellWaitsForAChoice(t *testing.T) {
	_, term := fakeLead(t)
	from := size(term)
	if err := term.Write([]byte("dialog\r")); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains("Esc to cancel"))
	from = size(term)
	done := tell(term, "Q04 answered: A. Continue.")
	none(t, term, from, "a choice is on screen")
	if err := term.Write([]byte("1")); err != nil { // Picks an option: the agent carries on, erasing the choice.
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`line "Q04 answered: A. Continue."`))
}

// TestTellGivesUpWhenTheProgramEnds: a line still waiting goes nowhere once the lead has ended.
func TestTellGivesUpWhenTheProgramEnds(t *testing.T) {
	m, term := fakeLead(t)
	if err := term.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	done := tell(term, "Q05 answered: A. Continue.")
	term.Hangup()
	select {
	case err := <-done:
		if err != ErrExited {
			t.Fatalf("Tell = %v, want ErrExited", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tell still waits for an ended program")
	}
	if m.Lookup("lead") != nil {
		t.Fatal("an ended lead is not found")
	}
}

// trustEnv gives the fake lead the file of Claude Code's folder trust, as recorded.
const trustEnv = "DJINN_TERMINAL_TRUST"

// screens are what the fake lead shows for a line, and whether it is a choice that the next key answers.
var screens = map[string]struct {
	out    string
	choice bool
}{
	// A permission prompt, as Claude Code draws it.
	"dialog": {"Do you want to proceed?\r\n\x1b[1m❯\x1b[0m 1. Yes\r\n  2. No\r\n\x1b[2mEsc to cancel\x1b[0m\r\n", true},
	// Claude Code's folder trust, in its earlier words, and the choices of its first run.
	"old-trust": {"Do you trust the files in this folder?\r\n❯ 1. Yes, proceed\r\n  2. No, exit\r\n\r\n" +
		"Enter to confirm · Esc to exit\r\n", true},
	"onboarding": {"Choose the text style that looks best with your terminal\r\n❯ 1. Dark mode\r\n  2. Light mode\r\n\r\n" +
		"Enter to confirm\r\n", true},
	// A choice's words in what the agent wrote, and a choice scrolled away: neither is a choice on screen.
	"prose":    {"To stop it, press Esc to cancel.\r\nThe dialog said Esc to cancel it.\r\n", false},
	"scrolled": {"Esc to cancel\r\n" + strings.Repeat("working…\r\n", 30), false},
}

// TestLeadProcess is the fake lead the Tell tests run in a terminal, not a test. In raw mode with bracketed paste,
// as Claude Code and Codex are, it reports each line it gets: whether it came as a paste, and whether Enter came in
// a read of its own. A line of screens shows its screen; a choice stays until a key answers it, then is erased in
// place, as Claude Code redraws. The line trust shows Claude Code's folder trust as recorded (claude 2.1.295). It
// erases as an agent's prompt does: Ctrl+W and Alt+Backspace a word, Ctrl+U and Esc twice the line.
func TestLeadProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "lead" {
		t.Skip("run by the Tell tests")
	}
	state, err := rawterm.MakeRaw(0)
	if err != nil {
		fmt.Println("raw:", err)
		os.Exit(1)
	}
	defer rawterm.Restore(0, state) //nolint:errcheck
	fmt.Print(pasteOn + "ready\r\n")
	var line []byte
	pasted, inPaste, escaped := false, false, false
	erase := "" // what erases the choice on screen, if any
	word := func() {
		line = bytes.TrimRight(line, " ")
		line = line[:bytes.LastIndexByte(line, ' ')+1]
	}
	b := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil {
			os.Exit(0)
		}
		in := b[:n]
		if erase != "" { // Any key answers it; the agent goes on, and erases the choice.
			fmt.Printf("%schose %q\r\n", erase, in)
			erase = ""
			continue
		}
		for i := 0; i < len(in); i++ {
			esc := escaped
			escaped = false
			switch {
			case bytes.HasPrefix(in[i:], []byte("\x1b[200~")):
				inPaste, pasted, i = true, true, i+5
			case bytes.HasPrefix(in[i:], []byte("\x1b[201~")):
				inPaste, i = false, i+5
			case in[i] == 0x1b && i+1 < len(in) && in[i+1] == 0x7f:
				word()
				i++
			case in[i] == 0x1b && i+1 < len(in) && in[i+1] == '[':
				for i += 2; i < len(in) && (in[i] < 0x40 || in[i] > 0x7e); i++ {
				}
			case in[i] == 0x1b && i+1 < len(in) && in[i+1] == 0x1b:
				line = line[:0]
				i++
			case in[i] == 0x1b && i+1 == len(in):
				if esc {
					line = line[:0]
				}
				escaped = true
			case in[i] == 0x17:
				word()
			case in[i] == 0x15:
				line = line[:0]
			case in[i] == '\r' && !inPaste:
				apart := "enter apart"
				if i > 0 {
					apart = "enter with the line"
				}
				how := "typed"
				if pasted {
					how = "pasted"
				}
				fmt.Printf("line %q %s, %s\r\n", strings.ReplaceAll(string(line), "\x1b", ""), how, apart)
				if string(line) == "trust" {
					recorded, err := os.ReadFile(os.Getenv(trustEnv))
					if err != nil {
						fmt.Println(err)
						os.Exit(1)
					}
					os.Stdout.Write(recorded) //nolint:errcheck
					erase = "\x1b[2J\x1b[H"
				} else if screen, ok := screens[string(line)]; ok {
					fmt.Print(screen.out)
					if screen.choice {
						erase = strings.Repeat("\x1b[1A\x1b[2K", strings.Count(screen.out, "\n"))
					}
				}
				line, pasted = line[:0], false
			case in[i] == 0x7f:
				line = line[:max(0, len(line)-1)]
			default:
				line = append(line, in[i])
			}
		}
	}
}

// TestPostKeepsLineBreaksAndOrder: what the user writes to the lead keeps its line breaks inside one paste, and the
// texts posted while the user types wait, then arrive in the order posted.
func TestPostKeepsLineBreaksAndOrder(t *testing.T) {
	_, term := fakeLead(t)
	from := size(term)
	if err := term.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	failed := make(chan error, 2)
	if !term.Post("first\nsecond\r\nthird", func(err error) { failed <- err }) {
		t.Error("Post says nothing waits, yet the user is typing")
	}
	term.Post("then\tthis", func(err error) { failed <- err })
	none(t, term, from, "the user is typing")
	if err := term.Write([]byte("lo\r")); err != nil {
		t.Fatal(err)
	}
	out := output(t, term, from, contains(`line "then this" pasted`))
	first := strings.Index(out, `line "first\rsecond\rthird" pasted, enter apart`)
	if i, j := strings.Index(out, `line "hello"`), strings.Index(out, `line "then this"`); i < 0 || first < i || j < first {
		t.Fatalf("the user's line, then the texts in the order posted:\n%q", out)
	}
	select {
	case err := <-failed:
		t.Fatal(err)
	default:
	}
}

// TestTellWaitsOnTrustAndOnboarding: Claude Code's folder trust, in its words of now and of before, and the choices of
// its first run are choices too. A line typed there would pick an option ("2" picks by number) and its Enter confirm
// it: trusting a folder is the developer's decision. Answered, the choice is erased, and the line goes.
func TestTellWaitsOnTrustAndOnboarding(t *testing.T) {
	for _, screen := range []string{"trust", "old-trust", "onboarding"} {
		t.Run(screen, func(t *testing.T) {
			_, term := fakeLead(t)
			from := size(term)
			if err := term.Write([]byte(screen + "\r")); err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains("confirm"))
			from = size(term)
			done := tell(term, "W2 ended (done). Continue.")
			none(t, term, from, "a choice is on screen")
			if err := term.Write([]byte("\x1b")); err != nil { // The developer answers.
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			out := output(t, term, from, contains(`line "W2 ended (done). Continue." pasted`))
			if !strings.Contains(out, `chose "\x1b"`) {
				t.Fatalf("the choice got another key than the developer's:\n%q", out)
			}
		})
	}
}

// TestTellReadsTheScreenNow: words of a choice in what the agent wrote, or a choice scrolled away, hold nothing: only
// a choice on screen now does.
func TestTellReadsTheScreenNow(t *testing.T) {
	for _, screen := range []string{"prose", "scrolled"} {
		t.Run(screen, func(t *testing.T) {
			_, term := fakeLead(t)
			from := size(term)
			if err := term.Write([]byte(screen + "\r")); err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains("cancel"))
			time.Sleep(2 * settle) // Past the settle of the keys above.
			from = size(term)
			select {
			case err := <-tell(term, "Q07 answered: A. Continue."):
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Tell waits on a choice that is not on screen")
			}
			output(t, term, from, contains(`line "Q07 answered: A. Continue." pasted`))
		})
	}
}

// TestTellFollowsErasures: a word erased by Ctrl+W or Alt+Backspace, a line by Esc twice, leave nothing under way; a
// line recalled from the history is under way.
func TestTellFollowsErasures(t *testing.T) {
	for name, keys := range map[string][]string{
		"ctrl+w":          {"hel", "\x17"},
		"alt+backspace":   {"hel", "\x1b\x7f"},
		"esc twice":       {"hello you", "\x1b", "\x1b"},
		"esc esc at once": {"hello you", "\x1b\x1b"},
	} {
		t.Run(name, func(t *testing.T) {
			_, term := fakeLead(t)
			from := size(term)
			for _, k := range keys {
				if err := term.Write([]byte(k)); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond) // One read each, as keys come.
			}
			select {
			case err := <-tell(term, "Q08 answered: B. Continue."):
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second): // Well within typingIdle.
				t.Fatal("Tell waits, yet the prompt was cleared")
			}
			output(t, term, from, contains(`line "Q08 answered: B. Continue." pasted`))
		})
	}
	t.Run("half a line erased", func(t *testing.T) {
		_, term := fakeLead(t)
		from := size(term)
		if err := term.Write([]byte("hello you\x17")); err != nil {
			t.Fatal(err)
		}
		done := tell(term, "Q09 answered: A. Continue.")
		none(t, term, from, "hello is still at the prompt")
		if err := term.Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		output(t, term, from, contains(`line "Q09 answered: A. Continue." pasted`))
	})
	t.Run("history", func(t *testing.T) {
		_, term := fakeLead(t)
		from := size(term)
		if err := term.Write([]byte("\x1b[A")); err != nil {
			t.Fatal(err)
		}
		done := tell(term, "Q10 answered: A. Continue.")
		none(t, term, from, "a line of the history is at the prompt")
		if err := term.Write([]byte("\x15")); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		output(t, term, from, contains(`line "Q10 answered: A. Continue." pasted`))
	})
}

// TestTellOutwaitsALineLeftUnderWay: Tell follows the keys, not the prompt, and may lose track of an edit; what is
// under way holds it typingIdle after the last key, no longer.
func TestTellOutwaitsALineLeftUnderWay(t *testing.T) {
	_, term := fakeLead(t)
	typingIdle = time.Second
	from := size(term)
	if err := term.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	done := tell(term, "Q11 answered: A. Continue.")
	none(t, term, from, "the user typed a moment ago")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tell still waits on keys typed long ago")
	}
	if waited := time.Since(start); waited < typingIdle-100*time.Millisecond {
		t.Fatalf("Tell waited %v, want typingIdle", waited)
	}
	output(t, term, from, contains(`Q11 answered: A. Continue." pasted`))
}
