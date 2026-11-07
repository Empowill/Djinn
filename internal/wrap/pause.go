package wrap

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// Pause freezes, with docker pause, the running containers that carry every label given (key=value), or, pause false,
// thaws the paused ones with docker unpause: a worker's (TaskLabel) as the memory brake freezes or thaws it. Docker's
// daemon owns their cgroups, which Djinn cannot write. Without docker, or with no such container, it does nothing; it
// gives up after half a minute. It says how many containers it froze or thawed.
func Pause(ctx context.Context, pause bool, labels ...string) int {
	return pauseWith(ctx, "docker", pause, labels...)
}

// pauseWith is Pause with the docker found as name on the path.
func pauseWith(ctx context.Context, name string, pause bool, labels ...string) int {
	docker, err := exec.LookPath(name)
	if err != nil || len(labels) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, verb := "paused", "unpause"
	if pause {
		status, verb = "running", "pause"
	}
	args := []string{"ps", "--quiet", "--filter", "status=" + status}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	out, err := exec.CommandContext(ctx, docker, args...).Output()
	if err != nil {
		return 0 // No daemon here, or none that answers.
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return 0
	}
	var errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, docker, append([]string{verb}, ids...)...)
	cmd.Stderr = &errOut
	err = cmd.Run()
	logx.L().Info("containers "+verb+"d", "labels", labels, "count", len(ids), "err", err,
		"stderr", strings.TrimSpace(errOut.String()))
	if err != nil {
		return 0
	}
	return len(ids)
}
