package harness

import (
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// TestCompareWithTheRunningDjinn: a build is named by the branch and wish it comes from, and compared with the commit
// the running Djinn was built from: the tasks merged since, by code and title, the commits it adds, and those it lacks
// when it comes from another branch (W468: feat/clement-port's build, installed over a Djinn of feat/wails-go).
func TestCompareWithTheRunningDjinn(t *testing.T) {
	testx.Portable(t)
	in := integrating(t)
	ctx := t.Context()
	project, err := store.Get[*planv1.Project](ctx, in.db, in.projectID)
	if err != nil {
		t.Fatal(err)
	}
	// Two wishes, each with its integration branch: feat/wails-go, and feat/clement-port cut from it.
	in.git(t, in.repo, "branch", "feat/wails-go")
	in.git(t, in.repo, "branch", "feat/clement-port")
	clement, err := in.wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{
		Title: "Clément's branches", ProjectIds: []string{in.projectID},
	}))
	if err != nil {
		t.Fatal(err)
	}
	clementID := clement.Msg.GetWish().GetId()
	for wish, branch := range map[string]string{in.wishID: "feat/wails-go", clementID: "feat/clement-port"} {
		if _, err := in.wishes.SetIntegration(ctx, connect.NewRequest(&planv1.WishServiceSetIntegrationRequest{
			WishId: wish, ProjectId: in.projectID, Branch: branch,
		})); err != nil {
			t.Fatal(err)
		}
	}
	// merge commits a task's work on its own branch, and merges it into into as the integration does.
	merge := func(wishID, code, into string) (work, merged string) {
		branch := code + "-work-0000000" + code[1:]
		in.git(t, in.repo, "switch", "--quiet", "-c", branch, into)
		writeFile(t, in.repo, "app/src/"+code+".txt", code+"\n")
		commitAll(t, in.repo, "Work of "+code)
		work = in.git(t, in.repo, "rev-parse", "HEAD")
		in.git(t, in.repo, "switch", "--quiet", into)
		in.git(t, in.repo, "merge", "--no-ff", "--quiet", "-m", "Merge branch '"+branch+"' into "+into, branch)
		if err := in.db.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal("local", "test", &planv1.Task{}); err != nil {
				return err
			}
			return tx.Put(&planv1.Task{
				Id: store.NewID(), WishId: wishID, ProjectId: in.projectID, Code: code, Title: "Title of " + code,
				Branch: branch, Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now(),
			})
		}); err != nil {
			t.Fatal(err)
		}
		return work, in.git(t, in.repo, "rev-parse", "HEAD")
	}
	_, running := merge(in.wishID, "W1", "feat/wails-go")
	_, ahead := merge(in.wishID, "W3", "feat/wails-go")
	work, side := merge(clementID, "W2", "feat/clement-port")
	in.git(t, in.repo, "switch", "--quiet", "feat/wails-go")
	in.h.version = "local-v0.1.0-3-g" + running[:8]

	b := Built{Branch: "feat/wails-go", Sha: ahead}
	in.h.compare(ctx, project, &b)
	if b.Base != running || !b.Running || !slices.Equal(b.Tasks, []string{"W3"}) || b.Summaries[0].Title != "Title of W3" ||
		!slices.Equal(b.Changes, []string{"Work of W3"}) || b.ChangesCount != 1 || b.LacksCount != 0 {
		t.Errorf("the running branch's build: %+v", b)
	}

	b, ok := in.h.DescribeVersion(ctx, "local-v0.1.0-5-g"+side[:8])
	if !ok || b.Sha != side || b.Branch != "feat/clement-port" || b.WishID != clementID || b.WishTitle != "Clément's branches" ||
		b.Running || !slices.Equal(b.Tasks, []string{"W2"}) || !slices.Equal(b.Changes, []string{"Work of W2"}) ||
		!slices.Equal(b.Lacks, []string{"Work of W1"}) || b.LacksCount != 1 {
		t.Errorf("the side branch's Djinn, ready: %+v, %v", b, ok)
	}
	// A commit no merge names: the branch whose history holds it.
	if b, ok := in.h.DescribeVersion(ctx, "local-"+work[:7]); !ok || b.Branch != "feat/clement-port" {
		t.Errorf("a task's commit: %+v, %v", b, ok)
	}
	// A release: its tag's commit.
	in.git(t, in.repo, "tag", "-a", "-m", "v0.2.0", "v0.2.0", side)
	if b, ok := in.h.DescribeVersion(ctx, "v0.2.0"); !ok || b.Sha != side || b.Branch != "feat/clement-port" {
		t.Errorf("a release: %+v, %v", b, ok)
	}
	// A version no project holds, or a development build: not named, nothing compared. CI of PR #2: local-g<commit>,
	// which go tool task install never stamps, was read as the tag refs/tags/local-g<commit>, which git 2.47 took for a
	// git describe name, the commit, and git 2.55 did not: it is no tag, whatever the git.
	for _, v := range []string{"v9.9.9", "local-g" + side[:8]} {
		if b, ok := in.h.DescribeVersion(ctx, v); ok {
			t.Errorf("%s, which names no tag and no commit of a checkout, is named: %+v", v, b)
		}
	}
	in.h.version = "dev"
	b = Built{Branch: "feat/wails-go", Sha: ahead, Tasks: []string{"W3"}}
	in.h.compare(ctx, project, &b)
	if b.Base != "" || !slices.Equal(b.Tasks, []string{"W3"}) {
		t.Errorf("a development build compares: %+v", b)
	}
}
