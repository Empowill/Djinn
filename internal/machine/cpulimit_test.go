package machine

import (
	"errors"
	"runtime"
	"slices"
	"testing"
)

func TestCheckQuota(t *testing.T) {
	for _, tt := range []struct {
		cpuMax  string
		percent int
		ok      bool
	}{
		{"150000 100000\n", 150, true},
		{"50000 100000", 50, true},
		{"200000 100000", 150, false},
		{"max 100000", 150, false}, // a scope whose quota was not applied
		{"", 150, false},
	} {
		if err := checkQuota(tt.cpuMax, tt.percent); (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrNoCPULimit)) {
			t.Errorf("checkQuota(%q, %d) = %v", tt.cpuMax, tt.percent, err)
		}
	}
	if got := scopePrefix(150); !slices.Equal(got, []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "-p", "CPUQuota=150%", "--"}) {
		t.Errorf("scopePrefix = %q", got)
	}
}

// TestCPULimit: on this machine, a scope caps the CPU, or Djinn says why not: no systemd, a systemd that does not
// delegate the cpu controller to the user, or another system.
func TestCPULimit(t *testing.T) {
	if _, err := CPULimit(t.Context(), 0); !errors.Is(err, ErrNoCPULimit) {
		t.Errorf("a limit of 0: %v", err)
	}
	prefix, err := CPULimit(t.Context(), 150)
	switch {
	case err == nil:
		if runtime.GOOS != "linux" || !slices.Equal(prefix, scopePrefix(150)) {
			t.Errorf("prefix %q on %s", prefix, runtime.GOOS)
		}
	case !errors.Is(err, ErrNoCPULimit) || prefix != nil:
		t.Errorf("CPULimit = %q, %v", prefix, err)
	default:
		t.Log(err)
	}
}
