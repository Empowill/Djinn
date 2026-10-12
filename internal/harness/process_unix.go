//go:build !windows

package harness

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"

	"github.com/empowill/djinn/internal/machine"
)

var (
	setPriorityFn     = syscall.Setpriority
	setCgroupWeightFn = machine.SetCgroupWeight
	workerPIDsFn      = machine.WorkerPIDs
)

// ownGroup makes the process lead a group of its own: a signal to the group reaches what it started.
func ownGroup(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// lowNice is the nice level of a worker at low CPU priority, below the developer's applications (nice 0).
const lowNice = 10

// niceOf reads the nice level of pid, 0 for the calling process. Linux's raw getpriority returns 20 - nice; the
// other systems return the nice level itself.
func niceOf(pid int) (int, error) {
	prio, err := syscall.Getpriority(syscall.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "linux" {
		return 20 - prio, nil
	}
	return prio, nil
}

// workerNice is the nice level of a worker. At standard priority, Djinn's own: a worker runs as Djinn was started
// (nice djinn, a launcher at -10), and an unprivileged process cannot go below its own nice level anyway. At low
// priority, lowNice, or Djinn's own when Djinn already runs lower.
func workerNice(low bool) int {
	own, err := niceOf(0)
	if err != nil {
		own = 0
	}
	if low && own < lowNice {
		return lowNice
	}
	return own
}

// lowCommand runs path under nice(1) at a worker's low nice level: nice sets its own level, then execs the program,
// which starts at it. A setpriority once the process started is not enough: on macOS, a program that execs another at
// once (/bin/sh execs bash, env execs node) can lose it, and the low-priority sh of PR #2's CI ran on at nice -10, its
// child too. When Djinn already runs at that level or lower, path as it is, which inherits it. False when the program
// does not start at its level (Djinn's own unknown, no nice on PATH): applyPriority sets it then.
func lowCommand(path string, args []string) (string, []string, bool) {
	own, err := niceOf(0)
	if err != nil {
		return path, args, false
	}
	if own >= lowNice {
		return path, args, true
	}
	nice, err := exec.LookPath("nice")
	if err != nil {
		return path, args, false
	}
	return nice, append([]string{"-n", strconv.Itoa(lowNice - own), path}, args...), true
}

// applyPriority sets low CPU priority on the started process if lowPriority is true, when it did not start under nice
// (lowCommand); at standard priority, it runs at Djinn's own, which it inherited.
func applyPriority(cmd *exec.Cmd, lowPriority bool) {
	if !lowPriority || cmd.Process == nil {
		return
	}
	_ = setPriorityFn(syscall.PRIO_PROCESS, cmd.Process.Pid, workerNice(true))
}

// setPriority sets CPU priority on the running process and its process tree,
// and adjusts the systemd scope cpu.weight if a cgroup is configured.
func setPriority(p *process, low bool) error {
	if p.cmd.Process == nil {
		return errors.New("the process has not started")
	}
	var errs []error
	if p.cgroup != "" {
		weight := 100
		if low {
			weight = 10
		}
		if err := setCgroupWeightFn(p.cgroup, weight); err != nil {
			errs = append(errs, err)
		}
	}
	nice := workerNice(low)
	pids, err := workerPIDsFn(p.cmd.Process.Pid, p.cgroup)
	if err != nil && len(pids) == 0 {
		pids = []int{p.cmd.Process.Pid}
	}
	for _, pid := range pids {
		if err := setPriorityFn(syscall.PRIO_PROCESS, pid, nice); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// terminate asks the process group to stop, and in a scope every process of its cgroup that left the group. A
// paused worker gets the request once it goes on: it goes on at once.
func terminate(p *process) {
	signalGroup(p.cmd, syscall.SIGTERM)
	signalOthers(p, syscall.SIGTERM)
	signalGroup(p.cmd, syscall.SIGCONT)
	if p.cgroup != "" {
		_ = machine.FreezeCgroup(p.cgroup, false)
	}
}

// pause stops the worker where it is: in a scope, its whole cgroup frozen at once; else its process group, by
// SIGSTOP, which cannot be caught nor ignored.
func pause(p *process) error {
	if p.cgroup != "" && machine.FreezeCgroup(p.cgroup, true) == nil {
		return nil
	}
	return signalGroupErr(p.cmd, syscall.SIGSTOP)
}

// resume lets the worker go on, however it was paused.
func resume(p *process) error {
	err := signalGroupErr(p.cmd, syscall.SIGCONT)
	if p.cgroup != "" && machine.FreezeCgroup(p.cgroup, false) == nil {
		return nil
	}
	return err
}

// kill kills what is left of the process group, and in a scope every process of its cgroup.
func kill(p *process) {
	signalGroup(p.cmd, syscall.SIGKILL)
	if p.cgroup != "" && machine.KillCgroup(p.cgroup) != nil {
		signalOthers(p, syscall.SIGKILL)
	}
}

// signalOthers sends sig to every process of the worker's cgroup outside its process group: a tool started in a
// session of its own. Without a cgroup, or once it is gone, nothing.
func signalOthers(p *process, sig syscall.Signal) {
	if p.cgroup == "" || p.cmd.Process == nil {
		return
	}
	pids, _ := machine.CgroupPIDs(p.cgroup)
	for _, pid := range pids {
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid != p.cmd.Process.Pid {
			_ = syscall.Kill(pid, sig)
		}
	}
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	// The group may be gone already: nothing to do then.
	_ = signalGroupErr(cmd, sig)
}

// signalGroupErr sends sig to the process group, or to the process alone when the group is gone.
func signalGroupErr(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return errors.New("the process has not started")
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}
