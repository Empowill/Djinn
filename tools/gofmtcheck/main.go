// Command gofmtcheck fails when a Go file under the given folders is not formatted by gofmt, and lists them: a
// portable check for the lint task, with no shell tool.
//
//	go run ./tools/gofmtcheck cmd internal tools
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var bad []string
	for _, root := range os.Args[1:] {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out, err := format.Source(src)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if !bytes.Equal(src, out) {
				bad = append(bad, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if len(bad) > 0 {
		fmt.Fprintln(os.Stderr, "not formatted (go tool task format):")
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "  "+b)
		}
		os.Exit(1)
	}
}
