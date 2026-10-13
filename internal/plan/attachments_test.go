package plan

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

type imageDelivererWorkers struct {
	attachments []AttachmentData
}

func (f *imageDelivererWorkers) Shelve(context.Context, string) {}
func (f *imageDelivererWorkers) Wake()                          {}
func (f *imageDelivererWorkers) StopWish(context.Context, string) error {
	return nil
}
func (f *imageDelivererWorkers) DeliverInstructionImages(_ context.Context, _ *planv1.Instruction, attachments []AttachmentData) (planv1.InstructionImageDelivery, string) {
	f.attachments = attachments
	return planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED, ""
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 0x80, 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestInstructionImageStorageRetryAndTypedRead(t *testing.T) {
	home := t.TempDir()
	c := serve(t, WithHome(home))
	wish := c.wish(t)
	data := pngBytes(t, 3, 2)
	requestID := store.NewID()
	sent, err := c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, RequestId: requestID,
		Attachments: []*planv1.AttachmentInput{{Name: "screen.png", MediaType: "image/png", Data: data}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	instruction := sent.Msg.GetInstruction()
	if instruction.GetText() != "" || len(instruction.GetAttachments()) != 1 {
		t.Fatalf("image-only instruction: %v", instruction)
	}
	if sent.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED {
		t.Fatalf("offline image delivery = %s, want deferred", sent.Msg.GetImageDelivery())
	}
	ref := instruction.GetAttachments()[0]
	if ref.GetId() == "" || ref.GetSize() != int64(len(data)) || ref.GetWidth() != 3 || ref.GetHeight() != 2 || ref.GetMediaType() != "image/png" {
		t.Fatalf("attachment metadata: %v", ref)
	}
	got, err := ReadInstructionAttachments(home, instruction)
	if err != nil || len(got) != 1 || !bytes.Equal(got[0].Data, data) {
		t.Fatalf("read stored image: %v", err)
	}
	retrieved, err := c.instructions.GetAttachment(t.Context(), connect.NewRequest(&planv1.InstructionServiceGetAttachmentRequest{
		WishId: wish, InstructionId: instruction.GetId(), AttachmentId: ref.GetId(),
	}))
	if err != nil || retrieved.Msg.GetAttachment().GetId() != ref.GetId() || !bytes.Equal(retrieved.Msg.GetData(), data) {
		t.Fatalf("typed persisted image retrieval: %v %v", retrieved, err)
	}
	_, err = c.instructions.GetAttachment(t.Context(), connect.NewRequest(&planv1.InstructionServiceGetAttachmentRequest{
		WishId: store.NewID(), InstructionId: instruction.GetId(), AttachmentId: ref.GetId(),
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong-wish image retrieval code=%v err=%v", code(err), err)
	}
	_, err = c.instructions.GetAttachment(t.Context(), connect.NewRequest(&planv1.InstructionServiceGetAttachmentRequest{
		WishId: wish, InstructionId: instruction.GetId(), AttachmentId: store.NewID(),
	}))
	if code(err) != connect.CodeNotFound {
		t.Fatalf("unknown image retrieval code=%v err=%v", code(err), err)
	}
	commands, err := store.Commands(t.Context(), c.store, func(command store.Command) bool {
		return command.Method == "" || command.Method == planv1connectInstructionSendDataProcedure()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if command.Method != planv1connectInstructionSendDataProcedure() {
			continue
		}
		request, err := request(command)
		if err != nil {
			t.Fatal(err)
		}
		if typed, ok := request.(*planv1.InstructionServiceSendDataRequest); !ok || len(typed.GetAttachments()) != 0 || len(typed.GetAttachmentMetadata()) != 1 || typed.GetAttachmentMetadata()[0].GetSize() != int64(len(data)) {
			t.Fatalf("journal carried image bytes: %T %v", request, request)
		}
	}
	retry, err := c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, RequestId: requestID,
		Attachments: []*planv1.AttachmentInput{{Name: "screen.png", MediaType: "image/png", Data: data}},
	}))
	if err != nil || retry.Msg.GetInstruction().GetId() != instruction.GetId() {
		t.Fatalf("same image retry: %v %v", retry, err)
	}
	_, err = c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, RequestId: requestID,
		Attachments: []*planv1.AttachmentInput{{Name: "other.png", MediaType: "image/png", Data: pngBytes(t, 4, 2)}},
	}))
	if code(err) != connect.CodeAlreadyExists {
		t.Fatalf("different image retry code=%v err=%v", code(err), err)
	}
	_, err = c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, Text: "server metadata only", AttachmentMetadata: []*planv1.Attachment{{Name: "image.png", MediaType: "image/png", Size: 1}},
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("caller-supplied image metadata code=%v err=%v", code(err), err)
	}
	_, err = c.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{WishId: wish}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("blank text-only send code=%v err=%v", code(err), err)
	}
}

