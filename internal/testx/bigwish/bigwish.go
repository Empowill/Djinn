// Package bigwish makes a wish of real size, to measure Djinn on: its azimas and tasks, the events of their workers,
// its questions and decisions, its blocks and its journal, as a wish export (WishService.Import reads it). It is test
// data: the same size always gives the same bytes (a fixed seed, no clock, no network).
//
// A Go test or benchmark loads it into a store through the import, as a person would:
//
//	data, _ := bigwish.Data(bigwish.Real)
//	(&plan.Wishes{Store: s}).ImportData(ctx, connect.NewRequest(&planv1.WishServiceImportDataRequest{Data: data}))
//
// The package never imports internal/plan, so that plan's own tests can use it. The e2e writes the file with
// tools/bigwish and imports it with djinn wish import (e2e/bigwish.ts).
package bigwish

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	machinev1 "github.com/empowill/djinn/gen/go/machine/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
)

// Size says how much a wish holds.
type Size struct {
	// Name, as tools/bigwish and the e2e take it: real, x10.
	Name string
	// Work tasks (W1…) and azimas (T01…); every work task but a few is part of an azima.
	Work, Azimas int
	// Questions (Q00…), of which the first Decisions are answered; at most 1000, the codes having three digits.
	Questions, Decisions int
	// Blocks, of every kind the lead writes: log, decision, report, section, diagram.
	Blocks int
	// Entries of the journal (WishExport.commands).
	Commands int
	// Events of a task its worker ran, on average: a pending task has none.
	EventsPerTask int
}

// Real is the size of the developer's own wish, Djinn sur Wails, as its export read on 2026-10-10: 180 work tasks,
// 14 azimas (28 here, the count the developer gave for its plan), 62 questions of which 61 decided, 253 blocks, 964
// commands and 26 738 events (about 160 per task that ran).
var Real = Size{
	Name: "real", Work: 180, Azimas: 28, Questions: 62, Decisions: 61, Blocks: 253, Commands: 964, EventsPerTask: 160,
}

// X10 is Real ten times over, the events per task aside: ten times the tasks makes ten times the events.
var X10 = Size{
	Name: "x10", Work: 1800, Azimas: 280, Questions: 620, Decisions: 610, Blocks: 2530, Commands: 9640,
	EventsPerTask: 160,
}

// Sizes are the named sizes, the smallest first.
func Sizes() []Size { return []Size{Real, X10} }

// Named returns the size of that name.
func Named(name string) (Size, error) {
	var names []string
	for _, s := range Sizes() {
		if s.Name == name {
			return s, nil
		}
		names = append(names, s.Name)
	}
	return Size{}, fmt.Errorf("no size %q: %s", name, strings.Join(names, ", "))
}

// WishID is the wish's identifier, the same at every size: import one size with --replace over another.
const WishID = "01a11833-a440-7000-8000-00000b16b16b"

// seed makes every wish of a size the same.
const seed = 61

// start is when the wish was made; everything else follows it.
var start = time.Date(2026, 10, 7, 21, 10, 0, 0, time.UTC)

// Data is the wish of that size, as the binary file djinn wish export writes.
func Data(size Size) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(Make(size))
}

// Make makes the wish of that size.
func Make(size Size) *planv1.WishExport {
	if size.Questions > 1000 || size.Decisions > size.Questions {
		panic(fmt.Sprintf("bigwish: %d questions and %d decisions: at most 1000, decisions among them", size.Questions, size.Decisions))
	}
	// A seeded generator, on purpose: the same size gives the same wish.
	g := &gen{size: size, rand: rand.New(rand.NewPCG(seed, uint64(size.Work))), now: start} //nolint:gosec // Test data, not a secret.
	g.wish()
	g.plan()
	g.tasks()
	g.questions()
	g.blocks()
	g.commands()
	return g.exp
}

