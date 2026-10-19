package plan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// The task an option of a question plans (decision Q25): the asker writes it as a plan's text, the question keeps it,
// and an answer launches it, or leaves it to the lead.

const (
	// planTextMax is the most a plan's text holds: a prompt of djinn task spawn, and its header.
	planTextMax = 210000
	// planPromptClip is how much of a plan's prompt the lead's revision quotes.
	planPromptClip = 400
)

// planKeys are the header keys of a plan's text: djinn task spawn's flags, the option, and a mockup's rounds.
var planKeys = []string{"option", "title", "prompt", "project-id", "provider", "model", "part-of", "after", "blocks",
	"tilasm", "kind", "rounds"}

// LaunchPlan spawns the task p plans for the answered question q, its decision q, and records it in q's plan: the
// harness does it, once the plan holds.
type LaunchPlan func(ctx context.Context, q *planv1.Question, p *planv1.PlannedTask) (*planv1.Task, error)

// WithLaunch gives QuestionService the harness that launches a question's planned task (Questions.Launch).
func WithLaunch(f LaunchPlan) Option { return func(o *options) { o.launch = f } }

// ParsePlan reads the text of a plan: header lines "key: value", named as djinn task spawn's flags, up to the first
// blank line, then the prompt. after, blocks and tilasm repeat, or separate their names with commas. The names stay
// as written: fillPlan resolves them in the question's wish.
func ParsePlan(text string) (*planv1.PlannedTask, error) {
	text = strings.TrimLeft(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	head, body, _ := strings.Cut(text, "\n\n")
	p := &planv1.PlannedTask{Prompt: strings.TrimSpace(body)}
	for line := range strings.SplitSeq(head, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-")
		value = strings.TrimSpace(value)
		if !ok {
			return nil, fmt.Errorf("plan line %q is not \"key: value\"; the prompt comes after a blank line", line)
		}
		var err error
		switch key {
		case "option":
			p.Option, err = planOption(value)
		case "title":
			p.Title = value
		case "prompt":
			if p.GetPrompt() != "" {
				return nil, errors.New("the plan gives its prompt twice: in its header and after the blank line")
			}
			p.Prompt = value
		case "project-id":
			p.ProjectId = value
		case "provider":
			var n int32
			n, err = enumWord(planv1.Provider_value, "PROVIDER_", key, value)
			p.Provider = planv1.Provider(n)
		case "model":
			p.Model = value
		case "part-of":
			p.PartOf = value
		case "after":
			p.After = append(p.After, planNames(value)...)
		case "blocks":
			p.Blocks = append(p.Blocks, planNames(value)...)
		case "tilasm":
			p.Tilasm = append(p.Tilasm, planNames(value)...)
		case "kind":
			var n int32
			n, err = enumWord(planv1.TaskKind_value, "TASK_KIND_", key, value)
			p.Kind = planv1.TaskKind(n)
		case "rounds":
			n, convErr := strconv.Atoi(value)
			if convErr != nil || n < 1 || n > maxMockupRounds {
				err = fmt.Errorf("plan rounds %q is not a number from 1 to %d", value, maxMockupRounds)
			}
			p.Rounds = uint32(max(n, 0))
		default:
			return nil, fmt.Errorf("plan key %q is none of %s", key, strings.Join(planKeys, ", "))
		}
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

// planOption is the option a plan's header names: a letter from A to D, or yes.
func planOption(value string) (planv1.Choice, error) {
	v := strings.ToUpper(value)
	switch {
	case v == "YES":
		return planv1.Choice_CHOICE_YES, nil
	case len(v) == 1 && v[0] >= 'A' && v[0] <= 'D':
		return planv1.Choice_CHOICE_A + planv1.Choice(v[0]-'A'), nil
	}
	return 0, fmt.Errorf("plan option %q is none of A, B, C, D, yes", value)
}

// enumWord is the value of an enum, prefixed so, that a plan's header names in a word: claude, ifrit.
func enumWord(values map[string]int32, prefix, key, word string) (int32, error) {
	n, ok := values[prefix+strings.ToUpper(strings.ReplaceAll(word, "-", "_"))]
	if ok && n != 0 {
		return n, nil
	}
	var words []string
	for name, v := range values {
		if v != 0 {
			words = append(words, strings.ToLower(strings.TrimPrefix(name, prefix)))
		}
	}
	slices.Sort(words)
	return 0, fmt.Errorf("plan %s %q is none of %s", key, word, strings.Join(words, ", "))
}

// planNames are the names a plan's header line gives, separated by commas or spaces.
func planNames(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// readPlans is texts, then the text of each file of files: the plans a request gives, as the journal keeps them.
func readPlans(texts, files []string) ([]string, error) {
	out := slices.Clone(texts)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("read plan_file: %w", err))
		}
		if len(data) > planTextMax {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("plan_file %s exceeds %d bytes (%d)",
				file, planTextMax, len(data)))
		}
		out = append(out, string(data))
	}
	return out, nil
}

