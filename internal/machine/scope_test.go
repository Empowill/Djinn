package machine

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestCheckQuota: a scope's cpu.max holds the CPU cap asked, else the cap is off.
func TestCheckQuota(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cpuMax  string
		percent int
		ok      bool
	}{
		{"held", "150000 100000\n", 150, true},
		{"half a core", "50000 100000", 50, true},
		{"another cap", "200000 100000", 150, false},
		{"not applied", "max 100000", 150, false}, // a scope whose quota was not applied
		{"no cpu controller", "none", 150, false},
		{"empty", "", 150, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := checkQuota(c.cpuMax, c.percent); (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrNoCPULimit)) {
				t.Errorf("checkQuota(%q, %d) = %v", c.cpuMax, c.percent, err)
			}
		})
	}
}

// TestCheckMemoryMax: a scope's memory.max holds the ceiling asked, rounded down to a page, else the ceiling is off.
func TestCheckMemoryMax(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		memoryMax string
		ceiling   uint64
		ok        bool
	}{
		{"held", "4294967296\n", 4 << 30, true},
		{"rounded down to a page", "4294963200", 4<<30 - 1, true},
		{"not applied", "max", 4 << 30, false},
		{"no memory controller", "none", 4 << 30, false},
		{"another ceiling", "1073741824", 4 << 30, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := checkMemoryMax(c.memoryMax, c.ceiling); (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrNoMemoryLimit)) {
				t.Errorf("checkMemoryMax(%q, %d) = %v", c.memoryMax, c.ceiling, err)
			}
		})
	}
}

// testSlice is the slice of the scopes in the tests of Scopes.
const testSlice = "user.slice/user-1000.slice/user@1000.service/app.slice"

// scopePrefix is the command line that starts a process in the scope unit, with the caps given.
func scopePrefix(unit string, caps ...string) []string {
	p := []string{"systemd-run", "--user", "--scope", "--quiet", "--collect", "--unit=" + unit}
	for _, c := range caps {
		p = append(p, "-p", c)
	}
	return append(p, "--")
}

// TestScopes_New: the command line that starts a worker in its scope, a unit of its own each time, with the caps
// asked and none other; its cgroup in the probe's slice, none without one.
func TestScopes_New(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		scopes Scopes
		caps   []string
		cgroup bool
	}{
		{name: "uncapped", scopes: Scopes{slice: testSlice}, cgroup: true},
		{name: "capped", scopes: Scopes{slice: testSlice, CPU: 150, Memory: 512 << 20},
			caps: []string{"CPUQuota=150%", "MemoryMax=536870912"}, cgroup: true},
		{name: "no slice", scopes: Scopes{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a, b := c.scopes.New("W12"), c.scopes.New("W12")
			if !regexp.MustCompile(`^djinn-W12-[0-9a-f]{8}\.scope$`).MatchString(a.Unit) || a.Unit == b.Unit {
				t.Errorf("units %q and %q", a.Unit, b.Unit)
			}
			if want := scopePrefix(a.Unit, c.caps...); !slices.Equal(a.Prefix, want) {
				t.Errorf("prefix = %q, want %q", a.Prefix, want)
			}
			want := ""
			if c.cgroup {
				want = "/sys/fs/cgroup/" + testSlice + "/" + a.Unit
			}
			if a.Cgroup != want {
				t.Errorf("cgroup = %q, want %q", a.Cgroup, want)
			}
		})
	}
}

// TestScopes_NewWithMemory: the memory ceiling given replaces the scopes' own; 0 caps no memory.
func TestScopes_NewWithMemory(t *testing.T) {
	t.Parallel()
	s := &Scopes{slice: testSlice, CPU: 150, Memory: 512 << 20}
	cases := []struct {
		name   string
		memory uint64
		caps   []string
	}{
		{"a ceiling", 1 << 30, []string{"CPUQuota=150%", "MemoryMax=1073741824"}},
		{"none", 0, []string{"CPUQuota=150%"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := s.NewWithMemory("W12", c.memory)
			if want := scopePrefix(d.Unit, c.caps...); !slices.Equal(d.Prefix, want) {
				t.Errorf("NewWithMemory prefix = %q, want %q", d.Prefix, want)
			}
		})
	}
}

// TestScopes_Plain: a push check Djinn runs gets a djinn-gate-* scope beside the workers', never capped as they are
// (W347).
func TestScopes_Plain(t *testing.T) {
	t.Parallel()
	s := &Scopes{slice: testSlice, CPU: 150, Memory: 512 << 20}
	g := s.Plain("test")
	if !regexp.MustCompile(`^djinn-gate-test-[0-9a-f]{8}\.scope$`).MatchString(g.Unit) || !slices.Equal(g.Prefix, scopePrefix(g.Unit)) {
		t.Errorf("gate scope %q, prefix %q", g.Unit, g.Prefix)
	}
	if want := "/sys/fs/cgroup/" + testSlice + "/" + g.Unit; g.Cgroup != want {
		t.Errorf("gate cgroup %q, want %q", g.Cgroup, want)
	}
}

// TestScopes_Slice: the cgroup folder of the scopes; none without scopes or a probe.
func TestScopes_Slice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		scopes *Scopes
		want   string
	}{
		{"probed", &Scopes{slice: testSlice}, "/sys/fs/cgroup/" + testSlice},
		{"not probed", &Scopes{}, ""},
		{"no scopes", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.scopes.Slice(); got != c.want {
				t.Errorf("Slice = %q, want %q", got, c.want)
			}
		})
	}
}