type gen struct {
	size    Size
	rand    *rand.Rand
	exp     *planv1.WishExport
	project string
	// now is the clock of the wish, which every object moves forward; seq tells apart the ids of one millisecond.
	now time.Time
	seq uint16
	// azimas and work are the tasks made, by kind.
	azimas, work []*planv1.Task
}

// tick moves the clock by about d and gives the time.
func (g *gen) tick(d time.Duration) *timestamppb.Timestamp {
	g.now = g.now.Add(d/2 + time.Duration(g.rand.Int64N(int64(d)+1)))
	return timestamppb.New(g.now)
}

// id is a UUIDv7 at the clock's time, as store.NewID makes them: the store lists objects in the order they were made.
func (g *gen) id() string {
	var u uuid.UUID
	ms := uint64(g.now.UnixMilli())
	binary.BigEndian.PutUint64(u[8:], g.rand.Uint64())
	u[0], u[1], u[2], u[3], u[4], u[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	g.seq++
	u[6], u[7] = 0x70|byte(g.seq>>8&0x0f), byte(g.seq)
	u[8] = u[8]&0x3f | 0x80
	return u.String()
}

func (g *gen) wish() {
	g.project = g.id()
	g.exp = &planv1.WishExport{
		Version:    1,
		CreateTime: timestamppb.New(start),
		Wish: &planv1.Wish{
			Id: WishID, Title: "A wish of real size (" + g.size.Name + ")", ProjectIds: []string{g.project},
			CreateTime:          timestamppb.New(start),
			State:               planv1.WishState_WISH_STATE_ACTIVE,
			Description:         g.text(40),
			Lead:                &planv1.Lead{Provider: planv1.Provider_PROVIDER_CLAUDE, SessionId: g.id(), Directory: "bigwish"},
			IntegrationBranches: []*planv1.IntegrationBranch{{ProjectId: g.project, Branch: "feat/bigwish"}},
		},
		Projects: []*planv1.ProjectRef{{Id: g.project, Name: "bigwish", Git: true}},
	}
}

// plan makes the azimas: a first half of top-level azimas, the wills, and the second half spread among them.
func (g *gen) plan() {
	wills := max(1, g.size.Azimas/2)
	for i := range g.size.Azimas {
		t := g.task(fmt.Sprintf("T%02d", i+1), planv1.TaskKind_TASK_KIND_AZIMA)
		t.PlanFile = fmt.Sprintf("plan/%08x-%s.md", g.rand.Uint32(), strings.ReplaceAll(g.words(3), " ", "-"))
		t.Phase = []string{"1", "2", "3", "later"}[g.rand.IntN(4)]
		if i >= wills {
			t.PartOf = g.azimas[i%wills].GetId()
		}
		// The first two thirds are done, by their plan file.
		if i < g.size.Azimas*2/3 {
			t.Status = planv1.TaskStatus_TASK_STATUS_DONE
			t.Closed = &planv1.Closure{Actor: planv1.Closer_CLOSER_PLAN_FILE, CreateTime: t.GetCreateTime()}
		}
		g.azimas = append(g.azimas, t)
	}
}

// tasks makes the work tasks, their workers' events, and their place in the plan: most are done, the last ones run or
// wait, as in a wish under way.
func (g *gen) tasks() {
	n := g.size.Work
	for i := range n {
		t := g.task(fmt.Sprintf("W%d", i+1), planv1.TaskKind_TASK_KIND_UNSPECIFIED)
		t.Branch = fmt.Sprintf("w%d-%s-%s", i+1, strings.ReplaceAll(g.words(5), " ", "-"), t.GetId()[28:])
		t.Access = planv1.TaskAccess_TASK_ACCESS_AGENTS
		t.MaxBudgetUsd = 20
		t.Scheduled = true
		if len(g.azimas) > 0 && g.rand.IntN(100) < 93 {
			t.PartOf = g.azimas[g.rand.IntN(len(g.azimas))].GetId()
		}
		if i > 0 && g.rand.IntN(100) < 30 {
			t.DependsOn = []string{g.work[g.rand.IntN(i)].GetId()}
		}
		switch left := n - i; {
		case left <= n*3/100:
			t.Status = planv1.TaskStatus_TASK_STATUS_PENDING
			t.WaitReason = "waits for a slot"
		case left <= n*8/100:
			t.Status = planv1.TaskStatus_TASK_STATUS_RUNNING
		case g.rand.IntN(100) < 2:
			t.Status = []planv1.TaskStatus{planv1.TaskStatus_TASK_STATUS_FAILED, planv1.TaskStatus_TASK_STATUS_STOPPED}[g.rand.IntN(2)]
			t.Error = g.text(12)
		default:
			t.Status = planv1.TaskStatus_TASK_STATUS_DONE
		}
		if i%25 == 7 && g.size.Questions > 0 {
			t.Role = planv1.TaskRole_TASK_ROLE_CONVERTER
			t.Question = fmt.Sprintf("Q%02d", g.rand.IntN(g.size.Questions))
			t.Decision = t.GetQuestion()
		}
		g.work = append(g.work, t)
		if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			g.events(t)
		}
		if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && n-i <= n*20/100 {
			t.Integration = &planv1.TaskIntegration{
				State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED, Branch: "feat/bigwish",
				Sha: fmt.Sprintf("%016x%016x%08x", g.rand.Uint64(), g.rand.Uint64(), g.rand.Uint32()), UpdateTime: t.GetEndTime(),
			}
		}
	}
}

