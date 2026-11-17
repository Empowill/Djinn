package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// ProjectConfigFiles are where a project writes Djinn's configuration (djinn.v1.ProjectConfig), in YAML, in the order
// Djinn looks: the first found is read, the other is not.
var ProjectConfigFiles = []string{"djinn.yaml", filepath.Join(".agents", "djinn.yaml")}

// ProjectConfigRead is what ReadProjectConfig found.
type ProjectConfigRead struct {
	// Config is the configuration read; nil when the project writes none, or when its file is unreadable.
	Config *djinnv1.ProjectConfig
	// File is the file read, relative to the project, with slashes: djinn.yaml or .agents/djinn.yaml; empty when
	// there is none.
	File string
	// Ignored is the other file, present beside File but not read; empty when there is none.
	Ignored string
	// Unknown are the fields of File the message does not know, as their paths in the file (containers.memroy_mib),
	// sorted: ignored, the rest of the file applies (Q21).
	Unknown []string
	// Dropped say each gate left out, by its name or its place, and why: the other gates and the rest of the file
	// apply.
	Dropped []string
	// Broad say each pattern of one word a gate keeps: it takes every command of that program in the project.
	Broad []string
}

// ReadProjectConfig reads Djinn's configuration of the project laid out in dir (ConfigDir): its YAML turned into
// JSON, then into the message. A field the message does not know is ignored, and named in Unknown; the rest of the
// file applies. A gate that is not valid is left out on its own, and said in Dropped; the other gates and the rest
// of the file apply. An error names the file: a file that does not parse, a value of the wrong type or out of range
// outside the gates, and none of the file applies. A project that writes none has neither a configuration nor an
// error.
func ReadProjectConfig(dir string) (ProjectConfigRead, error) {
	var read ProjectConfigRead
	var b []byte
	for _, f := range ProjectConfigFiles {
		content, err := os.ReadFile(filepath.Join(dir, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		name := filepath.ToSlash(f)
		if read.File != "" {
			read.Ignored = name
			break
		}
		read.File = name
		if err != nil {
			return read, fmt.Errorf("%s: %w", name, err)
		}
		b = content
	}
	if read.File == "" {
		return read, nil
	}
	c, unknown, gates, err := parseProjectConfig(b)
	read.Unknown, read.Dropped, read.Broad = unknown, gates.dropped, gates.broad
	if err != nil {
		return read, fmt.Errorf("%s: %w", read.File, err)
	}
	read.Config = c
	return read, nil
}

// protojsonPlace is where protojson places an error in the JSON it reads: a place in the JSON made from the YAML, which
// tells nothing of the file.
var protojsonPlace = regexp.MustCompile(`\(line \d+:\d+\): `)

// parseProjectConfig is the configuration a djinn.yaml holds in b: YAML, then JSON, then protojson; the fields the
// message does not know, dropped before protojson reads it, said even when the file is refused; and the gates left
// out, each read and checked on its own (readGates).
func parseProjectConfig(b []byte) (*djinnv1.ProjectConfig, []string, gatesRead, error) {
	var gates gatesRead
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, gates, err
	}
	if doc == nil {
		doc = map[string]any{} // An empty file, or comments only.
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, nil, gates, errors.New("not a mapping of fields")
	}
	c := &djinnv1.ProjectConfig{}
	unknown := dropUnknownFields(doc, c.ProtoReflect().Descriptor(), "", nil)
	rawGates, hasGates := m["gates"]
	delete(m, "gates")
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, unknown, gates, err
	}
	if err := protojson.Unmarshal(j, c); err != nil {
		return nil, unknown, gates, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if err := protovalidate.Validate(c); err != nil {
		return nil, unknown, gates, err
	}
	if hasGates {
		c.Gates, gates = readGates(rawGates)
	}
	return c, unknown, gates, nil
}

// MaxProjectGates is how many gates a project declares at most: those past it are left out.
const MaxProjectGates = 8

// gatesRead is what readGates says of a project's gates.
type gatesRead struct {
	dropped, broad []string
}

// readGates reads the gates of a djinn.yaml, v as YAML gives it, each on its own: one that does not read or is not
// valid (protovalidate), named twice, or past MaxProjectGates is left out, said in dropped by its name, or its place
// when it has none; the others are kept. A pattern of one word is kept, and said in broad.
func readGates(v any) ([]*djinnv1.ProjectGate, gatesRead) {
	var out gatesRead
	list, ok := v.([]any)
	if !ok {
		if v != nil {
			out.dropped = append(out.dropped, "gates: not a list of gates")
		}
		return nil, out
	}
	var gates []*djinnv1.ProjectGate
	for i, e := range list {
		label := fmt.Sprintf("gates[%d]", i)
		if m, ok := e.(map[string]any); ok {
			if name, ok := m["name"].(string); ok && name != "" {
				label = "gate " + name
			}
		}
		g, err := readGate(e)
		switch {
		case err != nil:
			out.dropped = append(out.dropped, label+": "+err.Error())
			continue
		case slices.ContainsFunc(gates, func(o *djinnv1.ProjectGate) bool { return o.GetName() == g.GetName() }):
			out.dropped = append(out.dropped, label+": declared twice, the first is kept")
			continue
		case len(gates) == MaxProjectGates:
			out.dropped = append(out.dropped, fmt.Sprintf("%s: past the %d gates a project declares at most", label, MaxProjectGates))
			continue
		}
		for _, p := range slices.Concat(g.GetAlone(), g.GetBeside()) {
			if len(strings.Fields(p)) == 1 {
				out.broad = append(out.broad, fmt.Sprintf("gate %s: the pattern %q takes every command %s of the project", g.GetName(), p, p))
			}
		}
		gates = append(gates, g)
	}
	return gates, out
}

// readGate is the gate e holds, a value of YAML, read and checked.
func readGate(e any) (*djinnv1.ProjectGate, error) {
	j, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	g := &djinnv1.ProjectGate{}
	if err := protojson.Unmarshal(j, g); err != nil {
		return nil, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if g.GetName() == "" {
		return nil, errors.New("a gate has a name")
	}
	if err := protovalidate.Validate(g); err != nil {
		return nil, err
	}
	return g, nil
}

// dropUnknownFields removes from v, a value read from YAML, the fields the message md does not know, and appends to
// out their paths in the file (containers.memroy_mib), keys sorted at each level. A field is known by its
// name or its JSON name, as protojson knows it.
func dropUnknownFields(v any, md protoreflect.MessageDescriptor, at string, out []string) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return out // Not a message: protojson says what is wrong with it.
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		path := k
		if at != "" {
			path = at + "." + k
		}
		fd := md.Fields().ByName(protoreflect.Name(k))
		if fd == nil {
			fd = md.Fields().ByJSONName(k)
		}
		if fd == nil {
			delete(m, k)
			out = append(out, path)
			continue
		}
		sub := fd.Message()
		if sub == nil || fd.IsMap() || strings.HasPrefix(string(sub.FullName()), "google.protobuf.") {
			continue
		}
		if fd.IsList() {
			list, _ := m[k].([]any)
			for i, e := range list {
				out = dropUnknownFields(e, sub, fmt.Sprintf("%s[%d]", path, i), out)
			}
			continue
		}
		out = dropUnknownFields(m[k], sub, path, out)
	}
	return out
}

