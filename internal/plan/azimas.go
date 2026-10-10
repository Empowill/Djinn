package plan

// Azimas are the tasks of kind AZIMA (Arabic ʿazīma, the incantation that binds and commands a djinn): the plan of a
// wish as a graph, which no worker runs. Work is part of an azima (Task.part_of), and an azima depends on other tasks
// like work does. Where an azima stands is computed on every read (FillAzimas). A project's plan/ folder holds one Markdown file per azima, its front matter read by djinn plan sync
// (ReadAzimaFiles), which writes back what the azima depends on (WriteAfter).

import (
	"bufio"
	"bytes"
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// IsAzima tells whether the task is an azima: no worker ever runs it.
func IsAzima(t *planv1.Task) bool { return t.GetKind() == planv1.TaskKind_TASK_KIND_AZIMA }

// FillAzimas sets Task.azima on the azimas among tasks, from the tasks of their wish among tasks: give it every task of
// the wishes read. An azima is done when its status is and none of its parts is still to finish: a part that runs or
// waits keeps it in progress, whatever its plan file says. It is in progress when one of its parts has a worker on it
// or is done, or an azima part of it is under way; open otherwise. Once every part is finished, its azimas done or to
// validate in their turn, it is to validate (AWAITING_PROOF): nothing is left for Djinn, and the person checks its
// plan file's boxes (Task.proof_needs say what each one needs) and validates it. It is ready when every task it
// depends on is done.
func FillAzimas(tasks []*planv1.Task) {
	byID := make(map[string]*planv1.Task, len(tasks))
	parts := map[string][]*planv1.Task{}
	for _, t := range tasks {
		byID[t.GetId()] = t
		if p := t.GetPartOf(); p != "" {
			parts[p] = append(parts[p], t)
		}
	}
	// under says whether an azima is under way, through its parts and theirs; seen guards a cycle the store refuses.
	var under func(id string, seen map[string]bool) bool
	under = func(id string, seen map[string]bool) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, p := range parts[id] {
			if p.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE || working(p) || IsAzima(p) && under(p.GetId(), seen) {
				return true
			}
		}
		return false
	}
	// state is where an azima stands, its azima parts first; a cycle the store refuses reads as in progress.
	states := map[string]planv1.AzimaState{}
	var state func(t *planv1.Task) planv1.AzimaState
	state = func(t *planv1.Task) planv1.AzimaState {
		if s, ok := states[t.GetId()]; ok {
			return s
		}
		states[t.GetId()] = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
		s := planv1.AzimaState_AZIMA_STATE_OPEN
		// unfinished tells whether a part is still to finish: work not finished, or an azima not done.
		unfinished := slices.ContainsFunc(parts[t.GetId()], func(p *planv1.Task) bool {
			if IsAzima(p) {
				return state(p) != planv1.AzimaState_AZIMA_STATE_DONE
			}
			return !Finished(p)
		})
		// left tells whether a part is still to finish: work not finished, or an azima neither done nor to validate.
		left := slices.ContainsFunc(parts[t.GetId()], func(p *planv1.Task) bool {
			if IsAzima(p) {
				ps := state(p)
				return ps != planv1.AzimaState_AZIMA_STATE_DONE && ps != planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
			}
			return !Finished(p)
		})
		switch {
		case t.GetDraft():
			s = planv1.AzimaState_AZIMA_STATE_DRAFT
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE && !unfinished:
			s = planv1.AzimaState_AZIMA_STATE_DONE
		case t.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE:
			// Closed, but work part of it still runs or waits: in progress until it ends.
			s = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
		case under(t.GetId(), map[string]bool{}):
			s = planv1.AzimaState_AZIMA_STATE_IN_PROGRESS
			if !left {
				s = planv1.AzimaState_AZIMA_STATE_AWAITING_PROOF
			}
		}
		states[t.GetId()] = s
		return s
	}
	for _, t := range tasks {
		if !IsAzima(t) {
			t.Azima = nil
			continue
		}
		e := &planv1.Azima{State: state(t), Ready: true}
		for _, p := range parts[t.GetId()] {
			e.Parts++
			if IsAzima(p) {
				if state(p) == planv1.AzimaState_AZIMA_STATE_DONE {
					e.PartsDone++
				}
			} else if p.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE {
				e.PartsDone++
			}
			if working(p) {
				e.PartsRunning++
			}
		}
		for _, id := range t.GetDependsOn() {
			dep := byID[id]
			depDone := false
			if dep != nil {
				if IsAzima(dep) {
					depDone = state(dep) == planv1.AzimaState_AZIMA_STATE_DONE
				} else {
					depDone = dep.GetStatus() == planv1.TaskStatus_TASK_STATUS_DONE
				}
			}
			if !depDone {
				e.Ready = false
			}
		}
		t.Azima = e
	}
}

