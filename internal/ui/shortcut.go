package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/internal/store"
)

const settingsFile = "settings.json"

// errChord marks a chord Djinn does not take.
var errChord = errors.New("invalid chord")

// FlightPlan is the wish id that shows the flight plan of the active wishes: PLAN in src/wish-app.tsx. A wish's id is
// a UUID, never this.
const FlightPlan = "plan"

// Registrar takes global shortcuts from the system: the window's (Wails), or a fake in tests. Register calls pressed
// on each press, wherever the focus is; it fails when the system refuses the chord.
type Registrar interface {
	Register(chord string, pressed func()) error
	Unregister(chord string) error
}

// Shortcuts holds the global shortcut that brings the window forward: on the wish with the newest open question,
// else on the flight plan. The chord is a setting, kept in settings.json; it is taken from the system while a
// registrar is set, that is while the native window runs.
type Shortcuts struct {
	// Home is the data directory.
	Home string
	// GOOS is the system the chords are written for; empty for this one.
	GOOS string
	// Store finds the open questions.
	Store *store.Store
	// Show brings the window forward on a wish, or on FlightPlan.
	Show func(wishID string)

	mu        sync.Mutex
	registrar Registrar
	taken     string // the chord registered; empty for none
	problem   string // why the chord chosen is not taken
}

// DefaultChord is the chord of a system until one is chosen. Ctrl+Alt+Space is free in GNOME (checked on GNOME 42),
// and supposed free in KDE Plasma and Windows. On macOS, Ctrl+Option+Space is VoiceOver's, and Cmd+Option+Space
// Finder's search: Ctrl+Cmd+J instead.
func DefaultChord(goos string) string {
	if goos == "darwin" {
		return "Ctrl+Cmd+J"
	}
	return "Ctrl+Alt+Space"
}

func (s *Shortcuts) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

// Use takes the chord from the system with r from now on. Nil forgets it, as the window closes: the system takes
// back the chords of an app that stops.
func (s *Shortcuts) Use(r Registrar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r == nil {
		s.registrar, s.taken, s.problem = nil, "", ""
		return
	}
	s.release()
	s.registrar = r
	chord, err := s.load()
	if err != nil {
		log.Printf("djinn: global shortcut: %v", err)
	}
	s.take(chord)
}

