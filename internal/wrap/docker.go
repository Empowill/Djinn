// Package wrap is the commands Djinn puts first on a worker's path: docker, which binds the containers the worker
// starts to its task and bounds them, and, for the agents Claude's hook does not cover (Codex, Antigravity), the heavy
// heads (go, npm, make…), which wait for their admission by the gates. Each finds the real command by leaving the
// wrappers' folder out of the path.
package wrap

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Caps are the runaway ceilings of a worker's containers (djinn.v1.Containers, resolved).
type Caps struct {
	// Memory is a container's memory, swap included, in bytes.
	Memory uint64
	// PIDs is a container's processes and threads.
	PIDs int64
}

// TaskLabel names the label that binds a container to the task whose worker started it; HomeLabel the one that
// names the Djinn, by its data folder: a Djinn removes at its start the containers its workers left, never another
// Djinn's.
const (
	TaskLabel = "djinn.task"
	HomeLabel = "djinn.home"
)

// Slice is the systemd slice a task's containers run under (docker's --cgroup-parent): djinn-<task id, its hyphens
// dropped>.slice, which systemd places under djinn.slice, a dash marking a level.
func Slice(taskID string) string {
	return "djinn-" + strings.ReplaceAll(taskID, "-", "") + ".slice"
}

// ErrRefused is why the wrapper refuses a docker command: it would lift a container's ceilings.
var ErrRefused = errors.New("refused by Djinn")

// Setting names where a project raises its ceilings, as the wrapper's messages say it.
const Setting = ".agents/containers.txtpb (memory_mib, pids)"

// Docker rewrites the arguments of docker (args, the command name left out) for the worker of taskID in the Djinn
// of home: docker run and docker create (docker container run, create) get the task's label and slice, and the
// ceilings caps, a lower memory or pids limit the worker gave kept, a higher one or none brought down; the swap is
// set equal to the memory, so that a container never fills the swap. docker update that would lift a ceiling is
// refused (ErrRefused). Every other command is left as it is. The notes say what was brought down.
func Docker(args []string, caps Caps, taskID, home string) (out []string, notes []string, err error) {
	sub, at := subcommand(args)
	switch sub {
	case "run", "create", "container run", "container create":
		return bound(args, at, caps, taskID, home)
	case "update", "container update":
		return args, nil, checkUpdate(args[at:], caps)
	}
	return args, nil, nil
}

// globalValue are docker's global options that take a value, before its command.
var globalValue = map[string]bool{
	"--config": true, "-c": true, "--context": true, "-H": true, "--host": true, "-l": true, "--log-level": true,
	"--tlscacert": true, "--tlscert": true, "--tlskey": true,
}

// subcommand finds docker's command among args, past its global options: "run", "container run"…, and the index of
// the first argument after it; "" when there is none.
func subcommand(args []string) (string, int) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return "", len(args)
		case strings.HasPrefix(a, "-"):
			if globalValue[a] {
				i++
			}
		case a == "container" && i+1 < len(args):
			return a + " " + args[i+1], i + 2
		default:
			return a, i + 1
		}
	}
	return "", len(args)
}

// runBool are the options of docker run and create that take no value; every other option takes one.
var runBool = map[string]bool{
	"--detach": true, "--interactive": true, "--tty": true, "--rm": true, "--init": true, "--privileged": true,
	"--read-only": true, "--publish-all": true, "--no-healthcheck": true, "--oom-kill-disable": true,
	"--disable-content-trust": true, "--quiet": true, "--sig-proxy": true, "--help": true, "--use-api-socket": true,
}

// runShortBool are the one-letter options of docker run and create that take no value.
const runShortBool = "ditPq"

// option is one option of a docker command: its name ("--memory", "-m"), its value, and where it lies in args.
type option struct {
	name, value string
	from, to    int    // args[from:to]
	flags       string // the one-letter booleans grouped before it ("-d" of "-dm 1g"), kept when it is dropped
}

// options lists the options of a docker run or create among args, up to its image, and returns where the image is.
func options(args []string, start int) ([]option, int) {
	var opts []option
	for i := start; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return opts, i + 1
		case strings.HasPrefix(a, "--"):
			if name, value, ok := strings.Cut(a, "="); ok {
				opts = append(opts, option{name: name, value: value, from: i, to: i + 1})
				continue
			}
			if runBool[a] || i+1 >= len(args) {
				opts = append(opts, option{name: a, from: i, to: i + 1})
				continue
			}
			opts = append(opts, option{name: a, value: args[i+1], from: i, to: i + 2})
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// A group of one-letter options: booleans, then at most one that takes the rest, or the next argument.
			j := 1
			for j < len(a) && strings.IndexByte(runShortBool, a[j]) >= 0 {
				j++
			}
			switch {
			case j == len(a):
				opts = append(opts, option{name: a, from: i, to: i + 1})
			case j+1 < len(a):
				opts = append(opts, option{"-" + a[j:j+1], strings.TrimPrefix(a[j+1:], "="), i, i + 1, booleans(a[1:j])})
			case i+1 < len(args):
				opts = append(opts, option{"-" + a[j:j+1], args[i+1], i, i + 2, booleans(a[1:j])})
				i++
			default:
				opts = append(opts, option{name: a, from: i, to: i + 1})
			}
		default:
			return opts, i
		}
	}
	return opts, len(args)
}

