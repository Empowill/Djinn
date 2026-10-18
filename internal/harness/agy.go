package harness

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/ui"
)

// Antigravity runs Google's Antigravity CLI headless: `agy --input-format stream-json --output-format stream-json`,
// one NDJSON message per turn on its input, one event per line on its output (docs/providers.md). Like Claude, the
// process stays open between turns, and ends once every message has its result.
//
// Djinn passes its environment on and sets nothing about signing in: agy signs in by itself (decision Q20). Djinn
// never reads agy's configuration, its tokens, nor the Application Default Credentials file. It writes one thing of
// agy's: a project of its own per worktree, for a worker with permissions (agyProject).
type Antigravity struct {
	// Command is the agy program; "agy", found on the PATH, by default.
	Command string
	// Grace is how long a worker asked to stop has before it is killed; Grace by default.
	Grace time.Duration
	// IdleTimeout is how long a model call that returns nothing may wait before being cut and continued;
	// AgyIdleTimeout by default.
	IdleTimeout time.Duration
	// ToolIdleTimeout is how long agy may say nothing while a tool step or a background task of its own runs, before
	// being cut and continued all the same; AgyToolIdleTimeout by default.
	ToolIdleTimeout time.Duration
}

// AgyIdleTimeout is Antigravity's IdleTimeout by default: above agy's own idle timeout on its model's stream (5 min,
// then agy retries the call by itself 4 s later and keeps its session; agy 1.4.1, W360), so that agy's retry comes
// first, and Djinn cuts only a call that its retry did not save either.
const AgyIdleTimeout = 8 * time.Minute

// AgyToolIdleTimeout is Antigravity's ToolIdleTimeout by default. agy's stream-json says nothing while a command runs,
// in the foreground or in the background: a background command is a tool step that stays ACTIVE until the command
// ends, and agy holds back the steps that follow it until then, the model's own included (agy 1.3.3 and 1.4.1, W16
// and W308). Past this cap, a silence is taken as a model call that returns nothing: a worker runs commands under 9
// minutes each (workerWaitRule), and a gate wait seldom lasts longer.
const AgyToolIdleTimeout = 30 * time.Minute

// args are the arguments of agy for spec. The prompt goes on the input, not on the command line: -p takes the
// prompt as its value, and stream-json input replaces it.
//
// In a project, agy's own settings decide; headless, agy cannot ask for a permission, so a tool that needs one
// is refused (soft-denied). Djinn never passes --dangerously-skip-permissions.
//
// A read-only task is refused: agy's plan mode asks before writing but is not read-only, and what --sandbox blocks is
// not documented. agy runs outside a project once a real capture proves that nothing is written or run. For the
// same reason, permissions without edit are refused.
//
// With permissions, edit becomes --mode accept-edits, and network off --sandbox, which only restricts more. The rest
// goes in an agy project of Djinn's own, given with --project (agyProject): agy takes no list of commands nor of
// writable folders at launch. agy 1.3.3 has no auto mode on its command line (--mode takes accept-edits or plan):
// AUTO runs as accept-edits, its project allowing every command but the denied ones.
func (a Antigravity) args(spec Spec) ([]string, error) {
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json"}
	if spec.ReadOnly {
		return nil, fmt.Errorf("agy cannot run a read-only task yet: %w; use claude or codex", ErrReadOnly)
	}
	if p := spec.Permissions; p != nil {
		if !p.GetEdit() {
			return nil, fmt.Errorf("agy cannot run without editing yet (the project's %s says edit: false): %w; use claude or codex",
				filepath.ToSlash(PermissionsFile), ErrReadOnly)
		}
		// No --sandbox, the developer's choice (Q66, 10/10/2026): agy's sandbox cannot reach Djinn's socket, so a
		// worker's `djinn gate run lint` asked for the "unsandboxed" permission, which a headless run cannot grant
		// (W228, agy 1.3.3), and some tasks need the network. The grants of its project still hold: writes to the
		// worktree and its Git folders only, the project's denied commands and git push refused.
		args = append(args, "--mode", "accept-edits")
		args = append(args, "--project", agyProjectID(spec.Dir))
	}
	if spec.SkillsDir != "" {
		// A workspace folder's .agents/skills is a customization root (the documentation in the agy 1.3.0 binary).
		args = append(args, "--add-dir", spec.SkillsDir)
	}
	if spec.Resume != "" {
		if spec.Fork {
			return nil, errors.New("agy cannot fork a conversation: resume it, or start a new one")
		}
		args = append(args, "--conversation", spec.Resume)
	}
	if spec.Model != "" && !foreignModel(planv1.Provider_PROVIDER_ANTIGRAVITY, spec.Model) {
		args = append(args, "--model", agyModel(spec.Model))
	}
	// agy has no spending cap: MaxBudgetUSD is not enforced.
	return args, nil
}

// agyModel maps short aliases or legacy labels for Antigravity's Argon model ("gemini-4-argon", "argon",
// "Gemini 4 Argon (High)") to the canonical model ID agy expects on --model: "gemini-4-argon-high".
func agyModel(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gemini 4 argon (high)", "gemini-4-argon-high", "gemini-4-argon", "argon":
		return "gemini-4-argon-high"
	default:
		return model
	}
}

