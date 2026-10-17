package harness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// Whole outputs, and a red tree not checked twice (W245, decision Q47 B, step 4 of L15). On 17/10/2026 the push checks
// of feat/qol ran 9 times on the same commit 818a59db, from 01:07 to 03:56, red each time on the same spec, and no
// correction started; each red kept only the last 10 lines of what the check printed, which named a trace file, not
// the spec. Now:
//   - each red check, a commit check, a push check or the setup, keeps its whole output in a file of its own under
//     Djinn's data folder (checksDir), with the tests and specs it names as failing (failingTests): the integration's
//     events link it, and so does the prompt of the correction that repairs it;
//   - push checks red on a tree do not run again on that very tree with the same command: when the push is due again
//     on it, Djinn starts its correction at once, or keeps the one that runs (holdSeen). Only a push the person asks
//     for, or approves, checks such a tree again.

// keptOutputs is how many outputs of red checks a project keeps: past it, the oldest files go.
const keptOutputs = 100

// checksDir is the folder of a project's red check outputs, in Djinn's data folder.
func checksDir(home, projectID string) string {
	return filepath.Join(home, "projects", projectID, "checks")
}

// fileWord is name as a file name's word: what is not a letter, a digit, - or _ becomes -.
var fileWord = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// keepOutput writes out, the whole output of run, a check that ran and failed, in a file of its own under the project
// projectID's checksDir, the oldest ones past keptOutputs removed. It returns the file's path; "" when it could not
// write it, said in the log.
func (h *Harness) keepOutput(projectID string, run *planv1.CheckRun, out string) string {
	dir := checksDir(h.home, projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logx.L().Warn("keep a red check's output", "project", projectID, "check", run.GetName(), "err", err)
		return ""
	}
	base := fmt.Sprintf("%s-%s-%s", h.now().UTC().Format("20060102T150405.000Z"),
		strings.Trim(fileWord.ReplaceAllString(run.GetName(), "-"), "-"), short8(run.GetSha()))
	file := filepath.Join(dir, base+".log")
	for i := 2; ; i++ { // Two runs at the same instant: a fake clock, or a clock that went back.
		if _, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
			break
		}
		file = filepath.Join(dir, fmt.Sprintf("%s-%d.log", base, i))
	}
	head := fmt.Sprintf("# %s (%s) at %s, wish %s: %s\n\n", run.GetName(), run.GetCommand(), run.GetSha(), run.GetWishId(),
		headline(run.GetReason()))
	if err := fsx.WriteAtomicNoSync(file, []byte(head+out), 0o600); err != nil {
		logx.L().Warn("keep a red check's output", "project", projectID, "check", run.GetName(), "file", file, "err", err)
		return ""
	}
	pruneOutputs(dir)
	return file
}

// pruneOutputs removes the oldest outputs in dir past keptOutputs: their names start with their time.
func pruneOutputs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keptOutputs {
		return
	}
	slices.Sort(names)
	for _, name := range names[:len(names)-keptOutputs] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// maxFailing is how many failing tests or specs a red check records.
const maxFailing = 50

var (
	// slowtestsFailed is the line tools/slowtests ends with: "Failed: <package> <test>, …" (W135).
	slowtestsFailed = regexp.MustCompile(`(?m)^Failed: (.+)$`)
	// playwrightFailed is a failure's heading in Playwright's list reporter: "  1) [chromium] › docs.spec.ts:32:3 › …".
	playwrightFailed = regexp.MustCompile(`(?m)^\s*\d+\) (?:\[[^\]]*\] › )?(\S+\.(?:spec|test)\.[cm]?[jt]sx?):(\d+)`)
	// nodeFailed is a failing test in node --test's output: "not ok 3 - name" (tap), "✖ name (1.2ms)" (spec).
	nodeFailed = regexp.MustCompile(`(?m)^\s*(?:not ok \d+ - |✖ )(.+?)(?: \([\d.]+m?s\))?\s*$`)
)

