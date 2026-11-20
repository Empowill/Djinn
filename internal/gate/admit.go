package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/empowill/djinn/internal/machine"
)

// A worker's Bash or Monitor command that neither reads nor edits goes through the gates: Claude's PreToolUse hook
// (djinn gate hook) waits until Djinn admits it, then lets it run as the worker wrote it. Its input is never changed:
// in auto mode, Claude refuses a call a hook changed after its classifier reviewed it (W9, "Auto-Mode Bypass"). Djinn
// finds the shell that runs it among the agent's processes (machine.FindCommand), and holds the admission until it ends.

// HookInput is what Claude's PreToolUse hook reads on its standard input: the tool and its input, the folder.
type HookInput struct {
	Tool  string         `json:"tool_name"`
	Input map[string]any `json:"tool_input"`
	Cwd   string         `json:"cwd"`
}

// Hook is Claude's PreToolUse hook on Bash and Monitor: it reads the call from in and, when its command neither reads
// nor edits (Plain), has admit wait for its admission, given the command as its cost is kept (machine.Key), its folder and
// what its shell is found by (machine.Match). admit says why the command waited, "" when it did not: the hook then
// writes it to out, for the worker to read beside the command's result. It writes nothing else: Claude runs the call
// as it was. It fails only when it cannot read the call; admit failing (no Djinn answers) lets the command run.
func Hook(in io.Reader, out io.Writer, admit func(what, dir, match string) (string, error)) error {
	var call HookInput
	if err := json.NewDecoder(in).Decode(&call); err != nil {
		return fmt.Errorf("read the call: %w", err)
	}
	command, _ := call.Input["command"].(string)
	if command == "" || Plain(command) {
		return nil
	}
	waited, err := admit(machine.Key(command, call.Cwd), call.Cwd, machine.Match(command))
	if err != nil || waited == "" {
		return nil //nolint:nilerr // The command runs: the gates never stop a worker's command.
	}
	return json.NewEncoder(out).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "additionalContext": waited},
	})
}

// gitReads are git's commands that read or edit the worktree, and run no hook.
var gitReads = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true, "rev-parse": true, "rev-list": true,
	"ls-files": true, "ls-tree": true, "cat-file": true, "grep": true, "blame": true, "describe": true,
	"remote": true, "config": true, "merge-base": true, "reflog": true, "shortlog": true, "tag": true,
	"show-ref": true, "for-each-ref": true, "symbolic-ref": true, "name-rev": true, "add": true, "mv": true,
	"rm": true, "restore": true, "switch": true, "checkout": true, "stash": true, "reset": true, "worktree": true,
	"apply": true, "check-ignore": true, "var": true, "version": true, "help": true,
}

// stripped are the words a shell runs its arguments under with no option, as Claude's permission rules strip them,
// and env, whose assignments are stripped after it.
var stripped = map[string]bool{"time": true, "nohup": true, "command": true, "builtin": true, "noglob": true, "env": true}

// Plain says whether command only reads or edits, its every part being a light command (machine.Light) or a reading git: it
// passes with no admission. A here-document is data its command reads, cat <<'EOF' > notes.md writes a file (W82);
// a substitution in its body runs, unless its delimiter is quoted. One it cannot read through, a substitution, a
// subshell, a find that runs or deletes, is not plain.
func Plain(command string) bool {
	code, docs := machine.HereDocs(command)
	texts := []string{code}
	for _, d := range docs {
		if !d.Quoted {
			texts = append(texts, d.Body)
		}
	}
	if slices.ContainsFunc(texts, func(s string) bool {
		return strings.Contains(s, "$(") || strings.Contains(s, "`") || strings.Contains(s, "<(") || strings.Contains(s, ">(")
	}) {
		return false
	}
	parts, ok := split(code)
	if !ok {
		return false
	}
	for _, words := range parts {
		for len(words) > 0 && (assignment(words[0]) || stripped[words[0]]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		head := path.Base(words[0])
		switch {
		case !machine.Light(head) && head != "git":
			return false
		case head == "git" && !gitRead(words[1:]):
			return false
		case head == "find" && slices.ContainsFunc(words, func(w string) bool {
			return w == "-exec" || w == "-execdir" || w == "-ok" || w == "-okdir" || w == "-delete"
		}):
			return false
		}
	}
	return true
}

// gitRead says whether git's arguments args name a command of gitReads, past its options (-C dir, -c name=value).
func gitRead(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return gitReads[a]
		}
	}
	return true // git alone, or git --version.
}

// assignment says whether word sets a variable for the command after it: NAME=value.
func assignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// errUnread is a command split cannot read through.
var errUnread = errors.New("unread")

// split cuts command into its simple commands, each into its words, quotes removed: at ;, &, |, &&, || and new lines.
// A redirection is left out with its target, a here-document's operator with its delimiter: command comes without
// their bodies (machine.HereDocs). ok is false for what it cannot read: an open quote, a group or a subshell, a
// here-string.
func split(command string) (parts [][]string, ok bool) {
	var words []string
	var word strings.Builder
	inWord := false
	end := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	cut := func() {
		end()
		if len(words) > 0 {
			parts = append(parts, words)
		}
		words = nil
	}
	err := func() error {
		for i := 0; i < len(command); i++ {
			c := command[i]
			switch {
			case c == '\'':
				j := strings.IndexByte(command[i+1:], '\'')
				if j < 0 {
					return errUnread
				}
				word.WriteString(command[i+1 : i+1+j])
				inWord = true
				i += j + 1
			case c == '"':
				j := i + 1
				for ; j < len(command) && command[j] != '"'; j++ {
					if command[j] == '\\' {
						j++
					}
				}
				if j >= len(command) {
					return errUnread
				}
				word.WriteString(command[i+1 : j])
				inWord = true
				i = j
			case c == '\\' && i+1 < len(command):
				if command[i+1] != '\n' {
					word.WriteByte(command[i+1])
					inWord = true
				}
				i++
			case c == ';' || c == '&' || c == '|' || c == '\n':
				cut()
			case c == '(' || c == ')' || c == '{' && !inWord || c == '}' && !inWord:
				return errUnread
			case c == '<' && strings.HasPrefix(command[i:], "<<<"):
				return errUnread
			case c == '>' || c == '<':
				// A redirection: its target is no word of the command. 2>&1 and >&2 name no file. A here-document's
				// target is its delimiter.
				if inWord && strings.Trim(word.String(), "0123456789") == "" {
					word.Reset()
					inWord = false
				}
				end()
				if strings.HasPrefix(command[i:], "<<") {
					i++
				}
				for i+1 < len(command) && (command[i+1] == '>' || command[i+1] == '&' || command[i+1] == '|') {
					i++
				}
				for i+1 < len(command) && (command[i+1] == ' ' || command[i+1] == '\t') {
					i++
				}
				// Skip the target, up to the next blank or separator.
				for i+1 < len(command) && !strings.ContainsRune(" \t\n;&|", rune(command[i+1])) {
					i++
				}
			case c == ' ' || c == '\t':
				end()
			case c == '#' && !inWord:
				// A comment, to the end of its line.
				for i+1 < len(command) && command[i+1] != '\n' {
					i++
				}
			default:
				word.WriteByte(c)
				inWord = true
			}
		}
		return nil
	}()
	if err != nil {
		return nil, false
	}
	cut()
	return parts, true
}
