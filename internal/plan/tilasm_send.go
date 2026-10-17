package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The send of a tilasm's comments (L08, step 6, decision Q29): every draft at once, with a comment on the whole tilasm
// written above the buttons, and the developer's choice, launch or iterate. One lead event per send; a launch without
// a comment answers the question that links the tilasm instead, as its approval does. A tilasm held takes no send.

func (t *Tilasms) SendComments(
	ctx context.Context, req *connect.Request[planv1.TilasmServiceSendCommentsRequest],
) (*connect.Response[planv1.TilasmServiceSendCommentsResponse], error) {
	m := req.Msg
	res := &planv1.TilasmServiceSendCommentsResponse{}
	var answered *planv1.Question
	var then func(context.Context)
	queued := false
	err := write(ctx, t.Store, req.Spec(), m, func(tx *store.Tx) error {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, tx, m.GetTilasmId())
		if err != nil {
			return err
		}
		holds, err := tilasmHolds(ctx, tx, tilasm.GetWishId(), []*planv1.Tilasm{tilasm})
		if err != nil {
			return err
		}
		if h := holds[tilasm.GetId()]; h != nil {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New(HoldText(tilasm, h)))
		}
		all, err := store.List[*planv1.TilasmComment](ctx, tx, store.Where{"tilasm_id": tilasm.GetId()})
		if err != nil {
			return err
		}
		now := timestamppb.Now()
		var drafts []*planv1.TilasmComment
		for _, c := range all {
			if c.GetState() == planv1.CommentState_COMMENT_STATE_DRAFT {
				drafts = append(drafts, c)
			}
		}
		slices.SortFunc(drafts, func(a, b *planv1.TilasmComment) int { return strings.Compare(a.GetId(), b.GetId()) })
		if text := strings.TrimSpace(m.GetText()); text != "" {
			whole := &planv1.TilasmComment{
				Id: store.NewID(), TilasmId: tilasm.GetId(), WishId: tilasm.GetWishId(), Text: text,
				Version: latestVersion(tilasm), State: planv1.CommentState_COMMENT_STATE_DRAFT, CreateTime: now, UpdateTime: now,
			}
			if whole.Code, err = nextCommentCode(ctx, tx, tilasm); err != nil {
				return err
			}
			drafts = append(drafts, whole)
		}
		if len(drafts) == 0 && m.GetChoice() == planv1.SendChoice_SEND_CHOICE_ITERATE {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"%s has no comment to iterate on: say what to change", tilasm.GetCode()))
		}
		question, err := linkingQuestion(ctx, tx, tilasm)
		if err != nil {
			return err
		}
		send := &planv1.TilasmSend{
			Id: store.NewID(), TilasmId: tilasm.GetId(), WishId: tilasm.GetWishId(), Choice: m.GetChoice(),
			QuestionId: question.GetId(), CreateTime: now,
		}
		if send.Number, err = nextSend(ctx, tx, tilasm, all); err != nil {
			return err
		}
		for _, c := range drafts {
			c.State, c.Send, c.SendTime = planv1.CommentState_COMMENT_STATE_SENT, send.GetNumber(), now
			if err := tx.Put(c); err != nil {
				return err
			}
		}
		if err := tx.Put(send); err != nil {
			return err
		}
		res.Send, res.Comments = send, drafts
		if len(drafts) == 0 && question != nil {
			// No comment: launch is the developer's yes to the question, as a click on its recommendation.
			if t.Marks == nil {
				return connect.NewError(connect.CodeUnimplemented, errors.New("this server answers no question"))
			}
			mark := &planv1.Mark{Kind: planv1.MarkKind_MARK_KIND_APPROVED, Actor: CallerActor(ctx), CreateTime: now}
			if then, err = t.Marks.approve(ctx, tx, question, mark); err != nil {
				return err
			}
			question.Marks = setMark(question.GetMarks(), mark, false)
			answered, res.Answered = question, question.GetCode()
			return tx.Put(question)
		}
		queued = true
		return tx.Put(&planv1.LeadEvent{
			Id: store.NewID(), CreateTime: now, Kind: planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS,
			WishId: tilasm.GetWishId(), Code: tilasm.GetCode(), Text: SendLine(tilasm, send, drafts, question),
		})
	})
	if err != nil {
		return nil, err
	}
	if answered != nil {
		t.Marks.approved(ctx, answered, then)
	}
	if queued && t.Wake != nil {
		t.Wake()
	}
	return connect.NewResponse(res), nil
}

