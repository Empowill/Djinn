// Command swapexe puts a freshly built executable in place of an installed one, in one rename, without disturbing a
// copy that runs: it is how `task install` updates the Djinn in use. The running copy keeps its old file, and notices
// the new one at its path (see cmd/djinn/update.go). The work is in internal/swapexe, which djinn uses too.
package main

import (
	"fmt"
	"os"

	"github.com/empowill/djinn/internal/swapexe"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: swapexe <new executable> <installed executable>")
		os.Exit(2)
	}
	if err := swapexe.Install(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "swapexe:", err)
		os.Exit(1)
	}
}
