package plan

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The watch of the configuration on the reference branch: every ConfigLookEvery, and after each push of Djinn, the
// harness fetches each project's reference branch alone, then LookAtConfig compares the digest of the .agents and
// .claude/skills trees (the name of their folder in the cache) with the one of its last look, kept in a ConfigWatch.
// No git runs to read anything when it has not moved. When it has, each commit of the branch that changed those trees
// becomes an entry of the project's journal of changes (ConfigChange), told in sentences by comparing the
// configuration before and after it. The change is applied already, since Djinn reads the configuration on that
// branch; one that gives the workers more, or changes what a skill makes or runs for Djinn, asks for your look: the
// project's gear lights until you mark it seen.

// ConfigLookEvery is how often Djinn looks at the reference branch of each project.
const ConfigLookEvery = 5 * time.Minute

// Journal entries of the watch.
const (
	actorConfig       = "config"
	methodConfigLook  = "project/config-look" // a look found the reference branch moved; the request is its ConfigWatch
	configCommitsMax  = 20                    // commits told one by one in a look; older ones are told together
	configFirstCommit = 5                     // commits told as history at the first look
	configChangesKept = 50                    // entries kept per project, the newest
	configChangesShow = 20                    // entries ProjectService.Show gives
)

// FetchRef fetches the remote ref (refs/remotes/origin/main) of the repository holding folder from its remote.
type FetchRef func(ctx context.Context, folder, ref string) error

// LookAtConfig is one look of the watch at project's reference branch, home Djinn's data folder: it fetches the
// branch (fetch), then writes what it found. A failed fetch is kept on the ConfigWatch, and the look goes on with
// what the copy holds. Nothing for a project outside Git or without a remote.
func LookAtConfig(ctx context.Context, st *store.Store, home string, project *planv1.Project, fetch FetchRef, now time.Time) error {
	ref, err := ConfigReference(ctx, home, project)
	if err != nil || ref == "" {
		return err
	}
	folder := project.GetDirectory()
	fetchError := ""
	if fetch != nil {
		if err := fetch(ctx, folder, ref); err != nil {
			fetchError = err.Error()
		}
	}
	config := ProjectConfig(home, project)
	if config.FromReference() && "refs/remotes/"+config.Ref != ref && fetch != nil {
		// The team's file, on the default branch just fetched, names another branch.
		if err := fetch(ctx, folder, "refs/remotes/"+config.Ref); err != nil {
			fetchError = err.Error()
		}
		config = ProjectConfig(home, project)
	}
	watches, err := store.List[*planv1.ConfigWatch](ctx, st, store.Where{"project_id": project.GetId()})
	if err != nil {
		return err
	}
	var last *planv1.ConfigWatch
	if len(watches) > 0 {
		last = watches[0]
	}
	next := &planv1.ConfigWatch{ProjectId: project.GetId(), Ref: shortRef(ref), FetchError: fetchError}
	if last != nil {
		next.Id = last.GetId()
		next.Commit, next.Digest, next.FindTime = last.GetCommit(), last.GetDigest(), last.GetFindTime()
	}
	var changes []*planv1.ConfigChange
	if config.FromReference() {
		next.Ref = config.Ref
		digest := filepath.Base(config.Dir)
		if next.GetCommit() != config.Commit {
			next.Commit, next.FindTime = config.Commit, timestamppb.New(now)
		}
		next.Digest = digest
		switch {
		case last == nil || last.GetDigest() == "":
			changes = firstChanges(ctx, home, project, config, now)
		case last.GetDigest() != digest:
			changes = changesSince(ctx, st, home, project, last, config, now)
		}
	}
	if last != nil && proto.Equal(last, next) {
		return nil // nothing moved: nothing written, nothing told
	}
	if next.Id == "" {
		next.Id = store.NewID()
	}
	return st.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal(actorConfig, methodConfigLook, next); err != nil {
			return err
		}
		if err := tx.Put(next); err != nil {
			return err
		}
		for _, c := range changes {
			if err := tx.Put(c); err != nil {
				return err
			}
		}
		return pruneChanges(ctx, tx, project.GetId())
	})
}

