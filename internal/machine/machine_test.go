package machine

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
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

// TestRoom: the machine holds a measured command when the memory free, less what the gates held take, holds its peak
// and the margin; a command never measured, or a machine whose memory is unknown, always has room.
func TestRoom(t *testing.T) {
	p := DefaultPolicy()
	s := Snapshot{MemoryTotal: 16 * GiB, MemoryAvailable: 4 * GiB}
	for _, tt := range []struct {
		name       string
		s          Snapshot
		peak, held uint64
		want       string
	}{
		{"room", s, 3 * GiB, 0, ""},
		{"no margin left", s, 36 * GiB / 10, 0, "e2e peaks at 3.6 GiB, 4.0 GiB free"},
		{"held", s, 2 * GiB, 2 * GiB, "e2e peaks at 2.0 GiB, 4.0 GiB free, 2.0 GiB of it for the commands holding a gate"},
		{"held beyond the free", s, 300 << 20, 6 * GiB, "e2e peaks at 300 MiB, 4.0 GiB free, 4.0 GiB of it for the commands holding a gate"},
		{"never measured", s, 0, 6 * GiB, ""},
		{"memory unknown", Snapshot{}, 3 * GiB, 0, ""},
	} {
		if got := p.Room(tt.s, "e2e", tt.peak, tt.held); got != tt.want {
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

// TestReadThisMachine reads the real machine: whatever it is, it has cores, and memory and a disk where Djinn reads
// them; and a verdict on a local model, with its reason. Its GPUs depend on the machine: they are logged.
func TestReadThisMachine(t *testing.T) {
	dir := t.TempDir()
	s := NewMonitor(DefaultPolicy(), Reader(dir)).Snapshot()
	if s.Cores < 1 {
		t.Errorf("no cores: %+v", s)
	}
	if s.OS == "linux" || s.OS == "darwin" || s.OS == "windows" {
		if s.MemoryTotal == 0 {
			t.Errorf("no memory: %+v", s)
		}
		if s.Disk == nil || s.Disk.Path != dir || s.Disk.Total == 0 || s.Disk.Available > s.Disk.Total {
			t.Errorf("disk: %+v", s.Disk)
		}
	}
	if _, err := ReadDisk(filepath.Join(dir, "missing")); err == nil {
		t.Error("read the disk of a missing folder")
	}
	ok, why := DefaultPolicy().LocalModel(s)
	if why == "" {
		t.Error("a verdict without a reason")
	}
	t.Logf("GPUs %+v; a local model: %v, %s", s.GPUs, ok, why)
}

// sysfs is a simulated /sys of a desktop: an NVIDIA card on its driver, with a connector, and an Intel integrated GPU.
func sysfs() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/drm/card0/device/uevent": {Data: []byte("DRIVER=nvidia\nPCI_CLASS=30000\nPCI_ID=10DE:2206\n" +
			"PCI_SUBSYS_ID=3842:3897\nPCI_SLOT_NAME=0000:01:00.0\n")},
		"sys/class/drm/card0-DP-1/status":        {Data: []byte("connected\n")},
		"sys/class/drm/card0-DP-1/device/uevent": {Data: []byte("DEVTYPE=drm_minor\n")},
		"sys/class/drm/card1/device/uevent":      {Data: []byte("DRIVER=i915\nPCI_ID=8086:4680\nPCI_SLOT_NAME=0000:00:02.0\n")},
		"sys/class/drm/renderD128/device/uevent": {Data: []byte("DRIVER=i915\nPCI_ID=8086:4680\n")},
		"sys/module/nvidia/version":              {Data: []byte("550.54.14\n")},
		"sys/class/drm/version":                  {Data: []byte("drm 1.1.0 20060810\n")},
	}
}

const smi = "NVIDIA GeForce RTX 3080, 10240, 550.54.14\n"

