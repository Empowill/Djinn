package terminal

import "testing"

// cmd.exe gets a ShellCommand's line as typed, its quotes and pipes kept: a PowerShell installer, a lead's brief.
// The C runtime's quoting (\") would break both.
func TestCmdLine(t *testing.T) {
	for _, c := range []struct {
		command []string
		want    string
		ok      bool
	}{
		{[]string{"cmd.exe", "/s", "/c", `powershell -NoProfile -Command "irm https://claude.ai/install.ps1 | iex"`},
			`cmd.exe /s /c "powershell -NoProfile -Command "irm https://claude.ai/install.ps1 | iex""`, true},
		{[]string{`C:\Program Files\cmd.exe`, "/s", "/c", `claude --append-system-prompt-file "C:\d\rules.md" "Lead it."`},
			`"C:\Program Files\cmd.exe" /s /c "claude --append-system-prompt-file "C:\d\rules.md" "Lead it.""`, true},
		{[]string{"powershell.exe"}, "", false},
		{[]string{"claude", "/s", "/c"}, "", false},
	} {
		if got, ok := cmdLine(c.command); got != c.want || ok != c.ok {
			t.Errorf("cmdLine(%q) = %q, %v; want %q, %v", c.command, got, ok, c.want, c.ok)
		}
	}
}
