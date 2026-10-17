package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// askOther records q as a move question someone other than Djinn asked: a worker when it names a task, the lead
// otherwise, through the plan's Ask like `djinn question ask --move`.
func (in *integration) askOther(t *testing.T, q *planv1.Question) *planv1.Question {
	t.Helper()
	q.Id, q.WishId, q.Move, q.CreateTime = store.NewID(), in.wishID, true, timestamppb.New(in.clock())
	if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "ask", q); err != nil {
			return err
		}
		return plan.Ask(t.Context(), tx, q)
	}); err != nil {
		t.Fatal(err)
	}
	return q
}

// installs are the install commands Djinn ran or gated so far.
func (in *integration) installs() []string {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out []string
	for _, r := range append(append([]string(nil), in.runs...), in.gates...) {
		if strings.HasPrefix(r, "install ") {
			out = append(out, r)
		}
	}
	return out
}

// TestAnswerOtherMoveInstallsNothing replays the incident of 12/10/2026: a worker's move question about cmd.exe,
// whose context mentions the PowerShell installers and a build, answered yes, installed the integration branch's tip
// over the running Djinn. Neither a worker's nor the lead's move question installs anything, whatever its words.
func TestAnswerOtherMoveInstallsNothing(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")
	w4 := in.finished(t, "W4", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	tip := in.tip(t)

	worker := in.askOther(t, &planv1.Question{
		TaskId: w4.GetId(),
		Text: "P4 (W4): on a Windows machine, does a terminal line with quotes and a pipe now reach cmd.exe as " +
			"typed? Install the build and check.",
		Context: "The build " + short8(tip) + " quotes the line for cmd.exe; the PowerShell installers are unchanged.",
		Options: []string{"Yes", "No"},
	})
	lead := in.askOther(t, &planv1.Question{
		Text:    "Install and restart " + short8(tip) + "?",
		Context: "The installers changed.",
		Options: []string{"Install and restart", "Skip"},
	})
	for _, q := range []*planv1.Question{worker, lead} {
		for _, choice := range []planv1.Choice{planv1.Choice_CHOICE_A, planv1.Choice_CHOICE_YES} {
			if did := in.answer(t, q, choice); did != "Djinn noted move" {
				t.Errorf("answer %v to %q: Djinn did %q, want only 'Djinn noted move'", choice, q.GetText(), did)
			}
		}
	}
	if got := in.installs(); len(got) != 0 {
		t.Errorf("Djinn installed on an answer to a move it did not ask: %v", got)
	}
	if got := in.h.installedSha(t.Context(), in.projectID); got != "" {
		t.Errorf("installed build %q, want none", got)
	}
}

// TestAnswerOtherMovePushesNoTag: a worker's move question naming a local tag, answered yes, pushes no tag, and one
// mentioning a release starts none; Djinn's own tag question, answered yes, pushes the tag it named.
func TestAnswerOtherMovePushesNoTag(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	bare := in.remote(t)
	w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"})
	in.pass(t, 0)
	in.git(t, in.repo, "tag", "v9.9.9")

	worker := in.askOther(t, &planv1.Question{
		TaskId:  w1.GetId(),
		Text:    "Push tag v9.9.9 to origin, then start the release?",
		Context: "Run `git push origin v9.9.9` to push tag v9.9.9 before the release.",
		Options: []string{"Push tag v9.9.9", "Skip"},
	})
	if did := in.answer(t, worker, planv1.Choice_CHOICE_A); did != "Djinn noted move" {
		t.Errorf("answer to the worker's tag question: Djinn did %q, want only 'Djinn noted move'", did)
	}
	if got := in.git(t, bare, "tag", "--list"); got != "" {
		t.Fatalf("tags on the remote after a worker's tag question: %q, want none", got)
	}

	own := tagMoveQuestion(in.wishID, in.projectID, "v9.9.9", "origin")
	if err := in.h.askMove(t.Context(), own); err != nil {
		t.Fatal(err)
	}
	if did := in.answer(t, own, planv1.Choice_CHOICE_A); did != "Djinn pushed tag v9.9.9" {
		t.Errorf("answer to Djinn's tag question: Djinn did %q, want 'Djinn pushed tag v9.9.9'", did)
	}
	if got := in.git(t, bare, "tag", "--list"); got != "v9.9.9" {
		t.Errorf("tags on the remote after Djinn's tag question: %q, want v9.9.9", got)
	}
}

// TestAnswerOwnInstallInstallsItsBuild: Djinn's own install question, answered yes, installs the build it named,
// even once the integration branch moved past it.
func TestAnswerOwnInstallInstallsItsBuild(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	in := integrating(t)
	writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
		"generate: \"gen\"\ntest: \"test\"\ninstall: \"install\"\n")
	in.finished(t, "W1", map[string]string{"app/src/a.txt": "v1\n"})
	in.pass(t, 0)
	named := in.tip(t)
	in.finished(t, "W2", map[string]string{"app/src/b.txt": "v2\n"})
	in.pass(t, 0)
	if in.tip(t) == named {
		t.Fatal("the integration branch did not move past the named build")
	}
	q := installMoveQuestion(in.wishID, in.projectID, named, in.branch, 10*time.Minute)
	if err := in.h.askMove(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	if did, want := in.answer(t, q, planv1.Choice_CHOICE_A), "Djinn installed build "+short8(named); did != want {
		t.Errorf("Djinn did %q, want %q", did, want)
	}
	if got := in.h.installedSha(t.Context(), in.projectID); got != named {
		t.Errorf("installed build %q, want the one the question named, %q", got, named)
	}
}
