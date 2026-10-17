package harness

// Network outages (W285). On 17/10/2026 an outage cut the work: the push checks of a wish went red on what was the
// network, a correction would have repaired code that was not broken, and every task of the wish waited. Djinn now
// rides an outage by itself.
//
// Its detection costs nothing while all goes well: no probe. An error that means "no network" (netWorker in what a
// worker's agent says, netCommand in what git, gh, glab and a check print; netWeak, a connection that broke, only with
// another sign at the same time) makes the network suspected. The scheduler's next pass then probes the provider's API
// host, one HEAD request (ProbeHTTP), and again every probeEvery while the network is not up: a probe that fails
// confirms the outage, one that gets an answer ends it.
//
// While the network is down, or suspected until the probe says: no worker starts; the running ones are paused once no
// tool call of theirs runs (W226's pause: no idle failure, no silent stop, W272); pushes, fetches and the forge calls of
// the integration and of babysitting wait; a worker that failed on a network error is not failed: its task waits,
// RESUMING, "network down since 15:12", as one a usage limit stopped waits for its reset. Once the network is back,
// Djinn resumes the paused workers and the tasks the outage cut one per pass, repairs first (W233), and runs once again
// the checks that failed on the network, never starting a correction for them. The Djinn hears one line as the network
// goes down, one as it comes back (LEAD_EVENT_KIND_NETWORK); the window's status bar says since when it is down.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

const (
	// probeEvery is how often Djinn probes the network while it is not up; never while it is.
	probeEvery = 30 * time.Second
	// probeTimeout is how long a probe waits for an answer.
	probeTimeout = 10 * time.Second
	// netTogether is how close a weak sign must come to a strong one to count: an e2e run's ERR_NETWORK_CHANGED alone
	// is Docker's interfaces changing, not an outage.
	netTogether = 2 * time.Minute
	// keptOutages is how many outages Djinn remembers, to tell whether one cut a worker.
	keptOutages = 8
)

// methodNetwork is journaled when Djinn pauses or resumes a worker for the network.
const methodNetwork = "harness/network"

// What a task the network cut waits with, how it resumes and what its worker is told then.
const (
	whyNetworkDown = "network down since "
	byNetwork      = "after the network came back"
	networkLine    = "The network went down and cut your turn short; it is back. Your worktree is as you left it. Continue your task."
)

// errNetworkDown ends a push the network failed: it goes again once the network is back, never held red.
var errNetworkDown = errors.New("the network is down: Djinn tries again once it is back")

// The errors that mean the network is down. The providers' own (claude's "API Error: Connection error.", "Request
// timed out", "cut off mid-stream" in the outage of 16/10/2026, node's ENOTFOUND, an API's 5xx and "overloaded"), git's
// ("Could not resolve host"), gh's and glab's ("error connecting to api.github.com"), the resolver's, Chromium's.
var (
	// netWorker is what a worker's agent says when it cannot reach its provider's API.
	netWorker = regexp.MustCompile(`(?i)connection error|unable to connect|request timed out|cut off mid-stream|` +
		`network (issue|error|is unreachable)|fetch failed|socket hang up|ECONNREFUSED|ECONNRESET|ENOTFOUND|EAI_AGAIN|` +
		`ETIMEDOUT|ENETUNREACH|EHOSTUNREACH|getaddrinfo|API Error: 5\d\d|overloaded|could not resolve host|` +
		`temporary failure in name resolution`)
	// netCommand is what a command prints when the network is down: a name not resolved, a host not reached. A
	// connection refused is left out: a check's own server on the machine refuses it too.
	netCommand = regexp.MustCompile(`(?i)could not resolve host|temporary failure in name resolution|` +
		`name or service not known|no such host|getaddrinfo (ENOTFOUND|EAI_AGAIN)|network is unreachable|` +
		`error connecting to|ERR_INTERNET_DISCONNECTED|ERR_NAME_NOT_RESOLVED|failed to connect to [^ ]+ port \d+`)
	// netWeak is a connection that broke, which a busy machine says too: a sign only beside another one.
	netWeak = regexp.MustCompile(`ERR_NETWORK_CHANGED|Failed to fetch|ECONNRESET|connection reset by peer|ETIMEDOUT|` +
		`i/o timeout|TLS handshake timeout`)
)

