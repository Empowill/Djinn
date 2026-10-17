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

// fastVoice is the pace of Say shortened for a test: quiet q, settle s, warmup w, and 20 ms around Enter.
func fastVoice(q, s, w time.Duration) *pace {
	return paced(func(p *pace) {
		p.quiet, p.settle, p.warmup, p.enter, p.afterEnter = q, s, w, 20*time.Millisecond, 20*time.Millisecond
	})
}

// leadPace is the pace of the fake lead's terminal, as edit changes it.
func leadPace(edit func(*pace)) *pace {
	p := fastVoice(100*time.Millisecond, 50*time.Millisecond, 2*time.Second)
	p.hold, p.typingIdle, p.keyGap = 20*time.Millisecond, 3*time.Second, 10*time.Millisecond
	edit(p)
	return p
}

// echoes is a program that says each line it reads, as the lead takes each line typed.
const echoes = `printf 'ready\n'; while read -r l; do printf 'got[%s]\n' "$l"; done`

// fakeLead runs, in a terminal called lead, a program that behaves as an agent's prompt does (TestLeadProcess); cfg
// is the manager's, its pace leadPace when it gives none.
func fakeLead(t *testing.T, cfg Config) (*Manager, *Terminal) {
	t.Helper()
	if cfg.pace == nil {
		cfg.pace = leadPace(func(*pace) {})
	}
	recorded, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	cfg.env = append(cfg.env, helperEnv+"=lead", recordedEnv+"="+recorded)
	m := NewManager(cfg)
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

// write types keys into term.
func write(t *testing.T, term *Terminal, keys string) {
	t.Helper()
	if err := term.Write([]byte(keys)); err != nil {
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

// none checks that the output of term from offset from shows no line read, and no choice answered, for a while: 20
// times the fake lead's hold at least.
func none(t *testing.T, term *Terminal, from uint64, what string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	if out := read(term, from); strings.Contains(out, "line") || strings.Contains(out, "chose") {
		t.Fatalf("%s, yet the program got:\n%q", what, out)
	}
}

// TestSay: Djinn types a line, then Enter, once the person is quiet, a program just started has drawn, no choice is on
// screen and nothing is under way at the prompt; never into a choice, never glued to a word left there.
func TestSay(t *testing.T) {
	t.Parallel()
	// At the fake lead's prompt: keys typed first, a read each, and what the screen shows once they are read; then
	// the line, held by them (why) until the developer's answer, or going at once.
	for _, c := range []struct {
		name   string
		keys   []string
		shown  string
		held   string // why the line waits for answer; empty: it goes at once
		answer string // the developer's keys
		chose  string // what the choice got: the developer's keys alone
		want   string // the line as the program got it
	}{
		// At an idle prompt that asked for bracketed paste, the line arrives as one paste, then Enter on its own: the
		// agent submits it, it does not take Enter for a line break.
		{name: "pastes a line then enter", want: `pasted, enter apart`},
		// A choice that keys answer (a permission prompt) holds the line: it would pick an option. The keys that answer
		// it are not a line under way.
		{name: "waits for a choice", keys: []string{"dialog\r"}, shown: "Esc to cancel", held: "a choice is on screen",
			answer: "1", chose: `chose "1"`, want: "pasted"},
		// The line Djinn types when events wait and the lead watches none waits while an approval of Claude Code or
		// Codex is on screen: its Enter would answer the approval for the developer, usually "Yes".
		{name: "nothing into a claude approval", keys: []string{"claude-approval\r"}, shown: "to cancel",
			held: "an approval is on screen", answer: "\x1b[B\r", chose: `chose "\x1b[B\r"`, want: "pasted, enter apart"},
		{name: "nothing into a codex approval", keys: []string{"codex-approval\r"}, shown: "to cancel",
			held: "an approval is on screen", answer: "\x1b[B\r", chose: `chose "\x1b[B\r"`, want: "pasted, enter apart"},
		// Claude Code's folder trust, in its words of now and of before, and the choices of its first run are choices
		// too: trusting a folder is the developer's decision. Answered, the choice is erased, and the line goes.
		{name: "waits on trust", keys: []string{"trust\r"}, shown: "confirm", held: "a choice is on screen",
			answer: "\x1b", chose: `chose "\x1b"`, want: "pasted"},
		{name: "waits on the old trust", keys: []string{"old-trust\r"}, shown: "confirm", held: "a choice is on screen",
			answer: "\x1b", chose: `chose "\x1b"`, want: "pasted"},
		{name: "waits on onboarding", keys: []string{"onboarding\r"}, shown: "confirm", held: "a choice is on screen",
			answer: "\x1b", chose: `chose "\x1b"`, want: "pasted"},
		// Words of a choice in what the agent wrote, or a choice scrolled away, hold nothing.
		{name: "prose is no choice", keys: []string{"prose\r"}, shown: "cancel", want: "pasted"},
		{name: "a choice scrolled away", keys: []string{"scrolled\r"}, shown: "cancel", want: "pasted"},
		// A word erased by Ctrl+W or Alt+Backspace, a line by Esc twice, leave nothing under way; a line recalled from
		// the history is under way.
		{name: "ctrl+w", keys: []string{"hel", "\x17"}, want: "pasted"},
		{name: "alt+backspace", keys: []string{"hel", "\x1b\x7f"}, want: "pasted"},
		{name: "esc twice", keys: []string{"hello you", "\x1b", "\x1b"}, want: "pasted"},
		{name: "esc esc at once", keys: []string{"hello you", "\x1b\x1b"}, want: "pasted"},
		{name: "half a line erased", keys: []string{"hello you\x17"}, held: "hello is still at the prompt",
			answer: "\r", want: "pasted"},
		{name: "history", keys: []string{"\x1b[A"}, held: "a line of the history is at the prompt", answer: "\x15",
			want: "pasted"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, term := fakeLead(t, Config{})
			from := term.End()
			for _, k := range c.keys {
				write(t, term, k)
				time.Sleep(20 * time.Millisecond) // One read each, as keys come.
			}
			if c.shown != "" {
				output(t, term, from, contains(c.shown))
				from = term.End()
			}
			const line = "Q01 answered: A. Continue."
			start := time.Now()
			say(t, term, line)
			if c.held != "" {
				none(t, term, from, c.held)
				write(t, term, c.answer)
			}
			out := output(t, term, from, contains(fmt.Sprintf("line %q %s", line, c.want)))
			if !strings.Contains(out, c.chose) {
				t.Fatalf("the choice got another answer than the developer's %s:\n%q", c.chose, out)
			}
			if took := time.Since(start); c.held == "" && took >= time.Second { // Well within typingIdle.
				t.Fatalf("the line came after %v, yet nothing held it", took)
			}
		})
	}

	t.Run("types one line and enter", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{pace: fastVoice(200*time.Millisecond, 50*time.Millisecond, 2*time.Second)})
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
	})

	t.Run("waits while the person types", func(t *testing.T) {
		t.Parallel()
		p := fastVoice(250*time.Millisecond, 50*time.Millisecond, 2*time.Second)
		m := NewManager(Config{pace: p})
		term := sh(t, m, "lead-w1", "-c", echoes)
		output(t, term, 0, contains("ready"))
		// The person types a line of their own, a key at a time, well under quiet apart: Djinn's line waits until they
		// are quiet, then follows.
		write(t, term, "m")
		if err := m.Say("lead-w1", "from djinn"); err != nil {
			t.Fatal(err)
		}
		var last time.Time
		for _, k := range "ine\r" {
			time.Sleep(75 * time.Millisecond)
			write(t, term, string(k))
			last = time.Now()
		}
		out := output(t, term, 0, contains("got[from djinn]"))
		if took := time.Since(last); took < p.quiet {
			t.Errorf("the line came %v after the last key, want at least %v", took, p.quiet)
		}
		if !strings.Contains(out, "got[mine]") || strings.Index(out, "got[mine]") > strings.Index(out, "got[from djinn]") {
			t.Errorf("want the person's line whole, then Djinn's:\n%q", out)
		}
	})

	t.Run("waits for a program just started", func(t *testing.T) {
		t.Parallel()
		p := fastVoice(0, 150*time.Millisecond, 600*time.Millisecond)
		m := NewManager(Config{pace: p})
		// Both start at once. Silent: the line waits for warmup. Drawn: the line goes once the output paused for
		// settle, well before.
		silent := sh(t, m, "silent", "-c", `while read -r l; do printf 'got[%s]\n' "$l"; done`)
		silentStart := time.Now()
		say(t, silent, "hello")
		drawn := sh(t, m, "drawn", "-c", echoes)
		drawnStart := time.Now()
		say(t, drawn, "hello")
		output(t, drawn, 0, contains("got[hello]"))
		if took := time.Since(drawnStart); took < p.settle || took >= p.warmup {
			t.Errorf("a program that drew got the line after %v, want between %v and %v", took, p.settle, p.warmup)
		}
		// Read after the drawn one, which came before warmup: a line to the silent one before warmup still shows.
		output(t, silent, 0, contains("got[hello]"))
		if took := time.Since(silentStart); took < p.warmup {
			t.Errorf("a silent program got the line after %v, want at least %v", took, p.warmup)
		}
	})

	t.Run("refuses", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{pace: fastVoice(time.Hour, 0, 0)})
		if err := m.Say("lead-w1", "hello"); !errors.Is(err, ErrNotFound) {
			t.Errorf("no terminal: %v, want ErrNotFound", err)
		}
		term := sh(t, m, "lead-w1", "-c", echoes)
		write(t, term, "x") // The person types: every line waits.
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
	})

	t.Run("several lines submitted mid output", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{pace: fastVoice(100*time.Millisecond, 30*time.Millisecond, 2*time.Second)})
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
	})

	// Claude Code and Codex at their prompt at rest, as recorded (claude 2.1.295 and codex 0.162, 120 columns,
	// started without a message, as a lead resumed or started with nothing to do), get the line at once. What the
	// window's terminal answers to their queries is no line under way, and a pointer moving over the terminal, which
	// both follow (mode 1003), no typing: Codex's colors, answered, held every line until the developer pressed Enter
	// in its terminal, and a pointer moving held it for as long as it moved.
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent+" at rest", func(t *testing.T) {
			t.Parallel()
			recorded, term := atRest(t, agent, nil)
			// The window answers, as it would, and the developer moves the pointer over the terminal, away and back.
			write(t, term, string(xtermAnswers(recorded)))
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

	// A word the developer left at Claude Code's or Codex's prompt, as each draws it (recorded), holds the line
	// however long the developer thinks, past typingIdle: typed then, it would be sent glued to the word
	// ("EstW24 ended (done). Continue."). Cleared, the prompt shows its placeholder again, and the line goes alone.
	for _, agent := range []string{"claude", "codex"} {
		t.Run("never joins a word left at the "+agent+" prompt", func(t *testing.T) {
			t.Parallel()
			p := leadPace(func(p *pace) { p.typingIdle = 100 * time.Millisecond })
			_, term := atRest(t, agent, p)
			from := term.End()
			write(t, term, "Est")
			est, err := os.ReadFile(filepath.Join("testdata", agent+"-est.out"))
			if err != nil {
				t.Fatal(err)
			}
			output(t, term, from, contains(string(est[len(est)-8:])))
			time.Sleep(2 * p.typingIdle) // The developer thinks.
			term.mu.Lock()
			typing := term.underWay()
			term.mu.Unlock()
			if !typing {
				t.Error("a word at the prompt is not under way")
			}
			say(t, term, "W24 ended (done). Continue.")
			time.Sleep(p.quiet + 4*p.typingIdle)
			if out := read(term, from); strings.Contains(out, "W24") {
				t.Fatalf("typed while a word was left at the prompt:\n%q", out)
			}
			write(t, term, "\x15") // The developer clears the prompt.
			out := output(t, term, from, contains(`line "W24 ended (done). Continue." pasted, enter apart`))
			if strings.Contains(out, "EstW24") {
				t.Fatalf("the line was glued to the word:\n%q", out)
			}
		})
	}
}

