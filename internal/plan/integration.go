package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// CheckedOutBranch is the branch the checkout in dir is on: "" when it is on none (a detached HEAD), or dir is not in
// Git.
func CheckedOutBranch(ctx context.Context, dir string) string {
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

func (w *Wishes) SetIntegration(
	ctx context.Context, req *connect.Request[planv1.WishServiceSetIntegrationRequest],
) (*connect.Response[planv1.WishServiceSetIntegrationResponse], error) {
	var wish *planv1.Wish
	err := write(ctx, w.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if wish, err = store.Get[*planv1.Wish](ctx, tx, req.Msg.GetWishId()); err != nil {
			return err
		}
		if branch := req.Msg.GetBranch(); branch != "" {
			id := req.Msg.GetProjectId()
			ids := wish.GetProjectIds()
			switch {
			case id == "" && len(ids) != 1:
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"the wish has %d projects: name one with --project-id", len(ids)))
			case id == "":
				id = ids[0]
			}
			i := slices.IndexFunc(ids, func(p string) bool { return strings.EqualFold(p, id) })
			if i < 0 {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("project %s is not one of the wish's projects", id))
			}
			SetIntegrationBranch(wish, ids[i], branch)
		}
		if n := req.Msg.GetCommitAfterMinutes(); n > 0 {
			wish.CommitAfterMinutes = n
		}
		if n := req.Msg.GetCommitAfterTasks(); n > 0 {
			wish.CommitAfterTasks = n
		}
		return tx.Put(wish)
	})
	if err != nil {
		return nil, err
	}
	if err := fill(ctx, w.Store, wish); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceSetIntegrationResponse{Wish: wish}), nil
}
