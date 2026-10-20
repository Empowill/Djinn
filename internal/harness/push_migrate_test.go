package harness

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// oldPush is a push as a store written before W63 keeps it: the hold of the branch held last in its own fields, those
// of hold and held_branch, with unchecked, dead since W59, and a field of a later build that must stay.
func oldPush(t *testing.T, p *planv1.WishPush, hold *planv1.PushHold, branch string) *planv1.WishPush {
	t.Helper()
	f, err := proto.Marshal(hold.GetFailure())
	if err != nil {
		t.Fatal(err)
	}
	b := protowire.AppendString(protowire.AppendTag(nil, oldHeld, protowire.BytesType), hold.GetHeld())
	b = protowire.AppendVarint(protowire.AppendTag(b, oldHeldRuns, protowire.VarintType), uint64(hold.GetHeldRuns()))
	b = protowire.AppendVarint(protowire.AppendTag(b, 8, protowire.VarintType), 1)
	b = protowire.AppendString(protowire.AppendTag(b, oldCorrectedBy, protowire.BytesType), hold.GetCorrectedBy())
	b = protowire.AppendVarint(protowire.AppendTag(b, oldAttempts, protowire.VarintType), uint64(hold.GetAttempts()))
	b = protowire.AppendString(protowire.AppendTag(b, oldFailedSha, protowire.BytesType), hold.GetFailedSha())
	b = protowire.AppendBytes(protowire.AppendTag(b, oldFailure, protowire.BytesType), f)
	if branch != "" {
		b = protowire.AppendString(protowire.AppendTag(b, oldHeldBranch, protowire.BytesType), branch)
	}
	b = protowire.AppendString(protowire.AppendTag(b, 99, protowire.BytesType), "later")
	p.ProtoReflect().SetUnknown(b)
	return p
}

// TestMigratePushes (W63): a store written before keeps a push's hold in the push's own fields, and its push question
// without its branch. Recover moves the hold into holds, on the branch it named, else the one its run held, else the
// wish's integration branch; a question gets the integration branch, or under the per-azima strategy, whose questions
// name no branch Djinn can read, is withdrawn, to be asked again. Nothing is left in the old fields, and a field of a
// later build stays.
func TestMigratePushes(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	hold := &planv1.PushHold{
		Held: "at 0f2e0240, test: exit status 1", HeldRuns: 2, CorrectedBy: "c", Attempts: 1, FailedSha: "0f2e02401234",
		Failure: &planv1.IntegrationFailure{Reason: "test: exit status 1", Base: "0f2e02401234"},
	}
	for _, c := range []struct {
		name, named, run string // The branch the hold named (W35), and the one its run held.
		perAzima         bool
		wantBranch       string // Where the hold goes; "" for the wish's integration branch.
	}{
		{name: "named", named: "djinn/T2-second", wantBranch: "djinn/T2-second"},
		{name: "its run held", run: "djinn/T1-first", wantBranch: "djinn/T1-first"},
		{name: "before W35"},
		{name: "per azima, named", named: "djinn/T2-second", perAzima: true, wantBranch: "djinn/T2-second"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			in := integrating(t)
			if c.perAzima {
				in.setAzimaStrategy(t)
			}
			q := &planv1.Question{Id: store.NewID(), WishId: in.wishID, Code: "Q1", Text: "Push it?", CreateTime: timestamppb.Now()}
			p := &planv1.WishPush{ProjectId: in.projectID, QuestionId: q.GetId(), Holds: map[string]*planv1.PushHold{
				"djinn/T3-third": {Held: "at 1, lint", HeldRuns: 1},
			}}
			if c.run != "" {
				p.Run = &planv1.PushRun{Branch: c.run, Stage: planv1.PushStage_PUSH_STAGE_HELD}
			}
			oldPush(t, p, hold, c.named)
			if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
				wish, err := store.Get[*planv1.Wish](t.Context(), tx, in.wishID)
				if err != nil {
					return err
				}
				wish.Pushes = []*planv1.WishPush{p}
				for _, m := range []proto.Message{q, wish} {
					if err := tx.Journal(actorLocal, "test/old", m); err != nil {
						return err
					}
					if err := tx.Put(m); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			if err := in.h.migratePushes(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := in.pushState(t)
			want := c.wantBranch
			if want == "" {
				want = in.branch
			}
			if h := got.GetHolds()[want]; !proto.Equal(h, hold) {
				t.Errorf("the hold of %s %v; want %v", want, h, hold)
			}
			if h := got.GetHolds()["djinn/T3-third"]; h.GetHeld() != "at 1, lint" || len(got.GetHolds()) != 2 {
				t.Errorf("the holds %v; want djinn/T3-third's kept beside", got.GetHolds())
			}
			if rest := protowire.AppendString(protowire.AppendTag(nil, 99, protowire.BytesType), "later"); string(got.ProtoReflect().GetUnknown()) != string(rest) {
				t.Errorf("unknown fields %q left; want the later build's alone", got.ProtoReflect().GetUnknown())
			}
			asked, err := store.Get[*planv1.Question](t.Context(), in.db, q.GetId())
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case c.perAzima && (asked.GetWithdrawal() == nil || got.GetQuestionId() != ""):
				t.Errorf("per azima, %s withdrawal %v, the push waits on %q; want it withdrawn and let go", asked.GetCode(),
					asked.GetWithdrawal(), got.GetQuestionId())
			case !c.perAzima && (asked.GetWithdrawal() != nil || got.GetQuestionId() != q.GetId() || got.GetQuestionBranch() != in.branch):
				t.Errorf("%s withdrawal %v, the push waits on %q about %q; want it open, about %s", asked.GetCode(),
					asked.GetWithdrawal(), got.GetQuestionId(), got.GetQuestionBranch(), in.branch)
			}

			// Once moved, a second start finds nothing to move.
			before := in.pushState(t)
			if err := in.h.migratePushes(t.Context()); err != nil {
				t.Fatal(err)
			}
			if after := in.pushState(t); !proto.Equal(after, before) {
				t.Errorf("a second migration changed the push: %v; want %v", after, before)
			}
		})
	}
}

