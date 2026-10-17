package render

import (
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// decisionLog is a wish with three decisions: one the developer answered, one a worker wrote, one the lead wrote and
// the developer approved; two tasks came from them.
func decisionLog(t *testing.T) *planv1.WishExport {
	exp := rich(t)
	exp.Questions[1].Icon = "🔒"
	exp.Tasks[0].Decision = "Q02"
	exp.Tasks[3].Decision = "b4"
	exp.Blocks = append(exp.Blocks,
		&planv1.Block{Id: "b3", WishId: "w", TaskId: "t1", Kind: "decision", Title: "Goldmark, no raw HTML", Content: "Safer.",
			Icon: "🧱", CreateTime: ts(-70), Position: 3000},
		&planv1.Block{Id: "b4", WishId: "w", Kind: "Decision", Title: "Postpone the dark theme", Content: "Later, once the page holds.",
			CreateTime: ts(-60), Position: 4000,
			Marks: []*planv1.Mark{{Kind: planv1.MarkKind_MARK_KIND_APPROVED, Actor: "local", CreateTime: ts(-50)}}},
		&planv1.Block{Id: "b5", WishId: "w", Kind: "decision", Title: "Split the CSS", CreateTime: ts(-40), Position: 5000},
	)
	return exp
}

// TestDecisions: the decisions are the answered questions and the decision blocks, the latest first, each with its
// emoji, who took it, and the tasks that name it.
func TestDecisions(t *testing.T) {
	t.Parallel()
	t.Run("blocks and questions", func(t *testing.T) {
		t.Parallel()
		ds := Decisions(decisionLog(t))
		var got []string
		for _, d := range ds {
			id := d.Question.GetCode() + d.Block.GetId()
			got = append(got, strings.Join([]string{id, d.Icon, d.By, strings.Join(d.Tasks, "+")}, " "))
			if d.Human != (d.By == "") {
				t.Errorf("%s: human %v, by %q", id, d.Human, d.By)
			}
		}
		want := []string{"b5 📌 lead ", "b4 📌  W4", "b3 🧱 W1 ", "Q02 🔒  W1"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("decisions = %q, want %q", got, want)
		}
		if !ds[1].Approved || ds[3].Approved {
			t.Error("the approved block reads approved, the answered question does not")
		}
	})

	// Questions answered by the developer, the lead or a worker record their actor.
	t.Run("question actor", func(t *testing.T) {
		t.Parallel()
		exp := &planv1.WishExport{
			Wish: &planv1.Wish{Id: "w", Title: "Wish"},
			Tasks: []*planv1.Task{
				{Id: "t1", WishId: "w", Code: "W1", Title: "Worker task"},
			},
			Questions: []*planv1.Question{
				{
					Id: "q1", Code: "Q01", WishId: "w", Text: "Developer decision",
					Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_A, Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: ts(-30)},
				},
				{
					Id: "q2", Code: "Q02", WishId: "w", Text: "Lead decision",
					Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_B, Actor: planv1.Closer_CLOSER_LEAD, CreateTime: ts(-20)},
				},
				{
					Id: "q3", Code: "Q03", WishId: "w", Text: "Worker decision",
					Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES, TaskId: "t1", CreateTime: ts(-10)},
				},
			},
		}
		ds := Decisions(exp)
		if len(ds) != 3 {
			t.Fatalf("got %d decisions, want 3", len(ds))
		}
		// Latest first: Q03 (worker), Q02 (lead), Q01 (developer)
		if !ds[2].Human || ds[2].By != "" || ds[2].Question.GetCode() != "Q01" {
			t.Errorf("Q01 developer: got human=%v, by=%q", ds[2].Human, ds[2].By)
		}
		if ds[1].Human || ds[1].By != ByLead || ds[1].Question.GetCode() != "Q02" {
			t.Errorf("Q02 lead: got human=%v, by=%q", ds[1].Human, ds[1].By)
		}
		if ds[0].Human || ds[0].By != "W1" || ds[0].Question.GetCode() != "Q03" {
			t.Errorf("Q03 worker: got human=%v, by=%q", ds[0].Human, ds[0].By)
		}
	})
}
