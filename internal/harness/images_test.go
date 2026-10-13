package harness

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testImage() plan.AttachmentData {
	return plan.AttachmentData{
		Metadata: &planv1.Attachment{Name: "diagram.png", MediaType: "image/png"},
		Data:     []byte("image bytes"),
	}
}

func TestClaudeImageWirePayload(t *testing.T) {
	line, err := claudeMessageWithImages("inspect this", []plan.AttachmentData{testImage()})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatal(err)
	}
	message := got["message"].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 2 || content[0].(map[string]any)["type"] != "text" || content[1].(map[string]any)["type"] != "image" {
		t.Fatalf("content = %#v", content)
	}
	image := content[1].(map[string]any)
	source := image["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" ||
		source["data"] != base64.StdEncoding.EncodeToString([]byte("image bytes")) || strings.Contains(string(line), "diagram.png") {
		t.Fatalf("image wire payload = %s", line)
	}
	imageOnly, err := claudeMessageWithImages("", []plan.AttachmentData{testImage()})
	if err != nil || strings.Contains(string(imageOnly), `"type":"text"`) {
		t.Fatalf("image-only Claude payload = %s, %v", imageOnly, err)
	}

	empty, err := claudeMessageLine("")
	if err != nil || !strings.Contains(string(empty), `"type":"text"`) {
		t.Fatalf("empty text payload = %s, %v", empty, err)
	}
}

func TestCodexImageWirePayload(t *testing.T) {
	params, err := (Codex{}).turnParamsInput(Spec{Dir: "/work"}, "thread", "inspect this", []plan.AttachmentData{testImage()})
	if err != nil {
		t.Fatal(err)
	}
	input := params["input"].([]any)
	if len(input) != 2 || input[0].(map[string]any)["type"] != "text" || input[1].(map[string]any)["type"] != "image" {
		t.Fatalf("input = %#v", input)
	}
	image := input[1].(map[string]any)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("image bytes"))
	if image["url"] != want || strings.Contains(image["url"].(string), "diagram.png") {
		t.Fatalf("image wire payload = %#v", image)
	}
	imageOnly, err := (Codex{}).turnParamsInput(Spec{Dir: "/work"}, "thread", "", []plan.AttachmentData{testImage()})
	if err != nil || len(imageOnly["input"].([]any)) != 1 {
		t.Fatalf("image-only Codex payload = %#v, %v", imageOnly, err)
	}
}

// TestClaudeImageProcessReplay exercises the complete provider path: Claude's process reads the actual stdin line,
// while fakeproc_test.go replays the recorded success stream without calling an installed agent.
func TestClaudeImageProcessReplay(t *testing.T) {
	data := testImage()
	data.Data = onePixelPNG(t)
	env, _, inputFile := fake{provider: "claude", fixture: "success", end: "eof"}.env(t)
	w, err := (Claude{Command: os.Args[0]}).Start(t.Context(), Spec{
		TaskID: "image-claude", Dir: t.TempDir(), Prompt: "inspect this", Env: env, Attachments: []plan.AttachmentData{data},
	})
	if err != nil {
		t.Fatal(err)
	}
	collect(w)
	if res := w.Wait(); res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("Claude replay result = %+v", res)
	}
	lines := recordedInputLines(t, inputFile)
	if len(lines) == 0 {
		t.Fatal("Claude replay recorded no stdin")
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &message); err != nil {
		t.Fatal(err)
	}
	content := message["message"].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("Claude replay content = %#v, want text and image blocks", content)
	}
	imageBlock := content[1].(map[string]any)
	source := imageBlock["source"].(map[string]any)
	if message["type"] != "user" || imageBlock["type"] != "image" || source["type"] != "base64" ||
		source["media_type"] != "image/png" || source["data"] != base64.StdEncoding.EncodeToString(data.Data) {
		t.Fatalf("Claude replay image input = %#v", message)
	}
}

