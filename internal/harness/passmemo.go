package harness

import (
	"context"
	"sync"
)

// passMemo keeps, for the time of an integration pass, what the pass asks git of the same repositories a dozen times
// and that only the person changes, never Djinn: each git costs 0.1 s on Windows. Its methods take a nil memo, out of
// a pass: they keep nothing.
type passMemo struct {
	mu      sync.Mutex
	remotes map[string]remoteConfig // by repository
	mains   map[string][2]string    // mainRef's main and ref, by repository and main_branch setting
	fetched map[string]bool         // the branches catchUp fetched, by repository and branch
}

type passMemoKey struct{}

// withPassMemo is ctx keeping what passMemo keeps until ctx ends.
func withPassMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, passMemoKey{}, &passMemo{
		remotes: map[string]remoteConfig{}, mains: map[string][2]string{}, fetched: map[string]bool{},
	})
}

// passMemoOf is the memo of ctx's pass, nil out of a pass.
func passMemoOf(ctx context.Context) *passMemo {
	m, _ := ctx.Value(passMemoKey{}).(*passMemo)
	return m
}

func (m *passMemo) remotesOf(repo string) (remoteConfig, bool) {
	if m == nil {
		return remoteConfig{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.remotes[repo]
	return c, ok
}

func (m *passMemo) keepRemotes(repo string, c remoteConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.remotes[repo] = c
}

func (m *passMemo) mainOf(repo, name string) (main, ref string, ok bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.mains[repo+"\x00"+name]
	return r[0], r[1], ok
}

func (m *passMemo) keepMain(repo, name, main, ref string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mains[repo+"\x00"+name] = [2]string{main, ref}
}

// fetchDue tells whether catchUp fetches branch of repo: once a pass, every time out of a pass.
func (m *passMemo) fetchDue(repo, branch string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := repo + "\x00" + branch
	due := !m.fetched[key]
	m.fetched[key] = true
	return due
}
