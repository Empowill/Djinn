package machine

import (
	"encoding/binary"
	"runtime"

	"golang.org/x/sys/unix"
)

// Read reads this machine through sysctl: the memory, the load, and the system's memory pressure level. macOS has
// no PSI.
func Read() (Snapshot, error) {
	s := Snapshot{Cores: runtime.NumCPU()}
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return s, err
	}
	s.MemoryTotal = total
	if page, err := unix.SysctlUint32("hw.pagesize"); err == nil {
		// Free, speculative and purgeable pages: what a new program gets without paging. It leaves out the
		// inactive pages, so it is low; the pressure level is what decides.
		var pages uint64
		for _, name := range []string{"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count"} {
			if n, err := unix.SysctlUint32(name); err == nil {
				pages += uint64(n)
			}
		}
		s.MemoryAvailable = pages * uint64(page)
	}
	if level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		s.MemoryLevel = int(level)
	}
	// struct loadavg { fixpt_t ldavg[3]; long fscale; }: three uint32, padding, then a 64-bit scale.
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil && len(raw) >= 24 {
		scale := binary.LittleEndian.Uint64(raw[16:24])
		if scale > 0 {
			s.Load1, s.LoadKnown = float64(binary.LittleEndian.Uint32(raw[0:4]))/float64(scale), true
		}
	}
	return s, nil
}

// readGPUs gives the GPU of an Apple Silicon chip, which shares the machine's memory. An Intel Mac's GPUs are not
// read: system_profiler takes seconds.
func readGPUs() []GPU {
	if runtime.GOARCH != "arm64" {
		return nil
	}
	chip, _ := unix.Sysctl("machdep.cpu.brand_string")
	memory, _ := unix.SysctlUint64("hw.memsize")
	return []GPU{AppleGPU(chip, memory)}
}
