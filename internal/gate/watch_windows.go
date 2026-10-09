package gate

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// accounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION; its times count in 100 ns.
type accounting struct {
	TotalUserTime, TotalKernelTime                       int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime   int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses uint32
	TotalTerminatedProcesses                             uint32
}

// watch puts the command in a Job Object as soon as it started. Its children join the job, which counts the CPU
// time of them all and the peak memory of the largest; Windows gives a process's children no other way. A child the
// command starts before it joins, within microseconds, escapes. Without a job, only the command's own CPU time is
// known.
func watch(p *os.Process) func(*os.ProcessState) (cpu time.Duration, peak uint64) {
	own := func(ps *os.ProcessState) (time.Duration, uint64) { return ps.UserTime() + ps.SystemTime(), 0 }
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return own
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
	}
	if err != nil {
		_ = windows.CloseHandle(job)
		return own
	}
	return func(ps *os.ProcessState) (time.Duration, uint64) {
		// Closing the job leaves its processes alone: it has no kill-on-close limit.
		defer windows.CloseHandle(job)
		var acct accounting
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil) != nil ||
			windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
				uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil) != nil {
			return own(ps)
		}
		return time.Duration(acct.TotalUserTime+acct.TotalKernelTime) * 100, uint64(limits.PeakProcessMemoryUsed)
	}
}
