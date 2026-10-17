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
	"github.com/empowill/djinn/lead/notes"
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

// installMoveRule formats the move rule for install and restart based on each project's install_questions setting.
func installMoveRule(projects []*planv1.Project, home string) string {
	if len(projects) == 0 {
		return "grant a right, install and restart"
	}
	sorted := slices.Clone(projects)
	slices.SortFunc(sorted, func(x, y *planv1.Project) int {
		return cmp.Compare(strings.ToLower(x.GetName()), strings.ToLower(y.GetName()))
	})
	var parts []string
	seen := make(map[string]bool)
	for _, p := range sorted {
		if p.GetName() == "" || seen[p.GetName()] {
			continue
		}
		seen[p.GetName()] = true
		iq := planv1.InstallQuestions_INSTALL_QUESTIONS_ALWAYS
		if s, err := LoadSettings(home, p); err == nil && s.InstallQuestions != planv1.InstallQuestions_INSTALL_QUESTIONS_UNSPECIFIED {
			iq = s.InstallQuestions
		}
		parts = append(parts, fmt.Sprintf("%s: %s", p.GetName(), InstallQuestionsWord(iq)))
	}
	if len(parts) == 0 {
		return "grant a right, install and restart"
	}
	return fmt.Sprintf("grant a right, install and restart (%s)", strings.Join(parts, ", "))
}

// DirectCommandsRule is how a lead runs djinn: one direct command at a time. The first message of every lead says it,
// and the brief at its start, in its rules and at its end, where a tool that cuts a long output keeps it.
const DirectCommandsRule = "Run each `djinn` command alone, as one direct command: never a pipe (`|`), a chain " +
	"(`&&`, `||`, `;`), a loop (`for`), a redirection (`>`, `<<`) nor a substitution (`$(...)`): any of them makes " +
	"the developer approve the command by hand."

// DirectFlags are the flags that replace what a lead would pipe or loop over.
const DirectFlags = "Flags replace them: `djinn brief --wishes` for the wishes alone, `djinn lead notes a,b` or " +
	"`--all` for several notes, `djinn task list --wish-id <wish> --short` for where the tasks stand, " +
	"`djinn task watch <task> --last 5` for a task's final note (it returns at once, the task running or not), " +
	"`djinn question get <question> --wish-id <wish>` for one question, its answer and note, " +
	"`djinn question list --wish-id <wish> --open`."

// IfritRule is how the lead uses an ifrit (T51, decision Q11): a worker that stays lit while the developer is in
// Djinn, for live iteration only, one per wish, its requests relayed, finished once its work is good.
const IfritRule = "- **An ifrit for live iteration (Q11).** An ifrit is a worker that stays lit while the developer " +
	"is in Djinn, listening between their requests without a slot or a token: `djinn task spawn <wish> --kind ifrit " +
	"--title \"…\" --prompt \"…\"` (`--idle-minutes 60` when they say they leave to try it for an hour), in a project. " +
	"Light one when the developer wants to iterate live (changes they judge by eye) or try a stack; never for work a " +
	"task with its checks does alone. One lit per wish. While it burns, relay each of the developer's requests to it, " +
	"short and concrete, with `djinn task send <ifrit> \"…\"`, instead of spawning tasks; ask a question about it with " +
	"`--about <ifrit>`, which shows it in the ifrit's band; do not repeat its reply to the developer, who follows it in " +
	"the window. When a task waits for a gate it holds, offer the developer to stop it (`djinn task stop`) or finish " +
	"it. Out (the developer left Djinn, or Djinn restarted), a message would light it again: never send it one " +
	"without the developer's word. Once the developer judges its work good, finish it, `djinn task done <ifrit> " +
	"--note \"…\"`: its work integrates like a task's, through the commit checks (`djinn lead notes ifrit`).\n"

