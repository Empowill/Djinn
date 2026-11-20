package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// KeyLength is the most characters a command's key keeps (Key).
const KeyLength = 200

// A command's key is what its cost is kept and found under in its project: the same command run by another task, in
// another worktree or scratch folder, with other values in its environment, finds the cost measured before (G4 of L04).
// Key builds it from what a shell would run:
//   - the folders that change with each task are named alike: the folder it runs in (dirs) becomes ".", a worktree of
//     Djinn $WORKTREE, a scratch folder $DJINN_SCRATCH, a file under /tmp /tmp/*, any other UUID $ID;
//   - a variable set for the command keeps its name, not its value (GOFLAGS=*);
//   - sh -c '…' (bash, zsh, dash; -lc, -ec…) is the command it runs;
//   - what only sets up the shell (cd, export, set, echo…), the wrappers that change nothing it costs (time, nohup,
//     env, timeout 600, taskset -c 0-15…), the redirections and the filters a pipe ends with (| tail -50, | grep FAIL)
//     are left out;
//   - a here-document is a word of its command, << and a digest of its body (HereDocs), the folders named alike: the
//     script python3 - <<'EOF' runs is its program, two scripts are two keys (W82); the commands after its body stay;
//   - its words are one space apart, quoted where a shell would need it, up to KeyLength characters.
//
// Arguments stay: go test ./... and go test ./internal/gate are two keys, the whole module and one package (W10).
// Key of a key is itself.
func Key(command string, dirs ...string) string {
	return cut(strings.Join(keyWords(command, dirs, 0), " "))
}

// KeyArgs is the key of the command args runs, a program and its arguments, as Key gives it: djinn gate run's.
func KeyArgs(args []string, dirs ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quote(a)
	}
	return Key(strings.Join(quoted, " "), dirs...)
}