// netState is where the network stands, as Djinn knows it.
type netState int

const (
	netUp        netState = iota // no sign of an outage, or the probe answered: no probe
	netSuspected                 // a sign came: the next pass probes
	netDown                      // a probe failed: Djinn holds its outside work, and probes every probeEvery
)

// ProbeFunc asks whether the network reaches provider's API: nil when it answered.
type ProbeFunc func(ctx context.Context, provider planv1.Provider) error

// WithNetworkProbe lets Djinn confirm an outage it suspects, and see it end (djinn up gives ProbeHTTP). Without one, no
// outage is ever confirmed: a worker a network error cut resumes at the next pass all the same.
func WithNetworkProbe(p ProbeFunc) Option { return func(h *Harness) { h.net.probe = p } }

// network is what Djinn knows of the network. Its mu is taken alone: never with Harness.mu or sched held inside it.
type network struct {
	probe ProbeFunc // nil: no outage is confirmed

	mu       sync.Mutex
	state    netState
	since    time.Time       // the first sign of the outage
	what     string          // that sign, cut
	provider planv1.Provider // whose API host the probe asks
	sign     time.Time       // the last strong sign, whatever the state
	probed   time.Time       // when the last probe started; zero: none yet
	probing  bool
	probes   int               // the probes made, for the tests
	backlog  bool              // back: the work the outage stopped resumes, one task per pass
	outages  []outage          // the outages confirmed, the last keptOutages
	reran    map[string]string // by check: what failed on the network once, run again once it is back
	up       chan struct{}     // nil while the network is up; closed when it comes back (waitNetwork)
	wg       sync.WaitGroup    // the probes under way; Close waits for them
}

// outage is a confirmed outage: from its first sign to the probe that saw it end.
type outage struct{ from, to time.Time }

// probeHosts are the API hosts of the providers: the probe asks the one of the provider whose error made it suspect
// an outage, Claude's for a command's.
var probeHosts = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE:      "https://api.anthropic.com",
	planv1.Provider_PROVIDER_CODEX:       "https://api.openai.com",
	planv1.Provider_PROVIDER_ANTIGRAVITY: "https://generativelanguage.googleapis.com",
}

// probeClient follows no redirect: any answer of the host says the network reaches it.
var probeClient = &http.Client{
	Timeout:       probeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ProbeHTTP is djinn up's probe: one HEAD request to provider's API host. Any answer under 500 says the network is up;
// a 5xx, the provider is down for its workers all the same.
func ProbeHTTP(ctx context.Context, provider planv1.Provider) error {
	url := cmp.Or(probeHosts[provider], probeHosts[planv1.Provider_PROVIDER_CLAUDE])
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	res, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%s answered %s", url, res.Status)
	}
	return nil
}

// netSign tells whether text means the network is down, said by a worker's agent (worker) or printed by a command,
// and records it: an outage is suspected, which the scheduler's next pass probes. A weak sign counts only while an
// outage is suspected, or within netTogether of a strong one. provider is the worker's; unspecified for a command.
func (h *Harness) netSign(provider planv1.Provider, text string, worker bool) bool {
	strong := netCommand
	if worker {
		strong = netWorker
	}
	n := &h.net
	now := h.now()
	n.mu.Lock()
	line := matchedLine(strong, text)
	if line == "" {
		if n.state == netUp && (n.sign.IsZero() || now.Sub(n.sign) > netTogether) {
			n.mu.Unlock()
			return false
		}
		if line = matchedLine(netWeak, text); line == "" {
			n.mu.Unlock()
			return false
		}
	} else {
		n.sign = now
	}
	// A probe that answered less than probeEvery ago says the network was up: no new one before its time, whatever
	// the signs (an overloaded API says so at each of its retries).
	suspected := n.state == netUp && n.probe != nil && (n.probed.IsZero() || now.Sub(n.probed) >= probeEvery)
	if suspected {
		n.state, n.since, n.what, n.provider = netSuspected, now, clipRunes(line, 80), provider
		n.up = make(chan struct{})
	}
	n.mu.Unlock()
	if suspected {
		logx.L().Info("network outage suspected", "what", line)
		h.wake() // Its pass probes.
	}
	return true
}

