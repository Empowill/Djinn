package machine

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
)

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
	t.Parallel()
	rtx := GPU{Vendor: "nvidia", Name: "NVIDIA GeForce RTX 3080", Driver: "nvidia", DriverVersion: "550.54.14", Memory: 10 * GiB}
	intel := GPU{Vendor: "intel", Name: "8086:4680", Driver: "i915"}
	amd := fstest.MapFS{
		"sys/class/drm/card0/device/uevent":              {Data: []byte("DRIVER=amdgpu\nPCI_ID=1002:73BF\nPCI_SLOT_NAME=0000:03:00.0\n")},
		"sys/class/drm/card0/device/mem_info_vram_total": {Data: []byte("17163091968\n")},
		"sys/class/drm/card0/device/product_name":        {Data: []byte("\n")},
		"sys/class/drm/card1/device/uevent":              {Data: []byte("DRIVER=amdgpu\nPCI_ID=1002:164E\nPCI_SLOT_NAME=0000:0e:00.0\n")},
		"sys/class/drm/card1/device/mem_info_vram_total": {Data: []byte("536870912\n")},
	}
	notFound := errors.New("not found")
	cases := []struct {
		name  string
		fsys  fstest.MapFS
		out   string
		err   error
		want  []GPU
		calls int // the runs of nvidia-smi
	}{
		{name: "with nvidia-smi", fsys: sysfs(), out: smi, want: []GPU{rtx, intel}, calls: 1},
		// No nvidia-smi: the card stays, named by its PCI identifiers, its driver's version from the module, its
		// memory unknown.
		{name: "without nvidia-smi", fsys: sysfs(), err: notFound, calls: 1,
			want: []GPU{{Vendor: "nvidia", Name: "10de:2206", Driver: "nvidia", DriverVersion: "550.54.14"}, intel}},
		// An AMD card gives its memory; no NVIDIA card, no nvidia-smi.
		{name: "amd", fsys: amd, out: smi, want: []GPU{
			{Vendor: "amd", Name: "1002:73bf", Driver: "amdgpu", Memory: 17163091968},
			{Vendor: "amd", Name: "1002:164e", Driver: "amdgpu", Memory: 512 << 20},
		}},
		// WSL has no DRM card: nvidia-smi alone finds the GPU.
		{name: "wsl", fsys: fstest.MapFS{}, out: smi, want: []GPU{rtx}, calls: 1},
		{name: "no GPU", fsys: fstest.MapFS{}, err: notFound, calls: 1},
		// Two GPUs, one with a comma in its name and its memory unknown.
		{name: "two GPUs", fsys: fstest.MapFS{}, out: "Tesla T4, 15360, 535.1\nGRID, vGPU, [N/A], 535.1\n", calls: 1,
			want: []GPU{
				{Vendor: "nvidia", Name: "Tesla T4", Driver: "nvidia", DriverVersion: "535.1", Memory: 15 * GiB},
				{Vendor: "nvidia", Name: "GRID, vGPU", Driver: "nvidia", DriverVersion: "535.1"},
			}},
		// A broken output is ignored.
		{name: "broken nvidia-smi", fsys: fstest.MapFS{}, out: "NVIDIA-SMI has failed\n", calls: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			gpus := ReadGPUs(c.fsys, func() ([]byte, error) { calls++; return []byte(c.out), c.err })
			if !slices.Equal(gpus, c.want) || (gpus == nil) != (c.want == nil) || calls != c.calls {
				t.Errorf("ReadGPUs = %+v (%d calls), want %+v (%d calls)", gpus, calls, c.want, c.calls)
			}
		})
	}
}
