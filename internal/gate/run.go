package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
)

// Command is a command to run under a gate, with its standard streams.
type Command struct {
	Args                   []string
	Dir                    string
	Stdin                  io.Reader
	Stdout, Stderr, Notice io.Writer // Notice gets what djinn says: why it waits, a gate lost
}

// Run takes the gate name from the server, for the task taskID when not empty, runs the command once it holds the
// gate, and gives the gate back when the command ends, fails or is interrupted. It returns the command's exit code.
// When the caller is killed, its connection ends, and the server gives the gate back.
func Run(ctx context.Context, client machinev1connect.GateServiceClient, name, taskID string, c Command) (int, error) {
	if len(c.Args) == 0 {
		return -1, errors.New("no command to run")
	}
	what := c.Args[0]
	for _, a := range c.Args[1:] {
		if len(what)+1+len(a) > 200 {
			break
		}
		what += " " + a
	}
	hold, give := context.WithCancel(ctx)
	defer give()
	stream, err := client.Hold(hold, connect.NewRequest(&machinev1.GateServiceHoldRequest{Name: name, TaskId: taskID, What: what}))
	if err != nil {
		return -1, err
	}
	defer stream.Close()
	held := false
	for !held && stream.Receive() {
		switch msg := stream.Msg(); msg.GetState() {
		case machinev1.GateState_GATE_STATE_WAITING:
			fmt.Fprintf(c.Notice, "djinn: gate %s: waiting: %s\n", name, msg.GetReason())
		case machinev1.GateState_GATE_STATE_HELD:
			held = true
		}
	}
	if !held {
		if err := stream.Err(); err != nil {
			return -1, fmt.Errorf("gate %s: %w", name, err)
		}
		return -1, fmt.Errorf("gate %s: djinn ended the call before granting it", name)
	}
	// The gate is held as long as the stream is open; say so if djinn up goes away meanwhile.
	go func() {
		for stream.Receive() {
		}
		if hold.Err() == nil {
			fmt.Fprintf(c.Notice, "djinn: gate %s lost: djinn no longer answers (%v)\n", name, stream.Err())
		}
	}()
	cmd := exec.Command(c.Args[0], c.Args[1:]...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Dir, c.Stdin, c.Stdout, c.Stderr
	cmd.Env = os.Environ()
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return -1, err
}
