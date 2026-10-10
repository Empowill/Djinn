package testx

import "testing"

func TestPortable(t *testing.T) {
	ran := false
	t.Run("everything", func(t *testing.T) {
		t.Setenv(SystemOnly, "")
		Portable(t)
		ran = true
	})
	if !ran {
		t.Error("a portable test skipped without " + SystemOnly)
	}
	ran = false
	t.Run("system only", func(t *testing.T) {
		t.Setenv(SystemOnly, "1")
		Portable(t)
		ran = true
	})
	if ran {
		t.Error("a portable test ran with " + SystemOnly + "=1")
	}
}