// HasUnfinishedParts tells whether an azima has parts still to finish: work not finished,
// or an azima part of it not done.
func HasUnfinishedParts(id string, tasks []*planv1.Task) bool {
	tasks = WithAzimas(tasks)
	for _, t := range tasks {
		if t.GetPartOf() != id {
			continue
		}
		if IsAzima(t) {
			if t.GetAzima().GetState() != planv1.AzimaState_AZIMA_STATE_DONE {
				return true
			}
		} else if !Finished(t) {
			return true
		}
	}
	return false
}

// Finished tells a task of work finished: done, stopped on request, or cut short for good. Djinn resumes by itself
// every task it can (RESUMING), so one left interrupted (resumed as a fork, imported, its worktree gone) is history.
func Finished(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_DONE, planv1.TaskStatus_TASK_STATUS_STOPPED, planv1.TaskStatus_TASK_STATUS_INTERRUPTED:
		return true
	}
	return false
}

// WithAzimas is tasks with Task.azima set on their azimas, as clones: the store's own messages stay as they are.
func WithAzimas(tasks []*planv1.Task) []*planv1.Task {
	if !slices.ContainsFunc(tasks, IsAzima) {
		return tasks
	}
	out := make([]*planv1.Task, len(tasks))
	for i, t := range tasks {
		out[i] = proto.CloneOf(t)
	}
	FillAzimas(out)
	return out
}

// working tells whether a worker is on the task now: running, paused, or about to resume.
func working(t *planv1.Task) bool {
	switch t.GetStatus() {
	case planv1.TaskStatus_TASK_STATUS_RUNNING, planv1.TaskStatus_TASK_STATUS_PAUSED, planv1.TaskStatus_TASK_STATUS_RESUMING:
		return true
	}
	return false
}

// CompareCodes orders task codes as a person reads them: by letter, then by number, T2 before T10.
func CompareCodes(a, b string) int {
	split := func(s string) (string, int) {
		i := strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' })
		if i < 0 {
			return strings.ToUpper(s), -1
		}
		n, err := strconv.Atoi(s[i:])
		if err != nil {
			return strings.ToUpper(s), -1
		}
		return strings.ToUpper(s[:i]), n
	}
	pa, na := split(a)
	pb, nb := split(b)
	return cmp.Or(strings.Compare(pa, pb), cmp.Compare(na, nb), strings.Compare(a, b))
}

// PlanDir is the folder of a project that holds its plan files.
const PlanDir = "plan"

// AzimaFile is what a plan file says of its azima: its front matter, between two lines "---" at the top, and its title,
// the first heading after it ("# T07 · The orchestrator", the code left out).
type AzimaFile struct {
	// Path is the file, as its project names it: plan/8e8d3d76-orchestrator.md.
	Path   string
	ID     string
	Code   string
	Phase  string
	Status string
	Title  string
	// Description is what the file holds after its title heading: its goal and body.
	Description string
	// After is what the file says the azima depends on, by code.
	After []string
	// DoneWhen is what its Done-when sections say: their boxes, and what the unchecked ones need.
	DoneWhen DoneWhen
}

// Done tells whether the file says its azima is done.
func (f AzimaFile) Done() bool { return strings.EqualFold(f.Status, "done") }

// Draft tells whether the file says its azima is a draft.
func (f AzimaFile) Draft() bool { return strings.EqualFold(f.Status, "draft") }

// Closes tells whether the file closes its azima: its status says done, or every box of its Done-when section is
// checked. A draft azima never closes.
func (f AzimaFile) Closes() bool { return !f.Draft() && (f.Done() || f.DoneWhen.AllChecked()) }

// ReadAzimaFiles reads the plan files of a project's folder: plan/*.md with a front matter that gives a code, and their
// Done-when section. A file without one (the README) is not an azima's.
func ReadAzimaFiles(project string) ([]AzimaFile, error) {
	names, err := filepath.Glob(filepath.Join(project, PlanDir, "*.md"))
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var out []AzimaFile
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		f, ok := parseAzimaFile(data)
		if !ok {
			continue
		}
		f.Path = PlanDir + "/" + filepath.Base(name)
		out = append(out, f)
	}
	return out, nil
}

