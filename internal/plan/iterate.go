package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Iterative mockups (W268, decision Q53 A, tilasm L18): an option of a question may plan a mockup (kind: mockup).
// Answering it stores no answer: it records an iteration round (ROUND_KIND_ITERATE), with the developer's note, and
// the harness starts a mockup task (TASK_KIND_MOCKUP) that puts the next version of the tilasm the plan names, then
// revises the question, which waits for it meanwhile, as it does for an investigator (Investigating). Sending comments
// to iterate on that tilasm records the same round: one mockup task reads them all. The plan's rounds bound the
// versions the developer reviews from the question; at the last one, the answer is stored, and goes to the lead.

// DefaultMockupRounds is how many versions of its tilasm a mockup plan shows the developer when it names none.
const DefaultMockupRounds = 3

// maxMockupRounds is the most a plan's rounds header may give.
const maxMockupRounds = 9

// IterateHook starts the mockup task of the iteration round, the last of q's, and returns its code; "" when none
// starts, which the lead hears of instead.
type IterateHook func(ctx context.Context, q *planv1.Question, round *planv1.Round) string

// WithIterated calls f with a question once an iteration round is stored on it: the harness starts its mockup task.
// With WithLeads, the wish's lead is told next when none started (Wishes.Iterated).
func WithIterated(f IterateHook) Option {
	return func(o *options) { o.iterated = append(o.iterated, f) }
}

// IsMockupPlan tells whether the plan p plans a mockup.
func IsMockupPlan(p *planv1.PlannedTask) bool { return p.GetKind() == planv1.TaskKind_TASK_KIND_MOCKUP }

// MockupRounds is how many versions of its tilasm the mockup plan p shows the developer, the first one included.
func MockupRounds(p *planv1.PlannedTask) int {
	if n := p.GetRounds(); n > 0 {
		return int(n)
	}
	return DefaultMockupRounds
}

// Iterations is how many iteration rounds q went through.
func Iterations(q *planv1.Question) int {
	n := 0
	for _, r := range q.GetRounds() {
		if r.GetKind() == planv1.RoundKind_ROUND_KIND_ITERATE {
			n++
		}
	}
	return n
}

// Tour is the version of its mockup the question shows: 1 before any iteration, one more per iteration round.
func Tour(q *planv1.Question) int { return 1 + Iterations(q) }

// planOf is q's plan for the option choice; nil when it has none.
func planOf(q *planv1.Question, choice planv1.Choice) *planv1.PlannedTask {
	for _, p := range q.GetPlans() {
		if p.GetOption() == choice {
			return p
		}
	}
	return nil
}

// Iterates is the mockup plan an answer choice to the open question q iterates with; nil when that answer is an
// answer: its plan is no mockup, or q shows its last version already (MockupRounds), or q is decided.
func Iterates(q *planv1.Question, choice planv1.Choice) *planv1.PlannedTask {
	p := planOf(q, choice)
	if !IsMockupPlan(p) || q.GetAnswer() != nil || q.GetWithdrawal() != nil || Tour(q) >= MockupRounds(p) {
		return nil
	}
	return p
}

// MockupOption is the mockup plan of the open question q that iterates on the tilasm tilasmID, within its rounds; nil
// when no option of q does.
func MockupOption(q *planv1.Question, tilasmID string) *planv1.PlannedTask {
	for _, p := range q.GetPlans() {
		if Iterates(q, p.GetOption()) != nil && strings.EqualFold(iteratedTilasm(q, p), tilasmID) {
			return p
		}
	}
	return nil
}

// Iterating is q's iteration round under way: its last round, while q is open; nil otherwise.
func Iterating(q *planv1.Question) *planv1.Round {
	rounds := q.GetRounds()
	if q.GetAnswer() != nil || q.GetWithdrawal() != nil || len(rounds) == 0 {
		return nil
	}
	if last := rounds[len(rounds)-1]; last.GetKind() == planv1.RoundKind_ROUND_KIND_ITERATE {
		return last
	}
	return nil
}

