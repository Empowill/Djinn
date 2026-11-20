package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

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

// TestProjectConfigVersion (W120): a djinn.yaml says the version of its schema, 1 when it says none; a version higher
// than the one this Djinn knows, or one that is not a whole number from 1, refuses the whole file, the error naming
// the file, the version read and the highest known; the project then declares no gate, and says why.
func TestProjectConfigVersion(t *testing.T) {
	t.Parallel()
	const gates = "gates:\n  - name: stack\n    alone: [task run-back]\n"
	cases := []struct {
		name, yaml string
		err        []string // what the error says, all of it; nil: read, as version 1
	}{
		{name: "absent", yaml: gates},
		{name: "empty", yaml: "version:\n" + gates},
		{name: "one", yaml: "version: 1\n" + gates},
		{name: "newer", yaml: "version: 2\n" + gates, err: []string{"djinn.yaml: version 2", "up to version 1", "update Djinn"}},
		{name: "past an int", yaml: "version: 18446744073709551615\n" + gates,
			err: []string{"djinn.yaml: version 18446744073709551615", "up to version 1"}},
		{name: "zero", yaml: "version: 0\n" + gates, err: []string{"djinn.yaml: version 0", "not a version", "1 to 1"}},
		{name: "negative", yaml: "version: -1\n" + gates, err: []string{"djinn.yaml: version -1", "not a version"}},
		{name: "a fraction", yaml: "version: 1.5\n" + gates, err: []string{"djinn.yaml: version 1.5", "not a version"}},
		{name: "a word", yaml: "version: two\n" + gates, err: []string{`djinn.yaml: version "two"`, "not a version"}},
		{name: "a text", yaml: "version: '1'\n" + gates, err: []string{`djinn.yaml: version "1"`, "not a version"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "djinn.yaml"), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			read, err := ReadProjectConfig(dir)
			if len(read.Unknown) != 0 {
				t.Errorf("unknown %q: version is a field of the file", read.Unknown)
			}
			project := &planv1.Project{Id: "p", Directory: dir}
			declared := LoadProjectGates(t.TempDir(), project)
			if c.err == nil {
				if err != nil {
					t.Fatal(err)
				}
				if v := read.Config.GetVersion(); v != 1 || ProjectConfigVersion != 1 {
					t.Errorf("version %d, highest %d; want 1, 1", v, ProjectConfigVersion)
				}
				if len(declared.Gates) != 1 || len(declared.Warnings) != 0 {
					t.Errorf("gates %v, warnings %q; want stack alone", declared.Gates, declared.Warnings)
				}
				return
			}
			if err == nil || read.Config != nil || read.Settings != nil {
				t.Fatalf("read %v, %v, err %v; want the file refused", read.Config, read.Settings, err)
			}
			for _, s := range c.err {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err = %q, want it to say %q", err, s)
				}
			}
			if len(declared.Gates) != 0 || len(declared.Warnings) != 1 ||
				declared.Warnings[0] != err.Error()+": no gate is declared" {
				t.Errorf("gates %v, warnings %q; want none, said with the error", declared.Gates, declared.Warnings)
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
		{name: "a gate left out", files: map[string]string{"djinn.yaml": "gates:\n  - name: commands\n  - name: stack\n"},
			want: []string{"WARNING SETTINGS djinn.yaml FIX_FILE"}, details: []string{"gate commands: "}},
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
	write(old, "app/.agents/djinn.yaml", "setup: [\n") // Not a file of this configuration.
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

// TestReadProjectGates (L17): each gate of a djinn.yaml is read and checked on its own. One that is not valid (a
// reserved name, a pattern that starts with *, too many patterns, a wrong type, no name), named twice, or past the
// eighth is left out, said with its name; the other gates and the containers apply. A field a gate does not know is
// ignored and named (lease, before it is delivered). A pattern of one word is kept, and said.
func TestReadProjectGates(t *testing.T) {
	t.Parallel()
	many := "[" + strings.TrimSuffix(strings.Repeat("go test, ", 33), ", ") + "]"
	var nine strings.Builder
	for i := range 9 {
		fmt.Fprintf(&nine, "  - name: g%d\n", i)
	}
	cases := []struct {
		name           string
		yaml           string
		gates          []string // name: alone | beside
		dropped, broad []string // what each says, in part
		unknown        []string
		pids           uint32
	}{
		{name: "dolmen", yaml: "containers: {pids: 64}\ngates:\n  - name: stack\n    alone: [task run-back, docker compose * up]\n" +
			"    beside: [task recette]\n    lease: 15m\n",
			gates: []string{"stack: task run-back, docker compose * up | task recette"}, unknown: []string{"gates[0].lease"}, pids: 64},
		{name: "reserved name", yaml: "containers: {pids: 64}\ngates:\n  - name: commands\n    alone: [go test]\n  - name: db\n    beside: [psql]\n",
			gates: []string{"db:  | psql"}, dropped: []string{"gate commands: "}, broad: []string{`gate db: the pattern "psql"`}, pids: 64},
		{name: "a pattern that starts with *", yaml: "gates:\n  - name: stack\n    alone: ['* up']\n",
			dropped: []string{"gate stack: "}},
		{name: "an empty pattern", yaml: "gates:\n  - name: stack\n    beside: ['']\n", dropped: []string{"gate stack: "}},
		{name: "too many patterns", yaml: "gates:\n  - name: stack\n    alone: " + many + "\n", dropped: []string{"32 patterns"}},
		{name: "a name in capitals", yaml: "gates:\n  - name: Stack\n", dropped: []string{"gate Stack: "}},
		{name: "no name", yaml: "gates:\n  - alone: [task run-back]\n", dropped: []string{"gates[0]: a gate has a name"}},
		{name: "a wrong type", yaml: "gates:\n  - name: stack\n    alone: task run-back\n  - name: db\n",
			gates: []string{"db:  | "}, dropped: []string{"gate stack: "}},
		{name: "declared twice", yaml: "gates:\n  - name: db\n    beside: [psql x]\n  - name: db\n",
			gates: []string{"db:  | psql x"}, dropped: []string{"gate db: declared twice"}},
		{name: "past eight", yaml: "gates:\n" + nine.String(),
			gates:   []string{"g0:  | ", "g1:  | ", "g2:  | ", "g3:  | ", "g4:  | ", "g5:  | ", "g6:  | ", "g7:  | "},
			dropped: []string{"gate g8: past the 8 gates"}},
		{name: "not a list", yaml: "gates: stack\ncontainers: {pids: 64}\n", dropped: []string{"not a list"}, pids: 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "djinn.yaml"), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			read, err := ReadProjectConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			var gates []string
			for _, g := range read.Config.GetGates() {
				gates = append(gates, g.GetName()+": "+strings.Join(g.GetAlone(), ", ")+" | "+strings.Join(g.GetBeside(), ", "))
			}
			if !slices.Equal(gates, c.gates) {
				t.Errorf("gates %q, want %q", gates, c.gates)
			}
			if read.Config.GetContainers().GetPids() != c.pids {
				t.Errorf("containers %v, want pids %d", read.Config.GetContainers(), c.pids)
			}
			if !slices.Equal(read.Unknown, c.unknown) {
				t.Errorf("unknown %q, want %q", read.Unknown, c.unknown)
			}
			for _, w := range []struct {
				got, want []string
				what      string
			}{{read.Dropped, c.dropped, "dropped"}, {read.Broad, c.broad, "broad"}} {
				if len(w.got) != len(w.want) {
					t.Errorf("%s %q, want %q", w.what, w.got, w.want)
					continue
				}
				for i := range w.got {
					if !strings.Contains(w.got[i], w.want[i]) {
						t.Errorf("%s %q, want it to say %q", w.what, w.got[i], w.want[i])
					}
				}
			}
		})
	}
}

// TestProjectGates (L17): djinn project gates shows the gates a project declares as Djinn applies them on this
// machine: one named as a check is left out, said; the developer turns one off for themselves, in their own file
// only, and back on; a gate the project does not declare cannot be turned off; the repository's gates_off is ignored.
func TestProjectGates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	dir := t.TempDir()
	writeSettings(t, filepath.Join(dir, ".agents", "djinn.yaml"), "gates:\n  - name: stack\n    alone: [task run-back]\n"+
		"    beside: [task recette]\n  - name: lint\n    alone: [task lint]\n  - name: db\n    beside: [psql]\n"+
		teamYAML("checks:\n  - {name: lint, command: task lint, when: commit}\ngates_off: db\n"))
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: dir, Name: "dolmen"}))
	if err != nil {
		t.Fatal(err)
	}
	gates := func(off, on *string) (*planv1.ProjectServiceGatesResponse, error) {
		res, err := c.projects.Gates(ctx, connect.NewRequest(&planv1.ProjectServiceGatesRequest{Project: "dolmen", Off: off, On: on}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	names := func(res *planv1.ProjectServiceGatesResponse) []string {
		var out []string
		for _, g := range res.GetGates() {
			out = append(out, g.GetName())
		}
		return out
	}
	devFile := DeveloperSettingsFile(home, add.Msg.GetProject().GetId())
	res, err := gates(nil, nil)
	if err != nil || !slices.Equal(names(res), []string{"stack", "db"}) || res.GetFile() != ".agents/djinn.yaml" ||
		res.GetDeveloperFile() != devFile || len(res.GetGatesOff()) != 0 {
		t.Fatalf("shown: %v, %v", res, err)
	}
	if !slices.ContainsFunc(res.GetWarnings(), func(w string) bool { return strings.HasPrefix(w, "gate lint: the name of one of the project's checks") }) ||
		!slices.ContainsFunc(res.GetWarnings(), func(w string) bool { return strings.HasPrefix(w, `gate db: the pattern "psql"`) }) {
		t.Errorf("warnings %q", res.GetWarnings())
	}
	if res, err := gates(proto.String("stack"), nil); err != nil || !slices.Equal(names(res), []string{"db"}) ||
		!slices.Equal(res.GetGatesOff(), []string{"stack"}) || len(res.GetOffGates()) != 1 ||
		res.GetOffGates()[0].GetName() != "stack" || len(res.GetOffGates()[0].GetAlone()) == 0 {
		// The page shows a gate turned off with its patterns, to turn it back on knowing what it holds (W111).
		t.Errorf("stack off: %v, %v", res, err)
	}
	if dev, err := ReadSettings(devFile); err != nil || !slices.Equal(dev.GetGatesOff(), []string{"stack"}) {
		t.Errorf("the developer's file: %v, %v", dev, err)
	}
	if _, err := gates(proto.String("e2e"), nil); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("a gate the project does not declare, off: %v", err)
	}
	if _, err := gates(proto.String("db"), proto.String("stack")); code(err) != connect.CodeInvalidArgument {
		t.Errorf("--off and --on at once: %v", err)
	}
	if res, err := gates(nil, proto.String("stack")); err != nil || !slices.Equal(names(res), []string{"stack", "db"}) {
		t.Errorf("stack back on: %v, %v", res, err)
	}
	if _, err := gates(nil, proto.String("stack")); code(err) != connect.CodeFailedPrecondition {
		t.Errorf("on twice: %v", err)
	}
	show, err := c.projects.Show(ctx, connect.NewRequest(&planv1.ProjectServiceShowRequest{Project: "dolmen"}))
	if err != nil || !slices.ContainsFunc(show.Msg.GetNotices(), func(n *planv1.ProjectNotice) bool {
		return strings.Contains(n.GetDetail(), "gates_off is ignored")
	}) {
		t.Errorf("the repository's gates_off is not said ignored: %v", err)
	}
}

// TestProjectGatesRead (W191, Q110): djinn project gates says where Djinn read the gates, the ref and its commit, and
// what the developer's config_branch changes: none named, origin/main without a djinn.yaml is said in a line, not left
// an answer of one field (W189); named, the gates of its branch; the branch gone, main again, said.
func TestProjectGatesRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	p := configProject(t, map[string]string{"README": "repo\n"})
	dir := p.GetDirectory()
	main := gitIn(t, dir, "rev-parse", "refs/remotes/origin/main")
	gitIn(t, dir, "checkout", "-q", "-b", "djinn/gates")
	writeFileAt(t, filepath.Join(dir, ProjectConfigFiles[1]), "gates:\n  - name: stack\n    alone: [task run-back]\n")
	branch := commitAs(t, dir, "refs/remotes/origin/djinn/gates")
	gitIn(t, dir, "checkout", "-q", "main")
	c.put(t, p)
	gates := func() *planv1.ProjectServiceGatesResponse {
		t.Helper()
		res, err := c.projects.Gates(ctx, connect.NewRequest(&planv1.ProjectServiceGatesRequest{Project: "repo"}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}

	// No config_branch, no djinn.yaml on origin/main.
	res := gates()
	if res.GetRef() != "origin/main" || res.GetCommit() != main || res.GetFile() != "" || len(res.GetGates()) != 0 ||
		res.GetConfigBranch() != "" || res.GetConfigBranchMissing() {
		t.Errorf("none named: %v", res)
	}
	if want := "No djinn.yaml nor .agents/djinn.yaml on origin/main (" + main[:10] + "): the project declares no gate. " +
		"You name no config_branch."; res.GetRead() != want {
		t.Errorf("none named: read %q, want %q", res.GetRead(), want)
	}

	// Named: its branch's gates.
	if err := SaveDeveloperConfigBranch(home, p.GetId(), "djinn/gates"); err != nil {
		t.Fatal(err)
	}
	res = gates()
	if res.GetRef() != "origin/djinn/gates" || res.GetCommit() != branch || res.GetFile() != ".agents/djinn.yaml" ||
		len(res.GetGates()) != 1 || res.GetGates()[0].GetName() != "stack" || res.GetConfigBranch() != "djinn/gates" ||
		res.GetConfigBranchMissing() {
		t.Errorf("named: %v", res)
	}
	if want := "Read .agents/djinn.yaml on origin/djinn/gates (" + branch[:10] + "). It is your config_branch djinn/gates, " +
		"to try it before it merges; the reference branch is origin/main."; res.GetRead() != want {
		t.Errorf("named: read %q, want %q", res.GetRead(), want)
	}

	// The branch merged and deleted: main again, said.
	gitIn(t, dir, "update-ref", "-d", "refs/remotes/origin/djinn/gates")
	res = gates()
	if res.GetRef() != "origin/main" || res.GetCommit() != main || res.GetFile() != "" || len(res.GetGates()) != 0 ||
		res.GetConfigBranch() != "djinn/gates" || !res.GetConfigBranchMissing() {
		t.Errorf("branch gone: %v", res)
	}
	if want := "No djinn.yaml nor .agents/djinn.yaml on origin/main (" + main[:10] + "): the project declares no gate. " +
		"Your config_branch djinn/gates is not on the remote (merged and deleted, or not fetched yet): Djinn reads the " +
		"reference branch instead."; res.GetRead() != want {
		t.Errorf("branch gone: read %q, want %q", res.GetRead(), want)
	}
}
