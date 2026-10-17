package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/dispatch"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

// Iterative mockups (W268, decision Q53 A, tilasm L18): answering the option of a question whose plan is a mockup, or
// sending comments to iterate on the tilasm it iterates on, records an iteration round (plan.Iterates); Djinn starts
// its mockup task (TASK_KIND_MOCKUP, Task.question set), which puts the tilasm's next version with the developer's
// note and the comments, then revises the question. At its end the lead hears what it did, as of an investigator: the
// question revised, or still waiting for its revision. One mockup task per question at a time: a second iteration
// while one works is sent to it.

// methodIterate is what the journal records when Djinn starts a mockup task on an iteration round; the request is the
// spawn.
const methodIterate = "harness/iterate"

// Iterated starts the mockup task of round, the iteration round just stored on q, and returns its code; "" when none
// starts: the plan services then tell the lead (plan.Wishes.Iterated). A start that hangs leaves q to the lead.
func (h *Harness) Iterated(ctx context.Context, q *planv1.Question, round *planv1.Round) string {
	t, err := h.startMockup(ctx, q, round)
	if err != nil {
		logx.L().Error("start a mockup task", "loop", "question", "question", q.GetCode(), "err", err)
	}
	return t.GetCode()
}

// startMockup starts the mockup task of round on q, or gives round to the one at work on q already, and returns it; nil
// when none starts. It waits for the start h.enlightenWait at most, as startInvestigator does, then gives up; the start
// goes on in the background, and Close waits for it.
func (h *Harness) startMockup(ctx context.Context, q *planv1.Question, round *planv1.Round) (*planv1.Task, error) {
	type started struct {
		task *planv1.Task
		err  error
	}
	done := make(chan started, 1)
	h.wg.Add(1)
	go func() {
		defer logx.Crash("question", "question", q.GetCode())
		defer h.wg.Done()
		h.questioning.Lock()
		defer h.questioning.Unlock()
		t, err := h.spawnMockup(ctx, q, round)
		done <- started{t, err}
	}()
	wait := cmp.Or(h.enlightenWait, defaultEnlightenWait)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case s := <-done:
		return s.task, s.err
	case <-timer.C:
		return nil, fmt.Errorf("%w (%s)", errStartHangs, wait)
	}
}

// spawnMockup is startMockup, done now. The caller holds h.questioning.
func (h *Harness) spawnMockup(ctx context.Context, q *planv1.Question, round *planv1.Round) (*planv1.Task, error) {
	wish, err := store.Get[*planv1.Wish](ctx, h.store, q.GetWishId())
	if err != nil || wish.GetState() == planv1.WishState_WISH_STATE_GRANTED {
		return nil, err
	}
	p := planFor(q, round.GetOption())
	if p == nil {
		return nil, fmt.Errorf("%s has no plan for %s", q.GetCode(), strings.TrimPrefix(round.GetOption().String(), "CHOICE_"))
	}
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": wish.GetId()})
	if err != nil {
		return nil, err
	}
	var tilasm *planv1.Tilasm
	var comments []*planv1.TilasmComment
	if id := round.GetTilasmId(); id != "" {
		if tilasm, err = store.Get[*planv1.Tilasm](ctx, h.store, id); err != nil {
			return nil, err
		}
		if comments, err = sentComments(ctx, h.store, tilasm, round.GetVersion()); err != nil {
			return nil, err
		}
	}
	if t := mockupAtWork(tasks, q.GetCode()); t != nil {
		h.tellMockupAgain(ctx, t, q, round)
		h.tookOn(ctx, q, round, t)
		return t, nil
	}
	req := &planv1.TaskServiceSpawnRequest{
		WishId: wish.GetId(), ProjectId: p.GetProjectId(), Title: mockupTitle(q, p), Provider: p.GetProvider(),
		Model: p.GetModel(), PartOf: p.GetPartOf(), Kind: planv1.TaskKind_TASK_KIND_MOCKUP, Question: q.GetCode(),
		Prompt: mockupPrompt(q, p, round, tilasm, comments, tasks...),
	}
	if tilasm != nil {
		req.Tilasm = []string{fmt.Sprintf("%s@%d", tilasm.GetId(), round.GetVersion())}
	}
	t, err := h.spawn(ctx, methodIterate, req, &planv1.Task{Question: q.GetCode()})
	if err != nil {
		return nil, err
	}
	h.tookOn(ctx, q, round, t)
	return t, nil
}

