package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// upkeep is an integration whose wish integrates into feat/x, a branch of its own that the person's checkout is on,
// while main, the branch the repository started on, lives on origin: a bare repository in a temporary folder, its
// default branch main, that a teammate pushes to from a clone of their own. No network.
type upkeep struct {
	*integration
	main, other string
}

// keepingUp makes an upkeep whose project's settings add settings to the integration's, and name the fake agent.
func keepingUp(t *testing.T, settings string) *upkeep {
	t.Helper()
	in := integrating(t)
	u := &upkeep{integration: in, main: in.branch, other: filepath.Join(t.TempDir(), "other")}
	bare := in.remote(t)
	in.git(t, in.repo, "remote", "set-head", "origin", u.main) // As a clone records the remote's default branch.
	in.git(t, in.repo, "switch", "--quiet", "-c", "feat/x")
	in.branch = "feat/x"
	if _, err := in.wishes.SetIntegration(t.Context(), connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
		WishId: in.wishID, ProjectId: in.projectID, Branch: in.branch,
	})); err != nil {
		t.Fatal(err)
	}
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"provider: PROVIDER_FAKE\ngenerated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\n"+settings)
	in.git(t, in.repo, "clone", "--quiet", "--branch", u.main, bare, u.other)
	for _, args := range [][]string{{"config", "user.name", "Mate"}, {"config", "user.email", "mate@example.com"}} {
		in.git(t, u.other, args...)
	}
	// Git's own upkeep after a commit, a fetch or a push costs a process each time: none in a test.
	for _, dir := range []string{in.repo, bare, u.other} {
		in.git(t, dir, "config", "maintenance.auto", "false")
		in.git(t, dir, "config", "gc.auto", "0")
	}
	return u
}

// onMain commits files on main as a teammate does, from their clone, pushes it, and returns the commit.
func (u *upkeep) onMain(t *testing.T, message string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		writeFile(t, u.other, name, content)
	}
	commitAll(t, u.other, message)
	u.git(t, u.other, "push", "--quiet", "origin", "HEAD:refs/heads/"+u.main)
	return u.git(t, u.other, "rev-parse", "HEAD")
}

// release tags the release v of main as `task release` does, and pushes the tag: a commit that adds the built interface
// on top of main, off any branch. It returns the release's commit.
func (u *upkeep) release(t *testing.T, v string) string {
	t.Helper()
	u.git(t, u.other, "switch", "--quiet", "--detach")
	writeFile(t, u.other, "dist/index.html", "<p>built</p>\n")
	commitAll(t, u.other, "Release "+v)
	u.git(t, u.other, "tag", v)
	u.git(t, u.other, "push", "--quiet", "origin", "refs/tags/"+v)
	sha := u.git(t, u.other, "rev-parse", "HEAD")
	u.git(t, u.other, "switch", "--quiet", u.main)
	return sha
}

// mainState is where the wish's integration branch stands against main.
func (u *upkeep) mainState(t *testing.T) *planv1.WishMain {
	t.Helper()
	wish, err := store.Get[*planv1.Wish](t.Context(), u.db, u.wishID)
	if err != nil {
		t.Fatal(err)
	}
	return mainState(wish, u.projectID)
}