func (g *gen) task(code string, kind planv1.TaskKind) *planv1.Task {
	t := &planv1.Task{
		Id: g.id(), WishId: WishID, ProjectId: g.project, Code: code, Title: g.title(), Kind: kind,
		Provider: planv1.Provider_PROVIDER_CLAUDE, Status: planv1.TaskStatus_TASK_STATUS_PENDING, CreateTime: g.tick(10 * time.Minute),
	}
	g.exp.Tasks = append(g.exp.Tasks, t)
	return t
}

// eventKinds are what a worker says, about as often as the real wish's workers do.
var eventKinds = []struct {
	kind   planv1.TaskEventKind
	weight int
}{
	{planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL, 30},
	{planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT, 30},
	{planv1.TaskEventKind_TASK_EVENT_KIND_OTHER, 16},
	{planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, 9},
	{planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, 9},
	{planv1.TaskEventKind_TASK_EVENT_KIND_GATE, 4},
	{planv1.TaskEventKind_TASK_EVENT_KIND_LOG, 1},
	{planv1.TaskEventKind_TASK_EVENT_KIND_ERROR, 1},
}

// events makes the run of a task's worker: its prompt, what it did, what it spent; then its end.
func (g *gen) events(t *planv1.Task) {
	n := g.size.EventsPerTask/4 + g.rand.IntN(g.size.EventsPerTask*3/2+1)
	if n < 3 {
		n = 3
	}
	t.StartTime = g.tick(time.Minute)
	usage := &planv1.Usage{}
	for seq := 1; seq <= n; seq++ {
		e := &planv1.TaskEvent{TaskId: t.GetId(), Seq: int64(seq), CreateTime: g.tick(3 * time.Second)}
		e.Id = g.id()
		switch seq {
		case 1:
			e.Kind, e.Text = planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT, g.prompt(t)
		case n:
			usage.InputTokens += int64(g.rand.IntN(500))
			usage.OutputTokens += int64(20_000 + g.rand.IntN(40_000))
			usage.CacheReadTokens += int64(1_000_000 + g.rand.IntN(5_000_000))
			usage.CacheWriteTokens += int64(50_000 + g.rand.IntN(200_000))
			usage.CostUsd = float64(usage.GetOutputTokens())/1e6*75 + float64(usage.GetCacheReadTokens())/1e6*1.5
			e.Kind, e.Usage = planv1.TaskEventKind_TASK_EVENT_KIND_USAGE, proto.CloneOf(usage)
		default:
			e.Kind = g.eventKind()
			e.Text = g.eventText(e.GetKind())
		}
		g.exp.Events = append(g.exp.Events, e)
	}
	t.Usage = usage
	if t.GetStatus() != planv1.TaskStatus_TASK_STATUS_RUNNING {
		t.EndTime = timestamppb.New(g.now)
	}
}

