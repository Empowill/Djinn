package harness

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
)

// Grace is how long a worker asked to stop has before it is killed.
const Grace = 5 * time.Second

// process is a worker's process: its output read line by line, stopped in two steps.
type process struct {
	cmd    *exec.Cmd
	cgroup string // the cgroup of its scope; empty without one
	stdin  io.WriteCloser
	grace  time.Duration
	// lines are the lines of the output, then of the error output, as they come; closed once both end.
	lines chan line
	done  chan struct{} // closed when the process has ended and its output is read
	res   Result
	stop  sync.Once
	// blanks keeps the empty lines of the output: an inbox source's item ends on one.
	blanks bool
}

type line struct {
	text   string
	stderr bool
}

// startProcess starts name in dir with args, in a scope of its own when scope is set. The process gets Djinn's
// environment plus env, which Djinn passes on without reading. On Unix it leads a process group of its own, so that
// stopping it stops what it started; in a scope, its cgroup holds even what left the group.
// When lowPriority is true, the process starts at low CPU priority (under nice 10 on Unix, BELOW_NORMAL_PRIORITY_CLASS
// on Windows); else at Djinn's own priority.
// When track is set, it is told of the process as it starts, and what it returns is called once the process ended.
func startProcess(
	dir, name string, args, env []string, scope func() machine.Scope, grace time.Duration, lowPriority bool,
	track func(pid int, cgroup string) func(),
) (*process, error) {
	return startCommand(dir, name, args, env, scope, grace, false, lowPriority, track)
}

// startCommand is startProcess; with blanks, the empty lines of the output are lines too.
func startCommand(
	dir, name string, args, env []string, scope func() machine.Scope, grace time.Duration, blanks bool, lowPriority bool,
	track func(pid int, cgroup string) func(),
) (*process, error) {
	var sc machine.Scope
	if scope != nil {
		sc = scope()
	}
	program := filepath.Base(name) // Logged without its arguments, which may hold a prompt.
	atLevel := false
	if lowPriority || len(sc.Prefix) > 0 {
		// The prefix, and nice at low priority, run the program found here, and a missing one fails here, as without
		// them.
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%s not found in PATH", name)
		}
		name = path
		if lowPriority {
			name, args, atLevel = lowCommand(name, args)
		}
		if prefix := sc.Prefix; len(prefix) > 0 {
			name, args = prefix[0], append(append(slices.Clone(prefix[1:]), name), args...)
		}
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	ownGroup(cmd, lowPriority)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout, cmd.Stderr = outW, errW
	// A child that outlives the process may hold its output open: Wait gives up on it after the grace delay.
	cmd.WaitDelay = grace
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s not found in PATH", name)
		}
		return nil, err
	}
	applyPriority(cmd, lowPriority && !atLevel)
	ended := func() {}
	if track != nil {
		ended = track(cmd.Process.Pid, sc.Cgroup)
	}
	started := time.Now()
	logx.L().Info("process started", "program", program, "pid", cmd.Process.Pid, "dir", dir)
	p := &process{cmd: cmd, cgroup: sc.Cgroup, stdin: stdin, grace: grace, lines: make(chan line, 64), done: make(chan struct{}), blanks: blanks}
	var read sync.WaitGroup
	read.Add(2)
	go p.scan(&read, outR, false)
	go p.scan(&read, errR, true)
	go func() {
		err := cmd.Wait()
		outW.Close()
		errW.Close()
		read.Wait()
		close(p.lines)
		p.res = exitResult(cmd, err)
		ended()
		logx.Took(slog.LevelInfo, "process ended", started, "program", program, "pid", cmd.Process.Pid,
			"exit", cmd.ProcessState.ExitCode())
		close(p.done)
	}()
	return p, nil
}

// scan sends each line of r. A line has no size limit: an agent may print a whole file in one.
func (p *process) scan(wg *sync.WaitGroup, r io.Reader, stderr bool) {
	defer wg.Done()
	br := bufio.NewReader(r)
	for {
		s, err := br.ReadString('\n')
		if s = strings.TrimRight(s, "\r\n"); s != "" || (p.blanks && err == nil) {
			p.lines <- line{text: s, stderr: stderr}
		}
		if err != nil {
			// Drain what is left so the copy into the pipe never blocks the process.
			_, _ = io.Copy(io.Discard, r)
			return
		}
	}
}

// Stop asks the process to stop, then kills it, and what it started, after the grace delay: in a scope, every
// process left in its cgroup.
func (p *process) Stop() {
	p.stop.Do(func() {
		terminate(p)
		go func() {
			select {
			case <-p.done:
			case <-time.After(p.grace):
			}
			kill(p)
		}()
	})
}

// Pause stops the process, and what it started, where they are.
func (p *process) Pause() error { return pause(p) }

// Resume lets them go on.
func (p *process) Resume() error { return resume(p) }

// SetPriority sets the process and its process tree priority.
func (p *process) SetPriority(low bool) error { return setPriority(p, low) }

func exitResult(cmd *exec.Cmd, err error) Result {
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // The process ended; a child kept its output open.
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Result{ExitCode: cmd.ProcessState.ExitCode()}
	case errors.As(err, &exit):
		return Result{ExitCode: exit.ExitCode()}
	}
	return Result{ExitCode: -1, Err: err}
}
