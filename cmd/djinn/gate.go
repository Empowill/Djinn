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

const gateUsage = `Usage: djinn gate run <name> [--task-id <task>] -- <command> [arguments]

Take the gate <name> (codegen, stack, e2e, paid, or any other), run the command once it is granted, and give the
gate back when the command ends, fails or is interrupted. Djinn grants a gate to one holder at a time, and only
while the machine is not under pressure; meanwhile it says why it waits. The exit code is the command's.
Djinn records what the command cost in its project (CPU time, peak memory, duration): djinn command list.

  --task-id <task>   Task the gate is taken for (default $DJINN_TASK_ID): its events show the gate.
  --addr URL         Address of the djinn server (default $DJINN_ADDR, then the one djinn up writes).
`

// runGate is djinn gate run: the one command that runs something on the caller's side, so it is written here and
// not generated from the protos. It returns the exit code.
func runGate(args []string) int {
	name, taskID, addr := "", os.Getenv("DJINN_TASK_ID"), os.Getenv("DJINN_ADDR")
	var command []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			command = args[i+1:]
			i = len(args)
		case a == "-h" || a == "--help":
			fmt.Print(gateUsage)
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
			fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n%s", a, gateUsage)
			return 2
		}
	}
	if name == "" || len(command) == 0 {
		fmt.Fprint(os.Stderr, "error: a gate name and a command after -- are required\n"+gateUsage)
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