// projectConfigLines say which of the project's djinn.yaml files changed from the configuration in old to the one in
// next, two folders laid out as a project's: a line each.
func projectConfigLines(old, next string) []*planv1.ConfigChangeLine {
	var out []*planv1.ConfigChangeLine
	for _, f := range ProjectConfigFiles {
		a, errA := os.ReadFile(filepath.Join(old, f))
		b, errB := os.ReadFile(filepath.Join(next, f))
		if (errA != nil) == (errB != nil) && bytes.Equal(a, b) {
			continue
		}
		name := filepath.ToSlash(f)
		text := "Djinn's configuration changes in " + name + "."
		switch {
		case errA != nil:
			text = "Djinn's configuration is new in " + name + "."
		case errB != nil:
			text = "Djinn's configuration in " + name + " is removed."
		}
		out = append(out, &planv1.ConfigChangeLine{Topic: topicSettings, Subject: name, Text: text})
	}
	return out
}

// ProjectGates are the gates a project declares in its djinn.yaml, as Djinn applies them on this machine
// (LoadProjectGates).
type ProjectGates struct {
	// Gates are the valid gates, in the file's order, those the developer turns off left out.
	Gates []*djinnv1.ProjectGate
	// File is the file that declares them, relative to the project, with slashes; empty when there is none.
	File string
	// Off are the gates the developer turns off, from their own settings file (gates_off).
	Off []string
	// Warnings say what is wrong in the declaration: the file unread, a gate left out and why, a pattern of one word.
	Warnings []string
}

// LoadProjectGates reads the gates project declares, where Djinn reads its configuration (ConfigDir): its djinn.yaml
// on the reference branch, never a task's worktree. A gate named as one of the project's checks is left out, as an
// invalid one is (ReadProjectConfig); one the developer turns off (gates_off) is not applied. A file Djinn cannot read
// declares no gate.
func LoadProjectGates(home string, project *planv1.Project) ProjectGates {
	var out ProjectGates
	dir := ConfigDir(home, project)
	if dir == "" {
		return out
	}
	read, err := ReadProjectConfig(dir)
	out.File = read.File
	if err != nil {
		out.Warnings = append(out.Warnings, err.Error()+": no gate is declared")
		return out
	}
	settings, _ := LoadSettings(home, project) // A settings file that does not read keeps the defaults: no check, no gate off.
	out.Off = settings.GatesOff
	out.Warnings = append(out.Warnings, read.Dropped...)
	for _, g := range read.Config.GetGates() {
		if slices.ContainsFunc(settings.Checks, func(c *planv1.ProjectCheck) bool { return strings.ToLower(c.GetName()) == g.GetName() }) {
			out.Warnings = append(out.Warnings, "gate "+g.GetName()+": the name of one of the project's checks, which "+
				"holds the gate of its name: left out")
			continue
		}
		if !slices.Contains(out.Off, g.GetName()) {
			out.Gates = append(out.Gates, g)
		}
	}
	out.Warnings = append(out.Warnings, read.Broad...)
	return out
}
