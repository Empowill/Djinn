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

// ContainersFile is where a project sets the runaway ceilings of the containers its workers start: a
// djinn.v1.Containers in text protobuf, beside its permissions.
var ContainersFile = filepath.Join(".agents", "containers.txtpb")

// ReadContainers reads the ceilings a project sets in dir, and the fields of the file a newer Djinn knows, which are
// ignored. It returns nil, and no error, when the project sets none.
func ReadContainers(dir string) (*djinnv1.Containers, []string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ContainersFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	c := &djinnv1.Containers{}
	unknowns := FindUnknownFields(b, c)
	name := filepath.ToSlash(ContainersFile)
	if err := (prototext.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, c); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := protovalidate.Validate(c); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return c, unknowns, nil
}
