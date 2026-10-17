package logx

import (
	"fmt"
	"strings"
	"testing"
)

// TestDump: the log folder keeps the last DumpsKept dumps.
func TestDump(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var paths []string
	for i := range DumpsKept + 2 {
		p, err := Dump(dir, fmt.Sprint("test ", i))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if got := Dumps(dir); strings.Join(got, ",") != strings.Join(paths[2:], ",") {
		t.Errorf("kept %v, want %v", got, paths[2:])
	}
}
