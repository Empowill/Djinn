package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/prototext"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// PermissionsFile is where a project declares, for every agent, what an agent may do in it: a djinn.v1.Permissions
// in text protobuf, under the .agents folder that Codex and Antigravity already read for skills.
var PermissionsFile = filepath.Join(".agents", "permissions.txtpb")

// ReadPermissions reads the permissions a project declares in dir, and the fields of the file a newer Djinn knows,
// which are ignored. It returns nil, and no error, when the project declares none.
func ReadPermissions(dir string) (*djinnv1.Permissions, []string, error) {
	path := filepath.Join(dir, PermissionsFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	p := &djinnv1.Permissions{}
	unknowns := FindUnknownFields(b, p)
	opts := prototext.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(b, p); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", PermissionsFile, err)
	}
	if err := protovalidate.Validate(p); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", PermissionsFile, err)
	}
	return p, unknowns, nil
}
