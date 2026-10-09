package machine

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is MEMORYSTATUSEX, which x/sys/windows does not define.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// Read reads this machine as well as Windows allows without a performance counter: the cores and the memory. It
// gives no load and no pressure.
func Read() (Snapshot, error) {
	s := Snapshot{Cores: runtime.NumCPU()}
	m := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return s, err
	}
	s.MemoryTotal, s.MemoryAvailable = m.totalPhys, m.availPhys
	return s, nil
}

// readGPUs reads no GPU on Windows yet.
func readGPUs() []GPU { return nil }
