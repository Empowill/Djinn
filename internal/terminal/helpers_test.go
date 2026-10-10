package terminal

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// helperEnv tells TestHelperProcess, run in a terminal by a test, what to do.
const helperEnv = "DJINN_TERMINAL_HELPER"

// output follows the output of term from offset from, and returns all of it once match finds what it looks for.
func output(t *testing.T, term *Terminal, from uint64, match func(string) bool) string {
	t.Helper()
	var out bytes.Buffer
	stop := make(chan struct{})
	read := make(chan bool)
	go func() {
		found := false
		_ = term.Read(from, stop, func(o Output) error {
			out.Write(o.Data)
			if found = match(out.String()); found {
				return errors.New("found")
			}
			return nil
		})
		read <- found
	}()
	timeout := time.AfterFunc(5*time.Second, func() { close(stop) })
	found := <-read
	timeout.Stop()
	if !found {
		t.Fatalf("not found in the output of the terminal:\n%q", out.String())
	}
	return out.String()
}

func contains(s string) func(string) bool {
	return func(out string) bool { return strings.Contains(out, s) }
}

// end waits for the program of term to end and returns its exit code.
func end(t *testing.T, term *Terminal) int {
	t.Helper()
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the program did not end")
	}
	_, _, exited, code := term.State()
	if !exited {
		t.Fatal("ended but not exited")
	}
	return code
}
