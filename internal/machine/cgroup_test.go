package machine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"
)

// TestReadCgroup reads a worker's scope from fixture cgroup files: its processes, the CPU of every process it held,
// its memory now and at its peak.
func TestReadCgroup(t *testing.T) {
	const dir = "sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-W1-0a1b2c3d.scope"
	fsys := fstest.MapFS{
		dir + "/cgroup.procs":   {Data: []byte("4100\n4107\n4230\n")},
		dir + "/cpu.stat":       {Data: []byte("usage_usec 2500000\nuser_usec 2000000\nsystem_usec 500000\n")},
		dir + "/memory.current": {Data: []byte("314572800\n")},
		dir + "/memory.peak":    {Data: []byte("524288000\n")},
	}
	g, err := ReadCgroup(fsys, "/"+dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Group{Processes: 3, CPU: 2500 * time.Millisecond, Memory: 300 << 20, Peak: 500 << 20}); g != want {
		t.Errorf("ReadCgroup = %+v, want %+v", g, want)
	}

	delete(fsys, dir+"/memory.peak") // Before Linux 5.19.
	if g, err := ReadCgroup(fsys, dir); err != nil || g.Peak != 0 || g.Memory != 300<<20 {
		t.Errorf("without memory.peak: %+v, %v", g, err)
	}
	delete(fsys, dir+"/memory.current") // Without the memory controller: read the processes instead.
	if _, err := ReadCgroup(fsys, dir); err == nil {
		t.Error("read without memory.current")
	}
	if _, err := ReadCgroup(fsys, "sys/fs/cgroup/gone.scope"); err == nil {
		t.Error("read a scope that is gone")
	}
	fsys[dir+"/memory.current"] = &fstest.MapFile{Data: []byte("1\n")}
	fsys[dir+"/cpu.stat"] = &fstest.MapFile{Data: []byte("user_usec 1\n")}
	if _, err := ReadCgroup(fsys, dir); err == nil {
		t.Error("read a cpu.stat without usage_usec")
	}
}

// TestCgroupFiles: the processes of a cgroup are listed from cgroup.procs; freezing, thawing and killing write their
// files, which must exist.
func TestCgroupFiles(t *testing.T) {
	dir := t.TempDir()
	for name, v := range map[string]string{"cgroup.procs": "12\n34\n", "cgroup.freeze": "0\n", "cgroup.kill": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pids, err := CgroupPIDs(dir); err != nil || !slices.Equal(pids, []int{12, 34}) {
		t.Errorf("CgroupPIDs = %v, %v", pids, err)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	if err := FreezeCgroup(dir, true); err != nil || read("cgroup.freeze")[0] != '1' {
		t.Errorf("freeze: %v, %q", err, read("cgroup.freeze"))
	}
	if err := FreezeCgroup(dir, false); err != nil || read("cgroup.freeze")[0] != '0' {
		t.Errorf("thaw: %v, %q", err, read("cgroup.freeze"))
	}
	if err := KillCgroup(dir); err != nil || read("cgroup.kill") != "1" {
		t.Errorf("kill: %v, %q", err, read("cgroup.kill"))
	}
	gone := filepath.Join(dir, "gone.scope")
	if _, err := CgroupPIDs(gone); err == nil {
		t.Error("listed a cgroup that is gone")
	}
	if err := FreezeCgroup(gone, true); err == nil {
		t.Error("froze a cgroup that is gone")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("a write made the cgroup")
	}
}