// TestScopeUnit: a unit name keeps what a unit name holds of the worker's name.
func TestScopeUnit(t *testing.T) {
	t.Parallel()
	if u := scopeUnit("a b/\u00e9", "0a1b2c3d"); u != "djinn-a_b__-0a1b2c3d.scope" {
		t.Errorf("scopeUnit = %q", u)
	}
}

// TestReadProbe: what the probe scope printed gives the slice of the scopes, and the caps that hold.
func TestReadProbe(t *testing.T) {
	t.Parallel()
	const out = "/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-probe-0a1b2c3d.scope\n"
	cases := []struct {
		name    string
		scopes  Scopes
		printed string
		want    Scopes
		notes   int
		wantErr bool
	}{
		{name: "both caps held", scopes: Scopes{CPU: 150, Memory: 1 << 30}, printed: out + "150000 100000\n1073741824\n",
			want: Scopes{CPU: 150, Memory: 1 << 30, slice: testSlice}},
		// Ubuntu 22.04: systemd 249 delegates memory and pids, not cpu.
		{name: "no cpu controller", scopes: Scopes{CPU: 150, Memory: 1 << 30}, printed: out + "none\n1073741824\n",
			want: Scopes{Memory: 1 << 30, slice: testSlice}, notes: 1},
		{name: "no cap asked", printed: out + "none\nnone\n", want: Scopes{slice: testSlice}},
		{name: "nothing useful", printed: "sh: sed: not found\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := c.scopes
			notes, err := readProbe(c.printed, &s)
			if c.wantErr {
				if !errors.Is(err, ErrNoScope) {
					t.Errorf("a probe that printed nothing useful: %v", err)
				}
				return
			}
			if err != nil || len(notes) != c.notes || s.CPU != c.want.CPU || s.Memory != c.want.Memory || s.slice != c.want.slice {
				t.Errorf("readProbe = %+v, %q, %v; want %+v, %d notes", s, notes, err, c.want, c.notes)
			}
		})
	}
}

// fakeSystemdRun puts on PATH, alone, a systemd-run that runs script, and returns the file where it writes its
// arguments.
func fakeSystemdRun(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemd-run"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return args
}

// TestProbeScopes: on this machine, workers run in scopes, or Djinn says why not: no systemd, no user systemd, or
// another system. A cap that does not hold says why too. On Linux, without systemd-run, or with one that finds no
// user systemd, there are no scopes, and the error says why; with one that makes the scope, its cgroup gives the
// slice and the caps. Not parallel: the fake systemd-run is put on PATH.
func TestProbeScopes(t *testing.T) {
	const probed = "echo /user.slice/user-1000.slice/user@1000.service/app.slice/djinn-probe-0a1b2c3d.scope; "
	cases := []struct {
		name      string
		linuxOnly bool
		run       func(t *testing.T)
	}{
		{"this machine", false, func(t *testing.T) {
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
		}},
		{"no systemd-run", true, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			if s, _, err := ProbeScopes(t.Context(), 150, 0); s != nil || !errors.Is(err, ErrNoScope) ||
				!strings.Contains(err.Error(), "no systemd-run") {
				t.Errorf("without systemd-run: %+v, %v", s, err)
			}
		}},
		{"no user systemd", true, func(t *testing.T) {
			fakeSystemdRun(t, "echo 'Failed to connect to bus: No medium found' >&2; exit 1")
			if s, _, err := ProbeScopes(t.Context(), 0, 0); s != nil || !errors.Is(err, ErrNoScope) ||
				!strings.Contains(err.Error(), "Failed to connect to bus") {
				t.Errorf("without a user systemd: %+v, %v", s, err)
			}
		}},
		{"no cpu controller", true, func(t *testing.T) {
			args := fakeSystemdRun(t, probed+"echo none; echo 1073741824")
			s, notes, err := ProbeScopes(t.Context(), 150, 1<<30)
			if err != nil || s.CPU != 0 || s.Memory != 1<<30 || !s.MemoryController || len(notes) != 1 || !strings.Contains(notes[0], "cpu controller") {
				t.Fatalf("a user systemd without the cpu controller: %+v, %q, %v", s, notes, err)
			}
			b, err := os.ReadFile(args)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(b); !strings.HasPrefix(got, "--user\n--scope\n--quiet\n--collect\n--unit=djinn-probe-") ||
				!strings.Contains(got, "\n-p\nCPUQuota=150%\n-p\nMemoryMax=1073741824\n--\nsh\n-c\n") {
				t.Errorf("the probe ran systemd-run with %q", got)
			}
			if c := s.New("W3").Cgroup; !strings.HasPrefix(c, "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/djinn-W3-") {
				t.Errorf("a worker's cgroup: %q", c)
			}
		}},
		{"no memory controller", true, func(t *testing.T) {
			fakeSystemdRun(t, probed+"echo 150000 100000; echo none")
			s, notes, err := ProbeScopes(t.Context(), 150, 0)
			if err != nil || s.CPU != 150 || s.MemoryController || len(notes) != 0 {
				t.Fatalf("a user systemd without memory controller: %+v, %q, %v", s, notes, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.linuxOnly && runtime.GOOS != "linux" {
				t.Skip("systemd-run is Linux's")
			}
			c.run(t)
		})
	}
}
