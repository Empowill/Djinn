// Package render writes the page of a wish: one HTML file standing alone, made by Djinn from its store, never by a
// model. It shows first what matters now: a bar that stays in sight while something waits for the user, the open
// questions, what waits for the user and the workers running. Then the tasks that need an eye (the planned ones
// folded), the decisions (the latest first), the free blocks, the journal and the workers' events. An empty section
// is not rendered, and the contents at the top name only the sections present.
//
// One colour language runs through the page, always with an icon and a word: done, running, waiting for you,
// planned, failed, interrupted, paused, stopped; and for what waits, blocking, waiting for you, can wait.
//
// The page shows what it is given: the caller strips secrets and local paths first, as an export does.
package render

import (
	"bytes"
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/locales"
)

// Input is what a page shows.
type Input struct {
	// Export is the wish and all it holds, made portable: no secret, no local path.
	Export *planv1.WishExport
	// Unattached names the wish's projects that have no folder on this machine.
	Unattached []string
	// Version of Djinn, shown in the header.
	Version string
	// Language of the page's own texts; the source language when empty.
	Language string
	// Now is when the page is rendered. Every time on the page is shown in its location.
	Now time.Time
}

// maxJournal is the most journal entries a page shows, the latest ones.
const maxJournal = 200

// shownJournal is the most journal entries shown before the others are folded.
const shownJournal = 10

// maxEvents is the most worker events a page shows, the latest ones; shownEvents are in sight, the others folded.
const maxEvents, shownEvents = 200, 10

// barQuestions is the most questions the bar at the top shows a line each, beside the blocking ones: more share
// one line.
const barQuestions = 2

// shownDecisions is the most decisions shown before the older ones are folded.
const shownDecisions = 15

// longBlock is the size, in characters or lines, from which a block is folded under its title.
const longBlock, longBlockLines = 1500, 30

// blockRun is the most blocks of one kind in a row shown whole: a longer run is gathered in one card, a folded line
// per block, the first shownRun of them in sight.
const blockRun, shownRun = 3, 8

// longNote is the size, in characters, from which the note of a decision is folded.
const longNote = 240

// logKind is the kind of the blocks that tell the story of the wish: they go to the journal, not to the notes.
const logKind = "log"

// maxLastWord is the most of a worker's last event a page shows, in characters; maxEvent, of an event in the table.
const maxLastWord, maxEvent = 600, 280

// icons give each class of the colour language its sign, so that no state is told by colour alone. A running one
// shows a live dot, drawn by the style sheet.
var icons = map[string]string{
	"ok": "✓", "run": "", "wait": "?", "idle": "○", "bad": "!", "fail": "✕", "amber": "↺", "pause": "‖", "stop": "■",
	"later": "◷",
}

//go:embed page.html.tmpl
var pageTemplate string

//go:embed page.css
var pageCSS string

var tmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(pageCSS) },
	// pair hands a nested template two values: {{template "task" (pair . $.T)}}.
	"pair": func(a, b any) []any { return []any{a, b} },
	"icon": func(class string) string { return icons[class] },
}).Parse(pageTemplate))

