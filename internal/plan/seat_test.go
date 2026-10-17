package plan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// An account's sign-in command and environment name its folder, for each agent and shell; Antigravity has none.
func TestAccountLoginAndEnv(t *testing.T) {
	t.Parallel()
	work := &planv1.Account{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE, ConfigDirectory: "/home/d/.claude-work"}
	odd := &planv1.Account{Name: "odd", Provider: planv1.Provider_PROVIDER_CODEX, ConfigDirectory: "/home/d/my codex's"}
	for _, tt := range []struct {
		a          *planv1.Account
		goos, want string
	}{
		{work, "linux", "CLAUDE_CONFIG_DIR=/home/d/.claude-work claude auth login"},
		{work, "windows", `set "CLAUDE_CONFIG_DIR=/home/d/.claude-work" && claude auth login`},
		{odd, "darwin", `CODEX_HOME='/home/d/my codex'\''s' codex login`},
		{&planv1.Account{Provider: planv1.Provider_PROVIDER_ANTIGRAVITY}, "linux", ""},
	} {
		if got := AccountLogin(tt.a, tt.goos); got != tt.want {
			t.Errorf("AccountLogin(%v, %s) = %q; want %q", tt.a, tt.goos, got, tt.want)
		}
	}
	if env := AccountEnv(work); !slices.Equal(env, []string{"CLAUDE_CONFIG_DIR=/home/d/.claude-work",
		"ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=", "CLAUDE_CODE_OAUTH_TOKEN="}) {
		t.Errorf("claude's env = %v", env)
	}
	if env := AccountEnv(odd); !slices.Equal(env, []string{"CODEX_HOME=/home/d/my codex's"}) {
		t.Errorf("codex's env = %v", env)
	}
	if env := AccountEnv(nil); env != nil {
		t.Errorf("no account's env = %v", env)
	}
}

// The seat and the accounts stay in settings.json beside the other settings, which a change keeps.
func TestSeatKeepsOtherSettings(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, settingsFile), []byte(`{"shortcut": "Ctrl+Alt+D", "leadDriven": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if seat := ReadLeadSeat(home); seat.GetProvider() != planv1.Provider_PROVIDER_CLAUDE || seat.GetAccount() != "" {
		t.Errorf("default seat = %v", seat)
	}
	err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Lead = &planv1.LeadSeat{Provider: planv1.Provider_PROVIDER_CODEX, Model: "gpt-5.5"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := readSettings(home)
	if settings.GetShortcut() != "Ctrl+Alt+D" || !settings.GetLeadDriven() || ReadLeadSeat(home).GetModel() != "gpt-5.5" {
		t.Errorf("settings = %v", settings)
	}
	if got := SeatText(ReadLeadSeat(home), nil); got != "Codex, model gpt-5.5" {
		t.Errorf("SeatText = %q", got)
	}
}
