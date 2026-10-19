package logx

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestShutdown: a stop held by a stuck step dumps the goroutines past DumpAfter, logs the step, then exits past
// ExitAfter; a stop that ends in time does neither.
func TestShutdown(t *testing.T) {
	t.Run("stuck step", func(t *testing.T) {
		b := capture(t)
		dir := t.TempDir()
		exited := make(chan int, 1)
		var before bool
		s := &Shutdown{Dir: dir, DumpAfter: 20 * time.Millisecond, ExitAfter: 200 * time.Millisecond,
			BeforeExit: func() { before = true }, Exit: func(code int) { exited <- code }}
		stuck := make(chan struct{})
		defer close(stuck)
		go s.Step("workers", func() { <-stuck })
		go s.Step("terminals", func() {})
		select {
		case code := <-exited:
			if code != HungExit {
				t.Errorf("exit %d, want %d", code, HungExit)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the watchdog did not exit")
		}
		if !before {
			t.Error("BeforeExit did not run")
		}
		dumps := Dumps(dir)
		if len(dumps) != 2 {
			t.Fatalf("dumps %v, want one when slow and one at the exit", dumps)
		}
		d, err := os.ReadFile(dumps[1])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(d), "still waiting for workers (") || !strings.Contains(string(d), "logx.(*Shutdown).Step") {
			t.Errorf("dump:\n%.2000s", d)
		}
		log := b.String()
		for _, want := range []string{"msg=\"shutdown begins\"", "msg=\"shutdown is slow\"", "waiting=\"workers (",
			"level=ERROR msg=\"shutdown hangs: exiting\"", "msg=\"shutdown step ended\"", "step=terminals"} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %s:\n%s", want, log)
			}
		}
		if strings.Contains(log, "terminals (") {
			t.Errorf("the ended step is still waited for:\n%s", log)
		}
	})
	t.Run("ended in time", func(t *testing.T) {
		capture(t)
		quick := &Shutdown{Dir: t.TempDir(), DumpAfter: 20 * time.Millisecond, ExitAfter: 40 * time.Millisecond,
			Exit: func(code int) { t.Errorf("a stop that ended exited %d", code) }}
		quick.Step("workers", func() {})
		quick.End()
		time.Sleep(80 * time.Millisecond)
		if d := Dumps(quick.Dir); len(d) != 0 {
			t.Errorf("a stop that ended dumped %v", d)
		}
	})
}
