package render

import (
	"cmp"
	"slices"
	"strings"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// DecisionKind is the kind of block that holds a decision taken outside a question.
const DecisionKind = "decision"

// IsDecision tells a block of kind decision, case ignored.
func IsDecision(b *planv1.Block) bool { return strings.EqualFold(b.GetKind(), DecisionKind) }

// Default icons of a decision without its own: an answered question, a decision block.
const (
	QuestionIcon = "💬"
	BlockIcon    = "📌"
)

// Decided is a decision of a wish, as the decision log shows it: an answered question or a block of kind decision,
// with who took it and the tasks it led to.
type Decided struct {
	// One of the two.
	Question *planv1.Question
	Block    *planv1.Block
	// When it was taken: the answer, or the block's creation.
	At time.Time
	// Human says the developer took it: an answered question, or a block the developer approved.
	Human bool
	// Approved says the developer took it by an approval: the recommendation on a question, a block as it is.
	Approved bool
	// By is the agent that took a block: "lead", or the code of the task the block is about. Empty when Human.
	By string
	// Icon is its emoji: its own, or the default of its kind.
	Icon string
	// Tasks are the codes of the tasks that name it as their decision, in their order.
	Tasks []string
}

// ByLead is what Decided.By says of a block written by the wish's lead.
const ByLead = "lead"

// Decisions are the decisions of an export, the latest first: answered questions and blocks of kind decision.
func Decisions(exp *planv1.WishExport) []Decided {
	codes := map[string]string{}
	for _, t := range exp.GetTasks() {
		codes[t.GetId()] = t.GetCode()
	}
	led := map[string][]string{}
	for _, t := range exp.GetTasks() {
		if ref := t.GetDecision(); ref != "" {
			led[strings.ToUpper(ref)] = append(led[strings.ToUpper(ref)], t.GetCode())
		}
	}
	var out []Decided
	for _, q := range exp.GetQuestions() {
		if q.GetAnswer() == nil {
			continue
		}
		out = append(out, Decided{
			Question: q, At: q.GetAnswer().GetCreateTime().AsTime(), Human: true, Approved: approved(q.GetMarks()),
			Icon: cmp.Or(q.GetIcon(), QuestionIcon), Tasks: led[strings.ToUpper(q.GetCode())],
		})
	}
	for _, b := range exp.GetBlocks() {
		if !IsDecision(b) {
			continue
		}
		d := Decided{
			Block: b, At: b.GetCreateTime().AsTime(), Icon: cmp.Or(b.GetIcon(), BlockIcon),
			Tasks: led[strings.ToUpper(b.GetId())], By: cmp.Or(codes[b.GetTaskId()], ByLead),
		}
		if approved(b.GetMarks()) {
			d.Human, d.Approved, d.By = true, true, ""
		}
		out = append(out, d)
	}
	slices.SortStableFunc(out, func(a, b Decided) int { return b.At.Compare(a.At) })
	return out
}

func approved(marks []*planv1.Mark) bool {
	return slices.ContainsFunc(marks, func(m *planv1.Mark) bool { return m.GetKind() == planv1.MarkKind_MARK_KIND_APPROVED })
}
