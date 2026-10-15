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
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/locales"
)

// Brief is where a wish stands and how to lead it, written by Djinn from the store, without a model: every lead, of
// any agent, starts from it. Moving is where the wish stands. Stable changes rarely: Djinn's rules and the rules of
// the wish's projects.
type Brief struct {
	Stable string
	Moving string
}

// Text is the whole brief: where the wish stands, its description first, then how to lead it.
func (b Brief) Text() string { return b.Moving + "\n" + b.Stable }

// How much of each section a brief holds: it starts an agent, it does not replace the page.
const (
	briefDecisions = 8
	briefDone      = 5
	briefBlocks    = 5
	briefMarks     = 10
	briefTilasms   = 20
	briefBlockText = 600
	// briefDescription is the most of a description a brief holds: a few lines.
	briefDescription = 2000
	briefLineMax     = 300
)

// ruleFiles are the files a project keeps its rules in for agents and contributors, read first.
var ruleFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md"}

// briefRules are Djinn's rules for whoever leads a wish. They name no wish: every lead reads the same text.
const briefRules = "# Leading a wish in Djinn\n\n" +
	"Djinn holds the plan of this wish: its tasks, questions, decisions and blocks. You lead it: you talk with the " +
	"developer, split the work into tasks for workers, and keep the plan true. Djinn computes the plan; you change it " +
	"with the `djinn` command, never in its data folder.\n\n" +
	"- **Start from the brief.** Djinn computes where the wish stands from its plan: `djinn wish brief <wish>`, above. " +
	"Run it when you start and whenever you lose track, then continue the wish from what it says. Another agent may " +
	"have led the wish before you: its plan carries over, its session does not.\n" +
	"- **Ask, do not guess, and never in your terminal.** Everything for the developer is a question, " +
	"`djinn question ask`, with its options and your recommendation, or with none when you really do not know what " +
	"to think; the analysis behind it goes in its `--context`. A question, a proposal or a \"shall I?\" written in " +
	"your terminal reaches neither the window, nor the decision log, nor the question workers: there, only name its " +
	"code (Q12). A decision the developer takes in the terminal gets a block of kind decision. An answered question " +
	"is a decision, resolved: do not bring it back to the developer; ask again, a new question, only on a real " +
	"doubt. A block of kind decision is a decision too. A task that follows from one names it: `--decision Q03`, or " +
	"the block's id.\n" +
	"- **Delegate every code change.** Never author source or test changes yourself, including small fixes, review " +
	"findings or failed checks. You lead; workers do the work: code, investigations, fixes and checks go to tasks, " +
	"even small ones, several side by side. Send the work to an existing worker with `djinn task send`, or create a " +
	"worker with `djinn task spawn`. You frame, coordinate, review, integrate and verify their work.\n" +
	"- **Durable developer instructions.** Reflect, delegate to a worker task of the same wish, then verify its result " +
	"and explicitly complete the instruction. A worker ending never completes an instruction automatically. Append a " +
	"concise informational reply with `djinn instruction reply <instruction> \"…\" --wish-id <wish>`; a reply keeps " +
	"the instruction's status and worker unchanged.\n" +
	"- **Workers start from a short prompt.** Say what to do, in which project, and how to check it. " +
	"`--fork <task>` or `--from-lead` start a worker from a copy of a conversation instead: it reads that context " +
	"again at every turn, so use them only when the whole context is needed.\n" +
	"- **To follow up on a task, continue it**: `djinn task continue <task> --prompt \"…\"` gives a task that " +
	"ended, failed, stopped or was cut short a new turn of its own session, in its worktree, as the same task. " +
	"Fork it only to start a different task from its context: a fork of a task cut short closes it, " +
	"\"continued in\" the fork.\n" +
	"- **The plan is a graph of azimas.** An azima (`T07`) is a task of the plan that no worker runs, nothing waits " +
	"for the developer on it. Work is part of an azima and waits only for what it depends on, so plan it as a graph, " +
	"never a line: spawn each task `--part-of <azima>`, and `--after` only the tasks whose result it needs, " +
	"several if need be. Two tasks that do not need each other run side by side. Work on the ready azimas first; an " +
	"azima is done when you mark it done (`djinn task done`) or its plan file says so. One whose work is done and " +
	"whose plan file's unchecked boxes all say `(needs: …)` waits for its proof, from a person, a machine, a release " +
	"or a real model: spawn no work for it. `djinn task depend` and " +
	"`djinn task group` re-sequence the plan as it learns; Djinn refuses a cycle.\n" +
	"- **An azima carries one clear goal**: one sentence a user reads as a feature, a will of its own. When a task " +
	"extends it, rephrase its goal (its plan file's Goal and its title, then `djinn plan sync`) rather than let it " +
	"drift. Before you add tasks or azimas, an answer turned into tasks included, look for the azima they belong to: " +
	"group what goes together, merge duplicates, and open a new azima only for a will no existing one carries; " +
	"never mix things that do not belong together.\n" +
	"- **Give a task its place when you spawn it.** What comes before it: `--after W1,W2`. To put a new task before " +
	"a planned one, spawn it `--blocks W5`: W5 waits for it from the same step. Never spawn, then depend: a pass of " +
	"the scheduler may start W5 in between. Djinn refuses `--blocks` on a task that has started.\n" +
	"- **Djinn acts on the developer's answers.** An answer starts a question worker, \"Q03 → tasks\": it turns " +
	"the decision into tasks (`--decision Q03`), or asks what the answer leaves open. \"Enlighten me\" starts " +
	"\"Q03: enlighten\": it reads, then revises the question. You are told when each one starts and ends, and what it " +
	"spawned, asked or revised: check it, do not do it again. When the line says \"Act on it\" or \"Investigate\", " +
	"question workers are off and the move is yours.\n" +
	"- **Blocks are for agents**: a hand-off, a reference, a decision taken outside a question (kind decision, which " +
	"the decision log shows). The developer does not read blocks: what the developer must see is a question.\n" +
	"- **To explain a concept, write a support** (a tilasm; the developer may say document or talisman): a Markdown " +
	"document kept by Djinn outside the projects, which the developer reads, annotates and edits in the window. One " +
	"support per subject to work on together (a brief, a spec, a comparison, a data model walked through), not one " +
	"per task or answer: look first with `djinn doc list --wish <wish> --search \"…\"`, and reuse one that fits, " +
	"`djinn doc put brief.md --wish <wish> --code L01`, a new version, its link unchanged. Else " +
	"`djinn doc put brief.md --wish <wish> --cites T07` makes it, `L01`, and names the azimas and tasks it explains. " +
	"Cite it in blocks and questions by its link, `djinn://tilasm/<id>`, rather than explaining again; " +
	"`--tilasm L01` gives it to a worker as context. An HTML page only when the developer asks for one: " +
	"`djinn doc put <folder>` with an `index.html` and its sources; draw its diagrams as inline SVG laid out by hand " +
	"(boxes, labelled arrows, a legend), in colours that read in light and dark, never Mermaid. A tilasm reaches no " +
	"network: everything it needs, fonts and scripts, is in its folder.\n" +
	"- **Each Markdown support has its own document agent.** Every comment or request the developer writes in a " +
	"support's discussions wakes it (a light task, \"L01: document agent\"): it answers in the thread, proposes the " +
	"changes asked for, and tells you in one line (`djinn wish tell`) when a decision there matters for the rest of " +
	"the wish. Leave those discussions to it: answer one yourself only when the developer or its line asks you to, or " +
	"when question workers are off.\n" +
	"- **When you discuss a support**: read its discussions with `djinn doc discussions L01`, and answer " +
	"in the thread (`djinn doc reply <thread> --text \"…\"`) with what `djinn doc context L01 --thread <thread>` gives. " +
	"To change a document the developer works on, propose rather than rewrite it: `djinn doc propose L01 --thread " +
	"<thread> --replacement \"…\"` (or `--quote \"…\"` for another passage) waits for the developer, who accepts or " +
	"rejects it in the window; you never accept a proposal, and a discussion alone never changes the document.\n" +
	"- **No secret, no local path** in the plan: name the project.\n" +
	"- **Every request finds its wish.** A request that is not about this wish goes through " +
	"`djinn wish route \"<request>\" --wish-id <wish> --ask`: Djinn asks the developer, on a card, to open a new wish " +
	"with its own lead, to file it in an existing wish, or to keep it in this one. Hand it over: do not do its work " +
	"here, unless the developer keeps it here.\n" +
	"- **Watch, do not poll.** To wait on something outside (a pipeline, a merge request, a queue), spawn a watcher: " +
	"`--provider watch --prompt \"<command>\"` runs the command with no agent, no model and no slot, and each new " +
	"paragraph it prints wakes you. `--restart` starts again a command that exits on each change. The project must " +
	"allow the command.\n" +
	"- **A request that comes back is a template.** A skill holds one under `metadata.djinn.wish` in its `SKILL.md` " +
	"(`title`, `match`, `watch`, `restart`, `done_when`): a routed request it matches becomes a wish that follows the " +
	"skill, its watcher started. When the developer asks for the same kind of work again, propose one. " +
	"`djinn skill list` shows the templates, or why one cannot be used.\n\n" +
	"## Commands\n\n" +
	"`<wish>` is the wish's identifier, given above.\n\n" +
	"- `djinn wish brief <wish>`: this brief, up to date.\n" +
	"- `djinn wish describe <wish> --text \"…\"`: the wish's description, a few lines: what it is for, its scope, " +
	"where it goes. The developer edits it in the window too.\n" +
	"- `djinn question ask \"<question>\" <wish> --options \"…\" --options \"…\" --recommendation \"…\" --icon 🔒` (one " +
	"emoji for the subject); `--before \"before the merge\"` says what the answer is needed before, without it the " +
	"question can wait (a question a waiting task needs is blocking anyway); " +
	"`djinn question list --wish-id <wish> --open`.\n" +
	"- `djinn task spawn <wish> --title \"…\" --prompt \"…\" --part-of T07 --after W1,W2 --blocks W5` (`--project-id`, " +
	"`--later`, `--fork W1`, `--from-lead`, `--provider watch`, `--restart`, `--decision Q03`, `--tilasm L01`); " +
	"`djinn task spawn <wish> --kind azima --title \"…\" --after T02` makes an azima; " +
	"`djinn task depend <task> --after W1,T02 --also W6=W5` sets what tasks wait for, in place of what they had, " +
	"all or none; " +
	"`djinn task group <task> --part-of T07` sets its azima; " +
	"`djinn plan sync <wish>` reads the azimas from the projects' plan files and writes their `after:` lines back; " +
	"`djinn task list --wish-id <wish>`; `djinn task watch <task>`; " +
	"`djinn task send <task> \"…\"`, an instruction for a running worker: \"received\" shows once it took it in; " +
	"`djinn task stop <task>`; `djinn task continue <task> --prompt \"…\"`; `djinn task done <task> --note " +
	"\"…\"` closes a task no worker runs (planned, cut short, failed, stopped, imported) once its work is done " +
	"elsewhere.\n" +
	"- `djinn instruction list <wish>`: every instruction in full; `djinn instruction reflect <instruction> --wish-id <wish>`; " +
	"`djinn instruction assign <instruction> <worker-task> --wish-id <wish>`; " +
	"`djinn instruction complete <instruction> --wish-id <wish>` after verifying the successful worker result. " +
	"`djinn instruction reply <instruction> \"…\" [--wish-id <wish>]` appends a reply of at most 2000 characters " +
	"without changing status or assignment. Use an instruction UUID or its I01 code; a code shared by wishes needs " +
	"`--wish-id`; tasks use a UUID or W1 code.\n" +
	"- `djinn wish route \"<request>\" --wish-id <wish> --ask`: where a request goes, asked to the developer on a " +
	"card; without `--ask`, the proposal only.\n" +
	"- `djinn block put <wish> --kind decision --title \"…\" --content \"…\" --icon 🧱`; `djinn block list <wish>`.\n" +
	"- `djinn doc put brief.md --wish <wish> --cites T07 --code L01`; `djinn doc create \"<title>\" --wish <wish> " +
	"--markdown \"…\"`; `djinn doc list --wish <wish> --search \"…\"`; `djinn doc get <code>` (its Markdown, the " +
	"folder of its files); `djinn doc history <code>`, `djinn doc restore <code> <version>`.\n" +
	"- `djinn doc discussions <code>`: its discussions and proposals, each passage's state (anchored, changed, missing, " +
	"ambiguous); `djinn doc context <code> --thread <thread>` (or `--proposal <id>`, `--quote \"…\"`): the passage, " +
	"its Markdown source, its paragraphs, the messages and the document; `djinn doc comment <code> --quote \"…\" " +
	"--text \"…\"` (`--prefix`/`--suffix` when the quote is there twice); `djinn doc reply <thread> --text \"…\"`; " +
	"`djinn doc propose <code> --thread <thread> --replacement \"…\"` (or `--quote \"…\"`, `--replacement-file x.md`, " +
	"`--delete`; an empty quote with `--prefix`/`--suffix` inserts there): the replacement replaces the passage's Markdown " +
	"source, markup included. Give `--request-id` to a write you may retry: sent again, it does nothing twice.\n" +
	"- `djinn question enlighten <question>` is the developer's \"tell me more\": the question waits for a " +
	"`djinn question revise <question> --context \"…\" --recommendation \"…\"`, by its question worker or by you.\n" +
	"- `djinn mark list <wish>`: the open questions the developer marked read in the window, without a word. Start a " +
	"recommendation with its option's letter (`B: …`): the developer approves it in one click.\n" +
	"- `djinn wish sync <wish>`: the wish's page, which Djinn keeps up to date in the file it prints. After the wish " +
	"changes, republish that file as it is, in one call, to the same address: do not read it, rewrite it, or write " +
	"HTML by hand. The first publish needs the developer's go. `djinn wish render <wish>` writes the page once.\n" +
	"- `djinn wish grant <wish>` is the developer's word, never yours.\n"

