package machine

import (
	"errors"
	"regexp"
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
		{"none", 150, false},       // no cpu controller
		{"", 150, false},
	} {
		if err := checkQuota(tt.cpuMax, tt.percent); (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrNoCPULimit)) {
			t.Errorf("checkQuota(%q, %d) = %v", tt.cpuMax, tt.percent, err)
		}
	}
}

func TestCheckMemoryMax(t *testing.T) {
	for _, tt := range []struct {
		memoryMax string
		ceiling   uint64
		ok        bool
	}{
		{"4294967296\n", 4 << 30, true},
		{"4294963200", 4<<30 - 1, true}, // rounded down to a page
		{"max", 4 << 30, false},
		{"none", 4 << 30, false},
		{"1073741824", 4 << 30, false},
	} {
		if err := checkMemoryMax(tt.memoryMax, tt.ceiling); (err == nil) != tt.ok || (err != nil && !errors.Is(err, ErrNoMemoryLimit)) {
			t.Errorf("checkMemoryMax(%q, %d) = %v", tt.memoryMax, tt.ceiling, err)
		}
	}
}

// TestScopePrefix: the command line that starts a worker in its scope, a unit of its own each time, with the caps
// asked and none other; its cgroup in the probe's slice.
func TestScopePrefix(t *testing.T) {
	s := &Scopes{slice: "user.slice/user-1000.slice/user@1000.service/app.slice"}
	a, b := s.New("W12"), s.New("W12")
	if !regexp.MustCompile(`^djinn-W12-[0-9a-f]{8}\.scope$`).MatchString(a.Unit) || a.Unit == b.Unit {
		t.Errorf("units %q and %q", a.Unit, b.Unit)
	}
	want := []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + a.Unit, "--"}
	if !slices.Equal(a.Prefix, want) {
		t.Errorf("prefix = %q, want %q", a.Prefix, want)
	}
	if want := "/sys/fs/cgroup/" + s.slice + "/" + a.Unit; a.Cgroup != want {
		t.Errorf("cgroup = %q, want %q", a.Cgroup, want)
	}
	s.CPU, s.Memory = 150, 512<<20
	c := s.New("W12")
	want = []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + c.Unit,
		"-p", "CPUQuota=150%", "-p", "MemoryMax=536870912", "--"}
	if !slices.Equal(c.Prefix, want) {
		t.Errorf("capped prefix = %q, want %q", c.Prefix, want)
	}
	d := s.NewWithMemory("W12", 1<<30)
	want = []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + d.Unit,
		"-p", "CPUQuota=150%", "-p", "MemoryMax=1073741824", "--"}
	if !slices.Equal(d.Prefix, want) {
		t.Errorf("NewWithMemory prefix = %q, want %q", d.Prefix, want)
	}
	e := s.NewWithMemory("W12", 0)
	want = []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + e.Unit,
		"-p", "CPUQuota=150%", "--"}
	if !slices.Equal(e.Prefix, want) {
		t.Errorf("NewWithMemory 0 prefix = %q, want %q", e.Prefix, want)
	}
	if u := scopeUnit("a b/\u00e9", "0a1b2c3d"); u != "djinn-a_b__-0a1b2c3d.scope" {
		t.Errorf("scopeUnit = %q", u)
	}
	if (&Scopes{}).New("W1").Cgroup != "" {
		t.Error("a cgroup without a slice")
	}
}

// TestReadProbe: what the probe scope printed gives the slice of the scopes, and the caps that hold.
func TestReadProbe(t *testing.T) {
	const out = "/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-probe-0a1b2c3d.scope\n"
	s := &Scopes{CPU: 150, Memory: 1 << 30}
	notes, err := readProbe(out+"150000 100000\n1073741824\n", s)
	if err != nil || len(notes) != 0 || s.CPU != 150 || s.Memory != 1<<30 ||
		s.slice != "user.slice/user-1000.slice/user@1000.service/app.slice" {
		t.Errorf("both caps held: %+v, %q, %v", s, notes, err)
	}
	// Ubuntu 22.04: systemd 249 delegates memory and pids, not cpu.
	s = &Scopes{CPU: 150, Memory: 1 << 30}
	notes, err = readProbe(out+"none\n1073741824\n", s)
	if err != nil || len(notes) != 1 || s.CPU != 0 || s.Memory != 1<<30 {
		t.Errorf("no cpu controller: %+v, %q, %v", s, notes, err)
	}
	s = &Scopes{}
	if notes, err = readProbe(out+"none\nnone\n", s); err != nil || len(notes) != 0 || s.slice == "" {
		t.Errorf("no cap asked: %+v, %q, %v", s, notes, err)
	}
	if _, err := readProbe("sh: sed: not found\n", &Scopes{}); !errors.Is(err, ErrNoScope) {
		t.Errorf("a probe that printed nothing useful: %v", err)
	}
}

// TestProbeScopes: on this machine, workers run in scopes, or Djinn says why not: no systemd, no user systemd, or
// another system. A cap that does not hold says why too.
func TestProbeScopes(t *testing.T) {
	s, notes, err := ProbeScopes(t.Context(), 150, 0)
	switch {
	case err == nil:
		if runtime.GOOS != "linux" || s.slice == "" || (s.CPU == 0) == (len(notes) == 0) {
			t.Errorf("scopes %+v, notes %q on %s", s, notes, runtime.GOOS)
		}
		for _, n := range notes {
			t.Log(n)
		}
	case !errors.Is(err, ErrNoScope) || s != nil:
		t.Errorf("ProbeScopes = %+v, %v", s, err)
	default:
		t.Log(err)
	}
}
