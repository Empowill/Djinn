package plan

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

func putInstructionTask(t *testing.T, s *store.Store, task *planv1.Task) {
	t.Helper()
	if err := s.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, "test/task", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		t.Fatal(err)
	}
}

func sendInstruction(t *testing.T, c clients, wish, text string) *planv1.Instruction {
	t.Helper()
	res, err := c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish, Text: text}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetInstruction()
}

func replyInstruction(t *testing.T, c clients, ref, wish, text string) *planv1.Instruction {
	t.Helper()
	res, err := c.instructions.Reply(t.Context(), connect.NewRequest(&planv1.InstructionServiceReplyRequest{
		Instruction: ref, WishId: wish, Reply: text,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetInstruction()
}

func instructionsOf(t *testing.T, c clients, wish string) []*planv1.Instruction {
	t.Helper()
	res, err := c.instructions.List(t.Context(), connect.NewRequest(&planv1.InstructionServiceListRequest{WishId: wish}))
	if err != nil {
		t.Fatal(err)
	}
	return res.Msg.GetInstructions()
}

func reflectInstruction(t *testing.T, c clients, instruction *planv1.Instruction) {
	t.Helper()
	_, err := c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: instruction.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
}

func assignInstruction(t *testing.T, c clients, instruction *planv1.Instruction, task *planv1.Task) {
	t.Helper()
	_, err := c.instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{Instruction: instruction.GetCode(), WishId: instruction.GetWishId(), Task: task.GetCode()}))
	if err != nil {
		t.Fatal(err)
	}
}

func journalLength(t *testing.T, s *store.Store) int {
	t.Helper()
	commands, err := store.Commands(t.Context(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(commands)
}

func TestInstructionLifecycleAndReady(t *testing.T) {
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	task := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W1", Title: "Implement", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now()}
	wrong := &planv1.Task{Id: store.NewID(), WishId: other, Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now()}
	putInstructionTask(t, c.store, task)
	putInstructionTask(t, c.store, wrong)
	assertReady := func(want bool) {
		t.Helper()
		snap, err := c.wishes.Snapshot(t.Context(), connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: wish}))
		if err != nil {
			t.Fatal(err)
		}
		if snap.Msg.GetExport().GetWish().GetReady() != want {
			t.Fatalf("snapshot ready=%v, want %v", snap.Msg.GetExport().GetWish().GetReady(), want)
		}
		listed, err := c.wishes.List(t.Context(), connect.NewRequest(&planv1.WishServiceListRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range listed.Msg.GetWishes() {
			if w.GetId() == wish && w.GetReady() != want {
				t.Errorf("list ready=%v, want %v", w.GetReady(), want)
			}
		}
	}
	assertReady(true)
	stream := watch(t, c.wishes, wish)
	full := strings.Repeat("Préciser tous les cas, sans perdre de texte.\n", 100) + "LAST LINE <script>"
	instruction := sendInstruction(t, c, wish, full)
	if instruction.GetCode() != "I01" || instruction.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING || instruction.GetTaskId() != "" || instruction.GetText() != full {
		t.Fatalf("new instruction: %v", instruction)
	}
	if msg := next(t, stream); msg.GetWishId() != wish || !slices.Equal(msg.GetChanges(), []planv1.Change{planv1.Change_CHANGE_WISH, planv1.Change_CHANGE_INSTRUCTION}) {
		t.Fatalf("instruction watch: %v", msg)
	}
	assertReady(false)
	brief, err := c.wishes.Brief(t.Context(), connect.NewRequest(&planv1.WishServiceBriefRequest{WishId: wish}))
	if err != nil || !strings.Contains(brief.Msg.GetText(), full) {
		t.Fatalf("brief lost instruction: %v", err)
	}
	before := journalLength(t, c.store)
	_, err = c.instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{Instruction: instruction.GetId(), Task: task.GetId()}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("assign pending: %v", err)
	}
	_, err = c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: instruction.GetId()}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("complete pending: %v", err)
	}
	_, err = c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: instruction.GetId(), WishId: other}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong wish instruction: %v", err)
	}
	if journalLength(t, c.store) != before {
		t.Fatal("invalid transitions left journal entries")
	}
	reflected, err := c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: "i01", WishId: wish}))
	if err != nil || reflected.Msg.GetInstruction().GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_REFLECTING {
		t.Fatalf("reflect: %v", err)
	}
	reflectInstruction(t, c, instruction) // Safe retry.
	before = journalLength(t, c.store)
	_, err = c.instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{Instruction: instruction.GetId(), Task: wrong.GetId()}))
	if code(err) != connect.CodeInvalidArgument || journalLength(t, c.store) != before {
		t.Fatalf("wrong wish worker: %v", err)
	}
	assignInstruction(t, c, instruction, task)
	if got := instructionsOf(t, c, wish)[0]; got.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING || got.GetTaskId() != task.GetId() {
		t.Fatalf("assigned: %v", got)
	}
	processingReply := replyInstruction(t, c, instruction.GetId(), "", "Processing update")
	if processingReply.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_PROCESSING || processingReply.GetTaskId() != task.GetId() {
		t.Fatalf("processing reply changed lifecycle: %v", processingReply)
	}
	for _, status := range []planv1.TaskStatus{planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING, planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_PAUSED} {
		task.Status = status
		putInstructionTask(t, c.store, task)
		before = journalLength(t, c.store)
		_, err := c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: instruction.GetId()}))
		if code(err) != connect.CodeFailedPrecondition || journalLength(t, c.store) != before {
			t.Fatalf("complete with task %v: %v", status, err)
		}
	}
	task.Status = planv1.TaskStatus_TASK_STATUS_DONE
	putInstructionTask(t, c.store, task)
	assertReady(false) // Successful worker never completes the instruction automatically.
	done, err := c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: "I01", WishId: wish}))
	if err != nil || done.Msg.GetInstruction().GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_DONE || done.Msg.GetInstruction().GetTaskId() != task.GetId() {
		t.Fatalf("complete: %v", err)
	}
	assertReady(true)
	again, err := c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: instruction.GetId()}))
	if err != nil || !proto.Equal(done.Msg.GetInstruction(), again.Msg.GetInstruction()) {
		t.Fatal("completion retry changed result")
	}
	doneReply := replyInstruction(t, c, instruction.GetId(), "", "Done update")
	if doneReply.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_DONE || doneReply.GetTaskId() != task.GetId() {
		t.Fatalf("done reply changed lifecycle: %v", doneReply)
	}
	_, err = c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: instruction.GetId()}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("reflect done: %v", err)
	}
	// Codes are scoped to a wish; UUIDs stay unambiguous.
	sendInstruction(t, c, other, "Other wish")
	_, err = c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: "I01"}))
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ambiguous code: %v", err)
	}
}

