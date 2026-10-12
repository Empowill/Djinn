package logx

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	testx.Main(m)
}

// syncBuffer is a buffer the log and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// capture makes the log a buffer, at debug, for the test.
func capture(t *testing.T) *syncBuffer {
	var b syncBuffer
	t.Cleanup(Use(New(&b, slog.LevelDebug)))
	return &b
}

// TestRotatingRollsOverAtSize: the file rolls over before a line takes it past its size, keeps that many older files
// and drops the oldest, so all of them stay under (keep+1) × size.
func TestRotatingRollsOverAtSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	r, err := OpenRotating(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two lines a file.
	for i := range 9 {
		if _, err := fmt.Fprintf(r, "%d%s", i, line[1:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{File: "8", File + ".1": "67", File + ".2": "45"}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(want) {
		t.Fatalf("files %v, want %d", entries, len(want))
	}
	total := 0
	for name, lines := range want {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
		var firsts string
		for l := range strings.Lines(string(b)) {
			firsts += l[:1]
		}
		if firsts != lines {
			t.Errorf("%s holds lines %q, want %q", name, firsts, lines)
		}
	}
	if total > 300 {
		t.Errorf("the files hold %d bytes, over (2+1) × 100", total)
	}
}

// TestRotatingAppendsAcrossOpens: a djinn up that starts again appends to its log, and counts what it held.
func TestRotatingAppendsAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	for range 2 {
		r, err := OpenRotating(path, 50, 1)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(r, strings.Repeat("a", 30))
		r.Close()
	}
	if b, _ := os.ReadFile(path); len(b) != 30 {
		t.Errorf("%s holds %d bytes, want the second start's 30", path, len(b))
	}
	if b, _ := os.ReadFile(path + ".1"); len(b) != 30 {
		t.Errorf("%s.1 holds %d bytes, want the first start's 30", path, len(b))
	}
}

// TestStartWritesTheLogFolder: Start writes logs/djinn.log at the level DJINN_LOG names, the log package's lines
// included.
func TestStartWritesTheLogFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv(Level, "info")
	stop, err := Start(home)
	if err != nil {
		t.Fatal(err)
	}
	L().Debug("hidden")
	L().Warn("shown", "n", 1)
	log.Printf("djinn: printed %d", 2)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	L().Warn("after stop") // Discarded again.
	b, err := os.ReadFile(filepath.Join(home, Dir, File))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "hidden") || !strings.Contains(s, "level=WARN msg=shown n=1") ||
		strings.Contains(s, "after stop") || !strings.Contains(s, `level=INFO msg="djinn: printed 2"`) {
		t.Errorf("log:\n%s", s)
	}
	t.Setenv(Level, "loud")
	if _, err := Start(home); err == nil {
		t.Error("DJINN_LOG=loud started")
	}
}

// TestTookWarnsWhenSlow: what takes over Slow logs at warn, whatever its level.
func TestTookWarnsWhenSlow(t *testing.T) {
	b := capture(t)
	Took(slog.LevelDebug, "quick", time.Now())
	Took(slog.LevelDebug, "slow", time.Now().Add(-2*Slow), "method", "/x.v1.S/M")
	s := b.String()
	if !strings.Contains(s, "level=DEBUG msg=quick") || !strings.Contains(s, "level=WARN msg=slow took=2s method=/x.v1.S/M") {
		t.Errorf("log:\n%s", s)
	}
}

// TestArgvHidesMessagesAndUsers: a command's message, title and body, and a URL's user, stay out of the log.
func TestArgvHidesMessagesAndUsers(t *testing.T) {
	got := Argv([]string{"/usr/bin/git", "-C", "/r", "commit", "-m", "secret words", "--body=more words",
		"https://x-access-token:ghs_abc@github.com/o/r.git", strings.Repeat("y", 100)})
	want := "git -C /r commit -m … --body=… https://%E2%80%A6@github.com/o/r.git " + strings.Repeat("y", argMax) + "…"
	if got != want {
		t.Errorf("Argv = %q, want %q", got, want)
	}
	if strings.Contains(got, "ghs_abc") || strings.Contains(got, "secret") {
		t.Errorf("Argv leaks: %q", got)
	}
	many := make([]string, argsMax+3)
	for i := range many {
		many[i] = "a"
	}
	if got := Argv(many); !strings.HasSuffix(got, " …+3") {
		t.Errorf("Argv of %d arguments = %q", len(many), got)
	}
}

