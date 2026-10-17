package plan

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/gitx"
	"github.com/empowill/djinn/internal/logx"
)

// ConfigFolders are the folders of a project that hold its configuration as Djinn reads it: its settings, its
// permissions and its skills. Djinn extracts them from the reference branch's tree.
var ConfigFolders = []string{".agents", filepath.Join(".claude", "skills")}

// Config is where Djinn reads a project's configuration on this machine: its .agents/settings.txtpb, its
// .agents/permissions.txtpb and its skills. In Git, with a remote, Djinn reads them in the reference branch's tree at
// the last fetch, extracted into its cache by digest: a branch checked out in the project's folder does not change
// them. Outside Git, without a remote, or when the reference branch is not fetched, it reads them in the project's
// folder.
type Config struct {
	// Dir is the folder that holds them, laid out as the project's folder: the project's folder, or a folder of the
	// cache.
	Dir string
	// Folder is the project's folder, where a file is fixed and the commands run.
	Folder string
	// Ref is the reference branch's ref Djinn read them from, short: origin/main; empty when Dir is Folder.
	Ref string
	// Commit is the commit Ref pointed at, in full; empty when Dir is Folder.
	Commit string
	// TeamBranch is the branch the team's file names in the default branch's tree, when Djinn took it as the
	// reference branch; empty otherwise.
	TeamBranch string
}

// FromReference tells whether Djinn reads the configuration in the reference branch's tree, not in the project's
// folder.
func (c Config) FromReference() bool { return c.Ref != "" }

// Shown is path, a file Djinn reads under c.Dir, as the developer finds it: the same file in the project's folder,
// where it is fixed.
func (c Config) Shown(path string) string {
	if !c.FromReference() {
		return path
	}
	if rel, err := filepath.Rel(c.Dir, path); err == nil && filepath.IsLocal(rel) {
		return filepath.Join(c.Folder, rel)
	}
	return path
}

// Named is path, a file Djinn reads under c.Dir, as a message names it: origin/main:.agents/settings.txtpb when Djinn
// reads the reference branch, the path itself otherwise.
func (c Config) Named(path string) string {
	if !c.FromReference() {
		return path
	}
	if rel, err := filepath.Rel(c.Dir, path); err == nil && filepath.IsLocal(rel) {
		return c.Ref + ":" + filepath.ToSlash(rel)
	}
	return path
}

// ConfigDir is the folder Djinn reads project's configuration in (ProjectConfig): empty when it has no folder here.
func ConfigDir(home string, project *planv1.Project) string {
	return ProjectConfig(home, project).Dir
}

// ConfigCache is where Djinn keeps the configuration it extracts from reference branches, in its data folder home: a
// folder per digest of the configuration folders' trees.
func ConfigCache(home string) string {
	return filepath.Join(home, "cache", "config")
}

// configSettle is how old the files a memo stamps must be for the memo to be kept: a file changed within the same tick
// of the file system's clock as the memo was made would keep its time.
const configSettle = 2 * time.Second

// configMemos keeps the Config of each project's folder, with the stamp of the files that would change it: the
// repository's configuration, its packed refs, the folders of its remote refs and the developer's settings file. Djinn
// reads a project's settings at almost every step of a pass: a Config read again from git each time would cost a few
// gits each.
var configMemos = struct {
	sync.Mutex
	m map[string]configMemo
}{m: map[string]configMemo{}}

type configMemo struct {
	stamp  string
	config Config
}

// configDigests keeps the digest of the configuration folders' trees by commit and folder: a commit's trees never
// change.
var configDigests = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// configDigestsMax bounds configDigests: past it, the memo starts again.
const configDigestsMax = 1024