func TestInstructionImageStoreReopen(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	database := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, database, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	wishes := &Wishes{Store: s, Home: home}
	made, err := wishes.Make(ctx, connect.NewRequest(&planv1.WishServiceMakeRequest{Title: "Image persistence"}))
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	wish := made.Msg.GetWish().GetId()
	data := pngBytes(t, 4, 3)
	sent, err := (&Instructions{Store: s, Home: home}).SendData(ctx, connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, Text: "Keep this image after restart", RequestId: store.NewID(),
		Attachments: []*planv1.AttachmentInput{{Name: "restart.png", MediaType: "image/png", Data: data}},
	}))
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if len(sent.Msg.GetInstruction().GetAttachments()) != 1 {
		s.Close()
		t.Fatalf("stored image count = %d", len(sent.Msg.GetInstruction().GetAttachments()))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, database, Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listed, err := (&Instructions{Store: s, Home: home}).List(ctx, connect.NewRequest(&planv1.InstructionServiceListRequest{WishId: wish}))
	if err != nil || len(listed.Msg.GetInstructions()) != 1 {
		t.Fatalf("reopen lost instruction: %v %v", listed, err)
	}
	got, err := ReadInstructionAttachments(home, listed.Msg.GetInstructions()[0])
	if err != nil || len(got) != 1 || !bytes.Equal(got[0].Data, data) {
		t.Fatalf("reopen lost image: %v", err)
	}
}

func TestInstructionImageUnsupportedLeadStillSucceeds(t *testing.T) {
	home := t.TempDir()
	leads := &fakeLeads{}
	c := serve(t, WithHome(home), WithLeads(leads))
	wish := c.wish(t)
	leads.running = map[string][]string{LeadTerminal(wish): {"claude"}}
	sent, err := c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, Attachments: []*planv1.AttachmentInput{{Name: "lead.png", MediaType: "image/png", Data: pngBytes(t, 2, 2)}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if sent.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_UNSUPPORTED {
		t.Fatalf("unsupported lead image delivery = %s", sent.Msg.GetImageDelivery())
	}
	listed := instructionsOf(t, c, wish)
	if len(listed) != 1 || len(listed[0].GetAttachments()) != 1 {
		t.Fatalf("unsupported lead lost durable instruction: %v", listed)
	}
	if len(leads.told) != 1 || !strings.Contains(leads.told[0], "instruction reflect") {
		t.Fatalf("unsupported lead did not receive durable lifecycle notice: %v", leads.told)
	}
}

func TestInstructionImageAssignUsesTypedWorkerDelivery(t *testing.T) {
	home := t.TempDir()
	workers := &imageDelivererWorkers{}
	c := serve(t, WithHome(home), WithWorkers(workers))
	wish := c.wish(t)
	task := &planv1.Task{Id: store.NewID(), WishId: wish, Code: "W1", Title: "Image worker", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, CreateTime: timestamppb.Now()}
	putInstructionTask(t, c.store, task)
	data := pngBytes(t, 2, 2)
	sent, err := c.instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wish, Text: "Review this", Attachments: []*planv1.AttachmentInput{{Name: "assign.png", MediaType: "image/png", Data: data}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: sent.Msg.GetInstruction().GetId()})); err != nil {
		t.Fatal(err)
	}
	assigned, err := c.instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{
		Instruction: sent.Msg.GetInstruction().GetId(), Task: task.GetId(),
	}))
	if err != nil || assigned.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED {
		t.Fatalf("assign image delivery=%s err=%v", assigned.Msg.GetImageDelivery(), err)
	}
	if len(workers.attachments) != 1 || !bytes.Equal(workers.attachments[0].Data, data) {
		t.Fatalf("typed worker attachment: %+v", workers.attachments)
	}
}

