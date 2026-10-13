//go:build !windows

package harness

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
)

// groupOf is the process group of pid, 0 when unknown or when it is Djinn's own: a gate's command runs in the group
// of the worker's shell, never in Djinn's.
func groupOf(pid int, _ bool) int {
	g, err := syscall.Getpgid(pid)
	if err != nil || g <= 1 || g == syscall.Getpgrp() {
		return 0
	}
	return g
}

// stopChild asks a child of a previous djinn up to stop, or kills it: its process group while the child still runs
// and leads it, or the child alone; and every process of its cgroup, a djinn scope's. A group is signalled only while
// the child, the same process, is in it: a group whose processes all ended may be another's now.
func stopChild(c child, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	if same(identity{c.PID, c.Start}) {
		if g, err := syscall.Getpgid(c.PID); err == nil && c.Group > 1 && g == c.Group {
			_ = syscall.Kill(-c.Group, sig)
			_ = syscall.Kill(-c.Group, syscall.SIGCONT) // A paused worker gets the request once it goes on.
		} else {
			_ = syscall.Kill(c.PID, sig)
		}
	}
	if !djinnScope(c.Cgroup) {
		return
	}
	if kill {
		_ = machine.KillCgroup(c.Cgroup)
		return
	}
	pids, _ := machine.CgroupPIDs(c.Cgroup)
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
	_ = machine.FreezeCgroup(c.Cgroup, false)
}

// djinnScope says whether cgroup is the folder of a worker's scope, the only cgroups a record may name: the unit of
// each is unique (Scopes.New), so it holds only what that worker started.
func djinnScope(cgroup string) bool {
	return strings.HasPrefix(cgroup, "/sys/fs/cgroup/") && !strings.Contains(cgroup, "/../") &&
		strings.HasPrefix(cgroup[strings.LastIndex(cgroup, "/")+1:], "djinn-") && strings.HasSuffix(cgroup, ".scope")
}

// sentinelScript is the sentinel's: it reads, on its input, "+ <pid> <group> <cgroup>" as Djinn starts a child and
// "- <pid>" as it ends. Its input ends when Djinn ends, however it ended, the kernel closing Djinn's end of the pipe:
// it then asks every child left, its process group and its cgroup to stop, and kills them after the grace delay, its
// first argument, in seconds. It ignores the signals of a terminal: it runs in a session of its own.
const sentinelScript = `trap '' INT HUP
p=
while read -r op pid grp cg; do
	case $op in
	+) p="$p $pid"; eval "g$pid=\$grp c$pid=\$cg" ;;
	-) n=; for x in $p; do [ "$x" = "$pid" ] || n="$n $x"; done; p=$n; unset "g$pid" "c$pid" ;;
	esac
done
[ -n "$p" ] || exit 0
stop() {
	for x in $p; do
		eval "grp=\$g$x cg=\$c$x"
		if [ "$grp" -gt 1 ] 2>/dev/null; then
			kill -s "$1" -- "-$grp" 2>/dev/null
			kill -s CONT -- "-$grp" 2>/dev/null
		fi
		kill -s "$1" "$x" 2>/dev/null
		if [ -n "$cg" ] && [ -r "$cg/cgroup.procs" ]; then
			if [ "$1" = KILL ] && [ -w "$cg/cgroup.kill" ]; then
				echo 1 > "$cg/cgroup.kill"
			else
				kill -s "$1" $(cat "$cg/cgroup.procs") 2>/dev/null
			fi
		fi
	done
}
stop TERM
sleep "$1"
stop KILL
`

// sentinel is the guard on Unix: a shell of its own, holding the other end of a pipe Djinn writes to (sentinelScript).
// Djinn's death closes the pipe, whatever killed it: the sentinel then stops what Djinn started, on Linux what
// PR_SET_PDEATHSIG left (what the workers started), on macOS everything.
type sentinel struct {
	mu sync.Mutex
	w  io.WriteCloser
}

// startGuard starts the sentinel, with the grace delay its children get between the request to stop and the kill.
func startGuard(grace time.Duration) (guard, error) {
	secs := int((grace + time.Second - 1) / time.Second)
	cmd := exec.Command("/bin/sh", "-c", sentinelScript, "djinn-sentinel", strconv.Itoa(max(secs, 1)))
	// A session of its own: a Ctrl+C in Djinn's terminal, or a signal to Djinn's group, leaves it be.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	logx.L().Info("sentinel started", "pid", cmd.Process.Pid)
	go cmd.Wait() //nolint:errcheck // It ends once Djinn's end of the pipe closes; nothing to tell.
	return &sentinel{w: w}, nil
}

func (s *sentinel) sayf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.w, format, args...); err != nil {
		logx.L().Warn("sentinel not told", "err", err)
	}
}

func (s *sentinel) add(c child) {
	s.sayf("+ %d %d %s\n", c.PID, c.Group, c.Cgroup)
}

func (s *sentinel) remove(c child) { s.sayf("- %d\n", c.PID) }

func (s *sentinel) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.w.Close()
}