// setPlans puts in q the plans texts give, resolved in its wish, each in place of the plan of its option. When remove
// is set, a text that names its option alone removes that option's plan.
func setPlans(ctx context.Context, r store.Reader, q *planv1.Question, texts []string, remove bool) error {
	given := map[planv1.Choice]bool{}
	for _, text := range texts {
		p, err := ParsePlan(text)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		if p.GetOption() == planv1.Choice_CHOICE_UNSPECIFIED && len(q.GetOptions()) == 0 {
			p.Option = planv1.Choice_CHOICE_YES
		}
		if given[p.GetOption()] {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("two plans for option %s",
				choiceLetter(p.GetOption())))
		}
		given[p.GetOption()] = true
		mine := func(o *planv1.PlannedTask) bool { return o.GetOption() == p.GetOption() }
		if remove && proto.Equal(p, &planv1.PlannedTask{Option: p.GetOption()}) {
			q.Plans = slices.DeleteFunc(q.Plans, mine)
			continue
		}
		if err := fillPlan(ctx, r, q, p); err != nil {
			return err
		}
		if i := slices.IndexFunc(q.GetPlans(), mine); i >= 0 {
			q.Plans[i] = p
		} else {
			q.Plans = append(q.Plans, p)
		}
	}
	slices.SortFunc(q.Plans, func(a, b *planv1.PlannedTask) int { return int(a.GetOption()) - int(b.GetOption()) })
	return nil
}

// fitPlans drops the plans of q whose option it no longer has, once a revision changed its options.
func fitPlans(q *planv1.Question) {
	q.Plans = slices.DeleteFunc(q.Plans, func(p *planv1.PlannedTask) bool { return planOptionError(q, p) != "" })
}

// planOptionError says why p's option is not one of q's; "" when it is.
func planOptionError(q *planv1.Question, p *planv1.PlannedTask) string {
	n, o := len(q.GetOptions()), p.GetOption()
	switch {
	case n == 0 && o != planv1.Choice_CHOICE_YES:
		return fmt.Sprintf("question %s has no options: its plan names no option, or yes", q.GetCode())
	case n > 0 && o == planv1.Choice_CHOICE_UNSPECIFIED:
		return "name the option the plan is for: option: A"
	case n > 0 && (o < planv1.Choice_CHOICE_A || o > planv1.Choice_CHOICE_D || int(o-planv1.Choice_CHOICE_A) >= n):
		return fmt.Sprintf("question %s has %d options: plan for A to %c", q.GetCode(), n, 'A'+n-1)
	}
	return ""
}

