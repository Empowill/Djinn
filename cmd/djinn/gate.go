package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

// runGate is djinn gate run: the one command that runs something on the caller's side, so it is written here and
// not generated from the protos; its help is cli.GateRun. It returns the exit code.
func runGate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	name, taskID, addr := "", os.Getenv("DJINN_TASK_ID"), os.Getenv("DJINN_ADDR")
	var command []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			command = args[i+1:]
			i = len(args)
		case a == "-h" || a == "--help":
			cli.GateRun.WriteHelp(stdout)
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
			fmt.Fprintf(stderr, "error: unexpected argument %q\n", a)
			cli.GateRun.WriteHelp(stderr)
			return 2
		}
	}
	if name == "" || len(command) == 0 {
		fmt.Fprintln(stderr, "error: a gate name and a command after -- are required")
		cli.GateRun.WriteHelp(stderr)
		return 2
	}
	code, err := func() (int, error) {
		client, base, err := dialDjinn(addr)
		if err != nil {
			return -1, err
		}
		// Ctrl-C reaches the command too: djinn waits for it to end, then gives the gate back.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return gate.Run(ctx, machinev1connect.NewGateServiceClient(client, base), name, taskID, gate.Command{
			Args: command, Stdin: stdin, Stdout: stdout, Stderr: stderr, Notice: stderr,
			Costs: machinev1connect.NewCommandServiceClient(client, base),
		})
	}()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return code
}

// dialDjinn reaches the djinn at addr, or the one djinn up runs when addr is empty.
func dialDjinn(addr string) (connect.HTTPClient, string, error) {
	if addr == "" {
		home, err := ui.Home()
		if err != nil {
			return nil, "", err
		}
		if addr, err = server.ReadAddr(home); err != nil {
			return nil, "", errors.New("no djinn answers: run djinn up first")
		}
	}
	return cli.Dial(addr)
}

// runGateHook is djinn gate hook, Claude's PreToolUse hook on a worker's Bash and Monitor (cli.GateHook): it waits
// until Djinn admits a command that neither reads nor edits, then lets it run as it was, telling the worker why it
// waited when it did.
func runGateHook(stdin io.Reader, stdout, stderr io.Writer) int {
	err := gate.Hook(stdin, stdout, func(what, dir, match string) (string, error) {
		client, base, err := dialDjinn(os.Getenv("DJINN_ADDR"))
		if err != nil {
			return "", err
		}
		asked := time.Now()
		stream, err := machinev1connect.NewGateServiceClient(client, base).Admit(context.Background(), connect.NewRequest(&machinev1.GateServiceAdmitRequest{
			TaskId: os.Getenv("DJINN_TASK_ID"), What: what, Directory: dir, AgentPid: int32(os.Getppid()), Match: match,
		}))
		if err != nil {
			return "", err
		}
		defer stream.Close()
		why := ""
		for stream.Receive() {
			switch msg := stream.Msg(); msg.GetState() {
			case machinev1.GateState_GATE_STATE_WAITING:
				why = msg.GetReason()
			case machinev1.GateState_GATE_STATE_HELD:
				if why == "" {
					return "", nil
				}
				return fmt.Sprintf("djinn: this command waited %s for its admission by the gates: %s",
					time.Since(asked).Round(time.Second), why), nil
			}
		}
		return "", cmp.Or(stream.Err(), errors.New("djinn ended the call"))
	})
	if err != nil {
		fmt.Fprintln(stderr, "djinn gate hook:", err)
		return 1
	}
	return 0
}
