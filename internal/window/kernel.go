package window

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// MaxHangs is how many hangs a Watch keeps, the latest: a driver that hangs in a loop fills the kernel's log.
const MaxHangs = 12

// Hang is a line of the kernel's log that tells a GPU hang or reset.
type Hang struct {
	// Time is when the kernel logged it; zero when the line names none.
	Time time.Time
	// Line is the line, without its time and host.
	Line string
}

// hangLine matches what the kernel logs at a GPU hang or reset: i915's "GPU HANG: ecode 12:1:84dffffb, in …",
// "Engine reset failed on 0:0 (rcs0)", "Resetting chip for …" and "Xorg[8937] context reset due to GPU hang" (W353),
// amdgpu's "GPU reset begin!" and "ring gfx timeout".
var hangLine = regexp.MustCompile(`(?i)gpu hang|engine reset failed|resetting chip|context reset|gpu reset|ring \S+ timeout`)

// Journal is the kernel's log of this boot since a time, as journalctl -o short-iso prints it; nothing outside
// Linux. Tests replace it.
var Journal = func(ctx context.Context, since time.Time) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "journalctl", "-k", "-q", "--no-pager", "-o", "short-iso",
		"--since", "@"+strconv.FormatInt(since.Unix(), 10)).Output()
}

// KernelHangs is the GPU hangs and resets of the kernel's log since a time, the last MaxHangs.
func KernelHangs(ctx context.Context, since time.Time) ([]Hang, error) {
	log, err := Journal(ctx, since)
	return Hangs(log), err
}

// Hangs is the GPU hangs and resets of a kernel log, the last MaxHangs.
func Hangs(log []byte) []Hang {
	var hangs []Hang
	lines := bufio.NewScanner(bytes.NewReader(log))
	lines.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for lines.Scan() {
		line := lines.Text()
		if !hangLine.MatchString(line) {
			continue
		}
		hangs = append(hangs, parseHang(line))
	}
	if len(hangs) > MaxHangs {
		hangs = hangs[len(hangs)-MaxHangs:]
	}
	return hangs
}

// parseHang reads a line of journalctl -o short-iso: "2026-10-17T23:26:55+0200 host kernel: i915 …".
func parseHang(line string) Hang {
	stamp, rest, ok := strings.Cut(line, " ")
	t, err := time.Parse("2006-01-02T15:04:05-0700", stamp)
	if !ok || err != nil {
		return Hang{Line: strings.TrimSpace(line)}
	}
	if _, text, ok := strings.Cut(rest, " kernel: "); ok {
		rest = text
	}
	return Hang{Time: t, Line: strings.TrimSpace(rest)}
}