// pruneChanges keeps the newest configChangesKept entries of the journal of changes of the project projectID.
func pruneChanges(ctx context.Context, tx *store.Tx, projectID string) error {
	all, err := store.List[*planv1.ConfigChange](ctx, tx, store.Where{"project_id": projectID})
	if err != nil || len(all) <= configChangesKept {
		return err
	}
	sortChanges(all)
	for _, c := range all[configChangesKept:] {
		if err := tx.Delete(c); err != nil {
			return err
		}
	}
	return nil
}

// sortChanges puts changes the newest first: by commit time, then by when Djinn found them.
func sortChanges(changes []*planv1.ConfigChange) {
	slices.SortStableFunc(changes, func(a, b *planv1.ConfigChange) int {
		return cmp.Or(
			b.GetCommitTime().AsTime().Compare(a.GetCommitTime().AsTime()),
			b.GetFindTime().AsTime().Compare(a.GetFindTime().AsTime()),
			strings.Compare(b.GetId(), a.GetId()),
		)
	})
}

// ProjectChanges are the entries of project's journal of configuration changes, the newest first.
func ProjectChanges(ctx context.Context, r store.Reader, projectID string) ([]*planv1.ConfigChange, error) {
	all, err := store.List[*planv1.ConfigChange](ctx, r, store.Where{"project_id": projectID})
	if err != nil {
		return nil, err
	}
	sortChanges(all)
	return all, nil
}

// Waits tells whether the change c waits for your look: found by a look after the first, not yet marked seen, and a
// line of it asks.
func Waits(c *planv1.ConfigChange) bool {
	return !c.GetFirstLook() && c.GetSeenTime() == nil &&
		slices.ContainsFunc(c.GetLines(), func(l *planv1.ConfigChangeLine) bool { return l.GetAsks() })
}

// gitCommit is a commit as the watch tells it.
type gitCommit struct {
	sha, parent, author, title string
	time                       time.Time
}

// configLog lists the commits of the range spec (a commit, or old..new) that changed the configuration's trees,
// following first parents, the newest first, at most n.
func configLog(ctx context.Context, folder, spec string, n int) ([]gitCommit, error) {
	args := []string{"log", "--first-parent", "--format=%H%x1f%P%x1f%an%x1f%ct%x1f%s%x1e", "-n", strconv.Itoa(n), spec, "--"}
	out, err := gitRawOut(ctx, folder, append(args, slashed(ConfigFolders)...)...)
	if err != nil {
		return nil, err
	}
	var commits []gitCommit
	for record := range strings.SplitSeq(string(out), "\x1e") {
		f := strings.Split(strings.TrimSpace(record), "\x1f")
		if len(f) != 5 {
			continue
		}
		parent, _, _ := strings.Cut(f[1], " ")
		seconds, _ := strconv.ParseInt(f[3], 10, 64)
		commits = append(commits, gitCommit{sha: f[0], parent: parent, author: f[2], time: time.Unix(seconds, 0), title: f[4]})
	}
	return commits, nil
}

