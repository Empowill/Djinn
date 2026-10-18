package machine

import (
	"path/filepath"
	"testing"
)

// TestReadDisk: the disk of a folder; a missing folder has none.
func TestReadDisk(t *testing.T) {
	t.Parallel()
	if _, err := ReadDisk(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("read the disk of a missing folder")
	}
}
