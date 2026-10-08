//go:build !windows

package terminal

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	rawterm "golang.org/x/term"
)

// fakeLead runs, in a terminal called lead, a program that behaves as an agent's prompt does (TestLeadProcess),
// with Tell's delays shortened.
func fakeLead(t *testing.T) (*Manager, *Terminal) {
	t.Helper()
	oldSettle, oldGap, oldPoll := settle, enterGap, tellPoll
	settle, enterGap, tellPoll = 100*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { settle, enterGap, tellPoll = oldSettle, oldGap, oldPoll })
	t.Setenv(helperEnv, "lead")
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
	if err := term.Write([]byte("1")); err != nil { // Picks an option: the agent carries on, covering the choice.
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

// TestLeadProcess is the fake lead the Tell tests run in a terminal, not a test. In raw mode with bracketed paste,
// as Claude Code and Codex are, it reports each line it gets: whether it came as a paste, and whether Enter came in
// a read of its own. The line dialog shows a choice, as a permission prompt does, until a key answers it.
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
	pasted, inPaste, choice := false, false, false
	b := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil {
			os.Exit(0)
		}
		in := b[:n]
		if choice { // Any key answers it; the agent goes on, and its output covers the choice.
			choice = false
			fmt.Print(strings.Repeat("working…\r\n", choiceTail/8))
			continue
		}
		for i := 0; i < len(in); i++ {
			switch {
			case bytes.HasPrefix(in[i:], []byte("\x1b[200~")):
				inPaste, pasted, i = true, true, i+5
			case bytes.HasPrefix(in[i:], []byte("\x1b[201~")):
				inPaste, i = false, i+5
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
				if string(line) == "dialog" {
					fmt.Print("Do you want to proceed?\r\n\x1b[1m❯\x1b[0m 1. Yes\r\n  2. No\r\n\x1b[2mEsc to cancel\x1b[0m\r\n")
					choice = true
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