// Head is what a key starts with, the program and its subcommand: go test, go tool task lint, npm run build, npx
// playwright test; python3 for an inline script (python3 - <<…). A command never measured is given the cost of the
// others of its head in its project (Peaks.Cost). "" when the key has no word, holds more than one command (gofmt -l .
// ; go build: its peak is the heavier one's, which no other command of its first word has), or starts with a light
// program (Light: sed -i, mv, cat): such a key neither lends its cost nor borrows one (W139).
func Head(key string) string {
	parts := keyParts(key, nil, 0)
	if len(parts) != 1 {
		return ""
	}
	words := parts[0].words
	for len(words) > 0 && assignment(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 || Light(path.Base(words[0])) {
		return ""
	}
	var head []string
	for i, w := range words {
		if i > 0 && (len(head) == 4 || strings.HasPrefix(w, "-") || strings.ContainsAny(w, "/.=$*:@<")) {
			break
		}
		head = append(head, w)
	}
	return strings.Join(head, " ")
}

// Light says whether program, a command's first word past its folder, only reads or edits and runs nothing else: ls,
// cat, sed, mv, djinn… A command made of light ones only passes the gates with no admission (gate.Plain), and a
// command whose head is light lends its cost to none (Head). Anything else, a build, a test, a package manager, a
// script, an interpreter, a loop, is admitted.
func Light(program string) bool { return lightPrograms[program] }

// lightPrograms are the commands that read or edit and run nothing else (Light).
var lightPrograms = map[string]bool{
	// Reading files and folders.
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true, "fgrep": true,
	"rg": true, "fd": true, "tree": true, "stat": true, "file": true, "du": true, "df": true, "less": true,
	"more": true, "diff": true, "cmp": true, "comm": true, "od": true, "xxd": true, "hexdump": true, "strings": true,
	"sha256sum": true, "sha1sum": true, "md5sum": true, "cksum": true, "realpath": true, "readlink": true,
	"basename": true, "dirname": true, "find": true,
	// Text in a pipe.
	"echo": true, "printf": true, "sort": true, "uniq": true, "cut": true, "tr": true, "awk": true, "sed": true,
	"jq": true, "column": true, "nl": true, "tee": true, "base64": true, "fold": true, "rev": true, "paste": true,
	"join": true, "expand": true, "fmt": true,
	// Editing files and folders.
	"mkdir": true, "rmdir": true, "touch": true, "cp": true, "mv": true, "rm": true, "ln": true, "chmod": true,
	"patch": true,
	// The shell itself, and what tells about the machine.
	"cd": true, "pwd": true, "true": true, "false": true, "test": true, "[": true, "export": true, "unset": true,
	"which": true, "type": true, "date": true, "printenv": true, "id": true, "whoami": true,
	"hostname": true, "uname": true, "ps": true, "pgrep": true, "free": true, "uptime": true, "nproc": true,
	"exit": true,
	// Djinn's own commands talk to Djinn; djinn gate run takes its gate.
	"djinn": true,
}

// cut keeps the first KeyLength characters of key, whole words when it can, whole runes always.
func cut(key string) string {
	if len(key) <= KeyLength {
		return key
	}
	if i := strings.LastIndexByte(key[:KeyLength+1], ' '); i > 0 {
		return key[:i]
	}
	key = key[:KeyLength]
	for !utf8.ValidString(key) {
		key = key[:len(key)-1]
	}
	return key
}

// part is a simple command of a shell line: its words, and the operator before it ("" for the first).
type part struct {
	op    string
	words []string
}

// keyWords is the key of command as words, its operators among them.
func keyWords(command string, dirs []string, depth int) []string {
	var out []string
	for _, p := range keyParts(command, dirs, depth) {
		if len(out) > 0 {
			out = append(out, p.op)
		}
		for _, w := range p.words {
			out = append(out, quote(w))
		}
	}
	return out
}

// keyParts is the simple commands of command that tell what it costs, normalized as Key says.
func keyParts(command string, dirs []string, depth int) []part {
	var all []part
	for _, p := range tokens(command, dirs) {
		words := setUp(p.words)
		if len(words) == 0 {
			continue
		}
		if inner, ok := shellC(words); ok && depth < 3 {
			sub := keyParts(inner, dirs, depth+1)
			if len(sub) > 0 {
				sub[0].op = p.op
			}
			all = append(all, sub...)
			continue
		}
		for i, w := range words {
			words[i] = normalWord(w, dirs)
		}
		all = append(all, part{op: p.op, words: words})
	}
	// What only sets the shell up, or tells, goes; a filter at the end of a pipe too, and a here-document written out
	// by cat or tee, a script before the command that runs it, unless a pipe hands it on. All of it when nothing else is
	// left: the command is then its own key.
	var kept []part
	for i, p := range all {
		head := path.Base(p.words[0])
		piped := i+1 < len(all) && all[i+1].op == "|"
		written := (head == "cat" || head == "tee") && !piped &&
			slices.ContainsFunc(p.words, func(w string) bool { return strings.HasPrefix(w, "<<") })
		if p.op == "|" && filters[head] || !assignment(p.words[0]) && setUps[head] || onlyAssignments(p.words) || written {
			continue
		}
		if len(kept) == 0 {
			p.op = ""
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return all
	}
	return kept
}

// setUps are the commands that only set the shell up or tell something: they cost nothing.
var setUps = map[string]bool{
	"cd": true, "export": true, "set": true, "unset": true, "pushd": true, "popd": true, "source": true, ".": true,
	":": true, "true": true, "false": true, "echo": true, "printf": true, "exit": true, "ulimit": true, "umask": true,
	"shopt": true, "trap": true,
}

// filters are the commands that read what a pipe hands them: at the end of a pipe, they do not change what the
// command before them costs.
var filters = map[string]bool{
	"tail": true, "head": true, "grep": true, "egrep": true, "fgrep": true, "rg": true, "tee": true, "sort": true,
	"uniq": true, "wc": true, "cat": true, "less": true, "more": true, "cut": true, "awk": true, "sed": true, "jq": true,
	"column": true, "tr": true, "nl": true, "fold": true,
}

// wrappers are the words a command runs under without changing what it costs; with the options and the one argument
// each takes (args).
var wrappers = map[string]int{
	"time": 0, "nohup": 0, "command": 0, "builtin": 0, "noglob": 0, "env": 0, "setsid": 0, "stdbuf": 0, "nice": 0,
	"ionice": 0, "timeout": 1, "exec": 0, "taskset": 1,
}

// setUp is words without the wrappers it runs under; a variable it sets keeps its name only (NAME=*).
func setUp(words []string) []string {
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		w := words[i]
		if len(out) > 0 && !assignment(out[len(out)-1]) {
			return append(out, words[i:]...)
		}
		switch n, ok := wrappers[w]; {
		case assignment(w):
			name, _, _ := strings.Cut(w, "=")
			out = append(out, name+"=*")
		case ok:
			// Its options, with their value when it is apart (nice -n 10, timeout -k 5 600), then its arguments.
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") && words[i+1] != "--" {
				i++
				if !strings.Contains(words[i], "=") && len(words[i]) == 2 && i+1 < len(words) && numeric(words[i+1]) {
					i++
				}
			}
			if i+1 < len(words) && words[i+1] == "--" {
				i++
			}
			i += min(n, len(words)-1-i)
		default:
			out = append(out, w)
		}
	}
	return out
}

