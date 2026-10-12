//go:build !windows

// Not on Windows: these tests drive /bin/sh on a pseudo-terminal (see terminal_test.go).

package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	rawterm "golang.org/x/term"
)

// fastVoice shortens the delays of Say for a test.
func fastVoice(t *testing.T, q, s, w time.Duration) {
	t.Helper()
	old := [...]time.Duration{quiet, settle, warmup, enter, afterEnter}
	quiet, settle, warmup, enter, afterEnter = q, s, w, 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { quiet, settle, warmup, enter, afterEnter = old[0], old[1], old[2], old[3], old[4] })
}

// echoes is a program that says each line it reads, as the lead takes each line typed.
const echoes = `printf 'ready\n'; while read -r l; do printf 'got[%s]\n' "$l"; done`

func TestSayTypesOneLineAndEnter(t *testing.T) {
	fastVoice(t, 200*time.Millisecond, 50*time.Millisecond, 2*time.Second)
	m := NewManager(Config{})
	term := sh(t, m, "lead-w1", "-c", echoes)
	output(t, term, 0, contains("ready"))
	// Line breaks and control characters do not type several lines, nor keys.
	if err := m.Say("lead-w1", "Djinn: Q01 answered A.\nNote: \"x\"\x1b[2J"); err != nil {
		t.Fatal(err)
	}
	if err := m.Say("lead-w1", "second"); err != nil {
		t.Fatal(err)
	}
	out := output(t, term, 0, contains("got[second]"))
	first := strings.Index(out, `got[Djinn: Q01 answered A. Note: "x" [2J]`)
	if first < 0 || first > strings.Index(out, "got[second]") {
		t.Fatalf("want one line, then the second, in order:\n%q", out)
	}
	if strings.Count(out, "got[") != 2 {
		t.Fatalf("want two lines read:\n%q", out)
	}
}

func TestSayWaitsWhileThePersonTypes(t *testing.T) {
	fastVoice(t, 250*time.Millisecond, 50*time.Millisecond, 2*time.Second)
	m := NewManager(Config{})
	term := sh(t, m, "lead-w1", "-c", echoes)
	output(t, term, 0, contains("ready"))
	// The person types a line of their own, a key at a time, well under quiet apart: Djinn's line waits until they are
	// quiet, then follows.
	if err := term.Write([]byte("m")); err != nil {
		t.Fatal(err)
	}
	if err := m.Say("lead-w1", "from djinn"); err != nil {
		t.Fatal(err)
	}
	var last time.Time
	for _, k := range "ine\r" {
		time.Sleep(75 * time.Millisecond)
		if err := term.Write([]byte(string(k))); err != nil {
			t.Fatal(err)
		}
		last = time.Now()
	}
	out := output(t, term, 0, contains("got[from djinn]"))
	if took := time.Since(last); took < quiet {
		t.Errorf("the line came %v after the last key, want at least %v", took, quiet)
	}
	if !strings.Contains(out, "got[mine]") || strings.Index(out, "got[mine]") > strings.Index(out, "got[from djinn]") {
		t.Errorf("want the person's line whole, then Djinn's:\n%q", out)
	}
}

func TestSayWaitsForAProgramJustStarted(t *testing.T) {
	fastVoice(t, 0, 150*time.Millisecond, 600*time.Millisecond)
	m := NewManager(Config{})
	// Both start at once. Silent: the line waits for warmup. Drawn: the line goes once the output paused for settle,
	// well before.
	silent := sh(t, m, "silent", "-c", `while read -r l; do printf 'got[%s]\n' "$l"; done`)
	silentStart := time.Now()
	if err := silent.Say("hello"); err != nil {
		t.Fatal(err)
	}
	drawn := sh(t, m, "drawn", "-c", echoes)
	drawnStart := time.Now()
	if err := drawn.Say("hello"); err != nil {
		t.Fatal(err)
	}
	output(t, drawn, 0, contains("got[hello]"))
	if took := time.Since(drawnStart); took < settle || took >= warmup {
		t.Errorf("a program that drew got the line after %v, want between %v and %v", took, settle, warmup)
	}
	// Read after the drawn one, which came before warmup: a line to the silent one before warmup still shows.
	output(t, silent, 0, contains("got[hello]"))
	if took := time.Since(silentStart); took < warmup {
		t.Errorf("a silent program got the line after %v, want at least %v", took, warmup)
	}
}