func TestInstructionRepliesValidationOrderingAndConcurrency(t *testing.T) {
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	instruction := sendInstruction(t, c, wish, "Keep this instruction pending")
	direct := &Instructions{Store: c.store}
	if _, err := direct.Reply(t.Context(), nil); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("nil reply request: %v", err)
	}
	bad := []*planv1.InstructionServiceReplyRequest{
		{Reply: "Missing reference"},
		{Instruction: instruction.GetId()},
		{Instruction: "garbage", Reply: "Malformed reference"},
		{Instruction: instruction.GetId(), Reply: " \n\t"},
		{Instruction: instruction.GetId(), Reply: strings.Repeat("x", 2001)},
		{Instruction: instruction.GetId(), WishId: "garbage", Reply: "Malformed wish"},
	}
	for _, req := range bad {
		before := journalLength(t, c.store)
		if _, err := direct.Reply(t.Context(), connect.NewRequest(req)); code(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid reply request %v: %v", req, err)
		}
		if journalLength(t, c.store) != before || len(instructionsOf(t, c, wish)[0].GetReplies()) != 0 {
			t.Fatalf("invalid reply request changed durable state: %v", req)
		}
	}
	before := journalLength(t, c.store)
	if _, err := direct.Reply(t.Context(), connect.NewRequest(&planv1.InstructionServiceReplyRequest{
		Instruction: instruction.GetId(), WishId: other, Reply: "Wrong wish",
	})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong-wish reply: %v", err)
	}
	if journalLength(t, c.store) != before {
		t.Fatal("wrong-wish reply left a journal entry")
	}
	for _, req := range []*planv1.InstructionServiceReplyRequest{
		{Instruction: store.NewID(), Reply: "Missing UUID"},
		{Instruction: "I99", WishId: wish, Reply: "Missing code"},
		{Instruction: "I01", WishId: other, Reply: "Code in wrong wish"},
	} {
		before := journalLength(t, c.store)
		if _, err := direct.Reply(t.Context(), connect.NewRequest(req)); code(err) != connect.CodeNotFound {
			t.Fatalf("missing reply target %v: %v", req, err)
		}
		if journalLength(t, c.store) != before {
			t.Fatalf("missing reply target left a journal entry: %v", req)
		}
	}
	otherInstruction := sendInstruction(t, c, other, "Other wish instruction")
	if otherInstruction.GetCode() != "I01" {
		t.Fatalf("other wish code = %s", otherInstruction.GetCode())
	}
	before = journalLength(t, c.store)
	if _, err := direct.Reply(t.Context(), connect.NewRequest(&planv1.InstructionServiceReplyRequest{Instruction: "I01", Reply: "Ambiguous code"})); code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ambiguous reply code: %v", err)
	}
	if journalLength(t, c.store) != before {
		t.Fatal("ambiguous reply code left a journal entry")
	}

	status, taskID := instruction.GetStatus(), instruction.GetTaskId()
	got := replyInstruction(t, c, "i01", wish, "First reply")
	if got.GetStatus() != status || got.GetTaskId() != taskID || len(got.GetReplies()) != 1 || got.GetReplies()[0].GetText() != "First reply" {
		t.Fatalf("first reply changed lifecycle or was not appended: %v", got)
	}
	got = replyInstruction(t, c, instruction.GetId(), "", "Second reply")
	if len(got.GetReplies()) != 2 || got.GetReplies()[1].GetText() != "Second reply" {
		t.Fatalf("UUID reply did not preserve earlier reply: %v", got.GetReplies())
	}
	commands, err := store.Commands(t.Context(), c.store, func(command store.Command) bool {
		return command.Method == planv1connect.InstructionServiceReplyProcedure
	})
	if err != nil || len(commands) != 2 {
		t.Fatalf("reply journal entries = %d, %v", len(commands), err)
	}
	var journalRequest planv1.InstructionServiceReplyRequest
	if err := proto.Unmarshal(commands[0].Request, &journalRequest); err != nil || journalRequest.GetInstruction() != instruction.GetId() || journalRequest.GetWishId() != wish {
		t.Fatalf("reply journal did not canonicalize IDs: %v, %v", journalRequest.GetInstruction(), journalRequest.GetWishId())
	}

	const concurrent = 16
	errs := make(chan error, concurrent)
	var wg sync.WaitGroup
	for n := range concurrent {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := direct.Reply(t.Context(), connect.NewRequest(&planv1.InstructionServiceReplyRequest{
				Instruction: instruction.GetId(), Reply: fmt.Sprintf("Concurrent reply %02d", n),
			}))
			if err != nil {
				errs <- err
			}
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	listed := instructionsOf(t, c, wish)
	if len(listed) != 1 || len(listed[0].GetReplies()) != concurrent+2 {
		t.Fatalf("concurrent replies lost an append: %d", len(listed[0].GetReplies()))
	}
	var previous time.Time
	for n, reply := range listed[0].GetReplies() {
		if err := reply.GetCreateTime().CheckValid(); err != nil {
			t.Fatalf("reply %d timestamp invalid: %v", n, err)
		}
		at := reply.GetCreateTime().AsTime()
		if n > 0 && at.Before(previous) {
			t.Fatalf("reply timestamps are out of order at %d", n)
		}
		previous = at
	}
}