// tookOn records in round, the iteration round of q, that the mockup task t took it on.
func (h *Harness) tookOn(ctx context.Context, q *planv1.Question, round *planv1.Round, t *planv1.Task) {
	err := h.store.Tx(ctx, func(tx *store.Tx) error {
		cur, err := store.Get[*planv1.Question](ctx, tx, q.GetId())
		if err != nil {
			return err
		}
		i := slices.IndexFunc(cur.GetRounds(), func(r *planv1.Round) bool {
			return r.GetKind() == planv1.RoundKind_ROUND_KIND_ITERATE && r.GetTaskId() == "" &&
				r.GetCreateTime().AsTime().Equal(round.GetCreateTime().AsTime())
		})
		if i < 0 {
			return nil
		}
		cur.Rounds[i].TaskId = t.GetId()
		if err := tx.Journal(actorHarness, methodTookOn, cur); err != nil {
			return err
		}
		return tx.Put(cur)
	})
	if err != nil {
		logx.L().Error("record a question's mockup task", "question", q.GetCode(), "task", t.GetCode(), "err", err)
	}
}

// methodTookOn is what the journal records when Djinn names the mockup task in its iteration round; the request is
// the question.
const methodTookOn = "harness/took-on"

// planFor is q's plan for the option choice; nil when it has none.
func planFor(q *planv1.Question, choice planv1.Choice) *planv1.PlannedTask {
	for _, p := range q.GetPlans() {
		if p.GetOption() == choice {
			return p
		}
	}
	return nil
}

// mockupTitle is the title of the mockup task of q's plan p: its own, with the version it makes.
func mockupTitle(q *planv1.Question, p *planv1.PlannedTask) string {
	return fmt.Sprintf("%s (%s, version %d of %d)", p.GetTitle(), q.GetCode(), plan.Tour(q), plan.MockupRounds(p))
}

// mockupLive are the statuses of a mockup task still at work, or to be.
var mockupLive = []planv1.TaskStatus{
	planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING,
	planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING, planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
}

// mockupAtWork is the mockup task among tasks that iterates on the question code and is at work, or planned; nil when
// none is.
func mockupAtWork(tasks []*planv1.Task, code string) *planv1.Task {
	for _, t := range tasks {
		if dispatch.Mockup(t) && t.GetQuestion() == code && slices.Contains(mockupLive, t.GetStatus()) {
			return t
		}
	}
	return nil
}

// tellMockupAgain gives the mockup task t, at work on q, the new iteration round: its note, or the send of comments
// to read.
func (h *Harness) tellMockupAgain(ctx context.Context, t *planv1.Task, q *planv1.Question, round *planv1.Round) {
	text := fmt.Sprintf("The developer iterates on %s again.", q.GetCode())
	if n := strings.TrimSpace(round.GetNote()); n != "" {
		text += " Their note: " + n
	}
	if s := round.GetSend(); s > 0 {
		text += fmt.Sprintf(" They sent comments (send %d): read them with djinn tilasm comments %s --send %d.", s,
			round.GetTilasmId(), s)
	}
	_, err := h.Send(ctx, planv1connect.TaskServiceSendProcedure, &planv1.TaskServiceSendRequest{TaskId: t.GetId(), Text: text})
	if err != nil {
		h.Note(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "not told: " + text + " (" + err.Error() + ")"})
	}
}

