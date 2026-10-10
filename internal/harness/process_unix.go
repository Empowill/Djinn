//go:build !windows

package harness

import (
	"errors"
	"os/exec"
	"syscall"

	"github.com/empowill/djinn/internal/machine"
)

// ownGroup makes the process lead a group of its own: a signal to the group reaches what it started.
func ownGroup(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// applyPriority sets low CPU priority (nice 10) on the started process if lowPriority is true.
func applyPriority(cmd *exec.Cmd, lowPriority bool) {
	if !lowPriority || cmd.Process == nil {
		return
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
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