// matchedLine is the line of text re matches, trimmed; "" when it matches none.
func matchedLine(re *regexp.Regexp, text string) string {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	start := strings.LastIndexByte(text[:loc[0]], '\n') + 1
	end := len(text)
	if i := strings.IndexByte(text[loc[1]:], '\n'); i >= 0 {
		end = loc[1] + i
	}
	return strings.TrimSpace(text[start:end])
}

// netHolds tells whether Djinn holds its outside work: the network is down, or suspected until the probe says.
func (h *Harness) netHolds() bool {
	h.net.mu.Lock()
	defer h.net.mu.Unlock()
	return h.net.state != netUp
}

// NetworkDownSince is since when the network is down, as a probe confirmed; nil while it is up, or only suspected.
// The window's status bar says it.
func (h *Harness) NetworkDownSince() *timestamppb.Timestamp {
	h.net.mu.Lock()
	defer h.net.mu.Unlock()
	if h.net.state != netDown {
		return nil
	}
	return timestamppb.New(h.net.since)
}

// netSince is when the outage under way began: its first sign; now when none is known.
func (h *Harness) netSince() time.Time {
	h.net.mu.Lock()
	defer h.net.mu.Unlock()
	if h.net.state == netUp {
		return h.now()
	}
	return h.net.since
}

// waitNetwork waits until the network is up; false when ctx ended first.
func (h *Harness) waitNetwork(ctx context.Context) bool {
	h.net.mu.Lock()
	up := h.net.up
	h.net.mu.Unlock()
	if up == nil {
		return true
	}
	select {
	case <-up:
		return true
	case <-ctx.Done():
		return false
	}
}

// netCut tells whether an outage Djinn confirmed took in end, when a worker ended: such a worker spends no resume.
func (h *Harness) netCut(end time.Time) bool {
	h.net.mu.Lock()
	defer h.net.mu.Unlock()
	if h.net.state == netDown && !end.Before(h.net.since.Add(-netTogether)) {
		return true
	}
	return slices.ContainsFunc(h.net.outages, func(o outage) bool {
		return !end.Before(o.from.Add(-netTogether)) && !end.After(o.to)
	})
}

// netCheckFailed tells whether checks that failed, printing text, failed on the network rather than on the code: they
// run once again when it is back, in place of a hold, a red merge or a correction. key names what they check (a
// branch's push, a wish's commit checks), at is what they ran on. Failed on the network again at the same place once
// it was back, they are red as any check: the network was not the cause, or not the only one.
func (h *Harness) netCheckFailed(key, at, text string) bool {
	if !h.netSign(planv1.Provider_PROVIDER_UNSPECIFIED, text, false) {
		return false
	}
	n := &h.net
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.reran == nil {
		n.reran = map[string]string{}
	}
	if n.reran[key] == at && n.state != netDown {
		delete(n.reran, key)
		return false
	}
	n.reran[key] = at
	return true
}

// netWaits tells whether the checks of key at failed on the network and wait for it to come back.
func (h *Harness) netWaits(key, at string) bool {
	h.net.mu.Lock()
	defer h.net.mu.Unlock()
	return h.net.state != netUp && h.net.reran[key] == at
}

