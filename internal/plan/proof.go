package plan

// The Done-when section of a plan file says when its azima is done: a box per proof, checked only with its proof, and
// left unchecked with "(needs: …)" when a person, a machine, a release or a real model must give it (plan/README.md).
// ReadDoneWhen reads it strictly: an unchecked box that says no needs, nor holds boxes that do, is work left. The words
// of the needs say who gives each proof (Provers), without a model.

import (
	"regexp"
	"slices"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// DoneWhen is what the Done-when section of a plan file says.
type DoneWhen struct {
	// Found: the file has a "## Done when" section.
	Found bool
	// How many of its boxes are checked, and how many are not, nested ones included.
	Checked, Unchecked int
	// What the unchecked boxes need, box by box, when every one of them says so or holds boxes that do: nothing is
	// left for a worker. Nil while one unchecked box is work.
	Needs []*planv1.ProofNeed
}

// AllChecked tells whether every box of the section is checked: the azima is done, whatever the status line says.
func (d DoneWhen) AllChecked() bool { return d.Found && d.Checked > 0 && d.Unchecked == 0 }

// box is a checkbox of the section: its indent, whether it is checked, its text joined over its lines.
type box struct {
	indent  int
	checked bool
	text    string
}

var (
	boxLine     = regexp.MustCompile(`^( *)[-*] \[([ xX])\] (.*)$`)
	doneHeading = regexp.MustCompile(`(?i)^(#{1,6}) +done[ -]when *$`)
	heading     = regexp.MustCompile(`^(#{1,6}) `)
)

// ReadDoneWhen reads the Done-when sections of a plan file's body: each from its heading to the next heading of its
// level or above. A file holds several when azimas were merged into it, one in each merged part ("### Done when"
// under "## From T03 · …"): their boxes count together. A box goes on over the lines indented under it; a box
// indented under another is nested in it; a box struck through is left out.
func ReadDoneWhen(body []byte) DoneWhen {
	var d DoneWhen
	var boxes []*box
	level := 0
	var cur *box
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line = strings.ReplaceAll(line, "\t", "    ")
		if level == 0 {
			if m := doneHeading.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				level, d.Found = len(m[1]), true
			}
			continue
		}
		if m := heading.FindStringSubmatch(line); m != nil && len(m[1]) <= level {
			level, cur = 0, nil
			if m := doneHeading.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				level = len(m[1])
			}
			continue
		}
		if m := boxLine.FindStringSubmatch(line); m != nil {
			cur = &box{indent: len(m[1]), checked: m[2] != " ", text: strings.TrimSpace(m[3])}
			boxes = append(boxes, cur)
			continue
		}
		text := strings.TrimSpace(line)
		switch {
		case text == "":
		case cur != nil && len(line)-len(strings.TrimLeft(line, " ")) > cur.indent:
			cur.text += " " + text
		default:
			cur = nil
		}
	}
	covered := true
	var needs []*planv1.ProofNeed
	for i, b := range boxes {
		if struck(b.text) {
			continue // Struck through: superseded, neither proof nor work.
		}
		if b.checked {
			d.Checked++
			continue
		}
		d.Unchecked++
		text, need, ok := CutNeeds(b.text)
		if ok {
			provers, reviewer := Provers(need)
			needs = append(needs, &planv1.ProofNeed{Box: text, Needs: need, Provers: provers, Reviewer: reviewer})
			continue
		}
		// A box without needs of its own waits on the boxes nested in it: one of them is unchecked, and is read in
		// turn. Without one, it is work.
		nested := false
		for _, n := range boxes[i+1:] {
			if n.indent <= b.indent {
				break
			}
			nested = nested || !n.checked && !struck(n.text)
		}
		if !nested {
			covered = false
		}
	}
	if covered && len(needs) > 0 {
		d.Needs = needs
	}
	return d
}

// struck tells a box struck through, "~~No React file changed.~~ Superseded by Q23": it no longer counts.
func struck(text string) bool {
	rest, ok := strings.CutPrefix(text, "~~")
	return ok && strings.Contains(rest, "~~")
}