func (a Antigravity) Start(ctx context.Context, spec Spec) (Worker, error) {
	command, grace := a.Command, a.Grace
	if command == "" {
		command = "agy"
	}
	if grace == 0 {
		grace = Grace
	}
	args, err := a.args(spec)
	if err != nil {
		return nil, err
	}
	if spec.Permissions != nil {
		if err := writeAgyProject(spec); err != nil {
			return nil, err
		}
	}
	isWorkDone := spec.IsWorkDone
	if isWorkDone == nil && spec.Dir != "" && spec.StartHead != "" {
		isWorkDone = func() bool {
			return agyWorkDone(ctx, spec.Dir, spec.StartHead, spec.Branch)
		}
	}
	idle, toolIdle := cmp.Or(a.IdleTimeout, AgyIdleTimeout), cmp.Or(a.ToolIdleTimeout, AgyToolIdleTimeout)
	parser := &agyParser{isWorkDone: isWorkDone, toolIdle: toolIdle}
	return startStream(ctx, spec, command, args, grace, streamAgent{name: "agy", parser: parser, idle: idle, encode: agyMessageLine})
}

// agyMessageLine is a user message as agy reads it in stream-json.
func agyMessageLine(text string) ([]byte, error) {
	return json.Marshal(map[string]any{"event": "user", "message": map[string]any{"content": text}})
}

// agyProjectsDir is the folder of agy's projects, ~/.gemini/config/projects; a test points it elsewhere.
var agyProjectsDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "config", "projects"), nil
}

// agyProjectID is the id of the agy project of a worker in dir: the same for every worker of the folder, as agy
// resumes a conversation in the project it began in.
func agyProjectID(dir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return "djinn-" + hex.EncodeToString(sum[:8])
}

// agyProjectFile is an agy project, as agy reads it in its projects folder (the descriptor of its project_pb, agy
// 1.3.3). Its other fields, settings among them, are left out: the command line does not apply a project's settings.
type agyProjectFile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	PermissionGrants struct {
		PermissionGrants agyGrants `json:"permissionGrants"`
	} `json:"permissionGrants"`
}

type agyGrants struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

// agyEveryCommand is the grant of every command: in AUTO, agy runs any command but the denied ones, as Claude does,
// agy having no auto mode of its own.
const agyEveryCommand = "command(*)"

// agyEveryURL grants fetching any URL with read_url in AUTO (Q143).
const agyEveryURL = "read_url(*)"

// agyAlwaysDenied are denied to an agy worker in AUTO whatever the project denies: pushing is the orchestrator's.
var agyAlwaysDenied = []string{"git push"}

// appendUnique appends items to slice only if they are non-empty and not already present.
func appendUnique(slice []string, items ...string) []string {
	for _, item := range items {
		if item != "" && !slices.Contains(slice, item) {
			slice = append(slice, item)
		}
	}
	return slice
}

var (
	agyCachesOnce sync.Once
	cachedGoRoot  string
	cachedMod     string
	cachedBuild   string
	cachedNpm     string
)

func agyCaches() (goRoot, modCache, buildCache, npmCache string) {
	agyCachesOnce.Do(func() {
		home, _ := os.UserHomeDir()
		// A go that would download its toolchain, or an npm that hangs, delays a worker's start no longer (W504).
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "go", "env", "GOROOT", "GOMODCACHE", "GOCACHE").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 3 {
				cachedGoRoot = strings.TrimSpace(lines[0])
				cachedMod = strings.TrimSpace(lines[1])
				cachedBuild = strings.TrimSpace(lines[2])
			}
		}
		if cachedGoRoot == "" {
			cachedGoRoot = os.Getenv("GOROOT")
		}
		if cachedMod == "" && home != "" {
			cachedMod = filepath.Join(home, "go", "pkg", "mod")
		}
		if cachedBuild == "" && home != "" {
			cachedBuild = filepath.Join(home, ".cache", "go-build")
		}
		if out, err := exec.CommandContext(ctx, "npm", "config", "get", "cache").Output(); err == nil {
			cachedNpm = strings.TrimSpace(string(out))
		}
		if cachedNpm == "" && home != "" {
			cachedNpm = filepath.Join(home, ".npm")
		}
	})
	return cachedGoRoot, cachedMod, cachedBuild, cachedNpm
}

// djinnDataDir is ui.Home by default; tests can override or set DJINN_HOME.
var djinnDataDir = ui.Home

// djinnDenials finds paths inside dir to deny when some keepPaths must remain accessible.
// Sibling directories and files that do not contain any keepPath are denied with read_file(<path>).
func djinnDenials(dir string, keepPaths []string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var denials []string
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		matched := false
		for _, k := range keepPaths {
			if k == entryPath {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		isAncestor := false
		for _, k := range keepPaths {
			if strings.HasPrefix(k, entryPath+string(filepath.Separator)) {
				isAncestor = true
				break
			}
		}
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(entryPath); err == nil && fi.IsDir() {
				isDir = true
			}
		}
		if isAncestor && isDir {
			denials = append(denials, djinnDenials(entryPath, keepPaths)...)
		} else {
			denials = append(denials, "read_file("+entryPath+")")
		}
	}
	slices.Sort(denials)
	return denials
}

