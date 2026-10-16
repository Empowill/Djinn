//go:build windows

package harness

import (
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// listeners lists the TCP ports that the processes pids listen on, but exclude's, on the loopback or every interface:
// the tables of GetExtendedTcpTable, each socket with its process. Untested: no Windows machine runs these tests yet.
func listeners(pids, exclude []int) []listener {
	want := map[int]bool{}
	for _, pid := range pids {
		if !slices.Contains(exclude, pid) {
			want[pid] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	const ownerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER
	var found []listener
	for _, family := range []uintptr{windows.AF_INET, windows.AF_INET6} {
		size := uint32(16 << 10)
		for range 3 { // The table may grow between two calls.
			buf := make([]byte, size)
			r, _, _ := getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, family, ownerPIDListener, 0)
			if r == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
				continue
			}
			if r == 0 {
				found = parseTCPTable(buf[:size], family == windows.AF_INET6, want, found)
			}
			break
		}
	}
	return found
}

// servePIDs lists the processes of the serve p: those its job holds, or p alone without one.
func servePIDs(p *process) []int {
	jobs.Lock()
	job, ok := jobs.of[p]
	jobs.Unlock()
	if ok {
		// JOBOBJECT_BASIC_PROCESS_ID_LIST: two counts, then the IDs, each the size of a pointer.
		const most = 1024
		word := int(unsafe.Sizeof(uintptr(0)))
		buf := make([]byte, 2*4+most*word)
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil) == nil {
			n := int(*(*uint32)(unsafe.Pointer(&buf[4])))
			pids := make([]int, 0, n)
			for i := range min(n, most) {
				pids = append(pids, int(*(*uintptr)(unsafe.Pointer(&buf[8+i*word]))))
			}
			return pids
		}
	}
	if p.cmd.Process != nil {
		return []int{p.cmd.Process.Pid}
	}
	return nil
}
