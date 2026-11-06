package machine

import (
	"context"
	"encoding/binary"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// Read reads this machine through sysctl and vm_stat: the memory, the load, and the system's memory pressure level.
// macOS has no PSI.
func Read() (Snapshot, error) {
	s := Snapshot{Cores: runtime.NumCPU()}
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return s, err
	}
	s.MemoryTotal = total
	s.MemoryAvailable = darwinAvailable(total, vmStat(), unix.SysctlUint32)
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

// vmStat runs vm_stat, for at most 2 seconds: the wired and compressed pages sysctl does not give. Nil when it fails.
func vmStat() []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
	if err != nil {
		return nil
	}
	return out
}
