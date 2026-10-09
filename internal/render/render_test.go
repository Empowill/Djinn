package render

import (
	"fmt"
	"html/template"
	"math"
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
	want := []string{"questions", "actions", "running", "tasks", "decisions", "notes", "journal", "events"}
	if got := sections(html); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections = %v, want %v", got, want)
	}
	for _, s := range []string{
		"<title>Ship the wish page</title>", "Rendered by Djinn v1.2.3, 2026-10-08 14:30 UTC", "<li>djinn</li>",
		// Open questions, with their context and recommendation in Markdown; Q02 is a decision.
		"Which Markdown library?", "<strong>MIT</strong>", "A: maintained.", "<span>goldmark</span>",
		"May W2 edit the folder?", "To be answered with a yes.",
		// What waits for the user.
		"W2 waits for your answer to Q03 before it may edit.",
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
	if running := between(html, `<section id="running">`, "</section>"); strings.Contains(running, "Starting.") {
		t.Error("a worker's card shows its last event only")
	}
	if strings.Index(html, "question answer") > strings.Index(html, "wish make") {
		t.Error("the journal shows the latest entry first")
	}
	if strings.Index(html, "Approach") > strings.Index(html, "Data") {
		t.Error("blocks keep their order")
	}
	if strings.Count(html, `<details class="q `) != 2 {
		t.Error("answered questions are decisions, not open questions")
	}
}

