package machine

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	"github.com/empowill/djinn/gen/go/djinn/v1/djinnv1connect"
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
	mux.Handle(LoadHandler(home, monitor, setPolicy, broadcast))
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

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