// fillPlan checks p, a plan of q, and resolves the names it gives in q's wish: its project, the tasks it comes after
// or blocks, its azima and its tilasms, kept by identifier; it notes how the tasks it names stand now.
func fillPlan(ctx context.Context, r store.Reader, q *planv1.Question, p *planv1.PlannedTask) error {
	invalid := func(format string, a ...any) error {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, a...))
	}
	if why := planOptionError(q, p); why != "" {
		return invalid("%s", why)
	}
	letter := choiceLetter(p.GetOption())
	switch {
	case strings.TrimSpace(p.GetTitle()) == "":
		return invalid("the plan for %s has no title: \"title: …\"", letter)
	case len([]rune(p.GetTitle())) > 500:
		return invalid("the plan for %s has a title longer than 500 characters", letter)
	case len([]rune(p.GetPrompt())) > 200000:
		return invalid("the plan for %s has a prompt longer than 200000 characters", letter)
	case len([]rune(p.GetModel())) > 200:
		return invalid("the plan for %s has a model longer than 200 characters", letter)
	case p.GetRounds() > 0 && !IsMockupPlan(p):
		return invalid("the plan for %s gives rounds, which are a mockup's: kind: mockup", letter)
	case IsMockupPlan(p) && p.GetRounds() > maxMockupRounds:
		return invalid("the plan for %s gives %d rounds: at most %d", letter, p.GetRounds(), maxMockupRounds)
	}
	if p.GetModel() != "" {
		prov := p.GetProvider()
		if prov == planv1.Provider_PROVIDER_UNSPECIFIED {
			prov = cmp.Or(ProviderForCircleModel(p.GetModel()), planv1.Provider_PROVIDER_CLAUDE)
		}
		norm, err := ValidateTaskModel(prov, p.GetModel())
		if err != nil {
			return invalid("the plan for %s: %v", letter, err)
		}
		p.Model = norm
	}
	wish, err := store.Get[*planv1.Wish](ctx, r, q.GetWishId())
	if err != nil {
		return err
	}
	if id := p.GetProjectId(); id != "" {
		project, err := store.Get[*planv1.Project](ctx, r, id)
		if errors.Is(err, store.ErrNotFound) || err == nil && !slices.Contains(wish.GetProjectIds(), project.GetId()) {
			return invalid("the plan for %s names project %s, not one of the wish's", letter, id)
		}
		if err != nil {
			return err
		}
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return err
	}
	if p.After, err = planTasks(tasks, p.GetAfter(), letter, "comes after"); err != nil {
		return err
	}
	if p.Blocks, err = planTasks(tasks, p.GetBlocks(), letter, "blocks"); err != nil {
		return err
	}
	for _, id := range p.GetBlocks() {
		if t := taskIn(tasks, id); t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"the plan for %s blocks %s, which is %s: a plan blocks planned tasks only", letter, t.GetCode(), taskState(t)))
		}
	}
	if name := p.GetPartOf(); name != "" {
		ids, err := planTasks(tasks, []string{name}, letter, "is part of")
		if err != nil {
			return err
		}
		if a := taskIn(tasks, ids[0]); !IsAzima(a) {
			return invalid("the plan for %s is part of %s, which is work, not an azima", letter, a.GetCode())
		}
		p.PartOf = ids[0]
	}
	var tilasms []string
	for _, list := range p.GetTilasm() {
		for _, name := range planNames(list) {
			tilasm, pin, err := pinnedTilasm(ctx, r, q.GetWishId(), name)
			if err != nil {
				return err
			}
			id := tilasm.GetId()
			if pin > 0 {
				id = fmt.Sprintf("%s@%d", id, pin) // The plan gives that version, whatever comes after it.
			}
			if !slices.Contains(tilasms, id) {
				tilasms = append(tilasms, id)
			}
		}
	}
	p.Tilasm = tilasms
	p.Dependencies = nil
	for _, id := range append(slices.Clone(p.GetAfter()), p.GetBlocks()...) {
		t := taskIn(tasks, id)
		p.Dependencies = append(p.Dependencies, &planv1.PlannedDependency{
			TaskId: id, Code: t.GetCode(), Status: t.GetStatus(), Title: t.GetTitle(),
		})
	}
	return nil
}

// planTasks are the identifiers of the tasks names give among tasks, by code or identifier, without repeats; what
// says what the plan for letter does with them, for the error of a name no task has.
func planTasks(tasks []*planv1.Task, names []string, letter, what string) ([]string, error) {
	var out []string
	for _, list := range names {
		for _, name := range planNames(list) {
			i := slices.IndexFunc(tasks, func(t *planv1.Task) bool {
				return t.GetId() == name || strings.EqualFold(t.GetCode(), name)
			})
			if i < 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"the plan for %s %s %s, not a task of the wish", letter, what, name))
			}
			if id := tasks[i].GetId(); !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// taskIn is the task id among tasks; nil when none is.