// markdown renders Markdown as GitHub does, without raw HTML: a tag in the source is left out, and a link to
// javascript:, vbscript:, file: or data: (other than an image) loses its target.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// SystemLanguage is the language of the user's locale, from the environment, or the source language.
func SystemLanguage() string {
	return locales.Match(os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"))
}

// Page renders the page of a wish.
func Page(in Input) ([]byte, error) {
	v, err := build(in)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type view struct {
	Lang       string
	T          map[string]string
	Title      string
	Projects   []string
	Rendered   string
	State      string // where the wish stands: active and its rank, paused, granted
	StateClass string
	// Bar holds what waits for the user, a line each, in sight as the page scrolls.
	Bar       []barLine
	Counts    []count // the tasks by status
	Contents  []link  // the sections present, in their order
	Questions []question
	Actions   []action
	Running   []task // the workers running now
	Finished  []task // the work finished, folded under them
	Tasks     []task // what needs an eye: waiting, cut short, failed
	Planned   []task
	Decisions []decision // the latest ones
	Older     []decision // the decisions before them, folded
	Blocks    []block
	Notes     []notes // the blocks as shown: alone, or gathered in a run of one kind
	Journal   []entry // the latest entries
	Earlier   []entry // the entries before them, folded
	// JournalCut says how many entries the page left out.
	JournalCut string
	Events     []event // the workers' latest events
	EarlierEv  []event // the events before them, folded
}

// link is an entry of the contents: a section of the page and how many things it holds.
type link struct {
	ID, Label, Class string
	Count            int
}

// count is how many tasks have a status, with the section that shows them.
type count struct {
	Class, Label, Href string
	Count              int
}

// barLine is a line of the bar at the top: how urgent, in a class and a word, and what, pointing to its place.
type barLine struct{ Class, Level, Text, Href string }

// action is what only the user can do, with how urgent it is.
type action struct{ Class, Level, Text string }

type question struct {
	Code, Text     string
	Context, Recom template.HTML
	// RecomLine is the recommendation's first line, shown on the folded card.
	RecomLine string
	Options   []option
	// Blocking names the tasks that wait for the answer: such a question is open, and comes first.
	Blocking string
	// Class and Level say how urgent it is: blocking, or waiting for the user.
	Class, Level string
	// Open questions are unfolded: a blocking one, or the only one.
	Open bool
}

type option struct{ Letter, Text string }

type decision struct {
	Code, Text, Choice, At string
	Note                   template.HTML
	// A long note is folded under its first line, to keep the table compact.
	LongNote bool
	NoteLine string
}

type task struct {
	Code, Title, Status, StatusClass, Project, Agent, Spent, Time, Error string
	// LastWord is the worker's last event, at LastAt.
	LastWord, LastAt string
	// After names the tasks this one waits to be done; Wait says why a planned task has not started yet.
	After, Wait string
}

type block struct {
	Kind, Title, Task, Updated string
	Markdown                   bool
	HTML                       template.HTML
	Text                       string
	// Long blocks are folded under their title.
	Long bool
	// Diagram is set when the block holds a Mermaid diagram: the page, with no script, shows its source.
	Diagram bool
}

// notes is a block shown alone, or a run of blocks of one kind gathered in one card, a folded line each.
type notes struct {
	Block       block
	Title, Kind string
	Run, More   []block // the blocks of a run in sight, and the others, folded
}

// entry is a line of the journal: a command, or a log block.
type entry struct {
	at                   time.Time
	At, Command, Summary string
	Note                 template.HTML
}

// event is a line of the workers' events: what a worker said, a change of its status, an error.
type event struct {
	at                          time.Time
	At, Task, Kind, Class, Text string
}

func build(in Input) (*view, error) {
	exp := in.Export
	if exp.GetWish() == nil {
		return nil, fmt.Errorf("render: no wish")
	}
	lang := in.Language
	if lang == "" {
		lang = locales.Source
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	loc := now.Location()
	at := func(t interface{ AsTime() time.Time }) string { return t.AsTime().In(loc).Format("2006-01-02 15:04") }
	tr := func(key string, params ...string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(params); i += 2 {
			m[params[i]] = params[i+1]
		}
		return locales.T(lang, key, m)
	}
	v := &view{Lang: lang, Title: exp.GetWish().GetTitle(), T: map[string]string{}}
	for _, key := range []string{
		"page.actions", "page.actions_sub", "page.after_col", "page.bar", "page.contents", "page.context",
		"page.counts", "page.decision", "page.decisions", "page.decisions_sub", "page.diagram", "page.earlier",
		"page.earlier_events", "page.events", "page.events_sub", "page.finished", "page.journal", "page.journal_sub",
		"page.last_event", "page.more", "page.none_running", "page.notes", "page.notes_sub", "page.older", "page.options",
		"page.planned", "page.projects", "page.questions", "page.questions_sub", "page.recommendation",
		"page.running", "page.running_sub", "page.spent", "page.status", "page.task", "page.tasks", "page.tasks_sub",
		"page.took", "page.wait_col", "page.what", "page.when", "page.why", "page.yes_only",
	} {
		v.T[key] = tr(key)
	}
	v.Rendered = tr("page.rendered", "version", cmp.Or(in.Version, "dev"), "time", now.In(loc).Format("2006-01-02 15:04 MST"))
	v.State, v.StateClass = state(exp.GetWish(), at, tr)

	projects := map[string]string{}
	for _, p := range exp.GetProjects() {
		projects[p.GetId()] = p.GetName()
	}
	for _, id := range exp.GetWish().GetProjectIds() {
		if name := projects[id]; name != "" {
			v.Projects = append(v.Projects, name)
		}
	}
	questions := map[string]*planv1.Question{}
	for _, q := range exp.GetQuestions() {
		questions[q.GetId()] = q
	}
	tasks := map[string]*planv1.Task{}
	for _, t := range exp.GetTasks() {
		tasks[t.GetId()] = t
	}

	// A question a waiting task needs answered blocks it: it comes first, open.
	blocking := map[string][]string{}
	for _, t := range exp.GetTasks() {
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_WAITING && t.GetEditQuestionId() != "" {
			blocking[t.GetEditQuestionId()] = append(blocking[t.GetEditQuestionId()], t.GetCode())
		}
	}

	// Open questions, the blocking ones first, then in the order they were asked. Decisions, the latest first.
	var answered []*planv1.Question
	for _, q := range exp.GetQuestions() {
		if q.GetAnswer() != nil {
			answered = append(answered, q)
			continue
		}
		cq := question{
			Code: q.GetCode(), Text: q.GetText(), Context: md(q.GetContext()), Recom: md(q.GetRecommendation()),
			Class: "wait", Level: tr("page.status_waiting"),
		}
		if line := firstLine(q.GetRecommendation(), 160); line != "" {
			cq.RecomLine = tr("page.recommended", "text", line)
		}
		if codes := blocking[q.GetId()]; len(codes) > 0 {
			cq.Blocking = tr("page.blocking", "tasks", strings.Join(codes, ", "))
			cq.Class, cq.Level, cq.Open = "bad", cq.Blocking, true
		}
		for i, o := range q.GetOptions() {
			cq.Options = append(cq.Options, option{Letter: string(rune('A' + i)), Text: o})
		}
		v.Questions = append(v.Questions, cq)
	}
	slices.SortStableFunc(v.Questions, func(a, b question) int {
		return cmp.Compare(b2i(a.Blocking == ""), b2i(b.Blocking == ""))
	})
	if len(v.Questions) == 1 {
		v.Questions[0].Open = true
	}
	slices.SortStableFunc(answered, func(a, b *planv1.Question) int {
		return b.GetAnswer().GetCreateTime().AsTime().Compare(a.GetAnswer().GetCreateTime().AsTime())
	})
	for i, q := range answered {
		d := decision{
			Code: q.GetCode(), Text: q.GetText(), Choice: choice(q, tr), At: at(q.GetAnswer().GetCreateTime()),
			Note: md(q.GetAnswer().GetNote()), LongNote: utf8.RuneCountInString(q.GetAnswer().GetNote()) > longNote,
			NoteLine: cmp.Or(firstLine(q.GetAnswer().GetNote(), 140), tr("page.details")),
		}
		if i < shownDecisions {
			v.Decisions = append(v.Decisions, d)
		} else {
			v.Older = append(v.Older, d)
		}
	}

	// The bar: each blocking question a line; the other questions too while they are few, else one line for them all.
	waiting, blocked, later := tr("page.status_waiting"), tr("page.level_blocking"), tr("page.level_later")
	var others []string
	for _, q := range v.Questions {
		if q.Blocking != "" {
			v.Bar = append(v.Bar, barLine{
				Class: q.Class, Level: blocked, Text: q.Code + " · " + cut(q.Text, 140) + " · " + q.Blocking, Href: "#q-" + q.Code,
			})
		} else {
			others = append(others, q.Code)
		}
	}
	if len(others) > barQuestions {
		v.Bar = append(v.Bar, barLine{Class: "wait", Level: waiting, Href: "#questions",
			Text: tr("page.bar_questions", "count", fmt.Sprint(len(others)), "codes", strings.Join(others, ", "))})
	} else {
		for _, q := range v.Questions[len(v.Questions)-len(others):] {
			v.Bar = append(v.Bar, barLine{Class: q.Class, Level: waiting, Text: q.Code + " · " + cut(q.Text, 140), Href: "#q-" + q.Code})
		}
	}

	// What waits for the user, as the lamp knows it: a worker that asks to edit, one that Djinn's stop cut
	// short, a project to bring to this machine. A worker that waits on an open question is in the bar by its
	// question already.
	addAction := func(class, level, text, bar string) {
		v.Actions = append(v.Actions, action{Class: class, Level: level, Text: text})
		if bar != "" {
			v.Bar = append(v.Bar, barLine{Class: class, Level: level, Text: bar, Href: "#actions"})
		}
	}
	var cutShort []string
	for _, t := range exp.GetTasks() {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			if q := questions[t.GetEditQuestionId()]; q != nil && q.GetAnswer() == nil {
				addAction("bad", blocked, tr("page.action_waiting", "task", t.GetCode(), "question", q.GetCode()), "")
			} else {
				addAction("bad", blocked, tr("page.action_waiting_unknown", "task", t.GetCode()),
					tr("page.bar_waiting", "task", t.GetCode()))
			}
		case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			addAction("wait", waiting, tr("page.action_interrupted", "task", t.GetCode()), "")
			cutShort = append(cutShort, t.GetCode())
		}
	}
	if len(cutShort) > 0 {
		v.Bar = append(v.Bar, barLine{Class: "wait", Level: waiting, Href: "#actions",
			Text: tr("page.bar_interrupted", "tasks", strings.Join(cutShort, ", "))})
	}
	for _, name := range in.Unattached {
		addAction("wait", waiting, tr("page.action_attach", "project", name), tr("page.bar_attach", "project", name))
	}
	// Djinn proposes a ready wish; only the user grants it.
	if exp.GetWish().GetReady() {
		addAction("later", later, tr("page.action_ready", "wish", exp.GetWish().GetId()), tr("page.bar_ready"))
	}
	// The most urgent lines first; the questions keep their order among them.
	slices.SortStableFunc(v.Bar, func(a, b barLine) int { return cmp.Compare(urgency[a.Class], urgency[b.Class]) })
	slices.SortStableFunc(v.Actions, func(a, b action) int { return cmp.Compare(urgency[a.Class], urgency[b.Class]) })

	// The workers' events a reader follows: what they said, a change of status, an error. Tool calls stay out.
	var events []event
	type last struct{ text, at string }
	lastWord := map[string]last{}
	for _, e := range exp.GetEvents() {
		text := strings.TrimSpace(e.GetText())
		kind, class := "", ""
		switch e.GetKind() {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT:
			kind, class = tr("page.event_text"), "idle"
		case planv1.TaskEventKind_TASK_EVENT_KIND_STATUS:
			kind, class = tr("page.event_status"), "run"
		case planv1.TaskEventKind_TASK_EVENT_KIND_ERROR:
			kind, class = tr("page.event_error"), "fail"
		}
		if kind == "" || text == "" {
			continue
		}
		when := ""
		if e.GetCreateTime() != nil {
			when = at(e.GetCreateTime())
		}
		lastWord[e.GetTaskId()] = last{text, when}
		events = append(events, event{at: e.GetCreateTime().AsTime(), At: when, Task: tasks[e.GetTaskId()].GetCode(), Kind: kind, Class: class, Text: cut(text, maxEvent)})
	}
	// The latest first; events of one time keep the order they came in.
	slices.Reverse(events)
	slices.SortStableFunc(events, func(a, b event) int { return b.at.Compare(a.at) })
	events = events[:min(len(events), maxEvents)]
	v.Events, v.EarlierEv = events[:min(len(events), shownEvents)], events[min(len(events), shownEvents):]

	byStatus := map[planv1.TaskStatus]int{}
	for _, t := range exp.GetTasks() {
		ct := task{
			Code: t.GetCode(), Title: t.GetTitle(), Project: projects[t.GetProjectId()], Error: t.GetError(),
			LastWord: cut(lastWord[t.GetId()].text, maxLastWord), LastAt: lastWord[t.GetId()].at,
		}
		ct.Status, ct.StatusClass = status(t.GetStatus(), tr)
		var after []string
		for _, id := range t.GetDependsOn() {
			if d := tasks[id]; d != nil {
				after = append(after, d.GetCode())
			}
		}
		if len(after) > 0 {
			ct.After = tr("page.after", "tasks", strings.Join(after, ", "))
		}
		ct.Wait = t.GetWaitReason()
		ct.Agent = strings.ToLower(strings.TrimPrefix(t.GetProvider().String(), "PROVIDER_"))
		if t.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
			ct.Agent = "claude"
		}
		if m := t.GetModel(); m != "" {
			ct.Agent += " · " + m
		}
		if c := t.GetUsage().GetCostUsd(); c > 0 {
			ct.Spent = fmt.Sprintf("$%.2f", c)
		}
		// A page is rendered on a change, not by the clock: a running task shows when it started, an ended one how
		// long it ran.
		if s, e := t.GetStartTime(), t.GetEndTime(); s != nil && e != nil && t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
			ct.Time = duration(e.AsTime().Sub(s.AsTime()))
		} else if s != nil {
			ct.Time = tr("page.started", "time", at(s))
		}
		byStatus[t.GetStatus()]++
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_PENDING:
			v.Planned = append(v.Planned, ct)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED:
			v.Finished = append(v.Finished, ct)
		case planv1.TaskStatus_TASK_STATUS_RUNNING:
			v.Running = append(v.Running, ct)
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			// Its error only says it waits: the status and the actions say it better.
			ct.Error = ""
			v.Tasks = append(v.Tasks, ct)
		default:
			// Failed, cut short, or a status this Djinn does not name yet: in clear.
			v.Tasks = append(v.Tasks, ct)
		}
	}
	// The tasks by status, what needs an eye first, each pointing to the section that shows it.
	for _, s := range []planv1.TaskStatus{
		planv1.TaskStatus_TASK_STATUS_WAITING, planv1.TaskStatus_TASK_STATUS_FAILED,
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_RUNNING,
		planv1.TaskStatus_TASK_STATUS_PENDING, planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED,
	} {
		if n := byStatus[s]; n > 0 {
			delete(byStatus, s)
			label, class := status(s, tr)
			href := "#tasks"
			if s == planv1.TaskStatus_TASK_STATUS_RUNNING || s == planv1.TaskStatus_TASK_STATUS_DONE ||
				s == planv1.TaskStatus_TASK_STATUS_STOPPED {
				href = "#running"
			}
			v.Counts = append(v.Counts, count{Class: class, Label: label, Href: href, Count: n})
		}
	}
	// A status this Djinn does not name yet, last.
	for _, s := range slices.Sorted(maps.Keys(byStatus)) {
		label, class := status(s, tr)
		v.Counts = append(v.Counts, count{Class: class, Label: label, Href: "#tasks", Count: byStatus[s]})
	}

	var journal []entry
	var kinds []string // the kind of each block shown, to find the runs
	for _, b := range exp.GetBlocks() {
		if strings.EqualFold(b.GetKind(), logKind) {
			e := entry{at: b.GetCreateTime().AsTime(), At: at(b.GetCreateTime()), Summary: b.GetTitle()}
			if content := strings.TrimSpace(b.GetContent()); content != "" && isMarkdown(b.GetMediaType()) {
				e.Summary, e.Note = "", md(content)
			} else if content != "" {
				e.Summary = cut(content, 600)
			}
			if e.Summary != "" || e.Note != "" {
				journal = append(journal, e)
			}
			continue
		}
		cb := block{Kind: b.GetKind(), Title: b.GetTitle(), Updated: at(b.GetUpdateTime())}
		if t := tasks[b.GetTaskId()]; t != nil {
			cb.Task = tr("page.about_task", "task", t.GetCode())
		}
		if isMarkdown(b.GetMediaType()) {
			cb.Markdown, cb.HTML = true, md(b.GetContent())
			cb.Diagram = strings.Contains(string(cb.HTML), `<code class="language-mermaid">`)
		} else {
			cb.Text = b.GetContent()
		}
		if cb.Title == "" && cb.HTML == "" && cb.Text == "" {
			continue
		}
		// Every block shows under a title: its own, or its first line.
		if cb.Title == "" {
			cb.Title = cmp.Or(firstLine(b.GetContent(), 80), cb.Kind)
		}
		cb.Long = utf8.RuneCountInString(b.GetContent()) > longBlock || strings.Count(b.GetContent(), "\n") >= longBlockLines
		v.Blocks = append(v.Blocks, cb)
		kinds = append(kinds, strings.ToLower(b.GetKind()))
	}
	for start := 0; start < len(kinds); {
		end := start + 1
		for end < len(kinds) && kinds[end] == kinds[start] {
			end++
		}
		if kinds[start] != "" && end-start > blockRun {
			run, n := v.Blocks[start:end], min(end-start, shownRun)
			v.Notes = append(v.Notes, notes{
				Title: tr("page.run", "kind", run[0].Kind, "count", fmt.Sprint(len(run))), Kind: run[0].Kind,
				Run: run[:n], More: run[n:],
			})
		} else {
			for _, b := range v.Blocks[start:end] {
				v.Notes = append(v.Notes, notes{Block: b})
			}
		}
		start = end
	}

	// The journal: the commands and the log blocks, the latest first.
	for _, c := range exp.GetCommands() {
		journal = append(journal, entry{
			at: c.GetAt().AsTime(), At: at(c.GetAt()), Command: command(c.GetMethod()),
			Summary: cut(summary(c, questions, tasks, projects, tr), 200),
		})
	}
	slices.SortStableFunc(journal, func(a, b entry) int { return b.at.Compare(a.at) })
	if n := len(journal) - maxJournal; n > 0 {
		v.JournalCut = tr("page.journal_cut", "count", fmt.Sprint(n))
		journal = journal[:maxJournal]
	}
	v.Journal, v.Earlier = journal[:min(len(journal), shownJournal)], journal[min(len(journal), shownJournal):]

	v.Contents = contents(v, tr)
	return v, nil
}

