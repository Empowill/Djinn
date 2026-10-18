package machine

import (
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
	t.Parallel()
	psi := fstest.MapFS{
		"proc/meminfo": {Data: []byte(meminfo)},
		"proc/loadavg": {Data: []byte("3.52 2.10 1.99 2/1500 12345\n")},
		"proc/pressure/cpu": {Data: []byte("some avg10=61.50 avg60=20.00 avg300=4.00 total=444300079\n" +
			"full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")},
		"proc/pressure/memory": {Data: []byte("some avg10=0.10 avg60=0.06 avg300=0.04 total=5378695\n" +
			"full avg10=0.05 avg60=0.06 avg300=0.04 total=4732309\n")},
	}
	// No PSI, an old kernel without MemAvailable.
	old := fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 1000 kB\nMemFree: 100 kB\nCached: 200 kB\n")}}
	broken := fstest.MapFS{
		"proc/meminfo":      {Data: []byte(meminfo)},
		"proc/pressure/cpu": {Data: []byte("some avg10=x\n")},
	}
	cases := []struct {
		name    string
		fsys    fstest.MapFS
		check   func(t *testing.T, s Snapshot)
		wantErr bool
	}{
		{name: "psi", fsys: psi, check: func(t *testing.T, s Snapshot) {
			if s.MemoryTotal != 32466216<<10 || s.MemoryAvailable != 19827652<<10 || !s.LoadKnown || s.Load1 != 3.52 {
				t.Errorf("snapshot = %+v", s)
			}
			if s.CPU == nil || s.CPU.Some != 61.5 || s.Memory == nil || s.Memory.Some != 0.1 || s.Memory.Full != 0.05 {
				t.Errorf("pressure = %+v %+v", s.CPU, s.Memory)
			}
		}},
		// Free memory and cache stand in for MemAvailable, the pressure is unknown.
		{name: "old kernel", fsys: old, check: func(t *testing.T, s Snapshot) {
			if s.CPU != nil || s.Memory != nil || s.MemoryAvailable != 300<<10 {
				t.Errorf("without PSI: %+v", s)
			}
		}},
		// A broken pressure file is an error, not a silent zero.
		{name: "broken pressure", fsys: broken, wantErr: true},
		{name: "no meminfo", fsys: fstest.MapFS{}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, err := ReadProc(c.fsys)
			if (err != nil) != c.wantErr {
				t.Fatalf("ReadProc: %v, want error %v", err, c.wantErr)
			}
			if c.check != nil {
				c.check(t, s)
			}
		})
	}
}
