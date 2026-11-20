package machine

import (
	"sync"
	"syscall"
	"unsafe"
)

// The libSystem functions below are called as golang.org/x/sys/unix calls its own, without cgo: an assembly
// trampoline jumps to the dynamic symbol (libsystem_darwin_*.s), run through the runtime's syscall. The tests, the
// macOS CI and the browser build have no cgo; this reads the same in every build.

//go:cgo_import_dynamic libc_proc_pid_rusage proc_pid_rusage "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"

// The trampolines' addresses, set by the assembly.
var procPidRusageAddr, hostStatistics64Addr, machHostSelfAddr uintptr

// libcCall and libcCall6 call the C function at fn: the runtime's own (runtime/sys_darwin.go), as x/sys/unix does.
// errno is set only when the result is -1 in 64 bits: a C int result is read by its low 32 bits instead.
//
//go:linkname libcCall syscall.syscall
func libcCall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname libcCall6 syscall.syscall6
func libcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

// rusageInfoV4 is struct rusage_info_v4 of XNU's bsd/sys/resource.h: ri_uuid, then 35 counters of 64 bits, from
// ri_user_time to ri_runnable_time. proc_pid_rusage(3) fills it for RUSAGE_INFO_V4 (libproc.h); Apple's
// documentation of the task_vm_info and footprint: "Reducing your app's memory use" (phys_footprint is what Xcode's
// memory gauge and Activity Monitor's Memory column show).
type rusageInfoV4 struct {
	UUID     [16]byte
	Counters [35]uint64
}

// The counters read in rusageInfoV4, by their place after ri_uuid in bsd/sys/resource.h.
const (
	riResidentSize             = 6  // ri_resident_size: the resident pages, what ps rss reads
	riPhysFootprint            = 7  // ri_phys_footprint: anonymous, compressed and IOKit memory of the process itself
	riLifetimeMaxPhysFootprint = 28 // ri_lifetime_max_phys_footprint (V4): the most ri_phys_footprint reached
	rusageInfoV4Flavor         = 4  // RUSAGE_INFO_V4
)

// Footprint reads, through proc_pid_rusage, the memory process pid holds now (ri_phys_footprint: its anonymous memory,
// compressed or swapped included, without what it shares with others, the file cache and libraries), and the most it
// held since it started (ri_lifetime_max_phys_footprint), which the kernel keeps: no sampling misses it. ok is false
// when the process ended, belongs to another user, or the call fails.
func Footprint(pid int) (now, most uint64, ok bool) {
	if pid <= 0 {
		return 0, 0, false
	}
	var ri rusageInfoV4
	r, _, _ := libcCall(procPidRusageAddr, uintptr(pid), rusageInfoV4Flavor, uintptr(unsafe.Pointer(&ri)))
	if int32(r) != 0 {
		return 0, 0, false
	}
	now, most = ri.Counters[riPhysFootprint], ri.Counters[riLifetimeMaxPhysFootprint]
	return now, max(now, most), true
}

// vmStatistics64 is struct vm_statistics64 of XNU's osfmk/mach/vm_statistics.h, what host_statistics64 gives for
// HOST_VM_INFO64 and vm_stat prints: page counts, natural_t (32 bits) or 64 bits.
type vmStatistics64 struct {
	Free, Active, Inactive, Wire                                                         uint32
	ZeroFill, Reactivations, Pageins, Pageouts, Faults, CowFaults, Lookups, Hits, Purges uint64
	Purgeable, Speculative                                                               uint32
	Decompressions, Compressions, Swapins, Swapouts                                      uint64
	Compressor, Throttled, External, Internal                                            uint32
	TotalUncompressedInCompressor                                                        uint64
}

const (
	hostVMInfo64 = 4 // HOST_VM_INFO64, osfmk/mach/host_info.h
	// HOST_VM_INFO64_COUNT: the size of vm_statistics64 in integer_t.
	hostVMInfo64Count = uint32(unsafe.Sizeof(vmStatistics64{}) / 4)
)

// hostPort is mach_host_self(), asked once: each call adds a send right to the same port.
var hostPort = sync.OnceValue(func() uintptr {
	r, _, _ := libcCall(machHostSelfAddr, 0, 0, 0)
	return uintptr(uint32(r))
})

// hostVMCounts reads the machine's page counts through host_statistics64, as vm_stat does, under vm_stat's labels
// (system_cmds' vm_stat.c): "Anonymous pages" is internal_page_count, "Pages purgeable" purgeable_count, "Pages
// wired down" wire_count, "Pages occupied by compressor" compressor_page_count. filed is every page the kernel
// files in a queue (free, speculative among them, active, inactive, throttled, wired) or the compressor holds, for
// pagesCover. False when the call fails.
func hostVMCounts() (counts map[string]uint64, filed uint64, ok bool) {
	var vs vmStatistics64
	count := hostVMInfo64Count
	r, _, _ := libcCall6(hostStatistics64Addr, hostPort(), hostVMInfo64, uintptr(unsafe.Pointer(&vs)),
		uintptr(unsafe.Pointer(&count)), 0, 0)
	if int32(r) != 0 {
		return nil, 0, false
	}
	counts = map[string]uint64{
		"Anonymous pages":              uint64(vs.Internal),
		"Pages purgeable":              uint64(vs.Purgeable),
		"Pages wired down":             uint64(vs.Wire),
		"Pages occupied by compressor": uint64(vs.Compressor),
	}
	filed = uint64(vs.Free) + uint64(vs.Active) + uint64(vs.Inactive) + uint64(vs.Throttled) + uint64(vs.Wire) +
		uint64(vs.Compressor)
	return counts, filed, true
}
