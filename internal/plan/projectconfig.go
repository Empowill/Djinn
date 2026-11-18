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
	"google.golang.org/protobuf/proto"
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
	// Settings are the settings the team shares, its settings: section (plan.v1.ProjectSettings); nil when the file
	// has none, or is unreadable. The developer's own file wins over them, setting by setting (ResolveSettings).
	Settings *planv1.ProjectSettings
	// File is the file read, relative to the project, with slashes: djinn.yaml or .agents/djinn.yaml; empty when
	// there is none.
	File string
	// Ignored is the other file, present beside File but not read; empty when there is none.
	Ignored string
	// Unknown are the fields of File the message does not know, as their paths in the file (containers.memroy_mib,
	// settings.setpu), sorted: ignored, the rest of the file applies (Q21).
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
// outside the gates, a version of the file this Djinn does not read (upgradeProjectConfig), and none of the file
// applies. A project that writes none has neither a configuration nor an error.
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
	c, settings, unknown, gates, err := parseProjectConfig(b)
	read.Unknown, read.Dropped, read.Broad = unknown, gates.dropped, gates.broad
	if err != nil {
		return read, fmt.Errorf("%s: %w", read.File, err)
	}
	read.Config, read.Settings = c, settings
	return read, nil
}

// protojsonPlace is where protojson places an error in the JSON it reads: a place in the JSON made from the YAML, which
// tells nothing of the file.
var protojsonPlace = regexp.MustCompile(`\(line \d+:\d+\): `)

// parseProjectConfig is the configuration a djinn.yaml holds in b: YAML, then JSON, then protojson; the settings of
// its settings: section, read the same way (readSettingsSection); the fields the messages do not know, dropped before
// protojson reads them, said even when the file is refused; and the gates left out, each read and checked on its own
// (readGates). A file that holds something that looks like a secret is refused, comments included, without repeating
// it: the file is versioned.
func parseProjectConfig(b []byte) (*djinnv1.ProjectConfig, *planv1.ProjectSettings, []string, gatesRead, error) {
	var gates gatesRead
	if line, why := secretIn(b); line > 0 {
		return nil, nil, nil, gates, fmt.Errorf("line %d %s; this file holds no secret: remove it", line, why)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, nil, gates, err
	}
	if doc == nil {
		doc = map[string]any{} // An empty file, or comments only.
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, nil, nil, gates, errors.New("not a mapping of fields")
	}
	if err := upgradeProjectConfig(m); err != nil {
		return nil, nil, nil, gates, err
	}
	c := &djinnv1.ProjectConfig{}
	rawSettings, hasSettings := m[settingsSection]
	delete(m, settingsSection)
	unknown := dropUnknownFields(doc, c.ProtoReflect().Descriptor(), "", nil)
	var settings *planv1.ProjectSettings
	if hasSettings {
		var err error
		settings, unknown, err = readSettingsSection(rawSettings, unknown)
		slices.Sort(unknown)
		if err != nil {
			return nil, nil, unknown, gates, fmt.Errorf("%s: %w", settingsSection, err)
		}
	}
	rawGates, hasGates := m["gates"]
	delete(m, "gates")
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, unknown, gates, err
	}
	if err := protojson.Unmarshal(j, c); err != nil {
		return nil, nil, unknown, gates, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if err := protovalidate.Validate(c); err != nil {
		return nil, nil, unknown, gates, err
	}
	if hasGates {
		c.Gates, gates = readGates(rawGates)
	}
	return c, settings, unknown, gates, nil
}

// ProjectConfigVersion is the highest version of djinn.yaml this Djinn reads: the version the parser reads, one more
// than the upgrades that lift an older file to it (projectConfigUpgrades).
var ProjectConfigVersion = len(projectConfigUpgrades) + 1

// projectConfigUpgrades lift a djinn.yaml, as YAML gives it, from one version to the next: the one at index i turns a
// file of version i+1 into one of version i+2, in place, or says why it cannot. A schema that changes what a field of
// the file means adds the upgrade from the version before it here, and the parser reads only the newest version.
// None yet: version 1 is the first.
var projectConfigUpgrades []func(m map[string]any) error

// upgradeProjectConfig reads the version of m, a djinn.yaml as YAML gives it (version:, 1 when it is missing or empty),
// and lifts m to ProjectConfigVersion through projectConfigUpgrades; m's version then says that version. A version
// that is not a whole number from 1, or higher than ProjectConfigVersion, is an error that says the version read and
// the highest known: the file is not read rather than read wrong.
func upgradeProjectConfig(m map[string]any) error {
	version := 1
	switch v := m["version"].(type) {
	case nil:
	case int:
		version = v
		if v < 1 {
			return fmt.Errorf("version %d: not a version, one of 1 to %d, the highest this Djinn reads", v,
				ProjectConfigVersion)
		}
	case uint64: // Past what an int holds.
		return fmt.Errorf("version %d: this Djinn reads djinn.yaml up to version %d; update Djinn to read it", v,
			ProjectConfigVersion)
	case string:
		return fmt.Errorf("version %q: not a version, one of 1 to %d, the highest this Djinn reads", v,
			ProjectConfigVersion)
	default:
		return fmt.Errorf("version %v: not a version, one of 1 to %d, the highest this Djinn reads", v,
			ProjectConfigVersion)
	}
	if version > ProjectConfigVersion {
		return fmt.Errorf("version %d: this Djinn reads djinn.yaml up to version %d; update Djinn to read it", version,
			ProjectConfigVersion)
	}
	for ; version < ProjectConfigVersion; version++ {
		if err := projectConfigUpgrades[version-1](m); err != nil {
			return fmt.Errorf("version %d to %d: %w", version, version+1, err)
		}
	}
	m["version"] = version
	return nil
}