// TestSayText: what the person writes to the lead keeps its line breaks in a program that takes bracketed paste,
// inside one paste; in one that does not, each break would send a line: the text goes as one line.
func TestSayText(t *testing.T) {
	t.Parallel()
	// The texts said while the person types wait, then arrive in the order said, after the person's own line.
	t.Run("keeps line breaks and order", func(t *testing.T) {
		t.Parallel()
		_, term := fakeLead(t, Config{})
		from := term.End()
		write(t, term, "hel")
		waiting, err := term.SayText("first\nsecond\r\nthird\n")
		if err != nil || !waiting {
			t.Errorf("SayText = %v, %v: want the text waiting, the person is typing", waiting, err)
		}
		if _, err := term.SayText("then\tthis\x1b[201~"); err != nil {
			t.Fatal(err)
		}
		none(t, term, from, "the person is typing")
		write(t, term, "lo\r")
		out := output(t, term, from, contains(`line "then this [201~" pasted`))
		first := strings.Index(out, `line "first\rsecond\rthird" pasted, enter apart`)
		if i, j := strings.Index(out, `line "hello"`), strings.Index(out, `line "then this [201~"`); i < 0 || first < i || j < first {
			t.Fatalf("the person's line, then the texts in the order said:\n%q", out)
		}
	})
	t.Run("joins lines without paste", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{pace: fastVoice(100*time.Millisecond, 50*time.Millisecond, 2*time.Second)})
		term := sh(t, m, "lead", "-c", echoes)
		output(t, term, 0, contains("ready"))
		if waiting, err := m.SayText("lead", "  one\ntwo\r\n"); err != nil || waiting {
			t.Fatalf("SayText = %v, %v", waiting, err)
		}
		out := output(t, term, 0, contains("got[one two]"))
		if strings.Count(out, "got[") != 1 {
			t.Fatalf("want one line read:\n%q", out)
		}
		if _, err := m.SayText("nobody", "hi"); !errors.Is(err, ErrNotFound) {
			t.Errorf("SayText to no terminal: %v, want ErrNotFound", err)
		}
	})
}

