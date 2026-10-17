// Package tones holds the Djinn's two tones (decision Q36, W156): the voice of the lead Djinn drives, by the theme the
// window shows. One file per theme, one line each, built into djinn: light.md is the solar Djinn, dark.md the mocking
// demon of the lamp. The rule that bounds them (only the tone changes, never the facts) is in the driven lead's rules
// (plan.DrivenRules); each batch's message carries the one tone of the theme shown as it starts.
package tones

import (
	_ "embed"
	"strings"
)

var (
	//go:embed light.md
	light string
	//go:embed dark.md
	dark string
)

// Light is the solar Djinn's tone, for the light theme.
func Light() string { return strings.TrimSpace(light) }

// Dark is the mocking demon's tone, for the dark theme.
func Dark() string { return strings.TrimSpace(dark) }