// The commits the push checks of the fixtures of testdata/oldpush were red at, made in a test repository by a fixed
// author at a fixed time (fixedCommits): their hashes are the ones the fixtures name.
const (
	redCommit   = "1fcf7b2fdfb476421cd997101c9def78b189cf90"
	movedCommit = "985328cbdb3eddf263adabbe64a64d1280bb5fad" // A commit of the developer's on redCommit.
)

// TestMigratePushes_OldStore (W65): the pushes of testdata/oldpush, each as a store of v0.2.0 or of the parent of W63
// wrote it, marshalled with that build's schema (gen.go there), are read by the store's own path at a start of Djinn,
// and Recover leaves each as its .want.txtpb says: every hold on its branch, the push question about the integration
// branch or, per azima, withdrawn; in a project gone from the store, which leaves the wish no integration branch there,
// the hold gone, never under a branch named "", and the question withdrawn (W66). A second start changes nothing.
func TestMigratePushes_OldStore(t *testing.T) {
	testx.Portable(t)
	t.Parallel()
	const question = "01a0f000-0000-7000-8000-000000000001" // The push question the fixtures name.
	for _, c := range []struct {
		fixture   string // testdata/oldpush/<build>/<case>
		perAzima  bool
		tips      map[string]string // The commit each azima's branch is at, by the azima's title: red or moved.
		gone      bool              // Whether the push's project is gone from the store: no integration branch there.
		withdrawn bool              // Whether the push question is withdrawn.
	}{
		{fixture: "v0.2.0/wish"},
		{fixture: "v0.2.0/wish-gone", gone: true, withdrawn: true},
		{fixture: "v0.2.0/wish-unheld"},
		{fixture: "v0.2.0/azima-run", perAzima: true},
		{fixture: "v0.2.0/azima-tip", perAzima: true, tips: map[string]string{"first": "moved", "second": "red"}, withdrawn: true},
		{fixture: "v0.2.0/azima-moved", perAzima: true, tips: map[string]string{"first": "moved"}},
		{fixture: "v0.2.0/azima-none", perAzima: true, tips: map[string]string{"first": "red", "second": "moved"}},
		{fixture: "1a219c84/wish-named"},
		{fixture: "1a219c84/azima-named", perAzima: true, withdrawn: true},
		{fixture: "1a219c84/azima-holds", perAzima: true},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			t.Parallel()
			old, err := os.ReadFile(filepath.Join("testdata", "oldpush", c.fixture+".binpb"))
			if err != nil {
				t.Fatal(err)
			}
			wantText, err := os.ReadFile(filepath.Join("testdata", "oldpush", c.fixture+".want.txtpb"))
			if err != nil {
				t.Fatal(err)
			}
			in := integrating(t)
			if c.perAzima {
				in.setAzimaStrategy(t)
				fixedCommits(t, in.repo)
				for i, title := range []string{"first", "second"} {
					a := in.azima(t, in.wishID, title)
					if name, want := azimaBranchName(a), fmt.Sprintf("djinn/T%d-%s", i+1, title); name != want {
						t.Fatalf("the azima %s's branch is %s; the fixtures name %s", title, name, want)
					}
					if tip, ok := c.tips[title]; ok {
						tip = map[string]string{"red": redCommit, "moved": movedCommit}[tip]
						if _, err := git(t.Context(), in.repo, "update-ref", "refs/heads/"+azimaBranchName(a), tip); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			q := &planv1.Question{Id: question, WishId: in.wishID, Code: "Q1", Text: "Push it?", CreateTime: timestamppb.Now()}
			if err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
				return errors.Join(tx.Journal(actorLocal, "test/old", q), tx.Put(q))
			}); err != nil {
				t.Fatal(err)
			}
			project := in.projectID
			if c.gone {
				project = store.NewID()
			}
			in.storeOldPush(t, old, project)

			in.restart(t)
			want := &planv1.WishPush{}
			r := strings.NewReplacer("@branch@", in.branch, "@project@", project)
			if err := prototext.Unmarshal([]byte(r.Replace(string(wantText))), want); err != nil {
				t.Fatal(err)
			}
			wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
			if err != nil {
				t.Fatal(err)
			}
			if got := pushState(wish, project); !proto.Equal(got, want) {
				t.Errorf("the push after the migration:\n%v\nunknown fields %q\nwant:\n%v", prototext.Format(got),
					got.ProtoReflect().GetUnknown(), prototext.Format(want))
			}
			asked, err := store.Get[*planv1.Question](t.Context(), in.db, question)
			if err != nil {
				t.Fatal(err)
			}
			if withdrawn := asked.GetWithdrawal() != nil; withdrawn != c.withdrawn {
				t.Errorf("%s withdrawn: %v; want %v", asked.GetCode(), withdrawn, c.withdrawn)
			}

			// Once moved, a second start finds nothing to move.
			before, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
			if err != nil {
				t.Fatal(err)
			}
			in.restart(t)
			after, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(after, before) {
				t.Errorf("a second start changed the wish's pushes: %v; want %v", after.GetPushes(), before.GetPushes())
			}
			if again, err := store.Get[*planv1.Question](t.Context(), in.db, question); err != nil || !proto.Equal(again, asked) {
				t.Errorf("a second start changed %s: %v (%v); want %v", asked.GetCode(), again, err, asked)
			}
		})
	}
}