// numeric says whether w is a number, a duration or a signal's (5, 10s, 2m).
func numeric(w string) bool {
	return w != "" && strings.Trim(w, "0123456789.smhd") == ""
}

// shellC is the command sh -c runs, when words are one: sh, bash, zsh or dash with an option that ends in c (-c, -lc,
// -ec, -euxc), then the command.
func shellC(words []string) (string, bool) {
	switch path.Base(words[0]) {
	case "sh", "bash", "zsh", "dash":
	default:
		return "", false
	}
	for i := 1; i+1 < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "--" {
			return "", false
		}
		if strings.HasSuffix(w, "c") && !strings.HasPrefix(w, "--") {
			return words[i+1], true
		}
	}
	return "", false
}

// onlyAssignments says whether words only set variables: FOO=1 alone, with no command.
func onlyAssignments(words []string) bool {
	return !slices.ContainsFunc(words, func(w string) bool { return !assignment(w) })
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

var (
	// A Djinn worktree: …/projects/<id>/worktrees/<id>, wherever Djinn's data is.
	worktreeRe = regexp.MustCompile(`/[^\s=:,'"]*/worktrees/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// A task's scratch folder: …/scratch/<project id>/<task id>, or its variable.
	scratchRe = regexp.MustCompile(`/[^\s=:,'"]*/scratch/[0-9a-f-]{36}/[0-9a-f-]{36}|\$\{DJINN_SCRATCH\}`)
	// A file under /tmp: mktemp's names change with each run.
	tmpRe  = regexp.MustCompile(`(^|[^\w/.-])/tmp/[^\s'"]+`)
	uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// normalWord is w with the folders that change with each task named alike, as Key says.
func normalWord(w string, dirs []string) string {
	for _, d := range dirs {
		d = strings.TrimRight(d, "/")
		if d == "" || !strings.HasPrefix(d, "/") || !strings.Contains(w, d) {
			continue
		}
		// The folder itself, or a path under it: a path relative to it.
		if i := strings.Index(w, d); i >= 0 {
			rest := w[i+len(d):]
			if rest == "" || rest[0] == '/' {
				w = w[:i] + "." + rest
			}
		}
	}
	w = worktreeRe.ReplaceAllLiteralString(w, "$WORKTREE")
	w = scratchRe.ReplaceAllLiteralString(w, "$DJINN_SCRATCH")
	w = tmpRe.ReplaceAllString(w, "${1}/tmp/*")
	return uuidRe.ReplaceAllLiteralString(w, "$ID")
}

// quote is w as a shell reads it back as one word: as it is when it can, else between single quotes.
func quote(w string) string {
	if w == "" {
		return "''"
	}
	if !strings.ContainsAny(w, " \t\n'\"\\;&|<>#`") {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}

// tokens cuts command into its simple commands, each into its words, quotes removed: at ;, &, |, &&, || and new
// lines, the operator kept with the command after it. A redirection is left out with its target, a comment and a
// here-string too; a here-document is the word << and the digest of its body (HereDocs), and the commands after its body follow. An open
// quote runs to the end.
func tokens(command string, dirs []string) []part {
	command, docs := HereDocs(command)
	var parts []part
	var words []string
	var word strings.Builder
	inWord, hereString := false, false
	op := ""
	end := func() {
		if inWord {
			if !hereString {
				words = append(words, word.String())
			}
			word.Reset()
			inWord, hereString = false, false
		}
	}
	cutAt := func(next string) {
		end()
		hereString = false
		if len(words) > 0 {
			parts = append(parts, part{op: op, words: words})
			op = next
		}
		words = nil
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(command[i+1:], '\'')
			if j < 0 {
				j = len(command) - i - 1
			}
			word.WriteString(command[i+1 : i+1+j])
			inWord = true
			i += j + 1
		case c == '"':
			j := i + 1
			for ; j < len(command) && command[j] != '"'; j++ {
				if command[j] == '\\' && j+1 < len(command) && strings.IndexByte(`"\$`+"`", command[j+1]) >= 0 {
					j++
				}
				word.WriteByte(command[j])
			}
			inWord = true
			i = j
		case c == '\\' && i+1 < len(command):
			if command[i+1] != '\n' {
				word.WriteByte(command[i+1])
				inWord = true
			}
			i++
		case c == '&' && strings.HasPrefix(command[i:], "&&"):
			cutAt("&&")
			i++
		case c == '|' && strings.HasPrefix(command[i:], "||"):
			cutAt("||")
			i++
		case c == '|':
			cutAt("|")
			if strings.HasPrefix(command[i:], "|&") {
				i++
			}
		case c == '&' && strings.HasPrefix(command[i:], "&>"):
			end()
			i = skipTarget(command, i+1)
		case c == ';' || c == '\n' || c == '&':
			if c == '\n' {
				c = ';'
			}
			cutAt(string(c))
		case c == '<' && strings.HasPrefix(command[i:], "<<<"):
			// A here-string: its word, quoted or not, is no word of the command.
			end()
			for i += 2; i+1 < len(command) && (command[i+1] == ' ' || command[i+1] == '\t'); i++ {
			}
			hereString = true
		case c == '<' && strings.HasPrefix(command[i:], "<<"):
			// A here-document: its delimiter goes, its body is a word of the command.
			end()
			i = skipTarget(command, i+1)
			body := ""
			if len(docs) > 0 {
				body, docs = docs[0].Body, docs[1:]
			}
			words = append(words, "<<"+digest(body, dirs))
		case c == '>' || c == '<':
			// A redirection: its target is no word of the command. 2>&1 and >&2 name no file.
			if inWord && strings.Trim(word.String(), "0123456789") == "" {
				word.Reset()
				inWord = false
			}
			end()
			i = skipTarget(command, i)
		case c == ' ' || c == '\t' || c == '\r':
			end()
		case c == '#' && !inWord:
			for i+1 < len(command) && command[i+1] != '\n' {
				i++
			}
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	cutAt("")
	return parts
}

// skipTarget is where the redirection at i ends in command: past its operators, its blanks and its target.
func skipTarget(command string, i int) int {
	for i+1 < len(command) && (command[i+1] == '>' || command[i+1] == '&' || command[i+1] == '|') {
		i++
	}
	for i+1 < len(command) && (command[i+1] == ' ' || command[i+1] == '\t') {
		i++
	}
	for i+1 < len(command) && !strings.ContainsRune(" \t\n;&|", rune(command[i+1])) {
		i++
	}
	return i
}

// HereDoc is a here-document's body, as its command reads it, and whether its delimiter was quoted: the shell then
// expands nothing in it, no variable, no substitution.
type HereDoc struct {
	Body   string
	Quoted bool
}

// HereDocs is command without the bodies of its here-documents (<<EOF, <<-'EOF'), and the bodies, in the order of
// their operators. The operators and their delimiters stay in code; each body, from the line after its operator's to
// its delimiter's line, goes with that line. A body with no delimiter runs to the end.
func HereDocs(command string) (code string, docs []HereDoc) {
	type pending struct {
		delim  string
		dash   bool
		quoted bool
	}
	var out strings.Builder
	var waiting []pending
	single, double := false, false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\\' && !single && i+1 < len(command):
			out.WriteString(command[i : i+2])
			i++
		case c == '\'' && !double:
			single = !single
			out.WriteByte(c)
		case c == '"' && !single:
			double = !double
			out.WriteByte(c)
		case single || double:
			out.WriteByte(c)
		case c == '#' && (i == 0 || strings.IndexByte(" \t\n;&|", command[i-1]) >= 0):
			// A comment, to the end of its line.
			j := strings.IndexByte(command[i:], '\n')
			if j < 0 {
				j = len(command) - i
			}
			out.WriteString(command[i : i+j])
			i += j - 1
		case c == '<' && strings.HasPrefix(command[i:], "<<<"):
			out.WriteString("<<<")
			i += 2
		case c == '<' && strings.HasPrefix(command[i:], "<<"):
			j := i + 2
			p := pending{}
			if j < len(command) && command[j] == '-' {
				p.dash = true
				j++
			}
			for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
				j++
			}
			var delim strings.Builder
			for ; j < len(command) && !strings.ContainsRune(" \t\n;&|<>()", rune(command[j])); j++ {
				switch command[j] {
				case '\'', '"':
					p.quoted = true
					if k := strings.IndexByte(command[j+1:], command[j]); k >= 0 {
						delim.WriteString(command[j+1 : j+1+k])
						j += k + 1
					}
				case '\\':
					p.quoted = true
					if j+1 < len(command) {
						j++
						delim.WriteByte(command[j])
					}
				default:
					delim.WriteByte(command[j])
				}
			}
			p.delim = delim.String()
			out.WriteString(command[i:j])
			i = j - 1
			waiting = append(waiting, p)
		case c == '\n' && len(waiting) > 0:
			out.WriteByte(c)
			// The bodies, one after the other, from the next line.
			for _, p := range waiting {
				var body []string
				for i+1 < len(command) {
					line, _, _ := strings.Cut(command[i+1:], "\n")
					i += len(line) + 1
					if p.dash {
						line = strings.TrimLeft(line, "\t")
					}
					if line == p.delim {
						break
					}
					body = append(body, line)
				}
				docs = append(docs, HereDoc{Body: strings.Join(body, "\n"), Quoted: p.quoted})
			}
			waiting = nil
		default:
			out.WriteByte(c)
		}
	}
	for _, p := range waiting {
		docs = append(docs, HereDoc{Quoted: p.quoted}) // Its body never came.
	}
	return out.String(), docs
}

// digest names a here-document's body in a key: 8 hexadecimal digits of its hash, with the folders that change with
// each task named alike first (normalWord).
func digest(body string, dirs []string) string {
	for _, d := range dirs {
		if d = strings.TrimRight(d, "/"); d != "" && strings.HasPrefix(d, "/") {
			body = strings.ReplaceAll(body, d, ".")
		}
	}
	body = worktreeRe.ReplaceAllLiteralString(body, "$WORKTREE")
	body = scratchRe.ReplaceAllLiteralString(body, "$DJINN_SCRATCH")
	body = tmpRe.ReplaceAllString(body, "${1}/tmp/*")
	body = uuidRe.ReplaceAllLiteralString(body, "$ID")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:4])
}
