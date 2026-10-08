package machine

import (
	"os"
	"runtime"
)

// Read reads this machine: /proc for the memory, the load and the pressure.
func Read() (Snapshot, error) {
	s, err := ReadProc(os.DirFS("/"))
	s.Cores = runtime.NumCPU()
	return s, err
}