// contents lists the sections the page holds, in their order, each with how many things it shows, coloured as the
// most urgent of them.
func contents(v *view, tr func(string, ...string) string) []link {
	var links []link
	add := func(id, key, class string, n int, present bool) {
		if present {
			links = append(links, link{ID: id, Label: tr(key), Class: class, Count: n})
		}
	}
	first := func(class string, ok bool) string {
		if ok {
			return class
		}
		return ""
	}
	if len(v.Questions) > 0 {
		add("questions", "page.questions", v.Questions[0].Class, len(v.Questions), true)
	}
	if len(v.Actions) > 0 {
		add("actions", "page.actions", v.Actions[0].Class, len(v.Actions), true)
	}
	add("running", "page.running", first("run", len(v.Running) > 0), len(v.Running), len(v.Running)+len(v.Finished) > 0)
	add("tasks", "page.tasks", "", len(v.Tasks)+len(v.Planned), len(v.Tasks)+len(v.Planned) > 0)
	add("decisions", "page.decisions", "", len(v.Decisions)+len(v.Older), len(v.Decisions) > 0)
	add("notes", "page.notes", "", len(v.Blocks), len(v.Blocks) > 0)
	add("journal", "page.journal", "", len(v.Journal)+len(v.Earlier), len(v.Journal) > 0)
	add("events", "page.events", "", len(v.Events)+len(v.EarlierEv), len(v.Events) > 0)
	return links
}

