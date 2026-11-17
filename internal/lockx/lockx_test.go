package lockx

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHold: a held lock refuses a second Hold of the same file, until it is released. Two Holds of one process
// conflict on Linux (an open file description lock) and Windows; a macOS record lock conflicts across processes only,
// which cmd/djinn's TestUpRefusesAHeldDataFolder covers.
func TestHold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "djinn.lock")
	first, err := Hold(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		if second, err := Hold(path); !errors.Is(err, ErrHeld) {
			second.Release() //nolint:errcheck // The test fails already.
			t.Fatalf("a second Hold of a held lock: %v, want ErrHeld", err)
		}
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Hold(path)
	if err != nil {
		t.Fatalf("Hold after Release: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
	if err := again.Release(); err != nil {
		t.Errorf("a second Release: %v", err)
	}
}