func TestSayRefuses(t *testing.T) {
	fastVoice(t, time.Hour, 0, 0)
	m := NewManager(Config{})
	if err := m.Say("lead-w1", "hello"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no terminal: %v, want ErrNotFound", err)
	}
	term := sh(t, m, "lead-w1", "-c", echoes)
	if err := term.Write([]byte("x")); err != nil { // The person types: every line waits.
		t.Fatal(err)
	}
	var err error
	for i := 0; i <= SayQueue+1 && err == nil; i++ {
		err = term.Say("hello")
	}
	if !errors.Is(err, ErrFull) {
		t.Errorf("too many lines: %v, want ErrFull", err)
	}
	term.Hangup()
	end(t, term)
	if err := term.Say("hello"); !errors.Is(err, ErrExited) {
		t.Errorf("program ended: %v, want ErrExited", err)
	}
	if err := m.Say("lead-w1", "hello"); !errors.Is(err, ErrNotFound) {
		t.Errorf("program ended: %v, want ErrNotFound", err)
	}
}

func TestSaySeveralLinesSubmittedMidOutput(t *testing.T) {
	fastVoice(t, 100*time.Millisecond, 30*time.Millisecond, 2*time.Second)
	m := NewManager(Config{})
	// A program that outputs text mid-turn while reading lines.
	prog := `printf 'ready\n'; (for i in 1 2 3 4 5; do printf 'output %s\n' "$i"; sleep 0.02; done) & while read -r l; do printf 'got[%s]\n' "$l"; [ "$l" = "fourth line" ] && exit 0; done`
	term := sh(t, m, "lead-w1", "-c", prog)
	output(t, term, 0, contains("ready"))

	// Rapidly send several lines while output is happening.
	lines := []string{"first line", "second line", "third line", "fourth line"}
	for _, l := range lines {
		if err := m.Say("lead-w1", l); err != nil {
			t.Fatal(err)
		}
	}

	out := output(t, term, 0, contains("got[fourth line]"))
	lastIdx := -1
	for _, l := range lines {
		idx := strings.Index(out, "got["+l+"]")
		if idx < 0 {
			t.Fatalf("missing submission for %q in output:\n%s", l, out)
		}
		if idx <= lastIdx {
			t.Fatalf("line %q (idx %d) appeared out of order (previous %d) in output:\n%s", l, idx, lastIdx, out)
		}
		lastIdx = idx
	}
	if strings.Count(out, "got[") != len(lines) {
		t.Fatalf("want exactly %d lines read, got:\n%s", len(lines), out)
	}
	term.Hangup()
	end(t, term)
}

