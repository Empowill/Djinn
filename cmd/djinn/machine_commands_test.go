package main

import (
	"bytes"
	"strings"
	"testing"

	"connectrpc.com/connect"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	"github.com/empowill/djinn/gen/go/machine/v1/machinev1connect"
	"github.com/empowill/djinn/internal/cli"
)

func TestSparkline(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		values []float64
		want   string
	}{
		{"empty", nil, ""},
		{"all zero", []float64{0, 0, 0}, "   "},
		{"constant", []float64{5, 5, 5}, "   "},
		{"ascending", []float64{1, 2, 3, 4, 5, 6, 7, 8}, " ▂▃▄▅▆▇█"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := sparkline(c.values); got != c.want {
				t.Errorf("sparkline(%v) = %q, want %q", c.values, got, c.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		bytes uint64
		want  string
	}{
		{"unknown", 0, "-"},
		{"under a KiB", 512, "0 KiB"},
		{"KiB", 1024, "1 KiB"},
		{"MiB", 500 * (1 << 20), "500 MiB"},
		{"GiB", 2 * (1 << 30), "2.0 GiB"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := formatBytes(c.bytes); got != c.want {
				t.Errorf("formatBytes(%d) = %q, want %q", c.bytes, got, c.want)
			}
		})
	}
}

// TestRunMachineCommands covers runMachineCommands, djinn machine commands: its arguments, the djinn it cannot reach,
// and the table of what each command measured costs, by project.
func TestRunMachineCommands(t *testing.T) {
	addr, project := machineServer(t)
	empty, _ := machineServer(t)
	httpClient, base, err := cli.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machinev1connect.NewCommandServiceClient(httpClient, base).Record(t.Context(), connect.NewRequest(
		&machinev1.CommandServiceRecordRequest{
			Directory: project.GetDirectory(), Command: "go test ./...", Seconds: 42, HeldSeconds: 42, WaitSeconds: 3,
			CpuSeconds: 300, PeakMemoryBytes: 3 << 30, ExitCode: 1, Gate: "test",
		})); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		env  map[string]string
		args []string
		code int
		out  []string
		errs string
	}{
		{name: "help", args: []string{"--help"}, out: []string{"Usage: djinn machine commands"}},
		{name: "an argument", args: []string{"lamp"}, code: 2, errs: `unexpected argument "lamp"`},
		{name: "no djinn runs", env: map[string]string{"DJINN_HOME": t.TempDir()}, code: 1, errs: "no djinn answers: run djinn up first"},
		{name: "djinn is gone", args: []string{"--addr", "http://127.0.0.1:1"}, code: 1, errs: "error: "},
		{name: "nothing measured", args: []string{"--addr=" + empty}, out: []string{"no commands measured yet"}},
		{name: "a project's commands", args: []string{"--addr", addr, "--project", "lamp"}, out: []string{
			"PROJECT  COMMAND", "lamp     go test ./...  1     42.0s", "300.0s  3.0 GiB   1",
		}},
		{name: "an unknown project", args: []string{"--addr", addr, "--project=bell"}, code: 1, errs: "error: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			var out, errs bytes.Buffer
			code := runMachineCommands(c.args, &out, &errs)
			ok := code == c.code && strings.Contains(errs.String(), c.errs)
			for _, want := range c.out {
				ok = ok && strings.Contains(out.String(), want)
			}
			if !ok {
				t.Errorf("djinn machine commands %q = %d\n%s%s", c.args, code, out.String(), errs.String())
			}
		})
	}
}