// failingTests are the tests and specs out, the output of a red check, names as failing, in the order it names them:
// the Go tests of slowtests' "Failed:" line, else those of go test's "--- FAIL:" lines, else its failed packages; the
// specs of Playwright's failures (file and line); the tests node --test says failed. At most maxFailing.
func failingTests(out string) []string {
	var names []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(names, s) && len(names) < maxFailing {
			names = append(names, s)
		}
	}
	if m := slowtestsFailed.FindAllStringSubmatch(out, -1); len(m) > 0 {
		for item := range strings.SplitSeq(m[len(m)-1][1], ", ") {
			pkg, test, ok := strings.Cut(strings.TrimSpace(item), " ")
			if !ok {
				add(item)
				continue
			}
			add(path.Base(pkg) + " " + test)
		}
	} else {
		for _, m := range goFail.FindAllStringSubmatch(out, -1) {
			add(m[1])
		}
		if len(names) == 0 {
			for _, m := range goFailPackage.FindAllStringSubmatch(out, -1) {
				add(m[1])
			}
		}
	}
	for _, m := range playwrightFailed.FindAllStringSubmatch(out, -1) {
		add(path.Base(m[1]) + ":" + m[2])
	}
	for _, m := range nodeFailed.FindAllStringSubmatch(out, -1) {
		if name := m[1]; !strings.Contains(name, "# SKIP") && !strings.Contains(name, "# TODO") && name != "failing tests:" {
			add(name)
		}
	}
	return names
}

// keptOutput is f with what run, its red check, kept: the file of its whole output, and the tests it names failing.
func keptOutput(f *planv1.IntegrationFailure, run *planv1.CheckRun) *planv1.IntegrationFailure {
	f.OutputFile, f.Failed = run.GetOutputFile(), slices.Clone(run.GetFailed())
	return f
}

// outputStep is the event that links the whole output of f's red check, run (nil: named after f's reason), and names
// what failed; "" when none was kept.
func outputStep(f *planv1.IntegrationFailure, run *planv1.CheckRun) string {
	if f.GetOutputFile() == "" {
		return ""
	}
	name := run.GetName()
	if name == "" {
		name = checkName(f)
	}
	text := "integration: the whole output of " + name + " is in " + f.GetOutputFile()
	if len(f.GetFailed()) > 0 {
		text += "; failing: " + listed(f.GetFailed())
	}
	return text
}

// checkName is the name of f's red check, as its reason starts with it ("test: test exited 1"); "the check" when it does
// not.
func checkName(f *planv1.IntegrationFailure) string {
	name, _, ok := strings.Cut(headline(f.GetReason()), ":")
	if !ok || strings.ContainsAny(name, " \t") || name == "" {
		return "the check"
	}
	return name
}

// listed is names joined, the first 10 of them, then how many more.
func listed(names []string) string {
	if len(names) > 10 {
		return strings.Join(names[:10], ", ") + fmt.Sprintf(" and %d more", len(names)-10)
	}
	return strings.Join(names, ", ")
}

// outputLines is what a correction's prompt says of f's whole output: where it is, and what it names failing; "" when
// none was kept.
func outputLines(f *planv1.IntegrationFailure) string {
	if f.GetOutputFile() == "" {
		return ""
	}
	text := fmt.Sprintf("\n\nDjinn kept the check's whole output in %s: read it before you change anything.", f.GetOutputFile())
	if len(f.GetFailed()) > 0 {
		text += " It names as failing: " + listed(f.GetFailed()) + "."
	}
	return text
}

// seenRed is the failure that holds the push at state when its push checks ran red on the very tree of sha, the
// branch's tip now, with a command they would run on it again (settings): they are not run on it twice; nil otherwise.
// A check that could not run, or whose output was not kept, is no verdict on the tree: nil.
func seenRed(ctx context.Context, repo string, state *planv1.WishPush, settings plan.Settings, sha string) *planv1.IntegrationFailure {
	f := state.GetFailure()
	if state.GetHeld() == "" || state.GetUnchecked() || f.GetState() != planv1.IntegrationState_INTEGRATION_STATE_RED ||
		f.GetOutputFile() == "" || f.GetCommand() == "" || f.GetBase() == "" {
		return nil
	}
	same := f.GetCommand() == settings.Setup || slices.ContainsFunc(settings.ChecksAt(planv1.CheckWhen_CHECK_WHEN_PUSH),
		func(c *planv1.ProjectCheck) bool { return c.GetCommand() == f.GetCommand() })
	if !same {
		return nil
	}
	if f.GetBase() != sha {
		was, err := git(ctx, repo, "rev-parse", f.GetBase()+"^{tree}")
		if err != nil {
			return nil
		}
		if now, err := git(ctx, repo, "rev-parse", sha+"^{tree}"); err != nil || now != was {
			return nil
		}
	}
	return f
}
