package machine

import (
	"testing"
	"testing/fstest"
	"time"
)

// TestReadSlice: a task's slice counts the memory and the CPU of all its containers, and the processes of each.
func TestReadSlice(t *testing.T) {
	t.Parallel()
	const dir = "sys/fs/cgroup/djinn.slice/djinn-0a1b.slice"
	fsys := fstest.MapFS{
		dir + "/cpu.stat":                    {Data: []byte("usage_usec 2500000\nuser_usec 2000000\n")},
		dir + "/memory.current":              {Data: []byte("1073741824\n")},
		dir + "/memory.peak":                 {Data: []byte("2147483648\n")},
		dir + "/docker-a.scope/cgroup.procs": {Data: []byte("10\n11\n")},
		dir + "/docker-b.scope/cgroup.procs": {Data: []byte("20\n")},
	}
	g, err := ReadSlice(fsys, "/"+dir)
	if err != nil || g != (Group{Processes: 3, CPU: 2500 * time.Millisecond, Memory: 1 << 30, Peak: 2 << 30}) {
		t.Errorf("ReadSlice = %+v, %v", g, err)
	}
	if _, err := ReadSlice(fsys, "/sys/fs/cgroup/djinn.slice/djinn-none.slice"); err == nil {
		t.Error("a task without a container: no error")
	}
}
