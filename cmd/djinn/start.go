package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/server"
)

const (
	// LogFile receives what a djinn up started in the background writes, in the data directory.
	LogFile = "djinn.log"
	// startTimeout is how long a djinn up started in the background has to answer.
	startTimeout = 30 * time.Second
)

// startDetached starts djinn up in the background, apart from this process and its terminal, its output in the log of
// the data directory, and returns its address once it answers. It needs no administrator rights.
func startDetached(ctx context.Context, home string, say io.Writer) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	logPath := filepath.Join(home, LogFile)
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer log.Close() // The child holds its own copy.
	before, _ := log.Seek(0, io.SeekEnd)
	cmd := exec.Command(exe, "up")
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
			return "", fmt.Errorf("djinn up stopped at once (%v): %s", err, tail(logPath, before))
		case <-deadline.C:
			return "", fmt.Errorf("djinn up does not answer after %s: see %s", startTimeout, logPath)
		case <-tick.C:
		}
	}
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
