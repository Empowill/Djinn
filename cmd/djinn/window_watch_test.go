package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/empowill/djinn/internal/ui"
	"github.com/empowill/djinn/internal/window"
	"github.com/empowill/djinn/locales"
)

// fakeNotifier keeps the notifications shown.
type fakeNotifier struct{ notes []ui.Notification }

func (f *fakeNotifier) Notify(n ui.Notification) error { f.notes = append(f.notes, n); return nil }

// TestWindowTold covers windowTold and windowNote: what the window's watch found is a line on djinn up's output and a
// notification, in English or French, saying the GPU hang the kernel logged and that the window runs in software
// rendering until Djinn restarts.
func TestWindowTold(t *testing.T) {
	hang := []window.Hang{{Time: time.Date(2026, 10, 17, 21, 26, 55, 0, time.Local), Line: "i915: GPU HANG"}}
	for _, c := range []struct {
		name, language string
		event          window.Event
		want           []string
	}{
		{"silent, loaded again, en", "en", window.Event{After: window.Silence, Did: window.Reloaded}, []string{
			"djinn: Djinn's window stayed black. Its page made no call to Djinn within 10 s. Djinn loaded its page again.\n",
		}},
		{"silent, new view after a GPU hang, en", "en", window.Event{After: window.Silence, Hangs: hang, Did: window.Recreated, Software: true}, []string{
			"The kernel logged a GPU hang or reset at 21:26:55.",
			"Djinn opened the window again in software rendering, slower, until Djinn restarts.\n",
		}},
		{"silent, new view, fr", "fr", window.Event{After: window.Silence, Did: window.Recreated}, []string{
			"djinn: " + locales.T("fr", "window.silent_title", nil) + ". " + locales.T("fr", "window.silent_body", map[string]string{"seconds": "10"}),
			locales.T("fr", "window.recreated", nil) + "\n",
		}},
		{"ended, nothing left", "en", window.Event{Ended: "crashed"}, []string{
			"djinn: Djinn's window lost its page. The process that draws its page ended: it crashed.",
			"It still does not answer: quit Djinn and start it again.",
		}},
		{"terminated, loaded again", "en", window.Event{Ended: "terminated", Did: window.Reloaded}, []string{
			"The process that draws its page ended: WebKit ended it. Djinn loaded its page again.",
		}},
		{"the rescue failed", "en", window.Event{After: window.Silence, Err: errors.New("no display")}, []string{
			"Djinn could not rescue it (no display): quit Djinn and start it again.",
		}},
		{"memory", "en", window.Event{Ended: "memory"}, []string{"it went past its memory limit."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			notes := &fakeNotifier{}
			notices := &ui.Notices{}
			notices.Use(notes)
			windowTold(&out, notices, c.language)(c.event)
			for _, want := range c.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("line %q lacks %q", out.String(), want)
				}
			}
			if len(notes.notes) != 1 || !strings.Contains(out.String(), notes.notes[0].Title+". "+notes.notes[0].Body) {
				t.Errorf("notifications %+v for the line %q", notes.notes, out.String())
			}
		})
	}
}