// storeOldPush stops Djinn and writes old, the bytes of a push as the store of an older build wrote them, as the one
// push of the wish's row, with the id of its project, project, the fixture cannot know: a field appended, which
// protobuf reads as set. The rest of the wish is this build's.
func (in *integration) storeOldPush(t *testing.T, old []byte, project string) {
	t.Helper()
	wish, err := store.Get[*planv1.Wish](t.Context(), in.db, in.wishID)
	if err != nil {
		t.Fatal(err)
	}
	wish.Pushes = nil
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wish)
	if err != nil {
		t.Fatal(err)
	}
	fields := func(m proto.Message) protoreflect.FieldDescriptors { return m.ProtoReflect().Descriptor().Fields() }
	push := protowire.AppendTag(slices.Clone(old), fields(&planv1.WishPush{}).ByName("project_id").Number(), protowire.BytesType)
	push = protowire.AppendString(push, project)
	payload = protowire.AppendTag(payload, fields(wish).ByName("pushes").Number(), protowire.BytesType)
	payload = protowire.AppendBytes(payload, push)
	in.down()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(in.home, store.File)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.ExecContext(t.Context(), `UPDATE wish SET payload = ? WHERE id = ?`, payload, in.wishID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("%d wish rows written (%v); want 1", n, err)
	}
}

// fixedCommits makes redCommit and movedCommit in repo: empty trees, by a fixed author at a fixed time.
func fixedCommits(t *testing.T, repo string) {
	t.Helper()
	gitAt := func(args ...string) string {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Djinn", "GIT_AUTHOR_EMAIL=djinn@example.com",
			"GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_NAME=Djinn", "GIT_COMMITTER_EMAIL=djinn@example.com",
			"GIT_COMMITTER_DATE=1700000000 +0000")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	tree := gitAt("mktree")
	red := gitAt("commit-tree", "--no-gpg-sign", "-m", "red", tree)
	moved := gitAt("commit-tree", "--no-gpg-sign", "-p", red, "-m", "moved", tree)
	if red != redCommit || moved != movedCommit {
		t.Fatalf("the fixed commits are %s and %s; the fixtures name %s and %s", red, moved, redCommit, movedCommit)
	}
}