// ProjectConfig is where Djinn reads project's configuration on this machine (Config), home its data folder. Without
// home, a folder, Git, a remote, or a reference branch the repository holds, Djinn reads the project's folder; also
// when the extraction fails, which it logs.
func ProjectConfig(home string, project *planv1.Project) Config {
	folder := project.GetDirectory()
	c := Config{Dir: folder, Folder: folder}
	if folder == "" || home == "" || !project.GetGit() {
		return c
	}
	common, ok := gitCommonDir(folder)
	if !ok {
		return c
	}
	devFile := DeveloperSettingsFile(home, project.GetId())
	stamp, settled := configStamp(common, devFile)
	key := home + "\x00" + folder
	configMemos.Lock()
	memo, found := configMemos.m[key]
	configMemos.Unlock()
	if found && stamp != "" && memo.stamp == stamp {
		if !memo.config.FromReference() {
			return memo.config
		}
		if _, err := os.Stat(memo.config.Dir); err == nil {
			return memo.config // unless its folder of the cache was removed since
		}
	}
	if read, err := readConfig(context.Background(), home, folder, common, devFile); err != nil {
		warnOnce(folder, "read the configuration in the project's folder", err)
	} else {
		c = read
	}
	configMemos.Lock()
	if settled && stamp != "" {
		configMemos.m[key] = configMemo{stamp: stamp, config: c}
	} else {
		delete(configMemos.m, key)
	}
	configMemos.Unlock()
	return c
}

// gitCommonDir is the folder of the repository that holds folder, shared by its worktrees: what holds its
// configuration and its refs. False when folder is in none.
func gitCommonDir(folder string) (string, bool) {
	for dir := folder; ; {
		dotGit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotGit)
		switch {
		case err == nil && info.IsDir():
			return commonOf(dotGit), true
		case err == nil:
			b, err := os.ReadFile(dotGit)
			if err != nil {
				return "", false
			}
			gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return "", false
			}
			if gitDir = strings.TrimSpace(gitDir); !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(dir, gitDir)
			}
			return commonOf(gitDir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// commonOf is the common folder of the git folder gitDir: the one its commondir file names, else itself.
func commonOf(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(b))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}

// configStamp is what the files that would change a project's Config look like now, in the repository whose common
// folder is common, devFile the developer's settings file: their times and sizes. A ref is written to a file
// renamed into its folder, which changes the folder's time. settled tells whether they are old enough for a later
// change to show; an empty stamp, when Djinn cannot read the refs as files (reftable), stamps nothing.
func configStamp(common, devFile string) (stamp string, settled bool) {
	if _, err := os.Stat(filepath.Join(common, "reftable")); err == nil {
		return "", false
	}
	var b strings.Builder
	settled = true
	limit := time.Now().Add(-configSettle)
	add := func(path string, info fs.FileInfo) {
		if info == nil {
			fmt.Fprintf(&b, "%s:-\n", path)
			return
		}
		fmt.Fprintf(&b, "%s:%d:%d\n", path, info.ModTime().UnixNano(), info.Size())
		if info.ModTime().After(limit) {
			settled = false
		}
	}
	for _, path := range []string{filepath.Join(common, "config"), filepath.Join(common, "packed-refs"), devFile} {
		info, _ := os.Stat(path)
		add(path, info)
	}
	remotes := filepath.Join(common, "refs", "remotes")
	if _, err := os.Stat(remotes); err != nil {
		add(remotes, nil)
	} else {
		_ = filepath.WalkDir(remotes, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr == nil && d.IsDir() {
				if info, err := d.Info(); err == nil {
					add(path, info)
				}
			}
			return nil // a folder that cannot be read is left out of the stamp
		})
	}
	return b.String(), settled
}

// readConfig reads, from git, where Djinn reads the configuration of the project in folder, its repository's common
// folder common, devFile the developer's settings file. The reference branch is the one devFile names, else the one
// the team's file names in the default branch's tree, else the default branch: the remote's, else main, else master.
// An error when git cannot be read or the tree cannot be extracted.
func readConfig(ctx context.Context, home, folder, common, devFile string) (Config, error) {
	c := Config{Dir: folder, Folder: folder}
	if !mayHaveRemote(common) {
		return c, nil
	}
	repo, err := readRefs(ctx, folder)
	if err != nil {
		return c, err
	}
	dev, _ := ReadSettings(devFile)
	name, team := dev.GetMainBranch(), false
	ref := repo.reference(name)
	if ref == "" {
		return c, nil // no remote: the project's folder
	}
	if name == "" {
		// The team may name another branch in the default branch's tree.
		if sha := repo.refs[ref]; sha != "" {
			dir, err := extractConfig(ctx, home, folder, sha)
			if err != nil {
				return c, err
			}
			if s, _ := ReadSettings(filepath.Join(dir, SettingsFile)); s.GetMainBranch() != "" {
				name, team = s.GetMainBranch(), true
				ref = repo.reference(name)
			}
		}
	}
	sha := repo.refs[ref]
	if sha == "" {
		return c, nil // not fetched: the project's folder, and the branch's notice says so
	}
	dir, err := extractConfig(ctx, home, folder, sha)
	if err != nil {
		return c, err
	}
	c = Config{Dir: dir, Folder: folder, Ref: shortRef(ref), Commit: sha}
	if team {
		c.TeamBranch = name
	}
	return c, nil
}