// parseAzimaFile reads a plan file's front matter, title and description; false when it has no front matter with a code.
func parseAzimaFile(data []byte) (AzimaFile, bool) {
	var f AzimaFile
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return f, false
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "id":
			f.ID = value
		case "code":
			f.Code = value
		case "phase":
			f.Phase = value
		case "status":
			f.Status = value
		case "after":
			f.After = strings.Fields(strings.ReplaceAll(value, ",", " "))
		}
	}
	if f.Code == "" {
		return f, false
	}
	f.DoneWhen = ReadDoneWhen(body)
	sc := bufio.NewScanner(bytes.NewReader(body))
	var afterTitle []string
	foundTitle := false
	for sc.Scan() {
		line := sc.Text()
		if !foundTitle {
			if title, ok := strings.CutPrefix(line, "# "); ok {
				f.Title = azimaTitle(strings.TrimSpace(title), f.Code)
				foundTitle = true
			}
			continue
		}
		afterTitle = append(afterTitle, line)
	}
	if foundTitle {
		f.Description = strings.TrimSpace(strings.Join(afterTitle, "\n"))
	} else {
		f.Description = strings.TrimSpace(string(body))
	}
	return f, true
}

// azimaTitle is a plan file's heading without its code: "T07 · The orchestrator" is "The orchestrator".
func azimaTitle(heading, code string) string {
	rest, ok := strings.CutPrefix(heading, code)
	if !ok || rest != "" && !strings.ContainsAny(rest[:1], " ·:-—") {
		return heading
	}
	if rest = strings.TrimLeft(rest, " ·:-—"); rest == "" {
		return heading
	}
	return rest
}

// splitFrontMatter splits a file into the lines of its front matter and what follows it; false without a front matter.
func splitFrontMatter(data []byte) (lines []string, body []byte, ok bool) {
	rest, found := bytes.CutPrefix(data, []byte("---\n"))
	if !found {
		if rest, found = bytes.CutPrefix(data, []byte("---\r\n")); !found {
			return nil, nil, false
		}
	}
	for len(rest) > 0 {
		line, next, _ := bytes.Cut(rest, []byte("\n"))
		text := strings.TrimRight(string(line), "\r")
		if text == "---" {
			return lines, next, true
		}
		lines = append(lines, text)
		rest = next
	}
	return nil, nil, false
}

// WriteAfter writes what an azima depends on, by code, into its plan file's front matter, as "after: T02 T05" after
// its status (or last); none removes the line. It tells whether the file changed: a file that says it already is left
// as it is.
func WriteAfter(file string, codes []string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	var out []string
	at := -1
	for _, line := range lines {
		key, _, _ := strings.Cut(line, ":")
		switch strings.TrimSpace(key) {
		case "after":
			continue
		case "status":
			at = len(out) + 1
		}
		out = append(out, line)
	}
	if at < 0 {
		at = len(out)
	}
	if len(codes) > 0 {
		out = slices.Insert(out, at, "after: "+strings.Join(codes, " "))
	}
	if slices.Equal(out, lines) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(body)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteStatus writes an azima's status ("open", "draft", etc.) into its plan file's front matter.
// It tells whether the file changed.
func WriteStatus(file string, status string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	var out []string
	found := false
	for _, line := range lines {
		key, _, hasKey := strings.Cut(line, ":")
		if hasKey && strings.TrimSpace(key) == "status" {
			out = append(out, "status: "+status)
			found = true
		} else {
			out = append(out, line)
		}
	}
	if !found {
		out = append(out, "status: "+status)
	}
	if slices.Equal(out, lines) {
		return false, nil
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range out {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.Write(body)
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, b.Bytes(), info.Mode().Perm())
}

// WriteDescription writes the description of an azima into its plan file, after its title heading.
// It tells whether the file changed.
func WriteDescription(file string, text string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}
	lines, body, ok := splitFrontMatter(data)
	if !ok {
		return false, nil
	}
	nl := "\n"
	if bytes.HasPrefix(data, []byte("---\r\n")) {
		nl = "\r\n"
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	var titleLine string
	var foundTitle bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# ") {
			titleLine = line
			foundTitle = true
			break
		}
	}
	var b bytes.Buffer
	b.WriteString("---" + nl)
	for _, line := range lines {
		b.WriteString(line + nl)
	}
	b.WriteString("---" + nl)
	b.WriteString(nl)
	if foundTitle {
		b.WriteString(titleLine + nl + nl)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed != "" {
		b.WriteString(trimmed + nl)
	}
	newContent := b.Bytes()
	if bytes.Equal(newContent, data) {
		return false, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(file, newContent, info.Mode().Perm())
}
