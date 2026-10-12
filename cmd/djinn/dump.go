package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

// dumpFile is the name, in the data folder, of the goroutine dump of a djinn up that was asked for one (W504): a frozen
// djinn tells where each goroutine waits without being stopped.
const dumpFile = "goroutines.txt"

// dumpGoroutines writes a dump of every goroutine to dumpFile in home, each time dump receives, until ctx ends, and
// says where on say.
func dumpGoroutines(ctx context.Context, home string, dump <-chan os.Signal, say io.Writer) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-dump:
		}
		path := filepath.Join(home, dumpFile)
		if err := writeGoroutines(path); err != nil {
			fmt.Fprintln(say, "djinn: dump the goroutines:", err)
			continue
		}
		fmt.Fprintf(say, "%s djinn: goroutines written to %s\n", time.Now().Format(time.RFC3339), path)
	}
}

// writeGoroutines writes the stacks of every goroutine to path, in the form of a panic, with how long each waits.
func writeGoroutines(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