// nextSend is the number of a tilasm's next send: past those it made, and those its comments carry.
func nextSend(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm, comments []*planv1.TilasmComment) (int32, error) {
	sends, err := store.List[*planv1.TilasmSend](ctx, r, store.Where{"tilasm_id": tilasm.GetId()})
	if err != nil {
		return 0, err
	}
	last := int32(0)
	for _, s := range sends {
		last = max(last, s.GetNumber())
	}
	for _, c := range comments {
		last = max(last, c.GetSend())
	}
	return last + 1, nil
}

// SendLine is what a send tells the lead, in a line a digest keeps whole: the version read, how many comments on
// what, the developer's choice, and the command that reads them. question is the open question that links the
// tilasm, or nil.
func SendLine(tilasm *planv1.Tilasm, send *planv1.TilasmSend, comments []*planv1.TilasmComment, question *planv1.Question) string {
	code := tilasm.GetCode()
	head := fmt.Sprintf("Djinn: %s v%d, ", code, latestVersion(tilasm))
	if len(comments) == 0 {
		return head + "launch its work: no comment, and no open question links it (djinn tilasm get " + code + ")."
	}
	var choice string
	switch {
	case send.GetChoice() == planv1.SendChoice_SEND_CHOICE_ITERATE:
		choice = "iterate: its next version with them"
	case question != nil:
		choice = "launch " + question.GetCode() + " with them"
	default:
		choice = "launch its work with them, no open question links it"
	}
	read := fmt.Sprintf(": djinn tilasm comments %s --send %d", code, send.GetNumber())
	count := fmt.Sprintf("%d comments", len(comments))
	if len(comments) == 1 {
		count = "1 comment"
	}
	line := head + count + " (" + commentKinds(comments) + "), " + choice + read
	if utf8.RuneCountInString(strings.TrimPrefix(line, "Djinn: "+code+" ")) > digestLineMax {
		line = head + count + ", " + choice + read // What they are on waits in the command.
	}
	return line
}

// commentKinds says what comments are on: 1 general, 3 on passages, 1 on an element.
func commentKinds(comments []*planv1.TilasmComment) string {
	var whole, passages, elements int
	for _, c := range comments {
		a := c.GetAnchor()
		switch {
		case a.GetExact() != "":
			passages++
		case a.GetElementId() != "" || a.GetPath() != "" || a.GetFingerprint() != "":
			elements++
		default:
			whole++
		}
	}
	var parts []string
	if whole > 0 {
		parts = append(parts, fmt.Sprintf("%d general", whole))
	}
	if passages == 1 {
		parts = append(parts, "1 on a passage")
	} else if passages > 1 {
		parts = append(parts, fmt.Sprintf("%d on passages", passages))
	}
	if elements == 1 {
		parts = append(parts, "1 on an element")
	} else if elements > 1 {
		parts = append(parts, fmt.Sprintf("%d on elements", elements))
	}
	return strings.Join(parts, ", ")
}

// linkingQuestion is the open question of the tilasm's wish that links it, the latest asked; nil when none does.
func linkingQuestion(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm) (*planv1.Question, error) {
	questions, err := store.List[*planv1.Question](ctx, r, store.Where{"wish_id": tilasm.GetWishId()})
	if err != nil {
		return nil, err
	}
	var found *planv1.Question
	for _, q := range questions {
		if openQuestion(q) && linksTilasm(q, tilasm) && (found == nil || q.GetId() > found.GetId()) {
			found = q
		}
	}
	return found, nil
}

// openQuestion tells a question that waits for an answer: neither answered nor withdrawn.
func openQuestion(q *planv1.Question) bool { return q.GetAnswer() == nil && q.GetWithdrawal() == nil }

// questionLinksTilasm tells whether q links any tilasm.
func questionLinksTilasm(q *planv1.Question) bool {
	for _, s := range append([]string{q.GetText(), q.GetContext(), q.GetRecommendation()}, q.GetOptions()...) {
		if strings.Contains(strings.ToLower(s), "djinn://tilasm/") {
			return true
		}
	}
	return false
}

// linksTilasm tells whether q links the tilasm, by its djinn:// link in its text, context, options or recommendation.
func linksTilasm(q *planv1.Question, tilasm *planv1.Tilasm) bool {
	link := TilasmLink(tilasm.GetId())
	for _, s := range append([]string{q.GetText(), q.GetContext(), q.GetRecommendation()}, q.GetOptions()...) {
		if strings.Contains(strings.ToLower(s), link) {
			return true
		}
	}
	return false
}

// holdingStatus are the statuses of a task that runs or is planned: it holds the tilasms it works on.
var holdingStatus = []planv1.TaskStatus{
	planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING,
	planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
}

