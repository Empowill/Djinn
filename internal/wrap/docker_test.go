package wrap

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	testTask = "01a1b6c3-9f02-7bb0-814d-c4a3ccdb511e"
	testHome = "/home/dev/.config/djinn"
)

var testCaps = Caps{Memory: 4 << 30, PIDs: 1024}

// bounded is what Djinn puts after docker's command: the label, the slice and the ceilings memory and pids.
func bounded(memory, pids string) []string {
	return []string{
		"--label", "djinn.task=" + testTask, "--cgroup-parent", "djinn-01a1b6c39f027bb0814dc4a3ccdb511e.slice",
		"--label", "djinn.home=" + testHome,
		"--memory", memory, "--memory-swap", memory, "--pids-limit", pids,
	}
}

// TestDocker: docker run and create get the task's label and slice and the ceilings, a lower value the worker gave
// kept, a higher one or none brought down with a note, the swap equal to the memory; what follows the image is the
// container's and stays as it is; every other docker command is left alone.
func TestDocker(t *testing.T) {
	t.Parallel()
	const fourG, pids = "4294967296", "1024"
	cases := []struct {
		name  string
		args  string
		want  []string // nil: args unchanged
		notes int
	}{
		{name: "run", args: "run --rm alpine echo hi",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"--rm", "alpine", "echo", "hi"})},
		{name: "lower memory kept", args: "run -m 512m alpine",
			want: slices.Concat([]string{"run"}, bounded("536870912", pids), []string{"alpine"})},
		{name: "higher memory brought down", args: "run --memory=8g --memory-swap=16g alpine",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"alpine"}), notes: 1},
		{name: "unlimited memory brought down", args: "run --memory -1 alpine",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"alpine"}), notes: 1},
		{name: "lower pids kept", args: "run --pids-limit 64 alpine",
			want: slices.Concat([]string{"run"}, bounded(fourG, "64"), []string{"alpine"})},
		{name: "no pids limit brought down", args: "run --pids-limit=0 alpine",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"alpine"}), notes: 1},
		{name: "own cgroup parent replaced", args: "run --cgroup-parent=mine.slice alpine",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"alpine"}), notes: 1},
		{name: "options with values and groups", args: "run -it -p8080:80 -e K=V -v /a:/b -w /b --name x img sh -c run",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids),
				[]string{"-it", "-p8080:80", "-e", "K=V", "-v", "/a:/b", "-w", "/b", "--name", "x", "img", "sh", "-c", "run"})},
		{name: "the container's own -m", args: "run alpine stress -m 99g",
			want: slices.Concat([]string{"run"}, bounded(fourG, pids), []string{"alpine", "stress", "-m", "99g"})},
		{name: "a group ending in -m", args: "run -dm 1g alpine",
			want: slices.Concat([]string{"run"}, bounded("1073741824", pids), []string{"-d", "alpine"})},
		{name: "global options", args: "--context ci --debug run alpine",
			want: slices.Concat([]string{"--context", "ci", "--debug", "run"}, bounded(fourG, pids), []string{"alpine"})},
		{name: "container create", args: "container create --init alpine",
			want: slices.Concat([]string{"container", "create"}, bounded(fourG, pids), []string{"--init", "alpine"})},
		{name: "ps", args: "ps -a"},
		{name: "build", args: "build -t x ."},
		{name: "compose", args: "compose up -d"},
		{name: "update within", args: "update --memory 1g --pids-limit 100 c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			args := strings.Fields(c.args)
			got, notes, err := Docker(args, testCaps, testTask, testHome)
			want := c.want
			if want == nil {
				want = args
			}
			if err != nil || !slices.Equal(got, want) || len(notes) != c.notes {
				t.Errorf("Docker(%s) = %q, notes %q, %v;\nwant %q, %d notes", c.args, got, notes, err, want, c.notes)
			}
			for _, n := range notes {
				if !strings.Contains(n, "4 GiB") && !strings.Contains(n, "1024") && !strings.Contains(n, "slice") {
					t.Errorf("note %q names no ceiling", n)
				}
			}
		})
	}
}

// TestDockerUpdate: a docker update that would lift a container's ceiling is refused, naming the setting.
func TestDockerUpdate(t *testing.T) {
	t.Parallel()
	for _, args := range []string{
		"update --memory 8g c", "update -m 5g c", "update --memory-swap -1 c", "update --pids-limit -1 c",
		"update --pids-limit 0 c", "container update --pids-limit=5000 c",
	} {
		_, _, err := Docker(strings.Fields(args), testCaps, testTask, testHome)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), Setting) {
			t.Errorf("Docker(%s) = %v, want it refused", args, err)
		}
	}
}

// TestBytes: sizes read as docker reads them, in units of 1024.
func TestBytes(t *testing.T) {
	t.Parallel()
	cases := map[string]int64{
		"1048576": 1 << 20, "512m": 512 << 20, "4g": 4 << 30, "4GB": 4 << 30, "1GiB": 1 << 30, "1.5g": 3 << 29,
		"64k": 64 << 10, "-1": -1,
	}
	for s, want := range cases {
		if got, err := Bytes(s); err != nil || got != want {
			t.Errorf("Bytes(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	if _, err := Bytes("lots"); err == nil {
		t.Error(`Bytes("lots"): no error`)
	}
}
