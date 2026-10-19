package plan

import (
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/lead/tones"
)

// The Djinn's tone follows the window's theme (decision Q36, W156): the page says the theme it shows, the system's
// resolved (UiService.View), Djinn keeps the last one said in settings.json, and each batch of the driven lead reads it
// as it starts: its message carries the one tone of that theme (lead/tones), its rules the limits of every tone
// (DrivenRules). No theme said yet, no tone: the Djinn stays plain.

// ReadShownTheme is the theme the window shows, as the last page said it, from settings.json in home; unspecified when
// none did.
func ReadShownTheme(home string) uiv1.ShownTheme {
	return readSettings(home).GetShownTheme()
}

// SetShownTheme keeps theme as the one the window shows, in settings.json in home; it writes only a change.
func SetShownTheme(home string, theme uiv1.ShownTheme) error {
	if home == "" || theme == uiv1.ShownTheme_SHOWN_THEME_UNSPECIFIED || ReadShownTheme(home) == theme {
		return nil
	}
	return changeSettings(home, func(settings *uiv1.Settings) error {
		settings.ShownTheme = theme
		return nil
	})
}

// Tone is the Djinn's tone in theme: the solar Djinn's in light, the demon's in dark; empty when the theme is not
// known.
func Tone(theme uiv1.ShownTheme) string {
	switch theme {
	case uiv1.ShownTheme_SHOWN_THEME_LIGHT:
		return tones.Light()
	case uiv1.ShownTheme_SHOWN_THEME_DARK:
		return tones.Dark()
	}
	return ""
}

// toneSection is the part of a batch's message that gives its tone, in theme; empty when the theme is not known.
func toneSection(theme uiv1.ShownTheme) string {
	tone := Tone(theme)
	if tone == "" {
		return ""
	}
	name := "dark"
	if theme == uiv1.ShownTheme_SHOWN_THEME_LIGHT {
		name = "light"
	}
	return "## Your tone\n\nThe window shows its " + name + " theme: speak in this tone, within your rule.\n\n" + tone
}