// fakeLead runs, in a terminal called lead, a program that behaves as an agent's prompt does (TestLeadProcess),
// with the delays of Say shortened.
func fakeLead(t *testing.T) (*Manager, *Terminal) {
	t.Helper()
	fastVoice(t, 100*time.Millisecond, 50*time.Millisecond, 2*time.Second)
	oldHold, oldIdle := hold, typingIdle
	hold, typingIdle = 20*time.Millisecond, 3*time.Second
	t.Cleanup(func() { hold, typingIdle = oldHold, oldIdle })
	t.Setenv(helperEnv, "lead")
	recorded, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(recordedEnv, recorded)
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	term, _, err := m.Open("lead", []string{os.Args[0], "-test.run=^TestLeadProcess$"}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	output(t, term, 0, contains("ready"))
	return m, term
}

// say runs Say on term, which returns at once: the line is queued.
func say(t *testing.T, term *Terminal, line string) {
	t.Helper()
	if err := term.Say(line); err != nil {
		t.Fatal(err)
	}
}

// read is the output of term from offset from, without waiting for more.
func read(term *Terminal, from uint64) string {
	var out bytes.Buffer
	stop := make(chan struct{})
	close(stop)
	_ = term.Read(from, stop, func(o Output) error {
		out.Write(o.Data)
		return nil
	})
	return out.String()
}

// none checks that the output of term from offset from shows no line read for a while.
func none(t *testing.T, term *Terminal, from uint64, what string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	if out := read(term, from); strings.Contains(out, "line") {
		t.Fatalf("%s, yet the program got:\n%q", what, out)
	}
}

// TestSayPastesALineThenEnter: at an idle prompt that asked for bracketed paste, the line arrives as one paste, then
// Enter on its own: the agent submits it, it does not take Enter for a line break.
func TestSayPastesALineThenEnter(t *testing.T) {
	_, term := fakeLead(t)
	from := term.End()
	say(t, term, "Q02 answered: A. Continue.")
	output(t, term, from, contains(`line "Q02 answered: A. Continue." pasted, enter apart`))
}

// TestSayWaitsForAChoice: while the agent shows a choice that keys answer (a permission prompt), the line waits: it
// would pick an option. The keys that answer the choice are not a line under way.
func TestSayWaitsForAChoice(t *testing.T) {
	_, term := fakeLead(t)
	from := term.End()
	if err := term.Write([]byte("dialog\r")); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains("Esc to cancel"))
	from = term.End()
	say(t, term, "Q04 answered: A. Continue.")
	none(t, term, from, "a choice is on screen")
	if err := term.Write([]byte("1")); err != nil { // Picks an option: the agent carries on, erasing the choice.
		t.Fatal(err)
	}
	output(t, term, from, contains(`line "Q04 answered: A. Continue."`))
}

// TestSayTypesNothingIntoAnApproval: the line Djinn types when events wait and the lead watches none (always for a
// Codex lead, whose watch tells it nothing) waits while an approval of Claude Code or Codex is on screen. Typed
// there, its Enter would answer the approval for the developer, usually "Yes". The approval gets the developer's keys
// only, then the line goes.
func TestSayTypesNothingIntoAnApproval(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			_, term := fakeLead(t)
			from := term.End()
			if err := term.Write([]byte(agent + "-approval\r")); err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains("to cancel"))
			from = term.End()
			say(t, term, "Djinn: 2 events waiting, run djinn lead watch")
			time.Sleep(quiet + settle + 20*hold)
			if out := read(term, from); strings.Contains(out, "chose") || strings.Contains(out, "line") {
				t.Fatalf("typed into the approval:\n%q", out)
			}
			if err := term.Write([]byte("\x1b[B\r")); err != nil { // The developer picks the second option.
				t.Fatal(err)
			}
			out := output(t, term, from, contains(`line "Djinn: 2 events waiting, run djinn lead watch" pasted, enter apart`))
			if !strings.Contains(out, `chose "\x1b[B\r"`) {
				t.Fatalf("the approval got another answer than the developer's:\n%q", out)
			}
		})
	}
}

// recordedEnv gives the fake lead the folder of the agents' screens as recorded: testdata.
const recordedEnv = "DJINN_TERMINAL_RECORDED"

