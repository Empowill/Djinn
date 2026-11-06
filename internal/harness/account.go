package harness

// The accounts the workers run on (T33). Every worker of an agent runs on one of its sign-ins (plan.SignIns): its own,
// the agent's default folder, or one of its accounts, each a folder of the agent's own signed in once (djinn lead
// add-account), in the developer's order of priority (plan.AccountOrder, decision Q295 B). An AccountPolicy picks one
// at each start of a worker: the one the task ran on before while it may, else the first in the order neither paused
// (djinn lead pause-account), limited, nor known out of quota (FirstInOrder, quota.go); with none such, the worker
// starts past its limits, in overflow, on the first that may spend extra credits. The lead follows the same policy
// before each batch unless it is pinned (L2, leaddriver.go). The account goes in the worker's environment
// (plan.AccountEnv) and on its task (Task.account, TaskRun.account).
//
// A usage limit of the whole account is held per sign-in: the one a worker hit holds its sign-in until it resets, and
// the task moves at once to another sign-in free (limited), its session copied into that account's folder when it can
// be, else on a new session told what came before (relay.go). A task that waits for its own account's limit moves as
// soon as another sign-in is free. A limit alone never holds a task back, as Djinn learns it from one worker's message
// (W409): with every sign-in not paused limited, a new task starts on the first, and waits for its own reset if the
// limit stops it too. Only a pause holds: a task whose agent has every sign-in paused waits, saying so. The limits are
// held in memory, and read again at each pass from the tasks that wait for one: after a restart, a limit no task
// waits for any more is forgotten, and the first worker to hit it again moves. Djinn never reads, writes nor logs a
// token: it names folders.

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/fsx"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
)

// AccountOption is a sign-in a worker may start on, as an AccountPolicy sees it.
type AccountOption struct {
	plan.SignIn
	// LimitedUntil is when the usage limit a worker hit on it resets, as Djinn knows it; zero when none holds it.
	LimitedUntil time.Time
	// Limit names that limit: "the account's session limit".
	Limit string
	// Quota is its usage limits as Djinn last read them (quota.go); nil when never read.
	Quota *Quota
}

// Free tells whether a worker may start on o at now: not paused, and no limit known to hold it.
func (o AccountOption) Free(now time.Time) bool { return !o.Paused && !o.LimitedUntil.After(now) }

// AccountChoice is what an AccountPolicy picks from.
type AccountChoice struct {
	// Provider is the worker's agent.
	Provider planv1.Provider
	// Options are its sign-ins in the developer's order of priority (plan.AccountOrder).
	Options []AccountOption
	// Current is the account the task ran on last, by name ("" for the agent's own sign-in); Ran tells it ran.
	Current string
	Ran     bool
	// Now is the time of the choice.
	Now time.Time
}

// AccountPick is the sign-in an AccountPolicy picks.
type AccountPick struct {
	// Name is its account's name; "" for the agent's own sign-in.
	Name string
	// Overflow: each sign-in of the agent is full, limited or paused; the session starts past its limits, where the
	// account may spend extra credits, and waits for the reset if the agent refuses.
	Overflow bool
}

// AccountPolicy picks the sign-in a worker starts on; ok is false when none may take it. It is the one place that
// decides: FirstInOrder by default, which reads each sign-in's quota (quota.go).
type AccountPolicy func(c AccountChoice) (pick AccountPick, ok bool)

// WithAccountPolicy sets how a worker's sign-in is picked; FirstInOrder by default.
func WithAccountPolicy(p AccountPolicy) Option { return func(h *Harness) { h.accountPolicy = p } }

// accountKey names a sign-in: its agent, and its account's name ("" for the agent's own).
type accountKey struct {
	provider planv1.Provider
	name     string
}

// heldLimit is a usage limit a worker hit on a sign-in: until when it holds it, and what it is.
type heldLimit struct {
	until time.Time
	what  string
}

// withAccounts tells whether a worker of provider runs on one of its sign-ins: the agents that sign in. A watcher's
// command, the fake agent and an unknown one run as they are.
func withAccounts(provider planv1.Provider) bool {
	switch provider {
	case planv1.Provider_PROVIDER_UNSPECIFIED, planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_CODEX,
		planv1.Provider_PROVIDER_ANTIGRAVITY:
		return true
	}
	return false
}