// agyProject is the agy project of a worker with permissions: what its permissions give, as agy's grants (real
// runs, agy 1.3.3, 2026-10-10, docs/providers.md).
//
// agy's sandbox (--sandbox) shows the workspace read-only to commands, and every .git folder read-only, whatever
// the workspace's trust or --add-dir; it hides the rest of the home folder, the repository of a linked worktree
// among them, so that git fails and agy asks to run it unsandboxed, which a headless run cannot. A write_file grant
// mounts its folder writable in the sandbox: edit grants the worker's folder and Git's folders for it (agyGitDirs).
// A command(<p>) grant lets a command starting with p run without approval, command(*) any command, in the sandbox
// still; a deny grant wins over any allow one, the user's settings' and command(*) included, in every part of a
// compound command (`a && git push`) and after an env prefix, and its command fails without ending the turn.
// In AUTO, command(*) grants every command and read_url(*) grants fetching any URL without approval (Q143).
//
// For reading, agy headless requires explicit read_file grants (W254, W269). Djinn grants read_file on what a
// worker needs to read: the repository (main checkout and .git), Go's root, module and build caches, npm's cache,
// /usr and /etc read-only, and the user's home with denials for secrets and Djinn's own data folder except the
// task's worktree and the folders it reads there (Spec.ReadDirs: the versions of the tilasms it is given, W360), as a
// denial wins over any grant.
func agyProject(spec Spec) agyProjectFile {
	p := spec.Permissions
	f := agyProjectFile{ID: agyProjectID(spec.Dir), Name: "Djinn " + filepath.Base(spec.Dir)}
	g := agyGrants{Allow: []string{}}
	gitDirs, repoCheckout := agyGitInfo(spec.Dir)

	cleanDir := filepath.Clean(spec.Dir)
	var keepPaths []string
	addKeep := func(path string) {
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if !slices.Contains(keepPaths, clean) {
			keepPaths = append(keepPaths, clean)
		}
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
			cleanResolved := filepath.Clean(resolved)
			if !slices.Contains(keepPaths, cleanResolved) {
				keepPaths = append(keepPaths, cleanResolved)
			}
		}
	}
	addKeep(cleanDir)
	addKeep(repoCheckout)
	for _, d := range gitDirs {
		addKeep(d)
	}
	if spec.SkillsDir != "" {
		addKeep(spec.SkillsDir)
	}
	for _, d := range spec.ReadDirs {
		addKeep(d)
	}

	if p.GetEdit() {
		for _, d := range append([]string{cleanDir}, gitDirs...) {
			g.Allow = appendUnique(g.Allow, "write_file("+d+")")
		}
	}

	// Reads: repository (main checkout and .git), Go root, module and build caches, npm cache, /usr, /etc, and user's home.
	g.Allow = appendUnique(g.Allow, "read_file("+cleanDir+")")
	if repoCheckout != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+repoCheckout+")")
	}
	for _, d := range gitDirs {
		g.Allow = appendUnique(g.Allow, "read_file("+d+")")
	}
	if spec.SkillsDir != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(spec.SkillsDir)+")")
	}
	for _, d := range spec.ReadDirs {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(d)+")")
	}
	goRoot, modCache, buildCache, npmCache := agyCaches()
	if goRoot != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(goRoot)+")")
	}
	if modCache != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(modCache)+")")
	}
	if buildCache != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(buildCache)+")")
	}
	if npmCache != "" {
		g.Allow = appendUnique(g.Allow, "read_file("+filepath.Clean(npmCache)+")")
	}
	if runtime.GOOS != "windows" {
		g.Allow = appendUnique(g.Allow, "read_file(/usr)", "read_file(/etc)")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		cleanHome := filepath.Clean(home)
		g.Allow = appendUnique(g.Allow, "read_file("+cleanHome+")")
	}

	denied := slices.Clone(p.GetDeniedCommands())
	if effectiveMode(p) == djinnv1.Mode_MODE_AUTO {
		g.Allow = appendUnique(g.Allow, agyEveryCommand, agyEveryURL)
		for _, c := range agyAlwaysDenied {
			if !slices.Contains(denied, c) {
				denied = append(denied, c)
			}
		}
	} else {
		if p.GetNetwork() {
			g.Allow = appendUnique(g.Allow, agyEveryURL)
		}
		for _, c := range p.GetCommands() {
			g.Allow = appendUnique(g.Allow, "command("+c+")")
		}
	}

	for _, c := range denied {
		g.Deny = appendUnique(g.Deny, "command("+c+")")
	}

	// Denials for secrets in user's home.
	if home != "" {
		cleanHome := filepath.Clean(home)
		secretNames := []string{
			".ssh",
			".gnupg",
			filepath.Join(".config", "gh"),
			filepath.Join(".config", "gcloud"),
			".aws",
			".netrc",
			".git-credentials",
		}
		for _, s := range secretNames {
			p := filepath.Join(cleanHome, s)
			keepSecret := false
			for _, k := range keepPaths {
				if k == p || strings.HasPrefix(k, p+string(filepath.Separator)) {
					keepSecret = true
					break
				}
			}
			if !keepSecret {
				g.Deny = appendUnique(g.Deny, "read_file("+p+")")
			}
		}
	}

	// Denials for Djinn's own data folder except the task's worktree.
	if djinnDir, err := djinnDataDir(); err == nil && djinnDir != "" {
		cleanDjinn := filepath.Clean(djinnDir)
		anyInside := false
		for _, k := range keepPaths {
			rel, err := filepath.Rel(cleanDjinn, k)
			if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
				anyInside = true
				break
			}
		}
		if anyInside {
			for _, d := range djinnDenials(cleanDjinn, keepPaths) {
				g.Deny = appendUnique(g.Deny, d)
			}
		} else {
			g.Deny = appendUnique(g.Deny, "read_file("+cleanDjinn+")")
		}
	}

	f.PermissionGrants.PermissionGrants = g
	return f
}

// agyGitInfo returns Git's folders for the work tree holding dir (same as agyGitDirs),
// and the repository's main checkout folder when one is found.
func agyGitInfo(dir string) (gitDirs []string, repoCheckout string) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		info, err := os.Stat(dotGit)
		switch {
		case err == nil && info.IsDir():
			return []string{dotGit}, d
		case err == nil:
			b, err := os.ReadFile(dotGit)
			gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if err != nil || !ok {
				return nil, ""
			}
			gitDir = absFrom(d, strings.TrimSpace(gitDir))
			repoDir := ""
			b, err = os.ReadFile(filepath.Join(gitDir, "commondir"))
			if err != nil {
				if filepath.Base(gitDir) == ".git" {
					repoDir = filepath.Dir(gitDir)
				}
				return []string{gitDir}, repoDir
			}
			common := absFrom(gitDir, strings.TrimSpace(string(b)))
			if filepath.Base(common) == ".git" {
				repoDir = filepath.Dir(common)
			} else {
				repoDir = common
			}
			if rel, err := filepath.Rel(common, gitDir); err == nil && filepath.IsLocal(rel) {
				return []string{common}, repoDir
			}
			return []string{common, gitDir}, repoDir
		}
		if filepath.Dir(d) == d {
			return nil, ""
		}
	}
}

