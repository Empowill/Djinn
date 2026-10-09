package plan

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
)

// Brief is what starts an agent on a wish, written by Djinn from the store, without a model. Stable changes rarely:
// Djinn's rules and the rules of the wish's projects. Moving is where the wish stands. An agent gets Stable first,
// so that it reads it from its cache from one session to the next.
type Brief struct {
	Stable string
	Moving string
}

// Text is the whole brief: the stable part, then the part that moves.
func (b Brief) Text() string { return b.Stable + "\n" + b.Moving }

// How much of each section a brief holds: it starts an agent, it does not replace the page.
const (
	briefDecisions = 8
	briefDone      = 5
	briefBlocks    = 5
	briefMarks     = 10
	briefBlockText = 600
	briefLineMax   = 300
)

// ruleFiles are the files a project keeps its rules in for agents and contributors, read first.
var ruleFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md"}

// briefRules are Djinn's rules for whoever leads a wish. They name no wish: every lead reads the same text.
const briefRules = "# Leading a wish in Djinn\n\n" +
	"Djinn holds the plan of this wish: its tasks, questions, decisions and blocks. You lead it: you talk with the " +
	"developer, split the work into tasks for workers, and keep the plan true. Djinn computes the plan; you change it " +
	"with the `djinn` command, never in its data folder.\n\n" +
	"- **Ask, do not guess.** A question for the developer goes through `djinn question ask`, with its options and " +
	"your recommendation. An answered question is a decision.\n" +
	"- **Workers start from a short prompt.** Say what to do, in which project, and how to check it. " +
	"`--fork <task>` or `--from-lead` start a worker from a copy of a conversation instead: it reads that context " +
	"again at every turn, so use them only when the whole context is needed.\n" +
	"- **What Djinn does not compute is a block**: a decision taken outside a question, an analysis, a hand-off.\n" +
	"- **No secret, no local path** in the plan: name the project.\n\n" +
	"## Commands\n\n" +
	"`<wish>` is the wish's identifier, given below.\n\n" +
	"- `djinn wish brief <wish>`: this brief, up to date.\n" +
	"- `djinn question ask \"<question>\" <wish> --options \"…\" --options \"…\" --recommendation \"…\"`; " +
	"`djinn question list --wish-id <wish> --open`.\n" +
	"- `djinn task spawn <wish> --title \"…\" --prompt \"…\"` (`--project-id`, `--depends-on W1`, `--later`, " +
	"`--fork W1`, `--from-lead`); `djinn task list --wish-id <wish>`; `djinn task watch <task>`; " +
	"`djinn task send <task> \"…\"`, an instruction for a running worker: \"received\" shows once it took it in; " +
	"`djinn task stop <task>`.\n" +
	"- `djinn block put <wish> --kind decision --title \"…\" --content \"…\"`; `djinn block list <wish>`.\n" +
	"- `djinn question enlighten <question>` is the developer's \"tell me more\": the question waits for your " +
	"`djinn question revise <question> --context \"…\" --recommendation \"…\"`, after you investigated.\n" +
	"- `djinn mark list <wish>`: what the developer read or approved in the window, without a word. An approved " +
	"block or decision is a go: act on it. Start a recommendation with its option's letter (`B: …`): the developer " +
	"approves it in one click.\n" +
	"- `djinn wish sync <wish>`: the wish's page, which Djinn keeps up to date in the file it prints. After the wish " +
	"changes, republish that file as it is, in one call, to the same address: do not read it, rewrite it, or write " +
	"HTML by hand. The first publish needs the developer's go. `djinn wish render <wish>` writes the page once.\n" +
	"- `djinn wish grant <wish>` is the developer's word, never yours.\n"

// BuildBrief writes the brief of a wish from what r holds. home is Djinn's data folder: like the projects' folders
// and the home folder, it never shows. No secret: Djinn stores none, and a URL loses its credentials.
func BuildBrief(ctx context.Context, r store.Reader, home, wishID string) (Brief, error) {
	exp, projects, err := collect(ctx, r, wishID)
	if err != nil {
		return Brief{}, err
	}
	all, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return Brief{}, err
	}
	wish := exp.GetWish()
	rank := wish.GetRank()
	ready := wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions())
	exp = portable(exp, newScrubber(all, home, dataName))
	return Brief{Stable: stableBrief(projects), Moving: movingBrief(exp, rank, ready)}, nil
}

