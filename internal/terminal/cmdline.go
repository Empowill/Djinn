package terminal

import "strings"

// cmdLine is the Windows command line of a ShellCommand: cmd.exe /s /c "line", the line as it is. Go and the
// pseudo-console quote each argument the C runtime's way, \" for a quote, which cmd.exe does not read: a line with
// quotes, a lead's brief or a PowerShell installer, would reach cmd.exe broken. With /s, cmd.exe removes the outer
// quotes and runs the rest, quotes, pipes and all, as typed. False when command is not a ShellCommand.
func cmdLine(command []string) (string, bool) {
	if len(command) != 4 || command[1] != "/s" || command[2] != "/c" {
		return "", false
	}
	exe := command[0]
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	return exe + ` /s /c "` + command[3] + `"`, true
}
