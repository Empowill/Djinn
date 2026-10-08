// Package terminal runs programs on pseudo-terminals for the window: a shell, or the lead agent, that the user types
// into as in any terminal. A terminal lives as long as djinn up: a window that closes and opens again finds it, and
// reads its output again from the part still kept in memory. Nothing is written to disk.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// Scrollback is how much output a terminal keeps for a window that attaches later: the last mebibyte at least,
	// a quarter more at most. Enough for many screens of a full-screen program, and bounded whatever runs.
	Scrollback = 1 << 20
	// Grace is how long a program has to end after its terminal hangs up, before it is killed.
	Grace = 3 * time.Second
	// chunk is the largest piece of output sent in one message.
	chunk = 64 << 10
	// drain is how long the output may still come after the program ended, from a child holding the terminal.
	drain = time.Second
)

// grace is Grace, shortened by the tests.
var grace = Grace

// Errors of the manager; the service turns them into Connect codes.
var (
	ErrNotFound = errors.New("no such terminal")
	ErrExited   = errors.New("the program of this terminal has ended")
	ErrClosed   = errors.New("the terminals are closed: djinn is stopping")
	ErrBusy     = errors.New("already running in another terminal")
)

// Config is what djinn up says about the terminals the window opens.
type Config struct {
	// Command runs when the window gives none: program first. Empty: the user's shell.
	Command []string
	// Dir is the working directory when the window gives none. Empty: the home directory.
	Dir string
}

// Manager holds the terminals of a djinn up.
type Manager struct {
	cfg Config

	mu     sync.Mutex
	byID   map[string]*Terminal
	byName map[string]*Terminal
	closed bool
}

// NewManager returns a manager that starts terminals with cfg as defaults.
func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg, byID: map[string]*Terminal{}, byName: map[string]*Terminal{}}
}

// proc is a program on a pseudo-terminal: the master side is read and written here.
type proc interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	resize(cols, rows int) error
	// wait returns the exit code once the program ended, -1 when a signal killed it.
	wait() int
	// hangup asks the program to end, as a terminal that closes does.
	hangup()
	// kill kills what is left of the program.
	kill()
	// close releases the terminal; it may run more than once.
	close()
}

// Terminal is a program running on a pseudo-terminal, and the last part of its output.
type Terminal struct {
	ID      string
	Name    string
	Command []string
	Dir     string

	p    proc
	wmu  sync.Mutex    // keeps the bytes of one write together
	done chan struct{} // closed once the program ended and its output is read
	hang sync.Once

	mu      sync.Mutex
	cols    int
	rows    int
	buf     []byte // the output kept, from offset base on
	base    uint64
	changed chan struct{} // closed and replaced at each change
	exited  bool
	code    int
}

// Open returns the running terminal of name, attached true, or starts one: command in dir at cols×rows, each
// falling back to the defaults of the manager when empty.
func (m *Manager) Open(name string, command []string, dir string, cols, rows int) (t *Terminal, attached bool, err error) {
	return m.OpenExclusive(name, command, dir, cols, rows, "")
}

// OpenExclusive is Open, but refuses with ErrBusy to start a program while a terminal of another name runs a
// command line that holds exclusive: an agent session, which two programs must never resume at once. An empty
// exclusive refuses nothing.
func (m *Manager) OpenExclusive(
	name string, command []string, dir string, cols, rows int, exclusive string,
) (t *Terminal, attached bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	if t := m.byName[name]; t != nil && !t.Exited() {
		return t, true, nil
	}
	if exclusive != "" {
		for _, other := range m.byName {
			if other.Name != name && !other.Exited() && strings.Contains(strings.Join(other.Command, " "), exclusive) {
				return nil, false, fmt.Errorf("%s: %w: %q runs %s", exclusive, ErrBusy, other.Name,
					strings.Join(other.Command, " "))
			}
		}
	}
	if len(command) == 0 {
		command = m.cfg.Command
	}
	if len(command) == 0 {
		command = Shell()
	}
	if dir == "" {
		dir = m.cfg.Dir
	}
	if dir == "" {
		if dir, err = os.UserHomeDir(); err != nil {
			return nil, false, err
		}
	}
	if !filepath.IsAbs(dir) {
		return nil, false, fmt.Errorf("the working directory must be absolute: %s", dir)
	}
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	// The program gets Djinn's environment as it is, which Djinn never reads, and says what terminal it talks to.
	env := append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	p, err := start(command, dir, env, cols, rows)
	if err != nil {
		return nil, false, fmt.Errorf("start %s: %w", command[0], err)
	}
	t = &Terminal{
		ID: uuid.NewString(), Name: name, Command: slices.Clone(command), Dir: dir,
		p: p, done: make(chan struct{}), cols: cols, rows: rows, changed: make(chan struct{}),
	}
	if old := m.byName[name]; old != nil {
		delete(m.byID, old.ID) // Ended: its output goes with it.
	}
	m.byID[t.ID] = t
	m.byName[name] = t
	go t.pump()
	return t, false, nil
}

