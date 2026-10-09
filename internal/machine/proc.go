package machine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// ReadProc reads the memory, the load and the pressure from a Linux /proc, fsys being the root of the file system.
// The pressure files are optional: a kernel without PSI leaves the pressure unknown. The cores are not read here.
func ReadProc(fsys fs.FS) (Snapshot, error) {
	var s Snapshot
	mem, err := fs.ReadFile(fsys, "proc/meminfo")
	if err != nil {
		return s, err
	}
	if s.MemoryTotal, s.MemoryAvailable, err = parseMeminfo(mem); err != nil {
		return s, err
	}
	if load, err := fs.ReadFile(fsys, "proc/loadavg"); err == nil {
		if f := strings.Fields(string(load)); len(f) > 0 {
			if s.Load1, err = strconv.ParseFloat(f[0], 64); err == nil {
				s.LoadKnown = true
			}
		}
	}
	if s.CPU, err = readPressure(fsys, "proc/pressure/cpu"); err != nil {
		return s, err
	}
	if s.Memory, err = readPressure(fsys, "proc/pressure/memory"); err != nil {
		return s, err
	}
	return s, nil
}

// parseMeminfo gives MemTotal and MemAvailable, in bytes. A kernel older than 3.14 has no MemAvailable: free memory
// and the page cache stand in for it.
func parseMeminfo(b []byte) (total, available uint64, err error) {
	fields := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			v <<= 10
		}
		fields[name] = v
	}
	total, ok := fields["MemTotal"]
	if !ok {
		return 0, 0, errors.New("meminfo: no MemTotal")
	}
	available, ok = fields["MemAvailable"]
	if !ok {
		available = fields["MemFree"] + fields["Cached"]
	}
	return total, available, nil
}

// readPressure reads a PSI file; nil when it does not exist, or when the kernel turned PSI off (reading fails with
// "operation not supported").
func readPressure(fsys fs.FS, name string) (*Pressure, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, nil //nolint:nilerr // No pressure to read: see above.
	}
	return parsePressure(b)
}

// parsePressure reads the avg10 of the "some" and "full" lines of a PSI file. The CPU file of older kernels has no
// "full" line.
func parsePressure(b []byte) (*Pressure, error) {
	p := &Pressure{}
	found := false
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, kv := range f[1:] {
			v, ok := strings.CutPrefix(kv, "avg10=")
			if !ok {
				continue
			}
			x, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("pressure %q: %w", line, err)
			}
			switch f[0] {
			case "some":
				p.Some, found = x, true
			case "full":
				p.Full = x
			}
		}
	}
	if !found {
		return nil, errors.New("pressure: no some line")
	}
	return p, nil
}