func taskIn(tasks []*planv1.Task, id string) *planv1.Task {
	if i := slices.IndexFunc(tasks, func(t *planv1.Task) bool { return t.GetId() == id }); i >= 0 {
		return tasks[i]
	}
	return nil
}

// taskState is a task's status in a word: done, running, cancelled.
func taskState(t *planv1.Task) string {
	if t.GetCancelled() != nil {
		return "cancelled"
	}
	return statusWord(t.GetStatus())
}

// ChosenPlan is the plan of the option q's answer chose; nil when it planned none.
func ChosenPlan(q *planv1.Question) *planv1.PlannedTask {
	choice := q.GetAnswer().GetChoice()
	if choice == planv1.Choice_CHOICE_UNSPECIFIED {
		return nil
	}
	for _, p := range q.GetPlans() {
		if p.GetOption() == choice {
			return p
		}
	}
	return nil
}

// WhyLeadLaunches says why the plan p of q's answer is left to the lead rather than launched with the answer: the answer
// has a note, or the plan comes after tasks or blocks some. "" when Djinn launches it at once.
func WhyLeadLaunches(q *planv1.Question, p *planv1.PlannedTask) string {
	switch {
	case strings.TrimSpace(q.GetAnswer().GetNote()) != "":
		return "the answer has a note"
	case len(p.GetAfter()) > 0 && len(p.GetBlocks()) > 0:
		return "it comes after tasks and blocks others"
	case len(p.GetAfter()) > 0:
		return "it comes after tasks"
	case len(p.GetBlocks()) > 0:
		return "it blocks tasks"
	}
	return ""
}

// WhyStale says why the plan p no longer holds among tasks, its wish's: its azima is closed or gone, a task it comes
// after was cancelled or is gone, a task it blocks has started or is gone. "" when it holds.
func WhyStale(p *planv1.PlannedTask, tasks []*planv1.Task) string {
	var why []string
	if id := p.GetPartOf(); id != "" {
		switch a := taskIn(tasks, id); {
		case a == nil:
			why = append(why, "its azima is gone")
		case a.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE, a.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED,
			a.GetCancelled() != nil:
			why = append(why, fmt.Sprintf("azima %s is %s", a.GetCode(), taskState(a)))
		}
	}
	for _, id := range p.GetAfter() {
		switch t := taskIn(tasks, id); {
		case t == nil:
			why = append(why, fmt.Sprintf("%s, which it comes after, is gone", planCode(p, id)))
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED, t.GetCancelled() != nil:
			why = append(why, fmt.Sprintf("%s, which it comes after, was cancelled", t.GetCode()))
		}
	}
	for _, id := range p.GetBlocks() {
		switch t := taskIn(tasks, id); {
		case t == nil:
			why = append(why, fmt.Sprintf("%s, which it blocks, is gone", planCode(p, id)))
		case t.GetStatus() != planv1.TaskStatus_TASK_STATUS_PENDING:
			why = append(why, fmt.Sprintf("%s, which it blocks, is %s", t.GetCode(), taskState(t)))
		}
	}
	return strings.Join(why, "; ")
}

// planCode is the code the task id had when p was planned; the identifier when p does not know it.
func planCode(p *planv1.PlannedTask, id string) string {
	for _, d := range p.GetDependencies() {
		if d.GetTaskId() == id && d.GetCode() != "" {
			return d.GetCode()
		}
	}
	return id
}

// LaunchCommand is the command that launches q's planned task, as it is.
func LaunchCommand(q *planv1.Question) string { return "djinn question launch " + q.GetCode() }

