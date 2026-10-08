// Package render writes the page of a wish: one HTML file standing alone, made by Djinn from its store, never by a
// model. Open questions come first, then what waits for the user, the tasks, the decisions, the free blocks and the
// journal. An empty section is not rendered.
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
	Questions  []question
	Actions    []string
	Active     []task
	Finished   []task
	Decisions  []decision
	Blocks     []block
	Journal    []entry
	JournalCut string
}

type question struct {
	Code, Text     string
	Context, Recom template.HTML
	Options        []option
}

type option struct{ Letter, Text string }

type decision struct {
	Code, Text, Choice, At string
	Note                   template.HTML
}

type task struct {
	Code, Title, Status, StatusClass, Project, Agent, Spent, Time, Error, LastWord string
}

type block struct {
	Kind, Title, Task, Updated string
	Markdown                   bool
	HTML                       template.HTML
	Text                       string
}

type entry struct{ At, Command, Summary string }

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
		"page.actions", "page.context", "page.decisions", "page.finished", "page.journal", "page.last_word",
		"page.options", "page.projects", "page.questions", "page.recommendation", "page.tasks", "page.notes",
		"page.yes_only",
	} {
		v.T[key] = tr(key)
	}
	v.Rendered = tr("page.rendered", "version", cmp.Or(in.Version, "dev"), "time", now.In(loc).Format("2006-01-02 15:04 MST"))

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

	// Open questions, oldest first: the order they were asked in. Decisions, the latest first.
	var answered []*planv1.Question
	for _, q := range exp.GetQuestions() {
		if q.GetAnswer() != nil {
			answered = append(answered, q)
			continue
		}
		cq := question{Code: q.GetCode(), Text: q.GetText(), Context: md(q.GetContext()), Recom: md(q.GetRecommendation())}
		for i, o := range q.GetOptions() {
			cq.Options = append(cq.Options, option{Letter: string(rune('A' + i)), Text: o})
		}
		v.Questions = append(v.Questions, cq)
	}
	slices.SortStableFunc(answered, func(a, b *planv1.Question) int {
		return b.GetAnswer().GetCreateTime().AsTime().Compare(a.GetAnswer().GetCreateTime().AsTime())
	})
	for _, q := range answered {
		v.Decisions = append(v.Decisions, decision{
			Code: q.GetCode(), Text: q.GetText(), Choice: choice(q, tr), At: at(q.GetAnswer().GetCreateTime()),
			Note: md(q.GetAnswer().GetNote()),
		})
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
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED:
			v.Finished = append(v.Finished, ct)
		case planv1.TaskStatus_TASK_STATUS_WAITING:
			// Its error only says it waits: the status and the actions say it better.
			ct.Error = ""
			v.Active = append(v.Active, ct)
		default:
			v.Active = append(v.Active, ct)
		}
	}

	for _, b := range exp.GetBlocks() {
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
		v.Blocks = append(v.Blocks, cb)
	}

	commands := exp.GetCommands()
	if n := len(commands) - maxJournal; n > 0 {
		v.JournalCut = tr("page.journal_cut", "count", fmt.Sprint(n))
		commands = commands[n:]
	}
	for i := len(commands) - 1; i >= 0; i-- {
		c := commands[i]
		v.Journal = append(v.Journal, entry{
			At: at(c.GetAt()), Command: command(c.GetMethod()), Summary: cut(summary(c, questions, tasks, projects, tr), 200),
		})
	}
	return v, nil
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
	case *planv1.WishServiceGrantRequest:
		return strings.TrimSpace(strings.ToLower(strings.TrimPrefix(m.GetMode().String(), "GRANT_")) + " " +
			projects[m.GetProjectId()])
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
