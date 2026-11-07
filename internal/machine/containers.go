package machine

import (
	"io/fs"
	"path"
	"strings"
	"time"
)

// ContainersSlice is where docker runs the containers of Djinn's workers, each task's in a slice of its own
// (wrap.Slice: --cgroup-parent=djinn-<task>.slice, which systemd places under djinn.slice). Docker's daemon owns it,
// outside the user's tree: its files are read, never written.
const ContainersSlice = cgroupRoot + "/djinn.slice"

// ReadSlice reads, from the cgroup v2 folder dir of the file system fsys, what the containers of a task's slice use:
// its memory.current, its memory.peak and its cpu.stat count every container under it, and its processes are those
// of the containers' scopes. It fails when the slice does not exist: the task runs no container.
func ReadSlice(fsys fs.FS, dir string) (Group, error) {
	dir = strings.TrimPrefix(dir, "/")
	var g Group
	stat, err := fs.ReadFile(fsys, path.Join(dir, "cpu.stat"))
	if err != nil {
		return Group{}, err
	}
	usec, err := statField(stat, "usage_usec")
	if err != nil {
		return Group{}, err
	}
	g.CPU = time.Duration(usec) * time.Microsecond
	if g.Memory, err = readUint(fsys, path.Join(dir, "memory.current")); err != nil {
		return Group{}, err
	}
	g.Peak, _ = readUint(fsys, path.Join(dir, "memory.peak"))
	for _, d := range sliceScopes(fsys, dir) {
		if pids, err := fs.ReadFile(fsys, path.Join(d, "cgroup.procs")); err == nil {
			g.Processes += len(strings.Fields(string(pids)))
		}
	}
	return g, nil
}

// sliceScopes are the cgroup folders right under dir, in fsys: the scopes of the containers of a task's slice.
func sliceScopes(fsys fs.FS, dir string) []string {
	entries, _ := fs.ReadDir(fsys, dir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, path.Join(dir, e.Name()))
		}
	}
	return dirs
}

// containerScopes are the scopes of the containers of every task's slice under the containers' slice root, in fsys:
// djinn.slice/djinn-<task>.slice/docker-<id>.scope, any Djinn's, as Djinn's own scopes are.
func containerScopes(fsys fs.FS, root string) []string {
	root = strings.TrimPrefix(root, "/")
	entries, _ := fs.ReadDir(fsys, root) // No slice: no container.
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "djinn-") && strings.HasSuffix(e.Name(), ".slice") {
			for _, d := range sliceScopes(fsys, path.Join(root, e.Name())) {
				dirs = append(dirs, "/"+d)
			}
		}
	}
	return dirs
}
