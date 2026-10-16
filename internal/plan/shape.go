package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// QuestionShape is the shape of a question's context, the same for every agent that asks or revises one: the lead
// reads it in its brief, a worker at the end of its first prompt, an investigator in its own. The developer, 13/10
// (W575), got revisions whose commands to run were lost in a wall of text; 16/10 (W55), a question is never long, its
// details in a tilasm it links. ContextWarnings checks it.
const QuestionShape = "A question's `--context` reads at a glance, in this order. First, every command the " +
	"developer must run, in one fenced code block, in run order, a `# …` comment above each command saying what it " +
	"prints and what to expect; fence it with `~~~` lines, which need no backtick (between double quotes, backticks " +
	"run as a command). Then short Markdown: a one-line why; a few bullets of facts, each saying whether it is " +
	"verified; the decision, with what each option changes and costs. No command, flag, path or hash inside a " +
	"sentence: it goes in the code block (a branch or a commit as a `#` comment) or in a table. No wall of text: " +
	"short paragraphs, lists, tables and code blocks, which the window renders. Short, too: outside the code block, " +
	"at most 1200 characters, one screen of the window (a one-line why, up to six bullets, the options, the " +
	"recommendation); the details the developer needs to choose with full light (an analysis, a comparison, a " +
	"mockup, a design) go in a tilasm that lays out the possible choices, and the question links it " +
	"(`djinn://tilasm/<id>`) instead of carrying them. `djinn question ask` and `djinn " +
	"question revise` print a warning for each break of this shape: revise the context at once."

// Beyond these, text is a wall: a paragraph (or a list item) of maxParagraph characters, or maxFlatText characters
// of text with no list, table nor heading. Beyond maxText characters of text outside the code blocks, structured or
// not, a context no longer fits on a screen: its details belong in a tilasm (W55).
const (
	maxParagraph = 400
	maxFlatText  = 800
	maxText      = 1200
)

