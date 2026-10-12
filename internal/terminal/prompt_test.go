package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// shown is what a program shows on a screen of cols×rows after out: the rows readPrompt reads.
func shown(cols, rows int, out string) []string {
	s := newScreen(cols, rows)
	s.write([]byte(out))
	return strings.Split(strings.TrimRight(s.text(), "\n"), "\n")
}

// The approvals read below are those of the fake lead (say_test.go): claudeApproval and codexApproval.

func TestReadPrompt(t *testing.T) {
	trust, err := os.ReadFile(filepath.Join("testdata", "claude-trust.out"))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		out  string
		want *Prompt
	}{
		"claude approval": {claudeApproval, &Prompt{
			Title: "Do you want to proceed?",
			Lines: []string{"Bash command", "djinn task list --wish-id 01a11f56", "List the tasks of the wish"},
			Options: []string{"Yes", "Yes, and don't ask again for djinn task list commands in /Users/me/CODE/dolmen",
				"No, and tell Claude what to do differently (esc)"},
			Selected: 0,
		}},
		// The reason ends with "?" too; the question is the one Codex asks. The list the agent wrote is no choice.
		"codex approval": {codexApproval, &Prompt{
			Title: "Would you like to run the following command?",
			Lines: []string{"Reason: Allow Djinn to read the wish?", "$ djinn wish brief 01a11f56"},
			Options: []string{"Yes, proceed (y)", "Yes, and don't ask again for commands that start with `djinn` (p)",
				"No, and tell Codex what to do differently (esc)"},
			Selected: 0,
		}},
		// Recorded: options without numbers, the first marked.
		"claude trust": {string(trust), &Prompt{
			Lines: []string{"Accessing workspace:", "/private/tmp/djinn-probe/untrusted",
				"Quick safety check: Is this a project you created or one you trust? (Like your own code, a well-known open source",
				"project, or work from your team). If not, take a moment to review what's in this folder first.",
				"Claude Code'll be able to read, edit, and execute files here.", "Security guide"},
			Options:  []string{"No, exit", "Yes, I trust this folder"},
			Selected: 0,
		}},
		"no hint":       {"1. one\r\n2. two\r\n", nil},
		"list far away": {"1. one\r\n2. two\r\n\r\nsome text\r\nmore text\r\nagain\r\nEsc to cancel\r\n", nil},
		"one option":    {"Do you want to proceed?\r\n❯ 1. Yes\r\n\r\nEsc to cancel\r\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := readPrompt(shown(120, 40, tc.out))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("readPrompt =\n%#v\nwant\n%#v", got, tc.want)
			}
		})
	}
}

// TestPromptNeedsAChoice: options on screen without the hint of the keys are no choice, and a choice scrolled
// away is gone.
func TestPromptNeedsAChoice(t *testing.T) {
	term := &Terminal{screen: newScreen(120, 30)}
	term.screen.write([]byte(codexApproval))
	if p := term.Prompt(); p == nil || p.Title != "Would you like to run the following command?" {
		t.Fatalf("Prompt = %#v", p)
	}
	term.screen.write([]byte(strings.Repeat("working…\r\n", 40)))
	if p := term.Prompt(); p != nil {
		t.Fatalf("Prompt after it scrolled away = %#v", p)
	}
}

// TestChoose: the window picks an option of the choice on screen as the keys would, the arrows from the option marked
// then Enter, and only while the screen shows that very choice. The terminal says when a choice comes and goes.
func TestChoose(t *testing.T) {
	oldGap := keyGap
	keyGap = 10 * time.Millisecond
	t.Cleanup(func() { keyGap = oldGap })
	prompted := make(chan string, 16)
	_, term := fakeLead(t, Config{Prompted: func(t *Terminal) { prompted <- promptKey(t.Prompt()) }})
	if term.Prompt() != nil {
		t.Fatal("a prompt at rest")
	}
	from := term.End()
	if err := term.Write([]byte("claude-approval\r")); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains("Tab to amend"))
	p := term.Prompt()
	if p == nil || p.Title != "Do you want to proceed?" || len(p.Options) != 3 || p.Selected != 0 {
		t.Fatalf("Prompt = %#v", p)
	}
	select {
	case key := <-prompted:
		if key != promptKey(p) {
			t.Errorf("prompted with %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no word of the choice")
	}
	from = term.End()
	if err := term.Choose("Do you want to continue?", p.Lines, p.Options, 1); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose on another choice = %v, want ErrNoPrompt", err)
	}
	if err := term.Choose(p.Title, p.Lines, p.Options, 3); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose past the options = %v, want ErrNoPrompt", err)
	}
	none(t, term, from, "nothing was chosen")
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`chose "\x1b[B\r"`))
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("Choose once answered = %v, want ErrNoPrompt", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case key := <-prompted:
			if key == "" {
				return // The choice went.
			}
		case <-deadline:
			t.Fatal("no word of the choice going")
		}
	}
}

// A changed command with the same title and buttons must require a new decision.
func TestChooseRejectsChangedCommand(t *testing.T) {
	term := &Terminal{screen: newScreen(120, 40), done: make(chan struct{})}
	term.screen.write([]byte(codexApproval))
	p := term.Prompt()
	if p == nil {
		t.Fatal("no prompt")
	}
	term.screen.write([]byte("\x1b[2J\x1b[H" + strings.ReplaceAll(codexApproval, "djinn wish brief 01a11f56", "delete another file")))
	if err := term.Choose(p.Title, p.Lines, p.Options, 0); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("stale command: %v", err)
	}
	if promptKey(term.Prompt()) == promptKey(p) {
		t.Fatal("different commands have the same identity")
	}
}