// TestReadGPUs reads the GPUs from a simulated /sys and nvidia-smi output: nvidia-smi names NVIDIA's cards and gives
// their memory, and runs only when a card is NVIDIA's or /sys shows none.
func TestReadGPUs(t *testing.T) {
	calls := 0
	run := func(out string, err error) func() ([]byte, error) {
		return func() ([]byte, error) { calls++; return []byte(out), err }
	}
	gpus := ReadGPUs(sysfs(), run(smi, nil))
	want := []GPU{
		{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 3080", Driver: "nvidia", DriverVersion: "550.54.14", Memory: 10 * GiB},
		{Vendor: "intel", Name: "8086:4680", Driver: "i915"},
	}
	if !slices.Equal(gpus, want) || calls != 1 {
		t.Errorf("with nvidia-smi: %+v (%d calls)", gpus, calls)
	}

	// No nvidia-smi: the card stays, named by its PCI identifiers, its driver's version from the module, its memory
	// unknown.
	gpus = ReadGPUs(sysfs(), run("", errors.New("not found")))
	if len(gpus) != 2 || gpus[0] != (GPU{Vendor: "nvidia", Name: "10de:2206", Driver: "nvidia", DriverVersion: "550.54.14"}) {
		t.Errorf("without nvidia-smi: %+v", gpus)
	}

	// An AMD card gives its memory; no NVIDIA card, no nvidia-smi.
	calls = 0
	amd := fstest.MapFS{
		"sys/class/drm/card0/device/uevent":              {Data: []byte("DRIVER=amdgpu\nPCI_ID=1002:73BF\nPCI_SLOT_NAME=0000:03:00.0\n")},
		"sys/class/drm/card0/device/mem_info_vram_total": {Data: []byte("17163091968\n")},
		"sys/class/drm/card0/device/product_name":        {Data: []byte("\n")},
		"sys/class/drm/card1/device/uevent":              {Data: []byte("DRIVER=amdgpu\nPCI_ID=1002:164E\nPCI_SLOT_NAME=0000:0e:00.0\n")},
		"sys/class/drm/card1/device/mem_info_vram_total": {Data: []byte("536870912\n")},
	}
	gpus = ReadGPUs(amd, run(smi, nil))
	if len(gpus) != 2 || gpus[0] != (GPU{Vendor: "amd", Name: "1002:73bf", Driver: "amdgpu", Memory: 17163091968}) ||
		gpus[1].Memory != 512<<20 || calls != 0 {
		t.Errorf("amd: %+v (%d calls)", gpus, calls)
	}

	// WSL has no DRM card: nvidia-smi alone finds the GPU. A machine with neither has none.
	if gpus := ReadGPUs(fstest.MapFS{}, run(smi, nil)); len(gpus) != 1 || gpus[0].Memory != 10*GiB {
		t.Errorf("wsl: %+v", gpus)
	}
	if gpus := ReadGPUs(fstest.MapFS{}, run("", errors.New("not found"))); gpus != nil {
		t.Errorf("no GPU: %+v", gpus)
	}
	// Two GPUs, one with a comma in its name and its memory unknown; a broken output is ignored.
	gpus = ReadGPUs(fstest.MapFS{}, run("Tesla T4, 15360, 535.1\nGRID, vGPU, [N/A], 535.1\n", nil))
	if len(gpus) != 2 || gpus[1].Name != "GRID, vGPU" || gpus[1].Memory != 0 {
		t.Errorf("two GPUs: %+v", gpus)
	}
	if gpus := ReadGPUs(fstest.MapFS{}, run("NVIDIA-SMI has failed\n", nil)); gpus != nil {
		t.Errorf("broken nvidia-smi: %+v", gpus)
	}
}

func TestLocalModel(t *testing.T) {
	p := DefaultPolicy()
	rtx := GPU{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 3080", Driver: "nvidia", DriverVersion: "550.54.14", Memory: 10 * GiB}
	intel := GPU{Vendor: "intel", Name: "8086:4680", Driver: "i915"}
	roomy := &Disk{Path: "/data", Total: 500 * GiB, Available: 100 * GiB}
	for _, tt := range []struct {
		name string
		s    Snapshot
		want bool
		why  string
	}{
		{"nvidia", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{intel, rtx}, Disk: roomy}, true,
			"on the NVIDIA GeForce RTX 3080, with 10.0 GiB of its own memory, driver nvidia 550.54.14"},
		{"full disk", Snapshot{MemoryTotal: 64 * GiB, GPUs: []GPU{rtx}, Disk: &Disk{Path: "/data", Available: 3 * GiB}}, false,
			"3.0 GiB free on the disk of /data, 10.0 GiB at least"},
		{"nouveau", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "nvidia", Name: "10de:2206", Driver: "nouveau"}}}, false,
			"the 10de:2206 has no driver a model runs on (nouveau), and 8.0 GiB of memory (16.0 GiB at least"},
		{"no smi", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "nvidia", Name: "10de:2206", Driver: "nvidia"}}}, false,
			"the memory of the 10de:2206 is unknown"},
		{"small amd", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "amd", Name: "1002:164e", Driver: "amdgpu", Memory: GiB / 2}}}, false,
			"has 512 MiB of its own memory, 6.0 GiB at least"},
		{"big amd", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{{Vendor: "amd", Name: "1002:73bf", Driver: "amdgpu", Memory: 16 * GiB}}}, true,
			"driver amdgpu"},
		{"apple", Snapshot{MemoryTotal: 32 * GiB, GPUs: []GPU{AppleGPU("Apple M2 Pro", 32*GiB)}}, true,
			"on the Apple M2 Pro, through metal, with 32.0 GiB of unified memory"},
		{"small apple", Snapshot{MemoryTotal: 8 * GiB, GPUs: []GPU{AppleGPU("Apple M1", 8*GiB)}}, false,
			"the Apple M1 has 8.0 GiB of unified memory, 16.0 GiB at least"},
		{"cpu", Snapshot{MemoryTotal: 32 * GiB, GPUs: []GPU{intel}, Disk: roomy}, true,
			"on the CPU, slowly, with 32.0 GiB of memory: the intel GPU 8086:4680 is not counted"},
		{"little", Snapshot{MemoryTotal: 8 * GiB}, false, "no GPU found, and 8.0 GiB of memory"},
		{"unknown", Snapshot{}, false, "no GPU found, and the memory is unknown"},
	} {
		ok, why := p.LocalModel(tt.s)
		if ok != tt.want || !strings.Contains(why, tt.why) {
			t.Errorf("%s: %v (%s), want %v (%s)", tt.name, ok, why, tt.want, tt.why)
		}
	}
}

