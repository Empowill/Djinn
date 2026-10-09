package machine

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Read reads this machine: /proc for the memory, the load and the pressure.
func Read() (Snapshot, error) {
	s, err := ReadProc(os.DirFS("/"))
	s.Cores = runtime.NumCPU()
	return s, err
}

// readGPUs reads the GPUs from /sys, and from nvidia-smi for NVIDIA's.
func readGPUs() []GPU { return ReadGPUs(os.DirFS("/"), nvidiaSMI) }

// nvidiaSMI runs nvidia-smi, when it is installed, for at most 5 seconds.
func nvidiaSMI() ([]byte, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, NvidiaSMIArgs...).Output()
}