// TestRanLogsACommand: a command logs its argv, folder, exit code; one killed at its deadline logs at warn.
func TestRanLogsACommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b := capture(t)
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Dir = t.TempDir()
	start := time.Now()
	Ran(context.Background(), cmd, start, cmd.Run())
	if s := b.String(); !strings.Contains(s, "level=INFO msg=command argv=\""+filepath.Base(exe)+" -test.run=^$\"") ||
		!strings.Contains(s, "exit=0") {
		t.Errorf("log:\n%s", s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	cmd = exec.CommandContext(ctx, exe, "-test.run=^$")
	Ran(ctx, cmd, time.Now(), cmd.Run())
	if s := b.String(); !strings.Contains(s, "level=WARN msg=command") || !strings.Contains(s, "timeout=true") {
		t.Errorf("log:\n%s", s)
	}
}

// TestShutdownDumpsThenExits: a stop held by a stuck step dumps the goroutines past DumpAfter, logs the step, then
// exits past ExitAfter; a stop that ends in time does neither.
func TestShutdownDumpsThenExits(t *testing.T) {
	b := capture(t)
	dir := t.TempDir()
	exited := make(chan int, 1)
	var before bool
	s := &Shutdown{Dir: dir, DumpAfter: 20 * time.Millisecond, ExitAfter: 200 * time.Millisecond,
		BeforeExit: func() { before = true }, Exit: func(code int) { exited <- code }}
	stuck := make(chan struct{})
	defer close(stuck)
	go s.Step("workers", func() { <-stuck })
	go s.Step("terminals", func() {})
	select {
	case code := <-exited:
		if code != HungExit {
			t.Errorf("exit %d, want %d", code, HungExit)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watchdog did not exit")
	}
	if !before {
		t.Error("BeforeExit did not run")
	}
	dumps := Dumps(dir)
	if len(dumps) != 2 {
		t.Fatalf("dumps %v, want one when slow and one at the exit", dumps)
	}
	d, err := os.ReadFile(dumps[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d), "still waiting for workers (") || !strings.Contains(string(d), "logx.(*Shutdown).Step") {
		t.Errorf("dump:\n%.2000s", d)
	}
	log := b.String()
	for _, want := range []string{"msg=\"shutdown begins\"", "msg=\"shutdown is slow\"", "waiting=\"workers (",
		"level=ERROR msg=\"shutdown hangs: exiting\"", "msg=\"shutdown step ended\"", "step=terminals"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %s:\n%s", want, log)
		}
	}
	if strings.Contains(log, "terminals (") {
		t.Errorf("the ended step is still waited for:\n%s", log)
	}

	quick := &Shutdown{Dir: t.TempDir(), DumpAfter: 20 * time.Millisecond, ExitAfter: 40 * time.Millisecond,
		Exit: func(code int) { t.Errorf("a stop that ended exited %d", code) }}
	quick.Step("workers", func() {})
	quick.End()
	time.Sleep(80 * time.Millisecond)
	if d := Dumps(quick.Dir); len(d) != 0 {
		t.Errorf("a stop that ended dumped %v", d)
	}
}

// TestDumpKeepsTheLatest: the log folder keeps the last DumpsKept dumps.
func TestDumpKeepsTheLatest(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range DumpsKept + 2 {
		p, err := Dump(dir, fmt.Sprint("test ", i))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if got := Dumps(dir); strings.Join(got, ",") != strings.Join(paths[2:], ",") {
		t.Errorf("kept %v, want %v", got, paths[2:])
	}
}
