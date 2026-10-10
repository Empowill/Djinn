package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

// runGate is djinn gate run: the one command that runs something on the caller's side, so it is written here and
// not generated from the protos; its help is cli.GateRun. It returns the exit code.
func runGate(args []string) int {
	name, taskID, addr := "", os.Getenv("DJINN_TASK_ID"), os.Getenv("DJINN_ADDR")
	var command []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			command = args[i+1:]
			i = len(args)
		case a == "-h" || a == "--help":
			cli.GateRun.WriteHelp(os.Stdout)
			return 0
		case (a == "--task-id" || a == "--addr") && i+1 < len(args):
			i++
			if a == "--task-id" {
				taskID = args[i]
			} else {
				addr = args[i]
			}
		case strings.HasPrefix(a, "--task-id="):
			taskID = strings.TrimPrefix(a, "--task-id=")
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		case name == "" && !strings.HasPrefix(a, "-"):
			name = a
		default:
			fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", a)
			cli.GateRun.WriteHelp(os.Stderr)
			return 2
		}
	}
	if name == "" || len(command) == 0 {
		fmt.Fprintln(os.Stderr, "error: a gate name and a command after -- are required")
		cli.GateRun.WriteHelp(os.Stderr)
		return 2
	}
	code, err := func() (int, error) {
		if addr == "" {
			home, err := ui.Home()
			if err != nil {
				return -1, err
			}
			if addr, err = server.ReadAddr(home); err != nil {
				return -1, errors.New("no djinn answers: run djinn up first")
			}
		}
		client, base, err := cli.Dial(addr)
		if err != nil {
			return -1, err
		}
		// Ctrl-C reaches the command too: djinn waits for it to end, then gives the gate back.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return gate.Run(ctx, machinev1connect.NewGateServiceClient(client, base), name, taskID, gate.Command{
			Args: command, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Notice: os.Stderr,
			Costs: machinev1connect.NewCommandServiceClient(client, base),
		})
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return code
}