// briefRules are Djinn's rules for whoever leads the wishes. They name no wish: every lead reads the same text.
func briefRules(projects []*planv1.Project, home string) string {
	return "# Leading in Djinn\n\n" +
		"Djinn holds the active wishes and their plans: tasks, questions, decisions and blocks. You lead them: you talk with the " +
		"developer, split the work into tasks for workers, and keep the plans true. Djinn computes the plans; you change them " +
		"with the `djinn` command, never in its data folder.\n\n" +
		"- **You are the Djinn.** In Djinn, the lead is called the Djinn, and you alone bear that name: the one of your " +
		"kind who lives in the lamp, the developer's single voice for every wish. Workers are your hands, never Djinns; " +
		"the app is \"Djinn\", you are \"the Djinn\". Speak of yourself that way in questions and blocks (\"the Djinn " +
		"recommends…\"). The commands keep their names: `djinn lead …` is yours.\n" +
		"- **Say nothing in your terminal, ever.** The developer never reads your terminal: whatever you write there " +
		"is lost to them, and costs output tokens for nothing. A turn ends with no text at all: no recap, no summary, no " +
		"question codes, no \"done\". Assume they never answer there either, even when they do. Everything for the " +
		"developer goes through questions in the window, in the wish concerned, statements included: a report, a " +
		"finding, a status, \"done as you asked\" is a question too, often a yes/no acknowledgement with \"OK, nothing " +
		"to do\" as an option; their answer tells you nothing is left. A real wait on the developer is a `--move` " +
		"question that says so plainly. Never two questions for one gesture; withdraw a question (`djinn question " +
		"withdraw`) the moment a task or Djinn does what it asked. A line the developer types in your terminal is a " +
		"request like any other: answer it in the window, never in the terminal, and a decision taken there gets a " +
		"block of kind decision.\n" +
		"- **Start from the brief.** Djinn computes where the active wishes stand: `djinn brief`. " +
		"Run it when you start and whenever you lose track, then continue from what it says. `djinn wish brief <wish>` gives one wish's detail. Another agent may " +
		"have led before you: the plan carries over, its session does not.\n" +
		"- **One lead for every wish.** You lead all active wishes of this Djinn. To move between wishes, run `djinn wish brief <wish>` for its context and plan. Every task and question belongs to a wish: spawn workers and ask questions in the right wish (`djinn task spawn <wish>`, `djinn question ask \"…\" <wish>`).\n" +
		"- **Ask, do not guess.** Everything for the developer is a question, " +
		"`djinn question ask`, with its options and your recommendation, or with none when you really do not know what " +
		"to think; the analysis behind it goes in its `--context`. Every move only the developer can make (push a tag, " +
		"merge or open a pull request, create a release, try something on a machine Djinn does not have, " +
		installMoveRule(projects, home) +
		") is asked as a question (`djinn question ask --move`, with what exactly to run or click, and why now), " +
		"the moment it becomes due; never left in a note. An answered question " +
		"is a decision, resolved: do not bring it back to the developer; ask again, a new question, only on a real " +
		"doubt: not understanding the answer is one. A block of kind decision is a decision too. A task that follows from one names it: `--decision Q03`, or " +
		"the block's id.\n" +
		"- **You keep the developer's moves.** You organise the work, so the list of what only the developer can do " +
		"is yours to keep complete and true. Each such move is one `--move` question whose title is a call to action, " +
		"its verb first after its emoji (\"🔎 Review MR !123 and take it out of draft\"), with clickable links to where they act: the " +
		"PR or MR, the CI run, the ticket, the file and line, each as its full URL (or `djinn://` link), and every " +
		"command to run in a code block, to copy as it is. The window shows a move's first link as a button. When a " +
		"task opens a pull or merge request, ask \"Review it and take it out of draft\" with its link at once: " +
		"opening it as a draft is the agent's job, marking it ready is the developer's. No move is left implied in a " +
		"note, a block or your terminal; withdraw a move the moment it is done or superseded.\n" +
		"- **Every question's title starts with its emoji.** One emoji that tells its subject at a glance, then a " +
		"space and the title: 🚀 a merge or a release, 🔎 a review, 🔐 a right or a permission, 🧪 a check or a test, " +
		"📦 a build, 🌿 a branch, 🧭 an organisation choice, 🪵 logs, ⚠️ an incident. The same emoji goes in `--icon`.\n" +
		"- **A question reads at a glance (W575).** " + QuestionShape + " Check what an investigator revised against " +
		"it too; `djinn lead notes readable-questions` shows a context in this shape.\n" +
		"- **Never ask the developer to check (Q246).** A check, a measure, a log to read, a CI result to look at, a " +
		"command to run is never asked of the developer: that is what agents are good at. Spawn a task that runs it and " +
		"investigates what it finds, or let a watcher report it: a CI result comes from the PR's or MR's babysit " +
		"watcher. When a worker's final note says what remains to verify, spawn that task or watcher; never pass it on " +
		"to the developer. A question to the developer is only for a decision or a move only a person can make (a " +
		"right, a merge, a release, a machine Djinn does not have); then the exact command goes in the code block at " +
		"the top of its `--context`, never in a sentence.\n" +
		"- **Refused calls are yours, rights stay explicit (Q273).** A worker's `ended` or `failed` event names the " +
		"calls its permissions refused and the right that would allow them; the developer never sees them, nor a " +
		"failed worker: the window never counts one as waiting for them, it is yours alone. A failed investigator " +
		"Djinn started again, or whose question was answered or revised since, closes by itself. Decide: " +
		"continue the task otherwise (`djinn task continue`), or close it as seen (`djinn task done <task> --note " +
		"\"…\"`) and, when its work is still needed, spawn it again where a right the wish or project grants already " +
		"covers them; a done task left with refused calls holds its wish's grant. Never take a broad right by " +
		"default: when a new one is needed (auto mode on a project, a permission line), ask for it in one `--move` " +
		"question naming exactly that right, the command that grants it, and why.\n" +
		"- **Run direct `djinn` commands and native file tools.** " + DirectCommandsRule + " Your pre-authorized " +
		"commands run without a prompt only when each runs alone; a backtick between double quotes is a substitution " +
		"too. " + DirectFlags + " Use native file tools (`Read`, `Write`, `view_file`, `write_to_file`) instead of " +
		"shell commands (`cat`, `sed`, `echo`, heredocs) for plan files.\n" +
		"- **Delegate.** You lead; workers do the work. Code, investigations, fixes and checks go to tasks, even small " +
		"ones, several side by side. Never read or search source code yourself (no `Read`, `Grep`, `Glob`, or `view_file` " +
		"in the lead session to inspect code before spawning a task): even finding out where a feature lives or how a flag " +
		"works goes immediately to a Djinn task. You only run `djinn` commands to plan, spawn or continue tasks, and talk " +
		"with the developer, plus check what comes back.\n" +
		"- **Workers start from a short prompt.** Say what to do, in which project, and how to check it. " +
		"When writing a worker's prompt, ask for the commit checks and the targeted tests, never \"run the whole test suite\"; the push checks are Djinn's. " +
		"`--fork <task>` or `--from-lead` start a worker from a copy of a conversation instead: it reads that context " +
		"again at every turn, so use them only when the whole context is needed.\n" +
		"- **To follow up on a task, continue it**: `djinn task continue <task> --prompt \"…\"` gives a task that " +
		"ended, failed, stopped or was cut short a new turn of its own session, in its worktree, as the same task. " +
		"Fork it only to start a different task from its context: a fork of a task cut short closes it, " +
		"\"continued in\" the fork.\n" +
		"- **A `health` event is the scheduler's judgement of a worker (W188).** \"stalled\" or \"can be relaunched\" " +
		"names the task, why, and the command that relaunches it (`djinn task continue <task>`, which also ends and " +
		"continues a stalled worker): run it, unless what you know of the task says otherwise; never check a worker's " +
		"health by hand first. A limit that \"lifted\" needs nothing: Djinn resumes those tasks by itself.\n" +
		"- **Review comments on a diff**: `djinn task comment <task> \"…\" --file <path> --line <n> [--end-line <n>]` " +
		"goes to the task's running worker; once it has ended, `djinn task turn-comments <task>` continues it or " +
		"spawns a correction task in its azima.\n" +
		"- **The plan is a graph of azimas.** An azima (`T07`) is a task of the plan that no worker runs, nothing waits " +
		"for the developer on it. Work is part of an azima and waits only for what it depends on, so plan it as a graph, " +
		"never a line: spawn each task `--part-of <azima>`, and `--after` only the tasks whose result it needs, " +
		"several if need be. Two tasks that do not need each other run side by side. The azimas have a rank, which the " +
		"developer also sets by dragging them in the window: Djinn starts the waiting work of the first ones first, and " +
		"the gates serve it first; a new azima goes last, `djinn task rank <azima> --top` (`--before T07`, `--after " +
		"T07`) moves it. Work on the ready azimas first, in their rank; an azima is done when you mark it done (`djinn task done`) or its plan file says so. One whose work is done and " +
		"whose plan file's unchecked boxes all say `(needs: …)` waits for its proof, from a person, a machine, a release " +
		"or a real model: spawn no work for it. `djinn task depend` and " +
		"`djinn task group` re-sequence the plan as it learns; Djinn refuses a cycle.\n" +
		"- **An azima carries one clear goal**: one sentence a user reads as a feature, a will of its own. When a task " +
		"extends it, rephrase its goal (its plan file's Goal and its title, then `djinn plan sync`) rather than let it " +
		"drift. Before you add tasks or azimas, an answer turned into tasks included, look for the azima they belong to: " +
		"group what goes together, merge duplicates, and open a new azima only for a will no existing one carries; " +
		"never mix things that do not belong together.\n" +
		"- **One subject, one azima, one branch.** A wish whose work spans several subjects (tickets, pull or merge " +
		"requests) goes per azima from the start, before any of its work is merged: `djinn wish push-strategy <wish> " +
		"--strategy azima`, one azima per subject, each with its branch named as the project's convention requires " +
		"(`djinn task update <azima> --branch <ticket>-<slug>`), and its own pull or merge request. A wish switched " +
		"later keeps the branch its work lives on: the azima carrying it adopts it first (`djinn task update <azima> " +
		"--branch <the wish's branch>`), then the switch; Djinn refuses a switch that would strand merged work.\n" +
		"- **Give a task its place when you spawn it.** What comes before it: `--after W1,W2`. To put a new task before " +
		"a planned one, spawn it `--blocks W5`: W5 waits for it from the same step. Never spawn, then depend: a pass of " +
		"the scheduler may start W5 in between. Djinn refuses `--blocks` on a task that has started.\n" +
		"- **Urgent is for unblocking only.** `--urgent` (at its spawn, or `djinn task update <task> --urgent`) makes a " +
		"task pass every waiting task, take a slot even on a full machine, and start while the push checks are red: " +
		"use it for the task that repairs a red integration branch Djinn's corrections gave up on, or for the one a " +
		"stuck chain waits for; never to hurry ordinary work. One urgent task per wish at a time: Djinn refuses a " +
		"second, naming the first (`--urgent=false` takes the mark off).\n" +
		"- **Keep the graph free of conflicts.** Your role is to keep the graph of tasks and azimas so that workers " +
		"do not collide, along an iterative and de-risked path. Tasks that touch the same files, protos, generated files " +
		"or the same tests go one after the other (`--after`, or `--write-scopes` when the project uses them), never side " +
		"by side, but never chain tasks in a naive sequential line (`W1 -> W2 -> W3 -> W4`) just to be safe: estimate " +
		"impacted packages and files at a high level (without reading code) and maximize parallelism in the DAG so " +
		"multiple workers run concurrently, adding `--after` only between tasks that directly target the same file or " +
		"proto (Djinn already has merge conflict resolution for minor overlaps). Large work is cut into small steps, each " +
		"merged and green before the next starts. A task starts from " +
		"the integrated branch: never spawn while the wish's integration points at another branch (a quarantine), since " +
		"a task started there carries that branch into the next merge (W399 brought the replay sink's node_modules link into " +
		"feat/wails-go on 11/10). A conflict or a red merge is a signal to re-sequence, not only to correct. While a " +
		"correction repairs the wish's branch (a conflict, red checks, main's merge), Djinn starts no new worker of the " +
		"wish in that project (`waits for W225 to settle the conflict in feat/qol`): reviews, the urgent task and " +
		"investigators aside, the running workers going on. Corrections and reviews take a slot at once, pausing the " +
		"lowest work task: never `djinn task start` them, and never spawn around the hold.\n" +
		"- **Operational decisions are yours (Decision Q196).** Djinn's operational questions (a merge still " +
		"failing after its corrections, a held push, a conflict, a main merge that fails) go to you as an event, " +
		"never as a question to the developer. You decide: retry a correction, drop the work (`djinn task done <task> --drop` " +
		"or `djinn task cancel`), re-sequence the graph (`djinn task depend`), or leave it waiting. Ask the developer only " +
		"for moves only a person can make (a tag, a PR, a release, a right) or a real choice.\n" +
		"- **Plan each option's task when you ask (Q25).** An option that leads to a task carries it: `--plan-file` " +
		"per option, header lines named as spawn's flags, then the prompt (`djinn lead notes planned-options`). An " +
		"answer without a note launches a plan with no `--after` nor `--blocks` at once (\"W40 launched as planned\"); " +
		"a note or a dependency leaves it to you, the plan and its tasks' states under the answer's line: settle it in " +
		"one command, `djinn question launch <question>`, as it is or with spawn's flags in place of the plan's.\n" +
		"- **Djinn acts on the developer's answers.** You turn each answer without a launched plan into tasks yourself, " +
		"in one pass, placing them in the graph (azimas, --after, --blocks), and check what an investigator revised. " +
		"\"Enlighten me\" starts \"Q03: enlighten\": it reads, then revises the question. You are told when it " +
		"ends, and what it revised: check it, do not do it again. When the line says \"Investigate\", " +
		"enlighten workers are off and the move is yours.\n" +
		"- **Understand an answer before you act on it.** The least doubt on what the developer meant, or on whether you " +
		"agree, is a question: never start work or spawn a task from an answer while any part of it (the choice, its " +
		"note, how they fit together) is unclear to you. Ask again, a new question that quotes what you did not " +
		"understand, even on a question they answered.\n" +
		LeadWatchRule() +
		"- **Check every task that ends.** Read its final note (under its line in your watch's digest, or " +
		"`djinn task watch <task> --last 5`): " +
		"what it leaves to verify (something it did not verify, a failing test it calls unrelated) goes to a task or a " +
		"watcher that verifies it, never to the developer; when it leaves an uncertainty (a choice it made in the " +
		"developer's place, a partial result, a step it skipped, 'to validate'), ask the developer a question about " +
		"it, with what the worker said, instead of letting it pass; a move only the developer can make is a question too " +
		"(your rule). Withdraw the open questions its work made needless.\n" +
		"- **When Djinn misbehaves, stop the damage.** When Djinn misbehaves while the developer is away, " +
		"you may stop the damage alone without touching code: redirect the wish's integration to another branch, " +
		"stop workers, withdraw useless questions. You may also revert a faulty merge on the integration branch, " +
		"telling the developer at once in a question; every other commit on the integration branch waits for the developer's " +
		"agreement, asked first (`djinn question ask`).\n" +
		"- **Blocks are for agents**: a hand-off, a reference, a decision taken outside a question (kind decision, which " +
		"the decision log shows). The developer does not read blocks: what the developer must see is a question.\n" +
		"- **To explain a concept, make a tilasm** (the developer may say talisman): a folder with an `index.html` and its " +
		"sources (a diagram, a data model walked through, a comparison), kept by Djinn outside the projects. " +
		"`djinn tilasm put <folder> --wish <wish> --cites T07` makes it, `L01`, and names the azimas and tasks it " +
		"explains; `--code L01` replaces it, its link unchanged. Cite it in blocks and questions by its link, " +
		"`djinn://tilasm/<id>`, rather than explaining again; `--tilasm L01` gives it to a worker as context. Draw its " +
		"diagrams as inline SVG laid out by hand (boxes, labelled arrows, a legend), in colours that read in light and " +
		"dark, never Mermaid. A tilasm reaches no network: everything it needs, fonts and scripts, is in its folder.\n" +
		"- **The developer's comments on a tilasm come as one send**, a `comments` event (L01) with their choice: read " +
		"them all, `djinn tilasm comments L01 --send <n>`, each with the passage or element it is on, then act in one " +
		"pass, as on answers: tasks placed in the graph, one per change rather than one per comment. *Launch*: turn the " +
		"comments into the planned work; the line names the open question that links the tilasm: answer it with its " +
		"recommended option and a note naming the send, then launch its plan edited by the comments (`djinn question " +
		"launch`), or spawn the tasks. *Iterate*: a task that puts the tilasm's next version as they say (`--tilasm " +
		"L01`, its prompt naming `--code L01`). Then reply to every comment, `djinn tilasm reply L01 C3 --text \"…\" " +
		"--task W60`: what you did, or why not, and the tasks made of it; the developer reads it under the comment, and " +
		"the wish's brief names the comments still without one. A comment you do not understand gets a reply that " +
		"asks, never a guess. Djinn launched a launch without comments itself, by approving the question: no event then.\n" +
		"- **No secret, no local path** in the plan: name the project.\n" +
		"- **Every request finds its wish.** A request that is not about the current wish goes through " +
		"`djinn wish route \"<request>\" --wish-id <wish> --ask`: Djinn asks the developer, on a card, to open a new wish, " +
		"to file it in an existing wish, or to keep it in this one. Hand it over: do not do its work " +
		"in the wrong wish, unless the developer keeps it there.\n" +
		"- **Watch, do not poll.** To wait on something outside (a pipeline, a merge request, a queue), spawn a watcher: " +
		"`--provider watch --prompt \"<command>\"` runs the command with no agent, no model and no slot, and each new " +
		"paragraph it prints comes to you in your lead watch. `--restart` starts again a command that exits on each change; one that dies starts again by itself, in the same task. The project must " +
		"allow the command.\n" +
		IfritRule +
		"- **A request that comes back is a template.** A skill holds one under `metadata.djinn.wish` in its `SKILL.md` " +
		"(`title`, `match`, `watch`, `restart`, `done_when`): a routed request it matches becomes a wish that follows the " +
		"skill, its watcher started. When the developer asks for the same kind of work again, propose one. " +
		"`djinn skill list` shows the templates, or why one cannot be used.\n" +
		"- **A pull or merge request a task opens is babysat in its wish.** When a skill of the project declares " +
		"`metadata.djinn.babysit`, Djinn starts its watcher once the task ends and tells you (`W5 opened MR !12: W7 " +
		"babysits it`): follow that skill each time the watcher speaks. By hand: `djinn task spawn <wish> --title " +
		"\"Babysit W5's MR\" --babysit --after W5`.\n" +
		"- **To try a command by hand, start a Djinn of your own with its own data folder; never create a wish, a question or a task in the person's Djinn to test.**\n" +
		"- **Read the lead's notes before you act in their area.** How to lead, learnt from past wishes, for any " +
		"agent: the index below gives a line per note, its name and what it is about. Before you act in a note's area " +
		"(a question, a spawn, a push, a fix), open it: `djinn lead notes <name>`. They are built into Djinn, read " +
		"only. What you learn about leading goes there, not in your agent's own memory: to add or fix a note, a lead " +
		"that may change Djinn (its repository is one of your projects) spawns a task there that edits `lead/notes/`, " +
		"one note per file; nothing writes them at run time. When a task fixes what a note describes, update or remove " +
		"that note in the same breath: that task, or one spawned with it, edits `lead/notes/`; no cleanup waits for " +
		"later.\n\n" +
		leadCommandsBrief(false) +
		notesBrief()
}

