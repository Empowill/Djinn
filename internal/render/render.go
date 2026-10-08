// Package render writes the page of a wish: one HTML file standing alone, made by Djinn from its store, never by a
// model. It shows first what matters now: the open questions, what waits for the user and the workers running. Then
// the tasks (the planned and the finished ones folded), the decisions (the latest first), the free blocks and the
// journal. An empty section is not rendered, and the contents at the top name only the sections present.
//
// The page shows what it is given: the caller strips secrets and local paths first, as an export does.
package render

import (
	"bytes"
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
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
const shownJournal = 30

// shownDecisions is the most decisions shown before the older ones are folded.
const shownDecisions = 15

// longBlock is the size, in characters or lines, from which a block is folded under its title.
const longBlock, longBlockLines = 800, 16

// blockRun is the most blocks of one kind in a row shown whole: a longer run is folded, block by block, to a list
// of titles.
const blockRun = 3

// longNote is the size, in characters, from which the note of a decision is folded.
const longNote = 240

// logKind is the kind of the blocks that tell the story of the wish: they go to the journal, not to the notes.
const logKind = "log"

// maxLastWord is the most of a worker's last text a page shows, in characters.
const maxLastWord = 600

//go:embed page.html.tmpl
var pageTemplate string

//go:embed page.css
var pageCSS string

var tmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(pageCSS) },
	// pair hands a nested template two values: {{template "task" (pair . $.T)}}.
	"pair": func(a, b any) []any { return []any{a, b} },
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
	Contents   []link // the sections present, in their order
	Questions  []question
	Actions    []string
	Running    []task // the workers running now
	Tasks      []task // what needs an eye: running, waiting, cut short, failed
	Planned    []task
	Finished   []task
	TaskCount  int
	Decisions  []decision // the latest ones
	Older      []decision // the decisions before them, folded
	Blocks     []block
	Journal    []entry // the latest entries
	Earlier    []entry // the entries before them, folded
	JournalCut string
}

// link is an entry of the contents: a section of the page and how many things it holds.
type link struct {
	ID, Label, Class string
	Count            int
}

type question struct {
	Code, Text     string
	Context, Recom template.HTML
	// RecomLine is the recommendation's first line, shown on the folded card.
	RecomLine string
	Options   []option
	// Blocking names the tasks that wait for the answer: such a question is open, and comes first.
	Blocking string
}

type option struct{ Letter, Text string }

type decision struct {
	Code, Text, Choice, At string
	Note                   template.HTML
	// A long note is folded, to keep the table compact.
	LongNote bool
}

type task struct {
	Code, Title, Status, StatusClass, Project, Agent, Spent, Time, Error, LastWord string
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
}