// BuildBrief writes the brief of a wish from what r holds. home is Djinn's data folder: like the projects' folders
// and the home folder, it never shows. No secret: Djinn stores none, and a URL loses its credentials.
func BuildBrief(ctx context.Context, r store.Reader, home, wishID string) (Brief, error) {
	exp, projects, err := collect(ctx, r, wishID, noEvents)
	if err != nil {
		return Brief{}, err
	}
	all, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return Brief{}, err
	}
	wish := exp.GetWish()
	rank := wish.GetRank()
	ready := wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions(), exp.GetInstructions()...)
	exp = portable(exp, newScrubber(all, home, dataName))
	return Brief{Stable: stableBrief(home, projects), Moving: movingBrief(exp, rank, ready)}, nil
}

// StableBrief is the part of a brief that changes rarely, for a wish on projects: Djinn's rules, then where each
// project keeps its own, and the checks Djinn runs on its work, as its settings say with Djinn's data folder home. It
// names the projects, never their folders.
// StableBrief accepts both the current (home, projects) form and the earlier
// projects-only form retained for callers from the harness-friendly branch.
func StableBrief(first any, rest ...[]*planv1.Project) string {
	var home string
	var projects []*planv1.Project
	switch v := first.(type) {
	case string:
		home = v
		if len(rest) > 0 {
			projects = rest[0]
		}
	case []*planv1.Project:
		projects = v
	}
	return stableBrief(home, projects)
}

