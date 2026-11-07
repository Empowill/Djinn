package harness

// A djinn up may start held: it resumes none of the workers the last one left interrupted, nor replays a task an
// environment failure stopped. djinn up asks it with --paused, after a stop that was not clean, and when the
// Hourglass's last reading had the machine's memory near full (cmd/djinn/held.go): a worker that brought the machine
// down would bring it down again. The tasks stay interrupted, the lead hears why in the restart event, and continues
// each that may go on (djinn task continue).

// WithResumeHeld starts the harness held, why saying it to the lead ("" for a start that resumes as usual).
func WithResumeHeld(why string) Option { return func(h *Harness) { h.resumeHeld = why } }

// Held is the codes of the tasks interrupted that Recover left for the lead, the watchers among them, when the harness
// started held; nil otherwise.
func (h *Harness) Held() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.heldTasks...)
}