// merges are the merges of main the journal recorded.
func (u *upkeep) merges(t *testing.T) []*planv1.MainMerge {
	t.Helper()
	entries, err := store.Commands(t.Context(), u.db, func(c store.Command) bool { return c.Method == methodMain })
	if err != nil {
		t.Fatal(err)
	}
	var out []*planv1.MainMerge
	for _, e := range entries {
		m := &planv1.MainMerge{}
		if err := proto.Unmarshal(e.Request, m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// holds tells whether the integration branch holds the commit sha.
func (u *upkeep) holds(t *testing.T, sha string) bool {
	t.Helper()
	return isAncestor(t.Context(), u.repo, sha, "refs/heads/"+u.branch)
}

// TestMergeMainAtARelease: main's commits wait for a release; the cadence keeps Djinn from looking at main more than
// hourly; once main holds a release, Djinn merges main into the integration branch in its own worktree, the commit
// checks run through their gates, the branch moves, the person's clean checkout follows, the journal and the wish say
// it, and the window proposes the build. The release's own commit, the built interface, stays out.
func TestMergeMainAtARelease(t *testing.T) {
	testx.Portable(t)
	built := make(chan Built, 1)
	u := keepingUp(t, "install: \"install\"\n")
	u.h.built = func(b Built) { built <- b }
	old := u.tip(t)
	fix := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})

	// main is ahead, without a release: nothing is merged, and Djinn noted when it looked.
	u.pass(t, 0)
	looked := u.mainState(t).GetCheckTime().AsTime()
	if u.tip(t) != old || len(u.merges(t)) != 0 || !looked.Equal(u.clock()) {
		t.Fatalf("main merged without a release: %s; looked at %v, want %v", u.tip(t), looked, u.clock())
	}

	// A release comes, but within the hour Djinn does not look at main again.
	dist := u.release(t, "v0.2.0")
	u.pass(t, 59*time.Minute)
	if u.tip(t) != old || !u.mainState(t).GetCheckTime().AsTime().Equal(looked) {
		t.Fatalf("Djinn looked at main within the hour: %s, %v", u.tip(t), u.mainState(t))
	}

	// An hour after the last look: main is merged, tested, the branch moved.
	u.pass(t, time.Minute)
	tip := u.tip(t)
	if tip == old || !u.holds(t, fix) || u.holds(t, dist) {
		t.Fatalf("after the release: %s holds main %v, the release commit %v", tip, u.holds(t, fix), u.holds(t, dist))
	}
	if parents := strings.Fields(u.git(t, u.repo, "log", "-1", "--format=%P", tip)); len(parents) != 2 || parents[0] != old || parents[1] != fix {
		t.Errorf("the merge's parents %v; want %s then main's %s", parents, old, fix)
	}
	if msg := u.git(t, u.repo, "log", "-1", "--format=%s", tip); msg != "Merge origin/"+u.main+" (v0.2.0) into feat/x" {
		t.Errorf("the merge's message %q", msg)
	}
	wt := integrationDir(u.home, u.projectID, u.wishID)
	if !slices.Contains(u.runs, "test in "+filepath.Join(wt, "app")) || !slices.Contains(u.gates, "test -") {
		t.Errorf("the commit checks did not run in the integration worktree, under their gate: %v, %v", u.runs, u.gates)
	}
	if b, err := os.ReadFile(filepath.Join(u.repo, "app", "src", "login.txt")); err != nil || string(b) != "fixed\n" {
		t.Errorf("your checkout did not follow: %q, %v", b, err)
	}
	merges := u.merges(t)
	if len(merges) != 1 {
		t.Fatalf("journal: %v", merges)
	}
	m := merges[0]
	if m.GetBranch() != "feat/x" || m.GetMain() != "origin/"+u.main || m.GetMainSha() != fix || m.GetOldSha() != old ||
		m.GetNewSha() != tip || m.GetCount() != 1 || m.GetRelease() != "v0.2.0" || !slices.Equal(m.GetCommits(), []string{"Fix the login"}) {
		t.Errorf("the merge recorded %v", m)
	}
	if state := u.mainState(t); !proto.Equal(state.GetLast(), m) || state.GetHeld() != "" {
		t.Errorf("the wish's head %v", state)
	}
	select {
	case b := <-built:
		if b.Sha != tip || b.Branch != "feat/x" || b.Install != "install" || !slices.Equal(b.Changes, []string{"Fix the login"}) {
			t.Errorf("the build proposed %+v", b)
		}
	default:
		t.Error("no build proposed")
	}
}

// TestLookAtMainAtOnce: a release the running Djinn finds makes it look at main at once, whatever the cadence.
func TestLookAtMainAtOnce(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "")
	u.pass(t, 0)
	fix := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})
	u.release(t, "v0.3.0")
	u.h.LookAtMain()
	u.pass(t, time.Minute)
	if !u.holds(t, fix) || len(u.merges(t)) != 1 || u.merges(t)[0].GetRelease() != "v0.3.0" {
		t.Errorf("v0.3.0 not merged at once: %v", u.merges(t))
	}
}

