package machine

import (
	"strings"
	"unicode"
)

// FindCommand finds the process that runs command among those root started, at any depth, leaving out each pid skip
// says: the shell an agent runs a command of its Bash in. It matches by the letters and digits of the command's
// first line (Match), which the shell's command line holds whatever the quoting; of a shell and the processes it
// started, the shell; of two shells, the one started last. 0 when none is found, or where the processes' command
// lines are not read (Windows).
func FindCommand(root int, match string, skip func(pid int) bool) int {
	if root <= 0 || len(match) < 2 {
		return 0
	}
	procs, lines, err := listCommands()
	if err != nil {
		return 0
	}
	return findCommand(procs, lines, root, match, skip)
}

// findCommand is FindCommand on the processes procs, lines[pid] their command lines.
func findCommand(procs []proc, lines map[int]string, root int, match string, skip func(pid int) bool) int {
	parent := make(map[int]int, len(procs))
	byPID := make(map[int]proc, len(procs))
	for _, p := range procs {
		parent[p.pid], byPID[p.pid] = p.ppid, p
	}
	matches := func(pid int) bool {
		return pid != root && !(skip != nil && skip(pid)) && strings.Contains(alnum(lines[pid], -1), match)
	}
	found := 0
	for _, p := range procs {
		if !matches(p.pid) {
			continue
		}
		outer, below := true, false
		for q, depth := parent[p.pid], 0; depth < 64; q, depth = parent[q], depth+1 {
			if q == root {
				below = true
				break
			}
			if q <= 1 {
				break
			}
			if matches(q) {
				outer = false
			}
		}
		if below && outer && (found == 0 || p.start > byPID[found].start) {
			found = p.pid
		}
	}
	return found
}

// Match is what FindCommand finds a command by: the letters and digits of its first line, up to 120. A shell that
// runs the command holds them in its command line, in that order, however it quoted the rest.
func Match(command string) string {
	line, _, _ := strings.Cut(command, "\n")
	return alnum(line, 120)
}

// alnum is the letters and digits of s, up to most bytes (-1: all).
func alnum(s string, most int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			if most >= 0 && b.Len() >= most {
				break
			}
		}
	}
	return b.String()
}
