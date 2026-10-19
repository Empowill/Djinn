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
	t.Parallel()
	const dir = "sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-W1-0a1b2c3d.scope"
	files := func(drop ...string) fstest.MapFS {
		fsys := fstest.MapFS{
			dir + "/cgroup.procs":   {Data: []byte("4100\n4107\n4230\n")},
			dir + "/cpu.stat":       {Data: []byte("usage_usec 2500000\nuser_usec 2000000\nsystem_usec 500000\n")},
			dir + "/memory.current": {Data: []byte("314572800\n")},
			dir + "/memory.peak":    {Data: []byte("524288000\n")},
		}
		for _, name := range drop {
			delete(fsys, dir+"/"+name)
		}
		return fsys
	}
	noUsage := files()
	noUsage[dir+"/cpu.stat"] = &fstest.MapFile{Data: []byte("user_usec 1\n")}
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		dir     string
		want    Group
		wantErr bool
	}{
		{name: "scope", fsys: files(), dir: "/" + dir,
			want: Group{Processes: 3, CPU: 2500 * time.Millisecond, Memory: 300 << 20, Peak: 500 << 20}},
		// Before Linux 5.19.
		{name: "without memory.peak", fsys: files("memory.peak"), dir: dir,
			want: Group{Processes: 3, CPU: 2500 * time.Millisecond, Memory: 300 << 20}},
		// Without the memory controller: read the processes instead.
		{name: "without memory.current", fsys: files("memory.peak", "memory.current"), dir: dir, wantErr: true},
		{name: "gone", fsys: files(), dir: "sys/fs/cgroup/gone.scope", wantErr: true},
		{name: "cpu.stat without usage_usec", fsys: noUsage, dir: dir, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, err := ReadCgroup(c.fsys, c.dir)
			if (err != nil) != c.wantErr || g != c.want {
				t.Errorf("ReadCgroup = %+v, %v; want %+v, error %v", g, err, c.want, c.wantErr)
			}
		})
	}
}

// cgroupDir is a cgroup folder of fixture files, holding processes 12 and 34, thawed.
func cgroupDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, v := range map[string]string{"cgroup.procs": "12\n34\n", "cgroup.freeze": "0\n", "cgroup.kill": "",
		"cpu.weight": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// readCgroupFile reads a file of a cgroup folder made by cgroupDir.
func readCgroupFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestCgroupPIDs: the processes of a cgroup are listed from cgroup.procs; a cgroup that is gone is an error.
func TestCgroupPIDs(t *testing.T) {
	t.Parallel()
	dir := cgroupDir(t)
	cases := []struct {
		name    string
		dir     string
		want    []int
		wantErr bool
	}{
		{name: "listed", dir: dir, want: []int{12, 34}},
		{name: "gone", dir: filepath.Join(dir, "gone.scope"), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if pids, err := CgroupPIDs(c.dir); (err != nil) != c.wantErr || !slices.Equal(pids, c.want) {
				t.Errorf("CgroupPIDs = %v, %v; want %v, error %v", pids, err, c.want, c.wantErr)
			}
		})
	}
}

// TestFreezeCgroup: freezing and thawing write cgroup.freeze, which must exist: a write never makes a cgroup.
func TestFreezeCgroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		frozen bool
		gone   bool
		want   byte
	}{
		{name: "freeze", frozen: true, want: '1'},
		{name: "thaw", frozen: false, want: '0'},
		{name: "gone", frozen: true, gone: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := cgroupDir(t)
			if c.gone {
				gone := filepath.Join(dir, "gone.scope")
				if err := FreezeCgroup(gone, c.frozen); err == nil {
					t.Error("froze a cgroup that is gone")
				}
				if _, err := os.Stat(gone); !os.IsNotExist(err) {
					t.Error("a write made the cgroup")
				}
				return
			}
			if !c.frozen { // Frozen first, to see the thaw.
				if err := FreezeCgroup(dir, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := FreezeCgroup(dir, c.frozen); err != nil || readCgroupFile(t, dir, "cgroup.freeze")[0] != c.want {
				t.Errorf("FreezeCgroup(%v): %v, %q", c.frozen, err, readCgroupFile(t, dir, "cgroup.freeze"))
			}
		})
	}
}

// TestKillCgroup: killing writes 1 to cgroup.kill.
func TestKillCgroup(t *testing.T) {
	t.Parallel()
	dir := cgroupDir(t)
	if err := KillCgroup(dir); err != nil || readCgroupFile(t, dir, "cgroup.kill") != "1" {
		t.Errorf("kill: %v, %q", err, readCgroupFile(t, dir, "cgroup.kill"))
	}
}

// TestSetCgroupWeight: the CPU weight is written to cpu.weight.
func TestSetCgroupWeight(t *testing.T) {
	t.Parallel()
	dir := cgroupDir(t)
	if err := SetCgroupWeight(dir, 10); err != nil || readCgroupFile(t, dir, "cpu.weight") != "10" {
		t.Errorf("weight: %v, %q", err, readCgroupFile(t, dir, "cpu.weight"))
	}
}
