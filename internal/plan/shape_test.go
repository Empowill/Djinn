package plan

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/lead/notes"
)

// noteExamples are the contexts the note readable-questions shows, in the order it shows them: the developer's
// example as it came, then in the shape.
func noteExamples(t *testing.T) []string {
	t.Helper()
	n, ok := notes.Find("readable-questions")
	if !ok {
		t.Fatal("no note readable-questions")
	}
	var examples []string
	for _, part := range strings.Split(n.Body, "````markdown\n")[1:] {
		example, _, ok := strings.Cut(part, "\n````")
		if !ok {
			t.Fatalf("an example of readable-questions has no end:\n%s", part)
		}
		examples = append(examples, example)
	}
	if len(examples) != 2 {
		t.Fatalf("readable-questions shows %d examples, want the developer's and its rewrite", len(examples))
	}
	return examples
}

// TestContextWarningsNoteExamples (W575): the developer's example breaks the shape, where its commands are lost in
// one paragraph; the note's rewrite keeps it.
func TestContextWarningsNoteExamples(t *testing.T) {
	examples := noteExamples(t)
	bad := ContextWarnings(examples[0])
	for _, want := range []string{
		"line 1: a command or a flag in the text, outside the code block",
		"line 1: a paragraph of",
	} {
		if !containsPrefix(bad, want) {
			t.Errorf("the developer's example: warnings %q, want one starting %q", bad, want)
		}
	}
	if good := ContextWarnings(examples[1]); len(good) > 0 {
		t.Errorf("the note's rewrite: warnings %q, want none", good)
	}
}

func TestContextWarnings(t *testing.T) {
	long := strings.Repeat("This sentence says something worth reading. ", 20)
	for _, c := range []struct {
		name, context string
		want          []string // the start of each warning, in order
	}{
		{name: "empty"},
		{name: "a plain why and bullets", context: "Why: the release waits for your tag.\n\n- Verified: the build is green.\n- Not verified: the notes."},
		{
			name:    "commands at the top, then Markdown",
			context: "```sh\n# Prints the tag\ngit tag v1.2.0\ngit push origin v1.2.0\n```\n\nWhy: the release starts on the tag.",
		},
		{
			name:    "a tilde fence",
			context: "~~~\n# the dry run\ngo run ./cmd/stork migrate --dry-run\n~~~\n\nWhy: it writes nothing.",
		},
		{
			name:    "an output block after the text holds no command",
			context: "Why: the test fails on main.\n\n```\n--- FAIL: TestX (0.01s)\n    x_test.go:12: got 1\n```",
		},
		{
			name:    "a command in a sentence",
			context: "Run git switch main, then answer.",
			want:    []string{"line 1: a command or a flag in the text"},
		},
		{
			name:    "a command in inline code in a sentence",
			context: "Why: it pushes.\n\n- Then run `djinn project push djinn` once done.",
			want:    []string{"line 3: a command or a flag in the text"},
		},
		{
			name:    "a flag in a sentence",
			context: "The same, without --dry-run.",
			want:    []string{"line 1: a command or a flag in the text"},
		},
		{
			name:    "a prompt line",
			context: "- $ make release",
			want:    []string{"line 1: a command or a flag in the text"},
		},
		{
			name:    "a path and a hash in sentences",
			context: "The fix is in internal/plan/brief.go.\n\nIt landed at 5d254fef6.",
			want:    []string{"lines 1, 3: a path or a hash in a sentence"},
		},
		{
			name: "a hash in a table, a link, a word and a number",
			context: "| Commit | Branch |\n|---|---|\n| 5d254fef6 | main |\n\nSee [the run](https://ci.example/runs/5d254fef6/a/b) " +
				"and https://git.example/x/commit/abcdef1234; facade and 1234567 are no hash.",
		},
		{
			name:    "two blocks of commands",
			context: "```\ngit fetch\n```\n\n```bash\nmake\n```",
			want:    []string{"2 code blocks of commands (lines 1, 5)"},
		},
		{
			name:    "commands after text",
			context: "Why: the tag starts the release.\n\n```\ngit push origin v1\n```",
			want:    []string{"the code block of commands starts at line 3, after text"},
		},
		{
			name:    "a wall",
			context: long,
			want:    []string{"line 1: a paragraph of 879 characters", "879 characters of text with no list"},
		},
		{name: "a long list", context: strings.Repeat("- "+long[:200]+"\n", 5)},
		{
			name:    "a list over a screen",
			context: strings.Repeat("- "+long[:200]+"\n", 6),
			want:    []string{"1212 characters of text outside the code block, over 1200"},
		},
		{
			name: "a long code block, short text",
			context: "~~~sh\n" + strings.Repeat("# "+long[:200]+"\ngit fetch\n", 8) + "~~~\n\n" +
				"Why: the fetch brings the fix.\n\nDetails: djinn://tilasm/01a1",
		},
		{
			name:    "a long list item",
			context: "- " + long,
			want:    []string{"line 1: a paragraph of 881 characters"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ContextWarnings(c.context)
			if len(got) != len(c.want) {
				t.Fatalf("warnings %q, want %d starting %q", got, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !strings.HasPrefix(got[i], w) {
					t.Errorf("warning %d = %q, want it to start %q", i, got[i], w)
				}
			}
		})
	}
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// TestAskReviseWarn (W575): ask and revise store a context as given, and say how it breaks the shape; a revision
// that fixes it, or keeps a context in shape, says nothing.
func TestAskReviseWarn(t *testing.T) {
	ctx := t.Context()
	c := serve(t)
	wish := c.wish(t)
	bad := "Run git switch main, then answer."
	ask, err := c.questions.Ask(ctx, connect.NewRequest(&planv1.QuestionServiceAskRequest{
		Text: "Which branch?", WishId: wish, Context: bad,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if q, w := ask.Msg.GetQuestion(), ask.Msg.GetWarnings(); q.GetContext() != bad || len(w) != 1 ||
		!strings.HasPrefix(w[0], "line 1: a command or a flag in the text") {
		t.Errorf("ask: context %q, warnings %q", q.GetContext(), w)
	}
	ref := &planv1.QuestionRef{Ref: &planv1.QuestionRef_Code{Code: ask.Msg.GetQuestion().GetCode()}}
	// A revision without a context keeps the former one, and its warning.
	rev, err := c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Recommendation: "A: main.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if w := rev.Msg.GetWarnings(); len(w) != 1 {
		t.Errorf("revise keeping the context: warnings %q, want its one", w)
	}
	good := "~~~\ngit switch main\n~~~\n\nWhy: the fix is on main."
	rev, err = c.questions.Revise(ctx, connect.NewRequest(&planv1.QuestionServiceReviseRequest{
		Question: ref, WishId: wish, Context: good,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if q, w := rev.Msg.GetQuestion(), rev.Msg.GetWarnings(); q.GetContext() != good || len(w) > 0 {
		t.Errorf("revise in shape: context %q, warnings %q", q.GetContext(), w)
	}
}
