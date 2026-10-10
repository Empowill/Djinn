package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// noPushScheme is the transport every push of a worker goes to: Git has none of that name, so the push fails, and
// the name says why.
const noPushScheme = "djinn-workers-do-not-push"

// noPushConfig is the Git configuration a worker's repository includes. An empty pushInsteadOf is a prefix of every
// URL, so a push to a remote, to a remote the worker added or to a URL or path given on the command line goes to
// noPushScheme instead; fetching is left alone. Unlike a pre-push hook, --no-verify does not skip it, and the
// project's own hooks keep running.
const noPushConfig = "# Written by Djinn: workers do not push, the orchestrator does.\n" +
	"[url \"" + noPushScheme + "://\"]\n\tpushInsteadOf =\n"

// gitGuardFile is where Djinn keeps noPushConfig, under its data folder home.
func gitGuardFile(home string) string {
	return filepath.Join(home, "git", "no-push.gitconfig")
}

// gitGuard returns the environment that keeps a worker in dir from pushing, whatever its command: Git's
// command-line configuration (GIT_CONFIG_COUNT, after the ones Djinn's own environment holds) includes noPushConfig
// in the repository holding dir, its worktrees all alike. Repositories elsewhere (a test's) push as before, and
// Djinn's own git calls never see it. None outside Git.
func gitGuard(home, dir string) ([]string, error) {
	dirs := agyGitDirs(dir)
	if len(dirs) == 0 {
		return nil, nil
	}
	file := gitGuardFile(home)
	if b, err := os.ReadFile(file); err != nil || string(b) != noPushConfig {
		if err := writeGitGuard(file); err != nil {
			return nil, err
		}
	}
	common := dirs[0]
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		common = resolved
	}
	// The repository's own .git, then everything under it: the linked worktrees' folders.
	pattern := globEscape(filepath.ToSlash(common))
	keys := []string{"includeIf.gitdir:" + pattern + ".path", "includeIf.gitdir:" + pattern + "/.path"}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(n+len(keys))}
	for i, key := range keys {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n+i, key), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n+i, file))
	}
	return env, nil
}

// writeGitGuard writes noPushConfig to file, whole: workers starting together each write their own and rename it.
func writeGitGuard(file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	_, err = f.WriteString(noPushConfig)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), file)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

// globEscape escapes what Git's wildmatch would read as a pattern in path.
func globEscape(path string) string {
	var b strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
