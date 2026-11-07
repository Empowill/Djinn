package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/wrap"
)

// A worker's containers are bound to its task and bounded: Djinn's docker, first on the worker's path (wrap), gives
// each docker run the task's label and slice and the project's runaway ceilings. The task's series counts what its
// slice uses, and the containers go when the worker ends.

// Containers reads and removes the containers of the workers (WithContainers).
type Containers struct {
	// Read reads what the containers of a task use, from its slice (machine.ReadSlice); nil: not read.
	Read func(taskID string) (machine.Group, error)
	// Remove removes the containers that carry every label given (key=value); nil: none removed.
	Remove func(ctx context.Context, labels ...string)
	// Pause freezes the running containers that carry every label given, or thaws the paused ones, and says how many
	// (wrap.Pause): the memory brake's (brake.go); nil: none frozen.
	Pause func(ctx context.Context, pause bool, labels ...string) int
}

// WithContainers has Djinn count each worker's containers in its task's series, and remove them once it ends.
func WithContainers(c Containers) Option { return func(h *Harness) { h.containers = c } }

// Default ceilings of a worker's container, where its project sets none (djinn.v1.Containers).
const (
	defaultContainerMemory = 4 << 30
	defaultContainerPIDs   = 1024
)

// machineMemory is the machine's memory, read once; 0 when unknown.
var machineMemory = sync.OnceValue(func() uint64 {
	s, err := machine.ReadProc(os.DirFS("/"))
	if err != nil {
		return 0
	}
	return s.MemoryTotal
})

// containerCaps are the ceilings of the containers of a worker in project: the project's (plan.ContainersFile), else
// Djinn's defaults, a quarter of the machine's memory when 4 GiB is more.
func (h *Harness) containerCaps(project *planv1.Project) wrap.Caps {
	caps := wrap.Caps{Memory: defaultContainerMemory, PIDs: defaultContainerPIDs}
	if total := machineMemory(); total > 0 {
		caps.Memory = min(caps.Memory, total/4)
	}
	if project == nil {
		return caps
	}
	dir := plan.ConfigDir(h.home, project)
	c, unknowns, err := plan.ReadContainers(dir)
	for _, u := range unknowns {
		plan.WarnUnknownField(filepath.Join(dir, plan.ContainersFile), u)
	}
	if err != nil {
		logx.L().Warn("containers' ceilings unread: Djinn's defaults hold", "project", project.GetId(), "err", err)
	}
	if c.GetMemoryMib() > 0 {
		caps.Memory = uint64(c.GetMemoryMib()) << 20
	}
	if c.GetPids() > 0 {
		caps.PIDs = int64(c.GetPids())
	}
	return caps
}

// wrapperEnv is what a worker's environment gets for the wrappers first on its path: docker for all, bounding its
// containers, and the heavy heads for an agent without a hook (heads), which wait for their admission. None without
// djinn's path (WithAdmit), for a read-only worker, or on Windows, which runs no sh.
func (h *Harness) wrapperEnv(spec Spec, heads bool) ([]string, error) {
	if h.admit == "" || h.home == "" || spec.ReadOnly || runtime.GOOS == "windows" {
		return nil, nil
	}
	root := wrap.Root(h.home)
	if err := wrap.Write(root, h.admit); err != nil {
		return nil, err
	}
	return wrap.Env(root, h.home, os.Getenv("PATH"), spec.containers, heads), nil
}

// trackContainers has track, the tracker of a worker's processes, remove the task's containers once a process of the
// worker ended, and what it left running: none outlives its worker (W514).
func (h *Harness) trackContainers(taskID string, track func(pid int, cgroup, mark string) func()) func(pid int, cgroup, mark string) func() {
	if h.containers.Remove == nil {
		return track
	}
	return func(pid int, cgroup, mark string) func() {
		ended := func() {}
		if track != nil {
			ended = track(pid, cgroup, mark)
		}
		return func() {
			ended()
			go func() {
				defer logx.Crash("remove containers", "task", taskID)
				h.containers.Remove(context.Background(), wrap.TaskLabel+"="+taskID, wrap.HomeLabel+"="+h.home)
			}()
		}
	}
}

// withContainers adds to g what the task's containers use, when they are read.
func (h *Harness) withContainers(taskID string, g machine.Group) machine.Group {
	if h.containers.Read == nil {
		return g
	}
	c, err := h.containers.Read(taskID)
	if err != nil {
		return g // No container.
	}
	g.Processes += c.Processes
	g.CPU += c.CPU
	g.Memory += c.Memory
	return g
}