// agentOf is t's agent; none is Claude.
func agentOf(t *planv1.Task) planv1.Provider {
	return cmp.Or(t.GetProvider(), planv1.Provider_PROVIDER_CLAUDE)
}

// holdAccount records that a usage limit holds provider's sign-in name until until; the later reset wins.
func (h *Harness) holdAccount(provider planv1.Provider, name string, until time.Time, what string) {
	h.accountMu.Lock()
	defer h.accountMu.Unlock()
	if h.accountLimits == nil {
		h.accountLimits = map[accountKey]heldLimit{}
	}
	k := accountKey{cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE), name}
	if old, ok := h.accountLimits[k]; !ok || until.After(old.until) {
		h.accountLimits[k] = heldLimit{until: until, what: what}
	}
}

// liftAccount forgets the limit held on provider's sign-in name: its folder signed in to another account since.
func (h *Harness) liftAccount(provider planv1.Provider, name string) {
	h.accountMu.Lock()
	defer h.accountMu.Unlock()
	delete(h.accountLimits, accountKey{cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE), name})
}

// seedAccountLimits holds the sign-in of each task that waits for its own usage limit, until it resets: a limit stays
// known across a restart while a task waits for it. A per-minute rate limit holds no account.
func (h *Harness) seedAccountLimits(tasks []*planv1.Task, now time.Time) {
	for _, t := range tasks {
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RESUMING || t.GetResumeAfter() == nil || len(t.GetRuns()) == 0 ||
			!t.GetResumeAfter().AsTime().After(now) || perMinuteHits(t) > 0 || !withAccounts(t.GetProvider()) {
			continue
		}
		l, ok := limitIn(t.GetWaitReason(), now)
		if !ok || !accountWide(l) {
			continue
		}
		h.holdAccount(agentOf(t), t.GetAccount(), t.GetResumeAfter().AsTime(), l.What)
	}
}

// accountOptions are provider's sign-ins, each with the limit known to hold it.
func (h *Harness) accountOptions(provider planv1.Provider) []AccountOption {
	provider = cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE)
	var signIns []plan.SignIn
	if h.home != "" {
		signIns = plan.SignIns(h.home, provider)
	} else {
		signIns = []plan.SignIn{{}}
	}
	h.accountMu.Lock()
	defer h.accountMu.Unlock()
	out := make([]AccountOption, len(signIns))
	for i, s := range signIns {
		k := accountKey{provider, s.Name}
		l := h.accountLimits[k]
		out[i] = AccountOption{SignIn: s, LimitedUntil: l.until, Limit: l.what}
		if q, ok := h.quotaOf(k); ok {
			out[i].Quota = copyQuota(q)
		}
	}
	return out
}

// choose asks the policy for t's sign-in among options at now.
func (h *Harness) choose(t *planv1.Task, options []AccountOption, now time.Time) (AccountPick, bool) {
	policy := h.accountPolicy
	if policy == nil {
		policy = FirstInOrder
	}
	return policy(AccountChoice{Provider: agentOf(t), Options: options, Current: t.GetAccount(), Ran: len(t.GetRuns()) > 0, Now: now})
}

// accountFor is the sign-in t's next worker starts on, and its account (nil for the agent's own); an error says why
// none may take it.
func (h *Harness) accountFor(t *planv1.Task) (AccountPick, *planv1.Account, error) {
	if !withAccounts(t.GetProvider()) {
		return AccountPick{}, nil, nil
	}
	now := h.now()
	options := h.accountOptions(t.GetProvider())
	pick, ok := h.choose(t, options, now)
	if !ok {
		return pick, nil, fmt.Errorf("%s: resume one (djinn lead resume-account) or wait", noSignInWhy(agentOf(t), options, now))
	}
	for _, o := range options {
		if o.Name == pick.Name {
			return pick, o.Account, nil
		}
	}
	return pick, nil, nil
}

// accountMoves tells whether t's next worker moves to another sign-in than the one it ran on, and to which.
func (h *Harness) accountMoves(t *planv1.Task) (string, bool) {
	if len(t.GetRuns()) == 0 {
		return "", false
	}
	pick, _, err := h.accountFor(t)
	return pick.Name, err == nil && pick.Name != t.GetAccount()
}

