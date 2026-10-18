//go:build !windows

// The pseudo-terminal tests drive /bin/sh, so they run on macOS and Linux; Windows has its own in
// pty_windows_test.go.
package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	rawterm "golang.org/x/term"
)

// sh opens a terminal running /bin/sh in a temporary directory, closed at the end of the test.
func sh(t *testing.T, m *Manager, name string, args ...string) *Terminal {
	t.Helper()
	term, _, err := m.Open(name, append([]string{"/bin/sh"}, args...), t.TempDir(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return term
}

// TestOpen: a terminal starts its program, or attaches to the one running under its name; the manager's defaults fill
// what the window leaves empty.
func TestOpen(t *testing.T) {
	t.Parallel()
	t.Run("attaches to the running terminal", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{Command: []string{"/bin/sh"}, Dir: t.TempDir()})
		t.Cleanup(m.Close)
		first, attached, err := m.Open("main", nil, "", 0, 0)
		if err != nil || attached {
			t.Fatalf("first open: attached %v, %v", attached, err)
		}
		again, attached, err := m.Open("main", nil, "", 0, 0)
		if err != nil || !attached || again != first {
			t.Fatalf("second open: attached %v, same %v, %v", attached, again == first, err)
		}
		other, attached, err := m.Open("other", nil, "", 0, 0)
		if err != nil || attached || other == first {
			t.Fatalf("other name: attached %v, %v", attached, err)
		}
		// The defaults of the manager apply: the shell runs in its directory.
		if err := first.Write([]byte("pwd\n")); err != nil {
			t.Fatal(err)
		}
		output(t, first, 0, contains(m.cfg.Dir))
		// Once ended, the name starts a new program, and the old one is forgotten.
		if err := first.Write([]byte("exit\n")); err != nil {
			t.Fatal(err)
		}
		end(t, first)
		next, attached, err := m.Open("main", nil, "", 0, 0)
		if err != nil || attached || next.ID == first.ID {
			t.Fatalf("open after the end: attached %v, new %v, %v", attached, next.ID != first.ID, err)
		}
		if _, err := m.Get(first.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get the ended terminal: %v, want ErrNotFound", err)
		}
	})
	// Without a folder from the window nor Dir, a terminal starts in the one Folder gives, asked at each start; with
	// none, it does not start, never in the home folder.
	t.Run("starts in a folder given", func(t *testing.T) {
		t.Parallel()
		if _, _, err := NewManager(Config{Command: []string{"/bin/sh"}}).Open("main", nil, "", 0, 0); !errors.Is(err, ErrNoFolder) {
			t.Fatalf("no folder at all: %v, want ErrNoFolder", err)
		}
		none := errors.New("no project yet")
		folder := ""
		m := NewManager(Config{Command: []string{"/bin/sh"}, Folder: func() (string, error) {
			if folder == "" {
				return "", none
			}
			return folder, nil
		}})
		t.Cleanup(m.Close)
		if _, _, err := m.Open("main", nil, "", 0, 0); !errors.Is(err, ErrNoFolder) || !errors.Is(err, none) {
			t.Fatalf("no folder yet: %v, want ErrNoFolder and why", err)
		}
		folder = t.TempDir()
		term, _, err := m.Open("main", nil, "", 0, 0)
		if err != nil || term.Dir != folder {
			t.Fatalf("open once a folder is given: %v in %q, want %q", err, term.Dir, folder)
		}
	})
	// BeforeStart runs before the program starts, and what it sets is what the program gets: djinn up brings its PATH
	// up to date there, so that a lead finds an agent installed from the window meanwhile.
	t.Run("before start runs before the program", func(t *testing.T) {
		t.Parallel()
		var m *Manager
		m = NewManager(Config{BeforeStart: func() { m.cfg.env = []string{"DJINN_BEFORE_START=set"} }})
		t.Cleanup(m.Close)
		term, _, err := m.Open("main", []string{"/bin/sh", "-c", `echo "got $DJINN_BEFORE_START"`}, t.TempDir(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		output(t, term, 0, contains("got set"))
	})
	// Changed runs once a program starts and once it ends, when Running already says so; an Open that attaches
	// changes nothing.
	t.Run("changed follows what runs", func(t *testing.T) {
		t.Parallel()
		calls := make(chan []string, 10)
		var m *Manager
		m = NewManager(Config{Changed: func() {
			var names []string
			for _, r := range m.Running() {
				names = append(names, r.Name)
			}
			calls <- names
		}})
		next := func() string {
			t.Helper()
			select {
			case names := <-calls:
				return strings.Join(names, ",")
			case <-time.After(5 * time.Second):
				t.Fatal("Changed did not run")
				return ""
			}
		}
		term := sh(t, m, "lead-a")
		if got := next(); got != "lead-a" {
			t.Fatalf("after the start: %q", got)
		}
		if _, attached, err := m.Open("lead-a", nil, "", 0, 0); err != nil || !attached {
			t.Fatalf("attach: %v, %v", attached, err)
		}
		if err := term.Write([]byte("exit\n")); err != nil {
			t.Fatal(err)
		}
		if got := next(); got != "" {
			t.Fatalf("after the end: %q", got)
		}
		select {
		case names := <-calls:
			t.Fatalf("one more call: %q", names)
		default:
		}
	})
}

// TestOpenExclusive: a session that runs in a terminal of another name refuses a second program; another session, or
// its own name, opens.
func TestOpenExclusive(t *testing.T) {
	t.Parallel()
	m := NewManager(Config{})
	t.Cleanup(m.Close)
	dir := t.TempDir()
	const session = "0b7e2a8c-5f1d-4c1e-9a3e-1f2d3c4b5a69"
	// The terminal of the window already resumes the session, as djinn up --terminal "claude --resume …" does.
	main, _, err := m.Open("main", []string{"/bin/sh", "-c", "exec cat # claude --resume " + session}, dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.OpenExclusive("lead-1", []string{"/bin/sh"}, dir, 0, 0, session); !errors.Is(err, ErrBusy) ||
		!strings.Contains(err.Error(), `"main"`) {
		t.Fatalf("open a second program on the session: %v, want ErrBusy naming main", err)
	}
	// Another session, or none, starts.
	lead, attached, err := m.OpenExclusive("lead-1", []string{"/bin/sh"}, dir, 0, 0, "another-session")
	if err != nil || attached {
		t.Fatalf("another session: attached %v, %v", attached, err)
	}
	// Its own name attaches, whatever runs elsewhere.
	if again, attached, err := m.OpenExclusive("lead-1", nil, "", 0, 0, session); err != nil || !attached || again != lead {
		t.Fatalf("attach to the lead: attached %v, %v", attached, err)
	}
	// Once the other program ended, the session is free.
	main.Hangup()
	end(t, main)
	if _, _, err := m.OpenExclusive("lead-2", []string{"/bin/sh"}, dir, 0, 0, session); err != nil {
		t.Fatalf("open once the session is free: %v", err)
	}
}

// TestWrite: keys written reach the program as typed, and none once it ended.
func TestWrite(t *testing.T) {
	t.Parallel()
	t.Run("then read", func(t *testing.T) {
		t.Parallel()
		m := NewManager(Config{})
		term := sh(t, m, "main")
		// The quotes keep the typed line, which the terminal echoes, from matching.
		if err := term.Write([]byte("echo hel''lo; exit 3\n")); err != nil {
			t.Fatal(err)
		}
		output(t, term, 0, contains("hello"))
		if code := end(t, term); code != 3 {
			t.Fatalf("exit code %d, want 3", code)
		}
		// The whole output is still there, and the end comes last.
		var last Output
		if err := term.Read(0, nil, func(o Output) error { last = o; return nil }); err != nil {
			t.Fatal(err)
		}
		if !last.Exited || last.Code != 3 {
			t.Fatalf("last piece %+v, want the end with code 3", last)
		}
		if err := term.Write([]byte("x")); !errors.Is(err, ErrExited) {
			t.Fatalf("write after the end: %v, want ErrExited", err)
		}
	})
	// What Claude Code's dictation needs in hold mode: a terminal sends no key release, so the program tells a held
	// Space from the key repeat, a quick run of spaces. Each space written to the terminal, at a key repeat rate, must
	// reach a program in raw mode on its own and at once, not batched.
	t.Run("a held space arrives as repeats", func(t *testing.T) {
		t.Parallel()
		const repeats, every = 20, 30 * time.Millisecond // A usual key repeat: 30 a second.
		m := NewManager(Config{env: []string{helperEnv + "=spaces"}})
		t.Cleanup(m.Close)
		term, _, err := m.Open("main", []string{os.Args[0], "-test.run=^TestHelperProcess$"}, t.TempDir(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		out := output(t, term, 0, contains("ready"))
		// The time each space is typed, on the clock the program reads too. Its reads are compared with these, not with
		// one another: a program late on one read, under load, then reads the next one sooner, and nothing was batched.
		typed := make([]int64, repeats)
		for i := range repeats {
			typed[i] = time.Now().UnixMicro()
			if err := term.Write([]byte(" ")); err != nil {
				t.Fatal(err)
			}
			time.Sleep(every)
		}
		out = output(t, term, uint64(len(out)), contains("done"))
		// One line a read: the byte count of the read and when it came, in microseconds since the epoch. A read that
		// came a whole key repeat after its space was typed (two for a read of several spaces, which a terminal
		// holding one space until the next shows as one repeat) says the program was late, not the terminal: under
		// load (the end-to-end tests run beside these, W306) the check proves nothing, and the test says so.
		reads := regexp.MustCompile(`read (\d+) at (\d+)`).FindAllStringSubmatch(out, -1)
		var late int64
		i := 0
		for _, r := range reads {
			n, _ := strconv.Atoi(r[1])
			at, _ := strconv.ParseInt(r[2], 10, 64)
			if i >= repeats {
				t.Fatalf("the program read more than %d spaces:\n%s", repeats, out)
			}
			if at-typed[i] >= every.Microseconds()*int64(min(n, 2)) {
				t.Skipf("read %d came %d ms after its space was typed, the program was late (the machine is under "+
					"load): the check needs a calm machine", i, (at-typed[i])/1000)
			}
			if n != 1 {
				t.Fatalf("read %d got %d bytes, want one space at a time", i, n)
			}
			if i+1 < repeats && at >= typed[i+1] {
				t.Fatalf("read %d came %d ms after the next space was typed: held back, not as typed",
					i, (at-typed[i+1])/1000)
			}
			late = max(late, at-typed[i])
			i += n
		}
		if i != repeats {
			t.Fatalf("the program read %d spaces, want %d:\n%s", i, repeats, out)
		}
		t.Logf("%d spaces, one read each, at most %d ms after they were typed", repeats, late/1000)
	})
}

// TestRead checks the scrollback alone: a read from an offset gets what follows it, and a read from an offset no
// longer kept starts at the oldest byte kept.
func TestRead(t *testing.T) {
	t.Parallel()
	term := &Terminal{changed: make(chan struct{}), screen: newScreen(80, 24)}
	var all []byte
	for i := range 40 {
		piece := bytes.Repeat([]byte{byte('a' + i%26)}, 50_000)
		all = append(all, piece...)
		term.append(piece)
	}
	read := func(from uint64) (first uint64, data []byte) {
		stop := make(chan struct{})
		close(stop) // Stop once all is sent: the program has not ended.
		first = ^uint64(0)
		if err := term.Read(from, stop, func(o Output) error {
			first = min(first, o.Offset)
			data = append(data, o.Data...)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return first, data
	}
	total := uint64(len(all))
	first, data := read(0)
	if kept := total - first; kept < Scrollback || kept > Scrollback+Scrollback/4 || first == 0 {
		t.Fatalf("kept %d bytes from %d, want the last mebibyte or a quarter more", kept, first)
	}
	if !bytes.Equal(data, all[first:]) {
		t.Fatal("the bytes kept are not the end of the output")
	}
	from := total - 1234
	if first, data := read(from); first != from || !bytes.Equal(data, all[from:]) {
		t.Fatalf("resume from %d: got %d bytes from %d", from, len(data), first)
	}
	if first, data := read(total + 10); len(data) != 0 {
		t.Fatalf("read past the end: %d bytes from %d", len(data), first)
	}
}

func TestResize(t *testing.T) {
	t.Parallel()
	m := NewManager(Config{})
	term, _, err := m.Open("main", []string{"/bin/sh"}, t.TempDir(), 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	// stty prints rows then columns; the offsets keep each answer apart.
	if err := term.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	out := output(t, term, 0, regexp.MustCompile(`(^|\s)30 120\r?\n`).MatchString)
	if err := term.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	if cols, rows, _, _ := term.State(); cols != 100 || rows != 40 {
		t.Fatalf("state %d×%d, want 100×40", cols, rows)
	}
	if err := term.Write([]byte("stty size\n")); err != nil {
		t.Fatal(err)
	}
	output(t, term, uint64(len(out)), regexp.MustCompile(`(^|\s)40 100\r?\n`).MatchString)
}

// TestClose: closing the manager hangs up every program, kills those still there after the grace delay, and opens
// nothing after.
func TestClose(t *testing.T) {
	t.Parallel()
	const grace = 300 * time.Millisecond
	m := NewManager(Config{pace: paced(func(p *pace) { p.grace = grace })})
	polite := sh(t, m, "polite", "-c", `trap 'exit 7' HUP; echo ready; while :; do sleep 0.05; done`)
	stubborn := sh(t, m, "stubborn", "-c", `trap '' HUP; echo ready; while :; do sleep 0.05; done`)
	output(t, polite, 0, contains("ready"))
	output(t, stubborn, 0, contains("ready"))
	start := time.Now()
	m.Close() // djinn up stopping.
	if code := end(t, polite); code != 7 {
		t.Fatalf("the program that handles SIGHUP ended with %d, want 7", code)
	}
	if code := end(t, stubborn); code != -1 {
		t.Fatalf("the program that ignores SIGHUP ended with %d, want -1 (killed)", code)
	}
	if took := time.Since(start); took < grace || took > grace+2*time.Second {
		t.Fatalf("closing took %v, want the grace delay %v and a little", took, grace)
	}
	if _, _, err := m.Open("again", nil, "", 0, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("open after close: %v, want ErrClosed", err)
	}
}

// TestHangup: a wish deleted hangs up its lead's terminal by name, and leaves the others running.
func TestHangup(t *testing.T) {
	t.Parallel()
	m := NewManager(Config{})
	lead := sh(t, m, "lead-w", "-c", `trap 'exit 7' HUP; echo ready; while :; do sleep 0.05; done`)
	other := sh(t, m, "other", "-c", `echo ready; while :; do sleep 0.05; done`)
	output(t, lead, 0, contains("ready"))
	output(t, other, 0, contains("ready"))
	if !m.Hangup("lead-w") {
		t.Fatal("hangup of a running terminal: false")
	}
	if code := end(t, lead); code != 7 {
		t.Fatalf("the lead ended with %d, want 7", code)
	}
	if m.Hangup("lead-w") || m.Hangup("none") {
		t.Fatal("hangup of an ended or unknown terminal: true")
	}
	if other.Exited() {
		t.Fatal("the other terminal ended")
	}
}

// TestHelperProcess is the program the tests run in a terminal, not a test: with spaces, it puts its terminal in raw
// mode, as Claude Code does, and reports each read until it has read 20 spaces.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "spaces" {
		t.Skip("run by TestWrite")
	}
	state, err := rawterm.MakeRaw(0)
	if err != nil {
		fmt.Println("raw:", err)
		os.Exit(1)
	}
	defer rawterm.Restore(0, state) //nolint:errcheck // Restored by hand before the exit below.
	fmt.Print("ready\r\n")
	b := make([]byte, 64)
	for count := 0; count < 20; {
		n, err := os.Stdin.Read(b)
		if err != nil {
			os.Exit(1) //nolint:gocritic // The pseudo-terminal goes with this process.
		}
		at := time.Now().UnixMicro()
		count += bytes.Count(b[:n], []byte(" "))
		fmt.Printf("read %d at %d\r\n", n, at)
	}
	fmt.Print("done\r\n")
	rawterm.Restore(0, state) //nolint:errcheck // Exits next: nothing to report it to.
	os.Exit(0)
}