func planv1connectInstructionSendDataProcedure() string {
	return "/plan.v1.InstructionService/SendData"
}

func TestInstructionImagePathExportImportAndDelete(t *testing.T) {
	srcHome, dstHome := t.TempDir(), t.TempDir()
	src := serve(t, WithHome(srcHome))
	wish := src.wish(t)
	path := filepath.Join(t.TempDir(), "input.png")
	data := pngBytes(t, 2, 2)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sent, err := src.instructions.Send(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendRequest{
		WishId: wish, AttachmentPaths: []string{path},
	}))
	if err != nil {
		t.Fatal(err)
	}
	oldID := sent.Msg.GetInstruction().GetAttachments()[0].GetId()
	file := filepath.Join(t.TempDir(), "wish.djinn")
	if _, err := src.wishes.Export(t.Context(), connect.NewRequest(&planv1.WishServiceExportRequest{WishId: wish, File: file})); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exported, []byte(path)) {
		t.Fatal("export leaked the local attachment path")
	}
	exp, err := decode(exported)
	if err != nil || len(exp.GetAttachments()) != 1 || !bytes.Equal(exp.GetAttachments()[0].GetData(), data) {
		t.Fatalf("exported attachment: %v %v", exp, err)
	}
	dst := serve(t, WithHome(dstHome))
	imported, err := dst.wishes.ImportData(t.Context(), connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: exported}))
	if err != nil {
		t.Fatal(err)
	}
	listed := instructionsOf(t, dst, imported.Msg.GetWish().GetId())
	if len(listed) != 1 || len(listed[0].GetAttachments()) != 1 || listed[0].GetAttachments()[0].GetId() == oldID {
		t.Fatalf("import did not remap attachment id: %v", listed)
	}
	if got, err := ReadInstructionAttachments(dstHome, listed[0]); err != nil || !bytes.Equal(got[0].Data, data) {
		t.Fatalf("imported attachment read: %v", err)
	}
	newID := listed[0].GetAttachments()[0].GetId()
	if _, err := dst.wishes.Delete(t.Context(), connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: imported.Msg.GetWish().GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dstHome, attachmentFolder, newID)); !os.IsNotExist(err) {
		t.Fatalf("deleted wish left attachment %s: %v", newID, err)
	}
}