// agyGitDirs are Git's folders for the work tree holding dir: its .git folder; in a linked worktree, the folder
// its .git file names and the repository's common folder (the commondir file there), when not inside it. None
// outside Git.
func agyGitDirs(dir string) []string {
	dirs, _ := agyGitInfo(dir)
	return dirs
}

// absFrom is path, absolute, relative to dir when it is not.
func absFrom(dir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

// writeAgyProject writes the agy project of spec's worker in agy's projects folder, over the one an earlier worker
// of its folder wrote. Djinn writes nothing else of agy's, and reads nothing of it.
func writeAgyProject(spec Spec) error {
	dir, err := agyProjectsDir()
	if err != nil {
		return fmt.Errorf("agy's projects folder: %w", err)
	}
	f := agyProject(spec)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agy's projects folder: %w", err)
	}
	path := filepath.Join(dir, f.ID+".json")
	if err := fsx.WriteAtomic(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write agy's project: %w", err)
	}
	return nil
}

// forgetAgyProject removes the agy project of the workers in dir, when there is one: its folder is gone.
func forgetAgyProject(dir string) {
	if projects, err := agyProjectsDir(); err == nil {
		_ = os.Remove(filepath.Join(projects, agyProjectID(dir)+".json"))
	}
}

// agyLine is a line of agy's stream-json output; only the fields Djinn reads.
type agyLine struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Model string `json:"model"`
	} `json:"init"`
	StepUpdate *agyStep        `json:"step_update"`
	Result     *agyResult      `json:"result"`
	Error      json.RawMessage `json:"error"`
	Message    json.RawMessage `json:"message"`
}

type agyStep struct {
	ConversationID string `json:"conversation_id"`
	StepIndex      int    `json:"step_index"`
	State          string `json:"state"` // ACTIVE, DONE, ERROR
	StepType       string `json:"step_type"`
	ToolName       string `json:"tool_name"`
	TextDelta      string `json:"text_delta"`
	ToolInfo       *struct {
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
		Output     *string         `json:"output"`
		// Error is why a tool step ended in ERROR: a command refused by a deny grant, for one (real runs, agy 1.3.3).
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"tool_info"`
	Error   json.RawMessage `json:"error"`
	Message json.RawMessage `json:"message"`
	// Content is the text of a step, a system message's in agy's transcript; the stream has carried none so far.
	Content json.RawMessage `json:"content"`
}

type agyResult struct {
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	Error          string    `json:"error"`
	Response       string    `json:"response"`
	NumTurns       int       `json:"num_turns"`
	Usage          *agyUsage `json:"usage"`
	// DeniedActions are the permissions agy refused in the turn, headless mode unable to ask for them:
	// [{"action":"command","display_name":"RunCommand"}] (real runs, agy 1.3.0).
	DeniedActions []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

type agyUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
}

// agyContinuePrompt is what Djinn says when agy ended its turn on one of its own model retry prompts.
const agyContinuePrompt = "continue where you stopped, keep answers and edits short"

// agyIdlePause is what agy prints when execution is paused waiting for tasks to complete.
const agyIdlePause = "There are no tools called. Execution paused. Waiting for task completion."

// agyBgNotice is agy's own notice of a command sent to the background, in the output of its tool step: "Tool is
// running as a background task with task id: <conversation>/task-19" (agy 1.4.1, its transcript). Nothing else names
// a background task: a branch, a file or a text that holds "task-" is no task (W360). The stream has not carried the
// notice so far (W16, W308): a background command stays an ACTIVE tool step there until it ends.
var agyBgNotice = regexp.MustCompile(`Tool is running as a background task with task id: (\S+/task-\d+)`)

// agyBgTask is a background task agy said it started, and the tool step that started it.
type agyBgTask struct {
	id   string
	step int
}

// agyBgStopped is in the system message agy writes when it stopped every background task: "[Notice] All your
// subagents and background tasks have been stopped due to server restart." (agy 1.4.1, its transcript).
const agyBgStopped = "background tasks have been stopped"

const (
	agyMaxRetryPromptContinuations = 3
	agyMaxTransientContinuations   = 4
)

var agyTransientBackoff = 500 * time.Millisecond

func transientBackoff(attempt int) time.Duration {
	if agyTransientBackoff <= 0 {
		return 0
	}
	factor := 1 << (attempt - 1)
	if factor < 1 {
		factor = 1
	}
	return agyTransientBackoff * time.Duration(factor)
}

// extractAgyError extracts an error message string from agy error JSON payloads, handling strings or nested objects.
func extractAgyError(raw json.RawMessage, fallback string) string {
	if len(raw) > 0 {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
		var obj struct {
			Message json.RawMessage `json:"message"`
			Error   json.RawMessage `json:"error"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			if str := extractAgyError(obj.Message, ""); str != "" {
				return str
			}
			if str := extractAgyError(obj.Error, ""); str != "" {
				return str
			}
			if str := extractAgyError(obj.Content, ""); str != "" {
				return str
			}
		}
	}
	return fallback
}

var re5xx = regexp.MustCompile(`\b5\d{2}\b`)