// TestMergeMainEachCommit: merge_main: MERGE_MAIN_COMMIT merges main once it holds any commit the branch lacks, a
// release or not; MERGE_MAIN_OFF never does.
func TestMergeMainEachCommit(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
	fix := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})
	u.pass(t, 0)
	if !u.holds(t, fix) || len(u.merges(t)) != 1 || u.merges(t)[0].GetRelease() != "" || u.merges(t)[0].GetCount() != 1 {
		t.Fatalf("main not merged at its commit: %v", u.merges(t))
	}

	writeFile(t, u.home, filepath.Join("projects", u.projectID, "settings.txtpb"), "test: \"test\"\nmerge_main: MERGE_MAIN_OFF\n")
	next := u.onMain(t, "More", map[string]string{"app/src/more.txt": "more\n"})
	u.h.LookAtMain()
	u.pass(t, 2*time.Hour)
	if u.holds(t, next) {
		t.Error("merge_main off, main merged")
	}
}

// TestMainConflictStartsACorrection: main conflicting in code with the integration branch leaves the branch as it
// was and starts a correction worker by itself, on the merge of main under way, never a rebase; its work integrates
// like any task's, and its success records main as merged, settled by it.
func TestMainConflictStartsACorrection(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
	writeFile(t, u.repo, "app/README.md", "# Branch\n")
	commitAll(t, u.repo, "The branch's readme")
	old := u.tip(t)
	theirs := u.onMain(t, "Main's readme", map[string]string{"app/README.md": "# Main\n"})
	var conflict string
	u.correctWith(func(dir, _ string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "README.md"))
		conflict = string(b)
		writeFile(t, dir, "README.md", "# Main and Branch\n")
		commitAll(t, dir, "Settle the readme with main") // Which concludes the merge.
		return ""
	})

	u.pass(t, 0)
	state := u.mainState(t)
	if u.tip(t) != old || state.GetCorrectedBy() == "" || state.GetAttempts() != 1 {
		t.Fatalf("after the conflict: tip %s, %v", u.tip(t), state)
	}
	c := u.ended(t, state.GetCorrectedBy())
	f := c.GetCorrection().GetFailure()
	if c.GetTitle() != "Settle the conflict of origin/"+u.main+" with feat/x" || c.GetProvider() != planv1.Provider_PROVIDER_FAKE ||
		f.GetMainSha() != theirs || f.GetBase() != old || !slices.Equal(f.GetFiles(), []string{"app/README.md"}) || len(f.GetTaskIds()) != 0 {
		t.Fatalf("the correction worker %v", c)
	}
	want := "conflict: origin/" + u.main + " conflicts with feat/x in app/README.md; feat/x stays as it was; " + c.GetCode() +
		" corrects it, attempt 1 of 2"
	if state.GetHeld() != want {
		t.Errorf("held %q; want %q", state.GetHeld(), want)
	}
	prompt, err := firstPrompt(u.db, c.GetId())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"could not integrate origin/" + u.main + " into feat/x", "the merge of origin/" + u.main + " at " +
		theirs[:8] + " is under way", "- app/README.md", "Its success brings origin/" + u.main + " in with yours"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("the correction's prompt lacks %q:\n%s", s, prompt)
		}
	}
	if !strings.Contains(conflict, "<<<<<<<") || !strings.Contains(conflict, "# Main") || !strings.Contains(conflict, "# Branch") {
		t.Errorf("the correction's worktree did not hold the conflict: %q", conflict)
	}

	// Its work integrates like any task's: main is in, recorded as merged by it.
	u.pass(t, time.Second)
	tip := u.tip(t)
	if !u.holds(t, theirs) || u.git(t, u.repo, "show", tip+":app/README.md") != "# Main and Branch" {
		t.Fatalf("after the correction: %s", u.git(t, u.repo, "show", tip+":app/README.md"))
	}
	merges := u.merges(t)
	if len(merges) != 1 || merges[0].GetCorrectedBy() != c.GetId() || merges[0].GetMainSha() != theirs || merges[0].GetNewSha() != tip ||
		merges[0].GetOldSha() != old || merges[0].GetCount() != 1 {
		t.Fatalf("journal: %v", merges)
	}
	if state := u.mainState(t); state.GetCorrectedBy() != "" || state.GetHeld() != "" || state.GetAttempts() != 0 {
		t.Errorf("the wish's head after the correction: %v", state)
	}
}

