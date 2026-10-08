package machine

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// agentDirs are the folders where the agents' installers put their programs, from the home folder unless absolute:
// Homebrew, then the installers of Claude Code, npm and Bun. A shell adds them in its own startup files, which an app
// started from the Finder or a desktop menu never reads: there claude is not found.
var agentDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", ".local/bin", ".npm-global/bin", ".bun/bin"}

// AgentPath is path with the folders of agentDirs that exist and it lacks, added at its end: what is on path keeps
// its place and wins. On Windows, where installers set the PATH of the user, it is path.
func AgentPath(goos, path, home string) string {
	if goos == "windows" {
		return path
	}
	dirs := filepath.SplitList(path)
	for _, d := range agentDirs {
		if !filepath.IsAbs(d) {
			if home == "" {
				continue
			}
			d = filepath.Join(home, d)
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// ExtendPath gives Djinn's own PATH the folders of AgentPath, so that the workers, the leads' terminals and the
// gates it starts find the agents as a terminal does.
func ExtendPath() {
	home, _ := os.UserHomeDir()
	_ = os.Setenv("PATH", AgentPath(runtime.GOOS, os.Getenv("PATH"), home))
}
