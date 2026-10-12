package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/server"
)

// LogFile receives what a djinn up started in the background writes on its output, in the data directory: the
// window's toolkit, a panic. What djinn up does is logged beside it, in logx.File.
var LogFile = filepath.Join(logx.Dir, logx.Console)

const (
	// startTimeout is how long a djinn up started in the background has to answer.
	startTimeout = 30 * time.Second
	// oldLogFile is where LogFile was before W511, moved at the next start.
	oldLogFile = "djinn.log"
	// consoleKept is how many older LogFile a start keeps when it rolls it over past logx.MaxSize.
	consoleKept = 2
)

// startDetached starts djinn with args (up and its flags) in the background, apart from this process and its
// terminal, its output in the log of the data directory, and returns its address once it answers. It needs no
// administrator rights.
func startDetached(ctx context.Context, home string, say io.Writer, args ...string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	logPath, err := openConsole(home)
	if err != nil {
		return "", err
	}
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer log.Close() // The child holds its own copy.
	before, _ := log.Seek(0, io.SeekEnd)
	cmd := exec.Command(exe, args...)
	// The same data directory, whatever decided it here.
	cmd.Env = append(os.Environ(), "DJINN_HOME="+home)
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start djinn up: %w", err)
	}
	fmt.Fprintf(say, "djinn: started djinn up in the background (pid %d, log %s)\n", cmd.Process.Pid, logPath)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(startTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if addr, err := server.ReadAddr(home); err == nil && cli.Alive(addr) {
			if !hasWindow {
				fmt.Fprintln(say, "djinn: open", addr)
			}
			return addr, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-exited:
			return "", fmt.Errorf("djinn up stopped at once (%w): %s", err, tail(logPath, before))
		case <-deadline.C:
			return "", fmt.Errorf("djinn up does not answer after %s: see %s", startTimeout, logPath)
		case <-tick.C:
		}
	}
}

// openConsole makes the folder of LogFile in home and returns its path, after moving the log of before W511 there
// and rolling it over when it grew past logx.MaxSize: a background djinn up holds it open, so it rolls over at a start.
func openConsole(home string) (string, error) {
	path := filepath.Join(home, LogFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		os.Rename(filepath.Join(home, oldLogFile), path) //nolint:errcheck // None to move: a new data folder.
	}
	if info, err := os.Stat(path); err == nil && info.Size() > logx.MaxSize {
		if err := logx.Shift(path, consoleKept); err != nil {
			return "", err
		}
	}
	return path, nil
}

// tail is what the log received from offset from on, its last lines.
func tail(path string, from int64) string {
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) < from {
		return "see " + path
	}
	b = bytes.TrimSpace(b[from:])
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	if len(b) == 0 {
		return errors.New("no output").Error()
	}
	return string(b)
}