// booleans writes the one-letter booleans grouped before an option: "-d" for "d"; "" for none.
func booleans(letters string) string {
	if letters == "" {
		return ""
	}
	return "-" + letters
}

// bound is Docker for docker run and create, whose options start at start.
func bound(args []string, start int, caps Caps, taskID, home string) ([]string, []string, error) {
	opts, image := options(args, start)
	memory, pids := caps.Memory, caps.PIDs
	var notes []string
	drop := map[int]bool{}
	for _, o := range opts {
		switch o.name {
		case "-m", "--memory":
			drop[o.from] = true
			if v, err := Bytes(o.value); err == nil && v > 0 && uint64(v) <= caps.Memory {
				memory = min(memory, uint64(v))
			} else {
				notes = append(notes, fmt.Sprintf("--memory %s brought down to the runaway ceiling of a worker's container, %s", o.value, Size(caps.Memory)))
			}
		case "--memory-swap":
			drop[o.from] = true // Set equal to the memory: a container never swaps past it.
		case "--pids-limit":
			drop[o.from] = true
			if v, err := strconv.ParseInt(o.value, 10, 64); err == nil && v > 0 && v <= caps.PIDs {
				pids = min(pids, v)
			} else {
				notes = append(notes, fmt.Sprintf("--pids-limit %s brought down to the runaway ceiling of a worker's container, %d", o.value, caps.PIDs))
			}
		case "--cgroup-parent":
			drop[o.from] = true
			notes = append(notes, "--cgroup-parent "+o.value+" replaced: the container runs in its task's slice")
		}
	}
	out := append(make([]string, 0, len(args)+16), args[:start]...)
	if taskID != "" {
		out = append(out, "--label", TaskLabel+"="+taskID, "--cgroup-parent", Slice(taskID))
	}
	if home != "" {
		out = append(out, "--label", HomeLabel+"="+home)
	}
	out = append(out, "--memory", strconv.FormatUint(memory, 10), "--memory-swap", strconv.FormatUint(memory, 10),
		"--pids-limit", strconv.FormatInt(pids, 10))
	for _, o := range opts {
		switch {
		case !drop[o.from]:
			out = append(out, args[o.from:o.to]...)
		case o.flags != "":
			out = append(out, o.flags)
		}
	}
	out = append(out, args[optionsEnd(opts, start, image):]...)
	return out, notes, nil
}

// optionsEnd is where what follows the options starts: the "--" that ends them, or the image.
func optionsEnd(opts []option, start, image int) int {
	end := start
	if len(opts) > 0 {
		end = opts[len(opts)-1].to
	}
	return min(end, image)
}

// checkUpdate refuses a docker update (its arguments after the command) that would lift a ceiling: a memory, a swap
// or a pids limit above caps, or unlimited.
func checkUpdate(args []string, caps Caps) error {
	opts, _ := options(args, 0)
	for _, o := range opts {
		switch o.name {
		case "-m", "--memory", "--memory-swap":
			if v, err := Bytes(o.value); err != nil || v < 0 || uint64(v) > caps.Memory {
				return fmt.Errorf("%w: %s %s would lift the runaway ceiling of a worker's container, %s; %s raises it",
					ErrRefused, o.name, o.value, Size(caps.Memory), Setting)
			}
		case "--pids-limit":
			if v, err := strconv.ParseInt(o.value, 10, 64); err != nil || v <= 0 || v > caps.PIDs {
				return fmt.Errorf("%w: --pids-limit %s would lift the runaway ceiling of a worker's container, %d; %s raises it",
					ErrRefused, o.value, caps.PIDs, Setting)
			}
		}
	}
	return nil
}

// sizeRe is a size as docker reads it: a number, then a binary unit (b, k, m, g, t, p), "i" and "b" allowed.
var sizeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?) ?([kKmMgGtTpP])?[iI]?[bB]?$`)

// Bytes reads a size as docker does (units of 1024): "4g", "512m", "1GiB", "1048576". "-1" reads as -1, unlimited.
func Bytes(s string) (int64, error) {
	if s == "-1" {
		return -1, nil
	}
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("not a size: %q", s)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	shift := 0
	if m[2] != "" {
		shift = strings.IndexByte("bkmgtp", strings.ToLower(m[2])[0])
	}
	return int64(f * float64(uint64(1)<<(10*shift))), nil
}

// Size writes bytes as a person reads it: 4 GiB, 512 MiB.
func Size(b uint64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", b>>30)
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	default:
		return fmt.Sprintf("%d MiB", b>>20)
	}
}