var (
	fenceRe = regexp.MustCompile("^(`{3,}|~{3,})\\s*([\\w+-]*)")
	// linkRe is what a link names, which may hold a path or a hash: a Markdown link's target, an address.
	linkRe = regexp.MustCompile(`\]\([^)]*\)|<[a-z]+://[^>]*>|[a-z][a-z0-9+.-]*://\S+`)
	flagRe = regexp.MustCompile("(?:^|[\\s(`\"'=])--[a-zA-Z][\\w-]*")
	// commandRe is a command the developer would run: a tool and one of its subcommands, lower case, as written in
	// a shell; tools whose name is also an English word need their subcommand.
	commandRe = regexp.MustCompile("(?:^|[\\s(`\"'])(?:(?:git (?:switch|checkout|worktree|push|pull|fetch|merge|" +
		"rebase|reset|clone|commit|cherry-pick|stash|revert|restore|tag|log|show|diff|status|add)|go (?:run|test|" +
		"build|tool|install|vet|mod|generate|get)|djinn (?:wish|task|question|lead|project|gate|block|tilasm|mark|" +
		"plan|skill|brief|up|install|update)|docker (?:run|exec|compose|build|logs|pull|push))\\b|(?:npm|npx|pnpm|" +
		"yarn|gh|glab|kubectl|psql|sudo|curl|wget) [\\w.-])")
	pathRe     = regexp.MustCompile("(?:^|[\\s(`\"'])(?:~|\\.{1,2})?/[\\w.-]+/[\\w./-]*|\\b[\\w-]+(?:/[\\w.-]+)+\\.[a-z]{1,5}\\b")
	hashRe     = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	listRe     = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s`)
	shellInfos = map[string]bool{"sh": true, "bash": true, "shell": true, "zsh": true, "console": true, "powershell": true, "ps1": true, "cmd": true}
)

// ContextWarnings says how context, a question's Markdown, breaks QuestionShape, a line per fault; none when it
// keeps it. Cheap guesses, not a parser: Djinn stores a context as given, and its author revises it.
func ContextWarnings(context string) []string {
	context = CleanContext(context)
	var (
		commands, mentions, commandBlocks []int
		walls                             [][2]int // line, characters
		fence                             string
		blockStart                        int
		blockCommands                     bool
		firstText                         int // line of the first text outside a code block
		flatText                          int
		structured                        bool
		para, paraStart                   int
	)
	endPara := func() {
		if para > maxParagraph {
			walls = append(walls, [2]int{paraStart, para})
		}
		para = 0
	}
	endBlock := func() {
		if blockCommands {
			commandBlocks = append(commandBlocks, blockStart)
		}
		fence, blockCommands = "", false
	}
	for i, raw := range strings.Split(strings.ReplaceAll(context, "\r\n", "\n"), "\n") {
		n, line := i+1, strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == "" {
				endBlock()
				continue
			}
			code := strings.TrimPrefix(line, "$ ")
			if code != "" && !strings.HasPrefix(code, "#") && commandRe.MatchString(code) {
				blockCommands = true
			}
			continue
		}
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			endPara()
			fence, blockStart, blockCommands = m[1], n, shellInfos[strings.ToLower(m[2])]
			continue
		}
		if line == "" {
			endPara()
			continue
		}
		if firstText == 0 {
			firstText = n
		}
		item := listRe.MatchString(line)
		table := strings.HasPrefix(line, "|")
		if item || table || strings.HasPrefix(line, "#") {
			structured = true
		}
		if item || table || strings.HasPrefix(line, "#") || para == 0 {
			endPara()
			paraStart = n
		}
		if !table && !strings.HasPrefix(line, "#") {
			para += len([]rune(line))
		}
		flatText += len([]rune(line))
		bare := linkRe.ReplaceAllString(line, " ")
		if commandRe.MatchString(bare) || flagRe.MatchString(bare) || strings.HasPrefix(strings.TrimLeft(line, "-*+> "), "$ ") {
			commands = append(commands, n)
		} else if !table && (pathRe.MatchString(bare) || hasHash(bare)) {
			mentions = append(mentions, n)
		}
	}
	endPara()
	if fence != "" {
		endBlock()
	}

	var warnings []string
	if len(commands) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: a command or a flag in the text, outside the code block: put "+
			"every command the developer runs in the one code block at the top, none in a sentence", lines(commands)))
	}
	if len(mentions) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: a path or a hash in a sentence: put it in the code block (a # "+
			"comment) or in a table", lines(mentions)))
	}
	if len(commandBlocks) > 1 {
		warnings = append(warnings, fmt.Sprintf("%d code blocks of commands (%s): put every command in one, in run "+
			"order", len(commandBlocks), lines(commandBlocks)))
	}
	if len(commandBlocks) > 0 && firstText > 0 && firstText < commandBlocks[0] {
		warnings = append(warnings, fmt.Sprintf("the code block of commands starts at line %d, after text: put it "+
			"first", commandBlocks[0]))
	}
	for _, w := range walls {
		warnings = append(warnings, fmt.Sprintf("line %d: a paragraph of %d characters: cut it into a one-line why "+
			"and a few bullets", w[0], w[1]))
	}
	if !structured && flatText > maxFlatText {
		warnings = append(warnings, fmt.Sprintf("%d characters of text with no list, table nor heading: write a "+
			"one-line why, then bullets", flatText))
	}
	if flatText > maxText {
		warnings = append(warnings, fmt.Sprintf("%d characters of text outside the code block, over %d: more than a "+
			"screen. Keep the decision here, what each option changes and the recommendation; put the details (an "+
			"analysis, a comparison, a mockup) in a tilasm that lays out the choices, and link it, "+
			"djinn://tilasm/<id>", flatText, maxText))
	}
	return warnings
}

// hasHash tells whether text names what looks like a commit or an identifier: seven hexadecimal digits or more,
// letters and digits mixed.
func hasHash(text string) bool {
	for _, h := range hashRe.FindAllString(text, -1) {
		if strings.ContainsAny(h, "0123456789") && strings.ContainsAny(h, "abcdef") {
			return true
		}
	}
	return false
}

// lines names the lines ns, the first three: "line 2", "lines 2, 5, 9…".
func lines(ns []int) string {
	var s []string
	for _, n := range ns[:min(len(ns), 3)] {
		s = append(s, strconv.Itoa(n))
	}
	text := strings.Join(s, ", ")
	if len(ns) > 3 {
		text += "…"
	}
	if len(ns) == 1 {
		return "line " + text
	}
	return "lines " + text
}