// The approvals below are written by hand from the words in the binaries (claude 2.1.295, codex-cli 0.162.0-alpha.2)
// and the layout of their screens; Claude Code's folder trust is recorded (testdata/claude-trust.out).
const (
	claudeApproval = "\x1b[38;5;220m" + "────────────────────────────────────────" + "\x1b[39m\r\n" +
		" Bash command\r\n\r\n" +
		"   djinn task list --wish-id 01a11f56\r\n" +
		"   List the tasks of the wish\r\n\r\n" +
		" Do you want to proceed?\r\n" +
		" \x1b[38;5;153m❯\x1b[39m 1. Yes\r\n" +
		"   2. Yes, and don't ask again for djinn task list commands in\r\n" +
		"      /Users/me/CODE/dolmen\r\n" +
		"   3. No, and tell Claude what to do differently (esc)\r\n\r\n" +
		" \x1b[2mEsc to cancel · Tab to amend\x1b[22m\r\n"
	codexApproval = "› Run the tests\r\n\r\n" +
		"• I use the djinn skill. I check:\r\n" +
		"  1. the tasks\r\n  2. the questions\r\n\r\n" +
		"  Would you like to run the following command?\r\n\r\n" +
		"  Reason: Allow Djinn to read the wish?\r\n\r\n" +
		"  $ djinn wish brief 01a11f56\r\n\r\n" +
		"\x1b[1m› 1. Yes, proceed (y)\x1b[0m\r\n" +
		"  2. Yes, and don't ask again for commands that start with `djinn` (p)\r\n" +
		"  3. No, and tell Codex what to do differently (esc)\r\n\r\n" +
		"  Press enter to confirm or esc to cancel\r\n"
)

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
	// Approvals: arrows move, only Enter confirms.
	"claude-approval": {claudeApproval, true},
	"codex-approval":  {codexApproval, true},
	// A choice's words in what the agent wrote, and a choice scrolled away: neither is a choice on screen.
	"prose":    {"To stop it, press Esc to cancel.\r\nThe dialog said Esc to cancel it.\r\n", false},
	"scrolled": {"Esc to cancel\r\n" + strings.Repeat("working…\r\n", 30), false},
}

