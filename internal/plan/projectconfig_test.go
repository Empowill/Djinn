package plan

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestReadProjectConfig: Djinn's configuration of a project is read from djinn.yaml at its root, else from
// .agents/djinn.yaml, the root's winning when both are there; a field the message does not know is ignored and named,
// the rest of the file applied (Q21); a YAML that does not parse, a value of the wrong type or out of range refuses
// the file, the error naming the file and the field.
func TestReadProjectConfig(t *testing.T) {
	t.Parallel()
	const agents = ".agents/djinn.yaml"
	cases := []struct {
		name          string
		files         map[string]string
		file, ignored string
		memory, pids  uint32
		unknown       []string
		err           []string // what the error says, all of it; nil: no error
	}{
		{name: "none"},
		{name: "root", files: map[string]string{"djinn.yaml": "containers:\n  memory_mib: 8192\n  pids: 2048\n"},
			file: "djinn.yaml", memory: 8192, pids: 2048},
		{name: "agents folder", files: map[string]string{agents: "containers:\n  memoryMib: 6144\n"},
			file: agents, memory: 6144},
		{name: "both", files: map[string]string{"djinn.yaml": "containers: {pids: 64}\n", agents: "containers: {pids: 99}\n"},
			file: "djinn.yaml", ignored: agents, pids: 64},
		{name: "empty", files: map[string]string{"djinn.yaml": "# nothing yet\n"}, file: "djinn.yaml"},
		{name: "unknown field", files: map[string]string{"djinn.yaml": "containers: {pids: 64}\nworkers: 3\n"},
			file: "djinn.yaml", pids: 64, unknown: []string{"workers"}},
		{name: "unknown nested fields", files: map[string]string{agents: "containers:\n  memroy_mib: 8192\n  pids: 99\n  cpu: 2\n"},
			file: agents, pids: 99, unknown: []string{"containers.cpu", "containers.memroy_mib"}},
		{name: "unknown field beside a wrong type", files: map[string]string{"djinn.yaml": "containers: {pids: many}\nworkers: 3\n"},
			file: "djinn.yaml", unknown: []string{"workers"}, err: []string{"djinn.yaml", "pids"}},
		{name: "invalid YAML", files: map[string]string{"djinn.yaml": "containers: [\n"},
			file: "djinn.yaml", err: []string{"djinn.yaml", "yaml"}},
		{name: "not a mapping", files: map[string]string{"djinn.yaml": "- containers\n"},
			file: "djinn.yaml", err: []string{"djinn.yaml", "not a mapping"}},
		{name: "wrong type", files: map[string]string{"djinn.yaml": "containers: {pids: many}\n"},
			file: "djinn.yaml", err: []string{"djinn.yaml", "pids"}},
		{name: "out of range", files: map[string]string{"djinn.yaml": "containers: {memory_mib: 1}\n"},
			file: "djinn.yaml", err: []string{"djinn.yaml", "memory_mib"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, content := range c.files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			read, err := ReadProjectConfig(dir)
			if read.File != c.file || read.Ignored != c.ignored {
				t.Errorf("read %q, ignored %q; want %q, %q", read.File, read.Ignored, c.file, c.ignored)
			}
			if !slices.Equal(read.Unknown, c.unknown) {
				t.Errorf("unknown %q, want %q", read.Unknown, c.unknown)
			}
			if c.err != nil {
				if err == nil || read.Config != nil {
					t.Fatalf("read %v, err %v; want an error", read.Config, err)
				}
				for _, s := range c.err {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("err = %q, want it to say %q", err, s)
					}
				}
				if strings.Contains(err.Error(), "(line") {
					t.Errorf("err = %q places the error in the JSON, not in the file", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.file != "" && read.Config == nil {
				t.Fatal("no configuration read")
			}
			got := read.Config.GetContainers()
			if got.GetMemoryMib() != c.memory || got.GetPids() != c.pids {
				t.Errorf("containers = %v, want memory_mib %d, pids %d", got, c.memory, c.pids)
			}
		})
	}
}

// TestDjinnYAMLNotices (Q21): the project view's To fix says what Djinn finds in the project's djinn.yaml. A file
// it cannot read is a thing to fix, counted in to_fix, naming the file and the cause, and none of it applies; a field
// it does not know is a warning, not counted, naming the file and the field, and the rest applies; a second file not
// read is a warning too.
func TestDjinnYAMLNotices(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		files   map[string]string
		want    []string // noticeLines, then the detail of each
		details []string
		toFix   int32
	}{
		{name: "unreadable", files: map[string]string{"djinn.yaml": "containers: {pids: many}\n"},
			want: []string{"TO_FIX SETTINGS djinn.yaml FIX_FILE"}, details: []string{"djinn.yaml: "}, toFix: 1},
		{name: "invalid YAML", files: map[string]string{".agents/djinn.yaml": "containers: [\n"},
			want: []string{"TO_FIX SETTINGS .agents/djinn.yaml FIX_FILE"}, details: []string{".agents/djinn.yaml: yaml"}, toFix: 1},
		{name: "unknown field", files: map[string]string{"djinn.yaml": "containers: {pid: 64, pids: 32}\nworkers: 3\n"},
			want:    []string{"WARNING SETTINGS djinn.yaml FIX_FILE", "WARNING SETTINGS djinn.yaml FIX_FILE"},
			details: []string{"unknown field containers.pid", "unknown field workers"}},
		{name: "both files", files: map[string]string{"djinn.yaml": "{}\n", ".agents/djinn.yaml": "{}\n"},
			want: []string{"WARNING SETTINGS .agents/djinn.yaml FIX_FILE"}, details: []string{""}},
		{name: "readable", files: map[string]string{"djinn.yaml": "containers: {pids: 32}\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := resolvedTempDir(t) // the notices name files under the folder as djinn stores it
			for name, content := range c.files {
				writeSettings(t, filepath.Join(dir, filepath.FromSlash(name)), content)
			}
			client := serve(t, WithHome(t.TempDir()))
			ctx := t.Context()
			added, err := client.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir}))
			if err != nil {
				t.Fatal(err)
			}
			res, err := client.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{
				Project: added.Msg.GetProject().GetName(),
			}))
			if err != nil {
				t.Fatal(err)
			}
			notices := res.Msg.GetNotices()
			if got := noticeLines(notices); !slices.Equal(got, c.want) {
				t.Fatalf("notices %q, want %q", got, c.want)
			}
			for i, n := range notices {
				if !strings.HasPrefix(n.GetDetail(), c.details[i]) || (c.details[i] == "") != (n.GetDetail() == "") {
					t.Errorf("notice %d detail %q, want %q", i, n.GetDetail(), c.details[i])
				}
				if want := filepath.Join(dir, filepath.FromSlash(n.GetSubject())); n.GetFile() != want || n.GetText() == "" {
					t.Errorf("notice %d: file %q, text %q; want %q and a sentence", i, n.GetFile(), n.GetText(), want)
				}
			}
			project := res.Msg.GetProject()
			if project.GetToFix() != c.toFix || (c.toFix > 0) != (project.GetFirstFix() != "") {
				t.Errorf("to fix %d %q, want %d", project.GetToFix(), project.GetFirstFix(), c.toFix)
			}
		})
	}
}

