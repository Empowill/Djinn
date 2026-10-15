package plan

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// contextBytes is how much of a document djinn doc context gives whole: beyond it, a window around the passage.
const contextBytes = 256 << 10

// errRepeated stops a transaction whose request was already done: the record it made is returned instead.
var errRepeated = errors.New("request already done")

// markdownOf is a Markdown tilasm's latest text, whole. Any other tilasm is refused: only a Markdown document is
// discussed.
func (t *Tilasms) markdownOf(tilasm *planv1.Tilasm) ([]byte, error) {
	if !markdown(tilasm) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"tilasm %s is an HTML page: only a Markdown tilasm has discussions and proposals", tilasm.GetCode()))
	}
	n, err := pickVersion(tilasm, 0)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(versionDir(t.Home, tilasm.GetId(), n), tilasmMarkdown))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("read the text of %s: %w", tilasm.GetCode(), err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, t.ceiling(tilasm)+1))
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("read the text of %s: %w", tilasm.GetCode(), err))
	}
	return data, nil
}

// discussed is the Markdown tilasm a request names, with its latest text rendered.
func (t *Tilasms) discussed(
	ctx context.Context, ref *planv1.TilasmRef, wish string,
) (*planv1.Tilasm, *rendered, error) {
	if err := t.ready(); err != nil {
		return nil, nil, err
	}
	tilasm, err := findTilasm(ctx, t.Store, ref, wish)
	if err != nil {
		return nil, nil, Status(err)
	}
	src, err := t.markdownOf(tilasm)
	if err != nil {
		return nil, nil, err
	}
	return tilasm, renderMarkdown(src), nil
}

// takeAnchor finds the passage a request names in the latest text, and returns its anchor as Djinn stores it: the
// passage as the document reads, with the context that tells it from any other. A passage not found once is refused,
// saying why.
func takeAnchor(r *rendered, tilasm *planv1.Tilasm, quote, prefix, suffix string) (*planv1.TilasmAnchor, anchorPlace, error) {
	if strings.TrimSpace(quote+prefix+suffix) == "" && strings.TrimSpace(r.text) != "" {
		return nil, anchorPlace{}, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"say where: --quote, the passage as the document reads; or --prefix and --suffix, the text around a place to insert at"))
	}
	p := r.resolve(&planv1.TilasmAnchor{Quote: quote, Prefix: prefix, Suffix: suffix}, false)
	if p.state != planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED {
		return nil, p, refused(tilasm, p, r, "the passage")
	}
	return r.anchorAt(p, tilasm.GetRevision()), p, nil
}

// refused says why a passage cannot be used: gone, changed or ambiguous.
func refused(tilasm *planv1.Tilasm, p anchorPlace, r *rendered, what string) error {
	switch p.state {
	case planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_AMBIGUOUS:
		n := ""
		if p.count > 1 {
			n = fmt.Sprintf(" %d times", p.count)
		}
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"ambiguous: %s is in tilasm %s%s, and what surrounds it does not say which; add --prefix or --suffix, "+
				"the text just before or after it", what, tilasm.GetCode(), n))
	case planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_CHANGED:
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"changed: %s of tilasm %s changed since it was taken; it now reads %q", what, tilasm.GetCode(), clip(r.current(p), 300)))
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"missing: %s is not in tilasm %s at revision %d; quote it as the document reads, without its Markdown markup "+
			"(djinn doc context %s shows the text)", what, tilasm.GetCode(), tilasm.GetRevision(), tilasm.GetCode()))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return firstRunes(s, n) + "…"
}

// fillThread computes where a discussion's passage stands in the latest text.
func fillThread(r *rendered, thread *planv1.TilasmThread) anchorPlace {
	p := r.resolve(thread.GetAnchor(), true)
	thread.AnchorState, thread.Current = p.state, r.current(p)
	return p
}

// fillProposal computes where a pending proposal's passage stands in the latest text, and what it would replace there.
// A decided one is left as stored: its passage is history.
func fillProposal(r *rendered, proposal *planv1.TilasmProposal) anchorPlace {
	if proposal.GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING {
		return anchorPlace{}
	}
	p := r.resolve(proposal.GetAnchor(), true)
	proposal.AnchorState, proposal.Original, proposal.Current = p.state, r.original(p), r.current(p)
	return p
}

