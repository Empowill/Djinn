package main

// The leads come back after a crash. While djinn up runs, RestartFile notes the lead terminals that run, written each
// time one starts or ends. A deliberate stop (Quit in the tray, Ctrl+Q, a signal) removes it; an update replaces it
// with its own note. A djinn that crashed leaves it, and the next djinn up reopens those leads on their sessions, as
// after an update (resumeTerminals).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// leadNote keeps RestartFile current with the leads that run.
type leadNote struct {
	path  string
	terms *terminal.Manager
	say   io.Writer

	mu        sync.Mutex
	done      bool // djinn up stops, or restarts: the note no longer follows the terminals
	restarted bool // an update wrote its note in place: it stays
}

// newLeadNote returns the note of the leads in home. It follows the terminals once terms is set and update runs.
func newLeadNote(home string, say io.Writer) *leadNote {
	return &leadNote{path: filepath.Join(home, RestartFile), say: say}
}

// update writes the leads that run, or removes the note when none runs. It is terminal.Config.Changed.
func (n *leadNote) update() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done || n.terms == nil {
		return
	}
	var note restartNote
	for _, t := range n.terms.Running() {
		if strings.HasPrefix(t.Name, "lead-") {
			note.Terminals = append(note.Terminals, restartTerminal{Name: t.Name, Command: resumable(t.Command), Directory: t.Dir})
		}
	}
	if len(note.Terminals) == 0 {
		if err := os.Remove(n.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintln(n.say, "djinn:", err)
		}
		return
	}
	if err := n.write(note); err != nil {
		fmt.Fprintln(n.say, "djinn: note the leads:", err)
	}
}

// restart writes the note of an update in place of the leads': it stays, whatever the terminals do after.
func (n *leadNote) restart(note restartNote) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.write(note); err != nil {
		return err
	}
	n.done, n.restarted = true, true
	return nil
}

// freeze stops following the terminals, and leaves the note as it is: what djinn up does when it stops on an
// error, before its terminals hang up.
func (n *leadNote) freeze() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.done = true
}

// quit stops following the terminals and removes the note, unless an update wrote it: djinn up stops as asked.
func (n *leadNote) quit() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.done = true
	if n.restarted {
		return
	}
	if err := os.Remove(n.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(n.say, "djinn:", err)
	}
}

// write writes note atomically; n.mu is held.
func (n *leadNote) write(note restartNote) error {
	data, err := json.MarshalIndent(note, "", "  ")
	if err != nil {
		return err
	}
	return ui.WriteAtomic(n.path, data)
}
