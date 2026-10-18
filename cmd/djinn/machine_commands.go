package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/ui"
)

var sparkBlocks = []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

func sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	minVal, maxVal := values[0], values[0]
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	runes := make([]rune, len(values))
	span := maxVal - minVal
	for i, v := range values {
		if span <= 0 {
			runes[i] = sparkBlocks[0]
		} else {
			idx := int((v - minVal) / span * float64(len(sparkBlocks)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(sparkBlocks) {
				idx = len(sparkBlocks) - 1
			}
			runes[i] = sparkBlocks[idx]
		}
	}
	return string(runes)
}

func formatBytes(b uint64) string {
	if b == 0 {
		return "-"
	}
	if b < 1<<20 {
		return fmt.Sprintf("%d KiB", b>>10)
	}
	if b < 1<<30 {
		return fmt.Sprintf("%d MiB", b>>20)
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

func runMachineCommands(args []string, stdout, stderr io.Writer) int {
	var project, addr string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			cli.MachineCommands.WriteHelp(stdout)
			return 0
		case (a == "--project" || a == "--addr") && i+1 < len(args):
			i++
			if a == "--project" {
				project = args[i]
			} else {
				addr = args[i]
			}
		case strings.HasPrefix(a, "--project="):
			project = strings.TrimPrefix(a, "--project=")
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		default:
			fmt.Fprintf(stderr, "error: unexpected argument %q\n", a)
			cli.MachineCommands.WriteHelp(stderr)
			return 2
		}
	}

	if addr == "" {
		home, err := ui.Home()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if addr, err = server.ReadAddr(home); err != nil {
			fmt.Fprintln(stderr, "error: no djinn answers: run djinn up first")
			return 1
		}
	}
	client, base, err := cli.Dial(addr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmdClient := machinev1connect.NewCommandServiceClient(client, base)
	res, err := cmdClient.List(ctx, connect.NewRequest(&machinev1.CommandServiceListRequest{Project: project}))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	costs := res.Msg.GetCosts()
	if len(costs) == 0 {
		fmt.Fprintln(stdout, "no commands measured yet")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tCOMMAND\tRUNS\tHELD P50\tHELD P95\tHELD LAST\tWAIT P50\tWAIT P95\tWAIT LAST\tCPU\tPEAK MEM\tEXIT\tSPARKLINE")
	for _, c := range costs {
		proj := c.GetProjectName()
		if proj == "" {
			proj = c.GetProjectId()
		}
		if proj == "" {
			proj = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%.1fs\t%.1fs\t%.1fs\t%.1fs\t%.1fs\t%.1fs\t%.1fs\t%s\t%d\t%s\n",
			proj,
			c.GetCommand(),
			c.GetRuns(),
			c.GetP50HeldSeconds(),
			c.GetP95HeldSeconds(),
			c.GetLastHeldSeconds(),
			c.GetP50WaitSeconds(),
			c.GetP95WaitSeconds(),
			c.GetLastWaitSeconds(),
			c.GetCpuSeconds(),
			formatBytes(c.GetPeakMemoryBytes()),
			c.GetLastExitCode(),
			sparkline(c.GetRecentHeldSeconds()),
		)
	}
	tw.Flush()
	return 0
}
