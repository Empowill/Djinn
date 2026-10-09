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
}

// TestDecisionTable: the page's decision table shows the emoji, the developer's decisions in the human colour with an
// icon and a word, an agent's in plain words, and links to the tasks they led to; a decision block is no longer a
// note.
func TestDecisionTable(t *testing.T) {
	html := page(t, Input{Export: decisionLog(t), Language: "en"})
	table := between(html, `<section id="decisions">`, "</section>")
	for _, s := range []string{
		`<tr class="human">`, `<span class="ico">🔒</span><span class="code">Q02</span>Keep the old page?`,
		`<span class="st human"><i aria-hidden="true">✋︎</i>By you</span>`,
		`<span class="st human"><i aria-hidden="true">✋︎</i>Approved by you</span>`,
		`<span class="by">By W1</span>`, `<span class="by">By the lead</span>`,
		`<b class="chosen"><span class="ico">🧱</span>Goldmark, no raw HTML</b>`,
		`<b class="chosen"><span class="ico">📌</span>Split the CSS</b>`,
		`Led to <a class="code" href="#t-W1">W1</a>`, `Led to <a class="code" href="#t-W4">W4</a>`,
	} {
		if !strings.Contains(table, s) {
			t.Errorf("the decision table lacks %q:\n%s", s, table)
		}
	}
	if strings.Index(table, "Split the CSS") > strings.Index(table, "Keep the old page?") {
		t.Error("the latest decision comes first")
	}
	for _, s := range []string{`id="t-W1"`, `id="t-W4"`} {
		if !strings.Contains(html, s) {
			t.Errorf("the page has no anchor %s for a decision's link", s)
		}
	}
	if notes := between(html, `<section id="notes">`, "</section>"); strings.Contains(notes, "Split the CSS") {
		t.Error("a decision block shows in the notes too")
	}
}
