package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func (t *Tilasms) Comments(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceCommentsRequest],
) (*connect.Response[planv1.TilasmServiceCommentsResponse], error) {
	tilasm, err := findTilasm(ctx, t.Store, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, Status(err)
	}
	sent, err := sentComments(ctx, t.Store, tilasm.GetId())
	if err != nil {
		return nil, Status(err)
	}
	if n := req.Msg.GetSend(); n > 0 {
		last := int32(0)
		if len(sent) > 0 {
			last = sent[len(sent)-1].GetSend()
		}
		sent = slices.DeleteFunc(sent, func(c *planv1.TilasmComment) bool { return c.GetSend() != n })
		if len(sent) == 0 {
			if last == 0 {
				return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%s has no comment sent", tilasm.GetCode()))
			}
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%s has no send %d: it has 1 to %d", tilasm.GetCode(), n, last))
		}
	}
	codes, err := replyTaskCodes(ctx, t.Store, sent)
	if err != nil {
		return nil, Status(err)
	}
	choices, err := sendChoices(ctx, t.Store, tilasm)
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.TilasmServiceCommentsResponse{
		Text: commentsText(tilasm, sent, codes, choices), Comments: sent,
	}), nil
}

// sentComments are the comments sent on a tilasm, by send then code: never a draft, which is the developer's alone.
func sentComments(ctx context.Context, r store.Reader, tilasmID string) ([]*planv1.TilasmComment, error) {
	all, err := store.List[*planv1.TilasmComment](ctx, r, store.Where{"tilasm_id": tilasmID})
	if err != nil {
		return nil, err
	}
	all = slices.DeleteFunc(all, func(c *planv1.TilasmComment) bool {
		return c.GetState() != planv1.CommentState_COMMENT_STATE_SENT
	})
	slices.SortFunc(all, func(a, b *planv1.TilasmComment) int {
		return cmp.Or(cmp.Compare(a.GetSend(), b.GetSend()), cmp.Compare(commentNumber(a.GetCode()), commentNumber(b.GetCode())))
	})
	return all, nil
}

// commentNumber is the number of a comment's code: 12 for C12.
func commentNumber(code string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(code), "C"))
	return n
}

// replyTaskCodes are the codes of the tasks the replies name, by identifier; a task no longer stored keeps its
// identifier.
func replyTaskCodes(ctx context.Context, r store.Reader, comments []*planv1.TilasmComment) (map[string]string, error) {
	codes := map[string]string{}
	for _, c := range comments {
		for _, id := range c.GetReply().GetTaskIds() {
			if _, ok := codes[id]; ok {
				continue
			}
			task, err := store.Get[*planv1.Task](ctx, r, id)
			switch {
			case errors.Is(err, store.ErrNotFound):
				codes[id] = id
			case err != nil:
				return nil, err
			default:
				codes[id] = cmp.Or(task.GetCode(), id)
			}
		}
	}
	return codes, nil
}

// sendChoices say what the developer asked with each send of a tilasm, by its number, and with which question: the
// line commentsText prints in its head.
func sendChoices(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm) (map[int32]string, error) {
	sends, err := store.List[*planv1.TilasmSend](ctx, r, store.Where{"tilasm_id": tilasm.GetId()})
	if err != nil {
		return nil, err
	}
	out := map[int32]string{}
	for _, s := range sends {
		choice := "iterate: put its next version with them"
		if s.GetChoice() == planv1.SendChoice_SEND_CHOICE_LAUNCH {
			choice = "launch its work with them"
			if q, err := store.Get[*planv1.Question](ctx, r, s.GetQuestionId()); err == nil {
				choice = "launch " + q.GetCode() + " with them"
			}
		}
		out[s.GetNumber()] = choice
	}
	return out, nil
}

