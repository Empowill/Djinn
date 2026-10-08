package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

var now = time.Date(2026, 10, 8, 14, 30, 0, 0, time.UTC)

func ts(minutes int) *timestamppb.Timestamp {
	return timestamppb.New(now.Add(time.Duration(minutes) * time.Minute))
}

func journalEntry(t *testing.T, method string, req proto.Message, minutes int) *planv1.Command {
	t.Helper()
	packed, err := anypb.New(req)
	if err != nil {
		t.Fatal(err)
	}
	return &planv1.Command{Id: "c" + method, Actor: "local", At: ts(minutes), Method: method, Request: packed}
}

// rich is a wish with something in every section.
func rich(t *testing.T) *planv1.WishExport {
	wish := &planv1.Wish{Id: "w", Title: "Ship the wish page", ProjectIds: []string{"p1"}, CreateTime: ts(-120)}
	return &planv1.WishExport{
		Wish:     wish,
		Projects: []*planv1.ProjectRef{{Id: "p1", Name: "djinn"}},
		Questions: []*planv1.Question{
			{
				Id: "q1", Code: "Q01", WishId: "w", Text: "Which Markdown library?", Options: []string{"goldmark", "blackfriday"},
				Context: "Both are **MIT**.", Recommendation: "A: maintained.", CreateTime: ts(-100),
			},
			{
				Id: "q2", Code: "Q02", WishId: "w", Text: "Keep the old page?", Options: []string{"Yes", "No"},
				CreateTime: ts(-90), Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_B, Note: "Drop it.", CreateTime: ts(-80)},
			},
			{Id: "q3", Code: "Q03", WishId: "w", Text: "May W2 edit the folder?", CreateTime: ts(-10)},
		},
		Tasks: []*planv1.Task{
			{
				Id: "t1", WishId: "w", ProjectId: "p1", Code: "W1", Title: "Render the page", Status: planv1.TaskStatus_TASK_STATUS_RUNNING,
				Provider: planv1.Provider_PROVIDER_CLAUDE, Model: "opus", StartTime: ts(-30), Usage: &planv1.Usage{CostUsd: 1.5},
			},
			{
				Id: "t2", WishId: "w", Code: "W2", Title: "Edit notes", Status: planv1.TaskStatus_TASK_STATUS_WAITING,
				EditQuestionId: "q3", Error: "waiting for the answer to its edit question",
			},
			{Id: "t3", WishId: "w", Code: "W3", Title: "Old run", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED},
			{
				Id: "t4", WishId: "w", Code: "W4", Title: "Spike", Status: planv1.TaskStatus_TASK_STATUS_FAILED, Error: "exit code 2",
				StartTime: ts(-60), EndTime: ts(-55),
			},
		},
		Events: []*planv1.TaskEvent{
			{Id: "e1", TaskId: "t1", Seq: 1, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "Starting."},
			{Id: "e2", TaskId: "t1", Seq: 2, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "Tests pass: 12 of 12."},
		},
		Blocks: []*planv1.Block{
			{Id: "b1", WishId: "w", Kind: "section", Title: "Approach", Content: "Djinn renders in **Go**.", Position: 1000, UpdateTime: ts(-5)},
			{Id: "b2", WishId: "w", TaskId: "t1", Kind: "report", Title: "Data", MediaType: "application/json", Content: `{"ok": true}`, Position: 2000},
		},
		Commands: []*planv1.Command{
			journalEntry(t, "/plan.v1.WishService/Make", &planv1.WishServiceMakeRequest{Title: "Ship the wish page"}, -120),
			journalEntry(t, "/plan.v1.QuestionService/Answer", &planv1.QuestionServiceAnswerRequest{
				Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: "Q02"}}, Choice: planv1.Choice_CHOICE_B,
			}, -80),
		},
	}
}

