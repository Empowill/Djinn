package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The Djinn's reply to the developer's comments (L08, step 7, decision Q29): one per sent comment, with the tasks made
// of it, shown under its card; the wish's brief lists the tilasms whose sent comments still wait for one.

func (t *Tilasms) Reply(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceReplyRequest],
) (*connect.Response[planv1.TilasmServiceReplyResponse], error) {
	m := req.Msg
	if strings.TrimSpace(m.GetText()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("text: a reply says what you did, or why not"))
	}
	res := &planv1.TilasmServiceReplyResponse{}
	err := write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		tilasm, err := findTilasm(ctx, tx, m.GetTilasm(), m.GetWish())
		if err != nil {
			return err
		}
		code := strings.ToUpper(m.GetComment())
		found, err := store.List[*planv1.TilasmComment](ctx, tx, store.Where{"tilasm_id": tilasm.GetId(), "code": code})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s has no comment %s", tilasm.GetCode(), code))
		}
		comment := found[0]
		if comment.GetState() != planv1.CommentState_COMMENT_STATE_SENT {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s %s is a draft: the developer has not sent it", tilasm.GetCode(), code))
		}
		tasks, err := resolveCites(ctx, tx, tilasm.GetWishId(), m.GetTask())
		if err != nil {
			return err
		}
		comment.Reply = &planv1.CommentReply{
			Text: m.GetText(), Author: cmp.Or(m.GetAuthor(), defaultAuthor), CreateTime: timestamppb.Now(), TaskIds: tasks,
		}
		if err := tx.Put(comment); err != nil {
			return err
		}
		sent, err := sentComments(ctx, tx, tilasm.GetId())
		if err != nil {
			return err
		}
		for _, c := range sent {
			if c.GetReply() == nil {
				res.Unanswered++
			}
		}
		res.Comment = comment
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}

// briefUnanswered is how many codes of the comments without a reply a brief line names.
const briefUnanswered = 8

// unansweredLines are the lines of a wish's brief for its tilasms whose sent comments wait for a reply, by tilasm
// identifier: how many were sent, how many wait, which. A draft is never counted: it is the developer's alone.
func unansweredLines(ctx context.Context, r store.Reader, wishID string) (map[string]string, error) {
	all, err := store.List[*planv1.TilasmComment](ctx, r, store.Where{"wish_id": wishID})
	if err != nil {
		return nil, err
	}
	sent := map[string][]*planv1.TilasmComment{}
	for _, c := range all {
		if c.GetState() == planv1.CommentState_COMMENT_STATE_SENT {
			sent[c.GetTilasmId()] = append(sent[c.GetTilasmId()], c)
		}
	}
	out := map[string]string{}
	for id, comments := range sent {
		var waiting []string
		for _, c := range comments {
			if c.GetReply() == nil {
				waiting = append(waiting, c.GetCode())
			}
		}
		if len(waiting) == 0 {
			continue
		}
		slices.SortFunc(waiting, func(a, b string) int { return cmp.Compare(commentNumber(a), commentNumber(b)) })
		named := strings.Join(waiting[:min(len(waiting), briefUnanswered)], ", ")
		if len(waiting) > briefUnanswered {
			named += ", …"
		}
		count := fmt.Sprintf("%d comments sent", len(comments))
		if len(comments) == 1 {
			count = "1 comment sent"
		}
		out[id] = fmt.Sprintf("%s, %d without a reply (%s)", count, len(waiting), named)
	}
	return out, nil
}