// TestCodexImageProcessReplay exercises the complete app-server path and inspects the recorded turn/start request,
// including the image item that was sent after initialize and thread/start.
func TestCodexImageProcessReplay(t *testing.T) {
	data := testImage()
	data.Data = onePixelPNG(t)
	env, _, inputFile := fake{provider: "codex", fixture: "success", end: "eof"}.env(t)
	w, err := (Codex{Command: os.Args[0]}).Start(t.Context(), Spec{
		TaskID: "image-codex", Dir: t.TempDir(), Prompt: "inspect this", Env: env, Attachments: []plan.AttachmentData{data},
	})
	if err != nil {
		t.Fatal(err)
	}
	collect(w)
	if res := w.Wait(); res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("Codex replay result = %+v", res)
	}
	lines := recordedInputLines(t, inputFile)
	var turn map[string]any
	for _, line := range lines {
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message["method"] == "turn/start" {
			turn = message
			break
		}
	}
	if turn == nil {
		t.Fatalf("Codex replay recorded no turn/start: %q", lines)
	}
	params := turn["params"].(map[string]any)
	input := params["input"].([]any)
	if len(input) != 2 || input[1].(map[string]any)["type"] != "image" {
		t.Fatalf("Codex replay input = %#v, want text and image items", input)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Data)
	if input[1].(map[string]any)["url"] != want {
		t.Fatalf("Codex replay image input = %#v, want %q", input[1], want)
	}
}

func recordedInputLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.Lines(string(b)) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestImageProviderPreflight(t *testing.T) {
	invalid := plan.AttachmentData{Metadata: &planv1.Attachment{MediaType: "image/png"}}
	for _, tt := range []struct {
		name  string
		start func() error
	}{
		{"codex", func() error {
			_, err := (Codex{Command: "djinn-no-such-codex"}).Start(t.Context(), Spec{Dir: t.TempDir(), Attachments: []plan.AttachmentData{invalid}})
			return err
		}},
		{"claude", func() error {
			_, err := (Claude{Command: "djinn-no-such-claude"}).Start(t.Context(), Spec{Dir: t.TempDir(), Attachments: []plan.AttachmentData{invalid}})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.start(); err == nil || strings.Contains(err.Error(), "not found in PATH") {
				t.Fatalf("preflight error = %v", err)
			}
		})
	}
	valid := []plan.AttachmentData{testImage()}
	for _, provider := range []Provider{
		Antigravity{Command: "djinn-no-such-agy"},
		Watch{},
	} {
		_, err := provider.Start(t.Context(), Spec{Dir: t.TempDir(), Prompt: "echo hi", Attachments: valid})
		if !errors.Is(err, ErrImageInputUnsupported) {
			t.Errorf("%T error = %v, want ErrImageInputUnsupported", provider, err)
		}
	}
}

func TestStreamImageEncoderPreflightRunsBeforeProcess(t *testing.T) {
	want := errors.New("invalid image wire payload")
	_, err := startStream(t.Context(), Spec{Dir: t.TempDir(), Prompt: "inspect", Attachments: []plan.AttachmentData{testImage()}},
		"djinn-no-such-stream-agent", nil, 0, streamAgent{
			name:         "recorded",
			encode:       func(string) ([]byte, error) { return nil, nil },
			encodeImages: func(string, []plan.AttachmentData) ([]byte, error) { return nil, want },
		},
	)
	if !errors.Is(err, want) || strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("preflight error = %v", err)
	}
}

func TestCloneImageInputsCopiesWireBytes(t *testing.T) {
	in := []plan.AttachmentData{testImage()}
	out := cloneImageInputs(in)
	in[0].Data[0] = 'X'
	in[0].Metadata.Name = "changed.png"
	if string(out[0].Data) != "image bytes" || out[0].Metadata.GetName() != "diagram.png" {
		t.Fatalf("clone changed with caller input: %#v", out)
	}
}

func TestTaskImageBudgetBoundsBeforeReadingBlobs(t *testing.T) {
	tooMany := &planv1.Instruction{Code: "I09"}
	for range maxProviderImages + 1 {
		tooMany.Attachments = append(tooMany.Attachments, &planv1.Attachment{Id: "id", Size: 1})
	}
	if err := checkTaskImageBudget([]*planv1.Instruction{tooMany}); err == nil {
		t.Fatal("accepted more images than one provider input batch supports")
	}
	tooLarge := &planv1.Instruction{Code: "I10", Attachments: []*planv1.Attachment{
		{Id: "first", Size: maxProviderImageBytes + 1},
	}}
	if err := checkTaskImageBudget([]*planv1.Instruction{tooLarge}); err == nil {
		t.Fatal("accepted an image over the provider byte bound")
	}
	var historical []*planv1.Instruction
	for range maxProviderImages + 1 {
		historical = append(historical, &planv1.Instruction{Code: "I09", Attachments: []*planv1.Attachment{{Id: "historical", Size: 1}}})
	}
	if err := checkTaskImageBudget(historical); err != nil {
		t.Fatalf("rejected independent historical image batches: %v", err)
	}
}