// State is the shortcut as it is now.
func (s *Shortcuts) State() *uiv1.Shortcut {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

func (s *Shortcuts) state() *uiv1.Shortcut {
	chord, _ := s.load()
	return &uiv1.Shortcut{
		Chord: chord, DefaultChord: DefaultChord(s.goos()), Available: s.registrar != nil, Problem: s.problem,
	}
}

// Set chooses chord, as NormalizeChord reads it; empty turns the shortcut off. It is kept even when the system
// refuses it: State says why, and the next start tries again.
func (s *Shortcuts) Set(chord string) (*uiv1.Shortcut, error) {
	chord, err := NormalizeChord(chord, s.goos())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.read()
	if err != nil {
		log.Printf("djinn: %v; written again", err)
		settings = &uiv1.Settings{}
	}
	settings.Shortcut = &chord
	data, err := protojson.Marshal(settings)
	if err != nil {
		return nil, err
	}
	if err := WriteAtomic(filepath.Join(s.Home, settingsFile), data); err != nil {
		return nil, fmt.Errorf("save the settings: %w", err)
	}
	s.release()
	s.take(chord)
	return s.state(), nil
}

// Refused says that the system refused the chord after Register returned: on Wayland, the desktop's portal answers
// later, and may have no global shortcuts at all.
func (s *Shortcuts) Refused(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken != "" {
		s.problem = err.Error()
	}
}

// read is the settings file; empty when there is none.
func (s *Shortcuts) read() (*uiv1.Settings, error) {
	settings := &uiv1.Settings{}
	data, err := os.ReadFile(filepath.Join(s.Home, settingsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return settings, nil
	}
	if err == nil {
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, settings)
	}
	if err != nil {
		return nil, fmt.Errorf("read the settings: %w", err)
	}
	return settings, nil
}

// load is the chord chosen: the default one until one is. A chord the file holds but Djinn does not take is off.
func (s *Shortcuts) load() (string, error) {
	settings, err := s.read()
	if err != nil {
		return "", err
	}
	if settings.Shortcut == nil {
		return DefaultChord(s.goos()), nil
	}
	return NormalizeChord(settings.GetShortcut(), s.goos())
}

// take registers chord, when a registrar is set; s.mu is held.
func (s *Shortcuts) take(chord string) {
	s.problem = ""
	if s.registrar == nil || chord == "" {
		return
	}
	if err := s.registrar.Register(chord, s.pressed); err != nil {
		s.problem = err.Error()
		log.Printf("djinn: global shortcut %s: %v", chord, err)
		return
	}
	s.taken = chord
}

// release gives back the chord registered; s.mu is held.
func (s *Shortcuts) release() {
	if s.taken == "" {
		return
	}
	if s.registrar != nil {
		if err := s.registrar.Unregister(s.taken); err != nil {
			log.Printf("djinn: global shortcut %s: %v", s.taken, err)
		}
	}
	s.taken = ""
}

// pressed brings the window forward on what waits.
func (s *Shortcuts) pressed() {
	if s.Show == nil {
		return
	}
	target, err := Summoned(context.Background(), s.Store)
	if err != nil {
		log.Printf("djinn: global shortcut: %v", err)
	}
	s.Show(target)
}

// Summoned is what the global shortcut shows: the active wish with the newest question that waits for the developer,
// else the flight plan. A question being investigated waits for the lead, not for the developer.
func Summoned(ctx context.Context, db *store.Store) (string, error) {
	if db == nil {
		return FlightPlan, nil
	}
	wishes, err := store.List[*planv1.Wish](ctx, db, nil)
	if err != nil {
		return FlightPlan, err
	}
	var newest *planv1.Question
	for _, w := range wishes {
		if w.GetState() != planv1.WishState_WISH_STATE_ACTIVE {
			continue
		}
		questions, err := store.List[*planv1.Question](ctx, db, store.Where{"wish_id": w.GetId()})
		if err != nil {
			return FlightPlan, err
		}
		for _, q := range questions {
			if !waiting(q) {
				continue
			}
			if newest == nil || q.GetCreateTime().AsTime().After(newest.GetCreateTime().AsTime()) {
				newest = q
			}
		}
	}
	if newest == nil {
		return FlightPlan, nil
	}
	return newest.GetWishId(), nil
}

// waiting tells that q waits for the developer's answer.
func waiting(q *planv1.Question) bool {
	if q.GetAnswer() != nil {
		return false
	}
	rounds := q.GetRounds()
	return len(rounds) == 0 || rounds[len(rounds)-1].GetKind() != planv1.RoundKind_ROUND_KIND_ENLIGHTEN
}

// modifiers are the names a chord's modifiers may have, lowercase, and the name each one is written with. Cmd is
// macOS's Command key; elsewhere it is Ctrl, as Wails reads it.
var modifiers = map[string]string{
	"ctrl":    "Ctrl",
	"control": "Ctrl",
	"alt":     "Alt",
	"option":  "Alt",
	"shift":   "Shift",
	"cmd":     "Cmd",
	"command": "Cmd",
	"super":   "Super",
	"meta":    "Super",
	"win":     "Super",
}

// order is the order a chord's modifiers are written in.
var order = []string{"Ctrl", "Alt", "Shift", "Cmd", "Super"}

// NormalizeChord checks a chord and writes it the one way Djinn shows it: modifiers in order, then the key, such as
// Ctrl+Alt+Space; on macOS Alt is written Option. A chord needs Ctrl, Alt, Cmd or Super, so that it never takes a key
// from typing, and one key: a letter, a digit, F1 to F12, or Space. Empty stays empty: off.
func NormalizeChord(chord, goos string) (string, error) {
	chord = strings.TrimSpace(chord)
	if chord == "" {
		return "", nil
	}
	parts := strings.Split(chord, "+")
	key, ok := chordKey(strings.TrimSpace(parts[len(parts)-1]))
	if !ok {
		return "", fmt.Errorf("%w: %q: the key must be a letter, a digit, F1 to F12 or Space", errChord, parts[len(parts)-1])
	}
	held := map[string]bool{}
	for _, p := range parts[:len(parts)-1] {
		name, ok := modifiers[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return "", fmt.Errorf("%w: %q is not a modifier: use Ctrl, Alt, Shift, Cmd or Super", errChord, p)
		}
		if name == "Cmd" && goos != "darwin" {
			name = "Ctrl"
		}
		held[name] = true
	}
	if !held["Ctrl"] && !held["Alt"] && !held["Cmd"] && !held["Super"] {
		return "", fmt.Errorf("%w: it needs Ctrl, Alt, Cmd or Super, or it would take a key from typing", errChord)
	}
	out := make([]string, 0, len(held)+1)
	for _, name := range order {
		if !held[name] {
			continue
		}
		if name == "Alt" && goos == "darwin" {
			name = "Option"
		}
		out = append(out, name)
	}
	return strings.Join(append(out, key), "+"), nil
}

// chordKey is the key of a chord as it is written, and whether Djinn takes it.
func chordKey(k string) (string, bool) {
	switch {
	case strings.EqualFold(k, "space"):
		return "Space", true
	case len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z' || k[0] >= '0' && k[0] <= '9'):
		return strings.ToUpper(k), true
	case len(k) >= 2 && len(k) <= 3 && (k[0] == 'f' || k[0] == 'F'):
		var n int
		if _, err := fmt.Sscanf(k[1:], "%d", &n); err == nil && fmt.Sprint(n) == k[1:] && n >= 1 && n <= 12 {
			return fmt.Sprintf("F%d", n), true
		}
	}
	return "", false
}
