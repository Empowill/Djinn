package logx

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// argMax is the longest an argument of a command is logged: the rest is cut.
const argMax = 80

// argsMax is how many arguments of a command are logged: the rest are counted.
const argsMax = 16

// Ran logs cmd once it has run since start, with the error it returned: its program and arguments (Args), its folder,
// how long it took, its exit code, and whether its context killed it at a deadline. Each one logs at info, at warn past
// Slow or killed at its deadline. ctx is the context cmd ran under, or nil; err is what running it returned, or nil
// when only its exit code is known.
func Ran(ctx context.Context, cmd *exec.Cmd, start time.Time, err error) {
	took := time.Since(start)
	level := slog.LevelInfo
	args := []any{"argv", Argv(cmd.Args), "dir", cmd.Dir, "took", took.Round(time.Millisecond)}
	exit := 0
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exit = -1
	}
	args = append(args, "exit", exit)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			args = append(args, "err", err)
		}
	}
	if took > Slow {
		level = slog.LevelWarn
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		level = slog.LevelWarn
		args = append(args, "timeout", true)
	}
	L().Log(context.Background(), level, "command", args...)
}

// Argv is args as logged: the program by its base name, a message, title or body hidden, a password or token of a URL hidden,
// each argument cut at argMax, and past argsMax only their count.
func Argv(args []string) string {
	var b strings.Builder
	hide := false
	for i, a := range args {
		if i == argsMax {
			b.WriteString(" …+")
			b.WriteString(strconv.Itoa(len(args) - i))
			break
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		switch {
		case i == 0:
			a = filepath.Base(a)
		case hide:
			a, hide = "…", false
		case a == "-m" || a == "--message" || a == "--body" || a == "--title" || a == "--description":
			hide = true
		case strings.HasPrefix(a, "--message=") || strings.HasPrefix(a, "--body=") || strings.HasPrefix(a, "--title=") ||
			strings.HasPrefix(a, "--description="):
			a = a[:strings.IndexByte(a, '=')+1] + "…"
		default:
			a = hideUser(a)
		}
		if len(a) > argMax {
			a = a[:argMax] + "…"
		}
		b.WriteString(a)
	}
	return b.String()
}

// hideUser hides the user and password of a URL.
func hideUser(a string) string {
	if !strings.Contains(a, "://") || !strings.Contains(a, "@") {
		return a
	}
	u, err := url.Parse(a)
	if err != nil || u.User == nil {
		return a
	}
	u.User = url.User("…")
	return u.String()
}