// configAt is the folder of the cache that holds the configuration of the commit sha, or an empty folder for no
// commit (a root commit's parent); remove cleans the empty folder.
func configAt(ctx context.Context, home, folder, sha string) (dir string, remove func(), err error) {
	if sha == "" {
		dir, err := os.MkdirTemp("", "djinn-config-")
		if err != nil {
			return "", nil, err
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}
	dir, err = extractConfig(ctx, home, folder, sha)
	return dir, func() {}, err
}

// pluggedHere tells whether the inbox source of a skill of project is plugged in on this machine.
func pluggedHere(ctx context.Context, r store.Reader, project *planv1.Project) func(skill string) bool {
	plugged, _ := store.List[*planv1.PluggedSource](ctx, r, store.Where{"project_id": project.GetId()})
	return func(skill string) bool {
		return slices.ContainsFunc(plugged, func(p *planv1.PluggedSource) bool { return strings.EqualFold(p.GetSkill(), skill) })
	}
}

// commitChange is the entry of the journal for the commit c, its lines what changed from its first parent.
func commitChange(ctx context.Context, home string, project *planv1.Project, config Config, c gitCommit,
	plugged func(string) bool, now time.Time,
) (*planv1.ConfigChange, error) {
	before, remove, err := configAt(ctx, home, config.Folder, c.parent)
	if err != nil {
		return nil, err
	}
	defer remove()
	after, err := extractConfig(ctx, home, config.Folder, c.sha)
	if err != nil {
		return nil, err
	}
	return &planv1.ConfigChange{
		Id: store.NewID(), ProjectId: project.GetId(), Ref: config.Ref, Commit: c.sha, Author: c.author, Title: c.title,
		CommitTime: timestamppb.New(c.time), FindTime: timestamppb.New(now), Commits: 1,
		Lines: linesOrFiles(before, after, plugged),
	}, nil
}

// linesOrFiles are configLines from before to after; when none says what changed, the files that did.
func linesOrFiles(before, after string, plugged func(string) bool) []*planv1.ConfigChangeLine {
	lines := configLines(before, after, plugged)
	if len(lines) > 0 {
		return lines
	}
	var files []string
	for _, f := range ConfigFolders {
		for _, c := range changedFiles(filepath.Join(before, f), filepath.Join(after, f)) {
			files = append(files, filepath.ToSlash(f)+"/"+c)
		}
	}
	if len(files) == 0 {
		return nil
	}
	return []*planv1.ConfigChangeLine{{Topic: topicSettings, Text: "Files of the configuration change: " +
		strings.Join(files, ", ") + "."}}
}

// firstChanges are the entries of the first look at project's reference branch: its last commits that changed the
// configuration, told as history.
func firstChanges(ctx context.Context, home string, project *planv1.Project, config Config, now time.Time) []*planv1.ConfigChange {
	commits, err := configLog(ctx, config.Folder, config.Commit, configFirstCommit)
	if err != nil {
		warnOnce(config.Folder, "read the history of the configuration", err)
		return nil
	}
	none := func(string) bool { return false }
	var out []*planv1.ConfigChange
	for _, c := range commits {
		change, err := commitChange(ctx, home, project, config, c, none, now)
		if err != nil {
			warnOnce(config.Folder, "tell a change of the configuration", err)
			continue
		}
		change.FirstLook = true
		out = append(out, change)
	}
	return out
}

// changesSince are the entries for what changed on project's reference branch since last, the watch's previous look:
// one per commit that changed the configuration, the older ones past configCommitsMax told together. When the
// reference branch itself changed, or git no longer holds the last commit, one entry tells the whole difference.
func changesSince(ctx context.Context, r store.Reader, home string, project *planv1.Project, last *planv1.ConfigWatch,
	config Config, now time.Time,
) []*planv1.ConfigChange {
	plugged := pluggedHere(ctx, r, project)
	if last.GetRef() == config.Ref {
		commits, err := configLog(ctx, config.Folder, last.GetCommit()+".."+config.Commit, 4*configCommitsMax)
		if err == nil && len(commits) > 0 {
			return commitChanges(ctx, home, project, last, config, commits, plugged, now)
		}
	}
	change, err := wholeChange(ctx, home, project, last, config, plugged, now)
	if err != nil {
		warnOnce(config.Folder, "tell a change of the configuration", err)
		return nil
	}
	return []*planv1.ConfigChange{change}
}

// commitChanges are the entries for commits, the newest first, that changed the configuration since last.
func commitChanges(ctx context.Context, home string, project *planv1.Project, last *planv1.ConfigWatch, config Config,
	commits []gitCommit, plugged func(string) bool, now time.Time,
) []*planv1.ConfigChange {
	var out []*planv1.ConfigChange
	kept, older := commits, []gitCommit(nil)
	if len(commits) > configCommitsMax {
		kept, older = commits[:configCommitsMax], commits[configCommitsMax:]
	}
	for _, c := range kept {
		change, err := commitChange(ctx, home, project, config, c, plugged, now)
		if err != nil {
			warnOnce(config.Folder, "tell a change of the configuration", err)
			continue
		}
		out = append(out, change)
	}
	if len(older) == 0 {
		return out
	}
	// The older ones together: from the last look to the parent of the oldest told alone.
	newest := older[0]
	before, err := lastConfig(ctx, home, config.Folder, last)
	if err != nil {
		return out
	}
	after, remove, err := configAt(ctx, home, config.Folder, kept[len(kept)-1].parent)
	if err != nil {
		return out
	}
	defer remove()
	out = append(out, &planv1.ConfigChange{
		Id: store.NewID(), ProjectId: project.GetId(), Ref: config.Ref, Commit: newest.sha, Author: newest.author,
		Title: newest.title, CommitTime: timestamppb.New(newest.time), FindTime: timestamppb.New(now),
		Commits: int32(len(older)), Lines: linesOrFiles(before, after, plugged),
	})
	return out
}

// lastConfig is the folder of the cache that holds the configuration of the watch's last look: by its digest, else
// extracted again from its commit.
func lastConfig(ctx context.Context, home, folder string, last *planv1.ConfigWatch) (string, error) {
	if dir := filepath.Join(ConfigCache(home), last.GetDigest()); last.GetDigest() != "" && exists(dir) {
		return dir, nil
	}
	return extractConfig(ctx, home, folder, last.GetCommit())
}

// wholeChange is the one entry that tells the whole difference between the configuration of the watch's last look
// and config: the reference branch changed, or git lost the range.
func wholeChange(ctx context.Context, home string, project *planv1.Project, last *planv1.ConfigWatch, config Config,
	plugged func(string) bool, now time.Time,
) (*planv1.ConfigChange, error) {
	before, err := lastConfig(ctx, home, config.Folder, last)
	if err != nil {
		return nil, err
	}
	head, err := configLog(ctx, config.Folder, config.Commit, 1)
	if err != nil {
		return nil, err
	}
	change := &planv1.ConfigChange{
		Id: store.NewID(), ProjectId: project.GetId(), Ref: config.Ref, Commit: config.Commit,
		FindTime: timestamppb.New(now), CommitTime: timestamppb.New(now), Commits: 1,
	}
	if len(head) > 0 {
		change.Author, change.Title, change.CommitTime = head[0].author, head[0].title, timestamppb.New(head[0].time)
	}
	if last.GetRef() != config.Ref {
		change.Lines = append(change.Lines, line(topicBranch, config.Ref, false,
			"The reference branch is now %s, not %s: Djinn reads the configuration there.", config.Ref, last.GetRef()))
	}
	change.Lines = append(change.Lines, linesOrFiles(before, config.Dir, plugged)...)
	return change, nil
}

// changeNotices are the things to fix the journal of changes of project holds: each change that waits for your look
// (Waits), with what asks for it.
func changeNotices(ctx context.Context, r store.Reader, project *planv1.Project) []*planv1.ProjectNotice {
	changes, err := ProjectChanges(ctx, r, project.GetId())
	if err != nil {
		return nil
	}
	var out []*planv1.ProjectNotice
	for _, c := range slices.Backward(changes) {
		if !Waits(c) {
			continue
		}
		var asks []string
		for _, l := range c.GetLines() {
			if l.GetAsks() {
				asks = append(asks, l.GetText())
			}
		}
		short := c.GetCommit()[:min(7, len(c.GetCommit()))]
		out = append(out, &planv1.ProjectNotice{
			Kind: toFix, Topic: topicChange, Subject: c.GetId(),
			Action:  planv1.ProjectNoticeAction_PROJECT_NOTICE_ACTION_ACKNOWLEDGE_CHANGE,
			Command: fmt.Sprintf("djinn project acknowledge %s %s", project.GetName(), short),
			Text: fmt.Sprintf("A change of %s on %s (%s) waits for your look: %s Djinn applies it already.",
				cmp.Or(c.GetAuthor(), "someone"), c.GetRef(), short, asks[0]),
			Detail: strings.Join(asks, "\n"),
		})
	}
	return out
}
