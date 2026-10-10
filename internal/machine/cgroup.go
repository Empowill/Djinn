package machine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ReadCgroup reads, from a cgroup v2 folder dir (Scope.Cgroup) of the file system fsys, what the processes in it
// use: cgroup.procs, cpu.stat (usage_usec, every process the cgroup held, ended ones too), memory.current (what the
// kernel charges the cgroup: resident memory and the page cache it filled) and memory.peak (since Linux 5.19; 0
// before). Without the memory controller, it fails: memory.current does not exist.
func ReadCgroup(fsys fs.FS, dir string) (Group, error) {
	dir = strings.TrimPrefix(dir, "/")
	pids, err := fs.ReadFile(fsys, path.Join(dir, "cgroup.procs"))
	if err != nil {
		return Group{}, err
	}
	var g Group
	g.Processes = len(strings.Fields(string(pids)))
	stat, err := fs.ReadFile(fsys, path.Join(dir, "cpu.stat"))
	if err != nil {
		return Group{}, err
	}
	usec, err := statField(stat, "usage_usec")
	if err != nil {
		return Group{}, fmt.Errorf("%s/cpu.stat: %w", dir, err)
	}
	g.CPU = time.Duration(usec) * time.Microsecond
	if g.Memory, err = readUint(fsys, path.Join(dir, "memory.current")); err != nil {
		return Group{}, err
	}
	if g.Peak, err = readUint(fsys, path.Join(dir, "memory.peak")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Group{}, err
	}
	return g, nil
}

// statField reads the value of key in a flat-keyed cgroup file: one "<key> <value>" a line.
func statField(b []byte, key string) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " "); ok && k == key {
			return strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, fmt.Errorf("no %s", key)
}

// readUint reads a file that holds one number.
func readUint(fsys fs.FS, name string) (uint64, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

// CgroupPIDs lists the processes in the cgroup folder dir; an error when it does not exist (yet, or any more).
func CgroupPIDs(dir string) ([]int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// FreezeCgroup freezes every process in the cgroup folder dir where it is, or thaws them: one write, which a process
// forking meanwhile does not escape. A frozen process still dies of SIGKILL.
func FreezeCgroup(dir string, frozen bool) error {
	v := "0"
	if frozen {
		v = "1"
	}
	return writeCgroup(dir, "cgroup.freeze", v)
}

// KillCgroup kills every process in the cgroup folder dir, at once (cgroup.kill, since Linux 5.14).
func KillCgroup(dir string) error {
	return writeCgroup(dir, "cgroup.kill", "1")
}

// writeCgroup writes v to a file of the cgroup folder dir, which must exist: a cgroup file is never created.
func writeCgroup(dir, name, v string) error {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(v)
	return errors.Join(err, f.Close())
}