func TestTaskEventImageExportImportAndDelete(t *testing.T) {
	srcHome, dstHome := t.TempDir(), t.TempDir()
	src := serve(t, WithHome(srcHome))
	wishID := src.wish(t)
	data := pngBytes(t, 3, 2)
	stored, ids, err := StoreAttachmentInputs(srcHome, []*planv1.AttachmentInput{{Name: "task.png", MediaType: "image/png", Data: data}})
	if err != nil || len(stored) != 1 || len(ids) != 1 {
		t.Fatalf("stage task image: %v", err)
	}
	task := &planv1.Task{Id: store.NewID(), WishId: wishID, Code: "W1", Title: "Image event", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: timestamppb.Now()}
	event := &planv1.TaskEvent{Id: store.NewID(), TaskId: task.GetId(), Seq: 1, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, CreateTime: timestamppb.Now(), Attachments: []*planv1.Attachment{stored[0].Metadata}}
	if err := src.store.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.TaskServiceSendDataProcedure, &planv1.TaskServiceSendDataRequest{
			TaskId: task.GetId(), Text: "inspect image", AttachmentMetadata: []*planv1.Attachment{stored[0].Metadata},
		}); err != nil {
			return err
		}
		if err := tx.Put(task); err != nil {
			return err
		}
		return tx.Put(event)
	}); err != nil {
		RemoveStoredAttachments(srcHome, ids)
		t.Fatal(err)
	}
	exp, _, err := collect(t.Context(), src.store, wishID, allEvents)
	if err != nil {
		t.Fatal(err)
	}
	if err := fillAttachmentExports(srcHome, exp); err != nil {
		t.Fatal(err)
	}
	if err := check(exp); err != nil {
		t.Fatalf("task event export validation: %v", err)
	}
	encoded, err := encode(exp, false)
	if err != nil {
		t.Fatal(err)
	}
	dst := serve(t, WithHome(dstHome))
	imported, err := dst.wishes.ImportData(t.Context(), connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: encoded}))
	if err != nil {
		t.Fatal(err)
	}
	taskEvents, err := store.List[*planv1.TaskEvent](t.Context(), dst.store, store.Where{"task_id": task.GetId()})
	if err != nil || len(taskEvents) != 1 || len(taskEvents[0].GetAttachments()) != 1 {
		t.Fatalf("imported task event: %v %v", taskEvents, err)
	}
	ref := taskEvents[0].GetAttachments()[0]
	if ref.GetId() == stored[0].Metadata.GetId() {
		t.Fatalf("task event attachment id was not remapped: %s", ref.GetId())
	}
	attachments, err := ReadTaskEventAttachments(dstHome, taskEvents[0])
	if err != nil || len(attachments) != 1 || !bytes.Equal(attachments[0].GetData(), data) {
		t.Fatalf("imported task event image: %v", err)
	}
	commands, err := store.Commands(t.Context(), dst.store, func(command store.Command) bool {
		return command.Method == planv1connect.WishServiceImportDataProcedure
	})
	if err != nil || len(commands) != 1 {
		t.Fatalf("import journal: %v %v", commands, err)
	}
	importRequest, err := request(commands[0])
	if err != nil {
		t.Fatal(err)
	}
	importData, ok := importRequest.(*planv1.WishServiceImportDataRequest)
	if !ok {
		t.Fatalf("import journal request type = %T", importRequest)
	}
	carried, err := decode(importData.GetData())
	if err != nil || len(carried.GetCommands()) == 0 {
		t.Fatalf("carried import commands: %v %v", carried.GetCommands(), err)
	}
	var carriedRequest proto.Message
	for _, command := range carried.GetCommands() {
		if command.GetMethod() != planv1connect.TaskServiceSendDataProcedure {
			continue
		}
		carriedRequest, err = command.GetRequest().UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	taskSend, ok := carriedRequest.(*planv1.TaskServiceSendDataRequest)
	if !ok || len(taskSend.GetAttachmentMetadata()) != 1 || taskSend.GetAttachmentMetadata()[0].GetId() != ref.GetId() {
		t.Fatalf("import journal kept stale attachment id: %T %v", carriedRequest, carriedRequest)
	}
	if imported.Msg.GetWish().GetId() != wishID {
		t.Fatalf("imported wish = %s, want %s", imported.Msg.GetWish().GetId(), wishID)
	}
	if _, err := dst.wishes.Delete(t.Context(), connect.NewRequest(&planv1.WishServiceDeleteRequest{WishId: wishID})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dstHome, attachmentFolder, ref.GetId())); !os.IsNotExist(err) {
		t.Fatalf("deleted task event left attachment %s: %v", ref.GetId(), err)
	}
}

