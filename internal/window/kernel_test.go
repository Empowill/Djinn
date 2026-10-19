package window

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// journal17 is the kernel's log around the hang of 17/10 (W353), with lines that tell none.
const journal17 = `2026-10-17T23:20:01+0200 host kernel: usb 3-2: new high-speed USB device number 9
2026-10-17T23:26:55+0200 host kernel: i915 0000:00:02.0: [drm] *ERROR* GT0: GUC: Engine reset failed on 0:0 (rcs0) because 0x00000000
2026-10-17T23:26:55+0200 host kernel: i915 0000:00:02.0: [drm] GT0: Resetting chip for GuC failed to reset engine mask=0x1
2026-10-17T23:26:55+0200 host kernel: i915 0000:00:02.0: [drm] Xorg[8937] context reset due to GPU hang
2026-10-17T23:26:55+0200 host kernel: i915 0000:00:02.0: [drm] eadedCompositor[1781401 context reset due to GPU hang
2026-10-17T23:26:55+0200 host kernel: i915 0000:00:02.0: [drm] GT0: GuC firmware i915/adlp_guc_70.bin version 70.20.0
2026-10-17T23:27:10+0200 host kernel: i915 0000:00:02.0: [drm] *ERROR* Atomic update failure on pipe A
`

// TestHangs covers Hangs: it keeps the lines that tell a GPU hang or reset, with their time, and the last MaxHangs.
func TestHangs(t *testing.T) {
	t.Run("the lines of a hang, with their time", func(t *testing.T) {
		hangs := Hangs([]byte(journal17))
		if len(hangs) != 4 {
			t.Fatalf("hangs = %+v; want the 4 lines of the reset", hangs)
		}
		want := time.Date(2026, 10, 17, 21, 26, 55, 0, time.UTC)
		if !hangs[0].Time.Equal(want) || !strings.HasPrefix(hangs[0].Line, "i915 0000:00:02.0: [drm] *ERROR* GT0: GUC: Engine reset failed") {
			t.Errorf("first = %+v", hangs[0])
		}
		if !strings.Contains(hangs[3].Line, "eadedCompositor[1781401 context reset due to GPU hang") {
			t.Errorf("last = %+v", hangs[3])
		}
	})

	t.Run("amdgpu's resets too", func(t *testing.T) {
		log := "2026-10-17T10:00:00+0000 h kernel: [drm:amdgpu_job_timedout] *ERROR* ring gfx timeout, signaled seq=1\n" +
			"2026-10-17T10:00:01+0000 h kernel: amdgpu 0000:03:00.0: amdgpu: GPU reset begin!\n"
		if hangs := Hangs([]byte(log)); len(hangs) != 2 {
			t.Errorf("hangs = %+v", hangs)
		}
	})

	t.Run("the last MaxHangs of a driver that hangs in a loop", func(t *testing.T) {
		var log strings.Builder
		for i := range MaxHangs + 5 {
			fmt.Fprintf(&log, "2026-10-17T10:00:%02d+0000 h kernel: i915: [drm] GPU HANG: ecode 12:1:84dffffb, n%d\n", i, i)
		}
		hangs := Hangs([]byte(log.String()))
		if len(hangs) != MaxHangs || !strings.HasSuffix(hangs[MaxHangs-1].Line, fmt.Sprintf("n%d", MaxHangs+4)) {
			t.Errorf("kept %d, last %+v", len(hangs), hangs[len(hangs)-1])
		}
	})

	t.Run("a line without a time keeps its text", func(t *testing.T) {
		hangs := Hangs([]byte("GPU HANG somewhere\n"))
		if len(hangs) != 1 || !hangs[0].Time.IsZero() || hangs[0].Line != "GPU HANG somewhere" {
			t.Errorf("hangs = %+v", hangs)
		}
	})
}

// TestKernelHangs covers KernelHangs: it reads the journal since the time given, and returns its error with what it
// read.
func TestKernelHangs(t *testing.T) {
	journal := Journal
	t.Cleanup(func() { Journal = journal })
	var asked time.Time
	Journal = func(_ context.Context, since time.Time) ([]byte, error) {
		asked = since
		return []byte(journal17), fmt.Errorf("journalctl: exit status 1")
	}
	since := time.Date(2026, 10, 17, 21, 17, 0, 0, time.UTC)
	hangs, err := KernelHangs(t.Context(), since)
	if !asked.Equal(since) || len(hangs) != 4 || err == nil {
		t.Errorf("asked since %v; got %d hangs, %v", asked, len(hangs), err)
	}
}
