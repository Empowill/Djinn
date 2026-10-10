// Command copydir copies a directory tree. It is a build tool for `task release`, kept apart from the
// djinn command, and portable where `cp -r` is not (Windows).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: copydir <from> <to>")
		os.Exit(2)
	}
	if err := os.CopyFS(os.Args[2], os.DirFS(os.Args[1])); err != nil {
		fmt.Fprintln(os.Stderr, "copydir:", err)
		os.Exit(1)
	}
}