// TestLeadProcess is the fake lead the Say tests run in a terminal, not a test. In raw mode with bracketed paste, as
// Claude Code and Codex are, it reports each line it gets: whether it came as a paste, and whether Enter came in a
// read of its own. A line of screens shows its screen; a choice stays until a key answers it (an approval, until
// Enter), then is erased in place, as Claude Code redraws. The line trust shows Claude Code's folder trust as
// recorded (claude 2.1.295), the line "replay <file>" a screen of testdata as recorded; after "replay
// <agent>-idle.out", the keys "Est" and Ctrl+U draw what that agent drew for them, as recorded (<agent>-est.out,
// <agent>-cleared.out). It erases as an agent's prompt does: Ctrl+W and Alt+Backspace a word, Ctrl+U and Esc twice
// the line. What the terminal answers to the agent's queries is no key, as the agents read it: a string, OSC and the
// like, up to BEL or ST.
func TestLeadProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "lead" {
		t.Skip("run by the Say tests")
	}
	state, err := rawterm.MakeRaw(0)
	if err != nil {
		t.Fatal("raw:", err)
	}
	defer func() {
		if err := rawterm.Restore(0, state); err != nil {
			t.Error("restore terminal:", err)
		}
	}()
	fmt.Print(pasteOn + "ready\r\n")
	var line []byte
	pasted, inPaste, escaped := false, false, false
	var approval bool
	var choiceKeys []byte
	erase := "" // what erases the choice on screen, if any
	agent := "" // the agent whose screen was replayed, if any
	word := func() {
		line = bytes.TrimRight(line, " ")
		line = line[:bytes.LastIndexByte(line, ' ')+1]
	}
	b := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(b)
		if err != nil {
			return
		}
		in := b[:n]
		if drawn, ok := map[string]string{"Est": "-est.out", "\x15": "-cleared.out"}[string(in)]; ok && agent != "" {
			recorded, err := os.ReadFile(filepath.Join(os.Getenv(recordedEnv), agent+drawn))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stdout.Write(recorded); err != nil {
				t.Fatal(err)
			}
		}
		if erase != "" { // An approval moves with arrows, and only Enter confirms it.
			if approval {
				choiceKeys = append(choiceKeys, in...)
				if !bytes.Contains(in, []byte("\r")) {
					continue
				}
				in = choiceKeys
				approval, choiceKeys = false, nil
			}
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
			case in[i] == 0x1b && i+1 < len(in) && bytes.IndexByte([]byte("]P_^X"), in[i+1]) >= 0:
				for i += 2; i < len(in) && in[i] != 0x07 && !bytes.HasPrefix(in[i:], []byte("\x1b\\")); i++ {
				}
				if i < len(in) && in[i] == 0x1b {
					i++
				}
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
				if file, ok := strings.CutPrefix(string(line), "replay "); ok || string(line) == "trust" {
					if !ok {
						file = "claude-trust.out"
					}
					recorded, err := os.ReadFile(filepath.Join(os.Getenv(recordedEnv), filepath.Base(file)))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stdout.Write(recorded); err != nil {
						t.Fatal(err)
					}
					if !ok {
						erase = "\x1b[2J\x1b[H"
					}
					agent, _ = strings.CutSuffix(filepath.Base(file), "-idle.out")
				} else if screen, ok := screens[string(line)]; ok {
					fmt.Print(screen.out)
					if screen.choice {
						approval = strings.HasSuffix(string(line), "-approval")
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

// TestSayWaitsOnTrustAndOnboarding: Claude Code's folder trust, in its words of now and of before, and the choices of
// its first run are choices too. A line typed there would pick an option ("2" picks by number) and its Enter confirm
// it: trusting a folder is the developer's decision. Answered, the choice is erased, and the line goes.
func TestSayWaitsOnTrustAndOnboarding(t *testing.T) {
	for _, screen := range []string{"trust", "old-trust", "onboarding"} {
		t.Run(screen, func(t *testing.T) {
			_, term := fakeLead(t)
			from := term.End()
			if err := term.Write([]byte(screen + "\r")); err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains("confirm"))
			from = term.End()
			say(t, term, "W2 ended (done). Continue.")
			none(t, term, from, "a choice is on screen")
			if err := term.Write([]byte("\x1b")); err != nil { // The developer answers.
				t.Fatal(err)
			}
			out := output(t, term, from, contains(`line "W2 ended (done). Continue." pasted`))
			if !strings.Contains(out, `chose "\x1b"`) {
				t.Fatalf("the choice got another key than the developer's:\n%q", out)
			}
		})
	}
}

// TestSayReadsTheScreenNow: words of a choice in what the agent wrote, or a choice scrolled away, hold nothing: only
// a choice on screen now does.
func TestSayReadsTheScreenNow(t *testing.T) {
	for _, screen := range []string{"prose", "scrolled"} {
		t.Run(screen, func(t *testing.T) {
			_, term := fakeLead(t)
			from := term.End()
			if err := term.Write([]byte(screen + "\r")); err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains("cancel"))
			from = term.End()
			say(t, term, "Q07 answered: A. Continue.")
			output(t, term, from, contains(`line "Q07 answered: A. Continue." pasted`))
		})
	}
}

// TestSayFollowsErasures: a word erased by Ctrl+W or Alt+Backspace, a line by Esc twice, leave nothing under way; a
// line recalled from the history is under way.
func TestSayFollowsErasures(t *testing.T) {
	for name, keys := range map[string][]string{
		"ctrl+w":          {"hel", "\x17"},
		"alt+backspace":   {"hel", "\x1b\x7f"},
		"esc twice":       {"hello you", "\x1b", "\x1b"},
		"esc esc at once": {"hello you", "\x1b\x1b"},
	} {
		t.Run(name, func(t *testing.T) {
			_, term := fakeLead(t)
			from := term.End()
			for _, k := range keys {
				if err := term.Write([]byte(k)); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond) // One read each, as keys come.
			}
			start := time.Now()
			say(t, term, "Q08 answered: B. Continue.")
			output(t, term, from, contains(`line "Q08 answered: B. Continue." pasted`))
			if took := time.Since(start); took >= typingIdle {
				t.Fatalf("the line came after %v, yet the prompt was cleared", took)
			}
		})
	}
	t.Run("half a line erased", func(t *testing.T) {
		_, term := fakeLead(t)
		from := term.End()
		if err := term.Write([]byte("hello you\x17")); err != nil {
			t.Fatal(err)
		}
		say(t, term, "Q09 answered: A. Continue.")
		none(t, term, from, "hello is still at the prompt")
		if err := term.Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		output(t, term, from, contains(`line "Q09 answered: A. Continue." pasted`))
	})
	t.Run("history", func(t *testing.T) {
		_, term := fakeLead(t)
		from := term.End()
		if err := term.Write([]byte("\x1b[A")); err != nil {
			t.Fatal(err)
		}
		say(t, term, "Q10 answered: A. Continue.")
		none(t, term, from, "a line of the history is at the prompt")
		if err := term.Write([]byte("\x15")); err != nil {
			t.Fatal(err)
		}
		output(t, term, from, contains(`line "Q10 answered: A. Continue." pasted`))
	})
}

