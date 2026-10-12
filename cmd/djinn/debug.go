package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

// dumpWait is how long djinn debug dump waits for the server's answer, then for the dump its request file asks.
var dumpWait = 10 * time.Second

// runDebugDump is djinn debug dump: the server writes its goroutines to its log folder, asked by a call or, when it
// does not answer, by a request file (W511).
func runDebugDump(args []string) int {
	var addr string
	flags := cli.DebugDump.FlagSet(map[string]any{"addr": &addr})
	flags.SetOutput(os.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "djinn debug dump: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	home, err := ui.Home()
	if err != nil {
		fmt.Fprintln(os.Stderr, "djinn debug dump:", err)
		return 1
	}
	if addr == "" {
		addr, _ = server.ReadAddr(home)
	}
	path, err := debugDump(context.Background(), home, addr, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "djinn debug dump:", err)
		return 1
	}
	fmt.Println(path)
	fmt.Fprintf(os.Stderr, "djinn: send it with the log, %s\n", filepath.Join(logx.Folder(home), logx.File))
	return 0
}

// debugDump asks the djinn at addr for a dump of its goroutines and returns its path; when the call fails, it leaves
// the request file in the log folder of home and waits for the dump it asks. It says why on say.
func debugDump(ctx context.Context, home, addr string, say io.Writer) (string, error) {
	if addr != "" {
		path, err := dumpCall(ctx, addr)
		if err == nil {
			return path, nil
		}
		fmt.Fprintf(say, "djinn: the server does not answer (%v): asking through %s\n", err, logx.DumpRequest)
	}
	dir := logx.Folder(home)
	before := logx.Dumps(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	request := filepath.Join(dir, logx.DumpRequest)
	if err := os.WriteFile(request, nil, 0o600); err != nil {
		return "", err
	}
	deadline := time.Now().Add(dumpWait)
	for time.Now().Before(deadline) {
		for _, d := range logx.Dumps(dir) {
			if !slices.Contains(before, d) {
				return d, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.Remove(request)
	return "", fmt.Errorf("no djinn up dumped its goroutines within %s: is one running on %s? On Linux and macOS, "+
		"kill -USR2 <pid> asks it too", dumpWait, home)
}

// dumpCall asks the server at addr for a dump, and returns its path.
func dumpCall(ctx context.Context, addr string) (string, error) {
	client, base, err := cli.Dial(addr)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, dumpWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+server.DumpPath, nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	return strings.TrimSpace(string(b)), nil
}