// TestWithoutReports: what the terminal sends on its own (colors, the pointer, the cursor's position, the device's
// attributes, the keyboard's flags) is no key; keys around it stay.
func TestWithoutReports(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"\x1b]10;rgb:ffff/ffff/ffff\x1b\\\x1b]11;rgb:0000/0000/0000\x07": "",
		"\x1b[<35;10;5M\x1b[<0;10;5m\x1b[M !!\x1b[I\x1b[O":               "",
		"\x1b[12;40R\x1b[?1;2c\x1b[>0;276;0c\x1b[?2004;1$y\x1b[?0u":      "",
		"a\x1b]11;rgb:0000/0000/0000\x1b\\b\x1b[<35;1;1Mc":               "abc",
		"\x1bP>|xterm.js(6.0.0)\x1b\\\x1b[A\x7f":                         "\x1b[A\x7f",
	} {
		t.Run(fmt.Sprintf("%q", in), func(t *testing.T) {
			t.Parallel()
			if got := string(withoutReports([]byte(in))); got != want {
				t.Errorf("withoutReports(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// TestKeys: reports leave nothing under way and are no key; keys typed are under way.
func TestKeys(t *testing.T) {
	t.Parallel()
	term := &Terminal{screen: newScreen(80, 24), pace: defaultPace}
	term.keys([]byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\\x1b[<35;10;5M"))
	if term.underWay() || !term.voice.typed.IsZero() {
		t.Fatalf("reports: draft %q, last key %v; want none", string(term.voice.draft), term.voice.typed)
	}
	term.keys([]byte("hi"))
	if !term.underWay() {
		t.Fatal("keys typed are under way")
	}
}

// atRest runs the fake lead at pace p (leadPace when nil) with agent's prompt at rest on screen, as recorded at 120
// columns and 40 rows, and returns what was recorded.
func atRest(t *testing.T, agent string, p *pace) ([]byte, *Terminal) {
	t.Helper()
	recorded, err := os.ReadFile(filepath.Join("testdata", agent+"-idle.out"))
	if err != nil {
		t.Fatal(err)
	}
	_, term := fakeLead(t, Config{pace: p})
	if err := term.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	from := term.End()
	write(t, term, "replay "+agent+"-idle.out\r")
	output(t, term, from, contains(string(recorded[len(recorded)-32:])))
	return recorded, term
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

// recordedEnv gives the fake lead the folder of the agents' screens as recorded: testdata.
const recordedEnv = "DJINN_TERMINAL_RECORDED"

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

// chooseOnScreen is the case of TestChoose at the fake lead's approval.
func chooseOnScreen(t *testing.T) {
	t.Parallel()
	prompted := make(chan string, 16)
	_, term := fakeLead(t, Config{Prompted: func(t *Terminal) { prompted <- promptKey(t.Prompt()) }})
	if term.Prompt() != nil {
		t.Fatal("a prompt at rest")
	}
	from := term.End()
	write(t, term, "claude-approval\r")
	output(t, term, from, contains("Tab to amend"))
	p := term.Prompt()
	if p == nil || p.Title != "Do you want to proceed?" || len(p.Options) != 3 || p.Selected != 0 {
		t.Fatalf("Prompt = %#v", p)
	}
	select {
	case key := <-prompted:
		if key != promptKey(p) {
			t.Errorf("prompted with %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no word of the choice")
	}
	from = term.End()
	if err := term.Choose("Do you want to continue?", p.Lines, p.Options, 1); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose on another choice = %v, want ErrNoPrompt", err)
	}
	if err := term.Choose(p.Title, p.Lines, p.Options, 3); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose past the options = %v, want ErrNoPrompt", err)
	}
	none(t, term, from, "nothing was chosen")
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`chose "\x1b[B\r"`))
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose once answered = %v, want ErrNoPrompt", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case key := <-prompted:
			if key == "" {
				return // The choice went.
			}
		case <-deadline:
			t.Fatal("no word of the choice going")
		}
	}
}