// RevisionDetail is what the lead reads under the line of q's answer when Djinn left the plan of the chosen option to it:
// the question, the answer and its note, the plan, how each task it names stands now against when it was planned, why
// it no longer holds if it does not, and how to launch it. "" when the option planned nothing, or its task is
// launched.
func RevisionDetail(ctx context.Context, r store.Reader, q *planv1.Question) (string, error) {
	p := ChosenPlan(q)
	if p == nil || p.GetTaskId() != "" {
		return "", nil
	}
	tasks, err := store.List[*planv1.Task](ctx, r, store.Where{"wish_id": q.GetWishId()})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n", clipRunes(oneLine(q.GetText()), briefLineMax))
	fmt.Fprintf(&b, "Answer: %s.%s\n", quotedChoice(q), noteInFull(q.GetAnswer().GetNote()))
	var traits []string
	if k := p.GetKind(); k != planv1.TaskKind_TASK_KIND_UNSPECIFIED && k != planv1.TaskKind_TASK_KIND_WORK {
		traits = append(traits, strings.ToLower(strings.TrimPrefix(k.String(), "TASK_KIND_")))
	}
	if IsMockupPlan(p) {
		traits = append(traits, fmt.Sprintf("version %d of %d", Tour(q), MockupRounds(p)))
	}
	if p.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED || p.GetModel() != "" {
		agent := strings.ToLower(strings.TrimPrefix(p.GetProvider().String(), "PROVIDER_"))
		if p.GetProvider() == planv1.Provider_PROVIDER_UNSPECIFIED {
			agent = "the project's agent"
		}
		traits = append(traits, strings.TrimSpace(agent+" "+p.GetModel()))
	}
	if id := p.GetPartOf(); id != "" {
		traits = append(traits, "part of "+cmp.Or(taskIn(tasks, id).GetCode(), id))
	}
	for _, name := range p.GetTilasm() {
		id, pin, pinned := strings.Cut(name, "@")
		code := id
		if tilasm, err := store.Get[*planv1.Tilasm](ctx, r, id); err == nil {
			code = tilasm.GetCode()
		}
		if pinned {
			code += " v" + pin
		}
		traits = append(traits, "tilasm "+code)
	}
	if id := p.GetProjectId(); id != "" {
		name := id
		if project, err := store.Get[*planv1.Project](ctx, r, id); err == nil {
			name = project.GetName()
		}
		traits = append(traits, "project "+name)
	}
	fmt.Fprintf(&b, "Plan for %s: %q", choiceLetter(p.GetOption()), oneLine(p.GetTitle()))
	if len(traits) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(traits, "; "))
	}
	b.WriteString("\n")
	for _, list := range []struct {
		name string
		ids  []string
	}{{"After", p.GetAfter()}, {"Blocks", p.GetBlocks()}} {
		if len(list.ids) == 0 {
			continue
		}
		states := make([]string, 0, len(list.ids))
		for _, id := range list.ids {
			states = append(states, dependencyState(p, id, taskIn(tasks, id)))
		}
		fmt.Fprintf(&b, "%s: %s\n", list.name, strings.Join(states, "; "))
	}
	if why := WhyStale(p, tasks); why != "" {
		fmt.Fprintf(&b, "No longer holds: %s.\n", why)
	}
	if prompt := oneLine(p.GetPrompt()); prompt != "" {
		fmt.Fprintf(&b, "Prompt: %s\n", clipRunes(prompt, planPromptClip))
	}
	fmt.Fprintf(&b, "Launch it as it is: %s; edited: the same with djinn task spawn's flags in place of the plan's "+
		"(--title, --prompt, --after \"\" for none…).", LaunchCommand(q))
	return b.String(), nil
}

// dependencyState is how the task id of p stands now, t (nil when gone), and what changed since p was planned.
func dependencyState(p *planv1.PlannedTask, id string, t *planv1.Task) string {
	var then *planv1.PlannedDependency
	for _, d := range p.GetDependencies() {
		if d.GetTaskId() == id {
			then = d
		}
	}
	if t == nil {
		return planCode(p, id) + " gone since the question"
	}
	state := t.GetCode() + " " + taskState(t)
	var changed []string
	if then != nil && then.GetStatus() != t.GetStatus() {
		changed = append(changed, "was "+statusWord(then.GetStatus()))
	}
	if then != nil && then.GetTitle() != t.GetTitle() {
		changed = append(changed, "retitled")
	}
	if len(changed) > 0 {
		state += ", changed since the question (" + strings.Join(changed, ", ") + ")"
	}
	return state
}