func TestInstructionAtomicRetriesAndOrdinarySubmissions(t *testing.T) {
	c := serve(t)
	wish, other := c.wish(t), c.wish(t)
	id := store.NewID()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish, Text: "Retry me", RequestId: id}))
			if err != nil {
				errs <- err
				return
			}
			ids <- res.Msg.GetInstruction().GetId()
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	first := ""
	for got := range ids {
		if first == "" {
			first = got
		}
		if got != first {
			t.Error("concurrent retries duplicated")
		}
	}
	if got := instructionsOf(t, c, wish); len(got) != 1 {
		t.Fatalf("retry created %d instructions", len(got))
	}
	before := journalLength(t, c.store)
	_, err := c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish, Text: "Different", RequestId: id}))
	if code(err) != connect.CodeAlreadyExists || journalLength(t, c.store) != before {
		t.Fatalf("retry conflict: %v", err)
	}
	_, err = c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: other, Text: "Separate wish", RequestId: id}))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Second", "Third"} {
		sendInstruction(t, c, wish, text)
	}
	got := instructionsOf(t, c, wish)
	if len(got) != 3 || got[1].GetCode() != "I02" || got[2].GetCode() != "I03" {
		t.Fatalf("ordinary empty retry identifiers: %v", got)
	}
	for _, bad := range []*planv1.InstructionServiceSendRequest{{WishId: wish, Text: " \n\t"}, {WishId: store.NewID(), Text: "Unknown wish"}, {WishId: wish, Text: "Invalid UUID", RequestId: "bad"}} {
		before := journalLength(t, c.store)
		if _, err := c.instructions.Send(t.Context(), connect.NewRequest(bad)); err == nil || journalLength(t, c.store) != before {
			t.Fatalf("invalid submission journaled: %v", bad)
		}
	}
}

