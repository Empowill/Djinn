package harness

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
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
	start, _, err := procStat(pid)
	return start, err
}

// procStat is when the system started pid (procStart) and its process group (field 5 of /proc/<pid>/stat); an error
// once it has ended.
func procStat(pid int) (start string, group int, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, err
	}
	// The command name, in parentheses, may hold spaces: the fields count from its end.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", 0, errors.New("unexpected stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", 0, errors.New("unexpected stat")
	}
	if f[0] == "Z" || f[0] == "X" {
		return "", 0, errors.New("the process has ended") // A zombie runs nothing.
	}
	group, _ = strconv.Atoi(f[2])
	return bootID() + ":" + f[19], group, nil
}

// procsOf lists the processes that run, Djinn aside, started since since (a procStart; empty or unknown: whenever),
// in the process group group (0: none) or whose environment holds entry, a whole NAME=value (empty: none). The
// environment is the one each process started with (/proc/<pid>/environ), readable only for the processes of Djinn's
// user that let it be read: the others are never in the list. What a process started, started after it: since spares
// reading the environment of every older process.
func procsOf(since string, group int, entry string) []identity {
	if group <= 1 && entry == "" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var ids []identity
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		start, g, err := procStat(pid)
		if err != nil || !startedSince(start, since) {
			continue
		}
		if (group > 1 && g == group) || (entry != "" && hasEntry(pid, entry)) {
			ids = append(ids, identity{pid: pid, start: start})
		}
	}
	return ids
}

// startedSince says whether start, a procStart, is since or after since; true when since is empty or unknown.
func startedSince(start, since string) bool {
	i, j := strings.LastIndexByte(since, ':'), strings.LastIndexByte(start, ':')
	if i < 0 || j < 0 {
		return true
	}
	from, err1 := strconv.ParseUint(since[i+1:], 10, 64)
	at, err2 := strconv.ParseUint(start[j+1:], 10, 64)
	if err1 != nil || err2 != nil {
		return true
	}
	// Since another boot: start, of this one, is after it.
	return since[:i] != start[:j] || at >= from
}

// hasEntry says whether the environment pid started with holds entry.
func hasEntry(pid int, entry string) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	return err == nil && slices.Contains(strings.Split(string(b), "\x00"), entry)
}

// holdExit waits for the process to end and leaves it unreaped, a zombie (waitid with WNOWAIT): its PID, and the ID of
// the group it leads, stay its own until cmd.Wait reaps it, so what is left in its group is told from any later
// process. False when it could not wait so.
func holdExit(cmd *exec.Cmd) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err == nil
		}
	}
}