// CutNeeds splits a box's text from its needs: "(needs: …)", a parenthesis that opens on them, to its closing one.
// False when the box says none, or leaves the parenthesis open.
func CutNeeds(text string) (rest, needs string, ok bool) {
	i := strings.Index(text, "(needs:")
	if i < 0 {
		return text, "", false
	}
	depth := 0
	for j := i; j < len(text); j++ {
		switch text[j] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				needs = strings.TrimSpace(text[i+len("(needs:") : j])
				if needs == "" {
					return text, "", false
				}
				rest = strings.Join(strings.Fields(text[:i]+" "+text[j+1:]), " ")
				return rest, needs, true
			}
		}
	}
	return text, "", false
}

// The words that name who or what gives a proof. Each is matched on whole words, case aside.
var (
	macWords     = regexp.MustCompile(`(?i)\b(mac|macos|finder|wkwebview)\b`)
	windowsWords = regexp.MustCompile(`(?i)\bwindows\b`)
	reviewWords  = regexp.MustCompile(`(?i)\breview(s|ed)?\b`)
	reviewer     = regexp.MustCompile(`([\p{Lu}][\p{L}-]*)['’]s review`)
	releaseWords = regexp.MustCompile(`(?i)\b(release|tag)\b|` + "`v\\*`")
	modelWords   = regexp.MustCompile(`(?i)\breal (model|run|lead|agent)s?\b|\bpaid\b|\breal lead session\b`)
	personWords  = regexp.MustCompile(`(?i)\b(person|people|maintainer|by hand|by voice|decide|decision|microphone)\b`)
)

// Provers says who or what gives the proof a box needs, from the words of its needs, in the order of the enum: a Mac,
// a Windows machine, a review by a named person, a release, a real model, a person; something else when none of
// them is named. reviewer is the person whose review it needs ("Clément's review").
func Provers(needs string) (provers []planv1.Prover, reviewerName string) {
	add := func(p planv1.Prover, ok bool) {
		if ok {
			provers = append(provers, p)
		}
	}
	if m := reviewer.FindStringSubmatch(needs); m != nil {
		reviewerName = m[1]
	}
	add(planv1.Prover_PROVER_MAC, macWords.MatchString(needs))
	add(planv1.Prover_PROVER_WINDOWS, windowsWords.MatchString(needs))
	add(planv1.Prover_PROVER_REVIEW, reviewerName != "" || reviewWords.MatchString(needs))
	add(planv1.Prover_PROVER_RELEASE, releaseWords.MatchString(needs))
	add(planv1.Prover_PROVER_REAL_MODEL, modelWords.MatchString(needs))
	add(planv1.Prover_PROVER_PERSON, personWords.MatchString(needs))
	if len(provers) == 0 {
		provers = []planv1.Prover{planv1.Prover_PROVER_OTHER}
	}
	return provers, reviewerName
}

// GivenByAPerson tells whether a person can give the proof: its needs name a person or a review.
func GivenByAPerson(n *planv1.ProofNeed) bool {
	return slices.ContainsFunc(n.GetProvers(), func(p planv1.Prover) bool {
		return p == planv1.Prover_PROVER_PERSON || p == planv1.Prover_PROVER_REVIEW
	})
}

// ProverWord names who or what gives a proof, in the brief's words: "a Mac", "Clément's review".
func ProverWord(p planv1.Prover, n *planv1.ProofNeed) string {
	switch p {
	case planv1.Prover_PROVER_MAC:
		return "a Mac"
	case planv1.Prover_PROVER_WINDOWS:
		return "a Windows machine"
	case planv1.Prover_PROVER_REVIEW:
		if n.GetReviewer() != "" {
			return n.GetReviewer() + "'s review"
		}
		return "a review"
	case planv1.Prover_PROVER_RELEASE:
		return "a release"
	case planv1.Prover_PROVER_REAL_MODEL:
		return "a real model"
	case planv1.Prover_PROVER_PERSON:
		return "a person"
	}
	return clipLine(n.GetNeeds())
}

// NeedsWords says what an azima's proofs need, each giver once, in the order of the enum: "a Mac, Clément's review".
func NeedsWords(needs []*planv1.ProofNeed) string {
	type word struct {
		p    planv1.Prover
		text string
	}
	var words []word
	for _, n := range needs {
		for _, p := range n.GetProvers() {
			w := word{p, ProverWord(p, n)}
			if !slices.Contains(words, w) {
				words = append(words, w)
			}
		}
	}
	slices.SortStableFunc(words, func(a, b word) int { return int(a.p) - int(b.p) })
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.text
	}
	return strings.Join(out, ", ")
}