// TestTypical: the typical peak of a worker is the median of the latest measured, WorkerPeak while none is.
func TestTypical(t *testing.T) {
	p := DefaultPolicy()
	p.WorkerPeaks = 3
	for _, tt := range []struct {
		name  string
		peaks []uint64
		want  uint64
		n     int
	}{
		{"none measured", nil, GiB, 0},
		{"odd", []uint64{3 * GiB, GiB, 2 * GiB}, 2 * GiB, 3},
		{"even", []uint64{GiB, 3 * GiB}, 2 * GiB, 2},
		{"the latest only", []uint64{GiB, GiB, 2 * GiB, 9 * GiB, 9 * GiB}, GiB, 3},
		{"zero is not measured", []uint64{0, 2 * GiB, 0}, 2 * GiB, 1},
	} {
		if got, n := p.Typical(tt.peaks); got != tt.want || n != tt.n {
			t.Errorf("%s: %d over %d, want %d over %d", tt.name, got, n, tt.want, tt.n)
		}
	}
}

// TestWorkerRoom: another worker starts only when the memory available, less what the running ones may still take,
// holds its typical peak and the margin, and the reason names those numbers.
func TestWorkerRoom(t *testing.T) {
	p := DefaultPolicy()
	for _, tt := range []struct {
		name                  string
		available, held, peak uint64
		measured              int
		want                  string
	}{
		{"room", 4 * GiB, GiB, 2 * GiB, 3, ""},
		{"tight", 2 * GiB, 0, 2 * GiB, 3, "a claude worker peaks at 2.0 GiB (the median of the last 3 measured), 2.0 GiB free, 512 MiB kept"},
		{"held", 3 * GiB, 2 * GiB, GiB, 0,
			"a claude worker peaks at 1.0 GiB (none measured yet), 3.0 GiB free, 2.0 GiB of it for the workers running, 512 MiB kept"},
		{"one measured", GiB, 4 * GiB, GiB, 1,
			"a claude worker peaks at 1.0 GiB (the one measured), 1.0 GiB free, 1.0 GiB of it for the workers running, 512 MiB kept"},
		{"memory unknown", 0, GiB, GiB, 0, ""},
	} {
		if got := p.WorkerRoom(0, tt.available, tt.held, 0, "claude", tt.peak, tt.measured); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNotchPolicy(t *testing.T) {
	for _, tt := range []struct {
		notch      djinnv1.LoadNotch
		name       string
		workers    int
		share      float64
		gateMargin uint64
		cpu        float64
		mem        float64
		load       float64
		free       float64
	}{
		{djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, "minimal", 1, 0.20, 1024 * MiB, 25, 5, 1.0, 0.20},
		{djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, "light", 3, 0.40, 768 * MiB, 40, 8, 1.5, 0.15},
		{djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, "medium", 16, 0.70, 512 * MiB, 50, 10, 2.0, 0.10},
		{djinnv1.LoadNotch_LOAD_NOTCH_HIGH, "high", 24, 0.85, 384 * MiB, 70, 15, 3.0, 0.05},
		{djinnv1.LoadNotch_LOAD_NOTCH_MAX, "max", 32, 1.00, 256 * MiB, 90, 25, 5.0, 0.02},
	} {
		p := NotchPolicy(tt.notch)
		if p.Notch != tt.notch {
			t.Errorf("%s notch: got %v, want %v", tt.name, p.Notch, tt.notch)
		}
		if p.NotchName() != tt.name {
			t.Errorf("%s name: got %s, want %s", tt.name, p.NotchName(), tt.name)
		}
		if p.MaxWorkers != tt.workers {
			t.Errorf("%s MaxWorkers: got %d, want %d", tt.name, p.MaxWorkers, tt.workers)
		}
		if p.MemoryShare != tt.share {
			t.Errorf("%s MemoryShare: got %f, want %f", tt.name, p.MemoryShare, tt.share)
		}
		if p.CommandMargin != tt.gateMargin {
			t.Errorf("%s CommandMargin: got %d, want %d", tt.name, p.CommandMargin, tt.gateMargin)
		}
		if p.CPUPressure != tt.cpu {
			t.Errorf("%s CPUPressure: got %f, want %f", tt.name, p.CPUPressure, tt.cpu)
		}
		if p.MemoryPressure != tt.mem {
			t.Errorf("%s MemoryPressure: got %f, want %f", tt.name, p.MemoryPressure, tt.mem)
		}
		if p.LoadPerCore != tt.load {
			t.Errorf("%s LoadPerCore: got %f, want %f", tt.name, p.LoadPerCore, tt.load)
		}
		if p.MemoryFree != tt.free {
			t.Errorf("%s MemoryFree: got %f, want %f", tt.name, p.MemoryFree, tt.free)
		}
	}
	// DefaultPolicy matches medium.
	def := DefaultPolicy()
	med := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	if def.Notch != med.Notch || def.MaxWorkers != med.MaxWorkers || def.MemoryShare != med.MemoryShare ||
		def.CommandMargin != med.CommandMargin {
		t.Errorf("DefaultPolicy does not match medium: %+v vs %+v", def, med)
	}
	// Unspecified falls back to medium.
	unspec := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED)
	if unspec.Notch != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("unspecified notch did not fall back to medium: %v", unspec.Notch)
	}
}

