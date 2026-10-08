// Command openapi writes the OpenAPI document of the public methods of Djinn, from the protos the command line
// embeds: go run ./tools/openapi docs/openapi.json. `go tool task gen` runs it.
package main

import (
	"fmt"
	"os"

	"github.com/empowill/djinn/internal/cli"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: openapi <file>")
		os.Exit(2)
	}
	b, err := cli.OpenAPI()
	if err == nil {
		err = os.WriteFile(os.Args[1], b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
}