// StableBrief is the part of a brief that changes rarely, for a wish on projects: Djinn's rules, then where each
// project keeps its own. It names the projects, never their folders.
func StableBrief(projects []*planv1.Project) string { return stableBrief(projects) }

func stableBrief(projects []*planv1.Project) string {
	var b strings.Builder
	b.WriteString(briefRules)
	b.WriteString("\n## The projects' rules\n\n")
	if len(projects) == 0 {
		b.WriteString("The wish has no project: its workers only read, each in an empty folder of its own.\n")
		return b.String()
	}
	sorted := slices.Clone(projects)
	slices.SortFunc(sorted, func(x, y *planv1.Project) int {
		return cmp.Compare(strings.ToLower(x.GetName()), strings.ToLower(y.GetName()))
	})
	for _, p := range sorted {
		fmt.Fprintf(&b, "- **%s**", p.GetName())
		if p.GetDirectory() == "" {
			b.WriteString(": not on this machine yet (`djinn project add <folder>`).\n")
			continue
		}
		if p.GetGit() {
			b.WriteString(", a Git repository: each worker has its own worktree and branch")
		} else {
			b.WriteString(", outside Git: workers share its folder, and their write scopes keep them apart")
		}
		if files := presentFiles(p.GetDirectory(), ruleFiles); len(files) > 0 {
			b.WriteString(". Read `" + strings.Join(files, "`, `") + "` at its root first.\n")
		} else {
			b.WriteString(". It keeps no rule file at its root.\n")
		}
	}
	return b.String()
}

