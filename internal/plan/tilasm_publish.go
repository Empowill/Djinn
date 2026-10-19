package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/tilasm"
)

// Publish publishes a tilasm's latest version on claude.ai. Its first publish waits for the developer's go-ahead,
// given in the window (ConfirmPublish): the lead and the workers publish again what the developer let out, never more.
func (t *Tilasms) Publish(
	ctx context.Context, req *connect.Request[planv1.TilasmServicePublishRequest],
) (*connect.Response[planv1.TilasmServicePublishResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	m := req.Msg
	found, err := findTilasm(ctx, t.Store, m.GetTilasm(), m.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	if wish := m.GetWish(); wish != "" && !strings.EqualFold(found.GetWishId(), wish) {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
			"tilasm %s is not a tilasm of wish %s", found.GetCode(), wish))
	}
	art := found.GetArtifact()
	if art.GetUrl() == "" && art.GetConfirmTime() == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s waits for the developer: its first publish on claude.ai is theirs, from its row in the window's Tilasms "+
				"tab, once they checked that it holds no personal data of a client; ask them in a question if it must go out",
			found.GetCode()))
	}
	if t.Publisher == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("this server publishes no tilasm: run djinn up"))
	}
	if _, busy := t.publishing.LoadOrStore(found.GetId(), true); busy {
		return nil, connect.NewError(connect.CodeAborted, fmt.Errorf("%s is being published already", found.GetCode()))
	}
	defer t.publishing.Delete(found.GetId())
	n, err := pickVersion(found, 0)
	if err != nil {
		return nil, err
	}
	dir, err := tempDir(t.Home, "publish")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer removeIf(dir)
	b, err := tilasm.Build(versionDir(t.Home, found.GetId(), n), dir)
	if err != nil {
		var large *tilasm.TooLarge
		if errors.As(err, &large) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s cannot go on claude.ai: %w", found.GetCode(), err))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var taskID string
	if caller := CallerFromContext(ctx); caller != nil {
		taskID = caller.TaskID
	}
	pub, err := t.Publisher.Publish(ctx, taskID, found.GetCode(), b, art.GetUrl())
	if err != nil {
		var blocked *PublishBlocked
		if errors.As(err, &blocked) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	var published *planv1.Tilasm
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if published, err = store.Get[*planv1.Tilasm](ctx, tx, found.GetId()); err != nil {
			return err
		}
		a := published.GetArtifact()
		if a == nil {
			a = &planv1.TilasmArtifact{}
			published.Artifact = a
		}
		a.Url, a.Version, a.ArtifactVersion = pub.URL, n, pub.Version
		a.PublishTime, a.CostUsd = timestamppb.Now(), pub.CostUSD
		return tx.Put(published)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServicePublishResponse{
		Tilasm: published, Url: pub.URL, Files: int32(len(b.Files)), LeftOut: b.Left, Size: b.Size, CostUsd: pub.CostUSD, //nolint:gosec // G115: MaxFiles at most.
	}), nil
}

// ConfirmPublish records the developer's go-ahead to publish a tilasm, from the window; a second one keeps the first.
func (t *Tilasms) ConfirmPublish(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceConfirmPublishRequest],
) (*connect.Response[planv1.TilasmServiceConfirmPublishResponse], error) {
	if err := t.ready(); err != nil {
		return nil, err
	}
	var confirmed *planv1.Tilasm
	err := write(ctx, t.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if confirmed, err = store.Get[*planv1.Tilasm](ctx, tx, req.Msg.GetTilasmId()); err != nil {
			return err
		}
		if confirmed.GetArtifact().GetConfirmTime() != nil {
			return nil
		}
		if confirmed.Artifact == nil {
			confirmed.Artifact = &planv1.TilasmArtifact{}
		}
		confirmed.Artifact.ConfirmTime = timestamppb.Now()
		return tx.Put(confirmed)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceConfirmPublishResponse{Tilasm: confirmed}), nil
}

// CheckPublish tells the window whether it shows the button that publishes a tilasm.
func (t *Tilasms) CheckPublish(
	ctx context.Context, _ *connect.Request[planv1.TilasmServiceCheckPublishRequest],
) (*connect.Response[planv1.TilasmServiceCheckPublishResponse], error) {
	res := &planv1.TilasmServiceCheckPublishResponse{}
	if t.Publisher == nil {
		res.Block, res.Detail = planv1.PublishBlock_PUBLISH_BLOCK_CHECK_FAILED, "this server publishes no tilasm"
	} else {
		res.Block, res.Detail, _ = t.Publisher.Check(ctx)
	}
	res.Available = res.Block == planv1.PublishBlock_PUBLISH_BLOCK_UNSPECIFIED
	return connect.NewResponse(res), nil
}