// noteInFull is the part of a revision that gives an answer's note, whole on one line; "" without one.
func noteInFull(note string) string {
	if note = oneLine(note); note == "" {
		return ""
	}
	return fmt.Sprintf(" Note: %q.", note)
}

// launchedPlan is p with what m gives in place of its own, resolved in q's wish as a plan is.
func launchedPlan(
	ctx context.Context, r store.Reader, q *planv1.Question, p *planv1.PlannedTask, m *planv1.QuestionServiceLaunchRequest,
) (*planv1.PlannedTask, error) {
	p = proto.CloneOf(p)
	if m.GetTitle() != "" {
		p.Title = m.GetTitle()
	}
	if m.GetPrompt() != "" {
		p.Prompt = m.GetPrompt()
	}
	if m.ProjectId != nil {
		p.ProjectId = m.GetProjectId()
	}
	if m.GetProvider() != planv1.Provider_PROVIDER_UNSPECIFIED {
		p.Provider = m.GetProvider()
	}
	if m.Model != nil {
		p.Model = m.GetModel()
	}
	if m.PartOf != nil {
		p.PartOf = m.GetPartOf()
	}
	if len(m.GetAfter()) > 0 {
		p.After = m.GetAfter()
	}
	if len(m.GetBlocks()) > 0 {
		p.Blocks = m.GetBlocks()
	}
	if len(m.GetTilasm()) > 0 {
		p.Tilasm = m.GetTilasm()
	}
	if m.GetKind() != planv1.TaskKind_TASK_KIND_UNSPECIFIED {
		p.Kind = m.GetKind()
	}
	if err := fillPlan(ctx, r, q, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Launch launches the task the chosen option of an answered question planned, as it is or with what the request gives
// in place of its own; refused when it is launched already, or no longer holds.
func (q *Questions) Launch(
	ctx context.Context, req *connect.Request[planv1.QuestionServiceLaunchRequest],
) (*connect.Response[planv1.QuestionServiceLaunchResponse], error) {
	if q.LaunchPlan == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("this server runs no tasks"))
	}
	precondition := func(format string, a ...any) error {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(format, a...))
	}
	question, err := find(ctx, q.Store, req.Msg.GetQuestion(), req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	if question.GetAnswer() == nil {
		return nil, precondition("question %s is not answered yet: its plan waits for the answer", question.GetCode())
	}
	planned := ChosenPlan(question)
	if planned == nil {
		return nil, precondition("%s, the answer to %s, planned no task: spawn one with djinn task spawn --decision %s",
			choiceLetter(question.GetAnswer().GetChoice()), question.GetCode(), question.GetCode())
	}
	if id := planned.GetTaskId(); id != "" {
		code := id
		if t, err := store.Get[*planv1.Task](ctx, q.Store, id); err == nil {
			code = t.GetCode()
		}
		return nil, precondition("the plan of %s was launched already: %s", question.GetCode(), code)
	}
	p, err := launchedPlan(ctx, q.Store, question, planned, req.Msg)
	if err != nil {
		return nil, Status(err)
	}
	tasks, err := store.List[*planv1.Task](ctx, q.Store, store.Where{"wish_id": question.GetWishId()})
	if err != nil {
		return nil, Status(err)
	}
	if why := WhyStale(p, tasks); why != "" {
		return nil, precondition("the plan of %s no longer holds: %s; launch it with flags that change what fails",
			question.GetCode(), why)
	}
	task, err := q.LaunchPlan(ctx, question, p)
	if err != nil {
		return nil, err
	}
	if question, err = store.Get[*planv1.Question](ctx, q.Store, question.GetId()); err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.QuestionServiceLaunchResponse{Task: task, Question: question}), nil
}
