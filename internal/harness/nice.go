package harness

import djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"

// The CPU priority of what Djinn starts, by kind (Q371 B): the window, the server and the lead run at Djinn's own
// (nice 0); a worker and what it starts run below them at nice 10, but at Overclock, where they run at Djinn's own;
// a check Djinn runs on a project's work (the lint before a merge, the tests before a push, the setup, a generation, an
// install Djinn starts by itself) at nice 5, between the workers and the window, never below the workers; a serve, a
// worker's local stack, at nice 10. A level of 0 is Djinn's own priority, as Djinn was started (nice djinn, a launcher
// at -10); a higher one is that level, or Djinn's own when Djinn already runs lower. On Windows, a level of 10 is
// BELOW_NORMAL_PRIORITY_CLASS, a lower one Djinn's own class.
const (
	// lowNice is the nice level of a worker below the window, at every notch but Overclock, and of a serve.
	lowNice = 10
	// checkNice is the nice level of a check, between the workers (lowNice) and the window (0).
	checkNice = 5
	// serveNice is the nice level of a serve: a worker's load, below the window at every notch.
	serveNice = lowNice
)

// workerNiceAt is the nice level a worker starts at under notch: lowNice, but 0 (Djinn's own) at Overclock, which
// accepts saturation. Auto weighs the notch it chose. The level is set at the start: a later change of notch reaches
// the next worker, never one that runs.
func workerNiceAt(notch djinnv1.LoadNotch) int {
	if notch == djinnv1.LoadNotch_LOAD_NOTCH_OVERCLOCK {
		return 0
	}
	return lowNice
}

// checkNiceAt is the nice level of a check under notch: checkNice, or the workers' when lower, as at Overclock: a
// check, which every merge or push waits for, never runs below the workers.
func checkNiceAt(notch djinnv1.LoadNotch) int {
	return min(checkNice, workerNiceAt(notch))
}

// workerNice is the nice level of a worker Djinn starts now (workerNiceAt).
func (h *Harness) workerNice() int { return workerNiceAt(h.Policy().Notch) }

// checkNice is the nice level of a check Djinn runs now (checkNiceAt).
func (h *Harness) checkNice() int { return checkNiceAt(h.Policy().Notch) }