// instructionRecordingLeads verifies that a provider-neutral wakeup can read the committed instruction and journal.
type instructionRecordingLeads struct {
	fakeLeads
	onTell func(string, string)
}

func (f *instructionRecordingLeads) Tell(name, text string) (planv1.TellWait, error) {
	if f.onTell != nil {
		f.onTell(name, text)
	}
	return f.fakeLeads.Tell(name, text)
}

func TestInstructionWakeupAfterCommitAndTellCompatibility(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "agy"} {
		t.Run(provider, func(t *testing.T) {
			leads := &instructionRecordingLeads{}
			c := serve(t, WithLeads(leads))
			wish := c.wish(t)
			leads.running = map[string][]string{LeadTerminal(wish): {provider}}
			calls := 0
			leads.onTell = func(name, text string) {
				calls++
				got := instructionsOf(t, c, wish)
				if len(got) != 1 {
					t.Fatal("notification before instruction commit")
				}
				commands, err := store.Commands(t.Context(), c.store, func(cmd store.Command) bool { return cmd.Method == planv1connect.InstructionServiceSendProcedure })
				if err != nil || len(commands) != 1 || commands[0].ID != got[0].GetId() {
					t.Fatalf("notification before atomic journal commit: %v", err)
				}
				for _, part := range []string{got[0].GetId(), "I01", "instruction list", "instruction reflect", "instruction assign", "Never implement source or test changes yourself", "instruction complete"} {
					if !strings.Contains(text, part) {
						t.Errorf("wakeup missing %q", part)
					}
				}
			}
			retry := store.NewID()
			for range 2 {
				if _, err := c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish, Text: "Developer request", RequestId: retry})); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 || len(leads.told) != 1 {
				t.Fatal("retry notified twice")
			}
			leads.onTell = nil
			_, err := c.wishes.Tell(t.Context(), connect.NewRequest(&planv1.WishServiceTellRequest{WishId: wish, Text: "Worker report"}))
			if err != nil || len(instructionsOf(t, c, wish)) != 1 {
				t.Fatalf("generic Tell became an instruction: %v", err)
			}
		})
	}
}