// sentComments are the comments the developer sent on version of tilasm, by code: the ones the iteration reads.
func sentComments(ctx context.Context, r store.Reader, tilasm *planv1.Tilasm, version int32) ([]*planv1.TilasmComment, error) {
	all, err := store.List[*planv1.TilasmComment](ctx, r, store.Where{"tilasm_id": tilasm.GetId()})
	if err != nil {
		return nil, err
	}
	all = slices.DeleteFunc(all, func(c *planv1.TilasmComment) bool {
		return c.GetState() == planv1.CommentState_COMMENT_STATE_DRAFT || c.GetVersion() != version
	})
	slices.SortFunc(all, func(a, b *planv1.TilasmComment) int { return plan.CompareCodes(a.GetCode(), b.GetCode()) })
	return all, nil
}

// mockupPrompt is the prompt of the mockup task of round, an iteration on q with its plan p: the plan, the note, the
// comments sent on the tilasm (nil when the plan names none), the question as a worker reads it, and what to do.
func mockupPrompt(
	q *planv1.Question, p *planv1.PlannedTask, round *planv1.Round, tilasm *planv1.Tilasm, comments []*planv1.TilasmComment,
	tasks ...*planv1.Task,
) string {
	code, wish := q.GetCode(), q.GetWishId()
	name, put := "a new tilasm", fmt.Sprintf("`djinn tilasm put <folder> --wish %s`", wish)
	if tilasm != nil {
		name = fmt.Sprintf("%s v%d", tilasm.GetCode(), round.GetVersion())
		put = fmt.Sprintf("`djinn tilasm put <folder> --code %s --wish %s`", tilasm.GetCode(), wish)
	}
	letter := strings.TrimPrefix(round.GetOption().String(), "CHOICE_")
	option := ""
	if i := int(round.GetOption() - planv1.Choice_CHOICE_A); i >= 0 && i < len(q.GetOptions()) {
		option = q.GetOptions()[i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are the mockup task of question %s in wish %s. The developer chose %s (%q), whose plan "+
		"iterates on the mockup: you make version %d of %d, from %s. Your one job: put the mockup's next version with "+
		"their note and the comments they sent, then revise %s so that it waits for the developer again.\n\n",
		code, wish, letter, option, plan.Tour(q), plan.MockupRounds(p), name, code)
	if prompt := strings.TrimSpace(p.GetPrompt()); prompt != "" {
		b.WriteString("## The plan\n\n" + prompt + "\n\n")
	}
	b.WriteString("## The developer's note\n\n")
	if n := strings.TrimSpace(round.GetNote()); n != "" {
		b.WriteString(n + "\n\n")
	} else {
		b.WriteString("None: the comments say what to change.\n\n")
	}
	if tilasm != nil {
		fmt.Fprintf(&b, "## The comments sent on %s\n\n", name)
		for _, c := range comments {
			on := "the whole tilasm"
			if a := c.GetAnchor(); a.GetExact() != "" {
				on = fmt.Sprintf("%q", a.GetExact())
			} else if a.GetElementId() != "" || a.GetPath() != "" {
				on = "an element"
			}
			fmt.Fprintf(&b, "- %s, on %s: %s\n", c.GetCode(), on, strings.Join(strings.Fields(c.GetText()), " "))
		}
		if len(comments) == 0 {
			b.WriteString("None sent on this version.\n")
		}
		fmt.Fprintf(&b, "\nRead each with what it is on: `djinn tilasm comments %s --wish %s`.\n\n", tilasm.GetCode(), wish)
	}
	questionText(&b, q, tasks...)
	revise := fmt.Sprintf("`djinn question revise %s --wish-id %s --context-file <file>`", code, wish)
	b.WriteString("## What to do\n\n")
	fmt.Fprintf(&b, "1. Read the mockup's files (below) and every comment; keep what the note and the comments do not "+
		"touch.\n"+
		"2. Copy its files into a folder of your worktree, outside the project's code, and change them there: Djinn "+
		"never merges your commits.\n"+
		"3. Put the folder as the mockup's next version: %s; its link stays the same.\n", put)
	if tilasm != nil {
		fmt.Fprintf(&b, "4. Reply to each comment sent: `djinn tilasm reply %s <comment> --text \"…\"`: what you "+
			"changed, or why not.\n", tilasm.GetCode())
	} else {
		b.WriteString("4. Link the new tilasm from the question's context (`djinn://tilasm/<id>`).\n")
	}
	fmt.Fprintf(&b, "5. Revise the question: write its context in a file of your worktree, then %s: the context says "+
		"what this version changes and links the mockup, in the shape below. Keep its options and its plans (no "+
		"`--options`, no `--plan-file`) unless the note asks to change them: the developer reviews the next version "+
		"from the same options.\n"+
		"6. End with one line: what the new version changes.\n\n", revise)
	b.WriteString("## The shape of the context\n\n" + plan.QuestionShape + "\n")
	return b.String()
}

// iterates tells whether t is a mockup task that iterates on a question: its end is followed up as an
// investigator's (mockupEnded).
func iterates(t *planv1.Task) bool { return dispatch.Mockup(t) && t.GetQuestion() != "" }

// mockupEnded tells the lead of t's wish what the mockup task t did, now that it ended: the version it put, and the
// question it revised, or that it still waits for its revision.
func (h *Harness) mockupEnded(t *planv1.Task) {
	h.mu.Lock()
	told := h.tell != nil || h.notifyLead != nil
	h.mu.Unlock()
	if !told {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	line, err := h.mockupEndLine(ctx, t)
	if err == nil {
		err = h.toLeadNote(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENDED, t.GetWishId(), t.GetCode(), line,
			h.finalNote(ctx, t.GetId()))
	}
	if err != nil && !errors.Is(err, plan.ErrNoLead) {
		h.Note(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_LOG, Text: "the lead was not told: " + err.Error()})
	}
}

// mockupEndLine is the line that tells a lead what the mockup task t did.
func (h *Harness) mockupEndLine(ctx context.Context, t *planv1.Task) (string, error) {
	tasks, questions, err := h.wishWork(ctx, t.GetWishId())
	if err != nil {
		return "", err
	}
	tilasms, err := store.List[*planv1.Tilasm](ctx, h.store, store.Where{"wish_id": t.GetWishId()})
	if err != nil {
		return "", err
	}
	return mockupLine(t, tasks, questions, tilasms), nil
}

// mockupLine is the line that tells a lead what the mockup task t did, from its wish's tasks, questions and tilasms:
// the versions it put, the question it revised, or what is left.
func mockupLine(t *planv1.Task, tasks []*planv1.Task, questions []*planv1.Question, tilasms []*planv1.Tilasm) string {
	code := t.GetQuestion()
	_, _, revised := questionOutcome(t, tasks, questions)
	var put []string
	for _, l := range tilasms {
		for _, v := range l.GetVersions() {
			if v.GetAuthor() != "" && (strings.EqualFold(v.GetAuthor(), t.GetCode()) || v.GetAuthor() == t.GetId()) {
				put = append(put, fmt.Sprintf("%s v%d", l.GetCode(), v.GetNumber()))
			}
		}
	}
	ended := "ended"
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
		ended = statusText(t)
	}
	line := fmt.Sprintf("Djinn: %s (%s) %s", t.GetCode(), t.GetTitle(), ended)
	var did []string
	if len(put) > 0 {
		did = append(did, "put "+strings.Join(put, ", "))
	}
	if revised {
		did = append(did, "revised "+code+": it waits for the developer again")
	}
	if len(did) > 0 {
		line += ": " + strings.Join(did, "; ")
	}
	line += "."
	switch {
	case !revised:
		line += fmt.Sprintf(" %s still waits for its revision: djinn task get %s has what it did.", code, t.GetId())
	case len(put) == 0:
		line += fmt.Sprintf(" Warning: it put no new version of its mockup, and %s shows the same one again: check it.", code)
	}
	return line
}