// netPass is the scheduler's look at the network, each pass: it probes it when due, pauses the running workers while it
// is down, and once it is back resumes them one per pass. It returns what the pass's dispatch must know: nil when the
// network holds nothing back. The caller holds h.sched.
func (h *Harness) netPass(ctx context.Context, tasks []*planv1.Task) *dispatch.Network {
	n := &h.net
	now := h.now()
	n.mu.Lock()
	state, since, backlog := n.state, n.since, n.backlog
	if state != netUp && n.probe != nil && !n.probing && (n.probed.IsZero() || now.Sub(n.probed) >= probeEvery) {
		n.probing, n.probed = true, now
		n.probes++
		n.wg.Add(1)
		go h.probeNetwork(n.probe, n.provider)
	}
	n.mu.Unlock()
	switch state {
	case netSuspected:
		return netDispatch(state, since, now)
	case netDown:
		h.pauseForNetwork(ctx, whyNetworkDown+ClockText(since, now))
		return netDispatch(state, since, now)
	}
	if !backlog {
		return nil
	}
	spent, left := h.resumeAfterNetwork(ctx, tasks)
	if !left {
		n.mu.Lock()
		n.backlog = false
		n.mu.Unlock()
		return nil
	}
	return &dispatch.Network{OnePerPass: true, Spent: spent}
}

// netDispatch is what a dispatch must know of the network while it is suspected or down: why no task starts.
func netDispatch(state netState, since, now time.Time) *dispatch.Network {
	switch state {
	case netSuspected:
		return &dispatch.Network{Down: "the network may be down since " + ClockText(since, now) + ": Djinn probes it"}
	case netDown:
		return &dispatch.Network{Down: whyNetworkDown + ClockText(since, now) + ": Djinn starts no worker until it is back"}
	}
	return nil
}

// netView is what a decision outside the scheduler's pass (a spawn) must know of the network: down, no task starts;
// back, the work the outage stopped resumes first, through the passes.
func (h *Harness) netView() *dispatch.Network {
	n := &h.net
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.state == netUp {
		if n.backlog {
			return &dispatch.Network{OnePerPass: true, Spent: true}
		}
		return nil
	}
	return netDispatch(n.state, n.since, h.now())
}

// probeNetwork probes the network, apart from the pass, and records what it found.
func (h *Harness) probeNetwork(probe ProbeFunc, provider planv1.Provider) {
	defer logx.Crash("network probe")
	defer h.net.wg.Done()
	ctx, cancel := context.WithTimeout(h.ctx, probeTimeout)
	err := probe(ctx, provider)
	cancel()
	if h.ctx.Err() != nil {
		return
	}
	h.netProbed(err)
}

// netProbed records what a probe found: a suspected outage confirmed (err) or not; a confirmed one over (no err). The
// Djinn hears one line as the network goes down, one as it comes back.
func (h *Harness) netProbed(err error) {
	n := &h.net
	now := h.now()
	n.mu.Lock()
	n.probing = false
	was, since, what := n.state, n.since, n.what
	switch {
	case err != nil && was == netSuspected:
		n.state = netDown
	case err == nil && was != netUp:
		if was == netDown {
			n.outages = append(n.outages, outage{from: since, to: now})
			if len(n.outages) > keptOutages {
				n.outages = n.outages[len(n.outages)-keptOutages:]
			}
			n.backlog = true
		}
		n.state = netUp
		close(n.up)
		n.up = nil
	}
	state := n.state
	n.mu.Unlock()
	switch {
	case err != nil && was == netSuspected:
		logx.L().Warn("network down", "since", since, "what", what, "probe", err)
		h.tellNetwork(downLine(since, what, now), downDetail)
	case err == nil && was == netDown:
		logx.L().Info("network back", "since", since)
		h.tellNetwork(h.backLine(since, now), backDetail)
		h.kickIntegrate() // The pushes and fetches it held go.
	case err == nil && was == netSuspected:
		logx.L().Info("network outage not confirmed", "what", what)
	case err != nil:
		logx.L().Debug("network still down", "probe", err)
	}
	if state != was {
		h.wake()
	}
}

// What the Djinn reads under the lines of the network, whole.
const (
	downDetail = "Djinn starts no worker, pauses the running ones between their tool calls, holds its pushes, fetches " +
		"and forge calls, and sends a worker the outage cuts back to resume, never failed. It probes the network every " +
		"30 s and resumes everything by itself once it is back: nothing to do."
	backDetail = "One task per pass, repairs first: the workers it paused, then the tasks it cut. The push and commit " +
		"checks that failed on the network run once again, with no correction."
)

