package plan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
)

// orderText names each sign-in of order: "claude", "claude/work".
func orderText(order []SignIn) []string {
	var out []string
	for _, s := range order {
		name := providerWord(s.Provider)
		if s.Name != "" {
			name += "/" + s.Name
		}
		out = append(out, name)
	}
	return out
}

// TestAccountOrder: without an order written, each agent's own sign-in comes first, then its accounts as added; a
// whole order written is kept, an account added since goes at the bottom, a forgotten one leaves it; an order that
// names an unknown sign-in, or one twice, is refused; SignIns reads the order of one agent.
func TestAccountOrder(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settings := &uiv1.Settings{
		Accounts: []*planv1.Account{
			{Name: "work", Provider: planv1.Provider_PROVIDER_CLAUDE},
			{Name: "gpt", Provider: planv1.Provider_PROVIDER_CODEX},
			{Name: "perso", Provider: planv1.Provider_PROVIDER_CLAUDE, Paused: true},
		},
		PausedSignIns: []planv1.Provider{planv1.Provider_PROVIDER_CODEX},
	}
	data, err := protojson.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, settingsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := orderText(AccountOrder(home)), []string{"claude", "claude/work", "claude/perso", "codex", "codex/gpt",
		"antigravity"}; !slices.Equal(got, want) {
		t.Errorf("order by default = %v, want %v", got, want)
	}

	claude := func(name string) *uiv1.SignInRef {
		return &uiv1.SignInRef{Provider: planv1.Provider_PROVIDER_CLAUDE, Account: name}
	}
	written, err := WriteAccountOrder(home, []*uiv1.SignInRef{claude("perso"), {Provider: planv1.Provider_PROVIDER_CODEX}, claude("")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude/perso", "codex", "claude", "claude/work", "codex/gpt", "antigravity"}
	if got := orderText(written); !slices.Equal(got, want) {
		t.Errorf("written = %v, want %v", got, want)
	}
	if got := orderText(AccountOrder(home)); !slices.Equal(got, want) {
		t.Errorf("read back = %v, want %v", got, want)
	}
	signIns := SignIns(home, planv1.Provider_PROVIDER_CLAUDE)
	if got := orderText(signIns); !slices.Equal(got, []string{"claude/perso", "claude", "claude/work"}) || !signIns[0].Paused {
		t.Errorf("claude's sign-ins = %v, %+v", got, signIns)
	}
	if codex := SignIns(home, planv1.Provider_PROVIDER_CODEX); !codex[0].Paused || codex[0].Name != "" {
		t.Errorf("codex's own pause lost: %+v", codex)
	}

	if err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Accounts = append(s.Accounts, &planv1.Account{Name: "late", Provider: planv1.Provider_PROVIDER_CLAUDE})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := orderText(AccountOrder(home)); got[len(got)-1] != "claude/late" {
		t.Errorf("an account added since: %v, want it at the bottom", got)
	}

	for _, bad := range [][]*uiv1.SignInRef{
		{claude("nobody")},
		{{Provider: planv1.Provider_PROVIDER_CODEX, Account: "work"}},
		{claude("work"), claude("work")},
	} {
		if _, err := WriteAccountOrder(home, bad); err == nil {
			t.Errorf("order %v written", bad)
		}
	}
	if got := orderText(AccountOrder(home)); got[0] != "claude/perso" {
		t.Errorf("a refused order changed it: %v", got)
	}
}
