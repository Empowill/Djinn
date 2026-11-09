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
}

// ReadProjectConfig reads Djinn's configuration of the project laid out in dir (ConfigDir): its YAML turned into
// JSON, then into the message. A field the message does not know is ignored, and named in Unknown; the rest of the
// file applies. An error names the file: a file that does not parse, a value of the wrong type or out of range, and
// none of the file applies. A project that writes none has neither a configuration nor an error.
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
	c, unknown, err := parseProjectConfig(b)
	read.Unknown = unknown
	if err != nil {
		return read, fmt.Errorf("%s: %w", read.File, err)
	}
	read.Config = c
	return read, nil
}

// protojsonPlace is where protojson places an error in the JSON it reads: a place in the JSON made from the YAML, which
// tells nothing of the file.
var protojsonPlace = regexp.MustCompile(`\(line \d+:\d+\): `)

// parseProjectConfig is the configuration a djinn.yaml holds in b: YAML, then JSON, then protojson; and the fields
// the message does not know, dropped before protojson reads it. Those are said even when the file is refused.
func parseProjectConfig(b []byte) (*djinnv1.ProjectConfig, []string, error) {
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, err
	}
	if doc == nil {
		doc = map[string]any{} // An empty file, or comments only.
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, nil, errors.New("not a mapping of fields")
	}
	c := &djinnv1.ProjectConfig{}
	unknown := dropUnknownFields(doc, c.ProtoReflect().Descriptor(), "", nil)
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, unknown, err
	}
	if err := protojson.Unmarshal(j, c); err != nil {
		return nil, unknown, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if err := protovalidate.Validate(c); err != nil {
		return nil, unknown, err
	}
	return c, unknown, nil
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
