package machine

import "os/exec"

// detach does nothing on Windows, where Djinn asks no login shell.
func detach(*exec.Cmd) {}
