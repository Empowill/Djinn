// Command bigwish writes a wish of real size (internal/testx/bigwish) to a file, as djinn wish export writes one:
// djinn wish import reads it into any djinn, the e2e's first (e2e/bigwish.ts).
//
//	go run ./tools/bigwish -size real -o /tmp/real.djinn   # the developer's wish today
//	go run ./tools/bigwish -size x10 -o /tmp/x10.json       # ten times over, as JSON to read
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/empowill/djinn/internal/testx/bigwish"
)

func main() {
	name := flag.String("size", bigwish.Real.Name, "the size: real or x10")
	out := flag.String("o", "", "the file to write; a name ending in .json is written as JSON, any other as binary protobuf")
	flag.Parse()
	if err := run(*name, *out); err != nil {
		fmt.Fprintln(os.Stderr, "bigwish:", err)
		os.Exit(1)
	}
}

func run(name, out string) error {
	size, err := bigwish.Named(name)
	if err != nil {
		return err
	}
	if out == "" {
		return fmt.Errorf("-o is required")
	}
	var data []byte
	if strings.EqualFold(filepath.Ext(out), ".json") {
		data, err = protojson.MarshalOptions{Multiline: true, Indent: "  ", UseProtoNames: true}.Marshal(bigwish.Make(size))
	} else {
		data, err = bigwish.Data(size)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return err
	}
	fmt.Printf("%s: wish %s, %d bytes\n", out, bigwish.WishID, len(data))
	return nil
}