// isAgyTransientNetworkError reports whether text is a transient network error from agy or its API.
func isAgyTransientNetworkError(text string) bool {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "stream was interrupted") || strings.Contains(lower, "stream interrupted") ||
		strings.Contains(lower, "stream error") || strings.Contains(lower, "stream closed"):
		return true
	case strings.Contains(lower, "connection reset"):
		return true
	case strings.Contains(lower, "broken pipe"):
		return true
	case strings.Contains(lower, "connection refused"):
		return true
	case strings.Contains(lower, "unexpected eof"):
		return true
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return true
	case strings.Contains(lower, "5xx") || re5xx.MatchString(lower):
		return true
	default:
		return false
	}
}

// isAgyRetryPrompt says whether text is one of agy's own retry prompts to its model (malformed
// function call, output token limit, or transient model retry error) rather than the agent's final answer.
func isAgyRetryPrompt(text string) bool {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "improperly formatted function call"):
		return true
	case strings.Contains(lower, "malformed function call"):
		return true
	case strings.Contains(lower, "properly formatted function call"):
		return true
	case strings.Contains(lower, "output token limit"):
		return true
	case strings.Contains(lower, "retries remaining"):
		return true
	case strings.Contains(lower, "model error") && strings.Contains(lower, "retry"):
		return true
	default:
		return false
	}
}

// agyWorkDone reports whether the worktree at dir is clean and branch has commits since startHead.
func agyWorkDone(ctx context.Context, dir, startHead, branch string) bool {
	if dir == "" || startHead == "" {
		return false
	}
	uncomm, err := uncommittedDir(ctx, dir)
	if err != nil || len(uncomm) > 0 {
		return false
	}
	curHead, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil || curHead == "" {
		return false
	}
	curHead = strings.TrimSpace(curHead)
	if curHead == startHead {
		return false
	}
	if _, err := git(ctx, dir, "merge-base", "--is-ancestor", startHead, curHead); err != nil {
		return false
	}
	countStr, err := git(ctx, dir, "rev-list", "--count", startHead+".."+curHead)
	if err != nil {
		return false
	}
	count, err := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil || count <= 0 {
		return false
	}
	if branch != "" {
		if branchHead, err := git(ctx, dir, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
			if strings.TrimSpace(branchHead) != curHead {
				return false
			}
		}
	}
	return true
}

// agyParser reads agy's stream-json. The agent's text comes in deltas: it is gathered per step, and said once
// the step is done.
type agyParser struct {
	text        map[int]*strings.Builder // text of the agent_response steps under way, by step index
	order       []int                    // their indexes, in the order they started
	toolsIn     map[int]bool             // tool steps whose call is said already
	retries     int                      // retry prompt continuations sent by Djinn
	bgTasks     []agyBgTask              // background tasks agy said it started (agyBgNotice), the oldest first
	idleRetries int                      // idle pause continuations sent by Djinn
	toolIdle    time.Duration            // how long agy may say nothing while a tool or a background task runs

	sessionID   string
	turnEnded   bool
	turnRetried bool

	// The last tool step started, and whether it gave an output: a denial ends the turn, and the denied step
	// ends without output, or does not end (real runs, agy 1.3.0).
	lastStep     int
	lastCall     string
	lastAnswered bool
	denied       string // the permission named by the denial notice on the error output, when one came
	failed       bool   // a result said the turn failed: the notice adds nothing

	answered      bool        // the agent completed an agent_response step with non-empty text, and no tool started since
	closingFailed bool        // closing call failed after work was done; suppress error failure
	isWorkDone    func() bool // reports whether clean work has been committed since task start
}

func (p *agyParser) toolRunning() bool {
	return p != nil && (len(p.toolsIn) > 0 || len(p.bgTasks) > 0)
}

// idleCap is how long agy may say nothing while a tool step or a background task runs: the stream idle timer stays
// armed, so that a model call that returns nothing is always cut (AgyToolIdleTimeout).
func (p *agyParser) idleCap() time.Duration {
	return p.toolIdle
}

func (p *agyParser) handleIdlePause(events *lineEvents) *turnEnd {
	if p.turnEnded {
		return nil
	}
	*events = append(*events, p.cut()...)
	if p.isWorkDone != nil && p.isWorkDone() {
		p.turnEnded = true
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "idle pause with work committed: done")
		return &turnEnd{}
	}
	if p.idleRetries < 1 {
		p.idleRetries++
		p.turnEnded = true
		p.turnRetried = true
		p.answered = false
		events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "continued: "+agyContinuePrompt)
		return &turnEnd{retry: agyContinuePrompt}
	}
	p.turnEnded = true
	p.failed = true
	errText := "idle loop: " + agyIdlePause
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, errText)
	return &turnEnd{failure: errText}
}

// bgStep is the index in bgTasks of the background task the tool step started; -1 for none.
func (p *agyParser) bgStep(step int) int {
	return slices.IndexFunc(p.bgTasks, func(t agyBgTask) bool { return t.step == step })
}

// endBg forgets the background tasks that end tells ended.
func (p *agyParser) endBg(ended func(t agyBgTask) bool) {
	p.bgTasks = slices.DeleteFunc(p.bgTasks, ended)
}

// manageTask reads a manage_task step: a task killed, or whose status is no longer RUNNING, has ended.
func (p *agyParser) manageTask(params json.RawMessage, output string) {
	var mp struct {
		Action string `json:"Action"`
		TaskID string `json:"TaskId"`
	}
	if json.Unmarshal(params, &mp) != nil || mp.TaskID == "" {
		return
	}
	status := ""
	for l := range strings.Lines(output) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "Status:"); ok {
			status = strings.TrimSpace(v)
		}
	}
	if mp.Action == "kill" || status != "" && status != "RUNNING" {
		p.endBg(func(t agyBgTask) bool { return t.id == mp.TaskID })
	}
}

