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

// TestReadAnon: a cgroup's anonymous memory comes from memory.stat, without its page cache; none read without it.
func TestReadAnon(t *testing.T) {
	t.Parallel()
	const dir = "sys/fs/cgroup/user.slice/djinn-w1-0a1b2c3d.scope"
	fsys := fstest.MapFS{
		dir + "/memory.stat": {Data: []byte("anon 734003200\nfile 2147483648\nkernel 1048576\n")},
		"cgroup/memory.stat": {Data: []byte("file 1024\n")},
	}
	if anon, err := ReadAnon(fsys, "/"+dir); err != nil || anon != 700<<20 {
		t.Errorf("ReadAnon = %d, %v; want 700 MiB", anon, err)
	}
	if _, err := ReadAnon(fsys, "/cgroup"); err == nil {
		t.Error("a memory.stat without anon: no error")
	}
	if _, err := ReadAnon(fsys, "/sys/fs/cgroup/none.scope"); err == nil {
		t.Error("a cgroup that does not exist: no error")
	}
}

// TestCgroupRuns: a program is found by the name the kernel keeps of each process of the cgroup.
func TestCgroupRuns(t *testing.T) {
	t.Parallel()
	const dir = "sys/fs/cgroup/user.slice/djinn-w1-0a1b2c3d.scope"
	fsys := fstest.MapFS{
		dir + "/cgroup.procs": {Data: []byte("10\n11\n12\n")},
		"proc/10/comm":        {Data: []byte("claude\n")},
		"proc/11/comm":        {Data: []byte("git\n")},
	}
	if !CgroupRuns(fsys, "/"+dir, "git") {
		t.Error("git runs in the scope: not found")
	}
	if CgroupRuns(fsys, "/"+dir, "go") {
		t.Error("go does not run in the scope: found")
	}
	if CgroupRuns(fsys, "/sys/fs/cgroup/none.scope", "git") {
		t.Error("no cgroup: found")
	}
}
