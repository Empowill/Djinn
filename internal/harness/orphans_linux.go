package harness

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// dieWithParent has the kernel kill the process as soon as its parent ends (PR_SET_PDEATHSIG): a worker, a watcher's
// command or a check dies with the djinn up that started it, even a killed one. The kernel ties it to the thread that
// started the process: Go never ends a thread but one locked to a goroutine that ends, and Djinn starts none from
// such a goroutine. The setting survives the exec of nice and systemd-run, which set no user ID.
func dieWithParent(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }

// bootID is the identity of the system's boot: a start time counts from it.
var bootID = sync.OnceValue(func() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
})

// procStart is when the system started pid: its boot and the clock ticks since (field 22 of /proc/<pid>/stat).
func procStart(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// The command name, in parentheses, may hold spaces: the fields count from its end.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", errors.New("unexpected stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", errors.New("unexpected stat")
	}
	if f[0] == "Z" || f[0] == "X" {
		return "", errors.New("the process has ended") // A zombie runs nothing.
	}
	return bootID() + ":" + f[19], nil
}