// systemMessage reads a system_message step: agy writes one as each background task ends, and as it stops them all.
// In the stream it carries no text (agy 1.3.3 and 1.4.1, W16 and W308): it ends the oldest background task. A text,
// when one comes, names the tasks that ended.
func (p *agyParser) systemMessage(text string) {
	switch {
	case len(p.bgTasks) == 0:
	case strings.TrimSpace(text) == "":
		p.bgTasks = p.bgTasks[1:]
	case strings.Contains(text, agyBgStopped):
		p.bgTasks = nil
	default:
		p.endBg(func(t agyBgTask) bool { return strings.Contains(text, t.id) })
	}
}

func (p *agyParser) canFinishNow() bool {
	return p.answered && len(p.toolsIn) == 0 && p.isWorkDone != nil && p.isWorkDone()
}

func (p *agyParser) finishClosingError(events *lineEvents, why string) *turnEnd {
	p.turnEnded = true
	p.closingFailed = true
	*events = append(*events, p.cut()...)
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, "closing call failed: "+why)
	return &turnEnd{}
}

func (p *agyParser) retryOrEnd(events *lineEvents, convID, retryText string, isTransient bool) *turnEnd {
	if p.turnEnded {
		return nil
	}
	p.turnEnded = true
	p.answered = false
	maxRetries := agyMaxRetryPromptContinuations
	if isTransient {
		maxRetries = agyMaxTransientContinuations
	}
	if p.retries < maxRetries {
		p.retries++
		p.turnRetried = true
		statusText := fmt.Sprintf("continued: %s (attempt %d of %d)", agyContinuePrompt, p.retries, maxRetries)
		ev := events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, statusText)
		ev.SessionID = convID
		var backoff time.Duration
		if isTransient {
			backoff = transientBackoff(p.retries)
		}
		return &turnEnd{retry: agyContinuePrompt, backoff: backoff}
	}
	p.failed = true
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, retryText)
	return &turnEnd{failure: retryText}
}

func (p *agyParser) errorEvent(events *lineEvents, m *agyLine) *turnEnd {
	if p.closingFailed {
		return nil
	}
	convID := m.ConversationID
	if convID == "" {
		convID = p.sessionID
	}
	errText := extractAgyError(m.Error, extractAgyError(m.Message, ""))
	if errText == "" {
		errText = "agy error"
	}
	if strings.Contains(errText, agyIdlePause) && len(p.bgTasks) == 0 {
		return p.handleIdlePause(events)
	}
	if p.canFinishNow() {
		return p.finishClosingError(events, errText)
	}
	if isAgyRetryPrompt(errText) {
		*events = append(*events, p.cut()...)
		return p.retryOrEnd(events, convID, errText, false)
	}
	if isAgyTransientNetworkError(errText) {
		*events = append(*events, p.cut()...)
		return p.retryOrEnd(events, convID, errText, true)
	}
	if p.turnEnded {
		return nil
	}
	p.turnEnded = true
	p.failed = true
	*events = append(*events, p.cut()...)
	events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, errText)
	return &turnEnd{failure: errText}
}

func (p *agyParser) stdout(raw string) ([]Event, *turnEnd) {
	var m agyLine
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		if strings.Contains(raw, agyIdlePause) && len(p.bgTasks) == 0 && !p.turnEnded {
			var events lineEvents
			end := p.handleIdlePause(&events)
			return events.done(raw, "idle_pause"), end
		}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, Text: raw, Raw: raw}}, nil
	}
	var events lineEvents
	var end *turnEnd
	what, silent := m.Event, false
	switch {
	case m.Event == "init":
		p.sessionID = m.ConversationID
		text := "session " + m.ConversationID
		var model string
		if m.Init != nil && m.Init.Model != "" {
			text += ", model " + m.Init.Model
			model = m.Init.Model
		}
		ev := events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, text)
		ev.SessionID = m.ConversationID
		ev.Model = model
	case m.Event == "step_update" && m.StepUpdate != nil:
		s := m.StepUpdate
		what = "step " + s.StepType + " " + s.State
		silent, end = p.step(&events, s)
	case m.Event == "error":
		end = p.errorEvent(&events, &m)
	case m.Event == "result" && m.Result != nil:
		end = p.result(&events, m.Result)
	}
	if len(events) == 0 && silent {
		return nil, end // A piece of a whole said later, or the echo of Djinn's prompt.
	}
	return events.done(raw, what), end
}

