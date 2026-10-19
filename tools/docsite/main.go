// Command docsite builds the Grimoire, the documentation site, into a folder that opens offline: docs/site, its pages
// rendered and its Command line filled in from the command tree djinn is built from. `go tool task docs` runs it: go run ./tools/docsite bin/docs.
// djinn up serves the same site at /docs/.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/empowill/djinn/internal/docsite"
)

func main() {
	os.Exit(run(".", os.Args[1:], os.Stdout, os.Stderr))
}

// run builds the site of the repository at repo into the folder args name, and returns the exit code.
func run(repo string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: docsite <folder>")
		return 2
	}
	if err := build(repo, args[0]); err != nil {
		fmt.Fprintln(stderr, "docsite:", err)
		return 1
	}
	index, _ := filepath.Abs(filepath.Join(args[0], "index.html"))
	fmt.Fprintln(stdout, "docsite: open", index)
	return 0
}

// build writes the site of the repository at repo into out, in place of what out held.
func build(repo, out string) error {
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return os.CopyFS(out, docsite.FS(os.DirFS(filepath.Join(repo, "docs", "site"))))
}
