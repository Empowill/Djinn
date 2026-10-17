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

	// Write an old saved "light" setting and ensure ReadNotch maps it to MEDIUM.
	settingsPath := filepath.Join(home, settingsFile)
	if err := os.WriteFile(settingsPath, []byte(`{"shortcut":"Ctrl+J","load":"LOAD_NOTCH_LIGHT"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("ReadNotch with saved light: got %v, want MEDIUM", got)
	}

	// Also test numeric value 2 for old saved light.
	if err := os.WriteFile(settingsPath, []byte(`{"load":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM {
		t.Errorf("ReadNotch with saved numeric 2 (light): got %v, want MEDIUM", got)
	}

	// Overwrite with OVERCLOCK and verify shortcut preservation.
	if err := os.WriteFile(settingsPath, []byte(`{"shortcut":"Ctrl+J"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteNotch(home, djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK); err != nil {
		t.Fatalf("WriteNotch OVERCLOCK: %v", err)
	}
	if got := ReadNotch(home); got != djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK {
		t.Errorf("ReadNotch after OVERCLOCK write: got %v, want OVERCLOCK", got)
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
	if getRes.Msg.GetWorkerPercentile() != 80 || getRes.Msg.GetWorkerEngagedAt() != "engaged at p80 (medium)" {
		t.Errorf("Get percentile: p%d, %q", getRes.Msg.GetWorkerPercentile(), getRes.Msg.GetWorkerEngagedAt())
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
	if getRes2.Msg.GetWorkerPercentile() != 95 || getRes2.Msg.GetWorkerEngagedAt() != "engaged at p95 (minimal)" {
		t.Errorf("Get after Set percentile: p%d, %q", getRes2.Msg.GetWorkerPercentile(), getRes2.Msg.GetWorkerEngagedAt())
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

func TestLoadServiceAuto(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()

	fakeNow := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fakeNow }
	outside := Outside{CPU: 60, CPUKnown: true}
	source := func(time.Time) (Outside, bool) { return outside, true }

	monitor := &Monitor{policy: NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)}
	var latestPolicy Policy
	var latestBroadcast djinnv1.LoadNotch
	s := &loadService{}
	mux := http.NewServeMux()
	mux.Handle(LoadHandler(
		home,
		monitor,
		func(p Policy) { latestPolicy = p },
		func(n djinnv1.LoadNotch) { latestBroadcast = n },
		nil,
		WithLoadClock(clock),
		WithLoadOutside(source),
		WithLoadTickInterval(0), // manual beats in the test
		func(ls *loadService) { s = ls },
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

	getRes, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM || getRes.Msg.GetAuto() || getRes.Msg.GetAutoReason() != nil {
		t.Fatalf("Initial Get: %v", getRes.Msg)
	}

	// Max chosen, then Auto: it starts from Max, the next beats deciding; nothing measured yet.
	if _, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX})); err != nil {
		t.Fatal(err)
	}
	setRes, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_AUTO}))
	if err != nil {
		t.Fatalf("Set AUTO: %v", err)
	}
	if !setRes.Msg.GetAuto() || setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX || setRes.Msg.GetChosenNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Fatalf("Set AUTO: %v, want auto at Max", setRes.Msg)
	}
	if ReadNotch(home) != djinnv1.LoadNotch_LOAD_NOTCH_AUTO {
		t.Errorf("saved notch %v, want auto", ReadNotch(home))
	}

	// A build outside Djinn: one notch down once it stayed 3 minutes above the high mark, from the first beat on,
	// applied to the policy and broadcast.
	for range 10 {
		fakeNow = fakeNow.Add(AutoBeat)
		s.Tick()
	}
	if latestPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_HIGH || !latestPolicy.Auto || latestBroadcast != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Errorf("after 3 min outside: policy %v auto %v, broadcast %v, want High", latestPolicy.Notch, latestPolicy.Auto, latestBroadcast)
	}
	getRes, err = client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	reason := getRes.Msg.GetAutoReason()
	if getRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_HIGH || reason.GetOutsidePercent() != 60 ||
		reason.GetStep() != djinnv1.LoadAutoStep_LOAD_AUTO_STEP_LOWER || reason.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM ||
		reason.GetInSeconds() != 180 {
		t.Errorf("Get in auto: %v, want High, outside 60 %%, down to Medium in 3 min", getRes.Msg)
	}
	if w := s.snapshot(ctx); w.GetAutoReason().GetOutsidePercent() != 60 {
		t.Errorf("Watch: reason %v", w.GetAutoReason())
	}

	// A manual notch leaves auto mode: no reason any more.
	setRes, err = client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{Notch: djinnv1.LoadNotch_LOAD_NOTCH_MAX}))
	if err != nil {
		t.Fatalf("Set MAX: %v", err)
	}
	if setRes.Msg.GetAuto() || setRes.Msg.GetNotch() != djinnv1.LoadNotch_LOAD_NOTCH_MAX || latestPolicy.Notch != djinnv1.LoadNotch_LOAD_NOTCH_MAX {
		t.Errorf("Set MAX: %v, policy %v", setRes.Msg, latestPolicy.Notch)
	}
	getRes, err = client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getRes.Msg.GetAuto() || getRes.Msg.GetAutoReason() != nil {
		t.Errorf("Get after manual set: %v", getRes.Msg)
	}
}

func TestAutoStartRunsTheNotchShown(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, settingsFile), []byte(`{"load":"LOAD_NOTCH_AUTO"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	monitor := NewMonitor(NotchPolicy(ReadNotch(home)), func() (Snapshot, error) {
		return Snapshot{Cores: 16}, nil
	})
	var calls []djinnv1.LoadNotch
	var lastPolicy Policy
	mux := http.NewServeMux()
	mux.Handle(LoadHandler(
		home,
		monitor,
		func(p Policy) {
			calls = append(calls, p.Notch)
			lastPolicy = p
		},
		nil,
		nil,
		WithLoadTickInterval(0),
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

	res, err := client.Get(ctx, connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp := res.Msg
	var barSlots int32
	for _, ns := range resp.GetNotchSlots() {
		if ns.GetNotch() == resp.GetNotch() {
			barSlots = ns.GetSlots()
			break
		}
	}
	if resp.GetNotch() != monitor.Policy().Notch || barSlots != resp.GetSlots() || len(calls) == 0 {
		t.Fatalf("the bar shows %v (%d slots), the scheduler runs %v (%d slots); setPolicy calls: %v",
			resp.GetNotch(), barSlots, monitor.Policy().Notch, resp.GetSlots(), calls)
	}
	if !monitor.Policy().Auto || !lastPolicy.Auto {
		t.Errorf("policy Auto: monitor=%v, setPolicy=%v, want true", monitor.Policy().Auto, lastPolicy.Auto)
	}
}

func TestApplyNotchKeepsWorkerMemoryGuard(t *testing.T) {
	ctx := t.Context()
	home := t.TempDir()

	initPolicy := NotchPolicy(djinnv1.LoadNotch_LOAD_NOTCH_MEDIUM)
	initPolicy.Workers = 5
	initPolicy.WorkerMemory = 4 * GiB
	initPolicy.WorkerMemoryGuard = 1.75
	monitor := NewMonitor(initPolicy, func() (Snapshot, error) {
		return Snapshot{Cores: 16}, nil
	})

	var recorded Policy
	mux := http.NewServeMux()
	mux.Handle(LoadHandler(
		home,
		monitor,
		func(p Policy) { recorded = p },
		nil,
		nil,
		WithLoadTickInterval(0),
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := djinnv1connect.NewLoadServiceClient(srv.Client(), srv.URL)

	if _, err := client.Set(ctx, connect.NewRequest(&djinnv1.LoadServiceSetRequest{
		Notch: djinnv1.LoadNotch_LOAD_NOTCH_HIGH,
	})); err != nil {
		t.Fatalf("Set HIGH: %v", err)
	}

	got := monitor.Policy()
	if got.Workers != 5 || got.WorkerMemory != 4*GiB || got.WorkerMemoryGuard != 1.75 {
		t.Errorf("monitor policy overrides after Set: Workers=%d WorkerMemory=%d WorkerMemoryGuard=%v, want 5, %d, 1.75",
			got.Workers, got.WorkerMemory, got.WorkerMemoryGuard, 4*GiB)
	}
	if recorded.Workers != 5 || recorded.WorkerMemory != 4*GiB || recorded.WorkerMemoryGuard != 1.75 {
		t.Errorf("setPolicy overrides after Set: Workers=%d WorkerMemory=%d WorkerMemoryGuard=%v, want 5, %d, 1.75",
			recorded.Workers, recorded.WorkerMemory, recorded.WorkerMemoryGuard, 4*GiB)
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