// commentsText lays out the comments sent on a tilasm for the lead: the tilasm, then each send with what the developer
// chose (choices, by send), then each comment with what it is on, so that it reads them without reading the tilasm
// again.
func commentsText(
	tilasm *planv1.Tilasm, sent []*planv1.TilasmComment, taskCodes map[string]string, choices map[int32]string,
) string {
	latest := latestVersion(tilasm)
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · version %d\n", tilasm.GetCode(), oneLine(tilasm.GetTitle()), latest)
	if len(sent) == 0 {
		b.WriteString("No comment sent yet.\n")
		return b.String()
	}
	for i, c := range sent {
		if i == 0 || c.GetSend() != sent[i-1].GetSend() {
			n := 0
			for _, o := range sent[i:] {
				if o.GetSend() == c.GetSend() {
					n++
				}
			}
			fmt.Fprintf(&b, "\nsend %d", c.GetSend())
			if t := c.GetSendTime(); t != nil {
				fmt.Fprintf(&b, " · %s", when(t.AsTime()))
			}
			if n == 1 {
				b.WriteString(" · 1 comment")
			} else {
				fmt.Fprintf(&b, " · %d comments", n)
			}
			if choice := choices[c.GetSend()]; choice != "" {
				b.WriteString(" · " + choice)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n" + commentHead(c, latest) + "\n")
		if q := commentQuote(c.GetAnchor()); q != "" {
			b.WriteString(indent("“"+q+"”") + "\n")
		}
		b.WriteString(indent(c.GetText()) + "\n")
		if r := c.GetReply(); r != nil {
			head := "Reply"
			if a := r.GetAuthor(); a != "" {
				head += " from " + a
			}
			var tasks []string
			for _, id := range r.GetTaskIds() {
				tasks = append(tasks, taskCodes[id])
			}
			if len(tasks) > 0 {
				head += ", tasks " + strings.Join(tasks, " ")
			}
			b.WriteString(indent(head+": "+r.GetText()) + "\n")
		}
	}
	return b.String()
}

// commentHead is a comment's first line: its code and what it is on, and how its anchor stands in a later version.
func commentHead(c *planv1.TilasmComment, latest int32) string {
	a := c.GetAnchor()
	head := c.GetCode() + " on the whole tilasm"
	var name string // an element the developer saw no label for: its id, else its path
	switch {
	case a.GetExact() != "":
		head = c.GetCode() + " on a passage"
	case a.GetElementId() != "" || a.GetPath() != "" || a.GetFingerprint() != "":
		head = c.GetCode() + " on an element"
		if name = a.GetPath(); a.GetElementId() != "" {
			name = "#" + a.GetElementId()
		}
	}
	parts := []string{head}
	if l := cmp.Or(oneLine(a.GetLabel()), name); l != "" {
		parts = append(parts, l)
	}
	if p := a.GetPage(); p != "" && p != tilasmIndex {
		parts = append(parts, "page "+p)
	}
	if v := c.GetVersion(); v != 0 && v != latest {
		parts = append(parts, fmt.Sprintf("written on version %d", v))
	}
	switch a.GetStatus() {
	case planv1.AnchorStatus_ANCHOR_STATUS_CHANGED:
		parts = append(parts, fmt.Sprintf("changed in version %d", a.GetStatusVersion()))
	case planv1.AnchorStatus_ANCHOR_STATUS_LOST:
		parts = append(parts, fmt.Sprintf("gone from version %d", a.GetStatusVersion()))
	}
	if f := c.GetFollows(); f != "" {
		parts = append(parts, "following up "+f)
	}
	return strings.Join(parts, ", ")
}

// commentQuote is what a comment's anchor quotes: the passage, or the start of the element's text.
func commentQuote(a *planv1.TilasmAnchor) string {
	return oneLine(cmp.Or(a.GetExact(), a.GetFingerprint()))
}

// indent puts each line of text two spaces in.
func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("  "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// maxCommentCode is the last code a tilasm's comments may take: C9999.
const maxCommentCode = 9999

func (t *Tilasms) PutComment(
	ctx context.Context, req *connect.Request[planv1.TilasmServicePutCommentRequest],
) (*connect.Response[planv1.TilasmServicePutCommentResponse], error) {
	m := req.Msg
	if m.GetDeleted() == nil && strings.TrimSpace(m.GetText()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("text: a comment says something"))
	}
	var comment *planv1.TilasmComment
	err := write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, tx, m.GetTilasmId())
		if err != nil {
			return err
		}
		now := timestamppb.Now()
		switch {
		case m.GetDeleted() != nil:
			comment, err = putBack(ctx, tx, tilasm, m.GetDeleted())
		case m.GetId() != "":
			comment, err = editDraft(ctx, tx, tilasm, m, now)
		default:
			comment = &planv1.TilasmComment{
				Id: store.NewID(), TilasmId: tilasm.GetId(), WishId: tilasm.GetWishId(), Text: m.GetText(),
				Anchor: m.GetAnchor(), Version: latestVersion(tilasm), State: planv1.CommentState_COMMENT_STATE_DRAFT,
				CreateTime: now, UpdateTime: now, Follows: m.GetFollows(),
			}
			comment.Code, err = nextCommentCode(ctx, tx, tilasm)
		}
		if err != nil {
			return err
		}
		return tx.Put(comment)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServicePutCommentResponse{Comment: comment}), nil
}

// editDraft is the draft m names with m's text: a sent comment no longer changes.
func editDraft(
	ctx context.Context, tx *store.Tx, tilasm *planv1.Tilasm, m *planv1.TilasmServicePutCommentRequest, now *timestamppb.Timestamp,
) (*planv1.TilasmComment, error) {
	comment, err := store.Get[*planv1.TilasmComment](ctx, tx, m.GetId())
	if err != nil {
		return nil, err
	}
	if comment.GetTilasmId() != tilasm.GetId() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is on another tilasm than %s", comment.GetCode(), tilasm.GetCode()))
	}
	if err := stillDraft(comment); err != nil {
		return nil, err
	}
	comment.Text, comment.Follows, comment.UpdateTime = m.GetText(), m.GetFollows(), now
	if m.GetAnchor() != nil {
		comment.Anchor = m.GetAnchor()
	}
	return comment, nil
}