// TestMainAttemptsSpent: past correction_attempts, a merge of main that fails stays out until main moves.
func TestMainAttemptsSpent(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\ncorrection_attempts: 0\n")
	u.testCode, u.testOut = 1, "--- FAIL: TestLogin"
	old := u.tip(t)
	u.onMain(t, "Break the login", map[string]string{"app/src/login.txt": "broken\n"})
	u.pass(t, 0)
	state := u.mainState(t)
	if u.tip(t) != old || state.GetCorrectedBy() != "" || state.GetFailedSha() == "" ||
		!strings.HasPrefix(state.GetHeld(), "red: test exited 1") || !strings.HasSuffix(state.GetHeld(), "Djinn merges origin/"+u.main+" again once it moves") {
		t.Fatalf("after a red merge, no attempt allowed: %v", state)
	}
	runs := len(u.runs)
	u.pass(t, 2*time.Hour)
	if len(u.runs) != runs {
		t.Errorf("the same main merged again: %v", u.runs[runs:])
	}
	u.testCode = 0
	fix := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})
	u.pass(t, 2*time.Hour)
	if !u.holds(t, fix) || u.mainState(t).GetFailedSha() != "" || u.mainState(t).GetHeld() != "" {
		t.Errorf("main moved, not merged: %v", u.mainState(t))
	}
}

// TestReleaseFit: what a release means for a Djinn built from a checkout. On main, a release that holds the build
// installs by itself, unless the build had changes not committed or the settings say not to; a build that holds the
// release is offered nothing; on a branch with work of its own, the release is not installed over it: Djinn looks at
// main at once to merge it instead. A build no project holds is offered the release, as a release binary is.
func TestReleaseFit(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "")
	ctx := t.Context()
	build := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})
	u.onMain(t, "Speed up the page", map[string]string{"app/src/page.txt": "fast\n"})
	u.release(t, "v0.2.0")
	ahead := u.onMain(t, "After the release", map[string]string{"app/src/after.txt": "after\n"})

	fit := func(build string, dirty bool) ReleaseFit {
		got, err := u.h.Release(ctx, build, dirty, "v0.2.0")
		if err != nil {
			t.Error(err)
		}
		return got
	}
	// The person's checkout on a branch, its build with work the release lacks: merged, not installed.
	writeFile(t, u.repo, "app/src/mine.txt", "mine\n")
	commitAll(t, u.repo, "My work")
	mine := u.git(t, u.repo, "rev-parse", "HEAD")
	if got := fit(mine, false); got != ReleaseMerge || !u.h.mainNow.Load() {
		t.Errorf("on a branch: %v, a look at main %v; want ReleaseMerge, true", got, u.h.mainNow.Load())
	}
	// On a branch, a build the release holds is offered.
	if got := fit(build, false); got != ReleaseOffer {
		t.Errorf("on a branch, a build in the release: %v; want ReleaseOffer", got)
	}

	u.git(t, u.repo, "switch", "--quiet", u.main)
	t.Run("on main", func(t *testing.T) { // Each case only reads the repository: they run side by side.
		for _, c := range []struct {
			name  string
			build string
			dirty bool
			want  ReleaseFit
		}{
			{"the release holds the build", build, false, ReleaseInstall},
			{"a build with changes not committed", build, true, ReleaseOffer},
			{"a build that holds the release", ahead, false, ReleaseHeld},
			{"a build no project holds", strings.Repeat("ab", 20), false, ReleaseOffer},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				if got := fit(c.build, c.dirty); got != c.want {
					t.Errorf("%v; want %v", got, c.want)
				}
			})
		}
	})
	writeFile(t, u.home, filepath.Join("projects", u.projectID, "settings.txtpb"), "test: \"test\"\ninstall_releases: false\n")
	if got := fit(build, false); got != ReleaseOffer {
		t.Errorf("on main, install_releases false: %v; want ReleaseOffer", got)
	}
}

