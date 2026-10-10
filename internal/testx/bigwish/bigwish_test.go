package bigwish_test

import (
	"bytes"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx/bigwish"
)

// TestSizes: each size holds what it says, and the shape of a wish under way: azimas within azimas, most tasks done
// and part of one, the last ones running or waiting, a few decisions left open.
func TestSizes(t *testing.T) {
	for _, size := range bigwish.Sizes() {
		t.Run(size.Name, func(t *testing.T) {
			exp := bigwish.Make(size)
			kinds, status := map[planv1.TaskKind]int{}, map[planv1.TaskStatus]int{}
			partOf, nested := 0, 0
			for _, task := range exp.GetTasks() {
				kinds[task.GetKind()]++
				if task.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA {
					if task.GetPartOf() != "" {
						nested++
					}
					continue
				}
				status[task.GetStatus()]++
				if task.GetPartOf() != "" {
					partOf++
				}
			}
			if kinds[planv1.TaskKind_TASK_KIND_UNSPECIFIED] != size.Work || kinds[planv1.TaskKind_TASK_KIND_AZIMA] != size.Azimas {
				t.Errorf("tasks = %v, want %d work and %d azimas", kinds, size.Work, size.Azimas)
			}
			if nested == 0 || nested == size.Azimas {
				t.Errorf("%d azimas of %d are part of another, want some", nested, size.Azimas)
			}
			if partOf < size.Work*85/100 {
				t.Errorf("%d work tasks of %d are part of an azima", partOf, size.Work)
			}
			done, running, pending := status[planv1.TaskStatus_TASK_STATUS_DONE], status[planv1.TaskStatus_TASK_STATUS_RUNNING], status[planv1.TaskStatus_TASK_STATUS_PENDING]
			if done < size.Work*85/100 || running == 0 || pending == 0 {
				t.Errorf("work by status = %v", status)
			}

			decided := 0
			for _, q := range exp.GetQuestions() {
				if q.GetAnswer() != nil {
					decided++
				}
			}
			if len(exp.GetQuestions()) != size.Questions || decided != size.Decisions {
				t.Errorf("%d questions, %d decided; want %d and %d", len(exp.GetQuestions()), decided, size.Questions, size.Decisions)
			}
			if len(exp.GetBlocks()) != size.Blocks || len(exp.GetCommands()) != size.Commands {
				t.Errorf("%d blocks and %d commands, want %d and %d", len(exp.GetBlocks()), len(exp.GetCommands()), size.Blocks, size.Commands)
			}
			// Every task that ran has events, size.EventsPerTask of them on average, give or take a fifth.
			ran := size.Work - pending
			if n := len(exp.GetEvents()); n < ran*size.EventsPerTask*8/10 || n > ran*size.EventsPerTask*12/10 {
				t.Errorf("%d events for %d tasks that ran, want about %d each", n, ran, size.EventsPerTask)
			}
		})
	}
}

// TestDeterministic: a size gives the same bytes every time, and another size other bytes.
func TestDeterministic(t *testing.T) {
	a, err := bigwish.Data(bigwish.Real)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bigwish.Data(bigwish.Real)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two wishes of the same size differ")
	}
	small := bigwish.Real
	small.Work = 10
	if c, err := bigwish.Data(small); err != nil || bytes.Equal(a, c) {
		t.Errorf("another size gives the same bytes (%v)", err)
	}
	if _, err := bigwish.Named("x10"); err != nil {
		t.Error(err)
	}
	if _, err := bigwish.Named("huge"); err == nil {
		t.Error("an unknown size is accepted")
	}
}

// TestImport: the store takes the real size through WishService.ImportData, as djinn wish import does, and gives it
// back whole through WishService.Snapshot. With fewer events: the 27 000 of the real size take 4 s to import (a
// statement prepared for each Put), past what a unit test may take; their shape is the same at any count.
func TestImport(t *testing.T) {
	size := bigwish.Real
	size.EventsPerTask = 8
	ctx := t.Context()
	s, err := store.Open(ctx, "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	data, err := bigwish.Data(size)
	if err != nil {
		t.Fatal(err)
	}
	wishes := &plan.Wishes{Store: s}
	res, err := wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetWish().GetId() != bigwish.WishID || res.Msg.GetNote() != "" {
		t.Fatalf("import = %v", res.Msg)
	}
	snap, err := wishes.Snapshot(ctx, connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: bigwish.WishID}))
	if err != nil {
		t.Fatal(err)
	}
	got, want := snap.Msg.GetExport(), bigwish.Make(size)
	if len(got.GetTasks()) != len(want.GetTasks()) || len(got.GetEvents()) != len(want.GetEvents()) ||
		len(got.GetCommands()) != len(want.GetCommands()) {
		t.Fatalf("read back %d tasks, %d events, %d commands; want %d, %d, %d", len(got.GetTasks()), len(got.GetEvents()),
			len(got.GetCommands()), len(want.GetTasks()), len(want.GetEvents()), len(want.GetCommands()))
	}
	for i, q := range want.GetQuestions() {
		if !proto.Equal(got.GetQuestions()[i], q) {
			t.Fatalf("question %s read back as %v", q.GetCode(), got.GetQuestions()[i])
		}
	}
	for i, b := range want.GetBlocks() {
		if !proto.Equal(got.GetBlocks()[i], b) {
			t.Fatalf("block %d read back as %v", i, got.GetBlocks()[i])
		}
	}
	for i, e := range want.GetEvents() {
		if !proto.Equal(got.GetEvents()[i], e) {
			t.Fatalf("event %d read back as %v", i, got.GetEvents()[i])
		}
	}
	for i, c := range want.GetCommands() {
		if !proto.Equal(got.GetCommands()[i], c) {
			t.Fatalf("command %d read back as %v", i, got.GetCommands()[i])
		}
	}
	// A task keeps its code, title, azima and dependencies; an import only interrupts what ran elsewhere.
	byID := map[string]*planv1.Task{}
	for _, task := range got.GetTasks() {
		byID[task.GetId()] = task
	}
	for _, w := range want.GetTasks() {
		g := byID[w.GetId()]
		if g.GetCode() != w.GetCode() || g.GetTitle() != w.GetTitle() || g.GetPartOf() != w.GetPartOf() ||
			!slices.Equal(g.GetDependsOn(), w.GetDependsOn()) ||
			w.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING && g.GetStatus() != w.GetStatus() {
			t.Fatalf("task %s read back as %v", w.GetCode(), g)
		}
	}
}
