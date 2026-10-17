//go:build windows

package harness

import (
	"errors"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// groupOf is 0: Windows has no process groups.
func groupOf(int, bool) int { return 0 }

// stopChild kills a child of a previous djinn up while it still runs, the same process: Windows has no signal to ask
// a console process to stop.
func stopChild(c child, _ bool) {
	if !same(identity{c.PID, c.Start}) {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(c.PID))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}

// procStart is when the system created pid, in 100 ns since 1601 (GetProcessTimes); an error once it has ended.
func procStart(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return "", err
	}
	if code != stillActive {
		return "", errors.New("the process has ended")
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), nil
}

// stillActive is the exit code of a process that runs (STILL_ACTIVE).
const stillActive = 259

// job is the guard on Windows: a job object that kills its processes when its last handle closes
// (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE). Only Djinn holds it: its end closes it, however it ended, and what Djinn's
// children start joins the job with them.
type job struct {
	mu sync.Mutex
	h  windows.Handle
}

func startGuard(time.Duration) (guard, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &job{h: h}, nil
}

// add puts the child in the job, as soon as it started: what it starts from then on joins the job too.
func (j *job) add(c child) {
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.PID))
	if err != nil {
		return
	}
	defer windows.CloseHandle(p)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.h != 0 {
		_ = windows.AssignProcessToJobObject(j.h, p)
	}
}

func (j *job) remove(child) {}

// close closes the job: what is left in it dies.
func (j *job) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.h != 0 {
		_ = windows.CloseHandle(j.h)
		j.h = 0
	}
}

// procsOf finds no process on Windows: a worker's job holds what it started (adopt).
func procsOf(string, int, string) []identity { return nil }
