//go:build windows

// The pseudo-console tests: the command interpreter, and this test binary run as a program of the terminal.
package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	rawterm "golang.org/x/term"
)

// helper opens a terminal running TestHelperProcess with mode, closed at the end of the test.
func helper(t *testing.T, m *Manager, name, mode string, cols, rows int) *Terminal {
	t.Helper()
	t.Setenv(helperEnv, mode)
	term, _, err := m.Open(name, []string{os.Args[0], "-test.run=^TestHelperProcess$"}, t.TempDir(), cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return term
}

func TestConsoleWriteThenRead(t *testing.T) {
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	term, _, err := m.Open("main", []string{comspec()}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// The caret keeps the typed line, which the console echoes, from matching. Enter is a carriage return.
	if err := term.Write([]byte("echo hel^lo& exit 3\r")); err != nil {
		t.Fatal(err)
	}
	output(t, term, 0, contains("hello"))
	start := time.Now()
	if code := end(t, term); code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	// No process holds the console any more: it closes at once, without waiting for a child.
	if took := time.Since(start); took >= drain {
		t.Fatalf("the end took %v, the delay left to a child holding the terminal", took)
	}
	if err := term.Write([]byte("x")); err != ErrExited {
		t.Fatalf("write after the end: %v, want ErrExited", err)
	}
}

// TestConsoleProgramThatEndsAtOnce: the output of a program that ends at once, in the folder and with the
// environment given, reaches the terminal whole.
func TestConsoleProgramThatEndsAtOnce(t *testing.T) {
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	dir := t.TempDir()
	term, _, err := m.Open("main", ShellCommand("cd& echo %TERM%& exit /b 4"), dir, 250, 24) // Wide: no line wraps.
	if err != nil {
		t.Fatal(err)
	}
	if code := end(t, term); code != 4 {
		t.Fatalf("exit code %d, want 4", code)
	}
	var out strings.Builder
	if err := term.Read(0, nil, func(o Output) error { out.Write(o.Data); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{strings.ToLower(dir), "xterm-256color"} {
		if !strings.Contains(strings.ToLower(out.String()), want) {
			t.Fatalf("%q not in the output:\n%q", want, out.String())
		}
	}
}

func TestConsoleResize(t *testing.T) {
	m := NewManager(Config{})
	term := helper(t, m, "main", "size", 120, 30)
	out := output(t, term, 0, contains("size 120x30"))
	if err := term.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	if cols, rows, _, _ := term.State(); cols != 100 || rows != 40 {
		t.Fatalf("state %d×%d, want 100×40", cols, rows)
	}
	output(t, term, uint64(len(out)), contains("size 100x40"))
}

// TestConsoleCloseKillsTheTree: closing the terminals sends CTRL_CLOSE_EVENT; the command interpreter ends, and a
// program that holds on is killed after the grace delay with the process it started.
func TestConsoleCloseKillsTheTree(t *testing.T) {
	defer func(g time.Duration) { grace = g }(grace)
	grace = 300 * time.Millisecond
	m := NewManager(Config{})
	polite, _, err := m.Open("polite", []string{comspec()}, t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	stubborn := helper(t, m, "stubborn", "stubborn", 80, 24)
	out := output(t, stubborn, 0, regexp.MustCompile(`ready \d+\r?\n`).MatchString)
	pid, _ := strconv.Atoi(regexp.MustCompile(`ready (\d+)`).FindStringSubmatch(out)[1])
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(child, 1)
		_ = windows.CloseHandle(child)
	})
	start := time.Now()
	m.Close() // djinn up stopping.
	if code := end(t, polite); code == -1 {
		t.Fatal("the command interpreter was killed, want it to end on CTRL_CLOSE_EVENT")
	}
	if code := end(t, stubborn); code != -1 {
		t.Fatalf("the program that holds on ended with %d, want -1 (killed)", code)
	}
	if took := time.Since(start); took < grace {
		t.Fatalf("closing took %v, want the grace delay %v at least", took, grace)
	}
	if ev, err := windows.WaitForSingleObject(child, 1000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the child of the killed program still runs: %d, %v", ev, err)
	}
}

// TestHelperProcess is the program the tests run in a terminal, not a test. With size, it prints the size of its
// console each time it changes, for 5 s; with stubborn, it starts a child like it, prints its id, and holds on through
// CTRL_CLOSE_EVENT as both wait to be killed.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "size":
		con, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			fmt.Println("console:", err)
			os.Exit(1)
		}
		last := ""
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			cols, rows, err := rawterm.GetSize(int(con.Fd()))
			if size := fmt.Sprintf("size %dx%d", cols, rows); err == nil && size != last {
				fmt.Print(size, "\r\n")
				last = size
			}
		}
		os.Exit(0)
	case "stubborn":
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), helperEnv+"=child")
		if err := cmd.Start(); err != nil {
			fmt.Println("child:", err)
			os.Exit(1)
		}
		signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM) // CTRL_CLOSE_EVENT then waits to be killed.
		fmt.Printf("ready %d\r\n", cmd.Process.Pid)
		time.Sleep(time.Hour) // Not select{}: the runtime would call that a deadlock.
	case "child":
		signal.Notify(make(chan os.Signal, 1), syscall.SIGTERM)
		time.Sleep(time.Hour)
	}
	t.Skip("run by the console tests")
}
