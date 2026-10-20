package plan

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// TestChangeIntegrationBranch: a wish's integration branch changes to an existing local branch, or to a new name made
// from the current one, or from the remote's branch of that name, through SetIntegration; Branches lists where it may
// land, the branches of Djinn's tasks left out; and while a merge runs into the branch, the change is refused with what
// runs, the branch kept.
func TestChangeIntegrationBranch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	repo := gitRepo(t, "app", origin)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# App\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "First")
	git("branch", "-M", "main")
	git("branch", "release")
	git("branch", "w1-work")

	c := serve(t)
	add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: repo}))
	if err != nil {
		t.Fatal(err)
	}
	projectID := add.Msg.GetProject().GetId()
	made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Land", ProjectIds: []string{projectID}}))
	if err != nil {
		t.Fatal(err)
	}
	wishID := made.Msg.GetWish().GetId()
	if got := IntegrationBranchOf(made.Msg.GetWish(), projectID); got != "main" {
		t.Fatalf("integration branch at the start = %q, want main, the reference branch", got)
	}
	worker := &planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W1", Branch: "w1-work",
		Status: planv1.TaskStatus_TASK_STATUS_DONE}
	c.put(t, worker)

	listed, err := c.projects.Branches(ctx, connect.NewRequest(&planv1.ProjectServiceBranchesRequest{Project: "app"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := listed.Msg.GetBranches(); !slices.Contains(got, "main") || !slices.Contains(got, "release") || slices.Contains(got, "w1-work") {
		t.Errorf("branches = %v, want main and release, not the worker's w1-work", got)
	}
	if got := listed.Msg.GetCheckedOut(); got != "main" {
		t.Errorf("checked out = %q, want main", got)
	}

	set := func(branch string, create bool) (*planv1.Wish, error) {
		res, err := c.wishes.SetIntegration(ctx, connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wishID, Branch: branch, CreateBranch: create,
		}))
		if err != nil {
			return nil, err
		}
		return res.Msg.GetWish(), nil
	}
	// An existing branch.
	wish, err := set("release", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := IntegrationBranchOf(wish, projectID); got != "release" {
		t.Errorf("integration branch = %q, want release", got)
	}
	// A new name, made at the tip of the current one.
	git("checkout", "-q", "release")
	if err := os.WriteFile(filepath.Join(repo, "NOTES.md"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "On release")
	git("checkout", "-q", "main")
	if wish, err = set("feat/landing", true); err != nil {
		t.Fatal(err)
	}
	if got := IntegrationBranchOf(wish, projectID); got != "feat/landing" {
		t.Errorf("integration branch = %q, want feat/landing", got)
	}
	if got, want := git("rev-parse", "feat/landing"), git("rev-parse", "release"); got != want {
		t.Errorf("feat/landing made at %s, want release's tip %s", got, want)
	}
	// A name the remote holds, an open merge request's branch: made from the remote's, its own commits kept, no
	// upstream set (wish Dew, 13/10/2026: made from the integration branch, it lacked them, and nothing could be pushed).
	git("switch", "-q", "-c", "ET-1-mr", "main")
	if err := os.WriteFile(filepath.Join(repo, "MR.md"), []byte("mr\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "On the merge request")
	git("push", "-q", "origin", "ET-1-mr")
	mr := git("rev-parse", "HEAD")
	git("switch", "-q", "main")
	git("branch", "-D", "ET-1-mr")
	if _, err := set("ET-1-mr", true); err != nil {
		t.Fatal(err)
	}
	if got := git("rev-parse", "refs/heads/ET-1-mr"); got != mr {
		t.Errorf("ET-1-mr made at %s, want the remote's %s", got, mr)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "branch.ET-1-mr.merge").CombinedOutput(); err == nil {
		t.Errorf("ET-1-mr tracks %s, want no upstream", out)
	}
	if _, err = set("feat/landing", false); err != nil {
		t.Fatal(err)
	}

	// A batch merging into it: refused, naming it, the branch kept.
	worker.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, Branch: "feat/landing"}
	c.put(t, worker)
	_, err = set("main", false)
	const merging = "cannot change the integration branch now: the work of W1 is being merged into feat/landing; change it once that merge has ended"
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), merging) {
		t.Errorf("change while W1 merges = %v, want %q", err, merging)
	}
	// Setting the branch it has is no change: nothing to refuse.
	if _, err := set("feat/landing", false); err != nil {
		t.Errorf("setting the same branch while W1 merges = %v, want it accepted", err)
	}

	// A correction worker bringing work into it.
	worker.Integration = &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_CONFLICT}
	c.put(t, worker)
	correction := &planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W2",
		Status:     planv1.TaskStatus_TASK_STATUS_RUNNING,
		Correction: &planv1.TaskCorrection{Failure: &planv1.IntegrationFailure{TaskIds: []string{worker.GetId()}}}}
	c.put(t, correction)
	if _, err = set("main", false); err == nil || !strings.Contains(err.Error(), "W2 brings work into feat/landing") {
		t.Errorf("change while W2 corrects = %v, want it refused naming W2", err)
	}
	correction.Status = planv1.TaskStatus_TASK_STATUS_DONE
	c.put(t, correction)

	// A correction worker settling main's merge.
	wish, err = store.Get[*planv1.Wish](ctx, c.store, wishID)
	if err != nil {
		t.Fatal(err)
	}
	mainFix := &planv1.Task{Id: store.NewID(), WishId: wishID, ProjectId: projectID, Code: "W3", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}
	c.put(t, mainFix)
	wish.Mains = []*planv1.WishMain{{ProjectId: projectID, CorrectedBy: mainFix.GetId()}}
	c.put(t, wish)
	if _, err = set("main", false); err == nil || !strings.Contains(err.Error(), "W3 settles the merge of main into feat/landing") {
		t.Errorf("change while W3 settles main = %v, want it refused naming W3", err)
	}
	wish.Mains = nil
	c.put(t, wish)

	// Nothing merges: the change goes.
	if wish, err = set("main", false); err != nil || IntegrationBranchOf(wish, projectID) != "main" {
		t.Errorf("change once the merges ended = %q, %v; want main", IntegrationBranchOf(wish, projectID), err)
	}
}