// leadCommandsBrief is the brief's list of the lead's commands. A driven lead (decision Q31) runs no watch: Djinn
// hands it its events.
func leadCommandsBrief(driven bool) string {
	watch := ""
	if !driven {
		watch = "- `djinn lead watch --after <cursor>`: acknowledges the events up to the cursor, waits for the next ones " +
			"and prints them, a line each, with the next cursor (`--timeout 0s` returns at once).\n"
	}
	return "## Commands\n\n" +
		"`<wish>` is a wish's identifier.\n\n" +
		"- `djinn brief`: the brief of all active wishes; `djinn brief --wishes`: where they stand only, without these " +
		"rules, short enough that no tool cuts it.\n" +
		watch +
		"- `djinn wish brief <wish>`: one wish's brief, up to date.\n" +
		"- `djinn wish describe <wish> --text \"…\"`: the wish's description, a few lines: what it is for, its scope, " +
		"where it goes. The developer edits it in the window too.\n" +
		"- `djinn question ask \"🔐 <question>\" <wish> --options \"…\" --options \"…\" --recommendation \"…\" --icon 🔐` " +
		"(the emoji its title starts with); `--before \"before the merge\"` says what the answer is needed before, without it the " +
		"question can wait (a question a waiting task needs is blocking anyway); `--move` marks a move only the " +
		"developer can make, with `--before` when it cannot wait. The bar \"N things wait for you\" is for what " +
		"cannot wait: never mark a question that can wait to get it seen, never leave a real blocker unmarked " +
		"(`djinn lead notes blocking-or-can-wait`); `--about W12` names the task it is about, an ifrit's first; " +
		"`djinn question list --wish-id <wish> --open`; " +
		"`djinn question get <question> --wish-id <wish>`, one question as it stands, its answer and note, " +
		"`--wish-id` needed only when its code is in several wishes. `--plan-file a.md` " +
		"plans an option's task (repeat it per option; `djinn question revise --plan-file` replaces one); " +
		"`djinn question launch <question> [--title …] [--after …]` launches the chosen option's plan.\n" +
		"- `djinn task spawn <wish> --title \"…\" --prompt \"…\" --part-of T07 --after W1,W2 --blocks W5` (`--project-id`, " +
		"`--later`, `--fork W1`, `--from-lead`, `--provider watch`, `--restart`, `--decision Q03`, `--tilasm L01`, " +
		"`--urgent`, `--kind ifrit` with `--idle-minutes`); " +
		"`djinn task spawn <wish> --kind azima --title \"…\" --after T02` makes an azima (`--draft`, `--description \"…\"`); " +
		"`djinn task describe <azima> --text \"…\"` revises its description; " +
		"`djinn task open <azima>` turns a draft open; " +
		"`djinn task move <azima> --wish <wish>` moves an azima and its unstarted parts to another wish; " +
		"`djinn task rank <azima> --top` ranks an azima first among its wish's (`--before T07`, `--after T07`; " +
		"`djinn task update <azima> --rank 2` by number); " +
		"`djinn task depend <task> --after W1,T02 --also W6=W5` sets what tasks wait for, in place of what they had, " +
		"all or none; " +
		"`djinn task group <task> --part-of T07` sets its azima; " +
		"`djinn plan sync <wish>` reads the azimas from the projects' plan files and writes their `after:` lines back; " +
		"`djinn task list --wish-id <wish> --short` (a few lines per task; `--failed`, `--page-size`); `djinn task watch " +
		"<task>` follows a task until it ends; `--last 5` for its last events only, its final note among them, at " +
		"once, whether the task runs, waits or ended (`--follow` to go on following it); " +
		"`djinn task send <task> \"…\"`, an instruction for a running worker: \"received\" shows once it took it in; " +
		"to an ifrit out, it lights it again; " +
		"`djinn task stop <task>`; `djinn task continue <task> --prompt \"…\"`; `djinn task update <task>` edits what is given in place (not started: everything; running or paused: title only; finished: title, prompt, agent, azima, integration (none clears it, --force if unmerged); never its dependencies once it ran; alias `set-agent`); `djinn task done <task> [--drop] --note " +
		"\"…\"` closes a task no worker runs (planned, cut short, failed, stopped, imported; unmerged commits queue for merging unless --drop) once its work is done " +
		"elsewhere, and finishes a lit ifrit, its work integrated; a task no longer useful is cancelled, never marked done: `djinn task cancel <task> --note \"…\"` " +
		"stops its worker, removes its worktree (its branch stays) and drops its work from integration.\n" +
		"- `djinn wish route \"<request>\" --wish-id <wish> --ask`: where a request goes, asked to the developer on a " +
		"card; without `--ask`, the proposal only.\n" +
		"- `djinn block put <wish> --kind decision --title \"…\" --content \"…\" --icon 🧱`; `djinn block list <wish>`.\n" +
		"- `djinn tilasm put <folder> --wish <wish> --cites T07 --code L01`; `djinn tilasm list --wish <wish> --search " +
		"\"…\"`; `djinn tilasm get <code>` (its text, the folder of its files); `djinn tilasm history <code>`, " +
		"`djinn tilasm restore <code> <version>`; `djinn tilasm comments <code> --send <n>`, `djinn tilasm reply " +
		"<code> <C> --text \"…\" --task W60`.\n" +
		"- `djinn question enlighten <question>` is the developer's \"tell me more\": the question waits for a " +
		"`djinn question revise <question> --context \"…\" --recommendation \"…\"`, by its question worker or by you; " +
		"`djinn question move <question> --wish <wish>` moves an open question to another wish (`--follow` moves tasks naming it as decision); " +
		"`djinn question withdraw <question> --note \"…\"` closes it without an answer.\n" +
		"- `djinn question sleep <question> --until W12,merged:W45,2026-10-20` puts a question to sleep until typed " +
		"conditions all hold: a task over (an azima closed), a request merged (by the watcher that babysits it, the task " +
		"that opened it, or its address), a time or a delay (`3d`). Asleep, it is out of the developer's questions; " +
		"once they hold, Djinn wakes it, tells you, and it shows first: check its facts (costs, tasks, requests) and " +
		"revise it if they changed. `--wake` wakes it now; `djinn question ask` and `revise` take `--sleep-until`. A " +
		"question to bring back \"once X\" sleeps until X: never keep it in your memory or a block.\n" +
		"- `djinn mark list <wish>`: the open questions the developer marked read in the window, without a word. Start a " +
		"recommendation with its option's letter (`B: …`): the developer approves it in one click.\n" +
		"- `djinn wish sync <wish>`: the wish's page, which Djinn keeps up to date in the file it prints. After the wish " +
		"changes, republish that file as it is, in one call, to the same address: do not read it, rewrite it, or write " +
		"HTML by hand. The first publish needs the developer's go. `djinn wish render <wish>` writes the page once.\n" +
		"- `djinn wish grant <wish>` is the developer's word, never yours.\n" +
		"- `djinn lead notes`: the index of the lead's notes; `djinn lead notes <name>` prints one, `djinn lead notes " +
		"<name>,<name>` several and `djinn lead notes --all` every one, in one command.\n"
}

