package harness

import (
	"errors"
	"fmt"
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