// presentFiles are the names of files found in dir, case ignored, as the folder spells them.
func presentFiles(dir string, names []string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range names {
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(e.Name(), name) {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out
}

// movingBrief is where the wish stands: its state, questions, decisions, tasks and latest blocks. Empty sections are
// left out.
func movingBrief(exp *planv1.WishExport, rank int32, ready bool) string {
	wish := exp.GetWish()
	var b strings.Builder
	fmt.Fprintf(&b, "# The wish: %s\n\n", oneLine(wish.GetTitle()))
	fmt.Fprintf(&b, "- Identifier: `%s`, the `<wish>` of the commands above.\n", wish.GetId())
	switch wish.GetState() {
	case planv1.WishState_WISH_STATE_PAUSED:
		b.WriteString("- Paused: `djinn wish activate <wish>` makes it active again.\n")
	case planv1.WishState_WISH_STATE_GRANTED:
		fmt.Fprintf(&b, "- Granted on %s: it is done.\n", when(wish.GetGrantTime().AsTime()))
	default:
		if rank > 0 {
			fmt.Fprintf(&b, "- Active, rank %d among the active wishes.\n", rank)
		} else {
			b.WriteString("- Active.\n")
		}
	}
	if refs := exp.GetProjects(); len(refs) > 0 {
		names := make([]string, len(refs))
		for i, p := range refs {
			names[i] = p.GetName()
		}
		b.WriteString("- Projects: " + strings.Join(names, ", ") + ".\n")
	}
	if ready {
		b.WriteString("- Djinn proposes to grant it: every task is finished and no question is open. Granting is the developer's word.\n")
	}

	var open, investigate, decided []*planv1.Question
	for _, q := range exp.GetQuestions() {
		if Investigating(q) {
			investigate = append(investigate, q)
		} else if q.GetAnswer() == nil {
			open = append(open, q)
		} else {
			decided = append(decided, q)
		}
	}
	if len(open) > 0 {
		b.WriteString("\n## Open questions\n\n")
		for _, q := range open {
			fmt.Fprintf(&b, "- **%s** %s\n", q.GetCode(), clipLine(q.GetText()))
			for i, o := range q.GetOptions() {
				fmt.Fprintf(&b, "  - %c: %s\n", 'A'+i, clipLine(o))
			}
			if rec := q.GetRecommendation(); rec != "" {
				fmt.Fprintf(&b, "  - Recommended: %s\n", clipLine(rec))
			}
			if n := q.GetRevision(); n > 0 {
				fmt.Fprintf(&b, "  - Revised %d times\n", n)
			}
		}
	}
	if len(investigate) > 0 {
		b.WriteString("\n## To investigate\n\n")
		b.WriteString("The developer asked to find out more before deciding. Dig, then `djinn question revise <question> " +
			"--wish-id <wish>` with what you found (`--context`, `--options`, `--recommendation`).\n\n")
		for _, q := range investigate {
			last := q.GetRounds()[len(q.GetRounds())-1]
			fmt.Fprintf(&b, "- **%s** %s (asked %s)", q.GetCode(), clipLine(q.GetText()), when(last.GetCreateTime().AsTime()))
			if note := last.GetNote(); note != "" {
				b.WriteString(": " + clipLine(note))
			}
			b.WriteString("\n")
		}
	}
	if len(decided) > 0 {
		slices.SortStableFunc(decided, func(x, y *planv1.Question) int {
			return y.GetAnswer().GetCreateTime().AsTime().Compare(x.GetAnswer().GetCreateTime().AsTime())
		})
		b.WriteString("\n## Latest decisions\n\n")
		for _, q := range decided[:min(len(decided), briefDecisions)] {
			fmt.Fprintf(&b, "- **%s** %s → %s", q.GetCode(), clipLine(q.GetText()), choiceText(q))
			if note := q.GetAnswer().GetNote(); note != "" {
				b.WriteString(" (" + clipLine(note) + ")")
			}
			b.WriteString("\n")
		}
	}

	if marks := marksOf(exp.GetQuestions(), exp.GetBlocks()); len(marks) > 0 {
		slices.Reverse(marks)
		b.WriteString("\n## Marked by the developer\n\n")
		for _, m := range marks[:min(len(marks), briefMarks)] {
			label := m.GetLabel()
			if m.GetBlockId() != "" {
				label = "block " + cmp.Or(m.GetTitle(), "(untitled)")
				if kind := m.GetLabel(); kind != "" {
					label += " (" + oneLine(kind) + ")"
				}
			} else if m.GetTitle() != "" {
				label += " " + m.GetTitle()
			}
			fmt.Fprintf(&b, "- **%s** %s, %s\n", markWord(m.GetMark().GetKind()), label, when(m.GetMark().GetCreateTime().AsTime()))
		}
	}

	var running, waiting, done []*planv1.Task
	for _, t := range exp.GetTasks() {
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
			running = append(running, t)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED:
			done = append(done, t)
		case planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			if render.ForkedAs(t, exp.GetTasks()) != "" {
				done = append(done, t)
			} else {
				waiting = append(waiting, t)
			}
		default:
			waiting = append(waiting, t)
		}
	}
	if len(running) > 0 {
		b.WriteString("\n## Running\n\n")
		for _, t := range running {
			fmt.Fprintf(&b, "- **%s** %s (%s", t.GetCode(), clipLine(t.GetTitle()), providerName(t.GetProvider()))
			if t.GetStatus() == planv1.TaskStatus_TASK_STATUS_PAUSED {
				b.WriteString(", paused")
			}
			if s := t.GetStartTime(); s != nil {
				b.WriteString(", since " + when(s.AsTime()))
			}
			b.WriteString(")\n")
		}
	}
	if len(waiting) > 0 {
		b.WriteString("\n## Waiting\n\n")
		for _, t := range waiting {
			fmt.Fprintf(&b, "- **%s** %s: %s\n", t.GetCode(), clipLine(t.GetTitle()), waitText(t))
		}
	}
	if len(done) > 0 {
		slices.SortStableFunc(done, func(x, y *planv1.Task) int {
			return y.GetEndTime().AsTime().Compare(x.GetEndTime().AsTime())
		})
		fmt.Fprintf(&b, "\n## Finished: %d\n\n", len(done))
		for _, t := range done[:min(len(done), briefDone)] {
			word := statusWord(t.GetStatus())
			if as := render.ForkedAs(t, exp.GetTasks()); as != "" && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
				word = "resumed as " + as
			}
			fmt.Fprintf(&b, "- **%s** %s: %s\n", t.GetCode(), clipLine(t.GetTitle()), word)
		}
	}

	blocks := slices.Clone(exp.GetBlocks())
	if len(blocks) > 0 {
		slices.SortStableFunc(blocks, func(x, y *planv1.Block) int {
			return cmp.Or(blockTime(y).Compare(blockTime(x)), strings.Compare(y.GetId(), x.GetId()))
		})
		b.WriteString("\n## Latest blocks\n")
		for _, bl := range blocks[:min(len(blocks), briefBlocks)] {
			title := cmp.Or(oneLine(bl.GetTitle()), "(untitled)")
			fmt.Fprintf(&b, "\n### %s", title)
			if kind := bl.GetKind(); kind != "" {
				b.WriteString(" (" + oneLine(kind) + ")")
			}
			b.WriteString("\n\n")
			if mt := bl.GetMediaType(); mt != "" && !strings.HasPrefix(mt, "text/") {
				fmt.Fprintf(&b, "A %s block: `djinn block list <wish>` shows it.\n", mt)
				continue
			}
			b.WriteString(clipText(bl.GetContent(), briefBlockText) + "\n")
		}
	}
	return stripCredentials(b.String())
}

