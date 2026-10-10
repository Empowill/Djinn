//go:build !windows

// Not on Windows: these tests drive /bin/sh on a pseudo-terminal (see terminal_test.go).

package terminal

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fastVoice shortens the delays of Say for a test.
func fastVoice(t *testing.T, q, s, w time.Duration) {
	t.Helper()
	old := [...]time.Duration{quiet, settle, warmup, enter}
	quiet, settle, warmup, enter = q, s, w, 20*time.Millisecond
	t.Cleanup(func() { quiet, settle, warmup, enter = old[0], old[1], old[2], old[3] })
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
