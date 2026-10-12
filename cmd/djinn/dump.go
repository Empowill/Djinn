package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// dumpPoll is how often djinn up looks for logx.DumpRequest in its log folder.
var dumpPoll = time.Second

// dumpGoroutines writes a dump of every goroutine to the log folder of home (logx.Dump), each time dump receives (a
// signal, W504) or logx.DumpRequest appears there (any system, W511), until ctx ends, and says where on say: a frozen
// djinn tells where each goroutine waits without being stopped.
func dumpGoroutines(ctx context.Context, home string, dump <-chan os.Signal, say io.Writer) {
	dir := logx.Folder(home)
	request := filepath.Join(dir, logx.DumpRequest)
	tick := time.NewTicker(dumpPoll)
	defer tick.Stop()
	for {
		why := "asked by a signal"
		select {
		case <-ctx.Done():
			return
		case <-dump:
		case <-tick.C:
			if err := os.Remove(request); errors.Is(err, fs.ErrNotExist) {
				continue
			}
			why = "asked by " + logx.DumpRequest
		}
		path, err := logx.Dump(dir, why)
		if err != nil {
			fmt.Fprintln(say, "djinn: dump the goroutines:", err)
			continue
		}
		fmt.Fprintf(say, "%s djinn: goroutines written to %s\n", time.Now().Format(time.RFC3339), path)
	}
}
