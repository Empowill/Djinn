package cli

import (
	"cmp"
	goflag "flag"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The commands written by hand, which the protos do not generate: cmd/djinn runs them, but their --help and their
// entry in the documentation are here, in one place. A test of cmd/djinn fails when it runs a command that is not in
// Builtins, or when one of Builtins runs nowhere.

// Up is djinn up.
var Up = Command{
	Name:    "up",
	Usage:   "djinn up [flags]",
	Summary: "Open Djinn: its window, its local server, and the lead's terminal.",
	Help: `Open Djinn: serve the interface and the server the command line talks to, then open the native window on them,
or with --browser print the URL to open. One djinn runs per data folder: a second djinn up brings the first one's
window to the front. On macOS and Linux the command line reaches it through a Unix socket and no port is open; on
Windows, and with --browser, through a loopback HTTP server guarded by a token. Either way djinn up writes its
address in the data folder, for the other commands.`,
	Flags: []Param{
		{Name: "--browser", Type: "bool", Help: "Print the URL to open in a browser instead of opening a window."},
		{Name: "--port", Type: "int", Help: "Port of the loopback HTTP server (with --browser, and on Windows); 0 picks a free one."},
		{
			Name: "--terminal", Type: "string",
			Help: `Command the terminal of the window runs, through the user's shell, e.g. "claude --resume <session>"; empty ` +
				`runs the shell itself.`,
		},
		{
			Name: "--terminal-dir", Type: "folder",
			Help: "Working directory of the terminal; empty is the folder of the first project, as the window lists them " +
				"(never the home folder: with no project, the window asks to create one).",
		},
		{
			Name: "--workers", Type: "int", Env: "DJINN_WORKERS",
			Help: "Most workers at once, 1 to 16; 0 decides from the machine (one per 2 cores and per 2 GiB of memory); " +
				"default $DJINN_WORKERS.",
		},
		{
			Name: "--warm-workers", Type: "bool",
			Help: "Keep a claude loaded and waiting for the next task of each project of the active wishes, in the slots " +
				"the running workers leave: it starts at once, and costs memory while it waits (about 300 MB each, " +
				"supposed), no token.",
		},
		{
			Name: "--worker-cpu", Type: "int", Env: "DJINN_WORKER_CPU",
			Help: "Cap each worker's CPU at this percent of one core (150 is a core and a half), in a systemd user scope of " +
				"its own; Linux with systemd only, and only where systemd gives your user the cpu controller, else workers " +
				"run uncapped and djinn says why; 0 caps nothing; default $DJINN_WORKER_CPU.",
		},
		{
			Name: "--worker-memory", Type: "int", Env: "DJINN_WORKER_MEMORY",
			Help: "Cap each worker's memory at this many MiB, in its systemd user scope: past it the kernel reclaims, then " +
				"kills a process of the worker; Linux with systemd only, and only where systemd gives your user the memory " +
				"controller; 0 caps nothing; default $DJINN_WORKER_MEMORY.",
		},
		{
			Name: "--worker-memory-guard", Type: "float", Env: "DJINN_WORKER_MEMORY_GUARD",
			Help: "Cap each worker's memory dynamically at this multiple of its provider's observed peak (median of latest " +
				"10 finished workers) and at least the forecast; typical values are 2 to 3; 0 turns the guardrail off; " +
				"Linux with systemd only, and only where systemd gives your user the memory controller; without measurements, " +
				"no ceiling applies; default $DJINN_WORKER_MEMORY_GUARD.",
		},
		{
			Name: "--answer-workers", Type: "bool", Default: "false", Env: "DJINN_ANSWER_WORKERS",
			Help: "Start a worker to turn a developer's answer into tasks; default $DJINN_ANSWER_WORKERS (on or off), " +
				"else off (the lead turns answers into tasks itself).",
		},
		{
			Name: "--enlighten-workers", Type: "bool", Default: "true", Env: "DJINN_ENLIGHTEN_WORKERS",
			Help: `Start a worker to investigate and revise a question after "Enlighten me"; default ` +
				"$DJINN_ENLIGHTEN_WORKERS (on or off), else on.",
		},
		{
			Name: "--question-workers", Type: "bool", Env: "DJINN_QUESTION_WORKERS",
			Help: "Deprecated: set --answer-workers and --enlighten-workers instead; sets both when given; " +
				"default $DJINN_QUESTION_WORKERS.",
		},
		{
			Name: "--pprof", Type: "bool", Env: "DJINN_PPROF",
			Help: "Serve Go's profiles of djinn up at /debug/pprof/, on its own address only (the Unix socket, or the " +
				"loopback server and its token), to measure what it spends its CPU and memory on; default " +
				"$DJINN_PPROF (on or off), else off.",
		},
	},
}

// Open is djinn open.
var Open = Command{
	Name:    "open",
	Usage:   "djinn open <link>",
	Summary: "Open a djinn:// link in Djinn, starting it when it does not run: what the system runs for one.",
	Help: `Open a djinn:// link in Djinn: djinn://tilasm/<id> shows the wish's Tilasms tab on that tilasm, djinn://wish/<id>
the wish. The system runs it for a link clicked anywhere (a browser, a chat, a Markdown file). It hands the link to
the running Djinn, starting it in the background when none runs. A link Djinn does not know is refused, and the
window says so. Without a link, djinn open is djinn up.`,
	Args: []Param{{Name: "<link>", Type: "string", Expect: "a djinn:// link", Help: "The link to open."}},
}

// Update is djinn update.
var Update = Command{
	Name:    "update",
	Usage:   "djinn update [--yes]",
	Summary: "Restart the running djinn on the newer one installed at its path, or on a newer release.",
	Help: `Restart the running djinn on the newer one installed at its path (go tool task install), or, for a djinn
installed from a release, on the newer release it offers, first downloaded and verified. Djinn notes its open
terminals, stops as when it quits (workers interrupted, nothing lost), and starts the new binary, which runs those
terminals again on the same sessions. The window's update button does the same.`,
	Flags: []Param{{Name: "--yes", Type: "bool", Help: "Restart without a terminal to run from, as a script does."}},
}

// Backup is djinn backup.
var Backup = Command{
	Name:    "backup",
	Usage:   "djinn backup [--file <archive>]",
	Summary: "Write the data folder into one archive, while Djinn runs or not.",
	Help: `Write the data folder into one archive: a consistent copy of the database, taken while Djinn runs, and the
files beside it. Worktrees (Git holds them), the socket, the server's address, the logs and environment files stay
out. Sending the archive elsewhere is left to a tool that does it well: see docs/backup.md. djinn backup restore puts
it back.`,
	Flags: []Param{{
		Name: "--file", Type: "file",
		Help: "Archive to write, ending in .tar.gz, .tgz or .zip; replaced if it exists. Default: a new file in the " +
			"Downloads folder.",
	}},
}

// Restore is djinn backup restore.
var Restore = Command{
	Name:    "backup restore",
	Usage:   "djinn backup restore <archive>",
	Summary: "Put an archive of djinn backup back as the data folder, while Djinn is stopped.",
	Help: `Put an archive back as the data folder, while Djinn is stopped. The previous folder is kept aside, next to
it. A project whose folder is not on this machine is detached: djinn project add attaches it again.`,
	Args: []Param{{Name: "<archive>", Type: "file", Help: "Archive written by djinn backup."}},
}

// Version is djinn version.
var Version = Command{
	Name:    "version",
	Usage:   "djinn version",
	Summary: "Print the version of djinn.",
	Help:    "Print the version of djinn.",
}

// MCP is djinn mcp.
var MCP = Command{
	Name:    "mcp",
	Usage:   "djinn mcp [--addr URL]",
	Summary: "Serve these commands as MCP tools on stdin and stdout, for an agent that speaks MCP.",
	Help: `Serve the commands of djinn as MCP tools, on stdin and stdout (the stdio transport of the Model Context
Protocol). Each public method that answers once is a tool: djinn wish set-lead is wish_set_lead. Its arguments
are the fields of the request, by their proto names, read and checked as the command line does. A relative path
starts from the folder djinn mcp runs in. The streaming methods (watch, gate hold) stay on the command line.

Add it to an agent, for example: claude mcp add djinn -- djinn mcp`,
}

// GateRun is djinn gate run.
var GateRun = Command{
	Name:    "gate run",
	Usage:   "djinn gate run <name> [--task-id <task>] -- <command> [arguments]",
	Summary: "Run a command under a gate: one holder at a time, while the machine is not under pressure.",
	Help: `Take the gate <name>, run the command once it is granted, and give the gate back when the command ends, fails
or is interrupted. Djinn grants a gate to one holder at a time, and only while the machine is not under pressure;
meanwhile it says why it waits. The exit code is the command's. Djinn records what the command cost in its project
(CPU time, peak memory, duration): djinn command list. Outside a running worker, the gate takes a worker's slot.
Djinn takes it back after an hour, the command going on.`,
	Args: []Param{
		{Name: "<name>", Type: "string", Help: "The gate: codegen, stack, e2e, paid, or any other."},
		{Name: "<command>", Type: "string", Help: "The command to run, after --, then its arguments."},
	},
	Flags: []Param{
		{Name: "--task-id", Type: "string", Env: "DJINN_TASK_ID", Help: "Task the gate is taken for (default $DJINN_TASK_ID): its events show the gate."},
		{Name: "--addr", Type: "string", Env: "DJINN_ADDR", Help: "Address of the djinn server (default $DJINN_ADDR, then the one djinn up writes)."},
	},
}

// Builtins are the commands written by hand, in the order the help lists them.
var Builtins = []Command{Up, Open, Update, Backup, Restore, Version, MCP, GateRun}

// Builtin returns the command written by hand whose words args start with, the longest, and the arguments after them.
func Builtin(args []string) (Command, []string, bool) {
	var found Command
	n := 0
	for _, b := range Builtins {
		words := strings.Fields(b.Name)
		if len(words) > n && len(args) >= len(words) && slices.Equal(args[:len(words)], words) {
			found, n = b, len(words)
		}
	}
	return found, args[n:], n > 0
}

// FlagSet is the flag set of c, each flag stored in its variable of vars, by name without its dashes: a *bool, an
// *int or a *string, set to the flag's default. Its usage is c's help. It panics when a flag has no variable or a
// variable no flag: the documentation would lie.
func (c Command) FlagSet(vars map[string]any) *goflag.FlagSet {
	fs := goflag.NewFlagSet(c.Name, goflag.ContinueOnError)
	fs.Usage = func() { c.WriteHelp(fs.Output()) }
	for _, p := range c.Flags {
		name := strings.TrimPrefix(p.Name, "--")
		v, ok := vars[name]
		if !ok {
			panic(fmt.Sprintf("djinn %s --%s has no variable", c.Name, name))
		}
		switch v := v.(type) {
		case *bool:
			def, err := strconv.ParseBool(cmp.Or(p.Default, "false"))
			if err != nil {
				panic(err)
			}
			fs.BoolVar(v, name, def, p.Help)
		case *int:
			def, err := strconv.Atoi(cmp.Or(p.Default, "0"))
			if err != nil {
				panic(err)
			}
			fs.IntVar(v, name, def, p.Help)
		case *float64:
			def, err := strconv.ParseFloat(cmp.Or(p.Default, "0"), 64)
			if err != nil {
				panic(err)
			}
			fs.Float64Var(v, name, def, p.Help)
		case *string:
			fs.StringVar(v, name, p.Default, p.Help)
		default:
			panic(fmt.Sprintf("djinn %s --%s: a %T is no flag", c.Name, name, v))
		}
	}
	for name := range vars {
		if fs.Lookup(name) == nil {
			panic(fmt.Sprintf("djinn %s has a variable %s and no such flag", c.Name, name))
		}
	}
	return fs
}