func TestInstructionOfflineRestart(t *testing.T) {
	ctx := t.Context()
	file := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, file, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	w := &Wishes{Store: s}
	made, err := w.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Offline"}))
	if err != nil {
		t.Fatal(err)
	}
	wish := made.Msg.GetWish().GetId()
	i := &Instructions{Store: s, Leads: &fakeLeads{}}
	full := strings.Repeat("Long offline instruction.\n", 500) + "FINAL REQUIREMENT"
	req := connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish, Text: full, RequestId: store.NewID()})
	// Direct invocation still exercises persistent storage and a missing lead.
	got, err := i.Send(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	replied, err := i.Reply(ctx, connect.NewRequest(&planv1.InstructionServiceReplyRequest{
		Instruction: got.Msg.GetInstruction().GetId(), Reply: "Persist this reply too",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, file, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listed, err := (&Instructions{Store: s}).List(ctx, connect.NewRequest(&planv1.InstructionServiceListRequest{WishId: wish}))
	if err != nil || len(listed.Msg.GetInstructions()) != 1 || !proto.Equal(replied.Msg.GetInstruction(), listed.Msg.GetInstructions()[0]) {
		t.Fatalf("restart lost instruction: %v", err)
	}
	brief, err := BuildBrief(ctx, s, "", wish)
	if err != nil || !strings.Contains(brief.Moving, full) {
		t.Fatalf("restart brief truncated: %v", err)
	}
}

func TestInstructionReplyTimestampBoundsAfterImport(t *testing.T) {
	c := serve(t)
	wishID := store.NewID()
	now := time.Now().UTC()
	future := timestamppb.New(now.Add(time.Hour))
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	maxTimestamp := timestamppb.New(maxTime)
	exp := &planv1.WishExport{
		Version: formatVersion,
		Wish:    &planv1.Wish{Id: wishID, Title: "Imported timestamps", State: planv1.WishState_WISH_STATE_ACTIVE, CreateTime: timestamppb.New(now)},
		Instructions: []*planv1.Instruction{
			{Id: store.NewID(), WishId: wishID, Code: "I01", Text: "Future reply", CreateTime: timestamppb.New(now), UpdateTime: future, Status: planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
				Replies: []*planv1.InstructionReply{{Text: "Imported future", CreateTime: future}}},
			{Id: store.NewID(), WishId: wishID, Code: "I02", Text: "Maximum reply", CreateTime: timestamppb.New(now), UpdateTime: maxTimestamp, Status: planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
				Replies: []*planv1.InstructionReply{{Text: "Imported maximum", CreateTime: maxTimestamp}}},
		},
	}
	data, err := protojson.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.wishes.ImportData(t.Context(), connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
		t.Fatal(err)
	}
	futureReply := replyInstruction(t, c, "I01", "", "After future import")
	if len(futureReply.GetReplies()) != 2 || !futureReply.GetReplies()[1].GetCreateTime().AsTime().After(future.AsTime()) {
		t.Fatalf("reply after future import was not monotonic: %v", futureReply.GetReplies())
	}
	maxReply := replyInstruction(t, c, "I02", "", "After maximum import")
	if len(maxReply.GetReplies()) != 2 || maxReply.GetReplies()[1].GetCreateTime().CheckValid() != nil || !proto.Equal(maxReply.GetReplies()[1].GetCreateTime(), maxTimestamp) {
		t.Fatalf("maximum timestamp reply became invalid or reordered: %v", maxReply.GetReplies())
	}
}

func TestInstructionExportImportAndValidation(t *testing.T) {
	c := serve(t)
	wish := c.wish(t)
	task := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W1", Title: "Worker", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now()}
	putInstructionTask(t, c.store, task)
	home, _ := os.UserHomeDir()
	text := "Read " + filepath.Join(home, "notes.md") + " https://name:secret@example.com/ref\nFINAL REQUIREMENT"
	first := sendInstruction(t, c, wish, text)
	replyText := "Reply at " + filepath.Join(home, "reply.md") + " https://name:replysecret@example.com/ref\nFINAL REPLY"
	replyInstruction(t, c, first.GetId(), "", replyText)
	reflectInstruction(t, c, first)
	assignInstruction(t, c, first, task)
	// Include pending and done alongside processing.
	sendInstruction(t, c, wish, "Still pending")
	third := sendInstruction(t, c, wish, "Done content")
	reflectInstruction(t, c, third)
	assignInstruction(t, c, third, task)
	if _, err := c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: third.GetId()})); err != nil {
		t.Fatal(err)
	}
	exp, _, err := collect(t.Context(), c.store, wish)
	if err != nil {
		t.Fatal(err)
	}
	portableExp := portable(exp, newScrubber(nil, ""))
	if len(portableExp.GetInstructions()) != 3 || strings.Contains(portableExp.GetInstructions()[0].GetText(), home) || strings.Contains(portableExp.GetInstructions()[0].GetText(), "secret") || !strings.Contains(portableExp.GetInstructions()[0].GetText(), "FINAL REQUIREMENT") {
		t.Fatal("portable text was lost or not scrubbed")
	}
	if replies := portableExp.GetInstructions()[0].GetReplies(); len(replies) != 1 || strings.Contains(replies[0].GetText(), home) || strings.Contains(replies[0].GetText(), "replysecret") || !strings.Contains(replies[0].GetText(), "FINAL REPLY") {
		t.Fatalf("portable reply was lost or not scrubbed: %v", replies)
	}
	var reflected, assigned, replied, replyScrubbed bool
	for _, cmd := range exp.GetCommands() {
		if cmd.GetMethod() == planv1connect.InstructionServiceReflectProcedure {
			m, _ := cmd.GetRequest().UnmarshalNew()
			if m.(*planv1.InstructionServiceReflectRequest).GetInstruction() == first.GetId() {
				reflected = true
			}
		}
		if cmd.GetMethod() == planv1connect.InstructionServiceAssignProcedure {
			m, _ := cmd.GetRequest().UnmarshalNew()
			if m.(*planv1.InstructionServiceAssignRequest).GetInstruction() == first.GetId() && m.(*planv1.InstructionServiceAssignRequest).GetTask() == task.GetId() {
				assigned = true
			}
		}
		if cmd.GetMethod() == planv1connect.InstructionServiceReplyProcedure {
			m, _ := cmd.GetRequest().UnmarshalNew()
			r := m.(*planv1.InstructionServiceReplyRequest)
			if r.GetInstruction() == first.GetId() && strings.Contains(r.GetReply(), "FINAL REPLY") {
				replied = true
			}
		}
	}
	for _, cmd := range portableExp.GetCommands() {
		if cmd.GetMethod() != planv1connect.InstructionServiceReplyProcedure {
			continue
		}
		m, _ := cmd.GetRequest().UnmarshalNew()
		r := m.(*planv1.InstructionServiceReplyRequest)
		if r.GetInstruction() == first.GetId() && strings.Contains(r.GetReply(), "FINAL REPLY") && !strings.Contains(r.GetReply(), "replysecret") && !strings.Contains(r.GetReply(), home) {
			replyScrubbed = true
		}
	}
	if !reflected || !assigned || !replied || !replyScrubbed {
		t.Fatal("canonical instruction journal missing from export")
	}
	dst := serve(t)
	data, err := protojson.Marshal(portableExp)
	if err != nil {
		t.Fatal(err)
	}
	for _, replace := range []bool{false, true} {
		_, err := dst.wishes.ImportData(t.Context(), connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data, Replace: replace}))
		if err != nil {
			t.Fatal(err)
		}
		got := instructionsOf(t, dst, wish)
		if len(got) != 3 || !proto.Equal(got[0], portableExp.GetInstructions()[0]) || got[2].GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_DONE {
			t.Fatal("import/replace lost lifecycle")
		}
	}
	for name, mutate := range map[string]func(*planv1.WishExport){
		"done with unsuccessful worker": func(e *planv1.WishExport) { e.Tasks[0].Status = planv1.TaskStatus_TASK_STATUS_FAILED },
		"missing worker":                func(e *planv1.WishExport) { e.Instructions[0].TaskId = "" },
		"unknown worker":                func(e *planv1.WishExport) { e.Instructions[0].TaskId = store.NewID() },
		"wrong wish":                    func(e *planv1.WishExport) { e.Instructions[0].WishId = store.NewID() },
		"duplicate code":                func(e *planv1.WishExport) { e.Instructions[1].Code = e.Instructions[0].Code },
		"unknown status":                func(e *planv1.WishExport) { e.Instructions[0].Status = 999 },
		"blank text":                    func(e *planv1.WishExport) { e.Instructions[0].Text = " \n" },
		"blank reply":                   func(e *planv1.WishExport) { e.Instructions[0].Replies[0].Text = " \n" },
		"missing reply timestamp":       func(e *planv1.WishExport) { e.Instructions[0].Replies[0].CreateTime = nil },
		"out of order replies": func(e *planv1.WishExport) {
			e.Instructions[0].Replies = append(e.Instructions[0].Replies, &planv1.InstructionReply{Text: "Earlier", CreateTime: timestamppb.New(time.Unix(0, 0))})
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := proto.Clone(portableExp).(*planv1.WishExport)
			mutate(bad)
			if check(bad) == nil {
				t.Fatal("invalid imported instruction accepted")
			}
		})
	}
	invalid := proto.Clone(first).(*planv1.Instruction)
	invalid.Status = planv1.InstructionStatus_INSTRUCTION_STATUS_DONE
	if err := protovalidate.Validate(invalid); err == nil {
		t.Fatal("done without worker accepted by proto")
	}
}