func stableBrief(home string, projects []*planv1.Project) string {
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
		if settings, err := LoadSettings(home, p); err == nil && settings.ChecksBrief() != "" {
			b.WriteString("  " + settings.ChecksBrief() + " Djinn says so in each worker's first prompt.\n")
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

// movingBrief is where the wish stands: its description, its azimas, what runs and waits, its tilasms, its open
// questions, its latest decisions and blocks, then the last lead and when a lead last acted. Empty sections are left
// out.
func movingBrief(exp *planv1.WishExport, rank int32, ready bool) string {
	wish := exp.GetWish()
	var b strings.Builder
	fmt.Fprintf(&b, "# The wish: %s\n\n", oneLine(wish.GetTitle()))
	if desc := strings.TrimSpace(wish.GetDescription()); desc != "" {
		b.WriteString(clipText(desc, briefDescription) + "\n\n")
	}
	fmt.Fprintf(&b, "- Identifier: `%s`, the `<wish>` of the commands below.\n", wish.GetId())
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
	how := "chosen with the wish"
	for _, c := range exp.GetCommands() {
		if c.GetMethod() == planv1connect.WishServiceSetProviderProcedure {
			how = "since " + when(c.GetAt().AsTime()) + ", when the developer changed the wish's agent: you take over " +
				"from a lead of another agent whose conversation did not pass to you. This brief, the blocks and the tasks' " +
				"results hold the thread"
		}
	}
	fmt.Fprintf(&b, "- Agent: %s, %s. You run it, and so does every task you spawn without `--provider`; give `--provider` for a task that needs another.\n", providerName(WishProvider(wish)), how)
	if t := wish.GetTemplate(); t != nil {
		fmt.Fprintf(&b, "- Made from the wish template of the skill `%s`: follow that skill.", t.GetSkill())
		if t.GetWatch() != "" {
			fmt.Fprintf(&b, " Its watcher runs `%s` and wakes you on each change.", stripCredentials(t.GetWatch()))
		}
		if t.GetDoneWhen() != "" {
			fmt.Fprintf(&b, " When it prints `%s`, Djinn asks the developer whether to grant the wish.", t.GetDoneWhen())
		}
		b.WriteString("\n")
	}
	if ready {
		b.WriteString("- Djinn proposes to grant it: every task is finished, no question is open and every developer instruction is done. Granting is the developer's word.\n")
	}

	// Keep the complete original request available to every new lead, even after it falls out of the latest blocks.
	originals := map[string]bool{}
	for _, bl := range exp.GetBlocks() {
		if !strings.EqualFold(bl.GetKind(), creationKind) || originals[bl.GetContent()] {
			continue
		}
		if len(originals) == 0 {
			b.WriteString("\n## Original request\n\n")
		}
		originals[bl.GetContent()] = true
		b.WriteString(bl.GetContent() + "\n")
	}
	if len(originals) > 0 && needsTitle(exp) {
		b.WriteString("\nAt startup, write a concise title in the developer's language, using the language of the complete original request above when no preference is given, then run `djinn wish rename <wish> \"<title>\"`. The current title is provisional; keep the original request whole.\n")
	}
	var unfinished []*planv1.Instruction
	for _, instruction := range exp.GetInstructions() {
		if instruction.GetStatus() != planv1.InstructionStatus_INSTRUCTION_STATUS_DONE {
			unfinished = append(unfinished, instruction)
		}
	}
	if len(unfinished) > 0 {
		b.WriteString("\n## Developer instructions\n\nEvery instruction below is unfinished. Full text is included without truncation; `djinn instruction list <wish>` also reads it. Reflect, delegate to a same-wish worker, verify, then explicitly complete.\n")
		for _, instruction := range unfinished {
			fmt.Fprintf(&b, "\n### %s (%s) — %s\n\n", instruction.GetCode(), instruction.GetId(), instructionStatusWord(instruction.GetStatus()))
			if instruction.GetTaskId() != "" {
				fmt.Fprintf(&b, "Worker task: `%s`. Read its result with `djinn task get` and `djinn task watch`.\n\n", instruction.GetTaskId())
			}
			b.WriteString(instruction.GetText() + "\n")
		}
	}

	codes := map[string]string{}
	for _, t := range exp.GetTasks() {
		codes[t.GetId()] = t.GetCode()
	}
	var tilasms []*planv1.Tilasm
	for _, t := range exp.GetTilasms() {
		tilasms = append(tilasms, t.GetTilasm())
	}
	azimasBrief(&b, exp.GetTasks(), codes, tilasms)
	var running, waiting, done []*planv1.Task
	for _, t := range exp.GetTasks() {
		if IsAzima(t) {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
			running = append(running, t)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED,
			planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
			// Cut short and not resumed by Djinn, which resumes every task it can: history.
			done = append(done, t)
		default:
			waiting = append(waiting, t)
		}
	}
	// What moves or waits, by status, as the window and the page show it.
	slices.SortStableFunc(running, render.ByMotion)
	slices.SortStableFunc(waiting, render.ByMotion)
	if len(running) > 0 {
		b.WriteString("\n## Running\n\n")
		for _, t := range running {
			fmt.Fprintf(&b, "- **%s** %s (%s", t.GetCode(), clipLine(t.GetTitle()), providerName(t.GetProvider()))
			if e := codes[t.GetPartOf()]; e != "" {
				b.WriteString(", part of " + e)
			}
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
			fmt.Fprintf(&b, "- **%s** %s", t.GetCode(), clipLine(t.GetTitle()))
			if e := codes[t.GetPartOf()]; e != "" {
				b.WriteString(" (part of " + e + ")")
			}
			b.WriteString(": " + waitText(t) + "\n")
		}
	}
	if len(done) > 0 {
		slices.SortStableFunc(done, render.NewestEnded)
		fmt.Fprintf(&b, "\n## Finished: %d, the latest\n\n", len(done))
		for _, t := range done[:min(len(done), briefDone)] {
			word := statusWord(t.GetStatus())
			if as := render.ForkedAs(t, exp.GetTasks()); as != "" && t.GetStatus() == planv1.TaskStatus_TASK_STATUS_INTERRUPTED {
				word = "resumed as " + as
			}
			if work := workText(t, codes); work != "" {
				word = work
			}
			fmt.Fprintf(&b, "- **%s** %s: %s", t.GetCode(), clipLine(t.GetTitle()), word)
			if c := t.GetClosed(); c != nil {
				b.WriteString(", closed by the " + CloserWord(c.GetActor()))
				if n := clipLine(c.GetNote()); n != "" {
					b.WriteString(": " + n)
				}
			}
			b.WriteString("\n")
		}
	}

	tilasmsBrief(&b, exp.GetTilasms())

	var open, investigate []*planv1.Question
	for _, q := range exp.GetQuestions() {
		if Investigating(q) {
			investigate = append(investigate, q)
		} else if q.GetAnswer() == nil {
			open = append(open, q)
		}
	}
	if len(open) > 0 {
		b.WriteString("\n## Open questions\n\n")
		render.ByUrgency(open, exp.GetTasks())
		blocked := render.Blocked(exp.GetTasks())
		for _, q := range open {
			level := "can wait"
			switch render.UrgencyOf(q, blocked) {
			case render.Blocking:
				level = "blocking: " + strings.Join(blocked[q.GetId()], ", ") + " waits"
			case render.Before:
				level = clipLine(q.GetBefore())
			}
			fmt.Fprintf(&b, "- **%s** %s (%s)\n", q.GetCode(), clipLine(q.GetText()), level)
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
			"--wish-id <wish>` with what you found (`--context`, `--options`, `--recommendation`, `--before`).\n\n")
		for _, q := range investigate {
			last := q.GetRounds()[len(q.GetRounds())-1]
			fmt.Fprintf(&b, "- **%s** %s (asked %s)", q.GetCode(), clipLine(q.GetText()), when(last.GetCreateTime().AsTime()))
			if note := last.GetNote(); note != "" {
				b.WriteString(": " + clipLine(note))
			}
			b.WriteString("\n")
		}
	}
	if decided := render.Decisions(exp); len(decided) > 0 {
		b.WriteString("\n## Latest decisions\n\n")
		for _, d := range decided[:min(len(decided), briefDecisions)] {
			b.WriteString("- " + d.Icon + " ")
			if q := d.Question; q != nil {
				fmt.Fprintf(&b, "**%s** %s → %s", q.GetCode(), clipLine(q.GetText()), choiceText(q))
				if note := q.GetAnswer().GetNote(); note != "" {
					b.WriteString(" (" + clipLine(note) + ")")
				}
			} else {
				fmt.Fprintf(&b, "**%s** (block %s)", cmp.Or(clipLine(d.Block.GetTitle()), "(untitled)"), d.Block.GetId())
			}
			b.WriteString(", " + deciderText(d))
			if len(d.Tasks) > 0 {
				b.WriteString("; led to " + strings.Join(d.Tasks, ", "))
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
	lastLeadBrief(&b, exp)
	return stripCredentials(b.String())
}

// needsTitle only asks the lead to name a prompt-only creation that has never been renamed.
func needsTitle(exp *planv1.WishExport) bool {
	needed := false
	for _, c := range exp.GetCommands() {
		if c.GetMethod() == planv1connect.WishServiceRenameProcedure {
			return false
		}
		if c.GetMethod() == planv1connect.WishServiceMakeProcedure {
			var m planv1.WishServiceMakeRequest
			if err := c.GetRequest().UnmarshalTo(&m); err == nil {
				needed = m.GetTitle() == "" && m.GetPrompt() != ""
			}
		}
	}
	return needed
}

// leadMethods are the commands a lead gives, as its rules list them, whatever its agent: the latest says when a lead
// last acted. The developer's own commands (answers, marks, pauses) are not among them.
var leadMethods = map[string]bool{
	planv1connect.TaskServiceSpawnProcedure:      true,
	planv1connect.TaskServiceContinueProcedure:   true,
	planv1connect.TaskServiceDependProcedure:     true,
	planv1connect.TaskServiceGroupProcedure:      true,
	planv1connect.BlockServicePutProcedure:       true,
	planv1connect.QuestionServiceAskProcedure:    true,
	planv1connect.QuestionServiceReviseProcedure: true,
	planv1connect.WishServiceRouteProcedure:      true,
	planv1connect.PlanServiceSyncProcedure:       true,
}

// lastLeadBrief writes the wish's last recorded lead, its agent and its session, and when a lead last acted on the
// wish through Djinn, from the journal: a lead of another agent knows whom it takes over from. Nothing when the wish
// never had a lead.
func lastLeadBrief(b *strings.Builder, exp *planv1.WishExport) {
	lead := exp.GetWish().GetLead()
	var recorded, acted *planv1.Command
	for _, c := range exp.GetCommands() {
		req, err := c.GetRequest().UnmarshalNew()
		if err != nil {
			continue
		}
		switch m := req.(type) {
		case *planv1.WishServiceSetLeadRequest:
			if lead.GetSessionId() != "" && m.GetSessionId() == lead.GetSessionId() {
				recorded = c
			}
		case *planv1.BlockServicePutRequest:
			if m.GetTaskId() == "" { // A worker's block names its task.
				acted = c
			}
		default:
			if leadMethods[c.GetMethod()] {
				acted = c
			}
		}
	}
	if lead.GetSessionId() == "" && acted == nil {
		return
	}
	b.WriteString("\n## The last lead\n\n")
	if lead.GetSessionId() != "" {
		fmt.Fprintf(b, "- %s, session `%s`", providerName(lead.GetProvider()), lead.GetSessionId())
		if recorded != nil {
			b.WriteString(", recorded " + when(recorded.GetAt().AsTime()))
		}
		b.WriteString(": `djinn wish resume` takes it back. A lead of another agent starts from this brief.\n")
	} else {
		b.WriteString("- No lead session recorded: Djinn records claude's; a codex lead needs `djinn wish set-lead`.\n")
	}
	if acted != nil {
		fmt.Fprintf(b, "- A lead last acted %s: `djinn %s`.\n", when(acted.GetAt().AsTime()), commandWords(acted.GetMethod()))
	}
}

// commandWords is a Connect procedure as the command line names it: /plan.v1.TaskService/Spawn is task spawn.
func commandWords(procedure string) string {
	service, method, _ := strings.Cut(strings.TrimPrefix(procedure, "/"), "/")
	service = strings.TrimSuffix(service[strings.LastIndexByte(service, '.')+1:], "Service")
	return strings.ToLower(service) + " " + strings.ToLower(method)
}

// azimasBrief writes the plan's azimas as a graph: the ready ones first, under way before open, then the blocked ones
// with what they wait for, then those whose work is done and that wait for their proof, with who gives it, then the
// done ones on one line; each with the tilasms that explain it. Nothing without an azima.
func azimasBrief(b *strings.Builder, tasks []*planv1.Task, codes map[string]string, tilasms []*planv1.Tilasm) {
	var ready, blocked, proof, done []*planv1.Task
	for _, t := range WithAzimas(tasks) {
		switch e := t.GetAzima(); {
		case !IsAzima(t):
		case e.GetState() == planv1.AzimaState_AZIMA_STATE_DONE:
			done = append(done, t)
		case e.GetState() == planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF:
			proof = append(proof, t)
		case e.GetReady():
			ready = append(ready, t)
		default:
			blocked = append(blocked, t)
		}
	}
	if len(ready)+len(blocked)+len(proof)+len(done) == 0 {
		return
	}
	byCode := func(a, b *planv1.Task) int { return CompareCodes(a.GetCode(), b.GetCode()) }
	slices.SortFunc(ready, func(a, b *planv1.Task) int {
		under := func(t *planv1.Task) int {
			return b2i(t.GetAzima().GetState() != planv1.AzimaState_AZIMA_STATE_IN_PROGRESS)
		}
		return cmp.Or(cmp.Compare(under(a), under(b)), byCode(a, b))
	})
	slices.SortFunc(blocked, byCode)
	slices.SortFunc(proof, byCode)
	slices.SortFunc(done, byCode)
	b.WriteString("\n## Azimas\n\n")
	b.WriteString("The plan as a graph, the ready azimas first. Spawn their work `--part-of <azima>`.\n\n")
	status := map[*planv1.Task]string{}
	for _, t := range append(slices.Clone(ready), blocked...) {
		e := t.GetAzima()
		text := "open"
		if e.GetState() == planv1.AzimaState_AZIMA_STATE_IN_PROGRESS {
			text = "in progress"
		}
		if n := e.GetParts(); n > 0 {
			text += fmt.Sprintf(", %d of %d parts done", e.GetPartsDone(), n)
			if r := e.GetPartsRunning(); r > 0 {
				text += fmt.Sprintf(", %d running", r)
			}
		}
		if ls := citing(tilasms, t.GetId()); len(ls) > 0 {
			text += "; explained by " + strings.Join(ls, ", ")
		}
		status[t] = text
	}
	byID := map[string]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
	}
	after := func(t *planv1.Task) (all, waits []string) {
		for _, id := range t.GetDependsOn() {
			code := codes[id]
			all = append(all, code)
			if byID[id].GetStatus() != planv1.TaskStatus_TASK_STATUS_DONE {
				waits = append(waits, code)
			}
		}
		slices.SortFunc(all, CompareCodes)
		slices.SortFunc(waits, CompareCodes)
		return all, waits
	}
	for _, t := range ready {
		fmt.Fprintf(b, "- **%s** %s: ready, %s\n", t.GetCode(), clipLine(t.GetTitle()), status[t])
	}
	for _, t := range blocked {
		all, waits := after(t)
		fmt.Fprintf(b, "- **%s** %s: waits for %s", t.GetCode(), clipLine(t.GetTitle()), strings.Join(waits, ", "))
		if len(all) > len(waits) {
			b.WriteString(" (after " + strings.Join(all, ", ") + ")")
		}
		b.WriteString("; " + status[t] + "\n")
	}
	if len(proof) > 0 {
		// No work is left in them: a person, a machine, a release or a real model gives the proof.
		needs := make([]string, len(proof))
		for i, t := range proof {
			needs[i] = t.GetCode() + " needs " + NeedsWords(t.GetProofNeeds())
			if ls := citing(tilasms, t.GetId()); len(ls) > 0 {
				needs[i] += " (explained by " + strings.Join(ls, ", ") + ")"
			}
		}
		fmt.Fprintf(b, "- Work done, waiting for its proof: %s.\n", strings.Join(needs, "; "))
	}
	if len(done) > 0 {
		names := make([]string, len(done))
		for i, t := range done {
			names[i] = t.GetCode()
			if ls := citing(tilasms, t.GetId()); len(ls) > 0 {
				names[i] += " (explained by " + strings.Join(ls, ", ") + ")"
			}
		}
		fmt.Fprintf(b, "- Done: %s.\n", strings.Join(names, ", "))
	}
}

// tilasmsBrief lists the wish's tilasms, in code order: each one's code, title, link and what it explains.
func tilasmsBrief(b *strings.Builder, tilasms []*planv1.TilasmExport) {
	if len(tilasms) == 0 {
		return
	}
	b.WriteString("\n## Tilasms\n\n")
	b.WriteString("The supports that explain the wish: reuse one before writing another. `djinn doc get <code>` gives " +
		"one's text and the folder of its files; cite one by its link.\n\n")
	for _, t := range tilasms[:min(len(tilasms), briefTilasms)] {
		tilasm := t.GetTilasm()
		fmt.Fprintf(b, "- **%s** %s: %s", tilasm.GetCode(), cmp.Or(clipLine(tilasm.GetTitle()), "(untitled)"),
			TilasmLink(tilasm.GetId()))
		if codes := t.GetCiteCodes(); len(codes) > 0 {
			b.WriteString(", explains " + strings.Join(codes, ", "))
		}
		b.WriteString(discussionsLine(t))
		b.WriteString("\n")
	}
	if n := len(tilasms) - briefTilasms; n > 0 {
		fmt.Fprintf(b, "- And %d more: `djinn doc list --wish <wish>`.\n", n)
	}
}

// discussionsLine says what of a tilasm's discussions and proposals waits: its open discussions and pending proposals.
func discussionsLine(t *planv1.TilasmExport) string {
	open, pending := 0, 0
	for _, thread := range t.GetThreads() {
		if !thread.GetResolved() {
			open++
		}
	}
	for _, p := range t.GetProposals() {
		if p.GetStatus() == planv1.TilasmProposalStatus_TILASM_PROPOSAL_STATUS_PENDING {
			pending++
		}
	}
	var parts []string
	if open > 0 {
		parts = append(parts, fmt.Sprintf("%d open discussion%s", open, plural(open)))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d proposal%s waiting for the developer", pending, plural(pending)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}

// CloserWord names who marked a task done by hand.
func CloserWord(c planv1.Closer) string {
	switch c {
	case planv1.Closer_CLOSER_DEVELOPER:
		return "developer"
	case planv1.Closer_CLOSER_PLAN_FILE:
		return "plan file"
	}
	return "lead"
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

// workText says where the finished work of t stands on its way into its wish's integration branch, as the page
// says it, in lower case: "committed into feat/x as 1a2b3c4d"; "" for work Djinn does not integrate. codes are the
// codes of the wish's tasks, by identifier.
func workText(t *planv1.Task, codes map[string]string) string {
	text, _ := render.Work(t, func(id string) string { return codes[id] }, render.Translator(locales.Source))
	if text == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(text)
	return clipLine(string(unicode.ToLower(r)) + text[n:])
}

// deciderText says who took a decision: the developer, by an answer or an approval, or an agent.
func deciderText(d render.Decided) string {
	switch {
	case d.Approved:
		return "approved by the developer"
	case d.Human:
		return "by the developer"
	case d.By == render.ByLead:
		return "by the lead"
	}
	return "by " + d.By
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

// choiceText is the answer of a decided question: the custom response, the option chosen, or yes.
func choiceText(q *planv1.Question) string {
	c := q.GetAnswer().GetChoice()
	if c == planv1.Choice_CHOICE_UNSPECIFIED {
		return "custom response"
	}
	if c == planv1.Choice_CHOICE_YES {
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