// urgency orders what waits for the user: blocking first, then what waits, then what can wait.
var urgency = map[string]int{"bad": 0, "wait": 1, "later": 2}

// firstLine is the first line of Markdown text, as plain text, shortened to n characters.
func firstLine(source string, n int) string {
	for line := range strings.Lines(source) {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-*+ ")
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		if line = strings.TrimSpace(line); line != "" {
			return cut(line, n)
		}
	}
	return ""
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// md renders Markdown, without raw HTML.
func md(source string) template.HTML {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	var b bytes.Buffer
	if err := markdown.Convert([]byte(source), &b); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(source) + "</pre>")
	}
	return template.HTML(b.String()) //nolint:gosec // goldmark leaves raw HTML and dangerous links out.
}

func isMarkdown(mediaType string) bool {
	base, _, _ := strings.Cut(mediaType, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	return base == "" || base == "text/markdown"
}

func choice(q *planv1.Question, tr func(string, ...string) string) string {
	c := q.GetAnswer().GetChoice()
	if c == planv1.Choice_CHOICE_YES {
		return tr("page.yes")
	}
	i := int(c - planv1.Choice_CHOICE_A)
	if i < 0 || i >= len(q.GetOptions()) {
		return strings.ToUpper(strings.TrimPrefix(c.String(), "CHOICE_"))
	}
	return fmt.Sprintf("%c · %s", 'A'+i, q.GetOptions()[i])
}

// state says where a wish stands, with the class that colours it: active and its rank, paused, or granted and when.
func state(w *planv1.Wish, at func(interface{ AsTime() time.Time }) string, tr func(string, ...string) string) (string, string) {
	switch w.GetState() {
	case planv1.WishState_WISH_STATE_PAUSED:
		return tr("page.state_paused"), "pause"
	case planv1.WishState_WISH_STATE_GRANTED:
		if w.GetGrantTime() != nil {
			return tr("page.state_granted_at", "time", at(w.GetGrantTime())), "ok"
		}
		return tr("page.state_granted"), "ok"
	}
	if w.GetRank() > 0 {
		return tr("page.state_ranked", "rank", fmt.Sprint(w.GetRank())), "run"
	}
	return tr("page.state_active"), "run"
}

// status names a task's status, with the class that colours it and gives its icon.
func status(s planv1.TaskStatus, tr func(string, ...string) string) (string, string) {
	keys := map[planv1.TaskStatus][2]string{
		planv1.TaskStatus_TASK_STATUS_PENDING:     {"page.status_pending", "idle"},
		planv1.TaskStatus_TASK_STATUS_RUNNING:     {"page.status_running", "run"},
		planv1.TaskStatus_TASK_STATUS_DONE:        {"page.status_done", "ok"},
		planv1.TaskStatus_TASK_STATUS_FAILED:      {"page.status_failed", "fail"},
		planv1.TaskStatus_TASK_STATUS_STOPPED:     {"page.status_stopped", "stop"},
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED: {"page.status_interrupted", "amber"},
		planv1.TaskStatus_TASK_STATUS_WAITING:     {"page.status_waiting", "wait"},
	}
	if k, ok := keys[s]; ok {
		return tr(k[0]), k[1]
	}
	// A status this Djinn does not name yet shows as the lamp spells it.
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(s.String(), "TASK_STATUS_"), "_", " ")), "idle"
}