func TestEmptyPage(t *testing.T) {
	html := page(t, Input{Export: &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Nothing yet"}}})
	if got := sections(html); len(got) != 0 {
		t.Errorf("an empty wish has no section, got %v", got)
	}
	for _, s := range []string{
		"To decide", "Waiting for you", "Decisions", "Journal", "Tasks", "Who runs now", "Worker events", `class="toc"`,
		`class="bar"`, `class="counts"`, "<details",
	} {
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

// TestPageKeepsDjinnLinks: a djinn:// link keeps its target on the page, where the system opens it with djinn open.
func TestPageKeepsDjinnLinks(t *testing.T) {
	const link = "djinn://tilasm/01a1223a-ae45-728f-8c37-c005eee91edb"
	exp := &planv1.WishExport{
		Wish:   &planv1.Wish{Id: "w", Title: "Explain"},
		Blocks: []*planv1.Block{{Id: "b1", WishId: "w", Title: "md", Content: "See [L01](" + link + ")."}},
	}
	if html := page(t, Input{Export: exp}); !strings.Contains(html, `<a href="`+link+`">L01</a>`) {
		t.Errorf("the page lost the link:\n%s", html)
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
			[]string{`<span class="st run"><i aria-hidden="true"></i>Active · rank 2</span>`}, []string{"djinn wish grant", `class="bar"`}},
		{"stored before states", &planv1.Wish{Id: "w", Title: "T"}, []string{`<span class="st run"><i aria-hidden="true"></i>Active</span>`}, nil},
		{"paused", &planv1.Wish{Id: "w", Title: "T", State: planv1.WishState_WISH_STATE_PAUSED},
			[]string{`<span class="st pause"><i aria-hidden="true">‖</i>Paused</span>`}, nil},
		{"granted", &planv1.Wish{Id: "w", Title: "T", State: planv1.WishState_WISH_STATE_GRANTED, GrantTime: ts(-5)},
			[]string{`<span class="st ok"><i aria-hidden="true">✓</i>Granted 2026-10-08 14:25</span>`}, nil},
		{"ready", &planv1.Wish{Id: "w1", Title: "T", State: planv1.WishState_WISH_STATE_ACTIVE, Rank: 1, Ready: true},
			[]string{
				`<section id="actions">`, "Djinn proposes to grant this wish", "djinn wish grant w1",
				// Granting can wait: the bar says so, in its neutral colour.
				`<a class="line later" href="#actions"><span class="st later"><i aria-hidden="true">◷</i>Can wait</span>`,
			}, nil},
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
		&planv1.Question{
			Id: "qa", Code: "Q90", Text: "A question nobody waits for", Recommendation: "**B**, because it is `simple`.\n\nMore.",
			Options: []string{"Keep it", "Change it"}, Context: "### What changes\n\nThe page.",
		},
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
		exp.Blocks = append(exp.Blocks, &planv1.Block{Id: fmt.Sprintf("r%d", i), Kind: "finding", Title: fmt.Sprintf("Run %d", i), Content: "Short."})
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
		`<details class="q bad" id="q-Q91" open>`, `<span class="st bad"><i aria-hidden="true">!</i>Blocks W5</span>`,
		`<details class="q wait" id="q-Q90">`, `<span class="st wait"><i aria-hidden="true">?</i>Waiting for you</span>`,
		`<span class="qreco">Recommended: B, because it is simple.</span>`,
	} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
	// In an open card, the recommendation comes first, then the options, then the context.
	card := between(html, `id="q-Q90"`, "</details>")
	if i, j, k := strings.Index(card, `class="recom"`), strings.Index(card, `class="options"`), strings.Index(card, `class="context"`); i < 0 || i > j || j > k {
		t.Errorf("a card shows the recommendation, the options, then the context:\n%s", card)
	}

	// Who runs now: the running worker as a card, the finished work folded below.
	running := between(html, `<section id="running">`, "</section>")
	if !strings.Contains(running, "W3") || strings.Count(running, `<article class="worker"`) != 1 {
		t.Errorf("who runs now shows the running worker only:\n%s", running)
	}
	if finished := between(running, `<details class="fold finished">`, "</details>"); !strings.Contains(finished, ">W1<") {
		t.Error("the finished work is folded under the workers")
	}
	// Tasks: waiting and failed in clear; planned folded, with what they wait for.
	tasks := between(html, `<section id="tasks">`, "</section>")
	inClear := between(tasks, "", "<details")
	for _, code := range []string{"W4", "W5"} {
		if !strings.Contains(inClear, ">"+code+"<") {
			t.Errorf("%s shows in clear", code)
		}
	}
	for _, code := range []string{"W1", "W2", "W3"} {
		if strings.Contains(inClear, ">"+code+"<") {
			t.Errorf("%s is not among the tasks in clear", code)
		}
	}
	for _, s := range []string{
		`<details class="fold planned">`, `Planned <span class="n">1</span>`, "after W1, W3", "waits for W3 to be done",
		"exit code 1",
	} {
		if !strings.Contains(tasks, s) {
			t.Errorf("the tasks lack %q", s)
		}
	}

	// Decisions: the 15 latest, the latest first, then the 5 before them folded.
	decisions := between(html, `<section id="decisions">`, "</section>")
	shown, older, _ := strings.Cut(decisions, `<details class="fold older">`)
	if n := strings.Count(shown, `class="at"><time>`); n != shownDecisions {
		t.Errorf("%d decisions shown, want %d", n, shownDecisions)
	}
	if n := strings.Count(older, `class="at"><time>`); n != 5 {
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
	if run := between(notes, `<article class="block series">`, "</article>"); !strings.Contains(run, "<h3>finding: 4 blocks</h3>") ||
		strings.Count(run, "<li><details>") != blockRun+1 {
		t.Errorf("a run of one kind is gathered in one card, a folded line each:\n%s", run)
	}
	if !strings.Contains(notes, `<div class="block-head"><span class="kind">section</span><h3>Short</h3>`) {
		t.Error("a short block shows whole")
	}

	// Journal: the latest first, 10 in sight, the 30 others folded.
	journal := between(html, `<section id="journal">`, "</section>")
	inSight, earlier, _ := strings.Cut(journal, `<details class="fold earlier">`)
	if n := strings.Count(inSight, `<tr><td class="at">`); n != shownJournal {
		t.Errorf("%d journal entries in sight, want %d", n, shownJournal)
	}
	if n := strings.Count(earlier, `<tr><td class="at">`); n != 40-shownJournal {
		t.Errorf("%d journal entries folded, want %d", n, 40-shownJournal)
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

// TestBar: while something waits for the user, a bar holds it, the most urgent first: each blocking question a line,
// the others too while they are few. A task that waits on an open question is in the bar by its question. A task cut
// short is not the user's move: Djinn resumes every one it can by itself, so the bar never asks to start one again.
func TestBar(t *testing.T) {
	html := page(t, Input{Export: rich(t), Unattached: []string{"web"}, Language: "en"})
	bar := between(html, `<aside class="bar"`, "</aside>")
	want := []string{
		`<a class="line bad" href="#q-Q03"><span class="st bad"><i aria-hidden="true">!</i>Blocking</span><span class="what">Q03 · May W2 edit the folder? · Blocks W2</span></a>`,
		`<a class="line wait" href="#q-Q01"><span class="st wait"><i aria-hidden="true">?</i>Waiting for you</span><span class="what">Q01 · Which Markdown library?</span></a>`,
		`<a class="line wait" href="#actions"><span class="st wait"><i aria-hidden="true">?</i>Waiting for you</span><span class="what">Project web is not on this machine</span></a>`,
	}
	last := -1
	for _, s := range want {
		i := strings.Index(bar, s)
		if i < 0 {
			t.Errorf("the bar lacks %q", s)
		} else if i < last {
			t.Errorf("the bar shows %q out of order", s)
		}
		last = i
	}
	if n := strings.Count(bar, `<a class="line`); n != len(want) {
		t.Errorf("the bar holds %d lines, want %d:\n%s", n, len(want), bar)
	}
	if strings.Index(html, `class="bar"`) > strings.Index(html, "<main>") {
		t.Error("the bar comes before the page")
	}

	// Many questions and tasks cut short: the bar keeps to a few lines, and asks nothing about the tasks.
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Many"}}
	for i := range barQuestions + 1 {
		exp.Questions = append(exp.Questions, &planv1.Question{Id: fmt.Sprint(i), Code: fmt.Sprintf("Q%02d", i), Text: "Which?"})
	}
	for i := range 3 {
		exp.Tasks = append(exp.Tasks, &planv1.Task{Id: fmt.Sprint("t", i), Code: fmt.Sprintf("W%d", i), Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED})
	}
	bar = between(page(t, Input{Export: exp, Language: "en"}), `<aside class="bar"`, "</aside>")
	for _, s := range []string{
		`<span class="what">3 questions wait for your answer: Q00, Q01, Q02</span>`,
	} {
		if !strings.Contains(bar, s) {
			t.Errorf("the bar lacks %q:\n%s", s, bar)
		}
	}
	if n := strings.Count(bar, `<a class="line`); n != 1 {
		t.Errorf("the bar holds %d lines, want 1:\n%s", n, bar)
	}
}

// TestResumingNotYourMove: a task Djinn resumes by itself, one waiting for its provider's limit, and one cut short
// but resumed as another task are not the user's move: the bar and the actions leave them, their status says it.
func TestResumingNotYourMove(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Resumes"}, Tasks: []*planv1.Task{
		{Id: "t1", WishId: "w", Code: "W1", Title: "Cut short", Status: planv1.TaskStatus_TASK_STATUS_RESUMING,
			WaitReason: "djinn restarted while the worker ran"},
		{Id: "t2", WishId: "w", Code: "W2", Title: "Limited", Status: planv1.TaskStatus_TASK_STATUS_RESUMING,
			WaitReason: "the account's session limit, resets at 07:20", ResumeAfter: ts(60)},
		{Id: "t3", WishId: "w", Code: "W3", Title: "Taken over", Status: planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
			Error: "djinn up ended while the worker ran"},
		{Id: "t4", WishId: "w", Code: "W4", Title: "Fork", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, ForkOf: "W3"},
	}}
	html := page(t, Input{Export: exp, Language: "en"})
	if bar := between(html, `<aside class="bar"`, "</aside>"); strings.Contains(bar, `<a class="line`) {
		t.Errorf("the bar lists what Djinn does by itself:\n%s", bar)
	}
	if strings.Contains(html, `id="actions"`) && strings.Contains(between(html, `id="actions"`, "</section>"), "W") {
		t.Errorf("the actions list what Djinn does by itself")
	}
	seen := map[string]bool{}
	for _, m := range pill.FindAllStringSubmatch(html, -1) {
		seen[m[1]+" "+m[3]] = true
	}
	for _, s := range []string{"run Resuming", "pause Waiting for the limit", "stop Resumed as W4"} {
		if !seen[s] {
			t.Errorf("the page lacks the pill %q; it has %v", s, seen)
		}
	}
	if strings.Contains(html, "Interrupted") {
		t.Error("a task resumed as another one still reads interrupted")
	}
}

var pill = regexp.MustCompile(`<(?:span|a) class="st ([a-z]+)"[^>]*><i aria-hidden="true">([^<]*)</i>([^<]*?)(?: <b>\d+</b>)?</(?:span|a)>`)

// TestColourLanguage: every status shows its colour, its icon and its word, never its colour alone; the header
// counts the tasks by status.
func TestColourLanguage(t *testing.T) {
	statuses := []planv1.TaskStatus{
		planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_WAITING,
		planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_FAILED,
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_STOPPED,
	}
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Every status", State: planv1.WishState_WISH_STATE_PAUSED}}
	for i, s := range statuses {
		exp.Tasks = append(exp.Tasks, &planv1.Task{Id: fmt.Sprint(i), Code: fmt.Sprintf("W%d", i+1), Title: "A task", Status: s})
	}
	html := page(t, Input{Export: exp, Language: "en"})
	seen := map[string]string{}
	for _, m := range pill.FindAllStringSubmatch(html, -1) {
		class, icon, word := m[1], m[2], m[3]
		if strings.TrimSpace(word) == "" {
			t.Errorf("a %s pill has no word", class)
		}
		if icon != icons[class] {
			t.Errorf("a %s pill shows %q, want its icon %q", class, icon, icons[class])
		}
		seen[class+" "+word] = icon
	}
	for _, s := range []string{
		"ok Done", "run Running", "wait Waiting for you", "idle Planned", "fail Failed", "amber Interrupted",
		"stop Stopped", "pause Paused",
	} {
		if _, ok := seen[s]; !ok {
			t.Errorf("the page lacks the pill %q; it has %v", s, seen)
		}
	}
	for class, icon := range icons {
		if icon == "" && class != "run" {
			t.Errorf("%s has no icon: only a running one shows a dot instead", class)
		}
	}
	counts := between(html, `<ul class="counts"`, "</ul>")
	for _, s := range []string{
		`<a class="st wait" href="#tasks"><i aria-hidden="true">?</i>Waiting for you <b>1</b></a>`,
		`<a class="st run" href="#running"><i aria-hidden="true"></i>Running <b>1</b></a>`,
		`<a class="st ok" href="#running"><i aria-hidden="true">✓</i>Done <b>1</b></a>`,
	} {
		if !strings.Contains(counts, s) {
			t.Errorf("the counts lack %q:\n%s", s, counts)
		}
	}
	if strings.Index(counts, "Waiting for you") > strings.Index(counts, "Done") {
		t.Error("the counts show what needs an eye first")
	}
	for _, class := range []string{"ok", "run", "wait", "amber", "bad", "fail", "idle", "stop", "pause", "later", "human"} {
		if !regexp.MustCompile(`(^|[ ,\n])\.` + class + `[ ,{]`).MatchString(pageCSS) {
			t.Errorf("the style sheet gives no colour to %s", class)
		}
	}
}

var cssVar = regexp.MustCompile(`--([a-z-]+):\s*#([0-9a-f]{6});`)

// TestContrast: each colour of the language reads on its soft colour and on a card, in light and in dark, at 4.5:1
// at least; so do the text and the muted text.
func TestContrast(t *testing.T) {
	light := between(pageCSS, ":root {", "}")
	dark := between(pageCSS, `:root[data-theme="dark"] {`, "}")
	media := between(between(pageCSS, "@media (prefers-color-scheme: dark)", "}\n}"), `:root:not([data-theme="light"]) {`, "}")
	if strings.Join(strings.Fields(strings.SplitN(dark, "{", 2)[1]), " ") != strings.Join(strings.Fields(strings.SplitN(media, "{", 2)[1]), " ") {
		t.Error("the dark theme differs between the system's choice and data-theme")
	}
	for name, block := range map[string]string{"light": light, "dark": dark} {
		colours := map[string]string{}
		for _, m := range cssVar.FindAllStringSubmatch(block, -1) {
			colours[m[1]] = m[2]
		}
		pairs := [][2]string{{"text", "bg"}, {"text", "surface"}, {"muted", "bg"}, {"muted", "surface"}, {"muted", "surface-soft"}}
		for _, s := range []string{"ok", "run", "wait", "amber", "bad", "idle", "pause", "human"} {
			pairs = append(pairs, [2]string{s, s + "-soft"}, [2]string{s, "surface"}, [2]string{s, "bg"})
		}
		for _, p := range pairs {
			fg, bg := colours[p[0]], colours[p[1]]
			if fg == "" || bg == "" {
				t.Errorf("%s: --%s or --%s is not a plain colour", name, p[0], p[1])
				continue
			}
			if c := contrast(fg, bg); c < 4.5 {
				t.Errorf("%s: --%s on --%s is %.2f:1, under 4.5:1", name, p[0], p[1], c)
			}
		}
	}
}

// contrast is the WCAG contrast ratio of two colours, given as six hex digits.
func contrast(a, b string) float64 {
	lum := func(hex string) float64 {
		var rgb [3]float64
		for i := range rgb {
			var v int
			if _, err := fmt.Sscanf(hex[2*i:2*i+2], "%02x", &v); err != nil {
				panic(err) // The colours come from page.css: a test that reads them wrong must stop.
			}
			c := float64(v) / 255
			if c <= 0.04045 {
				rgb[i] = c / 12.92
			} else {
				rgb[i] = math.Pow((c+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
	}
	la, lb := lum(a), lum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// TestEvents: the workers' events a reader follows go to a compact table, the latest first, the older ones folded;
// a worker's card shows its last one. Tool calls stay out.
func TestEvents(t *testing.T) {
	exp := rich(t)
	exp.Events = nil
	kinds := []planv1.TaskEventKind{
		planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, planv1.TaskEventKind_TASK_EVENT_KIND_TEXT,
		planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, planv1.TaskEventKind_TASK_EVENT_KIND_ERROR,
	}
	for i := range shownEvents + 6 {
		exp.Events = append(exp.Events, &planv1.TaskEvent{
			Id: fmt.Sprint(i), TaskId: "t1", Seq: int64(i + 1), Kind: kinds[i%len(kinds)], Text: fmt.Sprintf("event %02d", i),
			CreateTime: ts(i - 30),
		})
	}
	html := page(t, Input{Export: exp, Language: "en"})
	events := between(html, `<section id="events">`, "</section>")
	inSight, earlier, _ := strings.Cut(events, `<details class="fold earlier">`)
	// 16 events, 4 of them tool calls: 12 shown, 10 in sight.
	if n := strings.Count(inSight, `<tr><td class="at">`); n != shownEvents {
		t.Errorf("%d events in sight, want %d", n, shownEvents)
	}
	if n := strings.Count(earlier, `<tr><td class="at">`); n != 2 {
		t.Errorf("%d events folded, want 2", n)
	}
	for _, s := range []string{
		`<span class="kind fail">error</span> event 15`, `<span class="kind idle">said</span> event 13`,
		`<span class="kind run">status</span> event 12`, `<span class="code">W1</span>`, "2026-10-08 14:15",
	} {
		if !strings.Contains(inSight, s) {
			t.Errorf("the events lack %q", s)
		}
	}
	if strings.Contains(events, "event 14") || strings.Contains(events, "event 02") {
		t.Error("tool calls stay out of the events")
	}
	if strings.Index(inSight, "event 15") > strings.Index(inSight, "event 13") {
		t.Error("the latest event comes first")
	}
	// Events of several workers, in the order of their time.
	exp.Events = []*planv1.TaskEvent{
		{Id: "a", TaskId: "t1", Seq: 1, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "W1 early", CreateTime: ts(-20)},
		{Id: "b", TaskId: "t1", Seq: 2, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "W1 late", CreateTime: ts(-2)},
		{Id: "c", TaskId: "t2", Seq: 1, Kind: planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, Text: "W2 between", CreateTime: ts(-10)},
	}
	events = between(page(t, Input{Export: exp, Language: "en"}), `<section id="events">`, "</section>")
	if i, j, k := strings.Index(events, "W1 late"), strings.Index(events, "W2 between"), strings.Index(events, "W1 early"); i > j || j > k {
		t.Errorf("the events of several workers go by time, the latest first:\n%s", events)
	}
	running := between(html, `<section id="running">`, "</section>")
	if !strings.Contains(running, `Last event · <time>2026-10-08 14:15</time></div><p>event 15</p>`) {
		t.Errorf("a worker's card shows its last event:\n%s", running)
	}
}

// TestDiagram: the page runs no script; a Mermaid diagram shows as its source, and the page says so.
func TestDiagram(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "D"}, Blocks: []*planv1.Block{
		{Id: "b", Kind: "diagram", Title: "Flow", Content: "```mermaid\ngraph LR\n  A --> B\n```"},
	}}
	html := page(t, Input{Export: exp, Language: "en"})
	for _, s := range []string{`<code class="language-mermaid">graph LR`, "A Mermaid diagram, shown as its source"} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Error("the page runs no script")
	}
}

// A question the developer asked to look into is the lead's move: it reads investigated, leaves the bar, and
// keeps its rounds, folded.
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
	html := page(t, Input{Export: exp, Language: "en"})
	if n := strings.Count(html, `<i aria-hidden="true">⌕</i>Being investigated</span>`); n != 1 {
		t.Errorf("%d questions shown investigated, want Q01 only", n)
	}
	for _, s := range []string{`class="q dig" id="q-Q01"`, "Revised ×1", `Rounds <span class="n">3</span>`,
		"Asked to investigate: How long does it burn?", "Revised by the lead"} {
		if !strings.Contains(html, s) {
			t.Errorf("the page lacks %q", s)
		}
	}
	bar := between(html, `<aside class="bar"`, "</aside>")
	if strings.Contains(bar, "Q01") || !strings.Contains(bar, "Q02") {
		t.Errorf("the bar should hold Q02, waiting for you, and not Q01, investigated:\n%s", bar)
	}
}

// TestFinishedNewestFirst: the finished tasks show the latest ended first, then the latest created; a task closed by
// hand says who closed it, and why.
func TestFinishedNewestFirst(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Old work"}}
	exp.Tasks = []*planv1.Task{
		{Id: "t1", Code: "W1", Title: "Oldest", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: ts(0), EndTime: ts(10)},
		{Id: "t2", Code: "W2", Title: "Newest", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: ts(1), EndTime: ts(30),
			Closed: &planv1.Closure{Actor: planv1.Closer_CLOSER_DEVELOPER, CreateTime: ts(30), Note: "merged in Git"}},
		{Id: "t3", Code: "W3", Title: "Middle", Status: planv1.TaskStatus_TASK_STATUS_STOPPED, CreateTime: ts(2), EndTime: ts(20)},
		{Id: "t4", Code: "W4", Title: "Same end, created later", Status: planv1.TaskStatus_TASK_STATUS_DONE, CreateTime: ts(3), EndTime: ts(10),
			Closed: &planv1.Closure{Actor: planv1.Closer_CLOSER_LEAD, CreateTime: ts(10)}},
	}
	html := page(t, Input{Export: exp})
	last := -1
	for _, code := range []string{"W2", "W3", "W4", "W1"} {
		i := strings.Index(html, `<span class="code">`+code+`</span>`)
		if i <= last {
			t.Fatalf("%s at %d, after %d: finished tasks out of order", code, i, last)
		}
		last = i
	}
	for _, want := range []string{`<p class="closed">Closed by you: merged in Git</p>`, `<p class="closed">Closed by the lead</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if fr := page(t, Input{Export: exp, Language: "fr"}); !strings.Contains(fr, french(t, "page.closed_by_you", nil)+": merged in Git") {
		t.Error("the French page does not say who closed W2")
	}
}

// TestMovingByStatus: what moves or waits shows by status, as in the window: running before paused, cut short before
// failed before waiting, the planned ones apart.
func TestMovingByStatus(t *testing.T) {
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Busy"}}
	for i, s := range []planv1.TaskStatus{
		planv1.TaskStatus_TASK_STATUS_WAITING, planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_FAILED,
		planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_INTERRUPTED,
	} {
		exp.Tasks = append(exp.Tasks, &planv1.Task{Id: fmt.Sprintf("t%d", i), Code: fmt.Sprintf("W%d", i+1), Title: "A task", Status: s})
	}
	exp.Tasks = append(exp.Tasks,
		&planv1.Task{Id: "t5", Code: "W6", Title: "A watcher", Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Provider: planv1.Provider_PROVIDER_WATCH},
		&planv1.Task{Id: "t6", Code: "W7", Title: "Resuming", Status: planv1.TaskStatus_TASK_STATUS_RESUMING},
	)
	html := page(t, Input{Export: exp})
	last := -1
	// Running, resuming, paused, then a watcher in "Who runs now"; then cut short, failed, waiting in "Tasks".
	for _, code := range []string{"W4", "W7", "W2", "W6", "W5", "W3", "W1"} {
		i := strings.Index(html, `<span class="code">`+code+`</span>`)
		if i <= last {
			t.Fatalf("%s at %d, after %d: tasks out of order", code, i, last)
		}
		last = i
	}
}

// TestWorkStands: each finished task says where its work stands on its way into the wish's integration branch: done
// and waiting to be committed, committed with its short commit, a conflict or red tests and who corrects them; a task
// whose work Djinn does not integrate says only done.
func TestWorkStands(t *testing.T) {
	done := planv1.TaskStatus_TASK_STATUS_DONE
	in := func(state planv1.IntegrationState, reason, by string) *planv1.TaskIntegration {
		return &planv1.TaskIntegration{State: state, Branch: "feat/x", Sha: "1a2b3c4d5e6f", Reason: reason, CorrectedBy: by}
	}
	exp := &planv1.WishExport{Wish: &planv1.Wish{Id: "w", Title: "Integrates"}, Tasks: []*planv1.Task{
		{Id: "t1", WishId: "w", Code: "W1", Title: "Plain", Status: done},
		{Id: "t2", WishId: "w", Code: "W2", Title: "Waits", Status: done, Integration: in(planv1.IntegrationState_INTEGRATION_STATE_PENDING, "", "")},
		{Id: "t3", WishId: "w", Code: "W3", Title: "In", Status: done, Integration: in(planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, "", "")},
		{Id: "t4", WishId: "w", Code: "W4", Title: "Clash", Status: done,
			Integration: in(planv1.IntegrationState_INTEGRATION_STATE_CONFLICT, "W4 conflicts with feat/x in a.go", "W9")},
		{Id: "t5", WishId: "w", Code: "W5", Title: "Red", Status: done, Integration: in(planv1.IntegrationState_INTEGRATION_STATE_RED, "test exited 1", "")},
		{Id: "t6", WishId: "w", Code: "W6", Title: "Merging", Status: done, Integration: in(planv1.IntegrationState_INTEGRATION_STATE_INTEGRATING, "", "")},
	}}
	html := page(t, Input{Export: exp, Language: "en"})
	for code, want := range map[string]string{
		"W2": `<p class="work pause">Done, waiting to be committed</p>`,
		"W3": `<p class="work ok">Committed into feat/x as 1a2b3c4d</p>`,
		"W4": `<p class="work fail">Conflict, not committed: W4 conflicts with feat/x in a.go, corrected by W9</p>`,
		"W5": `<p class="work fail">Red tests, not committed: test exited 1</p>`,
		"W6": `<p class="work run">Being committed into feat/x</p>`,
	} {
		if row := between(html, `<tr id="t-`+code+`">`, "</tr>"); !strings.Contains(row, want) {
			t.Errorf("%s's row lacks %s:\n%s", code, want, row)
		}
	}
	if row := between(html, `<tr id="t-W1">`, "</tr>"); strings.Contains(row, `class="work`) {
		t.Errorf("a task whose work Djinn does not integrate says where it stands:\n%s", row)
	}
	if fr := page(t, Input{Export: exp, Language: "fr"}); !strings.Contains(fr,
		french(t, "work.committed", map[string]string{"branch": "feat/x", "sha": "1a2b3c4d"})) {
		t.Error("the French page does not say where the work stands")
	}
}
