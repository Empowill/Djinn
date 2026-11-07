package wrap

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// Remove removes, with docker, the containers that carry every label given (key=value), running or not: a worker's
// once it ended (TaskLabel), a Djinn's at its start (HomeLabel), its workers then all ended. Without docker, or with
// no such container, it does nothing; it gives up after half a minute.
func Remove(ctx context.Context, labels ...string) {
	docker, err := exec.LookPath("docker")
	if err != nil || len(labels) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"ps", "--all", "--quiet"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	out, err := exec.CommandContext(ctx, docker, args...).Output()
	if err != nil {
		return // No daemon here, or none that answers.
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return
	}
	var errOut bytes.Buffer
	rm := exec.CommandContext(ctx, docker, append([]string{"rm", "--force", "--volumes"}, ids...)...)
	rm.Stderr = &errOut
	err = rm.Run()
	logx.L().Info("containers removed", "labels", labels, "count", len(ids), "err", err, "stderr", strings.TrimSpace(errOut.String()))
}