// resumeIteration follows up the iteration round asked on q, unless it was: a mockup task took it on since, or the
// lead heard of it. Its mockup task starts now, or else the lead is told, once.
func (h *Harness) resumeIteration(ctx context.Context, q *planv1.Question, asked *planv1.Round) error {
	since := asked.GetCreateTime().AsTime()
	tasks, err := store.List[*planv1.Task](ctx, h.store, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return err
	}
	if slices.ContainsFunc(tasks, func(t *planv1.Task) bool {
		return dispatch.Mockup(t) && t.GetQuestion() == q.GetCode() &&
			(!t.GetCreateTime().AsTime().Before(since) || slices.Contains(mockupLive, t.GetStatus()))
	}) {
		return nil
	}
	events, err := store.List[*planv1.LeadEvent](ctx, h.store, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return err
	}
	if slices.ContainsFunc(events, func(e *planv1.LeadEvent) bool {
		return e.GetKind() == planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN && e.GetCode() == q.GetCode() &&
			!e.GetCreateTime().AsTime().Before(since)
	}) {
		return nil
	}
	t, err := h.startMockup(ctx, q, asked)
	if t != nil {
		return nil
	}
	if err != nil {
		logx.L().Error("start a mockup task", "loop", "question", "question", q.GetCode(), "err", err)
	}
	err = h.toLead(ctx, planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN, q.GetWishId(), q.GetCode(),
		plan.IterateLine(ctx, h.store, q, asked))
	if errors.Is(err, plan.ErrNoLead) {
		return nil
	}
	return err
}