func TestExportImportKeepsDeletedTaskHistoryPortable(t *testing.T) {
	ctx := t.Context()
	wishID, taskID, oldAttachmentID := store.NewID(), store.NewID(), store.NewID()
	request, err := anypb.New(&planv1.TaskServiceSendDataRequest{
		TaskId: taskID, Text: "historical image",
		AttachmentMetadata: []*planv1.Attachment{{Id: oldAttachmentID, Name: "old.png", MediaType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	exp := &planv1.WishExport{
		Version: formatVersion,
		Wish:    &planv1.Wish{Id: wishID, Title: "Portable history"},
		Tasks: []*planv1.Task{{Id: taskID, WishId: wishID, Code: "W1", Title: "Deleted task",
			Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: timestamppb.Now()}},
		Commands: []*planv1.Command{{Id: store.NewID(), At: timestamppb.Now(), Method: planv1connect.TaskServiceSendDataProcedure, Request: request}},
	}
	data, err := encode(exp, false)
	if err != nil {
		t.Fatal(err)
	}
	src := serve(t)
	if _, err := src.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
		t.Fatal(err)
	}
	if err := src.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actor, planv1connect.TaskServiceDeleteProcedure, &planv1.TaskServiceDeleteRequest{TaskId: taskID}); err != nil {
			return err
		}
		return tx.Delete(&planv1.Task{Id: taskID})
	}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "history.djinn")
	data = export(t, src, wishID, file)
	dst := serve(t)
	if _, err := dst.wishes.ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data})); err != nil {
		t.Fatalf("deleted task history made export unportable: %v", err)
	}
}

func TestAttachmentValidationBoundsAndPathNormalization(t *testing.T) {
	if _, err := validateAttachment("../escape.png", "image/png", pngBytes(t, 1, 1)); err == nil {
		t.Fatal("accepted a path-like attachment name")
	}
	if _, err := validateAttachment("bad.png", "image/jpeg", pngBytes(t, 1, 1)); err == nil {
		t.Fatal("accepted a MIME mismatch")
	}
	if _, err := validateAttachment("large.png", "image/png", pngBytes(t, maxAttachmentWidth+1, 1)); err == nil {
		t.Fatal("accepted an image over the dimension bound")
	}
	if _, err := validateAttachment(strings.Repeat("a", 256), "image/png", pngBytes(t, 1, 1)); err == nil {
		t.Fatal("accepted an attachment name over the metadata bound")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "relative.png")
	if err := os.WriteFile(file, pngBytes(t, 1, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	normalized, err := normalizeAttachmentPath("relative.png")
	resolved, resolveErr := filepath.EvalSymlinks(file)
	if err != nil || resolveErr != nil || !filepath.IsAbs(normalized) || normalized != resolved {
		t.Fatalf("normalized path=%q err=%v", normalized, err)
	}
}

func TestStoredAttachmentRejectsSymlinkedStoreAndFile(t *testing.T) {
	data := pngBytes(t, 1, 1)
	prepared, err := validateAttachment("safe.png", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	prepared.meta.Id = store.NewID()

	fileHome := t.TempDir()
	fileRoot := filepath.Join(fileHome, attachmentFolder)
	if err := os.MkdirAll(fileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(fileRoot, prepared.meta.GetId())); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadAttachment(fileHome, prepared.meta); err == nil {
		t.Fatal("accepted a symlinked attachment file")
	}

	storeHome := t.TempDir()
	storeTarget := t.TempDir()
	storeID := store.NewID()
	if err := os.WriteFile(filepath.Join(storeTarget, storeID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(storeTarget, filepath.Join(storeHome, attachmentFolder)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	prepared.meta.Id = storeID
	if _, err := ReadAttachment(storeHome, prepared.meta); err == nil {
		t.Fatal("accepted a symlinked attachment store")
	}
}