// tilasmTask says how a task's prompt names a tilasm, by its put (codeFlag) and its link: REWORK when the lead's words
// put its next version (--code L01), WORK when it is given it (--tilasm L01, its link), UNSPECIFIED otherwise. The text
// of the tilasms given, which may quote other codes, is not looked at, only their links.
func tilasmTask(task *planv1.Task, codeFlag *regexp.Regexp, link string) planv1.TilasmHoldReason {
	words, given, _ := strings.Cut(task.GetPrompt(), "\n"+tilasmContextHead)
	switch {
	case codeFlag.MatchString(words):
		return planv1.TilasmHoldReason_TILASM_HOLD_REASON_REWORK
	case strings.Contains(strings.ToLower(words), link) || strings.Contains(strings.ToLower(given), "- link: "+link+"\n"):
		return planv1.TilasmHoldReason_TILASM_HOLD_REASON_WORK
	}
	return planv1.TilasmHoldReason_TILASM_HOLD_REASON_UNSPECIFIED
}

// tilasmHolds are why the tilasms of the wish wishID take no send for now, by identifier; a tilasm free is not in it.
// In this order: a task that puts its next version, or one given it, runs or is planned; its work was launched, by a
// send or by answering the question that links it; a send waits for the Djinn to read it.
func tilasmHolds(ctx context.Context, r store.Reader, wishID string, tilasms []*planv1.Tilasm) (map[string]*planv1.TilasmHold, error) {
	out := map[string]*planv1.TilasmHold{}
	if len(tilasms) == 0 {
		return out, nil
	}
	where := store.Where{"wish_id": wishID}
	tasks, err := store.List[*planv1.Task](ctx, r, where)
	if err != nil {
		return nil, err
	}
	tasks = slices.DeleteFunc(tasks, func(task *planv1.Task) bool {
		kind := task.GetKind()
		return !slices.Contains(holdingStatus, task.GetStatus()) ||
			(kind != planv1.TaskKind_TASK_KIND_WORK && kind != planv1.TaskKind_TASK_KIND_UNSPECIFIED)
	})
	slices.SortFunc(tasks, func(a, b *planv1.Task) int { return CompareCodes(a.GetCode(), b.GetCode()) })
	questions, err := store.List[*planv1.Question](ctx, r, where)
	if err != nil {
		return nil, err
	}
	sends, err := store.List[*planv1.TilasmSend](ctx, r, where)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(sends, func(a, b *planv1.TilasmSend) int { return cmp.Compare(a.GetNumber(), b.GetNumber()) })
	waiting, err := waitingEvents(ctx, r)
	if err != nil {
		return nil, err
	}
	for _, tilasm := range tilasms {
		if h := tilasmHold(tilasm, tasks, questions, sends, waiting); h != nil {
			h.TilasmId = tilasm.GetId()
			out[tilasm.GetId()] = h
		}
	}
	return out, nil
}

// tilasmHold is why tilasm takes no send, from its wish's tasks that run or are planned, its questions, its sends by
// number and the lead's events waiting; nil when it takes one.
func tilasmHold(
	tilasm *planv1.Tilasm, tasks []*planv1.Task, questions []*planv1.Question, sends []*planv1.TilasmSend,
	waiting []*planv1.LeadEvent,
) *planv1.TilasmHold {
	codeFlag := regexp.MustCompile(`(?i)--code[ =]` + regexp.QuoteMeta(tilasm.GetCode()) + `\b`)
	link := TilasmLink(tilasm.GetId())
	var work *planv1.TilasmHold
	for _, task := range tasks {
		switch tilasmTask(task, codeFlag, link) {
		case planv1.TilasmHoldReason_TILASM_HOLD_REASON_REWORK:
			return &planv1.TilasmHold{Reason: planv1.TilasmHoldReason_TILASM_HOLD_REASON_REWORK, Code: task.GetCode()}
		case planv1.TilasmHoldReason_TILASM_HOLD_REASON_WORK:
			if work == nil {
				work = &planv1.TilasmHold{Reason: planv1.TilasmHoldReason_TILASM_HOLD_REASON_WORK, Code: task.GetCode()}
			}
		}
	}
	if work != nil {
		return work
	}
	codes := map[string]string{}
	var open bool
	var answered *planv1.Question
	for _, q := range questions {
		codes[q.GetId()] = q.GetCode()
		if !linksTilasm(q, tilasm) {
			continue
		}
		if openQuestion(q) {
			open = true
		} else if q.GetAnswer() != nil && (answered == nil || q.GetId() > answered.GetId()) {
			answered = q
		}
	}
	last := int32(0)
	for _, s := range sends {
		if s.GetTilasmId() != tilasm.GetId() {
			continue
		}
		last = s.GetNumber()
		if s.GetChoice() == planv1.SendChoice_SEND_CHOICE_LAUNCH {
			return &planv1.TilasmHold{Reason: planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED,
				Code: codes[s.GetQuestionId()], Send: s.GetNumber()}
		}
	}
	if answered != nil && !open {
		return &planv1.TilasmHold{Reason: planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED, Code: answered.GetCode()}
	}
	for _, e := range waiting {
		if e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS && e.GetWishId() == tilasm.GetWishId() &&
			e.GetCode() == tilasm.GetCode() {
			return &planv1.TilasmHold{Reason: planv1.TilasmHoldReason_TILASM_HOLD_REASON_SENT, Send: last}
		}
	}
	return nil
}