// mockupQuestion checks the question a spawn of a mockup names (--question): an open question of the wish; it returns
// its code.
func mockupQuestion(ctx context.Context, r store.Reader, req *planv1.TaskServiceSpawnRequest) (string, error) {
	ref := strings.ToUpper(strings.TrimSpace(req.GetQuestion()))
	if ref == "" {
		return "", nil
	}
	if req.GetKind() != planv1.TaskKind_TASK_KIND_MOCKUP {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("--question is for a mockup (--kind mockup)"))
	}
	found, err := store.List[*planv1.Question](ctx, r, store.Where{"wish_id": req.GetWishId(), "code": ref})
	if err != nil {
		return "", plan.Status(err)
	}
	if len(found) == 0 {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("the wish has no question %s", ref))
	}
	if q := found[0]; q.GetAnswer() != nil || q.GetWithdrawal() != nil {
		return "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("question %s is decided or withdrawn: a mockup iterates on an open question", ref))
	}
	return ref, nil
}

// pinned is the tilasms p names, each at the latest version created by the time q was answered: the version the
// developer saw when they chose to build it. A name pinned already stays.
func pinned(ctx context.Context, r store.Reader, q *planv1.Question, names []string) []string {
	at := q.GetAnswer().GetCreateTime()
	out := make([]string, 0, len(names))
	for _, name := range names {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, r, name)
		if strings.Contains(name, "@") || err != nil || at == nil {
			out = append(out, name)
			continue
		}
		n := int32(0)
		for _, v := range tilasm.GetVersions() {
			if !v.GetCreateTime().AsTime().After(at.AsTime()) {
				n = v.GetNumber()
			}
		}
		if n == 0 {
			out = append(out, name)
			continue
		}
		out = append(out, fmt.Sprintf("%s@%d", tilasm.GetId(), n))
	}
	return out
}

// approvedVersions says which versions of the tilasms a launched plan pins (pinned), for the lead: "L18 v2 approved".
// "" when it pins none.
func approvedVersions(ctx context.Context, r store.Reader, names []string) string {
	var out []string
	for _, name := range names {
		id, n, ok := strings.Cut(name, "@")
		if !ok {
			continue
		}
		code := id
		if t, err := store.Get[*planv1.Tilasm](ctx, r, id); err == nil {
			code = t.GetCode()
		}
		out = append(out, code+" v"+n)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, ", ") + " approved"
}

// mockupPast says what Djinn did with the answer that chose q's mockup plan p, which iterates no more: q showed its
// last version (plan.MockupRounds), or its answer came after another one. The note goes to the lead with the answer.
func mockupPast(q *planv1.Question, p *planv1.PlannedTask) string {
	return fmt.Sprintf("Its mockup plan iterates no more (version %d of %d shown): nothing launched; read the versions "+
		"with the developer's note and ask again, or %s launches one more", plan.Tour(q), plan.MockupRounds(p),
		plan.LaunchCommand(q))
}
