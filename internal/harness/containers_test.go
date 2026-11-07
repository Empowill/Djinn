package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/testx"
	"github.com/empowill/djinn/internal/wrap"
)

// TestContainersCounted: what a task's containers use, read from its slice, counts in its worker's reading, its
// series and its peaks; once the worker ends, its containers go, this Djinn's only.
func TestContainersCounted(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	providers := testProviders()
	providers[planv1.Provider_PROVIDER_CLAUDE] = envProvider{Claude{Command: os.Args[0]}, env}
	removed := make(chan []string, 4)
	var read []string
	home := t.TempDir()
	e := upWith(t, home, providers,
		WithMeasure(10*time.Millisecond, func(int, string) (machine.Group, error) {
			return machine.Group{Processes: 1, CPU: time.Second, Memory: 100 << 20}, nil
		}),
		WithContainers(Containers{
			Read: func(taskID string) (machine.Group, error) {
				read = append(read, taskID) // The pump alone reads: one at a time.
				return machine.Group{Processes: 2, Memory: 1 << 30}, nil
			},
			Remove: func(_ context.Context, labels ...string) { removed <- labels },
		}))
	wishID, _ := e.wish(t, nativeFolder(t))
	res, err := e.tasks.Spawn(t.Context(), connect.NewRequest(&planv1.TaskServiceSpawnRequest{
		WishId: wishID, Title: "Run a container", Prompt: "x", Provider: planv1.Provider_PROVIDER_CLAUDE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	task := res.Msg.GetTask()
	var got *planv1.Resources
	waitFor(t, "a reading with its containers", func() bool {
		got = e.get(t, task.GetId()).GetResources()
		return got.GetProcesses() > 0
	})
	if got.GetMemoryBytes() != 100<<20+1<<30 || got.GetProcesses() != 3 || got.GetPeakMemoryBytes() < 100<<20+1<<30 {
		t.Errorf("resources = %v, want the worker's 100 MiB and its containers' 1 GiB, 3 processes", got)
	}
	if uses := e.h.Uses(); len(uses) != 1 || uses[0].GetResources().GetMemoryBytes() != 100<<20+1<<30 {
		t.Errorf("uses = %v, want the containers in the worker's use", uses)
	}
	if _, err := e.tasks.Stop(t.Context(), connect.NewRequest(&planv1.TaskServiceStopRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	e.watch(t.Context(), t, task.GetId(), 0)
	select {
	case labels := <-removed:
		want := []string{wrap.TaskLabel + "=" + task.GetId(), wrap.HomeLabel + "=" + home}
		if !slices.Equal(labels, want) {
			t.Errorf("removed the containers labelled %q, want %q", labels, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the worker's containers were not removed once it ended")
	}
	if len(read) == 0 || read[0] != task.GetId() {
		t.Errorf("slices read %q, want the task's", read)
	}
}

// TestContainerCaps: a project's djinn.yaml sets the ceilings of its workers' containers, and the wrapper names it;
// without it, or with a field left out, Djinn's defaults hold, never more than a quarter of the machine's memory.
func TestContainerCaps(t *testing.T) {
	t.Parallel()
	defaultMemory := uint64(defaultContainerMemory)
	if total := machineMemory(); total > 0 {
		defaultMemory = min(defaultMemory, total/4)
	}
	cases := []struct {
		name, file string
		want       wrap.Caps
	}{
		{"no file", "", wrap.Caps{Memory: defaultMemory, PIDs: 1024}},
		{"both", "containers: {memory_mib: 8192, pids: 2048}\n", wrap.Caps{Memory: 8 << 30, PIDs: 2048, File: "djinn.yaml"}},
		{"memory only", "containers:\n  memory_mib: 6144\n", wrap.Caps{Memory: 6 << 30, PIDs: 1024, File: "djinn.yaml"}},
		{"invalid", "containers:\n  memory_mib: 1\n", wrap.Caps{Memory: defaultMemory, PIDs: 1024, File: "djinn.yaml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if c.file != "" {
				if err := os.WriteFile(filepath.Join(dir, "djinn.yaml"), []byte(c.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := &Harness{home: t.TempDir()}
			if got := h.containerCaps(&planv1.Project{Id: "p", Directory: dir}); got != c.want {
				t.Errorf("caps = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestWrapperEnv: a worker's path starts with Djinn's docker, and with the heavy heads for an agent without a hook;
// its environment tells the wrappers its containers' ceilings. None without djinn's path, nor for a read-only worker.
func TestWrapperEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no wrapper on Windows: no sh")
	}
	t.Parallel()
	home := t.TempDir()
	h := &Harness{home: home, admit: "/opt/djinn/djinn"}
	caps := wrap.Caps{Memory: 4 << 30, PIDs: 1024, File: "djinn.yaml"}
	root := wrap.Root(home)
	for _, heads := range []bool{false, true} {
		got, err := h.wrapperEnv(Spec{containers: caps}, heads)
		if err != nil {
			t.Fatal(err)
		}
		path := ""
		for _, kv := range got {
			if p, ok := strings.CutPrefix(kv, "PATH="); ok {
				path = p
			}
		}
		dirs := filepath.SplitList(path)
		want := []string{wrap.DockerDir(root)}
		if heads {
			want = []string{wrap.HeadsDir(root), wrap.DockerDir(root)}
		}
		if len(dirs) <= len(want) || !slices.Equal(dirs[:len(want)], want) {
			t.Errorf("heads %v: PATH = %q, want %q first", heads, path, want)
		}
		for _, kv := range []string{
			wrap.EnvMemory + "=4294967296", wrap.EnvPIDs + "=1024", wrap.EnvFile + "=djinn.yaml", wrap.EnvHome + "=" + home,
		} {
			if !slices.Contains(got, kv) {
				t.Errorf("heads %v: env %q lacks %s", heads, got, kv)
			}
		}
		if slices.Contains(got, wrap.EnvAdmit+"=1") != heads {
			t.Errorf("heads %v: env %q", heads, got)
		}
	}
	for _, head := range append([]string{"docker"}, wrap.Heads...) {
		dir := wrap.HeadsDir(root)
		if head == "docker" {
			dir = wrap.DockerDir(root)
		}
		b, err := os.ReadFile(filepath.Join(dir, head))
		if err != nil || !strings.Contains(string(b), "exec '/opt/djinn/djinn' gate wrap '"+root+"' "+head+` "$@"`) {
			t.Errorf("wrapper of %s: %q, %v", head, b, err)
		}
	}
	if got, _ := h.wrapperEnv(Spec{ReadOnly: true, containers: caps}, true); got != nil {
		t.Errorf("read-only worker: %q", got)
	}
	if got, _ := (&Harness{home: home}).wrapperEnv(Spec{containers: caps}, true); got != nil {
		t.Errorf("no djinn path: %q", got)
	}
}
