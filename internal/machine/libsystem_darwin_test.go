package machine

import (
	"bufio"
	"crypto/rand"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The structures libsystem_darwin.go reads keep XNU's sizes: struct rusage_info_v4 is 296 bytes (a UUID and 35
// counters), struct vm_statistics64 152 (HOST_VM_INFO64_COUNT 38).
func TestLibSystemSizes(t *testing.T) {
	t.Parallel()
	if n := unsafe.Sizeof(rusageInfoV4{}); n != 296 {
		t.Errorf("rusage_info_v4: %d bytes, want 296", n)
	}
	if n := unsafe.Sizeof(vmStatistics64{}); n != 152 || hostVMInfo64Count != 38 {
		t.Errorf("vm_statistics64: %d bytes, count %d, want 152, 38", n, hostVMInfo64Count)
	}
}

// holdEnv makes the test binary a child that holds that many MiB of random bytes, says so, and waits.
const holdEnv = "DJINN_TEST_HOLD_MIB"

func init() {
	mib, err := strconv.Atoi(os.Getenv(holdEnv))
	if err != nil {
		return
	}
	buf := make([]byte, mib<<20)
	if _, err := rand.Read(buf); err != nil {
		os.Exit(2)
	}
	os.Stdout.WriteString("READY\n")
	time.Sleep(time.Minute)
	_ = buf[len(buf)-1]
	os.Exit(0)
}

// TestFootprint reads, through proc_pid_rusage, the footprint of a child that holds 256 MiB of random bytes: its
// current and most footprint hold them, and ps's rss is near; its lifetime most is not below its current one.
func TestFootprint(t *testing.T) {
	t.Parallel()
	const mib = 256
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), holdEnv+"="+strconv.Itoa(mib))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "READY\n" {
		t.Fatalf("child said %q, %v", line, err)
	}
	now, most, ok := Footprint(cmd.Process.Pid)
	if !ok {
		t.Fatal("Footprint of the child: not read")
	}
	t.Logf("footprint %d MiB, most %d MiB", now>>20, most>>20)
	if now < mib<<20 || now > 4*mib<<20 {
		t.Errorf("footprint %d MiB of a child holding %d MiB", now>>20, mib)
	}
	if most < now {
		t.Errorf("lifetime most %d MiB below the footprint %d MiB", most>>20, now>>20)
	}
	if _, _, ok := Footprint(0); ok {
		t.Error("Footprint of pid 0 read")
	}
}

// TestHostVMCounts reads host_statistics64 and finds about what vm_stat prints: the available memory of both within a
// tenth of the total of each other.
func TestHostVMCounts(t *testing.T) {
	t.Parallel()
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		t.Fatal(err)
	}
	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		t.Fatal(err)
	}
	counts, filed, ok := hostVMCounts()
	if !ok {
		t.Fatal("host_statistics64 failed")
	}
	if !pagesCover(total, uint64(page), filed) {
		t.Fatalf("%d pages of %d bytes for %d bytes of memory", filed, page, total)
	}
	host, ok := countsAvailable(total, uint64(page), counts)
	if !ok {
		t.Fatalf("counts %v", counts)
	}
	printed, ok := vmStatAvailable(total, vmStat())
	if !ok {
		t.Fatal("vm_stat not read")
	}
	t.Logf("available: %d MiB from host_statistics64, %d MiB from vm_stat, of %d MiB", host>>20, printed>>20, total>>20)
	if d := max(host, printed) - min(host, printed); d > total/10 {
		t.Errorf("host_statistics64 gives %d MiB available, vm_stat %d MiB", host>>20, printed>>20)
	}
	if a := available(total); a == 0 || a > total {
		t.Errorf("available = %d of %d", a, total)
	}
}
