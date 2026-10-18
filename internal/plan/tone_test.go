package plan

import (
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/lead/tones"
)

// TestShownThemeKept: the theme a page said is kept in settings.json beside the other settings; an unknown one keeps
// it, and with none said the Djinn has no tone.
func TestShownThemeKept(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got := ReadShownTheme(home); got != uiv1.ShownTheme_SHOWN_THEME_UNSPECIFIED || Tone(got) != "" || toneSection(got) != "" {
		t.Errorf("no page said it: %v, tone %q", got, Tone(got))
	}
	if err := changeSettings(home, func(s *uiv1.Settings) error {
		s.Lead = &planv1.LeadSeat{Provider: planv1.Provider_PROVIDER_CODEX, Model: "gpt-6"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ set, want uiv1.ShownTheme }{
		{uiv1.ShownTheme_SHOWN_THEME_DARK, uiv1.ShownTheme_SHOWN_THEME_DARK},
		{uiv1.ShownTheme_SHOWN_THEME_UNSPECIFIED, uiv1.ShownTheme_SHOWN_THEME_DARK},
		{uiv1.ShownTheme_SHOWN_THEME_LIGHT, uiv1.ShownTheme_SHOWN_THEME_LIGHT},
	} {
		if err := SetShownTheme(home, step.set); err != nil {
			t.Fatal(err)
		}
		if got := ReadShownTheme(home); got != step.want {
			t.Errorf("set %v: kept %v, want %v", step.set, got, step.want)
		}
	}
	if ReadLeadSeat(home).GetModel() != "gpt-6" {
		t.Errorf("the lead's seat is lost: %v", ReadLeadSeat(home))
	}
	light, dark := toneSection(uiv1.ShownTheme_SHOWN_THEME_LIGHT), toneSection(uiv1.ShownTheme_SHOWN_THEME_DARK)
	if !strings.Contains(light, "light theme") || !strings.HasSuffix(light, tones.Light()) || strings.Contains(light, tones.Dark()) {
		t.Errorf("light: %q", light)
	}
	if !strings.Contains(dark, "dark theme") || !strings.HasSuffix(dark, tones.Dark()) || strings.Contains(dark, tones.Light()) {
		t.Errorf("dark: %q", dark)
	}
}
