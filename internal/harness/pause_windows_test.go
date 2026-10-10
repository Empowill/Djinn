//go:build windows

package harness

import (
	"errors"
	"os"
	"testing"
)

// TestPauseProcess: on Windows a worker's process refuses to pause, and says why.
func TestPauseProcess(t *testing.T) {
	t.Parallel()
	env, _, _ := fake{provider: "claude", fixture: "success", end: "wait"}.env(t)
	w, err := Claude{Command: os.Args[0]}.Start(t.Context(), Spec{TaskID: "t1", Dir: t.TempDir(), Prompt: "x", Env: env})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range w.Events() {
		}
	}()
	if err := w.(Pauser).Pause(); !errors.Is(err, errNoPause) {
		t.Errorf("pause on Windows: %v", err)
	}
	w.Stop()
	w.Wait()
}