// Overflowing tells whether the worker of the task taskID runs now past the limits of its sign-in
// (AccountPick.Overflow), on extra credits if its account allows them: what the window shows beside its account.
func (h *Harness) Overflowing(taskID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.runs[taskID]
	return r != nil && r.worker != nil && r.overflow
}

// overflowText says in a worker's start event that it starts past its limits: "in overflow: every sign-in of claude
// is full, limited or paused; it spends extra credits if the account allows them, else waits for the reset".
func overflowText(provider planv1.Provider) string {
	return fmt.Sprintf("in overflow: every sign-in of %s is full, limited or paused; it spends extra credits if the "+
		"account allows them, else waits for the reset", short(provider))
}

// accountWaits is the scheduler's view of the sign-ins (dispatch.Situation.WithAccounts), for one pass: why no
// sign-in of t's agent may take its worker now, "" when one may; and whether t, waiting for its own account's limit,
// moves to another sign-in free now. options caches each agent's sign-ins for the pass.
func (h *Harness) accountWaits(now time.Time) func(t *planv1.Task) (string, bool) {
	options := map[planv1.Provider][]AccountOption{}
	return func(t *planv1.Task) (string, bool) {
		if !withAccounts(t.GetProvider()) {
			return "", false
		}
		p := agentOf(t)
		o, ok := options[p]
		if !ok {
			o = h.accountOptions(p)
			options[p] = o
		}
		pick, ok := h.choose(t, o, now)
		if !ok {
			return noSignInWhy(p, o, now), false
		}
		return "", len(t.GetRuns()) > 0 && pick.Name != t.GetAccount()
	}
}

// noSignInWhy says why no sign-in of provider's agent is free: "waits for a claude sign-in: its own is paused;
// work hit the account's session limit, resets at 07:20".
func noSignInWhy(provider planv1.Provider, options []AccountOption, now time.Time) string {
	var parts []string
	for _, o := range options {
		who := cmp.Or(o.Name, "its own")
		switch {
		case o.Paused:
			parts = append(parts, who+" is paused")
		case o.LimitedUntil.After(now):
			parts = append(parts, fmt.Sprintf("%s hit %s, resets at %s", who, cmp.Or(o.Limit, "its usage limit"),
				ClockText(o.LimitedUntil, now)))
		}
	}
	return fmt.Sprintf("waits for a %s sign-in: %s", short(provider), strings.Join(parts, "; "))
}

// accountText names a sign-in in an event: "the account work", "its agent's own sign-in".
func accountText(name string) string {
	if name == "" {
		return "its agent's own sign-in"
	}
	return "the account " + name
}

// memoryDir is the auto memory folder of the agent's own sign-in for a worker in dir, which a session on an account
// keeps (autoMemoryDirectory): <own folder>/projects/<repository>/memory, the repository being the main checkout of
// dir's Git repository, which its worktrees share, or dir outside Git (code.claude.com/docs/en/memory, Storage
// location). "" when the own folder is unknown.
func (h *Harness) memoryDir(dir string) string {
	userHome, _ := os.UserHomeDir()
	own := plan.OwnFolder(planv1.Provider_PROVIDER_CLAUDE, userHome)
	if own == "" || dir == "" {
		return ""
	}
	root := dir
	if common, err := git(h.ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		if common = strings.TrimSpace(common); filepath.Base(common) == ".git" {
			root = filepath.Dir(common)
		}
	}
	return filepath.Join(own, "projects", plan.ClaudeProjectSlug(root), "memory")
}

// signInFolder is the folder of Claude Code's sign-in name: the account's, else the agent's own; "" when unknown.
func (h *Harness) signInFolder(name string) string {
	if name == "" {
		userHome, _ := os.UserHomeDir()
		return plan.OwnFolder(planv1.Provider_PROVIDER_CLAUDE, userHome)
	}
	if h.home == "" {
		return ""
	}
	return plan.FindAccount(plan.ReadAccounts(h.home), name).GetConfigDirectory()
}