// TestProjectConfigLines: a commit that adds, changes or removes a djinn.yaml is a line of the journal of changes,
// naming the file.
func TestProjectConfigLines(t *testing.T) {
	t.Parallel()
	old, next := t.TempDir(), t.TempDir()
	write := func(dir, name, content string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if lines := projectConfigLines(old, next); len(lines) != 0 {
		t.Errorf("no file: lines %v", lines)
	}
	write(next, "djinn.yaml", "containers: {pids: 64}\n")
	write(old, ".agents/djinn.yaml", "containers: {pids: 64}\n")
	write(old, ".agents/settings.txtpb", "setup: \"npm ci\"\n")
	write(next, ".agents/settings.txtpb", "setup: \"npm ci\"\n")
	var texts []string
	for _, l := range configLines(old, next, func(string) bool { return false }) {
		texts = append(texts, l.GetText())
	}
	want := []string{"Djinn's configuration is new in djinn.yaml.", "Djinn's configuration in .agents/djinn.yaml is removed."}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("lines %q, want %q", texts, want)
	}
	write(old, "djinn.yaml", "containers: {pids: 32}\n")
	write(old, ".agents/djinn.yaml", "")
	write(next, ".agents/djinn.yaml", "")
	if lines := projectConfigLines(old, next); len(lines) != 1 || lines[0].GetText() != "Djinn's configuration changes in djinn.yaml." {
		t.Errorf("changed: lines %v", lines)
	}
}
