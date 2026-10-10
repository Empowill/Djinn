package machine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	djinnv1connect "github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
)

func TestReadWriteNotch(t *testing.T) {
	home := t.TempDir()

	// Default when settings.json does not exist.
	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("ReadNotch on missing file: got %v, want MEDIUM", got)
	}

	// Write an existing setting (e.g. shortcut) and ensure WriteNotch preserves it.
	settingsPath := filepath.Join(home, settingsFile)
	if err := os.WriteFile(settingsPath, []byte(`{"shortcut":"Ctrl+J"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteNotch(home, djinnv1.LoadNotch_LOAD_NOTCH_LIGHT); err != nil {
		t.Fatalf("WriteNotch: %v", err)
	}

	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_LIGHT {
		t.Errorf("ReadNotch after write: got %v, want LIGHT", got)
	}

	// Verify shortcut was preserved in settings.json.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !containsSubstring(content, "Ctrl+J") {
		t.Errorf("WriteNotch did not preserve shortcut in %s", content)
	}

	// Overwrite with HIGH.
	if err := WriteNotch(home, djinnv1.LoadNotch_LOAD_NOTCH_HIGH); err != nil {
		t.Fatalf("WriteNotch: %v", err)
	}
	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Errorf("ReadNotch after second write: got %v, want HIGH", got)
	}
}

func TestLoadServiceHandler(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()

	initPolicy := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	monitor := &Monitor{policy: initPolicy}

	var updatedPolicy Policy
	setPolicyCalled := 0
	setPolicy := func(p Policy) {
		setPolicyCalled++
		updatedPolicy = p
	}

	var broadcastedNotch djinnv1.LoadNotch
	broadcastCalled := 0
	broadcast := func(n djinnv1.LoadNotch) {
		broadcastCalled++
		broadcastedNotch = n
	}

	mux := http.NewServeMux()
	mux.Handle(LoadHandler(home, monitor, setPolicy, broadcast, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

	// Test Get.
	getRes, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("Get notch: got %v, want MEDIUM", getRes.Msg.GetNotch())
	}

	// Test Set with invalid/unspecified notch.
	_, err = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED,
	}))
	if err == nil {
		t.Fatal("Set with UNSPECIFIED should fail")
	}

	// Test Set to MINIMAL.
	setRes, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL,
	}))
	if err != nil {
		t.Fatalf("Set MINIMAL: %v", err)
	}
	if setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("Set response notch: got %v, want MINIMAL", setRes.Msg.GetNotch())
	}

	// Verify callbacks, monitor state, and disk persistence.
	if setPolicyCalled != 1 {
		t.Errorf("setPolicy called %d times, want 1", setPolicyCalled)
	}
	if updatedPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("updatedPolicy notch: got %v, want MINIMAL", updatedPolicy.Notch)
	}
	if broadcastCalled != 1 {
		t.Errorf("broadcast called %d times, want 1", broadcastCalled)
	}
	if broadcastedNotch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("broadcasted notch: got %v, want MINIMAL", broadcastedNotch)
	}
	if monitor.Policy().Notch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("monitor policy notch: got %v, want MINIMAL", monitor.Policy().Notch)
	}
	if diskNotch := ReadNotch(home); diskNotch != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("disk notch: got %v, want MINIMAL", diskNotch)
	}

	// Test Get after Set.
	getRes2, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if getRes2.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MINIMAL {
		t.Errorf("Get after Set: got %v, want MINIMAL", getRes2.Msg.GetNotch())
	}
}

func TestLoadServiceWatch(t *testing.T) {
	restore := SetWatchInterval(30 * time.Millisecond)
	t.Cleanup(restore)

	home := t.TempDir()
	initPolicy := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	monitor := NewMonitor(initPolicy, func() (Snapshot, error) {
		return Snapshot{
			MemoryTotal:     16 * GiB,
			MemoryAvailable: 10 * GiB,
		}, nil
	})

	var engagedMem atomic.Uint64
	var workerMem atomic.Uint64
	engagedMem.Store(3 * GiB)
	workerMem.Store(1 * GiB)

	workerMemory := func(ctx context.Context, p Policy) (uint64, uint64, error) {
		return engagedMem.Load(), workerMem.Load(), nil
	}

	mux := http.NewServeMux()
	mux.Handle(LoadHandler(home, monitor, nil, nil, workerMemory))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stream, err := client.Watch(ctx, connect.NewRequest(&djinnv1.LoadServiceWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	recv := make(chan *djinnv1.LoadServiceWatchResponse, 10)
	recvErr := make(chan error, 1)
	go func() {
		for stream.Receive() {
			recv <- stream.Msg()
		}
		recvErr <- stream.Err()
	}()

	// 1. Initial message sent immediately
	var first *djinnv1.LoadServiceWatchResponse
	select {
	case first = <-recv:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial watch response")
	}

	if first.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("initial notch: got %v, want MEDIUM", first.GetNotch())
	}
	if first.GetMemoryTotalBytes() != 16*GiB {
		t.Errorf("initial total: got %v, want 16 GiB", first.GetMemoryTotalBytes())
	}
	if first.GetMemoryAvailableBytes() != 10*GiB {
		t.Errorf("initial available: got %v, want 10 GiB", first.GetMemoryAvailableBytes())
	}
	if first.GetEngagedMemoryBytes() != 3*GiB {
		t.Errorf("initial engaged: got %v, want 3 GiB", first.GetEngagedMemoryBytes())
	}
	if first.GetWorkerMemoryBytes() != 1*GiB {
		t.Errorf("initial worker: got %v, want 1 GiB", first.GetWorkerMemoryBytes())
	}

	// 2. Deduplication: no changes -> no messages sent across ticks (nothing sent when nothing changes)
	select {
	case msg := <-recv:
		t.Fatalf("unexpected message without changes: %v", msg)
	case <-time.After(80 * time.Millisecond):
		// Expected: nothing sent
	}

	// 3. Set notch triggers rate-limited update
	_, err = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
	}))
	if err != nil {
		t.Fatalf("Set HIGH: %v", err)
	}

	var second *djinnv1.LoadServiceWatchResponse
	select {
	case second = <-recv:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for update after Set")
	}
	if second.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Errorf("second notch: got %v, want HIGH", second.GetNotch())
	}

	// 4. Memory change detected on periodic tick
	engagedMem.Store(5 * GiB)
	workerMem.Store(2 * GiB)

	var third *djinnv1.LoadServiceWatchResponse
	select {
	case third = <-recv:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for update after memory change")
	}
	if third.GetEngagedMemoryBytes() != 5*GiB || third.GetWorkerMemoryBytes() != 2*GiB {
		t.Errorf("third memory: got engaged=%v worker=%v, want 5 GiB and 2 GiB",
			third.GetEngagedMemoryBytes(), third.GetWorkerMemoryBytes())
	}

	// 5. Rate limiting: rapid changes within interval are throttled
	t0 := time.Now()
	_, _ = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_LIGHT}))
	_, _ = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX}))

	var fourth *djinnv1.LoadServiceWatchResponse
	select {
	case fourth = <-recv:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fourth message")
	}
	if fourth.GetNotch() == djinnv1.LoadNotch_LOAD_NOTCH_UNSPECIFIED {
		t.Error("fourth notch unspecified")
	}
	elapsed := time.Since(t0)
	if elapsed < 20*time.Millisecond {
		t.Errorf("update arrived too fast (%v), expected rate limit around 30ms", elapsed)
	}

	// 6. Context cancellation ends stream
	cancel()
	select {
	case <-recvErr:
	case <-time.After(time.Second):
		t.Fatal("stream did not terminate after context cancel")
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