func (g *gen) eventKind() planv1.TaskEventKind {
	r := g.rand.IntN(100)
	for _, k := range eventKinds {
		if r < k.weight {
			return k.kind
		}
		r -= k.weight
	}
	return planv1.TaskEventKind_TASK_EVENT_KIND_OTHER
}

var tools = []string{"Bash", "Read", "Edit", "Grep", "Write", "Glob"}

func (g *gen) eventText(kind planv1.TaskEventKind) string {
	switch kind {
	case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
		return tools[g.rand.IntN(len(tools))] + " " + g.words(8)
	case planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_RESULT:
		return g.text(40)
	case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT:
		return g.text(30)
	case planv1.TaskEventKind_TASK_EVENT_KIND_GATE:
		return "gate test: " + []string{"waits", "taken", "given back"}[g.rand.IntN(3)]
	case planv1.TaskEventKind_TASK_EVENT_KIND_STATUS:
		return "session " + g.id()
	default:
		return g.words(6)
	}
}

func (g *gen) prompt(t *planv1.Task) string {
	return "You are a Djinn worker. " + t.GetTitle() + ".\n\n" + g.text(350)
}

// questions makes the questions: the first ones decided, the last ones open; a third offer options.
func (g *gen) questions() {
	for i := range g.size.Questions {
		q := &planv1.Question{
			Id: g.id(), Code: fmt.Sprintf("Q%02d", i), WishId: WishID, Text: g.sentence(25) + "?",
			Context: g.text(30), CreateTime: g.tick(20 * time.Minute),
		}
		if g.rand.IntN(3) == 0 {
			for range 2 + g.rand.IntN(3) {
				q.Options = append(q.Options, g.sentence(8))
			}
			q.Recommendation = "A: " + g.text(20)
		}
		if i%10 == 3 {
			q.Rounds = []*planv1.Round{{Kind: planv1.RoundKind_ROUND_KIND_ENLIGHTEN, Actor: "local", CreateTime: q.GetCreateTime(), Note: g.text(15)}}
		}
		if i < g.size.Decisions {
			choice := planv1.Choice_CHOICE_YES
			if len(q.GetOptions()) > 0 {
				choice = planv1.Choice_CHOICE_A + planv1.Choice(g.rand.IntN(len(q.GetOptions())))
			}
			q.Answer = &planv1.Answer{Choice: choice, Note: g.text(40), CreateTime: timestamppb.New(g.now.Add(5 * time.Minute))}
			q.Marks = []*planv1.Mark{{Kind: planv1.MarkKind_MARK_KIND_READ, Actor: "local", CreateTime: q.GetAnswer().GetCreateTime()}}
		}
		g.exp.Questions = append(g.exp.Questions, q)
	}
}

// blockKinds are the kinds of blocks the lead writes, about as often as in the real wish, and the words of their
// content.
var blockKinds = []struct {
	kind   string
	weight int
	words  int
}{
	{"log", 42, 25},
	{"decision", 30, 60},
	{"report", 16, 120},
	{"section", 8, 300},
	{"diagram", 3, 0},
	{"brief", 1, 150},
}