func TestInstructionSyncedPage(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, pages := servePages(t, t.TempDir())
	go pages.Run(ctx)
	wish := c.wish(t)
	file, err := pages.Sync(ctx, wish)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.blocks.Put(ctx, connect.NewRequest(&planv1.BlockServicePutRequest{WishId: wish, Kind: "section", Title: "Notes before instructions", Content: "Keep the notes section first."})); err != nil {
		t.Fatal(err)
	}
	text := "Unique instruction content <script>alert(1)</script>\nLAST FULL LINE"
	instruction := sendInstruction(t, c, wish, text)
	eventually(t, file, html.EscapeString(text))
	reflectInstruction(t, c, instruction)
	task := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now()}
	putInstructionTask(t, c.store, task)
	assignInstruction(t, c, instruction, task)
	eventually(t, file, "Worker W1")
	if _, err := c.instructions.Complete(t.Context(), connect.NewRequest(&planv1.InstructionServiceCompleteRequest{Instruction: instruction.GetId()})); err != nil {
		t.Fatal(err)
	}
	eventually(t, file, "Done")
	// The page is a full snapshot, not a clipped brief, and the text cannot execute HTML.
	page, err := pages.Page(ctx, wish)
	if err != nil {
		t.Fatal(err)
	}
	if notes, instructions := strings.Index(string(page), `<section id="notes">`), strings.Index(string(page), `<section id="instructions">`); notes < 0 || instructions <= notes {
		t.Fatal("rendered instructions must follow notes")
	}
	if notes, instructions := strings.Index(string(page), `href="#notes"`), strings.Index(string(page), `href="#instructions"`); notes < 0 || instructions <= notes {
		t.Fatal("instruction contents link must follow notes")
	}
	if strings.Contains(string(page), "<script>alert(1)</script>") || !strings.Contains(string(page), "LAST FULL LINE") {
		t.Fatal("page lost or executed developer text")
	}
}

