// Command docsite builds the documentation site into a folder that opens offline: docs/site, its Command line tab
// filled in from the command tree djinn is built from. `go tool task docs` runs it: go run ./tools/docsite bin/docs.
// djinn up serves the same site at /docs/.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/empowill/djinn/internal/docsite"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: docsite <folder>")
		os.Exit(2)
	}
	if err := build(".", os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
	index, _ := filepath.Abs(filepath.Join(os.Args[1], "index.html"))
	fmt.Println("docsite: open", index)
}

// build writes the site of the repository at repo into out, in place of what out held.
func build(repo, out string) error {
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return os.CopyFS(out, docsite.FS(os.DirFS(filepath.Join(repo, "docs", "site"))))
}