// downLine is the Djinn's line as the network goes down.
func downLine(since time.Time, what string, now time.Time) string {
	return fmt.Sprintf("Djinn: the network is down since %s (%s); Djinn holds its outside work and resumes it once back.",
		ClockText(since, now), clipRunes(what, 60))
}

// backLine is the Djinn's line as the network comes back: how long it was down, and what resumes.
func (h *Harness) backLine(since, now time.Time) string {
	h.mu.Lock()
	var paused []string
	for _, r := range h.runs {
		if r.paused && r.netPaused && !r.watcher && r.shown != nil {
			paused = append(paused, r.shown.GetCode())
		}
	}
	h.mu.Unlock()
	var cut []string
	if tasks, err := store.List[*planv1.Task](h.ctx, h.store, nil); err == nil {
		for _, t := range tasks {
			if networkWait(t) {
				cut = append(cut, t.GetCode())
			}
		}
	}
	slices.Sort(paused)
	slices.Sort(cut)
	line := fmt.Sprintf("Djinn: the network is back (down from %s, %d min)", ClockText(since, now),
		int(math.Round(now.Sub(since).Minutes())))
	var parts []string
	if len(paused) > 0 {
		parts = append(parts, "paused "+clipRunes(strings.Join(paused, ", "), 30))
	}
	if len(cut) > 0 {
		parts = append(parts, "cut "+clipRunes(strings.Join(cut, ", "), 30))
	}
	if len(parts) == 0 {
		return line + "; nothing to resume."
	}
	return line + "; Djinn resumes, one per pass: " + strings.Join(parts, "; ") + "."
}

// tellNetwork queues the Djinn's line about the network, of no wish.
func (h *Harness) tellNetwork(line, detail string) {
	e := &planv1.LeadEvent{Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_NETWORK, Text: line, Detail: detail}
	if err := h.toLeadEvent(h.ctx, e); err != nil && !errors.Is(err, plan.ErrNoLead) && h.ctx.Err() == nil {
		logx.L().Warn("tell the lead of the network", "err", err)
	}
}

// pauseForNetwork pauses the running workers while the network is down, why: each once no tool call of its runs (a
// test, a build goes on without the network), never an ifrit, nor a worker that holds or waits for a gate (hold refuses
// it), nor one whose agent cannot pause (Windows): those go on, and the network cuts them or not.
func (h *Harness) pauseForNetwork(ctx context.Context, why string) {
	h.mu.Lock()
	var ids []string
	for id, r := range h.runs {
		if r.paused || r.worker == nil || r.stopping || r.final || r.shelved || r.ifrit || r.inTool {
			continue
		}
		if _, ok := r.worker.(Pauser); !ok {
			continue
		}
		ids = append(ids, id)
	}
	h.mu.Unlock()
	slices.Sort(ids)
	for _, id := range ids {
		if _, err := h.hold(ctx, methodNetwork, &planv1.TaskServicePauseRequest{TaskId: id}, id, true, why); err != nil {
			logx.L().Debug("pause a worker for the network", "task", id, "err", err)
		}
	}
}