func TestInstructionChronologyAfterImport(t *testing.T) {
	c := serve(t)
	wish := &planv1.Wish{Id: store.NewID(), Title: "Imported chronology", State: planv1.WishState_WISH_STATE_ACTIVE}
	exp := &planv1.WishExport{Version: formatVersion, Wish: wish}
	// Imported UUIDs deliberately sort in reverse code order. Equal timestamps exercise I01/I02 and I99/I100.
	for n, code := range []string{"I100", "I99", "I10", "I02", "I01"} {
		at := timestamppb.New(time.Unix(200, 0))
		exp.Instructions = append(exp.Instructions, &planv1.Instruction{Id: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000004", "00000000-0000-4000-8000-000000000005"}[n], WishId: wish.GetId(), Code: code, Text: code + " full content", CreateTime: at, UpdateTime: at, Status: planv1.InstructionStatus_INSTRUCTION_STATUS_PENDING})
	}
	data, err := protojson.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.wishes.ImportData(t.Context(), connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
		t.Fatal(err)
	}
	sent := sendInstruction(t, c, wish.GetId(), "Latest content")
	if sent.GetCode() != "I101" {
		t.Fatalf("next imported code = %s", sent.GetCode())
	}
	assertOrder := func(instructions []*planv1.Instruction) {
		t.Helper()
		var codes []string
		for _, instruction := range instructions {
			codes = append(codes, instruction.GetCode())
		}
		if !slices.Equal(codes, []string{"I01", "I02", "I10", "I99", "I100", "I101"}) {
			t.Fatalf("chronology: %v", codes)
		}
	}
	assertOrder(instructionsOf(t, c, wish.GetId()))
	snap, err := c.wishes.Snapshot(t.Context(), connect.NewRequest(&planv1.WishServiceSnapshotRequest{WishId: wish.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(snap.Msg.GetExport().GetInstructions())
	collected, _, err := collect(t.Context(), c.store, wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(collected.GetInstructions())
	page, err := NewPages(c.store, t.TempDir(), "test").Page(t.Context(), wish.GetId())
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, code := range []string{"I01", "I02", "I10", "I99", "I100", "I101"} {
		pos := strings.Index(string(page), `class="code">`+code+`</span>`)
		if pos < previous || pos < 0 {
			t.Fatalf("rendered chronology at %s", code)
		}
		previous = pos
	}
}

func TestInstructionCodeBoundaryAtSameTimestamp(t *testing.T) {
	at := timestamppb.New(time.Unix(200, 0))
	instructions := []*planv1.Instruction{
		{Id: "4", Code: "I100", CreateTime: at},
		{Id: "3", Code: "I99", CreateTime: at},
		{Id: "2", Code: "I02", CreateTime: at},
		{Id: "1", Code: "I01", CreateTime: at},
		// An earlier timestamp comes first, regardless of the numeric code.
		{Id: "0", Code: "I200", CreateTime: timestamppb.New(time.Unix(100, 0))},
	}
	sortInstructions(instructions)
	var codes []string
	for _, instruction := range instructions {
		codes = append(codes, instruction.GetCode())
	}
	if !slices.Equal(codes, []string{"I200", "I01", "I02", "I99", "I100"}) {
		t.Fatalf("timestamp/numeric code ordering: %v", codes)
	}
}
