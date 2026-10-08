//go:build !windows

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// unixProc is a program leading a session of its own, whose controlling terminal is a Unix pseudo-terminal.
type unixProc struct {
	master *os.File
	cmd    *exec.Cmd
	once   sync.Once
}

func start(command []string, dir string, env []string, cols, rows int) (proc, error) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	ptm, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer tty.Close() // The program holds its own copy: the master reads EOF once the program and its children end.
	master, err := nonBlocking(ptm)
	if err != nil {
		return nil, err
	}
	p := &unixProc{master: master}
	if err := p.resize(cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	cmd := exec.Command(path, command[1:]...)
	cmd.Args[0] = command[0]
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// A session of its own whose controlling terminal is tty: job control works, and a hangup reaches the whole
	// group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	p.cmd = cmd
	return p, nil
}

// nonBlocking returns the master side in non-blocking mode, on Go's poller, so that closing it ends a pending
// read: pty.Open leaves it blocking.
func nonBlocking(ptm *os.File) (*os.File, error) {
	defer ptm.Close()
	fd, err := unix.Dup(int(ptm.Fd()))
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), ptm.Name()), nil
}

func (p *unixProc) Read(b []byte) (int, error) {
	n, err := p.master.Read(b)
	if errors.Is(err, syscall.EIO) {
		err = os.ErrClosed // Linux: no process holds the terminal any more.
	}
	return n, err
}

func (p *unixProc) Write(b []byte) (int, error) { return p.master.Write(b) }

func (p *unixProc) resize(cols, rows int) error {
	conn, err := p.master.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	if err := conn.Control(func(fd uintptr) {
		ioErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	}); err != nil {
		return err
	}
	return ioErr
}

func (p *unixProc) wait() int {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode() // -1 when a signal killed it
	}
	return -1
}

// hangup sends SIGHUP to the program's group and to the group in the foreground of the terminal, which a shell's
// job control puts elsewhere: what a terminal that closes does.
func (p *unixProc) hangup() { p.signal(syscall.SIGHUP) }

func (p *unixProc) kill() { p.signal(syscall.SIGKILL) }

func (p *unixProc) signal(sig syscall.Signal) {
	if conn, err := p.master.SyscallConn(); err == nil {
		_ = conn.Control(func(fd uintptr) {
			if fg, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil && fg > 0 {
				_ = syscall.Kill(-fg, sig)
			}
		})
	}
	// The group may be gone already: nothing to do then.
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}

func (p *unixProc) close() { p.once.Do(func() { p.master.Close() }) }

// Shell is the user's shell, as a terminal application starts it: $SHELL, a login shell on macOS as Terminal does
// (an app started from the Finder has a bare environment).
func Shell() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if runtime.GOOS == "darwin" {
		return []string{shell, "-l"}
	}
	return []string{shell}
}

// ShellCommand returns the command line that runs line through the user's shell; nil for an empty line.
func ShellCommand(line string) []string {
	if line == "" {
		return nil
	}
	return append(Shell(), "-c", line)
}