// sendOfLine finds the send a comments event names, at the end of its line (SendLine).
var sendOfLine = regexp.MustCompile(`--send ([0-9]+)$`)

// sendReads are the sends on the wish wishID's tilasms that the Djinn read, by tilasm then send: the lead
// acknowledged the comments event that names it, or Djinn no longer keeps that event, a week after it was read. A send
// whose event waits is not in it. A send without a comment queued no such event: it has no card to tell.
func sendReads(ctx context.Context, r store.Reader, wishID string, tilasms []*planv1.Tilasm) ([]*planv1.TilasmSendRead, error) {
	where := store.Where{"wish_id": wishID}
	sends, err := store.List[*planv1.TilasmSend](ctx, r, where)
	if err != nil {
		return nil, err
	}
	events, err := store.List[*planv1.LeadEvent](ctx, r, where)
	if err != nil {
		return nil, err
	}
	type sent struct {
		code string
		send int32
	}
	byLine := map[sent]*planv1.LeadEvent{}
	for _, e := range events {
		m := sendOfLine.FindStringSubmatch(e.GetText())
		if e.GetKind() != planv1.LeadEventKind_LEAD_EVENT_KIND_COMMENTS || m == nil {
			continue
		}
		n, err := strconv.ParseInt(m[1], 10, 32)
		if err != nil {
			continue
		}
		byLine[sent{strings.ToUpper(e.GetCode()), int32(n)}] = e
	}
	codes := map[string]string{}
	for _, tilasm := range tilasms {
		codes[tilasm.GetId()] = strings.ToUpper(tilasm.GetCode())
	}
	var out []*planv1.TilasmSendRead
	for _, s := range sends {
		code, ok := codes[s.GetTilasmId()]
		if !ok {
			continue
		}
		e, kept := byLine[sent{code, s.GetNumber()}]
		if kept && e.GetAckSessionId() == "" {
			continue
		}
		read := &planv1.TilasmSendRead{TilasmId: s.GetTilasmId(), Send: s.GetNumber()}
		if kept {
			read.ReadTime = e.GetAckTime()
		}
		out = append(out, read)
	}
	slices.SortFunc(out, func(a, b *planv1.TilasmSendRead) int {
		return cmp.Or(strings.Compare(a.GetTilasmId(), b.GetTilasmId()), cmp.Compare(a.GetSend(), b.GetSend()))
	})
	return out, nil
}

// HoldText says why a tilasm takes no send, in a sentence: what a refused send answers.
func HoldText(tilasm *planv1.Tilasm, h *planv1.TilasmHold) string {
	code := tilasm.GetCode()
	switch h.GetReason() {
	case planv1.TilasmHoldReason_TILASM_HOLD_REASON_REWORK:
		return fmt.Sprintf("%s puts the next version of %s: comment on that one", h.GetCode(), code)
	case planv1.TilasmHoldReason_TILASM_HOLD_REASON_WORK:
		return fmt.Sprintf("%s was launched: %s works on it", code, h.GetCode())
	case planv1.TilasmHoldReason_TILASM_HOLD_REASON_LAUNCHED:
		if h.GetSend() > 0 {
			return fmt.Sprintf("%s was launched already, by send %d", code, h.GetSend())
		}
		return fmt.Sprintf("%s was launched already: %s is answered", code, h.GetCode())
	case planv1.TilasmHoldReason_TILASM_HOLD_REASON_SENT:
		return fmt.Sprintf("send %d of %s waits for the Djinn to read it", h.GetSend(), code)
	}
	return fmt.Sprintf("%s takes no send for now", code)
}