// mayHaveRemote tells whether the repository whose common folder is common may have a remote: its configuration
// names one, or includes another file. Djinn runs no git for a repository without one.
func mayHaveRemote(common string) bool {
	b, err := os.ReadFile(filepath.Join(common, "config"))
	return err != nil || bytes.Contains(b, []byte("[remote")) || bytes.Contains(b, []byte("[include"))
}

// gitRefs is what Djinn reads of a repository to find its reference branch: its remotes, the upstream of each
// branch, and the remote refs, each with the commit it points at, or the ref a symbolic one names.
type gitRefs struct {
	remotes       []string
	merge, remote map[string]string
	refs, symrefs map[string]string
}

// readRefs reads the gitRefs of the repository holding folder, in two gits.
func readRefs(ctx context.Context, folder string) (gitRefs, error) {
	r := gitRefs{merge: map[string]string{}, remote: map[string]string{}, refs: map[string]string{}, symrefs: map[string]string{}}
	// git exits 1 when no key matches: a repository with no remote and no upstream.
	out, _ := gitRawOut(ctx, folder, "config", "--null", "--get-regexp", `^(branch|remote)\.`)
	for entry := range strings.SplitSeq(string(out), "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		section, rest, ok := strings.Cut(key, ".")
		dot := strings.LastIndex(rest, ".")
		if !ok || dot < 0 {
			continue
		}
		name, variable := rest[:dot], rest[dot+1:]
		switch {
		case section == "remote" && !slices.Contains(r.remotes, name):
			r.remotes = append(r.remotes, name)
		case section == "branch" && variable == "merge":
			r.merge[name] = value
		case section == "branch" && variable == "remote":
			r.remote[name] = value
		}
	}
	if len(r.remotes) == 0 {
		return r, nil
	}
	out, err := gitRawOut(ctx, folder, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(symref)", "refs/remotes")
	if err != nil {
		return r, err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 3 {
			continue
		}
		if parts[2] != "" {
			r.symrefs[parts[0]] = parts[2]
		}
		r.refs[parts[0]] = parts[1]
	}
	return r, nil
}

// reference is the remote ref of the branch name, or of the default branch when name is empty, as the harness finds
// it (findMain): the remote is name's upstream's, else origin, else the only remote. Empty without a remote, or,
// for the default branch, when the remote has neither HEAD, main nor master.
func (r gitRefs) reference(name string) string {
	remote := r.remote[name]
	if remote == "" || remote == "." || name == "" {
		switch {
		case slices.Contains(r.remotes, "origin"):
			remote = "origin"
		case len(r.remotes) == 1:
			remote = r.remotes[0]
		default:
			return ""
		}
	}
	if name != "" {
		target := strings.TrimPrefix(r.merge[name], "refs/heads/")
		if target == "" {
			target = name
		}
		return "refs/remotes/" + remote + "/" + target
	}
	if head := r.symrefs["refs/remotes/"+remote+"/HEAD"]; head != "" {
		return head
	}
	for _, name := range []string{"main", "master"} {
		if ref := "refs/remotes/" + remote + "/" + name; r.refs[ref] != "" {
			return ref
		}
	}
	return ""
}

// extractConfig is the folder of home's cache that holds the configuration folders (ConfigFolders) of the commit
// sha, as the project in folder lays them out: extracted once by the digest of their trees, then shared.
func extractConfig(ctx context.Context, home, folder, sha string) (string, error) {
	digest, err := configDigest(ctx, folder, sha)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(ConfigCache(home), digest)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(ConfigCache(home), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(ConfigCache(home), "."+digest+"-")
	if err != nil {
		return "", err
	}
	if err := writeTrees(ctx, folder, sha, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", err
		}
		// Another read extracted it first.
	}
	return dir, nil
}

// configDigest is the digest of the configuration folders' trees in the commit sha, the folders relative to folder:
// the same configuration has the same digest whatever the commit.
func configDigest(ctx context.Context, folder, sha string) (string, error) {
	key := sha + "\x00" + folder
	configDigests.Lock()
	digest, ok := configDigests.m[key]
	configDigests.Unlock()
	if ok {
		return digest, nil
	}
	out, err := gitRawOut(ctx, folder, append([]string{"ls-tree", "-z", sha, "--"}, slashed(ConfigFolders)...)...)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(out)
	digest = hex.EncodeToString(sum[:])[:32]
	configDigests.Lock()
	if len(configDigests.m) >= configDigestsMax {
		clear(configDigests.m)
	}
	configDigests.m[key] = digest
	configDigests.Unlock()
	return digest, nil
}

// slashed are paths, with slashes: git's own separator.
func slashed(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.ToSlash(p)
	}
	return out
}