// TestBriefSaysWhereWorkLands: the wish's brief and the brief of every active wish name, in one line, where a wish's
// work lands: its integration branch, the project named when it has several; or per azima, each azima's own branch.
func TestBriefSaysWhereWorkLands(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	home := t.TempDir()
	c := serve(t, WithHome(home))
	var ids []string
	for _, name := range []string{"api", "web"} {
		add, err := c.projects.Add(ctx, connect.NewRequest(&planv1.ProjectServiceAddRequest{Directory: t.TempDir(), Name: name}))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, add.Msg.GetProject().GetId())
	}
	newWish := func(title string, projects ...string) *planv1.Wish {
		t.Helper()
		made, err := c.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: title, ProjectIds: projects}))
		if err != nil {
			t.Fatal(err)
		}
		wish, err := store.Get[*planv1.Wish](ctx, c.store, made.Msg.GetWish().GetId())
		if err != nil {
			t.Fatal(err)
		}
		return wish
	}
	one, two, azimas := newWish("One", ids[0]), newWish("Two", ids...), newWish("Azimas", ids[0])
	for _, w := range []struct {
		wish     *planv1.Wish
		branches []string
	}{{one, []string{"feat/one"}}, {two, []string{"feat/two", "main"}}, {azimas, []string{"feat/rest"}}} {
		for i, b := range w.branches {
			SetIntegrationBranch(w.wish, ids[i], b)
		}
		c.put(t, w.wish)
	}
	if _, err := c.wishes.PushStrategy(ctx, connect.NewRequest(&planv1.WishServicePushStrategyRequest{
		WishId: azimas.GetId(), Strategy: planv1.PushStrategy_PUSH_STRATEGY_AZIMA.Enum(),
	})); err != nil {
		t.Fatal(err)
	}

	lines := map[string]string{
		one.GetId(): "- Work lands in `feat/one`: `djinn wish set-integration <wish> --branch <branch>` changes it.\n",
		two.GetId(): "- Work lands in `feat/two` (api), `main` (web): " +
			"`djinn wish set-integration <wish> --project-id <project> --branch <branch>` changes it.\n",
		azimas.GetId(): "- Work lands per azima: each azima on its own branch, `djinn/<code>-<slug>` or the one named with " +
			"`djinn task update <azima> --branch <name>`, cut from main and landing in main through its pull request; " +
			"work of no azima in `feat/rest`.\n",
	}
	// Per azima, each azima says its own branch and where its pull or merge request stands, as its card does.
	const mr = "https://gitlab.example.com/dolmen/-/merge_requests/12"
	c.put(t, &planv1.Task{
		Id: store.NewID(), WishId: azimas.GetId(), ProjectId: ids[0], Code: "T1", Title: "Silae", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
		Azima: &planv1.Azima{
			Branch: "ET-4368-silae", BaseBranch: "main", CutFrom: strings.Repeat("a", 40), Named: true,
			Pr: &planv1.AzimaPr{Url: mr, State: planv1.AzimaPrState_AZIMA_PR_STATE_OPEN},
		},
	}, &planv1.Task{
		Id: store.NewID(), WishId: azimas.GetId(), ProjectId: ids[0], Code: "T2", Title: "Other subject", Kind: planv1.TaskKind_TASK_KIND_AZIMA,
	})
	lines[azimas.GetId()+" T1"] = "- **T1** Silae: ready, open; branch `ET-4368-silae` (named), pull request open: " + mr + "\n"
	lines[azimas.GetId()+" T2"] = "- **T2** Other subject: ready, open; branch `djinn/T2-other-subject`\n"
	global, err := BuildGlobalBrief(ctx, c.store, home)
	if err != nil {
		t.Fatal(err)
	}
	for key, line := range lines {
		id, _, _ := strings.Cut(key, " ")
		brief, err := BuildBrief(ctx, c.store, home, id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(brief.Moving, line) != 1 {
			t.Errorf("brief of %s lacks %q once:\n%s", id, line, brief.Moving)
		}
		if strings.Count(global, line) != 1 {
			t.Errorf("global brief lacks %q once:\n%s", line, global)
		}
	}
}