func (g *gen) blocks() {
	for i := range g.size.Blocks {
		r, k := g.rand.IntN(100), blockKinds[0]
		for _, k = range blockKinds {
			if r < k.weight {
				break
			}
			r -= k.weight
		}
		b := &planv1.Block{
			Id: g.id(), WishId: WishID, Kind: k.kind, Title: g.sentence(7), Position: int64(i+1) * 1000,
			MediaType: "text/markdown", CreateTime: g.tick(10 * time.Minute),
		}
		b.UpdateTime = b.GetCreateTime()
		switch k.kind {
		case "diagram":
			b.Content = g.sentence(12) + "\n\n```mermaid\nflowchart TB\n"
			for j := range 8 {
				b.Content += fmt.Sprintf("  n%d[\"%s\"] --> n%d\n", j, g.words(2), j+1)
			}
			b.Content += "```\n"
		case "section", "report":
			b.Content = "## " + g.sentence(5) + "\n\n" + g.text(k.words/2) + "\n\n- " + g.sentence(10) + "\n- " +
				g.sentence(10) + "\n\n" + g.text(k.words/2)
		default:
			b.Content = g.text(k.words)
		}
		if k.kind == "report" && len(g.work) > 0 {
			b.TaskId = g.work[g.rand.IntN(len(g.work))].GetId()
		}
		g.exp.Blocks = append(g.exp.Blocks, b)
	}
}

// commandMethods are the methods of the journal, about as often as in the real wish's.
var commandMethods = []struct {
	method string
	weight int
}{
	{"/machine.v1.CommandService/Record", 42},
	{planv1connect.TaskServiceGroupProcedure, 18},
	{planv1connect.TaskServiceSpawnProcedure, 14},
	{planv1connect.TaskServiceCleanProcedure, 10},
	{planv1connect.BlockServicePutProcedure, 5},
	{planv1connect.QuestionServiceAskProcedure, 2},
	{planv1connect.QuestionServiceAnswerProcedure, 2},
	{planv1connect.TaskServiceDoneProcedure, 2},
	{planv1connect.TaskServiceDependProcedure, 2},
	{planv1connect.TaskServiceSendProcedure, 1},
	{planv1connect.MarkServicePutProcedure, 1},
	{planv1connect.QuestionServiceReviseProcedure, 1},
}

var commandLines = []string{"go tool task test-go", "go tool task lint", "go tool task gen", "go tool task e2e", "npm ci"}

// commands makes the journal: requests that name the wish's tasks, questions and blocks, the oldest first.
func (g *gen) commands() {
	// The journal spans the wish, from its start to the last object made.
	span := g.now.Sub(start) / time.Duration(max(1, g.size.Commands))
	g.now = start
	for range g.size.Commands {
		r, method := g.rand.IntN(100), commandMethods[0].method
		for _, m := range commandMethods {
			method = m.method
			if r < m.weight {
				break
			}
			r -= m.weight
		}
		c := &planv1.Command{At: g.tick(span), Method: method, Actor: "local"}
		c.Id = g.id()
		req := g.request(method)
		packed, err := anypb.New(req)
		if err != nil {
			panic(err)
		}
		c.Request = packed
		g.exp.Commands = append(g.exp.Commands, c)
	}
}