// step reads a step update, and says whether Djinn knows its kind: a known step may say nothing yet.
func (p *agyParser) step(events *lineEvents, s *agyStep) (bool, *turnEnd) {
	if p.closingFailed {
		return false, nil
	}
	if s.ConversationID != "" {
		p.sessionID = s.ConversationID
	}
	if p.turnRetried && (s.StepType == "user_input" || s.StepType == "agent_response" || s.StepType == "tool") {
		p.turnEnded = false
		p.turnRetried = false
	}
	if s.StepType == "user_input" {
		p.turnEnded = false
		p.answered = false
	}
	stepErr := extractAgyError(s.Error, extractAgyError(s.Message, ""))
	if s.ToolInfo != nil && s.ToolInfo.Error != nil && stepErr == "" {
		stepErr = s.ToolInfo.Error.Message
	}
	if stepErr != "" {
		convID := s.ConversationID
		if convID == "" {
			convID = p.sessionID
		}
		if p.canFinishNow() {
			delete(p.toolsIn, s.StepIndex)
			p.forget(s.StepIndex)
			return true, p.finishClosingError(events, stepErr)
		}
		if isAgyRetryPrompt(stepErr) {
			delete(p.toolsIn, s.StepIndex)
			p.forget(s.StepIndex)
			*events = append(*events, p.cut()...)
			return true, p.retryOrEnd(events, convID, stepErr, false)
		}
		if s.StepType != "tool" && isAgyTransientNetworkError(stepErr) {
			delete(p.toolsIn, s.StepIndex)
			p.forget(s.StepIndex)
			*events = append(*events, p.cut()...)
			return true, p.retryOrEnd(events, convID, stepErr, true)
		}
	}
	outputStr := ""
	if s.ToolInfo != nil && s.ToolInfo.Output != nil {
		outputStr = *s.ToolInfo.Output
	}
	if (strings.Contains(s.TextDelta, agyIdlePause) ||
		strings.Contains(outputStr, agyIdlePause) ||
		strings.Contains(stepErr, agyIdlePause)) && len(p.bgTasks) == 0 {
		return true, p.handleIdlePause(events)
	}
	switch s.StepType {
	case "system_message":
		if s.State == "DONE" {
			p.systemMessage(extractAgyError(s.Content, s.TextDelta))
		}
	case "user_input", "checkpoint", "system":
		// The prompt Djinn sent, and agy's own bookkeeping: nothing to say.
	case "agent_response":
		b := p.text[s.StepIndex]
		if b == nil {
			if p.text == nil {
				p.text = map[int]*strings.Builder{}
			}
			b = &strings.Builder{}
			p.text[s.StepIndex] = b
			p.order = append(p.order, s.StepIndex)
		}
		b.WriteString(s.TextDelta)
		if s.State == "DONE" {
			text := b.String()
			if strings.TrimSpace(text) != "" {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, strings.TrimRight(text, "\n"))
				p.answered = true
			}
			p.forget(s.StepIndex)
			if strings.Contains(text, agyIdlePause) && len(p.bgTasks) == 0 {
				return true, p.handleIdlePause(events)
			}
		}
	case "tool":
		p.answered = false
		name, params, output, failure := s.ToolName, json.RawMessage(nil), (*string)(nil), ""
		if ti := s.ToolInfo; ti != nil {
			if ti.Name != "" {
				name = ti.Name
			}
			params, output = ti.Parameters, ti.Output
			if ti.Error != nil {
				failure = ti.Error.Message
			}
		}
		bg := p.bgStep(s.StepIndex)
		if !p.toolsIn[s.StepIndex] && bg < 0 {
			if p.toolsIn == nil {
				p.toolsIn = map[int]bool{}
			}
			p.toolsIn[s.StepIndex] = true
			p.lastStep, p.lastCall, p.lastAnswered = s.StepIndex, agyCall(name, params), false
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, strings.TrimSpace(name+" "+compactOrEmpty(params)))
		}
		if m := agyBgNotice.FindStringSubmatch(outputStr); m != nil {
			// The command goes on in the background, and the turn without it, until a system message tells its end.
			if bg < 0 {
				p.bgTasks = append(p.bgTasks, agyBgTask{id: m[1], step: s.StepIndex})
				p.lastAnswered = p.lastAnswered || s.StepIndex == p.lastStep
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, strings.TrimRight(outputStr, "\r\n"))
			}
			delete(p.toolsIn, s.StepIndex)
			break
		}
		if s.State == "DONE" || s.State == "ERROR" {
			if bg >= 0 { // The background command ended: its step says how.
				p.endBg(func(t agyBgTask) bool { return t.step == s.StepIndex })
			}
		}
		if s.State == "DONE" {
			text := ""
			if output != nil {
				text = strings.TrimRight(*output, "\r\n")
				p.lastAnswered = p.lastAnswered || s.StepIndex == p.lastStep
			}
			if name == "manage_task" {
				p.manageTask(params, text)
			}
			events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, text)
			delete(p.toolsIn, s.StepIndex)
		}
		if s.State == "ERROR" {
			// The step failed and the turn goes on: a deny grant refused the command, or the tool failed.
			if s.StepIndex == p.lastStep {
				p.lastAnswered = true
			}
			if m := agyDeniedPath.FindStringSubmatch(failure); m != nil && strings.Contains(failure, agyDenyRule) {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, permissionDenied+m[1]+" "+m[2])
			} else if strings.Contains(failure, agyDenyRule) {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, permissionDenied+agyCall(name, params))
			} else {
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, strings.TrimSpace(failure))
			}
			delete(p.toolsIn, s.StepIndex)
		}
	default:
		return false, nil
	}
	return true, nil
}

func (p *agyParser) result(events *lineEvents, r *agyResult) *turnEnd {
	if p.closingFailed {
		return nil
	}
	convID := r.ConversationID
	if convID == "" {
		convID = p.sessionID
	}
	*events = append(*events, p.cut()...)
	retryText := ""
	if isAgyRetryPrompt(r.Error) {
		retryText = r.Error
	} else if isAgyRetryPrompt(r.Response) {
		retryText = r.Response
	}
	var end *turnEnd
	if !p.turnEnded {
		errText := r.Error
		if errText == "" {
			errText = retryText
		}
		switch {
		case p.canFinishNow() && (r.Status != "SUCCESS" || errText != "" || isAgyTransientNetworkError(errText)):
			if errText == "" {
				errText = "agy ended its turn with status " + r.Status
			}
			end = p.finishClosingError(events, errText)
		case retryText != "":
			end = p.retryOrEnd(events, convID, retryText, false)
		case (strings.Contains(r.Response, agyIdlePause) || strings.Contains(r.Error, agyIdlePause)) && len(p.bgTasks) == 0:
			end = p.handleIdlePause(events)
		case isAgyTransientNetworkError(r.Error):
			end = p.retryOrEnd(events, convID, r.Error, true)
		default:
			p.turnEnded = true
			end = &turnEnd{}
			switch {
			case r.Status != "SUCCESS":
				end.failure = r.Error
				if end.failure == "" {
					end.failure = "agy ended its turn with status " + r.Status
				}
			case len(r.DeniedActions) > 0:
				// A turn with a denial says SUCCESS, but the denial ended it: the work was not done.
				actions := make([]string, len(r.DeniedActions))
				for i, a := range r.DeniedActions {
					actions[i] = a.Action
				}
				end.failure = p.deniedReason(actions)
			}
			if end.failure != "" {
				p.failed = true
				events.add(planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, end.failure)
			}
		}
	}
	// The usage of a result is what the whole conversation spent so far. agy gives no cost: tokens only. Its
	// thinking tokens are counted as written ones.
	usage := &planv1.Usage{}
	if u := r.Usage; u != nil {
		usage.InputTokens, usage.OutputTokens = u.InputTokens, u.OutputTokens+u.ThinkingTokens
		usage.CacheReadTokens = u.CacheReadTokens
		usage.ThinkingTokens = u.ThinkingTokens
	}
	ev := events.add(planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, fmt.Sprintf("%d turns, %d tokens in, %d out",
		r.NumTurns, usage.GetInputTokens(), usage.GetOutputTokens()))
	ev.Usage, ev.SessionID = usage, r.ConversationID
	return end
}