// Get returns the terminal of id.
func (m *Manager) Get(id string) (*Terminal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.byID[id]; t != nil {
		return t, nil
	}
	return nil, ErrNotFound
}

// Close hangs up every terminal, kills the programs still there after Grace, and returns once they ended. No
// terminal opens after.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	all := make([]*Terminal, 0, len(m.byID))
	for _, t := range m.byID {
		all = append(all, t)
	}
	m.mu.Unlock()
	for _, t := range all {
		t.Hangup()
	}
	for _, t := range all {
		<-t.done
	}
}

// pump reads the output until the program ends, then marks the terminal ended.
func (t *Terminal) pump() {
	read := make(chan struct{})
	go func() {
		defer close(read)
		b := make([]byte, 32<<10)
		for {
			n, err := t.p.Read(b)
			if n > 0 {
				t.append(b[:n])
			}
			if err != nil {
				return // EOF, or EIO on Linux once no process holds the terminal any more.
			}
		}
	}()
	code := t.p.wait()
	select {
	case <-read:
	case <-time.After(drain): // A child of the program still holds the terminal: leave it.
	}
	t.p.close()
	<-read
	t.mu.Lock()
	t.exited, t.code = true, code
	t.notify()
	t.mu.Unlock()
	close(t.done)
}

func (t *Terminal) append(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > Scrollback+Scrollback/4 {
		drop := len(t.buf) - Scrollback
		t.base += uint64(drop)
		t.buf = append([]byte(nil), t.buf[drop:]...)
	}
	t.notify()
}

// notify wakes the readers; t.mu is held.
func (t *Terminal) notify() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Exited tells whether the program has ended, and its output was read.
func (t *Terminal) Exited() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exited
}

// State returns the size of the terminal, whether its program ended, and its exit code.
func (t *Terminal) State() (cols, rows int, exited bool, code int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows, t.exited, t.code
}

// Done is closed once the program ended and its output was read.
func (t *Terminal) Done() <-chan struct{} { return t.done }

// Write sends b to the program, as typed.
func (t *Terminal) Write(b []byte) error {
	if t.Exited() {
		return ErrExited
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err := t.p.Write(b)
	return err
}

// Resize sets the size of the terminal; the program is told.
func (t *Terminal) Resize(cols, rows int) error {
	if t.Exited() {
		return ErrExited
	}
	if err := t.p.resize(cols, rows); err != nil {
		return err
	}
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	t.mu.Unlock()
	return nil
}

// Hangup asks the program to end, as a terminal that closes does, and kills it after Grace.
func (t *Terminal) Hangup() {
	t.hang.Do(func() {
		t.p.hangup()
		go func() {
			select {
			case <-t.done:
			case <-time.After(grace):
				t.p.kill()
			}
		}()
	})
}

// Output is a piece of output from Read.
type Output struct {
	Offset uint64 // of the first byte of Data, counted from the start of the program's output
	Data   []byte
	Exited bool // last piece: the program ended and all its output was sent
	Code   int
}

// Read sends the output from offset from on, as it comes, and the end of the program last. It returns when send
// fails, when stop is closed, or after the last piece. An offset no longer kept starts at the oldest byte kept.
func (t *Terminal) Read(from uint64, stop <-chan struct{}, send func(Output) error) error {
	for {
		t.mu.Lock()
		end := t.base + uint64(len(t.buf))
		from = min(max(from, t.base), end)
		data := slices.Clone(t.buf[from-t.base : min(end, from+chunk)-t.base])
		exited, code, changed := t.exited, t.code, t.changed
		t.mu.Unlock()
		switch {
		case len(data) > 0:
			if err := send(Output{Offset: from, Data: data}); err != nil {
				return err
			}
			from += uint64(len(data))
			continue
		case exited:
			return send(Output{Offset: from, Exited: true, Code: code})
		}
		select {
		case <-stop:
			return nil
		case <-changed:
		}
	}
}