func (g *gen) request(method string) proto.Message {
	task := func() string {
		if len(g.work) == 0 {
			return WishID
		}
		return g.work[g.rand.IntN(len(g.work))].GetId()
	}
	question := func() string {
		if len(g.exp.GetQuestions()) == 0 {
			return "Q00"
		}
		return g.exp.GetQuestions()[g.rand.IntN(len(g.exp.GetQuestions()))].GetCode()
	}
	switch method {
	case planv1connect.TaskServiceGroupProcedure:
		return &planv1.TaskServiceGroupRequest{TaskId: task(), PartOf: fmt.Sprintf("T%02d", 1+g.rand.IntN(max(1, g.size.Azimas)))}
	case planv1connect.TaskServiceSpawnProcedure:
		return &planv1.TaskServiceSpawnRequest{
			WishId: WishID, ProjectId: g.project, Title: g.title(), Prompt: g.text(350),
			Provider: planv1.Provider_PROVIDER_CLAUDE, MaxBudgetUsd: 20,
		}
	case planv1connect.TaskServiceCleanProcedure:
		return &planv1.TaskServiceCleanRequest{TaskId: task()}
	case planv1connect.BlockServicePutProcedure:
		return &planv1.BlockServicePutRequest{WishId: WishID, Kind: "log", Title: g.sentence(7), Content: g.text(25)}
	case planv1connect.QuestionServiceAskProcedure:
		return &planv1.QuestionServiceAskRequest{WishId: WishID, Text: g.sentence(25) + "?", Context: g.text(30)}
	case planv1connect.QuestionServiceAnswerProcedure:
		return &planv1.QuestionServiceAnswerRequest{
			WishId: WishID, Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: question()}},
			Choice: planv1.Choice_CHOICE_YES, Note: g.text(40),
		}
	case planv1connect.TaskServiceDoneProcedure:
		return &planv1.TaskServiceDoneRequest{TaskId: task(), Note: g.sentence(8), By: planv1.Closer_CLOSER_LEAD}
	case planv1connect.TaskServiceDependProcedure:
		return &planv1.TaskServiceDependRequest{TaskId: task(), DependsOn: []string{task()}}
	case planv1connect.TaskServiceSendProcedure:
		return &planv1.TaskServiceSendRequest{TaskId: task(), Text: g.text(30)}
	case planv1connect.MarkServicePutProcedure:
		return &planv1.MarkServicePutRequest{
			WishId: WishID, Kind: planv1.MarkKind_MARK_KIND_READ,
			Target: &planv1.MarkTarget{Ref: &planv1.MarkTarget_Code{Code: question()}},
		}
	case planv1connect.QuestionServiceReviseProcedure:
		return &planv1.QuestionServiceReviseRequest{
			WishId: WishID, Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: question()}}, Context: g.text(30),
		}
	default:
		return &machinev1.CommandServiceRecordRequest{
			TaskId: task(), Directory: "bigwish", Command: commandLines[g.rand.IntN(len(commandLines))],
			CpuSeconds: g.rand.Float64() * 60, Seconds: g.rand.Float64() * 20, PeakMemoryBytes: uint64(g.rand.IntN(1 << 30)),
		}
	}
}

// vocabulary is the words of the text: plain English, as Djinn writes in its repository.
var vocabulary = strings.Fields(`the a wish task worker lead plan azima question decision block journal window page store
event branch merge test gate commit push review proof file folder project agent session model cost token tree list
render scroll filter sort read write keep show hide open close start stop wait run end fails passes ready done fast slow
large small every each one two three first last new old local remote main integration check build lint import export
measure bench time size frame paint layout memory query index row column table field value line word title note`)

// words is n words of the vocabulary.
func (g *gen) words(n int) string {
	var b strings.Builder
	g.write(&b, n, false)
	return b.String()
}

// sentence is about n words, the first capitalized.
func (g *gen) sentence(n int) string {
	var b strings.Builder
	g.write(&b, max(1, n/2+g.rand.IntN(n+1)), true)
	return b.String()
}

// title is a task's title, in a line.
func (g *gen) title() string { return g.sentence(9) }

// text is a few sentences, about n words in all.
func (g *gen) text(n int) string {
	var b strings.Builder
	b.Grow(n * 6)
	for left := n; left > 0; left -= 12 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		k := min(12, left)
		g.write(&b, max(1, k/2+g.rand.IntN(k+1)), true)
		b.WriteByte('.')
	}
	return b.String()
}

// write writes n words of the vocabulary to b, separated by spaces, the first capitalized if asked.
func (g *gen) write(b *strings.Builder, n int, capital bool) {
	for i := range n {
		w := vocabulary[g.rand.IntN(len(vocabulary))]
		if i > 0 {
			b.WriteByte(' ')
		} else if capital {
			b.WriteByte(w[0] - 'a' + 'A')
			w = w[1:]
		}
		b.WriteString(w)
	}
}
