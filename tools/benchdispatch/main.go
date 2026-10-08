// Command benchdispatch is the dispatch bench (T16, plan/263f074f-dispatch-bench.md): it runs the plain Go
// scheduler on every case of internal/dispatch/bench/cases.json and prints a Markdown table of its decisions, the
// expected ones, and the time per pass. It calls no model. The local-model side is not here yet.
//
//	go tool task bench-dispatch
package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/empowill/djinn/internal/dispatch/bench"
)

func main() {
	cases, err := bench.Cases()
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchdispatch:", err)
		os.Exit(1)
	}
	var results []bench.Result
	for _, c := range cases {
		results = append(results, bench.Go(c, 50*time.Millisecond))
	}
	fmt.Printf("Dispatch bench: %d cases, on %s/%s, %d cores.\n\n", len(cases), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	bench.Table(os.Stdout, "Go", results)
	for _, r := range results {
		if !r.Right() {
			os.Exit(1)
		}
	}
}
