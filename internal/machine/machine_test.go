package machine

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

const meminfo = `MemTotal:       32466216 kB
MemFree:         1530840 kB
MemAvailable:   19827652 kB
Cached:          9000000 kB
`

// TestReadProc reads a simulated /proc: memory, load, and pressure when the kernel has PSI.
func TestReadProc(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/meminfo": {Data: []byte(meminfo)},
		"proc/loadavg": {Data: []byte("3.52 2.10 1.99 2/1500 12345\n")},
		"proc/pressure/cpu": {Data: []byte("some avg10=61.50 avg60=20.00 avg300=4.00 total=444300079\n" +
			"full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")},
		"proc/pressure/memory": {Data: []byte("some avg10=0.10 avg60=0.06 avg300=0.04 total=5378695\n" +
			"full avg10=0.05 avg60=0.06 avg300=0.04 total=4732309\n")},
	}
	s, err := ReadProc(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if s.MemoryTotal != 32466216<<10 || s.MemoryAvailable != 19827652<<10 || !s.LoadKnown || s.Load1 != 3.52 {
		t.Errorf("snapshot = %+v", s)
	}
	if s.CPU == nil || s.CPU.Some != 61.5 || s.Memory == nil || s.Memory.Some != 0.1 || s.Memory.Full != 0.05 {
		t.Errorf("pressure = %+v %+v", s.CPU, s.Memory)
	}

	// No PSI, an old kernel without MemAvailable: free memory and cache stand in, the pressure is unknown.
	delete(fsys, "proc/pressure/cpu")
	delete(fsys, "proc/pressure/memory")
	fsys["proc/meminfo"] = &fstest.MapFile{Data: []byte("MemTotal: 1000 kB\nMemFree: 100 kB\nCached: 200 kB\n")}
	s, err = ReadProc(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU != nil || s.Memory != nil || s.MemoryAvailable != 300<<10 {
		t.Errorf("without PSI: %+v", s)
	}

	// A broken pressure file is an error, not a silent zero.
	fsys["proc/pressure/cpu"] = &fstest.MapFile{Data: []byte("some avg10=x\n")}
	if _, err := ReadProc(fsys); err == nil {
		t.Error("a broken pressure file was read")
	}
	if _, err := ReadProc(fstest.MapFS{}); err == nil {
		t.Error("read a /proc without meminfo")
	}
}

func TestSlots(t *testing.T) {
	p := DefaultPolicy()
	for _, tt := range []struct {
		name   string
		cores  int
		memory uint64
		want   int
		rule   string
	}{
		{"this laptop", 16, 32 * GiB, 8, "one per 2 cores of 16"},
		{"little memory", 16, 8 * GiB, 3, "one per 2.0 GiB of memory beyond 2.0 GiB, of 8.0 GiB"},
		{"one core", 1, 0, 1, "one per 2 cores of 1"},
		{"tiny", 2, GiB, 1, "1: one per 2 cores of 2, and one per"},
		{"a big server", 128, 512 * GiB, 16, "16, the most Djinn runs"},
	} {
		n, rule := p.Slots(Snapshot{Cores: tt.cores, MemoryTotal: tt.memory})
		if n != tt.want || !strings.Contains(rule, tt.rule) {
			t.Errorf("%s: %d (%s), want %d (%s)", tt.name, n, rule, tt.want, tt.rule)
		}
	}
	p.Workers = 3
	if n, rule := p.Slots(Snapshot{Cores: 64}); n != 3 || !strings.Contains(rule, "--workers") {
		t.Errorf("set by hand: %d (%s)", n, rule)
	}
}

func TestPressure(t *testing.T) {
	p := DefaultPolicy()
	for _, tt := range []struct {
		name string
		s    Snapshot
		want string
	}{
		{"calm", Snapshot{Cores: 8, CPU: &Pressure{Some: 10}, Memory: &Pressure{Some: 1}, LoadKnown: true, Load1: 40}, ""},
		{"cpu", Snapshot{Cores: 8, CPU: &Pressure{Some: 70}, Memory: &Pressure{}}, "waited for the CPU 70%"},
		{"memory", Snapshot{Cores: 8, CPU: &Pressure{}, Memory: &Pressure{Some: 12}}, "waited for memory 12%"},
		{"macOS level", Snapshot{Cores: 8, MemoryLevel: 2, MemoryTotal: 16 * GiB}, "memory pressure"},
		{"macOS calm", Snapshot{Cores: 8, MemoryLevel: 1, MemoryTotal: 16 * GiB, MemoryAvailable: GiB / 2}, ""},
		{"load without PSI", Snapshot{Cores: 4, LoadKnown: true, Load1: 9}, "load is 9.0 on 4 cores"},
		{"memory without PSI", Snapshot{Cores: 4, MemoryTotal: 16 * GiB, MemoryAvailable: GiB}, "1.0 GiB of memory available"},
	} {
		if got := p.Pressure(tt.s); (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestMonitor reads the machine once a second at most, and keeps the cores when reading fails.
func TestMonitor(t *testing.T) {
	reads := 0
	m := NewMonitor(DefaultPolicy(), func() (Snapshot, error) {
		reads++
		return Snapshot{Cores: 4, MemoryTotal: 64 * GiB, CPU: &Pressure{Some: 90}}, nil
	})
	slots, _, pressure := m.Capacity()
	m.Snapshot()
	if slots != 2 || pressure == "" || reads != 1 {
		t.Errorf("slots %d, pressure %q, %d reads", slots, pressure, reads)
	}
	failing := NewMonitor(DefaultPolicy(), func() (Snapshot, error) { return Snapshot{}, errors.New("no /proc") })
	if s := failing.Snapshot(); s.Cores == 0 || s.OS == "" {
		t.Errorf("after a failed read: %+v", s)
	}
}

// TestReadThisMachine reads the real machine: whatever it is, it has cores, and memory where Djinn reads it.
func TestReadThisMachine(t *testing.T) {
	s := NewMonitor(DefaultPolicy(), nil).Snapshot()
	if s.Cores < 1 {
		t.Errorf("no cores: %+v", s)
	}
	if (s.OS == "linux" || s.OS == "darwin" || s.OS == "windows") && s.MemoryTotal == 0 {
		t.Errorf("no memory: %+v", s)
	}
}
