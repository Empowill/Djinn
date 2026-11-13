package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// claudeSlugMax is the longest name Claude Code gives a folder of transcripts whole; past it, it keeps that many
// characters, a dash and the hash of the path in base 36: six characters at most (Claude Code 2.1.322, its
// sanitizePath).
const claudeSlugMax = 200

// ClaudeSlugLen is how long the name of the folder where Claude Code keeps the transcripts of a session in dir is:
// ClaudeProjectSlug, counted as JavaScript counts (a character outside the Basic Multilingual Plane is two), at most
// claudeSlugMax, a dash and six.
func ClaudeSlugLen(dir string) int {
	n := 0
	for _, r := range filepath.Clean(dir) {
		n += max(1, utf16.RuneLen(r))
	}
	if n > claudeSlugMax {
		return claudeSlugMax + 1 + 6
	}
	return n
}

// SessionName says whether Claude Code can keep the session of a worker in dir in its sign-in folder (its own
// folder or an account's): it writes the transcript in <folder>/projects/<dir as a name>/, and where that name is
// longer than the file system of the folder takes, it writes nothing and says nothing, and --resume then finds no
// conversation (W30: on eCryptfs, 143). Its length is n, and limit the longest nameMax measures there; lost when n is
// over it. A limit of 0 is unknown, and nothing is lost.
type SessionName struct {
	Folder   string
	N, Limit int
}

// Lost tells whether no transcript of the session can be written.
func (s SessionName) Lost() bool { return s.Limit > 0 && s.N > s.Limit }

// Why says it in a sentence's end: the name, the limit, and where.
func (s SessionName) Why() string {
	return fmt.Sprintf("the folder of its transcripts would be named in %d characters, and the file system of %s "+
		"takes %d at most", s.N, s.Folder, s.Limit)
}

// ClaudeSessionName is SessionName of a worker in dir whose sign-in folder is folder, the limit measured by nameMax.
func ClaudeSessionName(nameMax func(string) int, folder, dir string) SessionName {
	if nameMax == nil || folder == "" || dir == "" {
		return SessionName{Folder: folder}
	}
	return SessionName{Folder: folder, N: ClaudeSlugLen(dir), Limit: nameMax(filepath.Join(folder, "projects"))}
}

// sessionNotices are what Djinn finds of where Claude Code would keep the sessions of project's workers, on the
// sign-ins of this machine (its own folder, and each Claude account's): a thing to fix when one of them cannot write
// their transcripts, for a worker that stops would not resume in its session. A project whose workers are another
// agent's has none, nor one when nameMax is nil.
func sessionNotices(home string, project *planv1.Project, settings Settings, nameMax func(string) int) []*planv1.ProjectNotice {
	if nameMax == nil || project.GetDirectory() == "" {
		return nil
	}
	if p := settings.Provider; p != planv1.Provider_PROVIDER_UNSPECIFIED && p != planv1.Provider_PROVIDER_CLAUDE {
		return nil
	}
	dir, where := project.GetDirectory(), "the project's folder"
	if project.GetGit() {
		// A worktree is named after its task's UUID: every one is as long.
		dir, where = filepath.Join(home, "projects", project.GetId(), "worktrees", sampleTaskID), "Djinn's data folder"
	}
	userHome, _ := os.UserHomeDir()
	folders := []string{OwnFolder(planv1.Provider_PROVIDER_CLAUDE, userHome)}
	if home != "" {
		for _, in := range SignIns(home, planv1.Provider_PROVIDER_CLAUDE) {
			folders = append(folders, in.Account.GetConfigDirectory())
		}
	}
	var lost, seen []string
	for _, f := range folders {
		if f == "" || slices.Contains(seen, filepath.Clean(f)) {
			continue
		}
		seen = append(seen, filepath.Clean(f))
		if s := ClaudeSessionName(nameMax, f, dir); s.Lost() {
			lost = append(lost, s.Why())
		}
	}
	if len(lost) == 0 {
		return nil
	}
	return []*planv1.ProjectNotice{{
		Kind: toFix, Topic: topicSession, Subject: dir,
		Text: "Claude Code cannot save the sessions of this project's workers: a worker that stops starts over on a " +
			"new session, told what came before, instead of resuming its own. Move " + where + " to a shorter path, " +
			"or Claude Code's folder to a file system that takes longer names.",
		Detail: "A worker runs in " + dir + ": " + strings.Join(lost, "; ") + ".",
	}}
}

// sampleTaskID stands for any task's UUID in the path of a worktree.
const sampleTaskID = "00000000-0000-0000-0000-000000000000"