// settingsSection is the key of a djinn.yaml that holds the settings the team shares (plan.v1.ProjectSettings),
// apart from djinn.v1.ProjectConfig: plan.v1 imports djinn.v1, which cannot name it back.
const settingsSection = "settings"

// readSettingsSection reads v, the settings: section of a djinn.yaml as YAML gives it, into the team's settings: the
// fields ProjectSettings does not know are dropped and appended to unknown, under settings.; the short forms YAML
// invites are taken (normalizeYAML); the values are checked as the developer's file is (protovalidate, two checks of
// one name). A section that is empty holds no setting.
func readSettingsSection(v any, unknown []string) (*planv1.ProjectSettings, []string, error) {
	s := &planv1.ProjectSettings{}
	if v == nil {
		return s, unknown, nil
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, unknown, errors.New("not a mapping of settings")
	}
	md := s.ProtoReflect().Descriptor()
	unknown = dropUnknownFields(v, md, settingsSection, unknown)
	normalizeYAML(v, md)
	j, err := json.Marshal(v)
	if err != nil {
		return nil, unknown, err
	}
	if err := protojson.Unmarshal(j, s); err != nil {
		return nil, unknown, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if err := protovalidate.Validate(s); err != nil {
		return nil, unknown, err
	}
	if err := uniqueCheckNames(s); err != nil {
		return nil, unknown, err
	}
	return s, unknown, nil
}

// normalizeYAML turns, in v, a message md as YAML gives it, the short forms a person writes in YAML into what
// protojson reads: one value where a list is expected becomes a list of it (when: commit); an enum value written by
// the end of its name, in any case, becomes its name (commit for CHECK_WHEN_COMMIT, never for
// INSTALL_QUESTIONS_NEVER), when one value alone ends so; a number or a boolean where a string is expected becomes
// its text (model: 4.5). What does not fit is left as it is, and protojson says what is wrong with it.
func normalizeYAML(v any, md protoreflect.MessageDescriptor) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k, e := range m {
		fd := md.Fields().ByName(protoreflect.Name(k))
		if fd == nil {
			fd = md.Fields().ByJSONName(k)
		}
		if fd == nil || fd.IsMap() {
			continue
		}
		if !fd.IsList() {
			m[k] = normalizeYAMLValue(e, fd)
			continue
		}
		list, ok := e.([]any)
		if !ok {
			if e == nil {
				continue
			}
			list = []any{e}
		}
		for i := range list {
			list[i] = normalizeYAMLValue(list[i], fd)
		}
		m[k] = list
	}
}

// normalizeYAMLValue is e, one value of the field fd as YAML gives it, in the form protojson reads (normalizeYAML).
func normalizeYAMLValue(e any, fd protoreflect.FieldDescriptor) any {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		s, ok := e.(string)
		if !ok || fd.Enum().Values().ByName(protoreflect.Name(s)) != nil {
			return e
		}
		short := "_" + strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
		var found []string
		values := fd.Enum().Values()
		for i := range values.Len() {
			if name := string(values.Get(i).Name()); strings.HasSuffix(name, short) {
				found = append(found, name)
			}
		}
		if len(found) == 1 {
			return found[0]
		}
	case protoreflect.StringKind:
		switch e.(type) {
		case bool, int, int64, uint64, float64:
			return fmt.Sprint(e)
		}
	case protoreflect.MessageKind:
		if !strings.HasPrefix(string(fd.Message().FullName()), "google.protobuf.") {
			normalizeYAML(e, fd.Message())
		}
	}
	return e
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
// next, two folders laid out as a project's: a line each. A file whose change is all in its settings: section or its
// comments, or that does not read, says none here: settingsLines say what changed in the team's settings, or that
// Djinn can no longer read them.
func projectConfigLines(old, next string) []*planv1.ConfigChangeLine {
	var out []*planv1.ConfigChangeLine
	for _, f := range ProjectConfigFiles {
		a, errA := os.ReadFile(filepath.Join(old, f))
		b, errB := os.ReadFile(filepath.Join(next, f))
		if (errA != nil) == (errB != nil) && bytes.Equal(a, b) {
			continue
		}
		ca, parsedA := configOf(a, errA)
		cb, parsedB := configOf(b, errB)
		if !parsedA || !parsedB || proto.Equal(ca, cb) {
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

// configOf is the configuration of a djinn.yaml, b as read with err, without its settings: an empty one when the file
// is missing, of the version a file read holds (ProjectConfigVersion); parsed is false when it does not read.
func configOf(b []byte, err error) (c *djinnv1.ProjectConfig, parsed bool) {
	if err != nil {
		return &djinnv1.ProjectConfig{Version: uint32(ProjectConfigVersion)}, errors.Is(err, fs.ErrNotExist)
	}
	c, _, _, _, err = parseProjectConfig(b)
	return c, err == nil
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
	// OffGates are the valid gates the developer turns off, as the file declares them, in its order.
	OffGates []*djinnv1.ProjectGate
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
		if slices.Contains(out.Off, g.GetName()) {
			out.OffGates = append(out.OffGates, g)
		} else {
			out.Gates = append(out.Gates, g)
		}
	}
	out.Warnings = append(out.Warnings, read.Broad...)
	return out
}