// xtermAnswers is what xterm.js 6.0, the window's terminal, answers to the queries in out, as keys would come: the
// colors (OSC 10, 11 and 12 with ?), the cursor's position (CSI 6n) and the device's attributes (CSI c). It does not
// answer the keyboard's flags (CSI ? u), nor OSC 7501.
func xtermAnswers(out []byte) []byte {
	var answers []byte
	for _, q := range regexp.MustCompile(`\x1b\](1[012]);\?(?:\x07|\x1b\\)|\x1b\[6n|\x1b\[0?c`).FindAllSubmatch(out, -1) {
		switch {
		case len(q[1]) > 0:
			answers = append(answers, "\x1b]"+string(q[1])+";rgb:1e1e/1e1e/1e1e\x1b\\"...)
		case bytes.HasSuffix(q[0], []byte("n")):
			answers = append(answers, "\x1b[37;3R"...)
		default:
			answers = append(answers, "\x1b[?1;2c"...)
		}
	}
	return answers
}

// atRest runs the fake lead with agent's prompt at rest on screen, as recorded at 120 columns and 40 rows.
func atRest(t *testing.T, agent string, recorded []byte) *Terminal {
	t.Helper()
	_, term := fakeLead(t)
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	from := term.End()
	if err := term.Write([]byte("replay " + agent + "-idle.out\r")); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(string(recorded[len(recorded)-32:])))
	return term
}

// TestSayReachesAnAgentAtRest: Claude Code and Codex at their prompt at rest, as recorded (claude 2.1.295 and codex
// 0.162, 120 columns, started without a message, as a lead resumed or started with nothing to do), get the line at
// once. What the window's terminal answers to their queries is no line under way, and a pointer moving over the
// terminal, which both follow (mode 1003), no typing: Codex's colors, answered, held every line until the developer
// pressed Enter in its terminal, and a pointer moving held it for as long as it moved.
func TestSayReachesAnAgentAtRest(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			recorded, err := os.ReadFile(filepath.Join("testdata", agent+"-idle.out"))
			if err != nil {
				t.Fatal(err)
			}
			term := atRest(t, agent, recorded)
			// The window answers, as it would, and the developer moves the pointer over the terminal, away and back.
			if err := term.Write(xtermAnswers(recorded)); err != nil {
				t.Fatal(err)
			}
			if agent == "codex" && len(xtermAnswers(recorded)) == 0 {
				t.Fatal("Codex as recorded asks its colors: the answers are missing")
			}
			stop := make(chan struct{})
			moved := make(chan struct{})
			go func() {
				defer close(moved)
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					case <-time.After(10 * time.Millisecond):
					}
					report := fmt.Sprintf("\x1b[<35;%d;12M", 10+i%40)
					if i%20 == 0 {
						report += "\x1b[O\x1b[I"
					}
					if term.Write([]byte(report)) != nil {
						return
					}
				}
			}()
			defer func() { close(stop); <-moved }()
			term.mu.Lock()
			choosing, typing := term.choosing(), term.underWay()
			term.mu.Unlock()
			if choosing || typing {
				t.Fatalf("at rest: choosing %v, typing %v; want neither", choosing, typing)
			}
			from := term.End()
			start := time.Now()
			say(t, term, "Q01 answered: A. Continue.")
			output(t, term, from, contains(`line "Q01 answered: A. Continue." pasted`))
			if took := time.Since(start); took >= 2*time.Second { // Well within typingIdle.
				t.Fatalf("the line came after %v at a prompt at rest", took)
			}
		})
	}
}