// entry is a line of the journal: a command, or a log block.
type entry struct {
	at                   time.Time
	At, Command, Summary string
	Note                 template.HTML
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
		"page.actions", "page.contents", "page.context", "page.decision", "page.decisions", "page.details", "page.earlier",
		"page.finished", "page.journal", "page.last_word", "page.older", "page.options", "page.planned",
		"page.projects", "page.questions", "page.recommendation", "page.running", "page.tasks", "page.notes",
		"page.when", "page.yes_only",
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
			RecomLine: firstLine(q.GetRecommendation(), 160),
		}
		if codes := blocking[q.GetId()]; len(codes) > 0 {
			cq.Blocking = tr("page.blocking", "tasks", strings.Join(codes, ", "))
		}
		for i, o := range q.GetOptions() {
			cq.Options = append(cq.Options, option{Letter: string(rune('A' + i)), Text: o})
		}
		v.Questions = append(v.Questions, cq)
	}
	slices.SortStableFunc(v.Questions, func(a, b question) int {
		return cmp.Compare(b2i(a.Blocking == ""), b2i(b.Blocking == ""))
	})
	slices.SortStableFunc(answered, func(a, b *planv1.Question) int {
		return b.GetAnswer().GetCreateTime().AsTime().Compare(a.GetAnswer().GetCreateTime().AsTime())
	})
	for i, q := range answered {
		d := decision{
			Code: q.GetCode(), Text: q.GetText(), Choice: choice(q, tr), At: at(q.GetAnswer().GetCreateTime()),
			Note: md(q.GetAnswer().GetNote()), LongNote: utf8.RuneCountInString(q.GetAnswer().GetNote()) > longNote,
		}
		if i < shownDecisions {
			v.Decisions = append(v.Decisions, d)
		} else {
			v.Older = append(v.Older, d)
		}
	}

	// What waits for the user, as the lamp knows it: a worker that asks to edit, one that Djinn's stop cut
	// short, a project to bring to this machine.
	for _, t := range exp.GetTasks() {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			if q := questions[t.GetEditQuestionId()]; q != nil && q.GetAnswer() == nil {
				v.Actions = append(v.Actions, tr("page.action_waiting", "task", t.GetCode(), "question", q.GetCode()))
			} else {
				v.Actions = append(v.Actions, tr("page.action_waiting_unknown", "task", t.GetCode()))
			}
		case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			v.Actions = append(v.Actions, tr("page.action_interrupted", "task", t.GetCode()))
		}
	}
	for _, name := range in.Unattached {
		v.Actions = append(v.Actions, tr("page.action_attach", "project", name))
	}
	// Djinn proposes a ready wish; only the user grants it.
	if exp.GetWish().GetReady() {
		v.Actions = append(v.Actions, tr("page.action_ready", "wish", exp.GetWish().GetId()))
	}

	lastWord := map[string]string{}
	for _, e := range exp.GetEvents() {
		if e.GetKind() == planv1.TaskEventKind_TASK_EVENT_KIND_TEXT && strings.TrimSpace(e.GetText()) != "" {
			lastWord[e.GetTaskId()] = e.GetText()
		}
	}
	for _, t := range exp.GetTasks() {
		ct := task{
			Code: t.GetCode(), Title: t.GetTitle(), Project: projects[t.GetProjectId()], Error: t.GetError(),
			LastWord: cut(strings.TrimSpace(lastWord[t.GetId()]), maxLastWord),
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
		v.TaskCount++
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_PENDING:
			v.Planned = append(v.Planned, ct)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED:
			v.Finished = append(v.Finished, ct)
		case planv1.TaskStatus_TASK_STATUS_RUNNING:
			v.Running = append(v.Running, ct)
			v.Tasks = append(v.Tasks, ct)
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			// Its error only says it waits: the status and the actions say it better.
			ct.Error = ""
			v.Tasks = append(v.Tasks, ct)
		default:
			// Failed, cut short, or a status this Djinn does not name yet: in clear.
			v.Tasks = append(v.Tasks, ct)
		}
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
			for i := start; i < end; i++ {
				v.Blocks[i].Long = true
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

// contents lists the sections the page holds, in their order, each with how many things it shows.
func contents(v *view, tr func(string, ...string) string) []link {
	var links []link
	add := func(id, key, class string, n int) {
		if n > 0 {
			links = append(links, link{ID: id, Label: tr(key), Class: class, Count: n})
		}
	}
	add("questions", "page.questions", "ask", len(v.Questions))
	add("actions", "page.actions", "warn", len(v.Actions))
	add("running", "page.running", "run", len(v.Running))
	add("tasks", "page.tasks", "", v.TaskCount)
	add("decisions", "page.decisions", "", len(v.Decisions)+len(v.Older))
	add("notes", "page.notes", "", len(v.Blocks))
	add("journal", "page.journal", "", len(v.Journal)+len(v.Earlier))
	return links
}

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
		return tr("page.state_paused"), "idle"
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

// status names a task's status, with the class that colours it.
func status(s planv1.TaskStatus, tr func(string, ...string) string) (string, string) {
	keys := map[planv1.TaskStatus][2]string{
		planv1.TaskStatus_TASK_STATUS_PENDING:     {"page.status_pending", "idle"},
		planv1.TaskStatus_TASK_STATUS_RUNNING:     {"page.status_running", "run"},
		planv1.TaskStatus_TASK_STATUS_DONE:        {"page.status_done", "ok"},
		planv1.TaskStatus_TASK_STATUS_FAILED:      {"page.status_failed", "bad"},
		planv1.TaskStatus_TASK_STATUS_STOPPED:     {"page.status_stopped", "idle"},
		planv1.TaskStatus_TASK_STATUS_INTERRUPTED: {"page.status_interrupted", "warn"},
		planv1.TaskStatus_TASK_STATUS_WAITING:     {"page.status_waiting", "warn"},
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
