package terminal

import (
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

// The approvals below are written by hand from the words in the binaries (claude 2.1.295, codex-cli 0.162.0-alpha.2)
// and the layout of their screens; Claude Code's folder trust is recorded (testdata/claude-trust.out).
const (
	claudeBash = "\x1b[38;5;220m" + "────────────────────────────────────────" + "\x1b[39m\r\n" +
		" Bash command\r\n\r\n" +
		"   djinn task list --wish-id 01a11f56\r\n" +
		"   List the tasks of the wish\r\n\r\n" +
		" Do you want to proceed?\r\n" +
		" \x1b[38;5;153m❯\x1b[39m 1. Yes\r\n" +
		"   2. Yes, and don't ask again for djinn task list commands in\r\n" +
		"      /Users/me/CODE/dolmen\r\n" +
		"   3. No, and tell Claude what to do differently (esc)\r\n\r\n" +
		" \x1b[2mEsc to cancel · Tab to amend\x1b[22m\r\n"
	codexCommand = "› Lance le test\r\n\r\n" +
		"• J’utilise le skill djinn. Je vérifie :\r\n" +
		"  1. les tâches\r\n  2. les questions\r\n\r\n" +
		"  Would you like to run the following command?\r\n\r\n" +
		"  Reason: Autoriser l’accès à Djinn pour lire le souhait ?\r\n\r\n" +
		"  $ djinn wish brief 01a11f56\r\n\r\n" +
		"\x1b[1m› 1. Yes, proceed (y)\x1b[0m\r\n" +
		"  2. Yes, and don't ask again for commands that start with `djinn` (p)\r\n" +
		"  3. No, and tell Codex what to do differently (esc)\r\n\r\n" +
		"  Press enter to confirm or esc to cancel\r\n"
)

func TestReadPrompt(t *testing.T) {
	trust, err := os.ReadFile(filepath.Join("testdata", "claude-trust.out"))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		out  string
		want *Prompt
	}{
		"claude approval": {claudeBash, &Prompt{
			Title: "Do you want to proceed?",
			Lines: []string{"Bash command", "djinn task list --wish-id 01a11f56", "List the tasks of the wish"},
			Options: []string{"Yes", "Yes, and don't ask again for djinn task list commands in /Users/me/CODE/dolmen",
				"No, and tell Claude what to do differently (esc)"},
			Selected: 0,
		}},
		// The reason ends with "?" too; the question is the one Codex asks. The list the agent wrote is no choice.
		"codex approval": {codexCommand, &Prompt{
			Title: "Would you like to run the following command?",
			Lines: []string{"Reason: Autoriser l’accès à Djinn pour lire le souhait ?", "$ djinn wish brief 01a11f56"},
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
	term.screen.write([]byte(codexCommand))
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
	from := size(term)
	if err := term.Write([]byte("approval\r")); err != nil {
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
	from = size(term)
	if err := term.Choose("Do you want to continue?", p.Lines, p.Options, 1); err != ErrNoPrompt {
		t.Fatalf("Choose on another choice = %v, want ErrNoPrompt", err)
	}
	if err := term.Choose(p.Title, p.Lines, p.Options, 3); err != ErrNoPrompt {
		t.Fatalf("Choose past the options = %v, want ErrNoPrompt", err)
	}
	none(t, term, from, "nothing was chosen")
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); err != nil {
		t.Fatal(err)
	}
	output(t, term, from, contains(`chose "\x1b[B\r"`))
	if err := term.Choose(p.Title, p.Lines, p.Options, 1); err != ErrNoPrompt {
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
	term.screen.write([]byte(codexCommand))
	p := term.Prompt()
	if p == nil {
		t.Fatal("no prompt")
	}
	term.screen.write([]byte("\x1b[2J\x1b[H" + strings.ReplaceAll(codexCommand, "djinn wish brief 01a11f56", "delete another file")))
	if err := term.Choose(p.Title, p.Lines, p.Options, 0); err != ErrNoPrompt {
		t.Fatalf("stale command: %v", err)
	}
	if promptKey(term.Prompt()) == promptKey(p) {
		t.Fatal("different commands have the same identity")
	}
}
