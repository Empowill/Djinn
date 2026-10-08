package render

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/locales"
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
	want := []string{"questions", "actions", "running", "tasks", "decisions", "notes", "journal"}
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
		// Tasks: status, agent, time, cost, last word; the failed one in clear.
		"Running", "claude · opus", "started 2026-10-08 14:00", "5m", "$1.50", "Tests pass: 12 of 12.", "exit code 2", "Failed",
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
	if strings.Count(html, `<details class="card question`) != 2 {
		t.Error("answered questions are decisions, not open questions")
	}
}

func TestEmptyPage(t *testing.T) {
	html := page(t, Input{Export: &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Nothing yet"}}})
	if got := sections(html); len(got) != 0 {
		t.Errorf("an empty wish has no section, got %v", got)
	}
	for _, s := range []string{"To decide", "Waiting for you", "Decisions", "Journal", "Tasks", "Running now", `class="toc"`, "<details"} {
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

// french is the text of key in French, as the page escapes it. The French itself stays in locales/fr.json.
func french(t *testing.T, key string, params map[string]string) string {
	t.Helper()
	fr := locales.T("fr", key, params)
	if fr == locales.T(locales.Source, key, params) {
		t.Fatalf("%s is not translated into French", key)
	}
	return template.HTMLEscapeString(fr)
}

func TestFrench(t *testing.T) {
	html := page(t, Input{Export: rich(t), Language: "fr"})
	want := []string{`<html lang="fr">`}
	for _, key := range []string{"page.questions", "page.decisions", "page.actions"} {
		want = append(want, french(t, key, nil))
	}
	for _, s := range want {
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

// TestWishState: the page says where the wish stands, and proposes a ready wish to the user, who grants it.
func TestWishState(t *testing.T) {
	for _, tt := range []struct {
		name string
		wish *planv1.Wish
		want []string
		not  []string
	}{
		{"ranked", &planv1.Wish{Id: "w", Title: "T", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 2},
			[]string{`<span class="status run">Active · rank 2</span>`}, []string{"djinn wish grant"}},
		{"stored before states", &planv1.Wish{Id: "w", Title: "T"}, []string{`<span class="status run">Active</span>`}, nil},
		{"paused", &planv1.Wish{Id: "w", Title: "T", State: planv1.WishState_WISH_STATE_PAUSED},
			[]string{`<span class="status idle">Paused</span>`}, nil},
		{"granted", &planv1.Wish{Id: "w", Title: "T", State: planv1.WishState_WISH_STATE_GRANTED, GrantTime: ts(-5)},
			[]string{`<span class="status ok">Granted 2026-10-08 14:25</span>`}, nil},
		{"ready", &planv1.Wish{Id: "w1", Title: "T", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1, Ready: true},
			[]string{`<section id="actions">`, "Djinn proposes to grant this wish", "djinn wish grant w1"}, nil},
	} {
		html := page(t, Input{Export: &planv1.WishExport{Wish: tt.wish}, Language: "en"})
		for _, s := range tt.want {
			if !strings.Contains(html, s) {
				t.Errorf("%s: the page lacks %q", tt.name, s)
			}
		}
		for _, s := range tt.not {
			if strings.Contains(html, s) {
				t.Errorf("%s: the page shows %q", tt.name, s)
			}
		}
	}
	fr := page(t, Input{Export: &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "T", Rank: 1, Ready: true}}, Language: "fr"})
	if !strings.Contains(fr, french(t, "page.state_ranked", map[string]string{"rank": "1"})) ||
		!strings.Contains(fr, french(t, "page.action_ready", map[string]string{"wish": "w"})) {
		t.Error("the French page lacks the state or the proposal")
	}
}

var tocLink = regexp.MustCompile(`<a class="pill[^"]*" href="#([a-z]+)"`)

// TestBusyPage: on a wish with much in it, what matters now comes first, and the rest is folded.
func TestBusyPage(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Busy"}}
	exp.Questions = append(exp.Questions,
		&planv1.Question{Id: "qa", Code: "Q90", Text: "A question nobody waits for", Recommendation: "**B**, because it is `simple`.\n\nMore."},
		&planv1.Question{Id: "qb", Code: "Q91", Text: "May W5 edit?"},
	)
	for i := range 20 {
		exp.Questions = append(exp.Questions, &planv1.Question{
			Id: fmt.Sprintf("d%02d", i), Code: fmt.Sprintf("Q%02d", i), Text: fmt.Sprintf("Decision %02d", i),
			Answer: &planv1.Answer{Choice: planv1.Choice_CHOICE_YES, CreateTime: ts(i)},
		})
	}
	exp.Questions[len(exp.Questions)-1].Answer.Note = strings.Repeat("A long note. ", 30)
	exp.Tasks = []*planv1.Task{
		{Id: "t1", Code: "W1", Title: "Done one", Status: planv1.TaskStatus_TASK_STATUS_DONE},
		{
			Id: "t2", Code: "W2", Title: "Planned one", Status: planv1.TaskStatus_TASK_STATUS_PENDING, DependsOn: []string{"t1", "t3"},
			WaitReason: "waits for W3 to be done",
		},
		{Id: "t3", Code: "W3", Title: "Running one", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, StartTime: ts(-5)},
		{Id: "t4", Code: "W4", Title: "Failed one", Status: planv1.TaskStatus_TASK_STATUS_FAILED, Error: "exit code 1"},
		{Id: "t5", Code: "W5", Title: "Waiting one", Status: planv1.TaskStatus_TASK_STATUS_WAITING, EditQuestionId: "qb"},
	}
	exp.Blocks = []*planv1.Block{
		{Id: "b1", Kind: "section", Title: "Short", Content: "A few words."},
		{Id: "b2", Kind: "report", Content: "# Long report\n\n" + strings.Repeat("A line of the report.\n", 40)},
	}
	for i := range blockRun + 1 {
		exp.Blocks = append(exp.Blocks, &planv1.Block{Id: fmt.Sprintf("r%d", i), Kind: "decision", Title: fmt.Sprintf("Run %d", i), Content: "Short."})
	}
	for i := range 40 {
		exp.Blocks = append(exp.Blocks, &planv1.Block{
			Id: fmt.Sprintf("l%02d", i), Kind: "log", Title: "title of the log", Content: fmt.Sprintf("Log **%02d**", i), CreateTime: ts(i),
		})
	}
	html := page(t, Input{Export: exp, Language: "en"})

	got := sections(html)
	want := []string{"questions", "actions", "running", "tasks", "decisions", "notes", "journal"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sections = %v, want %v", got, want)
	}
	var toc []string
	for _, m := range tocLink.FindAllStringSubmatch(html, -1) {
		toc = append(toc, m[1])
	}
	if strings.Join(toc, ",") != strings.Join(got, ",") {
		t.Errorf("the contents list %v, the page holds %v", toc, got)
	}

	// The question a task waits for comes first, open and red; the other is folded, its recommendation in sight.
	if i, j := strings.Index(html, "May W5 edit?"), strings.Index(html, "A question nobody waits for"); i > j {
		t.Error("a blocking question comes first")
	}
	for _, s := range []string{
		`<details class="card question now" open>`, "Blocks W5", `<details class="card question">`, `→ B, because it is simple.`,
	} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}

	// Running now: the running worker only. Tasks: running, waiting and failed in clear; planned and finished folded.
	running := between(html, `<section id="running">`, "</section>")
	if !strings.Contains(running, "W3") || strings.Count(running, `class="card task"`) != 1 {
		t.Errorf("running now shows the running worker only:\n%s", running)
	}
	tasks := between(html, `<section id="tasks">`, "</section>")
	inClear := between(tasks, "", "<details")
	for _, code := range []string{"W3", "W4", "W5"} {
		if !strings.Contains(inClear, ">"+code+"<") {
			t.Errorf("%s shows in clear", code)
		}
	}
	for _, code := range []string{"W1", "W2"} {
		if strings.Contains(inClear, ">"+code+"<") {
			t.Errorf("%s is folded", code)
		}
	}
	for _, s := range []string{
		`<details class="fold planned">`, `Planned <span class="n">1</span>`, `<details class="fold finished">`,
		"after W1, W3", "waits for W3 to be done", "exit code 1",
	} {
		if !strings.Contains(tasks, s) {
			t.Errorf("the tasks lack %q", s)
		}
	}

	// Decisions: the 15 latest, the latest first, then the 5 before them folded.
	decisions := between(html, `<section id="decisions">`, "</section>")
	shown, older, _ := strings.Cut(decisions, `<details class="fold older">`)
	if n := strings.Count(shown, "<tr>\n      <td>"); n != shownDecisions {
		t.Errorf("%d decisions shown, want %d", n, shownDecisions)
	}
	if n := strings.Count(older, "<tr>\n      <td>"); n != 5 {
		t.Errorf("%d decisions folded, want 5", n)
	}
	if strings.Index(shown, "Decision 19") > strings.Index(shown, "Decision 18") || strings.Contains(shown, "Decision 04") {
		t.Error("the latest decisions come first, the older ones folded")
	}
	if strings.Count(shown, `<details class="note">`) != 1 {
		t.Error("a long note is folded")
	}

	// Notes: in their order, a long one folded under its first line; no log block.
	notes := between(html, `<section id="notes">`, "</section>")
	if strings.Contains(notes, "Log ") || strings.Contains(notes, "title of the log") {
		t.Error("log blocks go to the journal")
	}
	if !strings.Contains(notes, `<details class="long">`) || !strings.Contains(notes, "<h3>Long report</h3>") {
		t.Error("a long block is folded under its first line")
	}
	if n := strings.Count(notes, "<details"); n != 1+blockRun+1 {
		t.Errorf("%d blocks folded, want the long one and the run of %d", n, blockRun+1)
	}
	if !strings.Contains(notes, `<div class="block-head"><span class="kind">section</span><h3>Short</h3>`) {
		t.Error("a short block shows whole")
	}

	// Journal: the latest first, 30 in sight, the 10 others folded.
	journal := between(html, `<section id="journal">`, "</section>")
	inSight, earlier, _ := strings.Cut(journal, `<details class="fold earlier">`)
	if n := strings.Count(inSight, "<li>"); n != shownJournal {
		t.Errorf("%d journal entries in sight, want %d", n, shownJournal)
	}
	if n := strings.Count(earlier, "<li>"); n != 10 {
		t.Errorf("%d journal entries folded, want 10", n)
	}
	if !strings.Contains(inSight, "Log <strong>39</strong>") || strings.Index(inSight, "39") > strings.Index(inSight, "38") {
		t.Error("the journal shows the latest log first, in Markdown")
	}
}

// between is the part of s from start to the next end; from the beginning of s when start is empty.
func between(s, start, end string) string {
	i := 0
	if start != "" {
		if i = strings.Index(s, start); i < 0 {
			return ""
		}
	}
	s = s[i:]
	if j := strings.Index(s, end); j >= 0 {
		return s[:j]
	}
	return s
}

// TestRounds: a question being investigated says so, a revised one says how many times, and its rounds fold below,
// read-only, with the developer's note.
func TestRounds(t *testing.T) {
	enlighten := &planv1.Round{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, CreateTime: ts(-30), Note: "How long does it burn?"}
	revise := &planv1.Round{Kind: planv1.RoundKind_ROUND_KIND_REVISE, CreateTime: ts(-20)}
	exp := &planv1.WishExport{
		Wish: &planv1.Wish{Id: "w", Title: "Light the lamp"},
		Questions: []*planv1.Question{
			{Id: "q1", Code: "Q01", Text: "Which oil?", Revision: 1, CreateTime: ts(-40),
				Rounds: []*planv1.Round{enlighten, revise, {Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, CreateTime: ts(-10)}}},
			{Id: "q2", Code: "Q02", Text: "Which wick?", Revision: 1, CreateTime: ts(-40),
				Rounds: []*planv1.Round{enlighten, revise}},
		},
	}
	html := page(t, Input{Export: exp})
	if n := strings.Count(html, `<span class="status dig">Being investigated</span>`); n != 1 {
		t.Errorf("%d questions shown investigated, want Q01 only", n)
	}
	for _, s := range []string{`class="card question dig"`, "Revised ×1", `Rounds <span class="n">3</span>`,
		"Asked to investigate: How long does it burn?", "Revised by the lead"} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
}