// waitText says why a task that is not finished nor running waits.
func waitText(t *planv1.Task) string {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_PENDING:
		if t.GetWaitReason() != "" {
			return "planned, " + clipLine(t.GetWaitReason())
		}
		return "planned"
	case planv1.TaskStatus_TASK_STATUS_WAITING:
		return "waits for the answer to its edit question"
	case planv1.TaskStatus_TASK_STATUS_RESUMING:
		// Djinn resumes it by itself: not the lead's move.
		if t.GetWaitReason() != "" {
			return "resuming by itself, " + clipLine(t.GetWaitReason())
		}
		return "resuming by itself"
	}
	text := statusWord(t.GetStatus())
	if t.GetError() != "" {
		text += ", " + clipLine(t.GetError())
	}
	return text
}

func markWord(k planv1.MarkKind) string {
	return strings.ToLower(strings.TrimPrefix(k.String(), "MARK_KIND_"))
}

func statusWord(s planv1.TaskStatus) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "TASK_STATUS_"))
}

func providerName(p planv1.Provider) string {
	if p == planv1.Provider_PROVIDER_UNSPECIFIED {
		return "claude"
	}
	return strings.ToLower(strings.TrimPrefix(p.String(), "PROVIDER_"))
}

// choiceText is the answer of a decided question: the option chosen, or yes.
func choiceText(q *planv1.Question) string {
	c := q.GetAnswer().GetChoice()
	if c == planv1.Choice_CHOICE_YES || c == planv1.Choice_CHOICE_UNSPECIFIED {
		return "yes"
	}
	i := int(c - planv1.Choice_CHOICE_A)
	letter := string(rune('A' + i))
	if i >= 0 && i < len(q.GetOptions()) {
		return letter + ": " + clipLine(q.GetOptions()[i])
	}
	return letter
}

func blockTime(b *planv1.Block) time.Time {
	if t := b.GetUpdateTime(); t != nil {
		return t.AsTime()
	}
	return b.GetCreateTime().AsTime()
}

func when(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// oneLine puts text on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clipLine puts text on one line, cut to briefLineMax characters.
func clipLine(s string) string { return clipRunes(oneLine(s), briefLineMax) }

// clipText keeps at most n characters of text, cut at the end of a line when one is near.
func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, '\n'); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "\n…"
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// credentials are the user and password of a URL: what a remote loses before it travels.
var credentials = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]+@`)

// stripCredentials removes the credentials of every URL in text.
func stripCredentials(s string) string { return credentials.ReplaceAllString(s, "$1") }

// Brief writes the brief of a wish.
func (w *Wishes) Brief(
	ctx context.Context, req *connect.Request[planv1.WishServiceBriefRequest],
) (*connect.Response[planv1.WishServiceBriefResponse], error) {
	brief, err := BuildBrief(ctx, w.Store, w.home(), req.Msg.GetWishId())
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceBriefResponse{Text: brief.Text()}), nil
}

// home is Djinn's data folder, when the server keeps the wishes' pages there.
func (w *Wishes) home() string {
	if w.Pages == nil {
		return ""
	}
	return w.Pages.Home
}