// tilasmLinkID finds the tilasms a text links, by identifier.
var tilasmLinkID = regexp.MustCompile(`(?i)djinn://tilasm/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// iteratedTilasm is the tilasm the mockup plan p of q iterates on: the first its plan names, else the first q links;
// "" when neither names one.
func iteratedTilasm(q *planv1.Question, p *planv1.PlannedTask) string {
	if t := p.GetTilasm(); len(t) > 0 {
		id, _, _ := strings.Cut(t[0], "@")
		return id
	}
	for _, s := range append([]string{q.GetText(), q.GetContext(), q.GetRecommendation()}, q.GetOptions()...) {
		if m := tilasmLinkID.FindStringSubmatch(s); m != nil {
			return strings.ToLower(m[1])
		}
	}
	return ""
}

// iterationRound is the round that iterates on q with its mockup plan p: the note, the tilasm and the version the
// developer saw, the send of comments that started it if any (send); the question as it stands, as Enlighten keeps it.
func iterationRound(ctx context.Context, r store.Reader, q *planv1.Question, p *planv1.PlannedTask, note string, send int32) (*planv1.Round, error) {
	round := &planv1.Round{
		Kind: planv1.RoundKind_ROUND_KIND_ITERATE, Actor: actor, CreateTime: timestamppb.Now(), Note: strings.TrimSpace(note),
		Option: p.GetOption(), Send: send, Context: q.GetContext(), Options: slices.Clone(q.GetOptions()),
		Recommendation: q.GetRecommendation(),
	}
	if id := iteratedTilasm(q, p); id != "" {
		tilasm, err := store.Get[*planv1.Tilasm](ctx, r, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			round.TilasmId, round.Version = tilasm.GetId(), latestVersion(tilasm)
		}
	}
	return round, nil
}

// iterated runs the iteration hooks on q, its last round round just stored, and returns the code of the mockup task
// one started; none started, the lead hears of it (Wishes.Iterated).
func (q *Questions) iterated(ctx context.Context, question *planv1.Question, round *planv1.Round) string {
	// The round is stored: its mockup task starts, or its lead hears of it, even when the caller hangs up meanwhile.
	ctx = context.WithoutCancel(ctx)
	code := ""
	for _, f := range q.Iterated {
		code = cmp.Or(code, f(ctx, proto.CloneOf(question), proto.CloneOf(round)))
	}
	if code == "" && q.Wishes != nil {
		q.Wishes.Iterated(ctx, question, round)
	}
	return code
}

// Iterated tells the lead of q's wish that the developer iterates on its mockup with round, and that no mockup task
// started: the lead iterates.
func (w *Wishes) Iterated(ctx context.Context, q *planv1.Question, round *planv1.Round) {
	w.notify(ctx, q, planv1.LeadEventKind_LEAD_EVENT_KIND_ENLIGHTEN, IterateLine(ctx, w.Store, q, round), round.GetNote())
}

// IterateLine is the line that tells a lead the developer iterates on q's mockup with round, and that no mockup task
// works on it: the command that spawns one.
func IterateLine(ctx context.Context, r store.Reader, q *planv1.Question, round *planv1.Round) string {
	code := round.GetTilasmId()
	if t, err := store.Get[*planv1.Tilasm](ctx, r, round.GetTilasmId()); err == nil {
		code = t.GetCode()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Djinn: %s, the developer iterates on its mockup (%s, version %d of %d)", q.GetCode(),
		choiceLetter(round.GetOption()), Tour(q), MockupRounds(planOf(q, round.GetOption())))
	if code != "" {
		fmt.Fprintf(&b, " from %s v%d", code, round.GetVersion())
	}
	if round.GetSend() > 0 {
		fmt.Fprintf(&b, ", with send %d of its comments", round.GetSend())
	}
	b.WriteString(".")
	if note := oneLine(round.GetNote()); note != "" {
		fmt.Fprintf(&b, " Note: \"%s\".", note)
	}
	fmt.Fprintf(&b, " No mockup task started: spawn one with djinn task spawn %s --kind mockup --question %s", q.GetWishId(),
		q.GetCode())
	if code != "" {
		b.WriteString(" --tilasm " + code)
	}
	b.WriteString(", or revise it yourself.")
	return b.String()
}