// treeFile is a file of a tree as git lists it.
type treeFile struct {
	mode, id, path string
}

// writeTrees writes into dir the files of the configuration folders of the commit sha, the folders relative to
// folder: each with its mode, executable or not, and a symbolic link as one. A submodule is left out.
func writeTrees(ctx context.Context, folder, sha, dir string) error {
	out, err := gitRawOut(ctx, folder, append([]string{"ls-tree", "-r", "-z", sha, "--"}, slashed(ConfigFolders)...)...)
	if err != nil {
		return err
	}
	var files []treeFile
	for entry := range strings.SplitSeq(string(out), "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		if !filepath.IsLocal(filepath.FromSlash(path)) {
			return fmt.Errorf("git lists %q in %s: not a path under the project", path, sha)
		}
		files = append(files, treeFile{mode: fields[0], id: fields[2], path: filepath.FromSlash(path)})
	}
	if len(files) == 0 {
		return nil
	}
	return catFiles(ctx, folder, files, func(f treeFile, content []byte) error {
		path := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		switch f.mode {
		case "120000":
			if err := os.Symlink(string(content), path); err != nil {
				warnOnce(folder, "leave a link of the configuration out", err)
			}
			return nil
		case "100755":
			return os.WriteFile(path, content, 0o700) // #nosec G306 -- an executable, in Djinn's own data folder
		}
		return os.WriteFile(path, content, 0o600)
	})
}

// catFiles reads the content of each of files in one git, and gives it to write in their order.
func catFiles(ctx context.Context, folder string, files []treeFile, write func(treeFile, []byte) error) error {
	cmd, cancel := gitx.Command(ctx, folder, "cat-file", "--batch")
	defer cancel()
	var in bytes.Buffer
	for _, f := range files {
		in.WriteString(f.id + "\n")
	}
	cmd.Stdin = &in
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReader(stdout)
	var failed error
	for _, f := range files {
		header, err := r.ReadString('\n')
		if err != nil {
			failed = fmt.Errorf("git cat-file: %w", err)
			break
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			failed = fmt.Errorf("git cat-file: %s", strings.TrimSpace(header))
			break
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			failed = fmt.Errorf("git cat-file: %s", strings.TrimSpace(header))
			break
		}
		content := make([]byte, size+1) // and the newline that ends it
		if _, err := io.ReadFull(r, content); err != nil {
			failed = fmt.Errorf("git cat-file: %w", err)
			break
		}
		if err := write(f, content[:size]); err != nil {
			failed = err
			break
		}
	}
	if failed != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return failed
	}
	if err := cmd.Wait(); err != nil {
		return errors.Join(err, errors.New(strings.TrimSpace(errOut.String())))
	}
	return nil
}

// warned are the warnings logged, by folder and text: Djinn reads a configuration at almost every step.
var warned = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

// warnOnce logs, once for folder, that Djinn could not read its configuration as it should, and what it does.
func warnOnce(folder, what string, err error) {
	key := folder + "\x00" + what + "\x00" + err.Error()
	warned.Lock()
	seen := warned.m[key]
	warned.m[key] = true
	warned.Unlock()
	if !seen {
		logx.L().Warn(what, "loop", "config", "folder", folder, "err", err)
	}
}

// gitRawOut is git with args in dir, and what it printed, untrimmed.
func gitRawOut(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd, cancel := gitx.Command(ctx, dir, args...)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.Bytes(), nil
}
