package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// fakeRegistrar takes chords as a system would, without touching the keyboard: what it holds, and what it refuses.
type fakeRegistrar struct {
	mu      sync.Mutex
	held    map[string]func()
	refuse  map[string]bool
	history []string
}

func (f *fakeRegistrar) Register(chord string, pressed func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, "+"+chord)
	if f.refuse[chord] {
		return errors.New("another application holds " + chord)
	}
	if f.held == nil {
		f.held = map[string]func(){}
	}
	f.held[chord] = pressed
	return nil
}

func (f *fakeRegistrar) Unregister(chord string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, "-"+chord)
	delete(f.held, chord)
	return nil
}

// holds is the chords held, sorted.
func (f *fakeRegistrar) holds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for chord := range f.held {
		out = append(out, chord)
	}
	slices.Sort(out)
	return out
}

// press presses chord, as the system would.
func (f *fakeRegistrar) press(t *testing.T, chord string) {
	t.Helper()
	f.mu.Lock()
	pressed := f.held[chord]
	f.mu.Unlock()
	if pressed == nil {
		t.Fatalf("%s is not held", chord)
	}
	pressed()
}

func TestNormalizeChord(t *testing.T) {
	for _, c := range []struct{ in, goos, want string }{
		{"", "linux", ""},
		{"  ", "linux", ""},
		{"ctrl+alt+space", "linux", "Ctrl+Alt+Space"},
		{"Alt + Ctrl + j", "linux", "Ctrl+Alt+J"},
		{"Shift+Control+Option+F5", "linux", "Ctrl+Alt+Shift+F5"},
		{"Cmd+Shift+K", "linux", "Ctrl+Shift+K"}, // Wails reads Cmd as Ctrl off macOS.
		{"cmd+ctrl+j", "darwin", "Ctrl+Cmd+J"},
		{"alt+cmd+1", "darwin", "Option+Cmd+1"},
		{"Meta+F12", "windows", "Super+F12"},
	} {
		got, err := NormalizeChord(c.in, c.goos)
		if err != nil || got != c.want {
			t.Errorf("NormalizeChord(%q, %s) = %q, %v; want %q", c.in, c.goos, got, err, c.want)
		}
	}
	for _, in := range []string{"Space", "Shift+A", "Ctrl+Alt", "Ctrl+Alt+Enter", "Ctrl+Alt+F13", "Ctrl+Alt+F0",
		"Hyper+K", "Ctrl+Alt+é", "Ctrl+Alt+F01", "+"} {
		if got, err := NormalizeChord(in, "linux"); !errors.Is(err, errChord) {
			t.Errorf("NormalizeChord(%q) = %q, %v; want an invalid chord", in, got, err)
		}
	}
}

func TestDefaultChord(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		chord := DefaultChord(goos)
		if got, err := NormalizeChord(chord, goos); err != nil || got != chord {
			t.Errorf("%s: the default %q is not written as Djinn writes chords: %q, %v", goos, chord, got, err)
		}
	}
	if DefaultChord("linux") != "Ctrl+Alt+Space" || DefaultChord("darwin") != "Ctrl+Cmd+J" {
		t.Errorf("defaults = %s, %s", DefaultChord("linux"), DefaultChord("darwin"))
	}
}