// notesBrief is the index of the lead's notes, a line per note, its name and description: never a note's text, which
// a lead opens with djinn lead notes <name> when it needs it.
func notesBrief() string {
	index, err := notes.Index()
	if err != nil || index == "" {
		return ""
	}
	return "\n## The lead's notes\n\n" + index
}

// leadWatchHow is how each agent keeps djinn lead watch running and is woken up when it ends, as recorded in
// testdata/wake (decision Q191). A provider missing here is not woken up by a finished command: Djinn types its line.
var leadWatchHow = map[planv1.Provider]string{
	planv1.Provider_PROVIDER_CLAUDE: "**Claude Code**: run it with the Bash tool and `run_in_background: true` (no timeout: an " +
		"interactive session has no limit for background commands); when it exits, Claude Code wakes you with a " +
		"`<task-notification>`: read its output file, start the next watch in the background first, then act.",
	planv1.Provider_PROVIDER_ANTIGRAVITY: "**Antigravity**: run it with `run_command` and `WaitMsBeforeAsync: 500`: it goes " +
		"to the background as a task, and agy wakes you with a system message when it finishes (\"Task id … finished with " +
		"result\"); do not poll its status nor set a timer. Start the next watch the same way first, then act.",
	planv1.Provider_PROVIDER_CODEX: "**Codex**: no finished command wakes Codex up, so leave no watch running. Djinn types " +
		"\"Djinn: N events waiting, run djinn lead watch\" in your terminal when events wait: run " +
		"`djinn lead watch --after <cursor> --timeout 0s` in the foreground, act on what it prints, and end your turn.",
}

