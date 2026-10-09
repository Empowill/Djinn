// Command slowtests reads `go test -json` on its input, prints what go test prints without -json (the output of the
// tests that fail, the build errors, a line per package), and fails when a test fails or when a top-level test took
// longer than -max: tests stay fast, and a slow one is caught the day it slows down.
//
//	go test -json ./... | go run ./tools/slowtests
//
// A test that must stay slow goes in allowed, with why: the list stays empty unless a test has a reason.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// allowed are the tests that may take longer than -max, by package and name ("github.com/…/cmd/djinn TestX"), each
// with why it cannot be fast. Keep it empty.
var allowed = map[string]string{
	// Two real djinn up processes, the first restarting into a fake release's binary: about 1.3 s alone, its polls
	// already at 20–50 ms; up to 2 s while the other packages test in parallel.
	"github.com/empowill/djinn/cmd/djinn TestUpdateFromRelease": "two real djinn up processes, one restarting into the other",
}

// event is a line of go test -json (go doc test2json).
type event struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	ImportPath  string // build-output
	FailedBuild string
}

// slow is a test over the limit.
type slow struct {
	name    string
	elapsed time.Duration
}

// result is what check found.
type result struct {
	failed bool   // a test, a package or a build failed
	slow   []slow // the tests over the limit and not allowed, the slowest first
}

func main() {
	max := flag.Duration("max", 2*time.Second, "the longest a top-level test may take")
	flag.Parse()
	res, err := check(os.Stdin, os.Stdout, *max)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slowtests:", err)
		os.Exit(1)
	}
	if len(res.slow) > 0 {
		fmt.Fprintf(os.Stderr, "\nThese tests take longer than %s: make them fast (a fake clock, a short tick, a wait on an event "+
			"instead of a sleep), or say why in tools/slowtests/main.go (allowed).\n", *max)
		for _, s := range res.slow {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", s.elapsed.Round(10*time.Millisecond), s.name)
		}
	}
	if res.failed || len(res.slow) > 0 {
		os.Exit(1)
	}
}

// check reads the events of in, writes to out what go test prints without -json, and says what failed and which
// tests were too slow. A line that is not an event (go test's own error) is printed and fails.
func check(in io.Reader, out io.Writer, max time.Duration) (result, error) {
	var res result
	held := map[string][]string{} // the output of each running test, printed only if it fails
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var e event
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' || json.Unmarshal(line, &e) != nil {
			fmt.Fprintln(out, string(line))
			res.failed = true
			continue
		}
		key := e.Package + " " + e.Test
		switch {
		case e.Action == "build-output":
			fmt.Fprint(out, e.Output)
		case e.Action == "build-fail":
			res.failed = true
		case e.Test == "":
			if e.Action == "output" && e.Output != "PASS\n" { // go test says it only with -v
				fmt.Fprint(out, e.Output)
			}
			if e.Action == "fail" {
				res.failed = true
			}
		case e.Action == "output":
			held[key] = append(held[key], e.Output)
		case e.Action == "pass" || e.Action == "fail" || e.Action == "skip":
			if e.Action == "fail" {
				res.failed = true
				fmt.Fprint(out, strings.Join(held[key], ""))
			}
			delete(held, key)
			elapsed := time.Duration(e.Elapsed * float64(time.Second))
			if strings.Contains(e.Test, "/") || elapsed <= max {
				continue
			}
			if _, ok := allowed[key]; !ok {
				res.slow = append(res.slow, slow{key, elapsed})
			}
		}
	}
	slices.SortFunc(res.slow, func(a, b slow) int { return int(b.elapsed - a.elapsed) })
	return res, sc.Err()
}