// Without a window, the chord is a setting and nothing is taken; the window takes the default one, until another is
// chosen, which is kept for the next start.
func TestShortcutsSet(t *testing.T) {
	home := t.TempDir()
	s := &Shortcuts{Home: home, GOOS: "linux"}
	if got := s.State(); got.GetChord() != "Ctrl+Alt+Space" || got.GetAvailable() || got.GetDefaultChord() != "Ctrl+Alt+Space" {
		t.Fatalf("State before the window = %v", got)
	}

	r := &fakeRegistrar{}
	s.Use(r)
	if got := r.holds(); !slices.Equal(got, []string{"Ctrl+Alt+Space"}) {
		t.Fatalf("held = %v", got)
	}
	if got := s.State(); !got.GetAvailable() || got.GetProblem() != "" {
		t.Fatalf("State = %v", got)
	}

	got, err := s.Set("shift+ctrl+alt+k")
	if err != nil || got.GetChord() != "Ctrl+Alt+Shift+K" {
		t.Fatalf("Set = %v, %v", got, err)
	}
	if held := r.holds(); !slices.Equal(held, []string{"Ctrl+Alt+Shift+K"}) {
		t.Fatalf("held after Set = %v", held)
	}
	// A chord Djinn does not take changes nothing.
	if _, err := s.Set("Shift+K"); !errors.Is(err, errChord) {
		t.Fatalf("Set(Shift+K) = %v", err)
	}
	if held := r.holds(); !slices.Equal(held, []string{"Ctrl+Alt+Shift+K"}) {
		t.Fatalf("held after a refused Set = %v", held)
	}

	// The next start takes the chord chosen.
	next := &Shortcuts{Home: home, GOOS: "linux"}
	r2 := &fakeRegistrar{}
	next.Use(r2)
	if held := r2.holds(); !slices.Equal(held, []string{"Ctrl+Alt+Shift+K"}) {
		t.Fatalf("held at the next start = %v", held)
	}

	// Off: nothing held, and off it stays.
	if got, err := s.Set(""); err != nil || got.GetChord() != "" {
		t.Fatalf("Set(off) = %v, %v", got, err)
	}
	if held := r.holds(); len(held) != 0 {
		t.Fatalf("held when off = %v", held)
	}
	off := &Shortcuts{Home: home, GOOS: "linux"}
	r3 := &fakeRegistrar{}
	off.Use(r3)
	if len(r3.history) != 0 || off.State().GetChord() != "" {
		t.Fatalf("an off shortcut was taken: %v, %v", r3.history, off.State())
	}

	// The window closes: the shortcut is no longer available, and nothing is given back by hand.
	next.Use(nil)
	if next.State().GetAvailable() || !slices.Equal(r2.history, []string{"+Ctrl+Alt+Shift+K"}) {
		t.Fatalf("after the window = %v, %v", next.State(), r2.history)
	}
}

// A chord the system refuses is kept, with the reason; a later refusal (Wayland's portal) says it too.
func TestShortcutsRefused(t *testing.T) {
	s := &Shortcuts{Home: t.TempDir(), GOOS: "linux"}
	r := &fakeRegistrar{refuse: map[string]bool{"Ctrl+Alt+Space": true}}
	s.Use(r)
	if got := s.State(); got.GetChord() != "Ctrl+Alt+Space" || !strings.Contains(got.GetProblem(), "another application holds") {
		t.Fatalf("State = %v", got)
	}
	got, err := s.Set("Ctrl+Alt+J")
	if err != nil || got.GetProblem() != "" || !slices.Equal(r.holds(), []string{"Ctrl+Alt+J"}) {
		t.Fatalf("Set = %v, %v; held %v", got, err, r.holds())
	}
	s.Refused(errors.New("global shortcuts: portal did not grant shortcuts (response 1); they will not fire"))
	if got := s.State(); !strings.Contains(got.GetProblem(), "portal did not grant") {
		t.Fatalf("State after a refusal = %v", got)
	}
	// Off, a late refusal says nothing.
	if _, err := s.Set(""); err != nil {
		t.Fatal(err)
	}
	s.Refused(errors.New("global shortcuts: late"))
	if got := s.State(); got.GetProblem() != "" {
		t.Fatalf("State when off = %v", got)
	}
}