// moveSession copies the session of Claude Code in dir into the folder of account (nil for the agent's own sign-in),
// from the folder of the sign-in prior first, else of any other, so that --resume finds it there: its transcript, and
// the folder beside it that holds its subagents and its tool results. Nothing else moves: no token, no setting. A
// copy already there is kept unless the one found is newer (the task ran there before, then elsewhere). False when it
// cannot (no transcript found, its copy failed, another agent): the worker starts a new session, told what came
// before (relay.go).
func (h *Harness) moveSession(provider planv1.Provider, session, dir, prior string, account *planv1.Account) bool {
	if cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE) != planv1.Provider_PROVIDER_CLAUDE || session == "" ||
		filepath.Base(session) != session {
		return false
	}
	dst := account.GetConfigDirectory()
	if account == nil {
		dst = h.signInFolder("")
	}
	if dst == "" {
		return false
	}
	name := session + ".jsonl"
	slug := plan.ClaudeProjectSlug(dir)
	target := filepath.Join(dst, "projects", slug, name)
	folders := []string{h.signInFolder(prior)}
	for _, o := range h.accountOptions(planv1.Provider_PROVIDER_CLAUDE) {
		folders = append(folders, h.signInFolder(o.Name))
	}
	source := ""
	for _, f := range folders {
		if f == "" || filepath.Clean(f) == filepath.Clean(dst) {
			continue
		}
		if _, err := os.Stat(filepath.Join(f, "projects", slug, name)); err == nil {
			source = filepath.Join(f, "projects", slug, name)
			break
		}
		if matches, _ := filepath.Glob(filepath.Join(f, "projects", "*", name)); len(matches) > 0 {
			source = matches[0]
			break
		}
	}
	there, errThere := os.Stat(target)
	if source == "" {
		return errThere == nil
	}
	if from, err := os.Stat(source); errThere == nil && (err != nil || !from.ModTime().After(there.ModTime())) {
		return true
	}
	if err := copySessionFile(source, target); err != nil {
		// An older copy there would resume without what came since: a new session, told it, is better.
		logx.L().Warn("copy a session to another sign-in", "session", session, "err", err)
		return false
	}
	if err := copySessionFolder(strings.TrimSuffix(source, ".jsonl"), strings.TrimSuffix(target, ".jsonl")); err != nil {
		// The transcript is there: it resumes; its tool results name their files in the folder they were written in.
		logx.L().Warn("copy a session's folder to another sign-in", "session", session, "err", err)
	}
	return true
}

// copySessionFile copies the regular file src to dst, its folder made, readable by its owner alone.
func copySessionFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return fsx.WriteAtomic(dst, data, 0o600)
}

// copySessionFolder copies the regular files under the session's folder src (subagents/, tool-results/) to dst,
// replacing those there; none when src is not a folder. A link is left behind: it could lead out of the session.
func copySessionFolder(src, dst string) error {
	if info, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir() {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		return copySessionFile(path, filepath.Join(dst, rel))
	})
}

// pickLead is the sign-in the next batch of a lead on provider's agent starts on, by the account policy as for a new
// worker (L2): the pick, its account (nil for the agent's own sign-in), and whether a limit is known to hold it; why
// none may take it when ok is false (each is paused).
func (h *Harness) pickLead(provider planv1.Provider) (pick AccountPick, account *planv1.Account, held bool, why string, ok bool) {
	provider = cmp.Or(provider, planv1.Provider_PROVIDER_CLAUDE)
	now := h.now()
	options := h.accountOptions(provider)
	pick, ok = h.choose(&planv1.Task{Provider: provider}, options, now)
	if !ok {
		return pick, nil, false, noSignInWhy(provider, options, now), false
	}
	for _, o := range options {
		if o.Name == pick.Name {
			return pick, o.Account, o.LimitedUntil.After(now), "", true
		}
	}
	return pick, nil, false, "", true
}

// holdLead holds the sign-in name of provider's agent that the lead hit l on, for every worker as for the lead, when l
// holds the whole account: until it resets, else for plan.LimitWait.
func (h *Harness) holdLead(provider planv1.Provider, name string, l Limit) {
	if !accountWide(l) {
		return
	}
	until := l.Until
	if !until.After(h.now()) {
		until = h.now().Add(plan.LimitWait)
	}
	h.holdAccount(provider, name, until, l.What)
}

// freesAt is when the first sign-in of provider's agent that a limit holds, not paused, frees; zero when none is held.
func (h *Harness) freesAt(provider planv1.Provider) time.Time {
	now := h.now()
	var first time.Time
	for _, o := range h.accountOptions(provider) {
		if !o.Paused && o.LimitedUntil.After(now) && (first.IsZero() || o.LimitedUntil.Before(first)) {
			first = o.LimitedUntil
		}
	}
	return first
}
