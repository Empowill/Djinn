package harness

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Grace is how long a worker asked to stop has before it is killed.
const Grace = 5 * time.Second

// process is a worker's process: its output read line by line, stopped in two steps.
type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	grace time.Duration
	// lines are the lines of the output, then of the error output, as they come; closed once both end.
	lines chan line
	done  chan struct{} // closed when the process has ended and its output is read
	res   Result
	stop  sync.Once
}

type line struct {
	text   string
	stderr bool
}

// startProcess starts name in dir with args, under prefix when not empty. The process gets Djinn's environment
// plus env, which Djinn passes on without reading. On Unix it leads a process group of its own, so that stopping it
// stops what it started.
func startProcess(dir, name string, args, env, prefix []string, grace time.Duration) (*process, error) {
	if len(prefix) > 0 {
		// The prefix runs the program found here, and a missing one fails here, as without a prefix.
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%s not found in PATH", name)
		}
		name, args = prefix[0], append(append(slices.Clone(prefix[1:]), path), args...)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	ownGroup(cmd)
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
	p := &process{cmd: cmd, stdin: stdin, grace: grace, lines: make(chan line, 64), done: make(chan struct{})}
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
		if s = strings.TrimRight(s, "\r\n"); s != "" {
			p.lines <- line{text: s, stderr: stderr}
		}
		if err != nil {
			// Drain what is left so the copy into the pipe never blocks the process.
			_, _ = io.Copy(io.Discard, r)
			return
		}
	}
}

// Stop asks the process to stop, then kills it, and what it started, after the grace delay.
func (p *process) Stop() {
	p.stop.Do(func() {
		terminate(p.cmd)
		go func() {
			select {
			case <-p.done:
			case <-time.After(p.grace):
			}
			kill(p.cmd)
		}()
	})
}

// Pause stops the process, and what it started, where they are.
func (p *process) Pause() error { return pause(p.cmd) }

// Resume lets them go on.
func (p *process) Resume() error { return resume(p.cmd) }

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
