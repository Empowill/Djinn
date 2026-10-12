package plan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// readGitTimeout bounds a git that reads a ref, run while a store transaction holds the database: every other call of
// Djinn waits for it (W504).
const readGitTimeout = 5 * time.Second

// CheckedOutBranch is the branch the checkout in dir is on: "" when it is on none (a detached HEAD), or dir is not in
// Git, or git did not answer within readGitTimeout.
func CheckedOutBranch(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, readGitTimeout)
	defer cancel()
	out, err := gitOut(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// IntegrationBranchOf is the branch wish integrates its finished work into in the project projectID; "" when none is
// recorded.
func IntegrationBranchOf(wish *planv1.Wish, projectID string) string {
	for _, b := range wish.GetIntegrationBranches() {
		if strings.EqualFold(b.GetProjectId(), projectID) {
			return b.GetBranch()
		}
	}
	return ""
}

// ErrUpToDate says that a push found nothing to push: the remote holds the branch already.
var ErrUpToDate = errors.New("nothing to push: the remote is up to date")

// PushStateOf is the state of the push of wish in the project projectID; nil when none is recorded.
func PushStateOf(wish *planv1.Wish, projectID string) *planv1.WishPush {
	for _, p := range wish.GetPushes() {
		if strings.EqualFold(p.GetProjectId(), projectID) {
			return p
		}
	}
	return nil
}

// SetIntegrationBranch records branch as wish's integration branch in the project projectID, in place of the one it
// had.
func SetIntegrationBranch(wish *planv1.Wish, projectID, branch string) {
	wish.IntegrationBranches = slices.DeleteFunc(wish.IntegrationBranches, func(b *planv1.IntegrationBranch) bool {
		return strings.EqualFold(b.GetProjectId(), projectID)
	})
	wish.IntegrationBranches = append(wish.IntegrationBranches, &planv1.IntegrationBranch{ProjectId: projectID, Branch: branch})
}

// integrationBranches records, for each Git project of wish, the branch its checkout is on now: where the wish's
// finished work goes by default.
func integrationBranches(ctx context.Context, tx store.Reader, wish *planv1.Wish) error {
	for _, id := range wish.GetProjectIds() {
		project, err := store.Get[*planv1.Project](ctx, tx, id)
		if err != nil {
			return err
		}
		if !project.GetGit() || project.GetDirectory() == "" {
			continue
		}
		if branch := CheckedOutBranch(ctx, project.GetDirectory()); branch != "" {
			SetIntegrationBranch(wish, id, branch)
		}
	}
	return nil
}

// MergeRunning says, in a phrase, what merges into wish's integration branch in the project projectID now: a batch of
// its work being merged and tested, a correction or review worker bringing work into it, or one settling main's merge;
// "" when none does. While one runs, the branch does not change under it.
func MergeRunning(wish *planv1.Wish, tasks []*planv1.Task, projectID string) string {
	codes := map[string]string{}
	for _, t := range tasks {
		codes[t.GetId()] = t.GetCode()
	}
	recorded := IntegrationBranchOf(wish, projectID)
	var merging, merges []string
	for _, t := range tasks {
		if !strings.EqualFold(t.GetProjectId(), projectID) {
			continue
		}
		// A batch merged into another branch, an azima's, leaves the wish's alone.
		if in := t.GetIntegration(); in.GetState() == planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING &&
			(in.GetBranch() == "" || recorded == "" || strings.EqualFold(in.GetBranch(), recorded)) {
			merging = append(merging, t.GetCode())
		}
		if IsMerge(t) && !Finished(t) && t.GetStatus() != planv1.TaskStatus_TASK_STATUS_FAILED {
			merges = append(merges, t.GetCode())
		}
	}
	branch := recorded
	if branch == "" {
		branch = "the integration branch"
	}
	switch {
	case len(merging) > 0:
		return fmt.Sprintf("the work of %s is being merged into %s", strings.Join(merging, ", "), branch)
	case len(merges) > 0:
		return fmt.Sprintf("%s brings work into %s", strings.Join(merges, ", "), branch)
	}
	for _, m := range wish.GetMains() {
		if strings.EqualFold(m.GetProjectId(), projectID) && m.GetCorrectedBy() != "" {
			code := codes[m.GetCorrectedBy()]
			if code == "" {
				code = "a correction worker"
			}
			return fmt.Sprintf("%s settles the merge of main into %s", code, branch)
		}
	}
	return ""
}

// createIntegrationBranch makes branch in the repository of the project projectID, at the tip of wish's integration
// branch there (the branch its checkout is on, for a wish that records none); a branch of that name already there is
// left as it is.
func createIntegrationBranch(ctx context.Context, tx store.Reader, wish *planv1.Wish, projectID, branch string) error {
	project, err := store.Get[*planv1.Project](ctx, tx, projectID)
	if err != nil {
		return err
	}
	dir := project.GetDirectory()
	if !project.GetGit() || dir == "" {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("project %s is not in Git: no branch to make", project.GetName()))
	}
	if _, err := gitOut(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nil
	}
	from := IntegrationBranchOf(wish, projectID)
	if from == "" {
		from = CheckedOutBranch(ctx, dir)
	}
	if from == "" {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"project %s has no integration branch to make %s from: name an existing branch", project.GetName(), branch))
	}
	if _, err := gitOut(ctx, dir, "branch", "--no-track", branch, "refs/heads/"+from); err != nil {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("make %s from %s: %w", branch, from, err))
	}
	return nil
}