// TestReportsAreNoKeys: what the terminal sends on its own leaves nothing under way and is no key; keys around it
// still count.
func TestReportsAreNoKeys(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b]10;rgb:ffff/ffff/ffff\x1b\\\x1b]11;rgb:0000/0000/0000\x07": "",
		"\x1b[<35;10;5M\x1b[<0;10;5m\x1b[M !!\x1b[I\x1b[O":               "",
		"\x1b[12;40R\x1b[?1;2c\x1b[>0;276;0c\x1b[?2004;1$y\x1b[?0u":      "",
		"a\x1b]11;rgb:0000/0000/0000\x1b\\b\x1b[<35;1;1Mc":               "abc",
		"\x1bP>|xterm.js(6.0.0)\x1b\\\x1b[A\x7f":                         "\x1b[A\x7f",
	} {
		if got := string(withoutReports([]byte(in))); got != want {
			t.Errorf("withoutReports(%q) = %q, want %q", in, got, want)
		}
	}
	term := &Terminal{screen: newScreen(80, 24)}
	term.keys([]byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\\x1b[<35;10;5M"))
	if term.underWay() || !term.voice.typed.IsZero() {
		t.Fatalf("reports: draft %q, last key %v; want none", string(term.voice.draft), term.voice.typed)
	}
	term.keys([]byte("hi"))
	if !term.underWay() {
		t.Fatal("keys typed are under way")
	}
}

// TestSayNeverJoinsAWordLeftAtThePrompt: a word the developer left at Claude Code's or Codex's prompt, as each draws
// it (recorded), holds the line however long the developer thinks, past typingIdle: typed then, it would be sent
// glued to the word ("EstW24 ended (done). Continue."). Cleared, the prompt shows its placeholder again, and the line
// goes alone.
func TestSayNeverJoinsAWordLeftAtThePrompt(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			recorded, err := os.ReadFile(filepath.Join("testdata", agent+"-idle.out"))
			if err != nil {
				t.Fatal(err)
			}
			term := atRest(t, agent, recorded)
			typingIdle = 200 * time.Millisecond // fakeLead puts it back.
			from := term.End()
			if err := term.Write([]byte("Est")); err != nil {
				t.Fatal(err)
			}
			est, err := os.ReadFile(filepath.Join("testdata", agent+"-est.out"))
			if err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains(string(est[len(est)-8:])))
			time.Sleep(2 * typingIdle) // The developer thinks.
			term.mu.Lock()
			typing := term.underWay()
			term.mu.Unlock()
			if !typing {
				t.Error("a word at the prompt is not under way")
			}
			say(t, term, "W24 ended (done). Continue.")
			time.Sleep(quiet + 4*typingIdle)
			if out := read(term, from); strings.Contains(out, "W24") {
				t.Fatalf("typed while a word was left at the prompt:\n%q", out)
			}
			if err := term.Write([]byte("\x15")); err != nil { // The developer clears the prompt.
				t.Fatal(err)
			}
			out := output(t, term, from, contains(`line "W24 ended (done). Continue." pasted, enter apart`))
			if strings.Contains(out, "EstW24") {
				t.Fatalf("the line was glued to the word:\n%q", out)
			}
		})
	}
}
