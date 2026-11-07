package wrap

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/empowill/djinn/internal/fsx"
)

// What Djinn tells the wrappers through a worker's environment.
const (
	// EnvMemory and EnvPIDs are the ceilings of the worker's containers (Caps); without them, docker runs as is.
	EnvMemory = "DJINN_CONTAINER_MEMORY"
	EnvPIDs   = "DJINN_CONTAINER_PIDS"
	// EnvHome is the data folder of the Djinn that runs the worker, its containers' HomeLabel.
	EnvHome = "DJINN_CONTAINER_HOME"
	// EnvAdmit, "1", has the wrapped commands wait for their admission by the gates: a worker whose agent has no
	// hook Djinn runs before each command (Codex, Antigravity).
	EnvAdmit = "DJINN_ADMIT_HEADS"
	// EnvAdmitted marks the processes of an admitted command: a wrapped command it runs (go test running go, npm
	// running npx) is part of its admission, and runs at once.
	EnvAdmitted = "DJINN_ADMITTED"
)

// Heads are the heavy commands wrapped for an agent without a hook (G1 of L04): builds, tests, package managers.
// docker is wrapped for every worker, in a folder of its own (DockerDir).
var Heads = []string{"go", "npm", "npx", "pnpm", "task", "make", "golangci-lint"}

// DockerDir and HeadsDir are the wrappers' folders, under root (Root): docker alone, for every worker; the heavy
// heads, for the agents without a hook.
func DockerDir(root string) string { return filepath.Join(root, "docker") }

// HeadsDir is the folder of the heavy heads' wrappers, under root.
func HeadsDir(root string) string { return filepath.Join(root, "heads") }

// Root is where Djinn writes the wrappers, under its data folder home.
func Root(home string) string { return filepath.Join(home, "bin", "wrappers") }

// script is the wrapper of head: a shell script that runs djinn's wrapper in its place, the same process.
func script(djinn, root, head string) string {
	return "#!/bin/sh\n# Written by Djinn: " + head + " of a worker, bound and admitted by Djinn (djinn gate wrap).\n" +
		"exec " + quote(djinn) + " gate wrap " + quote(root) + " " + head + " \"$@\"\n"
}

// quote quotes s for sh.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Write writes the wrappers under root, each run by djinn, when they differ from what is there: workers starting
// together each write their own and rename it.
func Write(root, djinn string) error {
	write := func(dir, head string) error {
		file, body := filepath.Join(dir, head), script(djinn, root, head)
		if b, err := os.ReadFile(file); err == nil && string(b) == body {
			return nil
		}
		return fsx.WriteAtomic(file, []byte(body), 0o755)
	}
	if err := write(DockerDir(root), "docker"); err != nil {
		return err
	}
	for _, head := range Heads {
		if err := write(HeadsDir(root), head); err != nil {
			return err
		}
	}
	return nil
}

// Path is the PATH of a worker: the docker wrapper first, after the heavy heads' when heads, then path.
func Path(root, path string, heads bool) string {
	dirs := []string{DockerDir(root)}
	if heads {
		dirs = []string{HeadsDir(root), DockerDir(root)}
	}
	return strings.Join(append(dirs, path), string(os.PathListSeparator))
}

// Admit has command, as the worker wrote it, wait for its admission by the gates, unless it only reads (gate.Plain):
// its cost is kept under it. Its process is found under the process agent by what it runs, run: docker's arguments
// once bound. It says why it waited, "" when it did not.
type Admit func(command, run string, agent int) (string, error)

// Exec replaces the current process with the real command: Unix's exec.
type Exec func(path string, args, env []string) error

// Main runs head with args as djinn gate wrap does, root being the wrappers' folder: it finds the real head on the
// path without the wrappers, binds and bounds docker's containers (Docker), has the command wait for its admission
// when the environment asks it (EnvAdmit), then runs it in its place (exec). It returns an exit code only when it
// does not run the command.
func Main(root, head string, args []string, stderr io.Writer, admit Admit, exec Exec) int {
	if !wrapped(head) {
		fmt.Fprintf(stderr, "djinn: %s is not a command Djinn wraps\n", head)
		return 2
	}
	env := os.Environ()
	found, err := lookPath(head, os.Getenv("PATH"), root)
	if err != nil {
		fmt.Fprintf(stderr, "djinn: %s: not found on the path\n", head)
		return 127
	}
	command := strings.Join(append([]string{head}, args...), " ")
	if head == "docker" {
		if caps, ok := capsFrom(os.Getenv); ok {
			var notes []string
			args, notes, err = Docker(args, caps, os.Getenv("DJINN_TASK_ID"), os.Getenv(EnvHome))
			if err != nil {
				fmt.Fprintln(stderr, "djinn:", err)
				return 1
			}
			for _, n := range notes {
				fmt.Fprintf(stderr, "djinn: %s; %s raises it\n", n, Setting)
			}
		}
	}
	if os.Getenv(EnvAdmit) == "1" && os.Getenv(EnvAdmitted) == "" && admit != nil {
		// The command is found under the process that ran this one: this process becomes it.
		run := strings.Join(append([]string{head}, args...), " ")
		waited, err := admit(command, run, os.Getppid())
		if err == nil && waited != "" {
			fmt.Fprintln(stderr, waited)
		}
		env = append(env, EnvAdmitted+"=1")
	}
	err = exec(found, append([]string{head}, args...), env)
	fmt.Fprintf(stderr, "djinn: run %s: %v\n", found, err)
	return 126
}

// capsFrom reads the ceilings of a worker's containers from its environment; false when Djinn set none.
func capsFrom(getenv func(string) string) (Caps, bool) {
	memory, err1 := strconv.ParseUint(getenv(EnvMemory), 10, 64)
	pids, err2 := strconv.ParseInt(getenv(EnvPIDs), 10, 64)
	if err1 != nil || err2 != nil || memory == 0 || pids <= 0 {
		return Caps{}, false
	}
	return Caps{Memory: memory, PIDs: pids}, true
}

// lookPath finds the executable head on path, leaving out the folders under root: the wrappers.
func lookPath(head, path, root string) (string, error) {
	root = filepath.Clean(root)
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if d := filepath.Clean(dir); d == root || strings.HasPrefix(d, root+string(filepath.Separator)) {
			continue
		}
		file := filepath.Join(dir, head)
		if info, err := os.Stat(file); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			if !filepath.IsAbs(file) {
				if abs, err := filepath.Abs(file); err == nil {
					file = abs
				}
			}
			return file, nil
		}
	}
	return "", exec.ErrNotFound
}

// Env is what a worker's environment tells the wrappers: its path, its containers' ceilings and Djinn's data folder,
// and whether its heavy commands wait for their admission (heads). path is Djinn's own PATH.
func Env(root, home, path string, caps Caps, heads bool) []string {
	env := []string{
		"PATH=" + Path(root, path, heads),
		EnvMemory + "=" + strconv.FormatUint(caps.Memory, 10),
		EnvPIDs + "=" + strconv.FormatInt(caps.PIDs, 10),
		EnvHome + "=" + home,
	}
	if heads {
		env = append(env, EnvAdmit+"=1")
	}
	return env
}

// wrapped says whether head is one of the wrapped commands.
func wrapped(head string) bool { return head == "docker" || slices.Contains(Heads, head) }