// threadByRequest is the tilasm's discussion a request opened; nil when none did.
func threadByRequest(ctx context.Context, r store.Reader, tilasmID, requestID string) (*planv1.TilasmThread, error) {
	if requestID == "" {
		return nil, nil
	}
	found, err := store.List[*planv1.TilasmThread](ctx, r, store.Where{"tilasm_id": tilasmID, "request_id": requestID})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return found[0], nil
}

// proposalByRequest is the tilasm's proposal a request made; nil when none did.
func proposalByRequest(ctx context.Context, r store.Reader, tilasmID, requestID string) (*planv1.TilasmProposal, error) {
	if requestID == "" {
		return nil, nil
	}
	found, err := store.List[*planv1.TilasmProposal](ctx, r, store.Where{"tilasm_id": tilasmID, "request_id": requestID})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return found[0], nil
}

// unescaped is a value an agent wrote on one line with --escapes: \n a line break, \t a tab, \\ a backslash. Any
// other backslash escape is refused, so a value written for the shell is never stored half read.
func unescaped(field, value string) (string, error) {
	if !strings.Contains(value, `\`) {
		return value, nil
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(value) {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"%s ends with a lone backslash: with --escapes, write \\\\ for a backslash", field))
		}
		i++
		switch value[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		default:
			r, _ := utf8.DecodeRuneInString(value[i:])
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"%s holds the escape \\%c, which --escapes does not know: only \\n, \\t and \\\\ are read", field, r))
		}
	}
	return b.String(), nil
}

// unescapedComment is the request with its text decoded when it asks for --escapes, the flag then cleared: the
// journal and the request identifier see the text as stored.
func unescapedComment(m *planv1.TilasmServiceCommentRequest) (*planv1.TilasmServiceCommentRequest, error) {
	if !m.GetEscapes() {
		return m, nil
	}
	text, err := unescaped("text", m.GetText())
	if err != nil {
		return nil, err
	}
	m = proto.CloneOf(m)
	m.Text, m.Escapes = text, false
	return m, nil
}

// unescapedReply is the request with its text decoded when it asks for --escapes, the flag then cleared.
func unescapedReply(m *planv1.TilasmServiceReplyRequest) (*planv1.TilasmServiceReplyRequest, error) {
	if !m.GetEscapes() {
		return m, nil
	}
	text, err := unescaped("text", m.GetText())
	if err != nil {
		return nil, err
	}
	m = proto.CloneOf(m)
	m.Text, m.Escapes = text, false
	return m, nil
}

// unescapedProposal is the request with its replacement and note decoded when it asks for --escapes, the flag then
// cleared. A replacement file is read as it is.
func unescapedProposal(m *planv1.TilasmServiceProposeRequest) (*planv1.TilasmServiceProposeRequest, error) {
	if !m.GetEscapes() {
		return m, nil
	}
	replacement, err := unescaped("replacement", m.GetReplacement())
	if err != nil {
		return nil, err
	}
	note, err := unescaped("note", m.GetNote())
	if err != nil {
		return nil, err
	}
	m = proto.CloneOf(m)
	m.Replacement, m.Note, m.Escapes = replacement, note, false
	return m, nil
}

func (t *Tilasms) Comment(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceCommentRequest],
) (*connect.Response[planv1.TilasmServiceCommentResponse], error) {
	m, err := unescapedComment(req.Msg)
	if err != nil {
		return nil, err
	}
	tilasm, r, err := t.discussed(ctx, m.GetTilasm(), m.GetWish())
	if err != nil {
		return nil, err
	}
	if same, err := threadByRequest(ctx, t.Store, tilasm.GetId(), m.GetRequestId()); err != nil || same != nil {
		if err != nil {
			return nil, Status(err)
		}
		fillThread(r, same)
		return connect.NewResponse(&planv1.TilasmServiceCommentResponse{Thread: same}), nil
	}
	anchor, _, err := takeAnchor(r, tilasm, m.GetQuote(), m.GetPrefix(), m.GetSuffix())
	if err != nil {
		return nil, err
	}
	now := t.now()
	thread := &planv1.TilasmThread{
		Id: store.NewID(), WishId: tilasm.GetWishId(), TilasmId: tilasm.GetId(), Anchor: anchor,
		CreateTime: now, UpdateTime: now, RequestId: m.GetRequestId(), Kind: ThreadKind(&planv1.TilasmThread{Kind: m.GetKind()}),
		Messages: []*planv1.TilasmMessage{{
			Id: store.NewID(), Author: cmp.Or(m.GetAuthor(), defaultAuthor), Text: m.GetText(), CreateTime: now,
			RequestId: m.GetRequestId(),
		}},
	}
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		if same, err := threadByRequest(ctx, tx, tilasm.GetId(), m.GetRequestId()); err != nil || same != nil {
			if err == nil {
				thread, err = same, errRepeated
			}
			return err
		}
		return tx.Put(thread)
	})
	if err != nil && !errors.Is(err, errRepeated) {
		return nil, err
	}
	if err == nil {
		t.discussedBy(ctx, thread, thread.GetMessages()[0].GetAuthor())
	}
	fillThread(r, thread)
	// The window shows at once whether the document agent took the message up.
	if err := fillAgentPending(ctx, t.Store, thread.GetWishId(), thread.GetTilasmId(), []*planv1.TilasmThread{thread}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceCommentResponse{Thread: thread}), nil
}

// threadOf is a discussion with its tilasm's latest text rendered.
func (t *Tilasms) threadOf(ctx context.Context, id string) (*planv1.TilasmThread, *planv1.Tilasm, *rendered, error) {
	if err := t.ready(); err != nil {
		return nil, nil, nil, err
	}
	thread, err := store.Get[*planv1.TilasmThread](ctx, t.Store, id)
	if err != nil {
		return nil, nil, nil, Status(notFound(err, "discussion", id))
	}
	tilasm, r, err := t.discussed(ctx, &planv1.TilasmRef{Ref: &planv1.TilasmRef_Id{Id: thread.GetTilasmId()}}, "")
	if err != nil {
		return nil, nil, nil, err
	}
	return thread, tilasm, r, nil
}

// notFound names what is not found.
func notFound(err error, what, id string) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("no %s %s", what, id))
	}
	return err
}

func (t *Tilasms) Reply(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceReplyRequest],
) (*connect.Response[planv1.TilasmServiceReplyResponse], error) {
	m, err := unescapedReply(req.Msg)
	if err != nil {
		return nil, err
	}
	_, _, r, err := t.threadOf(ctx, m.GetThread())
	if err != nil {
		return nil, err
	}
	var thread *planv1.TilasmThread
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if thread, err = store.Get[*planv1.TilasmThread](ctx, tx, m.GetThread()); err != nil {
			return err
		}
		if rid := m.GetRequestId(); rid != "" && slices.ContainsFunc(thread.GetMessages(), func(msg *planv1.TilasmMessage) bool {
			return msg.GetRequestId() == rid
		}) {
			return errRepeated
		}
		now := t.now()
		thread.Messages = append(thread.Messages, &planv1.TilasmMessage{
			Id: store.NewID(), Author: cmp.Or(m.GetAuthor(), defaultAuthor), Text: m.GetText(), CreateTime: now,
			RequestId: m.GetRequestId(),
		})
		thread.UpdateTime = now
		return tx.Put(thread)
	})
	if err != nil && !errors.Is(err, errRepeated) {
		return nil, err
	}
	if err == nil {
		t.discussedBy(ctx, thread, cmp.Or(m.GetAuthor(), defaultAuthor))
	}
	fillThread(r, thread)
	// The window shows at once whether the document agent took the message up.
	if err := fillAgentPending(ctx, t.Store, thread.GetWishId(), thread.GetTilasmId(), []*planv1.TilasmThread{thread}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceReplyResponse{Thread: thread}), nil
}

func (t *Tilasms) Resolve(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceResolveRequest],
) (*connect.Response[planv1.TilasmServiceResolveResponse], error) {
	m := req.Msg
	_, _, r, err := t.threadOf(ctx, m.GetThread())
	if err != nil {
		return nil, err
	}
	var thread *planv1.TilasmThread
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if thread, err = store.Get[*planv1.TilasmThread](ctx, tx, m.GetThread()); err != nil {
			return err
		}
		if thread.GetResolved() == m.GetResolved() {
			return errRepeated
		}
		thread.Resolved, thread.UpdateTime = m.GetResolved(), t.now()
		return tx.Put(thread)
	})
	if err != nil && !errors.Is(err, errRepeated) {
		return nil, err
	}
	fillThread(r, thread)
	return connect.NewResponse(&planv1.TilasmServiceResolveResponse{Thread: thread}), nil
}

// replacement is the Markdown a proposal puts in place of its passage: given, read from a file, or none to delete.
func (t *Tilasms) replacement(m *planv1.TilasmServiceProposeRequest, ceiling int64) (string, error) {
	text, file := m.GetReplacement(), m.GetReplacementFile()
	switch {
	case file != "" && text != "":
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("give --replacement or --replacement-file, not both"))
	case m.GetDelete() && (file != "" || text != ""):
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("--delete takes no replacement"))
	case m.GetDelete():
		return "", nil
	case file == "" && text == "":
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New(
			"give the new Markdown with --replacement or --replacement-file, or --delete to delete the passage"))
	case file == "":
		return text, nil
	case !filepath.IsAbs(file):
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replacement_file: %q is not an absolute path", file))
	}
	f, err := os.Open(file)
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replacement_file: %w", err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, ceiling+1))
	if err != nil {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replacement_file: %w", err))
	}
	if int64(len(data)) > ceiling {
		return "", connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(
			"replacement_file holds more than the tilasm's ceiling, %s", bytesText(ceiling)))
	}
	if len(data) == 0 {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"replacement_file %s is empty: give --delete to delete the passage", file))
	}
	return string(data), nil
}

func (t *Tilasms) Propose(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceProposeRequest],
) (*connect.Response[planv1.TilasmServiceProposeResponse], error) {
	m, err := unescapedProposal(req.Msg)
	if err != nil {
		return nil, err
	}
	tilasm, r, err := t.discussed(ctx, m.GetTilasm(), m.GetWish())
	if err != nil {
		return nil, err
	}
	if same, err := proposalByRequest(ctx, t.Store, tilasm.GetId(), m.GetRequestId()); err != nil || same != nil {
		if err != nil {
			return nil, Status(err)
		}
		fillProposal(r, same)
		return connect.NewResponse(&planv1.TilasmServiceProposeResponse{Proposal: same}), nil
	}
	text, err := t.replacement(m, t.ceiling(tilasm))
	if err != nil {
		return nil, err
	}
	var anchor *planv1.TilasmAnchor
	var p anchorPlace
	named := strings.TrimSpace(m.GetQuote()+m.GetPrefix()+m.GetSuffix()) != ""
	if id := m.GetThread(); id != "" {
		thread, err := store.Get[*planv1.TilasmThread](ctx, t.Store, id)
		if err != nil {
			return nil, Status(notFound(err, "discussion", id))
		}
		if !strings.EqualFold(thread.GetTilasmId(), tilasm.GetId()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"discussion %s is not on tilasm %s", id, tilasm.GetCode()))
		}
		if !named {
			p = r.resolve(thread.GetAnchor(), true)
			if p.state != planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED {
				return nil, refused(tilasm, p, r, "the passage of discussion "+id)
			}
			anchor = r.anchorAt(p, tilasm.GetRevision())
		}
	}
	if anchor == nil {
		if anchor, p, err = takeAnchor(r, tilasm, m.GetQuote(), m.GetPrefix(), m.GetSuffix()); err != nil {
			return nil, err
		}
	}
	if m.GetDelete() && p.at == p.end {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"--delete needs a passage: --quote it; a place between --prefix and --suffix holds nothing to delete"))
	}
	journaled := proto.CloneOf(m)
	journaled.Replacement = "" // The proposal holds it.
	now := t.now()
	proposal := &planv1.TilasmProposal{
		Id: store.NewID(), WishId: tilasm.GetWishId(), TilasmId: tilasm.GetId(), ThreadId: m.GetThread(),
		Anchor: anchor, Replacement: text, Author: cmp.Or(m.GetAuthor(), defaultAuthor), Note: m.GetNote(),
		Status: planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING, BaseRevision: tilasm.GetRevision(),
		CreateTime: now, RequestId: m.GetRequestId(),
	}
	err = write(ctx, t.Store, req.Spec(), journaled, func(tx *store.Tx) error {
		if same, err := proposalByRequest(ctx, tx, tilasm.GetId(), m.GetRequestId()); err != nil || same != nil {
			if err == nil {
				proposal, err = same, errRepeated
			}
			return err
		}
		return tx.Put(proposal)
	})
	if err != nil && !errors.Is(err, errRepeated) {
		return nil, err
	}
	fillProposal(r, proposal)
	return connect.NewResponse(&planv1.TilasmServiceProposeResponse{Proposal: proposal}), nil
}

// proposalOf is a proposal with its tilasm.
func (t *Tilasms) proposalOf(ctx context.Context, id string) (*planv1.TilasmProposal, *planv1.Tilasm, error) {
	if err := t.ready(); err != nil {
		return nil, nil, err
	}
	proposal, err := store.Get[*planv1.TilasmProposal](ctx, t.Store, id)
	if err != nil {
		return nil, nil, Status(notFound(err, "proposal", id))
	}
	tilasm, err := store.Get[*planv1.Tilasm](ctx, t.Store, proposal.GetTilasmId())
	if err != nil {
		return nil, nil, Status(err)
	}
	return proposal, tilasm, nil
}

// decided refuses to decide a proposal again the other way.
func decided(proposal *planv1.TilasmProposal, want planv1.TilasmProposalStatus) error {
	s := proposal.GetStatus()
	if s == planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING || s == want {
		return nil
	}
	what := "accepted"
	if s == planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_REJECTED {
		what = "rejected"
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"proposal %s was %s by %s already", proposal.GetId(), what, cmp.Or(proposal.GetDecidedBy(), "someone")))
}

func (t *Tilasms) Accept(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceAcceptRequest],
) (*connect.Response[planv1.TilasmServiceAcceptResponse], error) {
	m := req.Msg
	accepted := planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_ACCEPTED
	proposal, tilasm, err := t.proposalOf(ctx, m.GetProposal())
	if err != nil {
		return nil, err
	}
	if err := decided(proposal, accepted); err != nil {
		return nil, err
	}
	if proposal.GetStatus() == accepted {
		return connect.NewResponse(&planv1.TilasmServiceAcceptResponse{Proposal: proposal, Tilasm: tilasm}), nil
	}
	src, err := t.markdownOf(tilasm)
	if err != nil {
		return nil, err
	}
	r := renderMarkdown(src)
	p := r.resolve(proposal.GetAnchor(), true)
	if p.state != planv1.TilasmAnchorState_TILASM_ANCHOR_STATE_ANCHORED {
		return nil, refused(tilasm, p, r, "the passage of this proposal")
	}
	var text bytes.Buffer
	text.Write(src[:p.src])
	text.WriteString(proposal.GetReplacement())
	text.Write(src[p.srcEnd:])
	by := cmp.Or(m.GetAuthor(), developerAuthor)
	written, err := t.writeMarkdown(ctx, req.Spec(), m, tilasm, markdownWrite{
		base: tilasm.GetRevision(), text: text.String(), author: cmp.Or(proposal.GetAuthor(), defaultAuthor),
		origin: planv1.TilasmOrigin_TILASM_ORIGIN_PROPOSAL,
		also: func(tx *store.Tx, tilasm *planv1.Tilasm) error {
			current, err := store.Get[*planv1.TilasmProposal](ctx, tx, proposal.GetId())
			if err != nil {
				return err
			}
			if current.GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING {
				return errRepeated // Decided meanwhile: nothing is written.
			}
			proposal = current
			proposal.Status, proposal.DecidedBy, proposal.DecideTime = accepted, by, t.now()
			proposal.AcceptedRevision = tilasm.GetRevision()
			return tx.Put(proposal)
		},
	})
	switch {
	case errors.Is(err, errRepeated):
		proposal, tilasm, err = t.proposalOf(ctx, m.GetProposal())
		if err == nil {
			err = decided(proposal, accepted)
		}
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(&planv1.TilasmServiceAcceptResponse{Proposal: proposal, Tilasm: tilasm}), nil
	case connect.CodeOf(err) == connect.CodeAborted:
		return nil, connect.NewError(connect.CodeAborted, fmt.Errorf(
			"tilasm %s changed while the proposal was being accepted: accept it again", tilasm.GetCode()))
	case err != nil:
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceAcceptResponse{Proposal: proposal, Tilasm: written}), nil
}

func (t *Tilasms) Reject(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceRejectRequest],
) (*connect.Response[planv1.TilasmServiceRejectResponse], error) {
	m := req.Msg
	rejected := planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_REJECTED
	proposal, _, err := t.proposalOf(ctx, m.GetProposal())
	if err != nil {
		return nil, err
	}
	if err := decided(proposal, rejected); err != nil {
		return nil, err
	}
	if proposal.GetStatus() == rejected {
		return connect.NewResponse(&planv1.TilasmServiceRejectResponse{Proposal: proposal}), nil
	}
	err = write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		var err error
		if proposal, err = store.Get[*planv1.TilasmProposal](ctx, tx, m.GetProposal()); err != nil {
			return err
		}
		if err := decided(proposal, rejected); err != nil || proposal.GetStatus() == rejected {
			return cmp.Or(err, errRepeated)
		}
		proposal.Status, proposal.DecidedBy, proposal.DecideTime = rejected, cmp.Or(m.GetAuthor(), developerAuthor), t.now()
		return tx.Put(proposal)
	})
	if err != nil && !errors.Is(err, errRepeated) {
		return nil, err
	}
	return connect.NewResponse(&planv1.TilasmServiceRejectResponse{Proposal: proposal}), nil
}

// discussionsOf are a tilasm's discussions and proposals, the first made first.
func discussionsOf(ctx context.Context, r store.Reader, tilasmID string) ([]*planv1.TilasmThread, []*planv1.TilasmProposal, error) {
	where := store.Where{"tilasm_id": tilasmID}
	threads, err := store.List[*planv1.TilasmThread](ctx, r, where)
	if err != nil {
		return nil, nil, err
	}
	proposals, err := store.List[*planv1.TilasmProposal](ctx, r, where)
	if err != nil {
		return nil, nil, err
	}
	slices.SortStableFunc(threads, func(a, b *planv1.TilasmThread) int {
		return cmp.Or(a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), strings.Compare(a.GetId(), b.GetId()))
	})
	slices.SortStableFunc(proposals, func(a, b *planv1.TilasmProposal) int {
		return cmp.Or(a.GetCreateTime().AsTime().Compare(b.GetCreateTime().AsTime()), strings.Compare(a.GetId(), b.GetId()))
	})
	return threads, proposals, nil
}

// position is where a place stands in the document, for ordering: a place not found comes last.
func position(p anchorPlace) int {
	if p.resolvable() {
		return p.src
	}
	return math.MaxInt
}

func (t *Tilasms) Discussions(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceDiscussionsRequest],
) (*connect.Response[planv1.TilasmServiceDiscussionsResponse], error) {
	tilasm, r, err := t.discussed(ctx, req.Msg.GetTilasm(), req.Msg.GetWish())
	if err != nil {
		return nil, err
	}
	threads, proposals, err := discussionsOf(ctx, t.Store, tilasm.GetId())
	if err != nil {
		return nil, Status(err)
	}
	at := map[string]int{}
	for _, thread := range threads {
		at[thread.GetId()] = position(fillThread(r, thread))
	}
	for _, proposal := range proposals {
		p := fillProposal(r, proposal)
		if proposal.GetStatus() != planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING {
			p = r.resolve(proposal.GetAnchor(), true) // Ordered by its passage still, when it is there.
		}
		at[proposal.GetId()] = position(p)
	}
	if err := fillAgentPending(ctx, t.Store, tilasm.GetWishId(), tilasm.GetId(), threads); err != nil {
		return nil, Status(err)
	}
	slices.SortStableFunc(threads, func(a, b *planv1.TilasmThread) int { return cmp.Compare(at[a.GetId()], at[b.GetId()]) })
	slices.SortStableFunc(proposals, func(a, b *planv1.TilasmProposal) int {
		return cmp.Compare(at[a.GetId()], at[b.GetId()])
	})
	return connect.NewResponse(&planv1.TilasmServiceDiscussionsResponse{
		TilasmId: tilasm.GetId(), Revision: tilasm.GetRevision(), Threads: threads, Proposals: proposals,
	}), nil
}

func (t *Tilasms) Context(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceContextRequest],
) (*connect.Response[planv1.TilasmServiceContextResponse], error) {
	m := req.Msg
	tilasm, r, err := t.discussed(ctx, m.GetTilasm(), m.GetWish())
	if err != nil {
		return nil, err
	}
	res := &planv1.TilasmServiceContextResponse{
		TilasmId: tilasm.GetId(), Code: tilasm.GetCode(), Title: tilasm.GetTitle(), Revision: tilasm.GetRevision(),
		TotalBytes: int64(len(r.source)),
	}
	threadID := m.GetThread()
	if id := m.GetProposal(); id != "" {
		proposal, err := store.Get[*planv1.TilasmProposal](ctx, t.Store, id)
		if err != nil {
			return nil, Status(notFound(err, "proposal", id))
		}
		if !strings.EqualFold(proposal.GetTilasmId(), tilasm.GetId()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("proposal %s is not on tilasm %s", id, tilasm.GetCode()))
		}
		fillProposal(r, proposal)
		res.Anchor, res.Proposals = proposal.GetAnchor(), []*planv1.TilasmProposal{proposal}
		threadID = cmp.Or(threadID, proposal.GetThreadId())
	}
	if threadID != "" {
		thread, err := store.Get[*planv1.TilasmThread](ctx, t.Store, threadID)
		if err != nil {
			return nil, Status(notFound(err, "discussion", threadID))
		}
		if !strings.EqualFold(thread.GetTilasmId(), tilasm.GetId()) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("discussion %s is not on tilasm %s", threadID, tilasm.GetCode()))
		}
		fillThread(r, thread)
		res.Thread = thread
		if res.Anchor == nil {
			res.Anchor = thread.GetAnchor()
			_, proposals, err := discussionsOf(ctx, t.Store, tilasm.GetId())
			if err != nil {
				return nil, Status(err)
			}
			for _, proposal := range proposals {
				if strings.EqualFold(proposal.GetThreadId(), threadID) {
					fillProposal(r, proposal)
					res.Proposals = append(res.Proposals, proposal)
				}
			}
		}
	}
	p := anchorPlace{}
	switch {
	case res.Anchor != nil:
		p = r.resolve(res.Anchor, true)
	case strings.TrimSpace(m.GetQuote()+m.GetPrefix()+m.GetSuffix()) != "":
		if res.Anchor, p, err = takeAnchor(r, tilasm, m.GetQuote(), m.GetPrefix(), m.GetSuffix()); err != nil {
			return nil, err
		}
	}
	res.AnchorState, res.Original, res.Current, res.Paragraphs = p.state, r.original(p), r.current(p), r.paragraphs(p)
	res.Markdown, res.WindowStart, res.Truncated = window(r.source, p, contextBytes)
	return connect.NewResponse(res), nil
}

// window is the document whole when it holds within limit bytes; else limit bytes of it around the place, or from
// its start, cut at lines.
func window(src []byte, p anchorPlace, limit int) (string, int64, bool) {
	if len(src) <= limit {
		return string(src), 0, false
	}
	start, end := 0, limit
	if p.resolvable() {
		mid := (p.src + p.srcEnd) / 2
		start = max(0, min(mid-limit/2, len(src)-limit))
		end = start + limit
	}
	if start > 0 {
		if i := bytes.IndexByte(src[start:end], '\n'); i >= 0 {
			start += i + 1
		}
	}
	if end < len(src) {
		if i := bytes.LastIndexByte(src[start:end], '\n'); i >= 0 {
			end = start + i + 1
		}
	}
	return string(src[start:end]), int64(start), true
}

// forgetDiscussions deletes the discussions and proposals of a wish's tilasms.
func forgetDiscussions(ctx context.Context, tx *store.Tx, wishID string) error {
	where := store.Where{"wish_id": wishID}
	threads, err := store.List[*planv1.TilasmThread](ctx, tx, where)
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if err := tx.Delete(thread); err != nil {
			return err
		}
	}
	proposals, err := store.List[*planv1.TilasmProposal](ctx, tx, where)
	if err != nil {
		return err
	}
	for _, proposal := range proposals {
		if err := tx.Delete(proposal); err != nil {
			return err
		}
	}
	return nil
}

// checkDiscussions refuses discussions and proposals of an exported tilasm that do not hold together: without an
// identifier, of another tilasm or wish, or a proposal on a discussion the tilasm does not hold.
func checkDiscussions(te *planv1.TilasmExport, valid func(string, proto.Message, string) error) error {
	t := te.GetTilasm()
	ours := func(what, id, wishID, tilasmID string) error {
		if id == "" || !strings.EqualFold(wishID, t.GetWishId()) || !strings.EqualFold(tilasmID, t.GetId()) {
			return fmt.Errorf("tilasm %s: %s %q needs an id, and to be of the tilasm and its wish", t.GetCode(), what, id)
		}
		return nil
	}
	threads := map[string]bool{}
	for _, thread := range te.GetThreads() {
		if err := errors.Join(valid("discussion", thread, thread.GetId()),
			ours("discussion", thread.GetId(), thread.GetWishId(), thread.GetTilasmId())); err != nil {
			return err
		}
		threads[strings.ToLower(thread.GetId())] = true
	}
	for _, proposal := range te.GetProposals() {
		if err := errors.Join(valid("proposal", proposal, proposal.GetId()),
			ours("proposal", proposal.GetId(), proposal.GetWishId(), proposal.GetTilasmId())); err != nil {
			return err
		}
		if id := proposal.GetThreadId(); id != "" && !threads[strings.ToLower(id)] {
			return fmt.Errorf("tilasm %s: proposal %s answers discussion %s, which the export does not hold",
				t.GetCode(), proposal.GetId(), id)
		}
	}
	return nil
}

// importDiscussions puts the discussions and proposals of an imported tilasm's manifest on tilasm, in its
// transaction. One this tilasm holds already stays as it is; one whose identifier another tilasm holds comes under a
// new one.
func importDiscussions(ctx context.Context, tx *store.Tx, tilasm *planv1.Tilasm, exp *planv1.TilasmExport) error {
	ids := map[string]string{} // a discussion's identifier in the file, its identifier here
	fresh := func(id string, get func() (string, error)) (string, bool, error) {
		tilasmID, err := get()
		switch {
		case errors.Is(err, store.ErrNotFound) && uuidLike(id):
			return id, true, nil
		case errors.Is(err, store.ErrNotFound):
			return store.NewID(), true, nil
		case err != nil:
			return "", false, err
		case strings.EqualFold(tilasmID, tilasm.GetId()):
			return id, false, nil
		}
		return store.NewID(), true, nil
	}
	for _, thread := range exp.GetThreads() {
		thread = proto.CloneOf(thread)
		id, add, err := fresh(thread.GetId(), func() (string, error) {
			got, err := store.Get[*planv1.TilasmThread](ctx, tx, thread.GetId())
			return got.GetTilasmId(), err
		})
		if err != nil {
			return err
		}
		ids[strings.ToLower(thread.GetId())] = id
		if !add {
			continue
		}
		thread.Id, thread.WishId, thread.TilasmId = id, tilasm.GetWishId(), tilasm.GetId()
		if err := tx.Put(thread); err != nil {
			return err
		}
	}
	for _, proposal := range exp.GetProposals() {
		proposal = proto.CloneOf(proposal)
		id, add, err := fresh(proposal.GetId(), func() (string, error) {
			got, err := store.Get[*planv1.TilasmProposal](ctx, tx, proposal.GetId())
			return got.GetTilasmId(), err
		})
		if err != nil || !add {
			return err
		}
		proposal.Id, proposal.WishId, proposal.TilasmId = id, tilasm.GetWishId(), tilasm.GetId()
		if proposal.GetThreadId() != "" {
			proposal.ThreadId = ids[strings.ToLower(proposal.GetThreadId())]
		}
		if err := tx.Put(proposal); err != nil {
			return err
		}
	}
	return nil
}