// resumeAfterNetwork resumes the work the outage stopped, once the network is back: the watchers it paused at once (they
// take no slot), then one paused worker a pass, repairs first, then by the order of the wishes (Q117). A repair the
// outage cut, which the pass starts, goes before a paused worker that is none. It says whether it resumed one in this
// pass (spent: the pass starts no task besides), and whether work the outage stopped is left.
func (h *Harness) resumeAfterNetwork(ctx context.Context, tasks []*planv1.Task) (spent, left bool) {
	h.mu.Lock()
	var watchers []string
	var paused []candidate
	for _, r := range h.runs {
		if !r.paused || !r.netPaused || r.stopping || r.final || r.worker == nil || r.shown == nil {
			continue
		}
		if r.watcher {
			watchers = append(watchers, r.id)
			continue
		}
		paused = append(paused, candidate{r: r, t: r.shown})
	}
	h.mu.Unlock()
	for _, id := range watchers {
		if _, err := h.hold(ctx, methodNetwork, &planv1.TaskServiceResumeRequest{TaskId: id}, id, false, "the network is back"); err != nil {
			logx.L().Debug("resume a watcher after the network", "task", id, "err", err)
		}
	}
	cut := slices.ContainsFunc(tasks, networkWait)
	if len(paused) == 0 {
		return false, cut
	}
	ids := make([]string, len(paused))
	for i, c := range paused {
		ids[i] = c.t.GetId()
	}
	ranks := h.Ranks(ctx, ids)
	rank := func(t *planv1.Task) int {
		if r, ok := ranks[t.GetId()]; ok {
			return r
		}
		return len(ranks) + 1
	}
	slices.SortFunc(paused, func(a, b candidate) int {
		return cmp.Or(
			-boolCompare(repairTask(a.t), repairTask(b.t)),
			cmp.Compare(rank(a.t), rank(b.t)),
			a.t.GetStartTime().AsTime().Compare(b.t.GetStartTime().AsTime()),
			strings.Compare(a.t.GetCode(), b.t.GetCode()),
		)
	})
	first := paused[0].t
	if !repairTask(first) && slices.ContainsFunc(tasks, func(t *planv1.Task) bool { return networkWait(t) && repairTask(t) }) {
		return false, true // The repair the outage cut starts first, through the pass.
	}
	if h.capacity != nil {
		if slots, _, _ := h.capacity(); slots > 0 && h.Running() >= slots {
			return true, true // No slot for it yet: no new task takes the one it waits for.
		}
	}
	if _, err := h.hold(ctx, methodNetwork, &planv1.TaskServiceResumeRequest{TaskId: first.GetId()}, first.GetId(), false,
		"the network is back"); err != nil {
		logx.L().Warn("resume a worker after the network", "task", first.GetCode(), "err", err)
		return false, true
	}
	return true, true
}

// boolCompare orders false before true.
func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// repairTask tells whether t repairs a branch, which goes first (W233): a correction, a review, a merge.
func repairTask(t *planv1.Task) bool {
	return t.GetCorrection() != nil || t.GetReview() != nil || plan.IsMerge(t)
}

// networkWait tells whether t waits for the network to come back, cut by an outage (networkCut).
func networkWait(t *planv1.Task) bool {
	return t.GetStatus() == planv1.TaskStatus_TASK_STATUS_RESUMING && strings.HasPrefix(t.GetWaitReason(), whyNetworkDown)
}

// networkWhy is why, a task the network cut waits for, keeping that first: what relaunch reads.
func networkWhy(t *planv1.Task, why string) string {
	if !networkWait(t) || why == "" || strings.HasPrefix(why, whyNetworkDown) {
		return why
	}
	orig, _, _ := strings.Cut(t.GetWaitReason(), "; ")
	return orig + "; " + why
}

// networkCut turns the failure of a worker a network error cut into a wait: the task resumes once the network is back,
// as one a usage limit stopped resumes once it resets. A worker whose stream fell idle while the network is down was
// cut so too. A task resumed maxResumes times, outside outages Djinn confirmed, stays failed. The caller is end, which
// owns the run's task.
func (h *Harness) networkCut(r *run, t *planv1.Task, streamIdle bool) {
	if !t.GetScheduled() || r.watcher || r.ifrit || t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
		return
	}
	text := t.GetError()
	if r.failure != "" && !strings.Contains(text, r.failure) {
		text += "\n" + r.failure
	}
	if !h.netSign(t.GetProvider(), text, true) && !(streamIdle && h.netHolds()) {
		return
	}
	if t.GetResumes() >= maxResumes && !h.netCut(h.now()) {
		t.Error = exhausted(t.GetError())
		return
	}
	t.Status, t.Error = planv1.TaskStatus_TASK_STATUS_RESUMING, ""
	t.WaitReason, t.ResumeAfter = whyNetworkDown+ClockText(h.netSince(), h.now()), nil
}