// TestPushHolds (W37): each branch keeps its hold. Acting on a branch's hold puts it in the push's fields and sets the
// other one aside; once it lifts, the one aside comes back; a hold recorded before, its branch named or not, reads as
// it was, without migration.
func TestPushHolds(t *testing.T) {
	t.Parallel()
	branches := func(p *planv1.WishPush) []string {
		var out []string
		for _, h := range PushHolds(p) {
			out = append(out, h.GetHeldBranch()+"="+h.GetHeld())
		}
		return out
	}
	for _, c := range []struct {
		name string
		p    *planv1.WishPush
		want []string
	}{
		{"none", nil, nil},
		{"lifted", &planv1.WishPush{ProjectId: "p"}, nil},
		{"before W35", &planv1.WishPush{Held: "at 1", FailedSha: "1"}, []string{"=at 1"}},
		{"W35", &planv1.WishPush{Held: "at 1", HeldBranch: "b1"}, []string{"b1=at 1"}},
	} {
		if got := branches(c.p); !slices.Equal(got, c.want) {
			t.Errorf("%s: holds %v, want %v", c.name, got, c.want)
		}
	}

	p := &planv1.WishPush{ProjectId: "p", Held: "at 2", HeldRuns: 2, CorrectedBy: "c2", Attempts: 1, FailedSha: "2", HeldBranch: "b2"}
	FocusPushHold(p, "b1", "")
	p.Held, p.HeldRuns, p.FailedSha = "at 1", p.GetHeldRuns()+1, "1"
	SettlePushHolds(p)
	if got, want := branches(p), []string{"b1=at 1", "b2=at 2"}; !slices.Equal(got, want) {
		t.Fatalf("holds %v, want %v", got, want)
	}
	if p.GetHeldRuns() != 1 || p.GetCorrectedBy() != "" || p.GetAttempts() != 0 {
		t.Errorf("b1's hold %v: it took b2's runs, correction or attempts", p)
	}
	if b2 := PushFor(p, "b2", "b1"); b2.GetHeldRuns() != 2 || b2.GetCorrectedBy() != "c2" || b2.GetAttempts() != 1 || b2.GetFailedSha() != "2" {
		t.Errorf("b2's hold %v, want it whole", b2)
	}
	if PushHeld(PushFor(p, "b3", "b1")) {
		t.Errorf("b3 is held: %v", PushFor(p, "b3", "b1"))
	}

	FocusPushHold(p, "b1", "")
	p.Held, p.HeldRuns, p.FailedSha = "", 0, ""
	SettlePushHolds(p)
	if got, want := branches(p), []string{"b2=at 2"}; !slices.Equal(got, want) || p.GetHolds() != nil || p.GetCorrectedBy() != "c2" {
		t.Errorf("after b1's lifts, holds %v, the push %v; want %v in its fields", got, p, want)
	}

	// A hold recorded before W35 names no branch: the one the harness finds goes aside under its name.
	old := &planv1.WishPush{Held: "at 2", FailedSha: "2"}
	FocusPushHold(old, "b1", "b2")
	old.Held = "at 1"
	SettlePushHolds(old)
	if got, want := branches(old), []string{"b1=at 1", "b2=at 2"}; !slices.Equal(got, want) {
		t.Errorf("holds %v, want %v", got, want)
	}
}