func TestFakeImageInputIsTyped(t *testing.T) {
	w, err := Fake{}.Start(t.Context(), Spec{Prompt: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.(ImageWorker); !ok {
		t.Fatal("fake worker has no typed image path")
	}
	if err := w.(ImageWorker).SendWithAttachments("", []plan.AttachmentData{testImage()}); err != nil {
		t.Fatal(err)
	}
	w.Stop()
	collect(w)
}

func TestFakeInitialImageInputIsTyped(t *testing.T) {
	data := testImage()
	w, err := Fake{}.Start(t.Context(), Spec{TaskID: "task", Prompt: "wait", Attachments: []plan.AttachmentData{data}})
	if err != nil {
		t.Fatal(err)
	}
	fake := w.(*fakeWorker)
	if len(fake.initial) != 1 || !bytes.Equal(fake.initial[0].Data, data.Data) || fake.initial[0].Metadata.GetName() != data.Metadata.GetName() {
		t.Fatalf("initial image input = %#v, want typed bytes", fake.initial)
	}
	w.Stop()
	collect(w)
}

func TestTransientImageInputsAreNotDeduplicated(t *testing.T) {
	r := &run{deliveredImages: map[string]struct{}{}}
	durable := plan.AttachmentData{Metadata: &planv1.Attachment{Id: "00000000-0000-7000-8000-000000000001"}, Data: []byte("durable")}
	transient := testImage()
	r.markImages([]plan.AttachmentData{durable})
	got := r.pendingImages([]plan.AttachmentData{durable, transient, transient})
	if len(got) != 2 || !bytes.Equal(got[0].Data, transient.Data) || !bytes.Equal(got[1].Data, transient.Data) {
		t.Fatalf("pending image inputs = %#v, want both transient indications", got)
	}
}

func TestDeliveredImagesResetForNewProviderWorker(t *testing.T) {
	old := plan.AttachmentData{Metadata: &planv1.Attachment{Id: "00000000-0000-7000-8000-000000000001"}, Data: []byte("old")}
	next := plan.AttachmentData{Metadata: &planv1.Attachment{Id: "00000000-0000-7000-8000-000000000002"}, Data: []byte("next")}
	r := &run{}
	r.markImages([]plan.AttachmentData{old})
	r.beginWorkerImages([]plan.AttachmentData{next})
	pending := r.pendingImages([]plan.AttachmentData{old, next})
	if len(pending) != 1 || pending[0].Metadata.GetId() != old.Metadata.GetId() {
		t.Fatalf("pending image IDs after provider restart = %#v, want old only", pending)
	}
}

func TestHistoricalInstructionImagesSplitAcrossLaunchAndCatchup(t *testing.T) {
	testx.Portable(t)
	// Hold the scheduler's automatic pass so the nine instructions can be assigned before the explicit start.
	limit := &limit{slots: 0}
	e := up(t, t.TempDir(), WithCapacity(limit.capacity), WithTick(time.Hour))
	wishID, _ := e.wish(t, gitRepo(t))
	task := e.mustSpawn(t, wishID, "planned image worker", "wait\nsleep 1h", &planv1.TaskServiceSpawnRequest{Later: true})
	instructions := planv1connect.NewInstructionServiceClient(e.srv.Client(), e.srv.URL)
	data := onePixelPNG(t)
	var wantIDs []string
	for n := 0; n < maxProviderImages+1; n++ {
		sent, err := instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
			WishId: wishID, Text: fmt.Sprintf("inspect image %d", n+1), Attachments: []*planv1.AttachmentInput{{
				Name: "pixel.png", MediaType: "image/png", Data: data,
			}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		instruction := sent.Msg.GetInstruction()
		wantIDs = append(wantIDs, instruction.GetAttachments()[0].GetId())
		if _, err := instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{
			Instruction: instruction.GetId(), WishId: wishID,
		})); err != nil {
			t.Fatal(err)
		}
		assigned, err := instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{
			Instruction: instruction.GetId(), Task: task.GetId(), WishId: wishID,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if assigned.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_DEFERRED {
			t.Fatalf("instruction %d image delivery = %s, want deferred before task start", n+1, assigned.Msg.GetImageDelivery())
		}
	}

	if _, err := e.tasks.Start(t.Context(), connect.NewRequest(&planv1.TaskServiceStartRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	e.h.mu.Lock()
	r := e.h.runs[task.GetId()]
	var worker *fakeWorker
	if r != nil {
		worker, _ = r.worker.(*fakeWorker)
	}
	e.h.mu.Unlock()
	if worker == nil {
		t.Fatalf("started worker = %T, want fakeWorker", r)
	}
	worker.mu.Lock()
	initial := append([]plan.AttachmentData(nil), worker.initial...)
	inbox := append([]fakeInput(nil), worker.inbox...)
	worker.mu.Unlock()
	if len(initial) != maxProviderImages {
		t.Fatalf("launch image count = %d, want %d", len(initial), maxProviderImages)
	}
	seen := map[string]struct{}{}
	for n, attachment := range initial {
		id := attachment.Metadata.GetId()
		if id != wantIDs[n] || !bytes.Equal(attachment.Data, data) {
			t.Fatalf("launch image %d = %q/%d bytes, want %q/%d", n+1, id, len(attachment.Data), wantIDs[n], len(data))
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate launch image ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(inbox) != 1 || inbox[0].text != "inspect image 9" || len(inbox[0].attachments) != 1 {
		t.Fatalf("catch-up inputs = %#v, want one typed image input", inbox)
	}
	catchup := inbox[0].attachments[0]
	if catchup.Metadata.GetId() != wantIDs[maxProviderImages] || !bytes.Equal(catchup.Data, data) {
		t.Fatalf("catch-up image = %q/%d bytes, want %q/%d", catchup.Metadata.GetId(), len(catchup.Data), wantIDs[maxProviderImages], len(data))
	}
	if _, duplicate := seen[catchup.Metadata.GetId()]; duplicate {
		t.Fatalf("catch-up duplicated launch image ID %q", catchup.Metadata.GetId())
	}
	seen[catchup.Metadata.GetId()] = struct{}{}
	if len(seen) != maxProviderImages+1 {
		t.Fatalf("delivered image IDs = %d, want %d", len(seen), maxProviderImages+1)
	}
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "text-only follow-up",
	})); err != nil {
		t.Fatalf("text-only send after image catch-up: %v", err)
	}
}

func TestTaskSendDataRejectsClientMetadata(t *testing.T) {
	_, err := (&Tasks{}).SendData(t.Context(), connect.NewRequest(&planv1.TaskServiceSendDataRequest{
		TaskId:             "00000000-0000-7000-8000-000000000001",
		AttachmentMetadata: []*planv1.Attachment{{Id: "00000000-0000-7000-8000-000000000002"}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "server-generated") {
		t.Fatalf("metadata-only task send error = %v", err)
	}
}

type heldImageProvider struct {
	mu     sync.Mutex
	worker *heldImageWorker
}

func (p *heldImageProvider) Start(context.Context, Spec) (Worker, error) {
	w := &heldImageWorker{events: make(chan Event), done: make(chan struct{}), accepted: make(chan struct{}), release: make(chan struct{})}
	p.mu.Lock()
	p.worker = w
	p.mu.Unlock()
	return w, nil
}

type heldImageWorker struct {
	events   chan Event
	done     chan struct{}
	accepted chan struct{}
	release  chan struct{}
	stop     sync.Once
}

func (w *heldImageWorker) Events() <-chan Event { return w.events }
func (w *heldImageWorker) Send(string) error    { return nil }
func (w *heldImageWorker) SendWithAttachments(string, []plan.AttachmentData) error {
	select {
	case <-w.accepted:
	default:
		close(w.accepted)
	}
	select {
	case <-w.release:
		return nil
	case <-w.done:
		return ErrClosed
	}
}
func (w *heldImageWorker) Stop() {
	w.stop.Do(func() {
		close(w.done)
		close(w.events)
	})
}
func (w *heldImageWorker) Wait() Result { <-w.done; return Result{} }

// TestImageSendCancellationAfterPumpAcceptance keeps staged bytes owned by the committed task event after the
// request context is canceled. The worker deliberately holds the pump after accepting the message, making the
// handoff deterministic without a real provider or a long sleep.
func TestImageSendCancellationAfterPumpAcceptance(t *testing.T) {
	testx.Portable(t)
	home := t.TempDir()
	db, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	h := New(db, home, nil)
	task := &planv1.Task{Id: store.NewID(), Code: "W1", Status: planv1.TaskStatus_TASK_STATUS_RUNNING}
	if err := db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal(actorLocal, "test/task", task); err != nil {
			return err
		}
		return tx.Put(task)
	}); err != nil {
		h.Close()
		_ = db.Close()
		t.Fatal(err)
	}
	r, err := h.newRun(task, 0)
	if err != nil {
		h.Close()
		_ = db.Close()
		t.Fatal(err)
	}
	provider := &heldImageProvider{}
	worker, err := provider.Start(h.ctx, Spec{TaskID: task.GetId()})
	if err != nil {
		h.Close()
		_ = db.Close()
		t.Fatal(err)
	}
	held := worker.(*heldImageWorker)
	r.worker = worker
	// If setup or the assertion fails before the pump replies, stop it so cleanup cannot wait on a held send.
	released := false
	t.Cleanup(func() {
		if !released {
			close(held.release)
		}
		worker.Stop()
		h.Close()
		_ = db.Close()
	})
	go h.pump(r)
	tasks := &Tasks{h: h}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	data := onePixelPNG(t)
	type response struct {
		value *connect.Response[planv1.TaskServiceSendDataResponse]
		err   error
	}
	result := make(chan response, 1)
	go func() {
		value, err := tasks.SendData(ctx, connect.NewRequest(&planv1.TaskServiceSendDataRequest{
			TaskId: task.GetId(), Text: "inspect while blocked", Attachments: []*planv1.AttachmentInput{{
				Name: "pixel.png", MediaType: "image/png", Data: data,
			}},
		}))
		result <- response{value: value, err: err}
	}()
	<-held.accepted
	cancel()
	select {
	case got := <-result:
		t.Fatalf("image send returned before pump reply: value=%v err=%v", got.value, got.err)
	default:
	}
	entries, err := os.ReadDir(filepath.Join(home, "attachments"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("staged attachment after cancellation: entries=%d err=%v", len(entries), err)
	}
	close(held.release)
	released = true
	got := <-result
	if got.err != nil || got.value == nil || len(got.value.Msg.GetEvent().GetAttachments()) != 1 {
		t.Fatalf("image send after pump reply: value=%v err=%v", got.value, got.err)
	}
	ref := got.value.Msg.GetEvent().GetAttachments()[0]
	if _, err := os.Stat(filepath.Join(home, "attachments", ref.GetId())); err != nil {
		t.Fatalf("committed event attachment was removed: %v", err)
	}
}

func TestAssignedImageReachesRunningFakeTaskOnce(t *testing.T) {
	testx.Portable(t)
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.spawn(t, wishID, "text working\nsleep 1h")

	instructions := planv1connect.NewInstructionServiceClient(e.srv.Client(), e.srv.URL)
	data := onePixelPNG(t)
	sent, err := instructions.SendData(t.Context(), connect.NewRequest(&planv1.InstructionServiceSendDataRequest{
		WishId: wishID,
		Text:   "inspect this image",
		Attachments: []*planv1.AttachmentInput{{
			Name: "pixel.png", MediaType: "image/png", Data: data,
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	instruction := sent.Msg.GetInstruction()
	if _, err := instructions.Reflect(t.Context(), connect.NewRequest(&planv1.InstructionServiceReflectRequest{Instruction: instruction.GetId()})); err != nil {
		t.Fatal(err)
	}
	assigned, err := instructions.Assign(t.Context(), connect.NewRequest(&planv1.InstructionServiceAssignRequest{
		Instruction: instruction.GetId(), Task: task.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED {
		t.Fatalf("assignment image delivery = %s, want queued", assigned.Msg.GetImageDelivery())
	}
	e.h.mu.Lock()
	r := e.h.runs[task.GetId()]
	if r == nil {
		e.h.mu.Unlock()
		t.Fatal("running task disappeared after image assignment")
	}
	w, ok := r.worker.(*fakeWorker)
	e.h.mu.Unlock()
	if !ok {
		t.Fatalf("worker = %T, want fakeWorker", r)
	}
	w.mu.Lock()
	inputs := append([]fakeInput(nil), w.inbox...)
	w.mu.Unlock()
	if len(inputs) != 1 || len(inputs[0].attachments) != 1 || !bytes.Equal(inputs[0].attachments[0].Data, data) {
		t.Fatalf("typed assignment input = %#v, want one image attachment", inputs)
	}
	stored, err := plan.ReadInstructionAttachments(e.home, instruction)
	if err != nil {
		t.Fatal(err)
	}
	if delivery, note := e.h.DeliverInstructionImages(t.Context(), assigned.Msg.GetInstruction(), stored); delivery != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED || note != "" {
		t.Fatalf("repeat assignment image delivery = %s (%s), want queued without a second input", delivery, note)
	}
	w.mu.Lock()
	inputs = append([]fakeInput(nil), w.inbox...)
	w.mu.Unlock()
	if len(inputs) != 1 {
		t.Fatalf("repeat assignment added a worker input: %#v", inputs)
	}
	dataSent, err := e.tasks.SendData(t.Context(), connect.NewRequest(&planv1.TaskServiceSendDataRequest{
		TaskId: task.GetId(), Text: "inspect again", Attachments: []*planv1.AttachmentInput{{
			Name: "pixel.png", MediaType: "image/png", Data: data,
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if dataSent.Msg.GetImageDelivery() != planv1.InstructionImageDelivery_INSTRUCTION_IMAGE_DELIVERY_QUEUED {
		t.Fatalf("task image delivery = %s, want queued", dataSent.Msg.GetImageDelivery())
	}
	if event := dataSent.Msg.GetEvent(); len(event.GetAttachments()) != 1 {
		t.Fatalf("task image event attachments = %d, want one", len(event.GetAttachments()))
	} else {
		reloaded, err := e.tasks.GetEvent(t.Context(), connect.NewRequest(&planv1.TaskServiceGetEventRequest{
			TaskId: task.GetId(), EventId: event.GetId(),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded.Msg.GetAttachments()) != 1 || !bytes.Equal(reloaded.Msg.GetAttachments()[0].GetData(), data) {
			t.Fatalf("reloaded task image = %#v, want exact stored bytes", reloaded.Msg.GetAttachments())
		}
	}
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: task.GetId(), Text: "continue"})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{TaskId: task.GetId(), Text: "continue again"})); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	pathSent, err := e.tasks.Send(t.Context(), connect.NewRequest(&planv1.TaskServiceSendRequest{
		TaskId: task.GetId(), Text: "inspect path", AttachmentPaths: []string{path},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pathSent.Msg.GetEvent().GetAttachments()) != 1 {
		t.Fatalf("path task image event attachments = %d, want one", len(pathSent.Msg.GetEvent().GetAttachments()))
	}
	w.mu.Lock()
	deferred := append([]fakeInput(nil), w.inbox...)
	w.mu.Unlock()
	if len(deferred) != 5 || len(deferred[1].attachments) != 1 || !bytes.Equal(deferred[1].attachments[0].Data, data) ||
		len(deferred[2].attachments) != 0 || len(deferred[3].attachments) != 0 || len(deferred[4].attachments) != 1 ||
		!bytes.Equal(deferred[4].attachments[0].Data, data) {
		t.Fatalf("duplicate image delivery = %#v, want assignment and explicit task images only", deferred)
	}
}

func TestTaskDeleteRemovesEventImageFiles(t *testing.T) {
	testx.Portable(t)
	e := up(t, t.TempDir())
	wishID, _ := e.wish(t, t.TempDir())
	task := e.mustSpawn(t, wishID, "planned", "text planned", &planv1.TaskServiceSpawnRequest{Later: true})
	data := onePixelPNG(t)
	attachments, ids, err := plan.StoreAttachmentInputs(e.home, []*planv1.AttachmentInput{{Name: "event.png", MediaType: "image/png", Data: data}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || len(ids) != 1 {
		t.Fatalf("stored event attachments = %d/%d, want one", len(attachments), len(ids))
	}
	event := &planv1.TaskEvent{Id: store.NewID(), TaskId: task.GetId(), Seq: 99,
		Kind: planv1.TaskEventKind_TASK_EVENT_KIND_MESSAGE, Text: "image", CreateTime: timestamppb.New(time.Now()),
		Attachments: []*planv1.Attachment{attachments[0].Metadata}}
	if err := e.db.Tx(t.Context(), func(tx *store.Tx) error {
		if err := tx.Journal("test", "task/event", event); err != nil {
			return err
		}
		return tx.Put(event)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Delete(t.Context(), connect.NewRequest(&planv1.TaskServiceDeleteRequest{TaskId: task.GetId()})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.home, "attachments", ids[0])); !os.IsNotExist(err) {
		t.Fatalf("event attachment file after task delete: err=%v, want not found", err)
	}
}

func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
