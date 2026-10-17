package tones

import (
	"strings"
	"testing"
)

// TestTonesOneLineEach: each tone is one line of its own file, editable at a glance, and short: a batch's message
// carries it whole. Measured on Opus 5.5 (W156, 17/10/2026), the light section of a batch's message is 127 tokens and
// the dark one 145; the bound keeps a tone under about 170.
func TestTonesOneLineEach(t *testing.T) {
	for name, text := range map[string]string{"light.md": light, "dark.md": dark} {
		line := strings.TrimSuffix(text, "\n")
		if strings.TrimSpace(line) == "" || strings.Contains(line, "\n") {
			t.Errorf("%s is not one line: %q", name, text)
		}
		if n := len([]rune(line)); n > 400 {
			t.Errorf("%s is %d characters, over 400: each batch pays for it", name, n)
		}
	}
	if Light() == Dark() {
		t.Errorf("the two tones are the same")
	}
}
