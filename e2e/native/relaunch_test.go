package native

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// watched starts binary for a test of the window's watch, on the GPU whatever started the tests: a Djinn started by
// hand in software rendering passes it to its workers.
func watched(t *testing.T, env ...string) (*djinn, func() string) {
	t.Helper()
	binary := os.Getenv("DJINN_E2E_NATIVE")
	if binary == "" {
		t.Skip("opens a native window: run it with go tool task e2e-native")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the window is watched on Linux only")
	}
	d := start(t, binary, append(env, "WEBKIT_DISABLE_DMABUF_RENDERER", "WEBKIT_DISABLE_COMPOSITING_MODE")...)
	return d, func() string {
		b, _ := os.ReadFile(filepath.Join(d.home, "logs", "djinn.log"))
		return string(b)
	}
}

// logs waits until the log holds each of want.
func logs(t *testing.T, log func() string, want ...string) {
	t.Helper()
	eventually(t, 30*time.Second, func() error {
		for _, w := range want {
			if !strings.Contains(log(), w) {
				return fmt.Errorf("the log lacks %q:\n%s", w, log())
			}
		}
		return nil
	})
}

// shows waits until the window shows the interface.
func shows(t *testing.T, d *djinn) {
	t.Helper()
	eventually(t, 30*time.Second, func() error {
		var page struct {
			Bridge bool `json:"bridge"`
		}
		if err := d.eval(`return { bridge: !!document.querySelector(".wish-app") };`, &page); err != nil {
			return err
		}
		if !page.Bridge {
			return fmt.Errorf("the interface does not show")
		}
		return nil
	})
}

// TestNativeRelaunch: a page silent past the watch's threshold, here 1 ms (DJINN_WINDOW_SILENCE, test builds only),
// loads again, then the window opens again with a new view, in the same process; on the GPU, or in software rendering
// after a GPU hang the kernel logged (DJINN_WINDOW_KERNEL_LOG, test builds only). Each start logs the page's load
// events and, at each rescue, the web processes; the stop names its source (decision Q84).
func TestNativeRelaunch(t *testing.T) {
	t.Run("without a GPU hang, a reload then a new view on the GPU", func(t *testing.T) {
		d, log := watched(t, "DJINN_WINDOW_SILENCE=1ms")
		logs(t, log, `msg="window: load" event=started`, `msg="window: load" event=committed`,
			"web_processes=", "WebKitWebProces", `msg="window: the page loads again" page=2`,
			`msg="window: opened again with a new view" page=3 software_rendering=false`,
			`msg="window: the page calls the server" page=3`)
		if !strings.Contains(d.out.String(), "djinn: Djinn's window stayed black.") {
			t.Errorf("not said on the output:\n%s", d.out.String())
		}
		shows(t, d)
		if web := webProcesses(t, d.cmd.Process.Pid); len(web) == 0 || len(gpuThreads(web[len(web)-1])) == 0 {
			t.Errorf("web processes %v: the new view does not draw on the GPU", web)
		}
		d.stop(t)
		if !strings.Contains(log(), `msg="shutdown begins" why="SIGINT (Ctrl+C in djinn up's terminal, or kill -INT)"`) {
			t.Errorf("the stop does not name its source:\n%s", log())
		}
	})

	t.Run("after a GPU hang, the new view in software rendering", func(t *testing.T) {
		kernel := filepath.Join(t.TempDir(), "kernel.log")
		line := time.Now().Format("2006-01-02T15:04:05-0700") + " host kernel: i915 0000:00:02.0: [drm] Xorg[8937] context reset due to GPU hang\n"
		if err := os.WriteFile(kernel, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		d, log := watched(t, "DJINN_WINDOW_SILENCE=1ms", "DJINN_WINDOW_KERNEL_LOG="+kernel)
		logs(t, log, "gpu_hangs=1", `msg="window: opened again with a new view" page=3 software_rendering=true`,
			`msg="window: the page calls the server" page=3`)
		if !strings.Contains(d.out.String(), "in software rendering, slower, until Djinn restarts") {
			t.Errorf("not said on the output:\n%s", d.out.String())
		}
		shows(t, d)
		web := webProcesses(t, d.cmd.Process.Pid)
		if len(web) == 0 {
			t.Fatal("no web process")
		}
		if gpu := gpuThreads(web[len(web)-1]); len(gpu) > 0 {
			t.Errorf("web process %d has GPU threads %v (all: %v)", web[len(web)-1], gpu, web)
		}
	})
}

// TestNativeWebProcessEnds: the web process of a window that runs dies mid-session, with no GPU hang (W366): its end
// is logged with its reason, the page loads again with a new web process, and the page's console reaches djinn up's
// output.
func TestNativeWebProcessEnds(t *testing.T) {
	d, log := watched(t)
	logs(t, log, `msg="window: the page calls the server" page=1`)
	web := webProcesses(t, d.cmd.Process.Pid)
	if len(web) != 1 {
		t.Fatalf("web processes %v; want one", web)
	}
	// Only the web process of this test's own djinn, by its PID.
	if err := syscall.Kill(web[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	logs(t, log, `msg="window: the web process of the page ended" reason=crashed`,
		`msg="window: the page loads again" page=2`, `msg="window: the page calls the server" page=2`)
	if !strings.Contains(d.out.String(), "djinn: Djinn's window lost its page.") {
		t.Errorf("not said on the output:\n%s", d.out.String())
	}
	shows(t, d)
	var none struct{}
	if err := d.eval(`console.log("w354 console check"); return {};`, &none); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() error {
		if !strings.Contains(d.out.String(), "w354 console check") {
			return fmt.Errorf("the page's console is not on djinn's output:\n%s", d.out.String())
		}
		return nil
	})
	if after := webProcesses(t, d.cmd.Process.Pid); len(after) != 1 || after[0] == web[0] {
		t.Errorf("web processes %v after the reload; want one new", after)
	}
}

// webProcesses is the WebKit web processes started by pid, the oldest first.
func webProcesses(t *testing.T, pid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var found []int
	for _, e := range entries {
		child, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(stat)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) && strings.Contains(s, "(WebKitWeb") {
			found = append(found, child)
		}
	}
	return found // /proc lists by PID: the oldest first, short of a wrap.
}

// gpuThreads is the threads of pid that draw on the GPU: WebKit's compositor, Mesa's, Skia's (W353).
func gpuThreads(pid int) []string {
	tasks, _ := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "task"))
	var gpu []string
	for _, task := range tasks {
		comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "task", task.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if strings.Contains(name, "Compositor") || strings.Contains(name, "gdrv") || strings.Contains(name, "Skia") {
			gpu = append(gpu, name)
		}
	}
	return gpu
}