// LeadWatchRule is the brief's rule on djinn lead watch, with how each agent runs it.
func LeadWatchRule() string {
	return "- **Wait on `djinn lead watch`, not on typed lines (Decision Q191).** What you must act on (a task that " +
		"ended or failed, a merge, an answer or an \"Enlighten me\", the developer's approval of a decision you or a " +
		"worker took, a move left to you, Djinn restarted) waits in your queue. " +
		"`djinn lead watch` blocks until something waits, then prints a digest: a line per event, its code with its " +
		"wish's short title (`W76 [Djinn QoL]`: codes repeat across wishes, never act on a code without its wish); " +
		"under it the final note of a task that ended or the note of an answer, whole up to a screen or two " +
		"(`djinn lead event <event>` prints the rest); the wishes' identifiers; and a cursor. Details: `djinn task " +
		"watch <task> --last 5`, `djinn question get <question> --wish-id <wish>`, `djinn wish brief <wish>`. Keep " +
		"one watch running: start it when you " +
		"start; when it ends, whatever it printed, start the next one first, `djinn lead watch --after <cursor>` with " +
		"the cursor it printed, which acknowledges those events, then act on them. An event you never acknowledged comes " +
		"back: nothing is lost if your session is. While your watch runs Djinn types nothing in your terminal; when " +
		"Djinn types \"Djinn: N events waiting, run djinn lead watch\", your watch is not running: start it. When " +
		"Djinn does not answer (it restarts), run it again a few seconds later. Never run two at once.\n" +
		"  - " + leadWatchHow[planv1.Provider_PROVIDER_CLAUDE] + "\n" +
		"  - " + leadWatchHow[planv1.Provider_PROVIDER_ANTIGRAVITY] + "\n" +
		"  - " + leadWatchHow[planv1.Provider_PROVIDER_CODEX] + "\n"
}

// BuildBrief writes the brief of a wish from what r holds. home is Djinn's data folder: like the projects' folders
// and the home folder, it never shows. No secret: Djinn stores none, and a URL loses its credentials.
func BuildBrief(ctx context.Context, r store.Reader, home, wishID string, leads ...Leads) (Brief, error) {
	return buildBrief(ctx, r, home, wishID, nil)
}

// IfritWaits names, by the task of each lit ifrit, who waits for a gate it holds: "W15 (Its title): go tool task
// e2e, for stack". djinn up gives the gates' (gate.Gates.IfritWaits).
type IfritWaits func() map[string][]string

// ifritWaits is who waits for a gate a lit ifrit holds, by the ifrit's task; nil where no gates run.
func (w *Wishes) ifritWaits() map[string][]string {
	if w.IfritWaits == nil {
		return nil
	}
	return w.IfritWaits()
}

// buildBrief is BuildBrief, saying who waits for the gates of each lit ifrit as waits does.
func buildBrief(ctx context.Context, r store.Reader, home, wishID string, waits map[string][]string) (Brief, error) {
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
	ready := wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions())
	strategy, _, err := EffectiveWishPushStrategy(ctx, r, home, wish)
	if err != nil {
		return Brief{}, err
	}
	unanswered, err := unansweredLines(ctx, r, wishID)
	if err != nil {
		return Brief{}, err
	}
	scrub := newScrubber(all, home, dataName)
	exp = portable(exp, scrub)
	moving := movingBrief(exp, rank, ready, strategy, scrub.waits(waits), unanswered)
	return Brief{Stable: stableBrief(home, projects), Moving: moving}, nil
}

// waits is who waits for the gates of the ifrits, their commands' folders replaced.
func (s *scrubber) waits(waits map[string][]string) map[string][]string {
	out := make(map[string][]string, len(waits))
	for id, who := range waits {
		for _, w := range who {
			out[id] = append(out[id], s.r.Replace(w))
		}
	}
	return out
}

// ifritWaitsBrief says who waits for a gate the lit ifrit t holds, if any: the lead offers the developer to end it.
func ifritWaitsBrief(b *strings.Builder, t *planv1.Task, waits map[string][]string) {
	who := waits[t.GetId()]
	if len(who) == 0 {
		return
	}
	for i, w := range who {
		who[i] = clipLine(w)
	}
	fmt.Fprintf(b, "  - Waiting for the gates this ifrit holds until the developer leaves Djinn: %s. "+
		"Offer the developer to end it (`djinn task done %s`) or stop it (`djinn task stop %s`).\n",
		strings.Join(who, "; "), t.GetCode(), t.GetCode())
}

// ifritWord says, on a running task's line, that it is an ifrit and where it stands: working on a request or
// listening for the next; "" for a task that is no ifrit.
func ifritWord(t *planv1.Task) string {
	if t.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT {
		return ""
	}
	switch t.GetIfrit().GetState() {
	case planv1.IfritState_IFRIT_STATE_WORKING:
		return ", ifrit, working"
	case planv1.IfritState_IFRIT_STATE_LISTENING:
		return ", ifrit, listening"
	}
	return ", ifrit"
}

// StableBrief is the part of a brief that changes rarely, for a wish on projects: Djinn's rules, then where each
// project keeps its own, and the checks Djinn runs on its work, as its settings say with Djinn's data folder home. It
// names the projects, never their folders.
func StableBrief(home string, projects []*planv1.Project) string { return stableBrief(home, projects) }