func (p *agyParser) forget(step int) {
	delete(p.text, step)
	for i, s := range p.order {
		if s == step {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

// flush says the text of a response cut short by the end of the process, and why agy stopped when its error
// output alone said it denied a permission: that notice may come after the result, the two outputs being read
// apart.
func (p *agyParser) flush() []Event {
	events := p.cut()
	if p.closingFailed {
		return events
	}
	if p.denied != "" && !p.failed {
		p.failed = true
		events = append(events, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: p.deniedReason([]string{p.denied})})
	}
	return events
}

// deniedReason says, for a person, why agy stopped: it denied the actions (permissions) it could not ask for.
func (p *agyParser) deniedReason(actions []string) string {
	what := "it cannot run commands headless"
	if !slices.Contains(actions, "command") {
		what = `headless, it cannot ask for the "` + strings.Join(actions, `", "`) + `" permission`
	}
	if p.lastCall != "" && !p.lastAnswered {
		what += " (" + p.lastCall + ")"
	}
	return "agy stopped: " + what + ". Run this task with claude or codex."
}

// cut says the text of a response cut short.
func (p *agyParser) cut() []Event {
	var events []Event
	for _, step := range p.order {
		if text := p.text[step].String(); strings.TrimSpace(text) != "" {
			events = append(events, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: strings.TrimRight(text, "\n") + " [cut]"})
		}
	}
	p.text, p.order = nil, nil
	return events
}

// agyErrorPrefix starts the line agy writes on its error output when a turn ends on a model or agent failure.
const agyErrorPrefix = "AGY_ERROR:"

// agyDenied is in the line agy writes on its error output when it refused tools it could not ask for:
// `… a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied. …`
// (real runs, agy 1.3.0).
const agyDenied = "headless mode cannot prompt for"

// agyDenyRule is in the error of a tool step a deny grant refused: `permission check failed for command "git push
// origin w1": Permission denied for command(git push origin w1). Matches user-configured deny rule.` (real runs, agy
// 1.3.3), a project's deny grant included.
const agyDenyRule = "deny rule"

// agyDeniedPath finds the path a deny grant refused to a file tool: `permission check failed for read_file "/x/a.html":
// Permission denied for read_file(/x/a.html). Matches user-configured deny rule.` (real runs, agy 1.4.1, W308): a
// path outside the folders Djinn's project grants, which no mode grants (pathPermissions).
var agyDeniedPath = regexp.MustCompile(`Permission denied for (read_file|write_file)\((.+?)\)\. `)

// agyDeniedPermission finds the permission in the denial notice.
var agyDeniedPermission = regexp.MustCompile(`required the "([^"]+)" permission`)

func (p *agyParser) stderr(raw string) []Event {
	if p.closingFailed {
		return nil
	}
	switch {
	case strings.HasPrefix(raw, agyErrorPrefix):
		text := strings.TrimSpace(strings.TrimPrefix(raw, agyErrorPrefix))
		var e struct {
			ShortError string `json:"short_error"`
			Retryable  *bool  `json:"retryable"`
			HTTPStatus int    `json:"http_status"`
		}
		if json.Unmarshal([]byte(text), &e) == nil && e.ShortError != "" {
			text = e.ShortError
			if e.HTTPStatus != 0 {
				text += " (HTTP " + strconv.Itoa(e.HTTPStatus) + ")"
			}
			if e.Retryable != nil && *e.Retryable {
				text += ", retryable"
			}
		}
		if p.canFinishNow() {
			p.turnEnded = true
			p.closingFailed = true
			return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: "closing call failed: " + text, Raw: raw}}
		}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, Text: text, Raw: raw}}
	case strings.Contains(raw, agyDenied):
		p.denied = "command"
		if m := agyDeniedPermission.FindStringSubmatch(raw); m != nil {
			p.denied = m[1]
		}
		return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: permissionDenied + raw, Raw: raw}}
	}
	return []Event{{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: raw, Raw: raw}}
}

// agyCall is what a tool step calls, for a person: the command line of run_command, else the tool and its
// parameters.
func agyCall(name string, params json.RawMessage) string {
	if name == "run_command" {
		var p struct {
			CommandLine string `json:"CommandLine"`
		}
		if json.Unmarshal(params, &p) == nil && p.CommandLine != "" {
			return p.CommandLine
		}
	}
	return strings.TrimSpace(name + " " + compactOrEmpty(params))
}

// compactOrEmpty writes JSON on one line; nothing for no JSON.
func compactOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return compact(raw)
}