// A settings file that says nothing of the shortcut means the default; one Djinn cannot read leaves it off.
func TestShortcutsSettingsFile(t *testing.T) {
	home := t.TempDir()
	s := &Shortcuts{Home: home, GOOS: "darwin"}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, settingsFile), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"later": true}`)
	if got := s.State().GetChord(); got != "Ctrl+Cmd+J" {
		t.Errorf("chord of an empty setting = %q", got)
	}
	write(`{"shortcut": "Hyper+K"}`)
	r := &fakeRegistrar{}
	s.Use(r)
	if got := s.State().GetChord(); got != "" || len(r.history) != 0 {
		t.Errorf("chord of a bad setting = %q, taken %v", got, r.history)
	}
	if _, err := s.Set("option+cmd+k"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, settingsFile))
	if err != nil || !strings.Contains(string(data), `"Option+Cmd+K"`) {
		t.Errorf("settings = %s, %v", data, err)
	}
}

// A press shows the active wish with the newest question that waits for you; with none, the flight plan.
func TestShortcutShowsWhatWaits(t *testing.T) {
	db, err := store.Open(t.Context(), "", plan.Entities()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	shown := make(chan string, 1)
	s := &Shortcuts{Home: t.TempDir(), GOOS: "linux", Store: db, Show: func(id string) { shown <- id }}
	r := &fakeRegistrar{}
	s.Use(r)
	press := func() string {
		t.Helper()
		r.press(t, "Ctrl+Alt+Space")
		select {
		case id := <-shown:
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("nothing shown")
			return ""
		}
	}
	if got := press(); got != FlightPlan {
		t.Fatalf("with no wish, shown %q", got)
	}

	older := makeWish(t, db, "Older", planv1.WishState_WISH_STATE_ACTIVE)
	newer := makeWish(t, db, "Newer", planv1.WishState_WISH_STATE_ACTIVE)
	paused := makeWish(t, db, "Paused", planv1.WishState_WISH_STATE_PAUSED)
	at := time.Now().Add(-time.Hour)
	put := func(wish *planv1.Wish, code string, minutes int, edit func(*planv1.Question)) {
		t.Helper()
		q := &planv1.Question{
			Id: store.NewID(), Code: code, WishId: wish.GetId(), Text: code,
			CreateTime: timestamppb.New(at.Add(time.Duration(minutes) * time.Minute)),
		}
		if edit != nil {
			edit(q)
		}
		if err := db.Tx(t.Context(), func(tx *store.Tx) error { return putJournaled(tx, q) }); err != nil {
			t.Fatal(err)
		}
	}
	// Each one newer than the last, but only Q02 of Newer waits for you: Q01 is answered, Q03 is being investigated,
	// and the wish of Q04 is paused.
	put(older, "Q01", 1, nil)
	put(newer, "Q02", 2, nil)
	put(older, "Q03", 3, func(q *planv1.Question) { q.Answer = &planv1.Answer{Choice: planv1.Choice_CHOICE_YES} })
	put(older, "Q04", 4, func(q *planv1.Question) {
		q.Rounds = []*planv1.Round{{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, Note: "dig"}}
	})
	put(paused, "Q05", 5, nil)
	if got := press(); got != newer.GetId() {
		t.Fatalf("shown %q, want the wish %q", got, newer.GetId())
	}
	// A question the lead revised after investigating waits for you again.
	put(older, "Q06", 6, func(q *planv1.Question) {
		q.Rounds = []*planv1.Round{
			{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN}, {Kind: planv1.RoundKind_ROUND_KIND_REVISE},
		}
	})
	if got := press(); got != older.GetId() {
		t.Fatalf("shown %q, want the wish %q", got, older.GetId())
	}
}

// The service says what the shortcut is, and sets it; without a window it has none.
func TestSetShortcut(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	if _, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "Ctrl+Alt+J"})); code(err) != connect.CodeUnimplemented {
		t.Fatalf("SetShortcut without a window = %v", err)
	}
	env, err := s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if err != nil || env.Msg.GetShortcut() != nil {
		t.Fatalf("GetEnvironment without a window = %v, %v", env, err)
	}

	s.Shortcuts = &Shortcuts{Home: s.Home, GOOS: "linux"}
	s.Shortcuts.Use(&fakeRegistrar{})
	res, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "alt+ctrl+j"}))
	if err != nil || res.Msg.GetShortcut().GetChord() != "Ctrl+Alt+J" {
		t.Fatalf("SetShortcut = %v, %v", res, err)
	}
	if _, err := s.SetShortcut(ctx, connect.NewRequest(&uiv1.UiServiceSetShortcutRequest{Chord: "J"})); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("SetShortcut(J) = %v", err)
	}
	env, err = s.GetEnvironment(ctx, connect.NewRequest(&uiv1.UiServiceGetEnvironmentRequest{}))
	if sc := env.Msg.GetShortcut(); err != nil || sc.GetChord() != "Ctrl+Alt+J" || !sc.GetAvailable() {
		t.Fatalf("GetEnvironment = %v, %v", env, err)
	}
}