// putBack is the draft DeleteComment took, as it was: with its code, unless another comment took it meanwhile.
func putBack(ctx context.Context, tx *store.Tx, tilasm *planv1.Tilasm, deleted *planv1.TilasmComment) (*planv1.TilasmComment, error) {
	comment := proto.CloneOf(deleted)
	if comment.GetTilasmId() != tilasm.GetId() || comment.GetState() != planv1.CommentState_COMMENT_STATE_DRAFT {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("deleted: a draft of this tilasm is put back"))
	}
	if _, err := store.Get[*planv1.TilasmComment](ctx, tx, comment.GetId()); err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%s is already there", comment.GetCode()))
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	comment.WishId = tilasm.GetWishId()
	taken, err := store.List[*planv1.TilasmComment](ctx, tx, store.Where{"tilasm_id": tilasm.GetId(), "code": comment.GetCode()})
	if err != nil {
		return nil, err
	}
	if len(taken) > 0 || comment.GetCode() == "" {
		if comment.Code, err = nextCommentCode(ctx, tx, tilasm); err != nil {
			return nil, err
		}
	}
	return comment, nil
}

// stillDraft refuses a comment sent: it belongs to the Djinn.
func stillDraft(c *planv1.TilasmComment) error {
	if c.GetState() == planv1.CommentState_COMMENT_STATE_DRAFT {
		return nil
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s was sent: it no longer changes; write another", c.GetCode()))
}

// nextCommentCode follows the highest code of the tilasm's comments.
func nextCommentCode(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm) (string, error) {
	all, err := store.List[*planv1.TilasmComment](ctx, r, store.Where{"tilasm_id": tilasm.GetId()})
	if err != nil {
		return "", err
	}
	last := 0
	for _, c := range all {
		last = max(last, commentNumber(c.GetCode()))
	}
	if last >= maxCommentCode {
		return "", connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("%s has used its %d comment codes", tilasm.GetCode(), maxCommentCode))
	}
	return "C" + strconv.Itoa(last+1), nil
}

// latestVersion is the number of a tilasm's latest version.
func latestVersion(tilasm *planv1.Tilasm) int32 {
	if vs := tilasm.GetVersions(); len(vs) > 0 {
		return vs[len(vs)-1].GetNumber()
	}
	return 0
}

func (t *Tilasms) DeleteComment(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceDeleteCommentRequest],
) (*connect.Response[planv1.TilasmServiceDeleteCommentResponse], error) {
	var comment *planv1.TilasmComment
	err := write(ctx, t.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if comment, err = store.Get[*planv1.TilasmComment](ctx, tx, req.Msg.GetId()); err != nil {
			return err
		}
		if err := stillDraft(comment); err != nil {
			return err
		}
		return tx.Delete(comment)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceDeleteCommentResponse{Comment: comment}), nil
}

func (t *Tilasms) Drafts(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceDraftsRequest],
) (*connect.Response[planv1.TilasmServiceDraftsResponse], error) {
	all, err := store.List[*planv1.TilasmComment](ctx, t.Store, store.Where{"wish_id": req.Msg.GetWish()})
	if err != nil {
		return nil, Status(err)
	}
	drafts := slices.DeleteFunc(all, func(c *planv1.TilasmComment) bool {
		return c.GetState() != planv1.CommentState_COMMENT_STATE_DRAFT
	})
	tilasms, err := store.List[*planv1.Tilasm](ctx, t.Store, store.Where{"wish_id": req.Msg.GetWish()})
	if err != nil {
		return nil, Status(err)
	}
	holds, err := tilasmHolds(ctx, t.Store, req.Msg.GetWish(), tilasms)
	if err != nil {
		return nil, Status(err)
	}
	res := &planv1.TilasmServiceDraftsResponse{Comments: drafts}
	for _, tilasm := range tilasms {
		if h := holds[tilasm.GetId()]; h != nil {
			res.Holds = append(res.Holds, h)
		}
	}
	return connect.NewResponse(res), nil
}

func (t *Tilasms) PlaceComments(
	ctx context.Context, req *connect.Request[planv1.TilasmServicePlaceCommentsRequest],
) (*connect.Response[planv1.TilasmServicePlaceCommentsResponse], error) {
	m := req.Msg
	res := &planv1.TilasmServicePlaceCommentsResponse{}
	err := write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, tx, m.GetTilasmId())
		if err != nil {
			return err
		}
		// The window read an earlier version: what it found there no longer says where the comments stand.
		if m.GetVersion() != latestVersion(tilasm) {
			return nil
		}
		for _, p := range m.GetPlaces() {
			comment, err := store.Get[*planv1.TilasmComment](ctx, tx, p.GetId())
			if err != nil {
				return err
			}
			if comment.GetTilasmId() != tilasm.GetId() {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is on another tilasm than %s", comment.GetCode(), tilasm.GetCode()))
			}
			a := comment.GetAnchor()
			if a == nil {
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is on the whole tilasm: it has no place", comment.GetCode()))
			}
			if a.GetStatus() == p.GetStatus() && a.GetStatusVersion() == m.GetVersion() {
				continue
			}
			a.Status, a.StatusVersion = p.GetStatus(), m.GetVersion()
			if err := tx.Put(comment); err != nil {
				return err
			}
			res.Comments = append(res.Comments, comment)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