// integrationChange is the project of wish whose integration branch req names, and whether the branch changes; an
// error when req names no project of the wish, or a merge into the branch runs.
func integrationChange(
	ctx context.Context, r store.Reader, wish *planv1.Wish, req *planv1.WishServiceSetIntegrationRequest,
) (projectID string, change bool, err error) {
	id := req.GetProjectId()
	ids := wish.GetProjectIds()
	switch {
	case id == "" && len(ids) != 1:
		return "", false, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"the wish has %d projects: name one with --project-id", len(ids)))
	case id == "":
		id = ids[0]
	}
	i := slices.IndexFunc(ids, func(p string) bool { return strings.EqualFold(p, id) })
	if i < 0 {
		return "", false, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("project %s is not one of the wish's projects", id))
	}
	if strings.EqualFold(req.GetBranch(), IntegrationBranchOf(wish, ids[i])) {
		return ids[i], false, nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return "", false, err
	}
	if merge := MergeRunning(wish, tasks, ids[i]); merge != "" {
		return "", false, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"cannot change the integration branch now: %s; change it once that merge has ended", merge))
	}
	return ids[i], true, nil
}

func (w *Wishes) SetIntegration(
	ctx context.Context, req *connect.Request[planv1.WishServiceSetIntegrationRequest],
) (*connect.Response[planv1.WishServiceSetIntegrationResponse], error) {
	// The branch is made before the transaction: a git run in it would hold every other call of Djinn (W504).
	if branch := req.Msg.GetBranch(); branch != "" && req.Msg.GetCreateBranch() {
		wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
		if err != nil {
			return nil, Status(err)
		}
		projectID, change, err := integrationChange(ctx, w.Store, wish, req.Msg)
		if err != nil {
			return nil, Status(err)
		}
		if change {
			if err := createIntegrationBranch(ctx, w.Store, wish, projectID, branch); err != nil {
				return nil, Status(err)
			}
		}
	}
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		if branch := req.Msg.GetBranch(); branch != "" {
			projectID, _, err := integrationChange(ctx, tx, wish, req.Msg)
			if err != nil {
				return err
			}
			SetIntegrationBranch(wish, projectID, branch)
		}
		if n := req.Msg.GetPushAfterMinutes(); n > 0 {
			wish.PushAfterMinutes = n
		}
		if n := req.Msg.GetPushAfterTasks(); n > 0 {
			wish.PushAfterTasks = n
		}
		if m := req.Msg.GetPushMode(); m != planv1.PushMode_PUSH_MODE_UNSPECIFIED {
			wish.PushMode = m
		}
		if s := req.Msg.GetPushStrategy(); s != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
			curStrategy, _, err := EffectiveWishPushStrategy(ctx, tx, w.Home, wish)
			if err != nil {
				return err
			}
			if s != curStrategy {
				tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wish.GetId()})
				if err != nil {
					return err
				}
				for _, t := range tasks {
					if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
						return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
							"cannot switch push strategy: work of wish %q is already merged into its integration branch", wish.GetTitle()))
					}
				}
				if s == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
					if err := RefuseSwitchToAzima(tasks); err != nil {
						return err
					}
				}
			}
			wish.PushStrategy = s
		}
		return tx.Put(wish)
	})
	if err != nil {
		return nil, Status(err)
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceSetIntegrationResponse{Wish: wish}), nil
}

// PushStrategy shows or sets the push strategy of a wish.
func (w *Wishes) PushStrategy(
	ctx context.Context, req *connect.Request[planv1.WishServicePushStrategyRequest],
) (*connect.Response[planv1.WishServicePushStrategyResponse], error) {
	var (
		strategy planv1.PushStrategy
		source   planv1.SettingSource
	)
	if req.Msg.Strategy != nil && req.Msg.GetStrategy() != planv1.PushStrategy_PUSH_STRATEGY_UNSPECIFIED {
		err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
			wish, err := store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId())
			if err != nil {
				return err
			}
			curStrategy, _, err := EffectiveWishPushStrategy(ctx, tx, w.Home, wish)
			if err != nil {
				return err
			}
			newStrategy := req.Msg.GetStrategy()
			if newStrategy != curStrategy {
				tasks, err := store.List[*planv1.Task](ctx, tx, store.Where{"wish_id": wish.GetId()})
				if err != nil {
					return err
				}
				for _, t := range tasks {
					if t.GetIntegration().GetState() == planv1.IntegrationState_INTEGRATION_STATE_COMMITTED {
						return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
							"cannot switch push strategy: work of wish %q is already merged into its integration branch", wish.GetTitle()))
					}
				}
				if newStrategy == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
					if err := RefuseSwitchToAzima(tasks); err != nil {
						return err
					}
				}
			}
			wish.PushStrategy = newStrategy
			strategy = newStrategy
			source = planv1.SettingSource_SETTING_SOURCE_WISH
			return tx.Put(wish)
		})
		if err != nil {
			return nil, Status(err)
		}
		return connect.NewResponse(&planv1.WishServicePushStrategyResponse{
			Strategy: strategy,
			Source:   source,
		}), nil
	}

	wish, err := store.Get[*planv1.Wish](ctx, w.Store, req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	strategy, source, err = EffectiveWishPushStrategy(ctx, w.Store, w.Home, wish)
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServicePushStrategyResponse{
		Strategy: strategy,
		Source:   source,
	}), nil
}