func TestParseNotch(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want djinnv1.LoadNotch
	}{
		{"minimal", djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL},
		{"light", djinnv1.LoadNotch_LOAD_NOTCH_LIGHT},
		{"medium", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM},
		{"high", djinnv1.LoadNotch_LOAD_NOTCH_HIGH},
		{"max", djinnv1.LoadNotch_LOAD_NOTCH_MAX},
	} {
		got, err := ParseNotch(tt.s)
		if err != nil || got != tt.want {
			t.Errorf("ParseNotch(%q) = %v, %v; want %v, nil", tt.s, got, err, tt.want)
		}
		if name := NotchName(tt.want); name != tt.s {
			t.Errorf("NotchName(%v) = %q, want %q", tt.want, name, tt.s)
		}
	}
	if _, err := ParseNotch("unknown"); err == nil {
		t.Error("ParseNotch(unknown) should fail")
	}
}

func TestCommittableLimit(t *testing.T) {
	total16 := uint64(16 * GiB)
	for _, tt := range []struct {
		name  string
		notch djinnv1.LoadNotch
		total uint64
		want  uint64
	}{
		{"minimal 16G", djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL, total16, uint64(float64(total16) * 0.20)},
		{"light 16G", djinnv1.LoadNotch_LOAD_NOTCH_LIGHT, total16, uint64(float64(total16) * 0.40)},
		{"medium 16G", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, total16, uint64(float64(total16) * 0.70)},
		{"high 16G", djinnv1.LoadNotch_LOAD_NOTCH_HIGH, total16, uint64(float64(total16) * 0.85)},
		{"max 16G", djinnv1.LoadNotch_LOAD_NOTCH_MAX, total16, total16 - 512*MiB},
		{"zero total", djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM, 0, 0},
		{"max small total", djinnv1.LoadNotch_LOAD_NOTCH_MAX, 256 * MiB, 0},
	} {
		p := NotchPolicy(tt.notch)
		if got := p.CommittableLimit(tt.total); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestWorkerRoomNotches(t *testing.T) {
	total := uint64(16 * GiB)
	available := uint64(12 * GiB)
	for _, tt := range []struct {
		notch   djinnv1.LoadNotch
		engaged uint64
		peak    uint64
		wantWhy string
	}{
		{
			djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL,
			2 * GiB,
			GiB, // forecast = 1.0 GiB + 512 MiB = 1.5 GiB; 2.0 GiB + 1.5 GiB = 3.5 GiB > 3.2 GiB (20%)
			"load minimal commits up to 3.2 GiB of memory, 2.0 GiB already engaged, needs 1.5 GiB",
		},
		{
			djinnv1.LoadNotch_LOAD_NOTCH_LIGHT,
			5 * GiB,
			GiB, // forecast = 1.5 GiB; 5.0 GiB + 1.5 GiB = 6.5 GiB > 6.4 GiB (40%)
			"load light commits up to 6.4 GiB of memory, 5.0 GiB already engaged, needs 1.5 GiB",
		},
		{
			djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM,
			10 * GiB,
			GiB, // forecast = 1.5 GiB; 10.0 GiB + 1.5 GiB = 11.5 GiB > 11.2 GiB (70%)
			"load medium commits up to 11.2 GiB of memory, 10.0 GiB already engaged, needs 1.5 GiB",
		},
		{
			djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
			13 * GiB,
			GiB, // forecast = 1.5 GiB; 13.0 GiB + 1.5 GiB = 14.5 GiB > 13.6 GiB (85%)
			"load high commits up to 13.6 GiB of memory, 13.0 GiB already engaged, needs 1.5 GiB",
		},
		{
			djinnv1.LoadNotch_LOAD_NOTCH_MAX,
			15 * GiB,
			GiB, // forecast = 1.5 GiB; 15.0 GiB + 1.5 GiB = 16.5 GiB > 15.5 GiB (16G - 512M)
			"load max commits up to 15.5 GiB of memory, 15.0 GiB already engaged, needs 1.5 GiB",
		},
	} {
		p := NotchPolicy(tt.notch)
		why := p.WorkerRoom(total, available, 0, tt.engaged, "claude", tt.peak, 0)
		if why != tt.wantWhy {
			t.Errorf("%v WorkerRoom exceeded: got %q, want %q", tt.notch, why, tt.wantWhy)
		}

		// When within limit and memory is available, WorkerRoom allows it.
		fits := p.WorkerRoom(total, available, 0, 0, "claude", 500*MiB, 0)
		if fits != "" {
			t.Errorf("%v WorkerRoom fits: got %q, want empty", tt.notch, fits)
		}
	}

	// Verify message when engaged is 0.
	pMin := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL)
	whyZeroEngaged := pMin.WorkerRoom(total, available, 0, 0, "claude", 3*GiB, 0)
	wantZeroEngaged := "load minimal commits up to 3.2 GiB of memory, needs 3.5 GiB"
	if whyZeroEngaged != wantZeroEngaged {
		t.Errorf("zero engaged WorkerRoom: got %q, want %q", whyZeroEngaged, wantZeroEngaged)
	}
}
