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
}

// ReadProjectConfig reads Djinn's configuration of the project laid out in dir (ConfigDir): its YAML turned into
// JSON, then into the message, a field the message does not know refused. An error names the file, and the field when
// one is unknown. A project that writes none has neither a configuration nor an error.
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
	c, err := parseProjectConfig(b)
	if err != nil {
		return read, fmt.Errorf("%s: %w", read.File, err)
	}
	read.Config = c
	return read, nil
}

// protojsonPlace is where protojson places an error in the JSON it reads: a place in the JSON made from the YAML, which
// tells nothing of the file.
var protojsonPlace = regexp.MustCompile(`\(line \d+:\d+\): `)

// parseProjectConfig is the configuration a djinn.yaml holds in b: YAML, then JSON, then protojson.
func parseProjectConfig(b []byte) (*djinnv1.ProjectConfig, error) {
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{} // An empty file, or comments only.
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, errors.New("not a mapping of fields")
	}
	c := &djinnv1.ProjectConfig{}
	if field := unknownField(doc, c.ProtoReflect().Descriptor(), ""); field != "" {
		return nil, fmt.Errorf("unknown field %s", field)
	}
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := protojson.Unmarshal(j, c); err != nil {
		return nil, errors.New(protojsonPlace.ReplaceAllString(err.Error(), ""))
	}
	if err := protovalidate.Validate(c); err != nil {
		return nil, err
	}
	return c, nil
}

// unknownField is the first field of v, a value read from YAML, that the message md does not know, as its path in the
// file (containers.memroy_mib); empty when it knows them all. A field is known by its name or its JSON name, as
// protojson knows it.
func unknownField(v any, md protoreflect.MessageDescriptor, at string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return "" // Not a message: protojson says what is wrong with it.
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
			return path
		}
		sub := fd.Message()
		if sub == nil || fd.IsMap() || strings.HasPrefix(string(sub.FullName()), "google.protobuf.") {
			continue
		}
		if fd.IsList() {
			list, _ := m[k].([]any)
			for i, e := range list {
				if f := unknownField(e, sub, fmt.Sprintf("%s[%d]", path, i)); f != "" {
					return f
				}
			}
			continue
		}
		if f := unknownField(m[k], sub, path); f != "" {
			return f
		}
	}
	return ""
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