// command spells a journal method as the command line does: /plan.v1.QuestionService/Answer is question answer.
func command(method string) string {
	service, name, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !ok {
		return method
	}
	if i := strings.LastIndexByte(service, '.'); i >= 0 {
		service = service[i+1:]
	}
	return kebab(strings.TrimSuffix(service, "Service")) + " " + kebab(name)
}

func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// summary says in a line what a command of the journal did, from its request.
func summary(
	c *planv1.Command, questions map[string]*planv1.Question, tasks map[string]*planv1.Task, projects map[string]string,
	tr func(string, ...string) string,
) string {
	req, err := c.GetRequest().UnmarshalNew()
	if err != nil {
		return ""
	}
	taskCode := func(id string) string { return tasks[id].GetCode() }
	switch m := req.(type) {
	case *planv1.WishServiceMakeRequest:
		return m.GetTitle()
	case *planv1.QuestionServiceAskRequest:
		return m.GetText()
	case *planv1.QuestionServiceAnswerRequest:
		code := m.GetQuestion().GetCode()
		if q := questions[m.GetQuestion().GetId()]; q != nil {
			code = q.GetCode()
		}
		return strings.TrimSpace(code + " " + strings.ToLower(strings.TrimPrefix(m.GetChoice().String(), "CHOICE_")) +
			" " + m.GetNote())
	case *planv1.BlockServicePutRequest:
		return strings.TrimSpace(m.GetKind() + " " + m.GetTitle())
	case *planv1.TaskServiceSpawnRequest:
		return m.GetTitle()
	case *planv1.WishServiceAllowRequest:
		return strings.TrimSpace(strings.ToLower(strings.TrimPrefix(m.GetMode().String(), "ALLOWANCE_")) + " " +
			projects[m.GetProjectId()])
	case *planv1.WishServiceMoveRequest:
		return fmt.Sprint(m.GetTo())
	case interface{ GetTaskId() string }:
		return taskCode(m.GetTaskId())
	}
	return ""
}

// cut shortens s to at most n characters, with an ellipsis.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
}
