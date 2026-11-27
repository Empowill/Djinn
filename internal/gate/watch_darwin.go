package gate

import (
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/empowill/djinn/internal/machine"
)

// footprintEvery is how often the command's processes are read while it runs.
const footprintEvery = 500 * time.Millisecond

// watch measures the command's CPU time through its resource usage, which counts its children once it waited for
// them, and its peak memory as the most footprint one of its processes reached (machine.Footprint, Q86 A): the
// kernel keeps each process's own most, read every footprintEvery while it lives. resident memory (ru_maxrss) leaves
// the compressed memory out. Like Linux's, the peak is the largest process's, not the tree's sum. A process born
// and gone between two reads escapes the footprint, so the peak is never below the command's ru_maxrss, which wait4
// gives as the most of its own and of the children it waited for (a 400 ms child between two reads gave 19 MB for
// 64 MB, W1161), and a command that ended before the first read has its ru_maxrss alone.
func watch(p *os.Process) func(*os.ProcessState) (cpu time.Duration, peak uint64) {
	done, ended := make(chan struct{}), make(chan struct{})
	var most uint64
	go func() {
		defer close(ended)
		t := time.NewTicker(footprintEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			if m, ok := treeFootprint(p.Pid); ok {
				most = max(most, m)
			}
		}
	}()
	var once sync.Once
	return func(ps *os.ProcessState) (time.Duration, uint64) {
		once.Do(func() { close(done) })
		<-ended
		return ps.UserTime() + ps.SystemTime(), max(most, peakMemory(ps))
	}
}

// treeFootprint gives the most footprint one process of the tree under pid (pid included) reached in its life, from
// the kernel's process list (sysctl kern.proc.all) and proc_pid_rusage; false when none could be read.
func treeFootprint(pid int) (uint64, bool) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return 0, false
	}
	children := map[int][]int{}
	for _, k := range procs {
		children[int(k.Eproc.Ppid)] = append(children[int(k.Eproc.Ppid)], int(k.Proc.P_pid))
	}
	var most uint64
	read := false
	for queue := []int{pid}; len(queue) > 0; queue = queue[1:] {
		if _, m, ok := machine.Footprint(queue[0]); ok {
			most, read = max(most, m), true
		}
		queue = append(queue, children[queue[0]]...)
	}
	return most, read
}