func page(t *testing.T, in Input) string {
	t.Helper()
	if in.Now.IsZero() {
		in.Now = now
	}
	b, err := Page(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var sectionID = regexp.MustCompile(`<section id="([a-z]+)"`)

func sections(html string) []string {
	var ids []string
	for _, m := range sectionID.FindAllStringSubmatch(html, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func TestRichPage(t *testing.T) {
	html := page(t, Input{Export: rich(t), Unattached: []string{"web"}, Version: "v1.2.3", Language: "en"})
	want := []string{"questions", "actions", "tasks", "decisions", "notes", "journal"}
	if got := sections(html); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections = %v, want %v", got, want)
	}
	for _, s := range []string{
		"<title>Ship the wish page</title>", "Rendered by Djinn v1.2.3, 2026-10-08 14:30 UTC", "<li>djinn</li>",
		// Open questions, with their context and recommendation in Markdown; Q02 is a decision.
		"Which Markdown library?", "<strong>MIT</strong>", "A: maintained.", "<span>goldmark</span>",
		"May W2 edit the folder?", "To be answered with a yes.",
		// What waits for the user.
		"W2 waits for your answer to Q03 before it may edit.", "W3 was cut short when Djinn stopped",
		"Project web is not on this machine yet",
		// Tasks: status, agent, time, cost, last word; the failed one among the finished.
		"Running", "claude · opus", "started 2026-10-08 14:00", "5m", "$1.50", "Tests pass: 12 of 12.", "exit code 2", "Finished",
		// Decisions, dated, with the option chosen.
		"Keep the old page?", "B · No", "2026-10-08 13:10", "<p>Drop it.</p>",
		// Blocks in order, Markdown rendered, another media type as text.
		"<strong>Go</strong>", "About W1", "{&#34;ok&#34;: true}",
		// The journal, the latest first, spelled as commands.
		"question answer", "wish make",
	} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
	if strings.Contains(html, "waiting for the answer to its edit question") {
		t.Error("a waiting task shows no error")
	}
	if strings.Contains(html, "Starting.") {
		t.Error("only the worker's last word shows")
	}
	if strings.Index(html, "question answer") > strings.Index(html, "wish make") {
		t.Error("the journal shows the latest entry first")
	}
	if strings.Index(html, "Approach") > strings.Index(html, "Data") {
		t.Error("blocks keep their order")
	}
	if strings.Count(html, `class="card question"`) != 2 {
		t.Error("answered questions are decisions, not open questions")
	}
}

func TestEmptyPage(t *testing.T) {
	html := page(t, Input{Export: &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Nothing yet"}}})
	if got := sections(html); len(got) != 0 {
		t.Errorf("an empty wish has no section, got %v", got)
	}
	for _, s := range []string{"To decide", "Waiting for you", "Decisions", "Journal", "Tasks", `class="counts"`} {
		if strings.Contains(html, s) {
			t.Errorf("an empty wish shows %q", s)
		}
	}
	if !strings.Contains(html, "<h1>Nothing yet</h1>") {
		t.Error("the header shows the wish")
	}
}

func TestPageIsSafe(t *testing.T) {
	exp := &planv1.WishExport{
		Wish: &planv1.Wish{Id: "w", Title: `<script>alert("title")</script>`},
		Questions: []*planv1.Question{{
			Id: "q", Code: "Q01", WishId: "w", Text: "<img src=x onerror=alert(1)>",
			Context: "[click](javascript:alert(1)) <iframe src=\"https://example.com\"></iframe>",
		}},
		Blocks: []*planv1.Block{
			{Id: "b1", WishId: "w", Title: "md", Content: "Hi <script>alert(1)</script>\n\n<div onclick=\"x()\">raw</div>\n\n[x](JavaScript:alert(1))"},
			{Id: "b2", WishId: "w", Title: "html", MediaType: "text/html", Content: "<script>alert(2)</script>"},
		},
	}
	html := page(t, Input{Export: exp})
	lower := strings.ToLower(html)
	// Text is escaped: <img onerror=…> shows as text, never as a tag.
	for _, bad := range []string{"<script", "<iframe", "<img", "<div onclick", "javascript:"} {
		if strings.Contains(lower, bad) {
			t.Errorf("the page holds %q", bad)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;alert(2)&lt;/script&gt;") {
		t.Error("a block of another media type shows as escaped text")
	}
}

func TestFrench(t *testing.T) {
	html := page(t, Input{Export: rich(t), Language: "fr"})
	for _, s := range []string{`<html lang="fr">`, "À décider", "Décisions", "Vous attend"} {
		if !strings.Contains(html, s) {
			t.Errorf("the French page lacks %q", s)
		}
	}
}

func TestJournalCut(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Busy"}}
	for i := range maxJournal + 5 {
		exp.Commands = append(exp.Commands, journalEntry(t, "/plan.v1.BlockService/Put", &planv1.BlockServicePutRequest{Title: "b"}, i))
	}
	html := page(t, Input{Export: exp})
	if n := strings.Count(html, "<code>block put</code>"); n != maxJournal {
		t.Errorf("the journal shows %d entries, want %d", n, maxJournal)
	}
	if !strings.Contains(html, "Earlier entries not shown: 5.") {
		t.Error("the page says how many entries it left out")
	}
}