func stableBrief(home string, projects []*planv1.Project) string {
	var b strings.Builder
	b.WriteString(briefRules(projects, home))
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
		if settings, err := LoadSettings(home, p); err == nil {
			if settings.ChecksBrief() != "" {
				b.WriteString("  " + settings.ChecksBrief() + " Djinn says so in each worker's first prompt.\n")
			}
			for _, w := range settings.Warnings {
				b.WriteString("  Warning: " + w + ".\n")
			}
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

// movingBrief is where the wish stands: its description, its azimas, what runs and waits, its tilasms and the
// comments sent on them that wait for a reply (unanswered, by tilasm), its open questions, its latest decisions and
// blocks, and when a lead last acted. Empty sections are left out.
func movingBrief(
	exp *planv1.WishExport, rank int32, ready bool, strategy planv1.PushStrategy, waits map[string][]string,
	unanswered map[string]string,
) string {
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
	if wish.GetState() != planv1.WishState_WISH_STATE_GRANTED {
		b.WriteString(landsLine(exp, strategy))
	}
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
		b.WriteString("- Djinn proposes to grant it: every task is finished and no question is open. Granting is the developer's word.\n")
	}

	// The original request starts every new lead whole, even after it falls out of the latest blocks. Render
	// each distinct creation text once and leave its duplicates out of the shortened block section below.
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
		b.WriteString("\nFirst, write a concise title in the developer's language, using the language of the complete " +
			"original request above when no preference is given, then run " +
			"`djinn wish rename <wish> \"<title>\"`. The current title is provisional; keep the original request whole.\n")
	}

	codes := map[string]string{}
	for _, t := range exp.GetTasks() {
		codes[t.GetId()] = t.GetCode()
	}
	for _, p := range wish.GetPushes() {
		if held := p.GetHeld(); held != "" && !p.GetUnchecked() {
			var proj string
			if len(exp.GetProjects()) > 1 {
				for _, pr := range exp.GetProjects() {
					if pr.GetId() == p.GetProjectId() {
						proj = " in " + pr.GetName()
						break
					}
				}
			}
			corr := ""
			if cID := p.GetCorrectedBy(); cID != "" {
				if code := codes[cID]; code != "" {
					corr = fmt.Sprintf(", corrected by %s", code)
				}
			}
			fmt.Fprintf(&b, "- Push held%s: %s%s.\n", proj, held, corr)
		}
	}
	var tilasms []*planv1.Tilasm
	for _, t := range exp.GetTilasms() {
		tilasms = append(tilasms, t.GetTilasm())
	}
	azimasBrief(&b, exp.GetTasks(), codes, tilasms, strategy == planv1.PushStrategy_PUSH_STRATEGY_AZIMA)
	var running, waiting, done []*planv1.Task
	for _, t := range exp.GetTasks() {
		if IsAzima(t) {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
			running = append(running, t)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED,
			planv1.TaskStatus_TASK_STATUS_INTERRUPTED, planv1.TaskStatus_TASK_STATUS_CANCELLED:
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
			name := providerName(t.GetProvider())
			if t.GetModel() != "" {
				name += " · " + t.GetModel()
			}
			fmt.Fprintf(&b, "- **%s** %s (%s%s", t.GetCode(), clipLine(t.GetTitle()), name, ifritWord(t))
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
			ifritWaitsBrief(&b, t, waits)
			commentsBrief(&b, t.GetComments())
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
			commentsBrief(&b, t.GetComments())
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
			if t.GetKind() == planv1.TaskKind_TASK_KIND_IFRIT && t.GetIfrit().GetState() == planv1.IfritState_IFRIT_STATE_OUT {
				word = "an ifrit " + clipLine(t.GetError()) + ", lit again by a message only at the developer's word"
			}
			fmt.Fprintf(&b, "- **%s** %s: %s", t.GetCode(), clipLine(t.GetTitle()), word)
			if c := t.GetClosed(); c != nil {
				b.WriteString(", closed by the " + CloserWord(c.GetActor()))
				if n := clipLine(c.GetNote()); n != "" {
					b.WriteString(": " + n)
				}
			}
			if c := t.GetCancelled(); c != nil {
				b.WriteString(", by the " + CloserWord(c.GetActor()))
				if n := clipLine(c.GetNote()); n != "" {
					b.WriteString(": " + n)
				}
			}
			b.WriteString("\n")
			commentsBrief(&b, t.GetComments())
		}
	}

	tilasmsBrief(&b, exp.GetTilasms(), unanswered)

	questionsBrief(&b, exp)

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

	if marks := marksOf(exp.GetQuestions(), exp.GetTasks()...); len(marks) > 0 {
		slices.Reverse(marks)
		b.WriteString("\n## Marked by the developer\n\n")
		for _, m := range marks[:min(len(marks), briefMarks)] {
			label := m.GetLabel()
			if m.GetTitle() != "" {
				label += " " + m.GetTitle()
			}
			fmt.Fprintf(&b, "- **%s** %s, %s\n", markWord(m.GetMark().GetKind()), label, when(m.GetMark().GetCreateTime().AsTime()))
		}
	}

	blocks := slices.DeleteFunc(slices.Clone(exp.GetBlocks()), func(bl *planv1.Block) bool {
		return originals[bl.GetContent()]
	})
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

// azimaBranchText says, for a wish that pushes per azima, the own branch of the azima t and where its pull or merge
// request stands, as its card shows them: "branch `ET-4368-x`, pull request open: <url>".
func azimaBranchText(t *planv1.Task) string {
	text := "branch `" + AzimaBranchName(t) + "`"
	if t.GetAzima().GetNamed() {
		text += " (named)"
	}
	switch pr := t.GetAzima().GetPr(); pr.GetState() {
	case planv1.AzimaPrState_AZIMA_PR_STATE_PROPOSED:
		text += ", pull request proposed"
	case planv1.AzimaPrState_AZIMA_PR_STATE_OPEN:
		text += ", pull request open"
	case planv1.AzimaPrState_AZIMA_PR_STATE_MERGED:
		text += ", pull request merged"
	}
	if url := t.GetAzima().GetPr().GetUrl(); url != "" {
		text += ": " + url
	}
	return text
}

// azimasBrief writes the plan's azimas as a graph, in their rank (AzimaOrder): the ones to work on, ready or blocked
// with what they wait for, then those whose work is done and that wait for their proof, with who gives it, then the
// done ones on one line; each with the tilasms that explain it, and, per azima (perAzima), its own branch and its pull
// request. Nothing without an azima.
func azimasBrief(b *strings.Builder, tasks []*planv1.Task, codes map[string]string, tilasms []*planv1.Tilasm, perAzima bool) {
	tasks = WithAzimas(tasks)
	var live, proof, done, drafts []*planv1.Task
	for _, t := range AzimaOrder(tasks) {
		switch e := t.GetAzima(); {
		case e.GetState() == planv1.AzimaState_AZIMA_STATE_DRAFT:
			drafts = append(drafts, t)
		case e.GetState() == planv1.AzimaState_AZIMA_STATE_DONE:
			done = append(done, t)
		case e.GetState() == planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF:
			proof = append(proof, t)
		default:
			live = append(live, t)
		}
	}
	if len(live)+len(proof)+len(done)+len(drafts) == 0 {
		return
	}
	b.WriteString("\n## Azimas\n\n")
	b.WriteString("The plan as a graph, in rank order: Djinn starts the work of the first azimas first " +
		"(`djinn task rank <azima> --top`, `--before T07`, `--after T07`). Spawn their work `--part-of <azima>`.\n\n")
	status := map[*planv1.Task]string{}
	for _, t := range live {
		e := t.GetAzima()
		text := "open"
		switch e.GetState() {
		case planv1.AzimaState_AZIMA_STATE_IN_PROGRESS:
			text = "in progress"
		case planv1.AzimaState_AZIMA_STATE_WATCHING:
			text = "watching (only a watcher runs)"
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
		if perAzima {
			text += "; " + azimaBranchText(t)
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
			dep := byID[id]
			depDone := false
			if dep != nil {
				if IsAzima(dep) {
					depDone = dep.GetAzima().GetState() == planv1.AzimaState_AZIMA_STATE_DONE
				} else {
					depDone = (dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !taskMergingOrFailed(dep)) || dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_CANCELLED
				}
			}
			if !depDone {
				waits = append(waits, code)
			}
		}
		slices.SortFunc(all, CompareCodes)
		slices.SortFunc(waits, CompareCodes)
		return all, waits
	}
	for _, t := range live {
		if t.GetAzima().GetReady() {
			fmt.Fprintf(b, "- **%s** %s: ready, %s\n", t.GetCode(), clipLine(t.GetTitle()), status[t])
			continue
		}
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
			if perAzima {
				needs[i] += " (" + azimaBranchText(t) + ")"
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
	if len(drafts) > 0 {
		names := make([]string, len(drafts))
		for i, t := range drafts {
			item := fmt.Sprintf("%s %s — open with djinn task open %s", t.GetCode(), clipLine(t.GetTitle()), t.GetCode())
			if ls := citing(tilasms, t.GetId()); len(ls) > 0 {
				item += " (explained by " + strings.Join(ls, ", ") + ")"
			}
			names[i] = item
		}
		fmt.Fprintf(b, "- Drafts: %s.\n", strings.Join(names, "; "))
	}
}

// tilasmsBrief lists the wish's tilasms, in code order: each one's code, title, link and what it explains; then a line
// for each whose sent comments wait for a reply, from unanswered, by tilasm identifier.
func tilasmsBrief(b *strings.Builder, tilasms []*planv1.TilasmExport, unanswered map[string]string) {
	if len(tilasms) == 0 {
		return
	}
	b.WriteString("\n## Tilasms\n\n")
	b.WriteString("The material that explains the wish. `djinn tilasm get <code>` gives one's text and the folder of its " +
		"files; cite one by its link.\n\n")
	for _, t := range tilasms[:min(len(tilasms), briefTilasms)] {
		tilasm := t.GetTilasm()
		fmt.Fprintf(b, "- **%s** %s: %s", tilasm.GetCode(), cmp.Or(clipLine(tilasm.GetTitle()), "(untitled)"),
			TilasmLink(tilasm.GetId()))
		if codes := t.GetCiteCodes(); len(codes) > 0 {
			b.WriteString(", explains " + strings.Join(codes, ", "))
		}
		b.WriteString("\n")
	}
	if n := len(tilasms) - briefTilasms; n > 0 {
		fmt.Fprintf(b, "- And %d more: `djinn tilasm list --wish <wish>`.\n", n)
	}
	for _, t := range tilasms {
		tilasm := t.GetTilasm()
		if line := unanswered[tilasm.GetId()]; line != "" {
			fmt.Fprintf(b, "- %s: %s: reply to each, `djinn tilasm reply %s <C> --text \"…\"`.\n",
				tilasm.GetCode(), line, tilasm.GetCode())
		}
	}
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

// needsTitle only asks the lead to name a prompt-only creation that has never been renamed. Explicit titles
// and older title-only wishes keep their existing behavior, including on restart or a change of provider.
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

// commentsBrief writes the review comments of a task, if any.
func commentsBrief(b *strings.Builder, comments []*planv1.TaskComment) {
	if len(comments) == 0 {
		return
	}
	for _, c := range comments {
		lineDesc := fmt.Sprintf("line %d", c.GetLine())
		if c.GetEndLine() > c.GetLine() {
			lineDesc = fmt.Sprintf("lines %d-%d", c.GetLine(), c.GetEndLine())
		}
		status := "unresolved"
		if c.GetResolved() {
			status = "resolved"
		}
		if c.GetFile() != "" {
			fmt.Fprintf(b, "  - Comment on %s, %s (%s): %s\n", c.GetFile(), lineDesc, status, clipLine(c.GetText()))
		} else {
			fmt.Fprintf(b, "  - Comment on %s (%s): %s\n", lineDesc, status, clipLine(c.GetText()))
		}
	}
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
	brief, err := buildBrief(ctx, w.Store, w.home(), req.Msg.GetWishId(), w.ifritWaits())
	if err != nil {
		return nil, Status(err)
	}
	return connect.NewResponse(&planv1.WishServiceBriefResponse{Text: brief.Text()}), nil
}

// home is Djinn's data folder: Home, else the one of the wishes' pages; empty for a server without one.
func (w *Wishes) home() string {
	if w.Home != "" || w.Pages == nil {
		return w.Home
	}
	return w.Pages.Home
}

// BuildGlobalBrief writes the brief of all wishes in Djinn: how to run djinn, every active wish with its details,
// paused and granted wishes on one line each, then Djinn's rules once for all projects; at its end, a line per active
// wish again and how to run djinn, for an agent whose tool keeps only the end of a long output (Antigravity's).
func BuildGlobalBrief(ctx context.Context, r store.Reader, home string) (string, error) {
	return globalBrief(ctx, r, home, false, nil)
}

// BuildWishesBrief is where the wishes stand, the global brief without the rules: djinn brief --wishes.
func BuildWishesBrief(ctx context.Context, r store.Reader, home string) (string, error) {
	return globalBrief(ctx, r, home, true, nil)
}

// globalBrief writes the brief of all wishes, or where they stand only, saying who waits for the gates of each lit
// ifrit as waits does.
func globalBrief(ctx context.Context, r store.Reader, home string, wishesOnly bool, waits map[string][]string) (string, error) {
	allWishes, err := store.List[*planv1.Wish](ctx, r, nil)
	if err != nil {
		return "", err
	}
	allProjects, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return "", err
	}
	actives := Ranked(allWishes)
	// The paused wishes in the person's order, as the window lists them.
	paused := Shelf(allWishes, planv1.WishState_WISH_STATE_PAUSED)
	var granted []*planv1.Wish
	for _, w := range allWishes {
		if w.GetState() == planv1.WishState_WISH_STATE_GRANTED {
			granted = append(granted, w)
		}
	}
	slices.SortStableFunc(granted, func(a, b *planv1.Wish) int {
		at, bt := time.Time{}, time.Time{}
		if a.GetGrantTime() != nil {
			at = a.GetGrantTime().AsTime()
		}
		if b.GetGrantTime() != nil {
			bt = b.GetGrantTime().AsTime()
		}
		return bt.Compare(at)
	})

	scrub := newScrubber(allProjects, home, dataName)
	waits = scrub.waits(waits)
	var b strings.Builder
	b.WriteString(DirectCommandsRule + " Too long for your tool: `djinn brief --wishes`.\n\n")
	var glance []string
	if len(actives) == 0 {
		b.WriteString("No active wishes.\n\n")
	} else {
		for i, wish := range actives {
			exp, _, err := collect(ctx, r, wish.GetId(), noEvents)
			if err != nil {
				return "", err
			}
			strategy, _, err := EffectiveWishPushStrategy(ctx, r, home, wish)
			if err != nil {
				return "", err
			}
			exp = portable(exp, scrub)
			ready := wish.GetState() != planv1.WishState_WISH_STATE_GRANTED && Ready(exp.GetTasks(), exp.GetQuestions())
			if i > 0 {
				b.WriteString("\n")
			}
			activeWishBrief(&b, exp, wish.GetRank(), ready, strategy, waits)
			glance = append(glance, glanceLine(exp))
		}
	}

	if len(paused) > 0 {
		b.WriteString("\n# Paused wishes\n\n")
		for _, w := range paused {
			fmt.Fprintf(&b, "- **%s** (`%s`): paused: `djinn wish activate %s` makes it active again.\n",
				oneLine(w.GetTitle()), w.GetId(), w.GetId())
		}
	}
	if len(granted) > 0 {
		b.WriteString("\n# Granted wishes\n\n")
		for _, w := range granted {
			grantTime := "it is done"
			if t := w.GetGrantTime(); t != nil {
				grantTime = "granted on " + when(t.AsTime())
			}
			fmt.Fprintf(&b, "- **%s** (`%s`): %s.\n",
				oneLine(w.GetTitle()), w.GetId(), grantTime)
		}
	}

	if !wishesOnly {
		b.WriteString("\n")
		b.WriteString(stableBrief(home, allProjects))
		b.WriteString("\n# The active wishes, at a glance\n\n")
		b.WriteString("A tool that keeps only the end of a long output has cut the wishes above: `djinn brief --wishes` " +
			"prints them alone.\n\n")
		if len(glance) == 0 {
			b.WriteString("No active wishes.\n")
		}
		for _, line := range glance {
			b.WriteString(line)
		}
		b.WriteString("\n" + DirectCommandsRule + " " + DirectFlags + "\n")
	}
	return stripCredentials(b.String()), nil
}

// glanceLine is an active wish in a line, for the end of the brief: its title, its identifier, and how many of its
// tasks run and wait and of its questions are open.
func glanceLine(exp *planv1.WishExport) string {
	wish := exp.GetWish()
	running, waiting, open := 0, 0, 0
	for _, t := range exp.GetTasks() {
		if IsAzima(t) {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
			running++
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED,
			planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		default:
			waiting++
		}
	}
	for _, q := range exp.GetQuestions() {
		if q.GetWithdrawal() == nil && q.GetAnswer() == nil && !render.Asleep(q) {
			open++
		}
	}
	return fmt.Sprintf("- **%s** (`%s`): %d running, %d waiting, %d open questions.\n",
		oneLine(wish.GetTitle()), wish.GetId(), running, waiting, open)
}

// landsLine says in one line where the wish's work lands, and how to change it: its integration branch in each of its
// projects, named when it has several; or, per azima, each azima's own branch landing in main, and the wish's branch
// for work of no azima. Empty for a wish without a branch, outside Git.
func landsLine(exp *planv1.WishExport, strategy planv1.PushStrategy) string {
	wish := exp.GetWish()
	names := map[string]string{}
	for _, p := range exp.GetProjects() {
		names[strings.ToLower(p.GetId())] = p.GetName()
	}
	var branches []string
	for _, id := range wish.GetProjectIds() {
		branch := IntegrationBranchOf(wish, id)
		if branch == "" {
			continue
		}
		if len(wish.GetProjectIds()) > 1 {
			branches = append(branches, fmt.Sprintf("`%s` (%s)", branch, names[strings.ToLower(id)]))
		} else {
			branches = append(branches, "`"+branch+"`")
		}
	}
	change := "`djinn wish set-integration <wish> --branch <branch>`"
	if len(wish.GetProjectIds()) > 1 {
		change = "`djinn wish set-integration <wish> --project-id <project> --branch <branch>`"
	}
	if strategy == planv1.PushStrategy_PUSH_STRATEGY_AZIMA {
		line := "- Work lands per azima: each azima on its own branch, `djinn/<code>-<slug>` or the one named with " +
			"`djinn task update <azima> --branch <name>`, cut from main and landing in main through its pull request"
		if len(branches) > 0 {
			line += "; work of no azima in " + strings.Join(branches, ", ")
		}
		return line + ".\n"
	}
	if len(branches) == 0 {
		return ""
	}
	return "- Work lands in " + strings.Join(branches, ", ") + ": " + change + " changes it.\n"
}

func activeWishBrief(b *strings.Builder, exp *planv1.WishExport, rank int32, ready bool, strategy planv1.PushStrategy, waits map[string][]string) {
	wish := exp.GetWish()
	fmt.Fprintf(b, "# The wish: %s\n\n", oneLine(wish.GetTitle()))
	if desc := strings.TrimSpace(wish.GetDescription()); desc != "" {
		b.WriteString(clipText(desc, briefDescription) + "\n\n")
	}
	fmt.Fprintf(b, "- Identifier: `%s`, the `<wish>` of the commands below.\n", wish.GetId())
	if rank > 0 {
		fmt.Fprintf(b, "- Active, rank %d among the active wishes.\n", rank)
	} else {
		b.WriteString("- Active.\n")
	}
	if refs := exp.GetProjects(); len(refs) > 0 {
		names := make([]string, len(refs))
		for i, p := range refs {
			names[i] = p.GetName()
		}
		b.WriteString("- Projects: " + strings.Join(names, ", ") + ".\n")
	}
	b.WriteString(landsLine(exp, strategy))
	if t := wish.GetTemplate(); t != nil {
		fmt.Fprintf(b, "- Made from the wish template of the skill `%s`: follow that skill.", t.GetSkill())
		if t.GetWatch() != "" {
			fmt.Fprintf(b, " Its watcher runs `%s` and wakes you on each change.", stripCredentials(t.GetWatch()))
		}
		if t.GetDoneWhen() != "" {
			fmt.Fprintf(b, " When it prints `%s`, Djinn asks the developer whether to grant the wish.", t.GetDoneWhen())
		}
		b.WriteString("\n")
	}
	if ready {
		b.WriteString("- Djinn proposes to grant it: every task is finished and no question is open. Granting is the developer's word.\n")
	}
	codes := map[string]string{}
	for _, t := range exp.GetTasks() {
		codes[t.GetId()] = t.GetCode()
	}
	for _, p := range wish.GetPushes() {
		if held := p.GetHeld(); held != "" {
			var proj string
			if len(exp.GetProjects()) > 1 {
				for _, pr := range exp.GetProjects() {
					if pr.GetId() == p.GetProjectId() {
						proj = " in " + pr.GetName()
						break
					}
				}
			}
			corr := ""
			if cID := p.GetCorrectedBy(); cID != "" {
				if code := codes[cID]; code != "" {
					corr = fmt.Sprintf(", corrected by %s", code)
				}
			}
			fmt.Fprintf(b, "- Push held%s: %s%s.\n", proj, held, corr)
		}
	}
	var tilasms []*planv1.Tilasm
	for _, t := range exp.GetTilasms() {
		tilasms = append(tilasms, t.GetTilasm())
	}
	azimasBrief(b, exp.GetTasks(), codes, tilasms, strategy == planv1.PushStrategy_PUSH_STRATEGY_AZIMA)

	var running, waiting []*planv1.Task
	for _, t := range exp.GetTasks() {
		if IsAzima(t) {
			continue
		}
		switch t.GetStatus() {
		case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED:
			running = append(running, t)
		case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED,
			planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		default:
			waiting = append(waiting, t)
		}
	}
	slices.SortStableFunc(running, render.ByMotion)
	slices.SortStableFunc(waiting, render.ByMotion)
	if len(running) > 0 {
		b.WriteString("\n## Running\n\n")
		for _, t := range running {
			name := providerName(t.GetProvider())
			if t.GetModel() != "" {
				name += " · " + t.GetModel()
			}
			fmt.Fprintf(b, "- **%s** %s (%s%s", t.GetCode(), clipLine(t.GetTitle()), name, ifritWord(t))
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
			ifritWaitsBrief(b, t, waits)
		}
	}
	if len(waiting) > 0 {
		b.WriteString("\n## Waiting\n\n")
		for _, t := range waiting {
			fmt.Fprintf(b, "- **%s** %s", t.GetCode(), clipLine(t.GetTitle()))
			if e := codes[t.GetPartOf()]; e != "" {
				b.WriteString(" (part of " + e + ")")
			}
			b.WriteString(": " + waitText(t) + "\n")
		}
	}
	questionsBrief(b, exp)
}

// questionsBrief writes a wish's open questions, the most urgent first; those asleep, each with what it sleeps until;
// and those to investigate.
func questionsBrief(b *strings.Builder, exp *planv1.WishExport) {
	var open, asleep, investigate []*planv1.Question
	for _, q := range exp.GetQuestions() {
		switch {
		case q.GetWithdrawal() != nil, q.GetAnswer() != nil:
		case Investigating(q):
			investigate = append(investigate, q)
		case render.Asleep(q):
			asleep = append(asleep, q)
		default:
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
			case render.Move:
				level = "your move"
			case render.Before:
				level = clipLine(q.GetBefore())
			}
			fmt.Fprintf(b, "- **%s** %s (%s)\n", q.GetCode(), clipLine(q.GetText()), level)
			for i, o := range q.GetOptions() {
				fmt.Fprintf(b, "  - %c: %s\n", 'A'+i, clipLine(o))
			}
			if rec := q.GetRecommendation(); rec != "" {
				fmt.Fprintf(b, "  - Recommended: %s\n", clipLine(rec))
			}
			if n := q.GetRevision(); n > 0 {
				fmt.Fprintf(b, "  - Revised %d times\n", n)
			}
			if q.GetLaterTime() != nil {
				b.WriteString("  - Put off for later: listed below the others\n")
			}
			if render.Woken(q) {
				fmt.Fprintf(b, "  - Woke %s, after sleeping until %s: listed first; check its facts still hold\n",
					when(q.GetSleep().GetWakeTime().AsTime()), SleepTexts(q))
			}
		}
	}
	if len(asleep) > 0 {
		b.WriteString("\n## Asleep questions\n\n")
		b.WriteString("Out of the developer's questions until what each sleeps until holds; then it wakes, and you check " +
			"it. `djinn question sleep <question> --until …` changes that, `--wake` wakes it now.\n\n")
		for _, q := range asleep {
			fmt.Fprintf(b, "- **%s** %s (until %s)\n", q.GetCode(), clipLine(q.GetText()), SleepTexts(q))
		}
	}
	if len(investigate) > 0 {
		b.WriteString("\n## To investigate\n\n")
		b.WriteString("The developer asked to find out more before deciding. Dig, then `djinn question revise <question> " +
			"--wish-id <wish>` with what you found (`--context`, `--options`, `--recommendation`, `--before`), the " +
			"context in the shape every question keeps: the commands in one code block at the top, then short Markdown.\n\n")
		for _, q := range investigate {
			last := q.GetRounds()[len(q.GetRounds())-1]
			fmt.Fprintf(b, "- **%s** %s (asked %s)", q.GetCode(), clipLine(q.GetText()), when(last.GetCreateTime().AsTime()))
			if note := last.GetNote(); note != "" {
				b.WriteString(": " + clipLine(note))
			}
			b.WriteString("\n")
		}
	}
}
