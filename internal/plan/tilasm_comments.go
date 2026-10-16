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
	return connect.NewResponse(&planv1.TilasmServiceCommentsResponse{
		Text: commentsText(tilasm, sent, codes), Comments: sent,
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

// commentsText lays out the comments sent on a tilasm for the lead: the tilasm, then each send, then each comment
// with what it is on, so that it reads them without reading the tilasm again.
func commentsText(tilasm *planv1.Tilasm, sent []*planv1.TilasmComment, taskCodes map[string]string) string {
	var latest int32
	if vs := tilasm.GetVersions(); len(vs) > 0 {
		latest = vs[len(vs)-1].GetNumber()
	}
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
				b.WriteString(" · 1 comment\n")
			} else {
				fmt.Fprintf(&b, " · %d comments\n", n)
			}
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
