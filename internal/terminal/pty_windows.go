//go:build windows

package terminal

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

// winProc is a program attached to a Windows pseudo-console (ConPTY, Windows 10 1809 and later).
type winProc struct {
	*conpty.ConPty
	process *os.Process
	once    sync.Once
}

func start(command []string, dir string, env []string, cols, rows int) (proc, error) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	c, err := conpty.New(cols, rows, 0)
	if err != nil {
		return nil, err
	}
	pid, handle, err := c.Spawn(path, command, &syscall.ProcAttr{Dir: dir, Env: env})
	if err != nil {
		c.Close()
		return nil, err
	}
	defer windows.CloseHandle(windows.Handle(handle)) //nolint:errcheck // FindProcess holds a handle of its own.
	process, err := os.FindProcess(pid)
	if err != nil {
		_ = windows.TerminateProcess(windows.Handle(handle), 1)
		c.Close()
		return nil, err
	}
	return &winProc{ConPty: c, process: process}, nil
}

func (p *winProc) resize(cols, rows int) error { return p.Resize(cols, rows) }

// wait uses the process itself: exec.Cmd.Wait does not work with a pseudo-console.
func (p *winProc) wait() int {
	state, err := p.process.Wait()
	if err != nil {
		return -1
	}
	return state.ExitCode()
}

// hangup closes the pseudo-console, which sends its programs CTRL_CLOSE_EVENT.
func (p *winProc) hangup() { p.close() }

func (p *winProc) kill() { _ = p.process.Kill() }

func (p *winProc) close() { p.once.Do(func() { p.ConPty.Close() }) }

// Shell is PowerShell when found, else the command interpreter.
func Shell() []string {
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		return []string{"powershell.exe"}
	}
	return []string{comspec()}
}

// ShellCommand returns the command line that runs line through the command interpreter; nil for an empty line.
func ShellCommand(line string) []string {
	if line == "" {
		return nil
	}
	return []string{comspec(), "/c", line}
}

func comspec() string {
	if c := os.Getenv("COMSPEC"); c != "" && strings.HasSuffix(strings.ToLower(c), ".exe") {
		return c
	}
	return "cmd.exe"
}
