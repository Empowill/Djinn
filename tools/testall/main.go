// Command testall runs the parts of `go tool task test` at once, each as its own `go tool task <part>` whose output
// goes to its own file (test-results/<part>.log), and prints them apart: a green part's output as soon as it ends, a
// red part's at the end, under its name, and last a line naming the parts that failed. A push check keeps only the
// last lines of its output (W124): they name what failed, never another part's green summary. The parts run until
// each ends, whatever the others do: one run shows every red.
//
//	go run ./tools/testall test-go test-ui e2e
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	dir := flag.String("out", "test-results", "the folder of the parts' logs, <part>.log each")
	flag.Parse()
	parts := flag.Args()
	if len(parts) == 0 {
		parts = []string{"test-go", "test-ui", "e2e"}
	}
	// A stop (the gate's cancel, Ctrl-C) ends the parts, which Task passes on to their commands.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Stdout, *dir, parts, startTask)
	stop()
	os.Exit(code)
}

// startTask runs `go tool task part`, its output to log, until it ends. The parts know they run beside the others
// (DJINN_TEST_BESIDE): a time limit on a test, measured for a part alone, is reported there and fails only well past.
func startTask(ctx context.Context, part string, log io.Writer) error {
	cmd := exec.CommandContext(ctx, "go", "tool", "task", part)
	cmd.Env = append(os.Environ(), "DJINN_TEST_BESIDE=1")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second
	return cmd.Run()
}

// ended is a part that ran: how long it took and how it ended.
type ended struct {
	part string
	took time.Duration
	err  error
}

// run runs the parts at once through start, each writing dir/<part>.log, prints their outputs apart on out and
// returns the exit code: 0 when every part passed, 1 otherwise.
func run(ctx context.Context, out io.Writer, dir string, parts []string, start func(context.Context, string, io.Writer) error) int {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(out, "testall:", err)
		return 1
	}
	var mu sync.Mutex // one part's output at a time on out
	var wg sync.WaitGroup
	results := make([]ended, len(parts))
	begin := time.Now()
	for i, part := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := filepath.Join(dir, part+".log")
			f, err := os.Create(name)
			if err != nil {
				results[i] = ended{part, 0, err}
				return
			}
			err = start(ctx, part, f)
			f.Close()
			took := time.Since(begin).Round(time.Second)
			results[i] = ended{part, took, err}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Fprintf(out, "testall: %s RED after %s, its output (%s) comes at the end\n", part, took, name)
				return
			}
			fmt.Fprintf(out, "testall: %s green after %s, its output (%s):\n", part, took, name)
			copyLog(out, name)
		}()
	}
	wg.Wait()
	var failed []string
	for _, r := range results {
		if r.err == nil {
			continue
		}
		failed = append(failed, r.part)
		name := filepath.Join(dir, r.part+".log")
		fmt.Fprintf(out, "\ntestall: %s RED after %s (%v), its output (%s):\n", r.part, r.took, r.err, name)
		copyLog(out, name)
	}
	if len(failed) == 0 {
		var took []string
		for _, r := range results {
			took = append(took, r.part+" "+r.took.String())
		}
		fmt.Fprintf(out, "\ntestall: every part green in %s (%s)\n", time.Since(begin).Round(time.Second), strings.Join(took, ", "))
		return 0
	}
	// Last, since a push check keeps only the end of the output.
	fmt.Fprintf(out, "\nFailed: %s\n", strings.Join(failed, ", "))
	return 1
}

// copyLog writes the file name to out, ending with a newline.
func copyLog(out io.Writer, name string) {
	f, err := os.Open(name)
	if err != nil {
		fmt.Fprintln(out, "testall:", err)
		return
	}
	defer f.Close()
	n, _ := io.Copy(out, f)
	if n > 0 {
		b := make([]byte, 1)
		if _, err := f.ReadAt(b, n-1); err == nil && b[0] != '\n' {
			fmt.Fprintln(out)
		}
	}
}
