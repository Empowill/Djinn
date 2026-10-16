package harness

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
)

// dieWithParent does nothing on macOS, which has no PR_SET_PDEATHSIG: the sentinel, which sees Djinn end, stops
// the children.
func dieWithParent(*syscall.SysProcAttr) {}

// procStart is when the system started pid, to the microsecond (kinfo_proc's p_starttime).
func procStart(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(kp.Proc.P_pid) != pid {
		return "", errors.New("no such process")
	}
	if kp.Proc.P_stat == 5 { // SZOMB: a zombie runs nothing.
		return "", errors.New("the process has ended")
	}
	t := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), nil
}

// procsOf lists the processes of Djinn's user that run, Djinn aside, started since since (a procStart; empty or
// unknown: whenever), in the process group group (0: none) or whose environment holds entry, a whole NAME=value
// (empty: none): the one each process started with (kern.procargs2).
func procsOf(since string, group int, entry string) []identity {
	if group <= 1 && entry == "" {
		return nil
	}
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	self, uid := os.Getpid(), uint32(os.Geteuid())
	var ids []identity
	for _, kp := range kps {
		pid := int(kp.Proc.P_pid)
		if pid <= 1 || pid == self || kp.Proc.P_stat == 5 || kp.Eproc.Ucred.Uid != uid {
			continue
		}
		t := kp.Proc.P_starttime
		start := fmt.Sprintf("%d.%06d", t.Sec, t.Usec)
		if !startedSince(start, since) {
			continue
		}
		if (group > 1 && int(kp.Eproc.Pgid) == group) || (entry != "" && hasEntry(pid, entry)) {
			ids = append(ids, identity{pid: pid, start: start})
		}
	}
	return ids
}

// startedSince says whether start, a procStart, is since or after since; true when since is empty or unknown.
func startedSince(start, since string) bool {
	var s1, u1, s2, u2 int64
	if _, err := fmt.Sscanf(since, "%d.%d", &s1, &u1); err != nil {
		return true
	}
	if _, err := fmt.Sscanf(start, "%d.%d", &s2, &u2); err != nil {
		return true
	}
	return s2 > s1 || (s2 == s1 && u2 >= u1)
}

// hasEntry says whether the environment pid started with holds entry. kern.procargs2 gives its argument count, its
// executable's path, its arguments, then its environment, each string ending with a NUL: an entry is a whole string
// of it.
func hasEntry(pid int, entry string) bool {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return false
	}
	return slices.ContainsFunc(bytes.Split(b[4:], []byte{0}), func(s []byte) bool { return string(s) == entry })
}

// holdExit is false: macOS has no waitid with WNOWAIT in Go. What a worker left in its group is found by its mark only.
func holdExit(*exec.Cmd) bool { return false }
