package harness

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// The pace of Djinn's fetches (W65, study W63, decision Q44 B): Djinn fetched about a hundred times an hour, two
// thirds of them for the watch of the configuration, the developer there or not. Now:
//   - one freshness of each remote branch by repository (fetchFresh), shared by the watch of the configuration, the
//     merge of main, the look for azimas merged and a worker's start: a branch fetched lately by one is not fetched
//     again by another; main comes with the tags in one command; two loops after the same branch at once share one
//     fetch;
//   - the watch looks every plan.ConfigLookEvery, and after a push of Djinn at the pushed project alone;
//   - the integration fetches the branches of a wish's lines due in a pass, in one project, together (fetchLines);
//   - away (away): the developer touched nothing in Djinn for awayAfter, and no worker runs. The watch then looks
//     every configLookAway, and each line keeps up with main every mainLookAway at most. The pace follows presence
//     alone, never the hour of the day. Back in front of the window, or at their first touch after being away
//     (Back), Djinn looks at the configuration and at main at once, at most once each backEvery.
//
// A worker always starts from a fresh base (W65, the developer): before its worktree is made, Djinn fetches the
// branch it starts from unless it was fetched within referenceFetchEvery, by any loop, away or not (freshBase,
// fetchReference); workers that start together share that fetch.

// Pace of the fetches while the developer is away.
const (
	awayAfter      = 15 * time.Minute // no touch for this long, no worker running: away
	configLookAway = time.Hour        // the watch of the configuration, away
	mainLookAway   = 3 * time.Hour    // each line's look at main, away (at least: a slower merge_main_minutes stays)
	backEvery      = 5 * time.Minute  // a look at the configuration and at main on the developer's return, at most this often
)

// fetchPace keeps the freshness of the fetches, the watch of the configuration's asks, and the developer's returns.
type fetchPace struct {
	paced atomic.Bool // paced by presence (PaceByPresence)

	mu         sync.Mutex
	fetched    map[string]fetchedAt     // by fetchedKey, the last fetch of the remote branch
	flights    map[string]chan struct{} // by fetchedKey: full while it is fetched, so that loops share one fetch
	configAll  bool                     // the watch looks at every project next
	configAsk  map[string]bool          // the projects the watch looks at next, by id
	configLast time.Time                // when the watch last looked at every project
	backLast   time.Time                // when the developer's return last made Djinn look
}

// PaceByPresence makes Djinn fetch slower while the developer is away (away). djinn up calls it; without it, the pace
// is the one of a developer always there.
func (h *Harness) PaceByPresence() { h.pace.paced.Store(true) }

// away tells whether Djinn fetches at the slow pace: paced by presence, the developer touched nothing in Djinn for
// awayAfter, or nothing since it started, and no worker runs.
func (h *Harness) away() bool {
	if !h.pace.paced.Load() {
		return false
	}
	if last := h.presence.Last(); !last.IsZero() && h.now().Sub(last) < awayAfter {
		return false
	}
	return h.Running() == 0
}

// configEvery is how often the watch of the configuration looks at every project now.
func (h *Harness) configEvery() time.Duration {
	every := h.configTick
	if every == 0 {
		every = plan.ConfigLookEvery
	}
	if h.away() {
		every = max(every, configLookAway)
	}
	return every
}

// mainEvery is how often a line keeps up with main now, every as its project's settings say.
func (h *Harness) mainEvery(every time.Duration) time.Duration {
	if h.away() {
		return max(every, mainLookAway)
	}
	return every
}

// mainFresh is how lately main may have been fetched, by any loop, for the merge of main to read it without fetching
// it again: within the watch's own pace; now (a release found, the developer back), it is fetched whatever.
func (h *Harness) mainFresh(now bool) time.Duration {
	if now {
		return 0
	}
	return h.configEvery()
}

// Back says that the developer is back in front of Djinn: the window came to the front, or they touched it after
// being away. Djinn looks at once at every project's configuration and at main, at most once each backEvery.
func (h *Harness) Back() {
	now := h.now()
	h.pace.mu.Lock()
	due := h.pace.backLast.IsZero() || now.Sub(h.pace.backLast) >= backEvery
	if due {
		h.pace.backLast = now
	}
	h.pace.mu.Unlock()
	if !due {
		return
	}
	logx.L().Debug("the developer is back: a look at the configuration and at main", "loop", "pace")
	h.kickConfig()
	h.LookAtMain()
}

// touched is the presence's touch: the ifrits look again, and a touch after being away is a return (Back).
func (h *Harness) touched(before time.Time) {
	h.pokeIfrits()
	if h.pace.paced.Load() && (before.IsZero() || h.now().Sub(before) >= awayAfter) {
		h.Back()
	}
}

// fetchedAt is a fetch of a remote branch: when it began, and its error.
type fetchedAt struct {
	at  time.Time
	err error
}

// fetchFresh runs fetch, the fetch of the remote branch key names (fetchedKey), unless that branch was fetched within
// fresh by any loop; 0 fetches it whatever. A fetch that failed within referenceFetchEvery gives its error again, not
// to wait on a remote that does not answer at each ask. A loop after it while another fetches it waits for that
// fetch, and shares it; until ctx ends at most, its error then.
func (h *Harness) fetchFresh(ctx context.Context, key string, fresh time.Duration, fetch func() error) error {
	h.pace.mu.Lock()
	h.pace.init()
	flight := h.pace.flights[key]
	if flight == nil {
		flight = make(chan struct{}, 1)
		h.pace.flights[key] = flight
	}
	h.pace.mu.Unlock()
	select {
	case flight <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-flight }()
	start := h.now()
	h.pace.mu.Lock()
	last, ok := h.pace.fetched[key]
	h.pace.mu.Unlock()
	if ok && fresh > 0 {
		switch age := start.Sub(last.at); {
		case last.err == nil && age < fresh:
			return nil
		case last.err != nil && age < min(fresh, referenceFetchEvery):
			return last.err
		}
	}
	err := fetch()
	h.fetchedNow(key, fetchedAt{at: start, err: err})
	return err
}

// fetchedNow records f, a fetch of the remote branch key names (fetchedKey), when no later one is recorded.
func (h *Harness) fetchedNow(key string, f fetchedAt) {
	h.pace.mu.Lock()
	defer h.pace.mu.Unlock()
	h.pace.init()
	if !f.at.Before(h.pace.fetched[key].at) {
		h.pace.fetched[key] = f
	}
}

// init makes the maps of p; its mu held.
func (p *fetchPace) init() {
	if p.flights == nil {
		p.flights, p.fetched = map[string]chan struct{}{}, map[string]fetchedAt{}
	}
}

// fetchMainFresh fetches main, ref in repo, and the tags (fetchMain) unless main was fetched within fresh (fetchFresh).
func (h *Harness) fetchMainFresh(ctx context.Context, wishID, repo, ref string, fresh time.Duration) error {
	return h.fetchFresh(ctx, fetchedKey(repo, ref), fresh, func() error { return h.fetchMain(ctx, wishID, repo, ref) })
}
