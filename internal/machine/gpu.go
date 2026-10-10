package machine

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
)

// GPU is a graphics processor of the machine.
type GPU struct {
	Vendor        string // nvidia, amd, intel, apple, or the PCI vendor identifier of another
	Name          string // as its driver gives it; its PCI identifiers when none does
	Driver        string // nvidia, nouveau, amdgpu, i915, xe; metal on Apple Silicon; "" when none is bound
	DriverVersion string // "" when unknown
	Memory        uint64 // bytes of its own; with unified memory, the machine's; 0 when unknown
	Unified       bool   // it shares the machine's memory (Apple Silicon)
}

// Disk is the space of the file system that holds a folder.
type Disk struct {
	Path             string
	Total, Available uint64 // bytes; Available is what Djinn may use
}

// vendors names the PCI vendors of the GPUs Djinn knows.
var vendors = map[string]string{"10de": "nvidia", "1002": "amd", "8086": "intel"}

// ReadGPUs reads the GPUs of a Linux machine from its /sys, fsys being the root of the file system: one per DRM card
// on the PCI bus, with its driver, the driver's version, and the memory of its own when the driver gives it (amdgpu).
// nvidiaSMI, called only when a card is NVIDIA's or when /sys shows none (WSL), runs nvidia-smi; what it gives
// replaces the NVIDIA cards of /sys. A /sys that cannot be read gives no GPU.
func ReadGPUs(fsys fs.FS, nvidiaSMI func() ([]byte, error)) []GPU {
	var gpus []GPU
	nvidia := false
	seen := map[string]bool{}
	entries, _ := fs.ReadDir(fsys, "sys/class/drm")
	for _, e := range entries {
		// card0 is a card; card0-DP-1 is one of its connectors, renderD128 another view of it.
		n, ok := strings.CutPrefix(e.Name(), "card")
		if !ok || n == "" || strings.Trim(n, "0123456789") != "" {
			continue
		}
		dev := "sys/class/drm/" + e.Name() + "/device/"
		uevent, err := fs.ReadFile(fsys, dev+"uevent")
		if err != nil {
			continue
		}
		kv := parseUevent(uevent)
		vendor, _, ok := strings.Cut(strings.ToLower(kv["PCI_ID"]), ":")
		slot := kv["PCI_SLOT_NAME"]
		if !ok || (slot != "" && seen[slot]) {
			continue
		}
		seen[slot] = true
		g := GPU{
			Vendor: cmp.Or(vendors[vendor], vendor),
			Name:   cmp.Or(readLine(fsys, dev+"product_name"), strings.ToLower(kv["PCI_ID"])),
			Driver: kv["DRIVER"],
		}
		if g.Driver != "" {
			g.DriverVersion = readLine(fsys, "sys/module/"+g.Driver+"/version")
		}
		if v, err := strconv.ParseUint(readLine(fsys, dev+"mem_info_vram_total"), 10, 64); err == nil {
			g.Memory = v
		}
		nvidia = nvidia || g.Vendor == "nvidia"
		gpus = append(gpus, g)
	}
	if (nvidia || len(gpus) == 0) && nvidiaSMI != nil {
		if out, err := nvidiaSMI(); err == nil {
			if smi, err := parseNvidiaSMI(out); err == nil && len(smi) > 0 {
				others := gpus[:0:0]
				for _, g := range gpus {
					if g.Vendor != "nvidia" {
						others = append(others, g)
					}
				}
				gpus = append(smi, others...)
			}
		}
	}
	return gpus
}

// NvidiaSMIArgs are the arguments ReadGPUs expects nvidia-smi to run with.
var NvidiaSMIArgs = []string{"--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits"}

// parseNvidiaSMI reads the output of nvidia-smi run with NvidiaSMIArgs: a line per GPU, its name, its memory in MiB
// ("[N/A]" when unknown), and the driver's version.
func parseNvidiaSMI(b []byte) ([]GPU, error) {
	var gpus []GPU
	for line := range strings.Lines(string(b)) {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 3 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("nvidia-smi: %q", line)
		}
		g := GPU{
			Vendor: "nvidia", Driver: "nvidia",
			Name:          strings.TrimSpace(strings.Join(f[:len(f)-2], ",")),
			DriverVersion: strings.TrimSpace(f[len(f)-1]),
		}
		if mib, err := strconv.ParseUint(strings.TrimSpace(f[len(f)-2]), 10, 64); err == nil {
			g.Memory = mib << 20
		}
		gpus = append(gpus, g)
	}
	if len(gpus) == 0 {
		return nil, errors.New("nvidia-smi: no GPU")
	}
	return gpus, nil
}

// AppleGPU is the GPU of an Apple Silicon chip: its name is the chip's (machdep.cpu.brand_string), and it shares the
// machine's memory, through Metal.
func AppleGPU(chip string, memory uint64) GPU {
	return GPU{Vendor: "apple", Name: cmp.Or(chip, "Apple Silicon"), Driver: "metal", Memory: memory, Unified: true}
}

// parseUevent reads the KEY=value lines of a sysfs uevent file.
func parseUevent(b []byte) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// readLine reads a one-line sysfs file, trimmed; "" when it does not exist.
func readLine(fsys fs.FS, name string) string {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Reader reads this machine for a monitor: Read, then the disk of dir at each reading, and the GPUs once. Reading
// the GPUs starts now, in the background: asking a driver may take a moment.
func Reader(dir string) func() (Snapshot, error) {
	gpus := sync.OnceValue(readGPUs)
	go gpus()
	return func() (Snapshot, error) {
		s, err := Read()
		if d, err := ReadDisk(dir); err == nil {
			s.Disk = d
		}
		s.GPUs = gpus()
		return s, err
	}
}