// TestMainTagClash: when a local tag differs from origin's, fetching and merging main proceeds anyway; the clashing tag
// is reported once on the wish as a block, and subsequent passes do not duplicate it.
func TestMainTagClash(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
	// Make a local tag v0.1.0 on u.repo (pointing to the initial commit of u.repo).
	u.git(t, u.repo, "tag", "v0.1.0")

	// Teammate commits on main and tags it with a different commit as v0.1.0, then pushes branch and tag to origin.
	fix := u.onMain(t, "Fix the login", map[string]string{"app/src/login.txt": "fixed\n"})
	u.git(t, u.other, "tag", "v0.1.0")
	u.git(t, u.other, "push", "--quiet", "origin", "refs/tags/v0.1.0")

	// Verify that the local tag and remote tag differ.
	localSha := u.git(t, u.repo, "rev-parse", "refs/tags/v0.1.0")
	remoteSha := u.git(t, u.other, "rev-parse", "refs/tags/v0.1.0")
	if localSha == remoteSha {
		t.Fatalf("local tag and remote tag point to the same sha: %s", localSha)
	}

	// First pass: main should be fetched and merged despite the clashing tag.
	u.pass(t, 0)
	if !u.holds(t, fix) || len(u.merges(t)) != 1 {
		t.Fatalf("main was not merged: holds=%v merges=%d", u.holds(t, fix), len(u.merges(t)))
	}
	if state := u.mainState(t); state.GetHeld() != "" {
		t.Errorf("main is held: %q", state.GetHeld())
	}

	// Verify the clashing tag was reported once on the wish as a block.
	msg := "tag v0.1.0 differs from origin's: kept the local one"
	blocks, err := store.List[*planv1.Block](t.Context(), u.db, store.Where{"wish_id": u.wishID})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, b := range blocks {
		if b.GetTitle() == msg && b.GetContent() == msg && b.GetKind() == "report" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 report block for clashing tag, found %d (total blocks: %d)", count, len(blocks))
	}

	// Verify the local tag remained unchanged.
	if got := u.git(t, u.repo, "rev-parse", "refs/tags/v0.1.0"); got != localSha {
		t.Errorf("local tag changed to %s; want %s", got, localSha)
	}

	// Second pass: another commit on main. The clash report must not be duplicated.
	next := u.onMain(t, "More work", map[string]string{"app/src/more.txt": "more\n"})
	u.pass(t, 2*time.Hour)
	if !u.holds(t, next) {
		t.Fatalf("next commit was not merged: %s", next)
	}

	blocks, err = store.List[*planv1.Block](t.Context(), u.db, store.Where{"wish_id": u.wishID})
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for _, b := range blocks {
		if b.GetTitle() == msg {
			count++
		}
	}
	if count != 1 {
		t.Errorf("report block duplicated on second pass: count = %d", count)
	}
}

// TestMainFetchFails: when fetching main from its remote fails, the reason shown is git's own message, not
// "exit status 1".
func TestMainFetchFails(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "")
	// Set origin remote URL to a nonexistent path.
	u.git(t, u.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nonexistent"))

	u.pass(t, 0)
	state := u.mainState(t)
	held := state.GetHeld()
	if held == "" {
		t.Fatal("main state held is empty; expected failure reason")
	}
	if strings.Contains(held, "exit status 1") {
		t.Errorf("held contains 'exit status 1': %q", held)
	}
	if !strings.Contains(held, "fatal:") {
		t.Errorf("held does not contain git's fatal error message: %q", held)
	}
	if !strings.HasPrefix(held, "fetch ") {
		t.Errorf("held lacks 'fetch ' prefix: %q", held)
	}
}

// TestMainMergeFails: when merging main fails without conflicts (e.g. unrelated histories), the reason shown
// is git's own message, not "exit status 1".
func TestMainMergeFails(t *testing.T) {
	testx.Portable(t)
	u := keepingUp(t, "merge_main: MERGE_MAIN_COMMIT\n")
	// Make an unrelated branch on u.other and force push it as main to origin.
	u.git(t, u.other, "checkout", "--orphan", "unrelated")
	commitAll(t, u.other, "Unrelated initial commit")
	u.git(t, u.other, "push", "--force", "--quiet", "origin", "unrelated:refs/heads/"+u.main)

	u.pass(t, 0)
	state := u.mainState(t)
	held := state.GetHeld()
	if held == "" {
		t.Fatal("main state held is empty; expected failure reason")
	}
	if strings.Contains(held, "exit status 1") {
		t.Errorf("held contains 'exit status 1': %q", held)
	}
	if !strings.Contains(held, "refusing to merge unrelated histories") {
		t.Errorf("held does not contain git merge message: %q", held)
	}
}
