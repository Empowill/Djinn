package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

// gitlabRemote is a bare remote of in that takes push options and writes those of each push it receives, one line per
// push, in the file it returns; the project's remote reads as GitLab's.
func (in *integration) gitlabRemote(t *testing.T) string {
	t.Helper()
	bare := in.remote(t)
	in.git(t, bare, "config", "receive.advertisePushOptions", "true")
	pushed := filepath.Join(t.TempDir(), "push-options")
	hook := "#!/bin/sh\necho \"push:${GIT_PUSH_OPTION_0}\" >> '" + filepath.ToSlash(pushed) + "'\n"
	writeFile(t, bare, filepath.Join("hooks", "post-receive"), hook)
	if err := os.Chmod(filepath.Join(bare, "hooks", "post-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := in.db.Tx(t.Context(), func(tx *store.Tx) error {
		p, err := store.Get[*planv1.Project](t.Context(), tx, in.projectID)
		if err != nil {
			return err
		}
		p.Remote = "https://gitlab.example.com/acme/project.git"
		if err := tx.Journal("test", "remote", p); err != nil {
			return err
		}
		return tx.Put(p)
	})
	if err != nil {
		t.Fatal(err)
	}
	return pushed
}

// pushOptions are the push options of each push the remote received, in order: "" for a push with none.
func pushOptions(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.Lines(string(b)) {
		out = append(out, strings.TrimPrefix(strings.TrimSpace(line), "push:"))
	}
	return out
}

func TestPushCISkipBeforeRequest(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	// The push that ends an azima, on GitLab, with no merge request open on its branch, goes with ci.skip: its request,
	// proposed next, runs the one pipeline of that commit (Q331). Once a request is open on the branch, the next push
	// goes with no option: its request's pipeline runs on it.
	t.Run("first push, then request open", func(t *testing.T) {
		t.Parallel()
		in := integrating(t)
		in.setAzimaStrategy(t)
		pushed := in.gitlabRemote(t)
		forge := &fakeForge{lists: map[string]string{}}
		in.mu.Lock()
		in.forge = forge
		in.mu.Unlock()
		azima := in.azima(t, in.wishID, "Letters")
		partOf := func(task *planv1.Task) { task.PartOf = azima.GetId() }
		w1 := in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, partOf)

		in.pass(t, 0)
		branch := azimaBranchName(in.stored(t, azima.GetId()))
		if got := pushOptions(t, pushed); !slices.Equal(got, []string{ciSkip}) {
			t.Fatalf("pushes received with options %q, want one with %s", got, ciSkip)
		}
		if !slices.Contains(forge.called(), "glab mr list --source-branch "+branch+" --output json") {
			t.Errorf("forge calls %q: no look for a request open on %s", forge.called(), branch)
		}
		if _, texts := in.integration(t, w1); !slices.ContainsFunc(texts, func(s string) bool {
			return strings.HasPrefix(s, "pushed "+branch) && strings.HasSuffix(s, "with ci.skip: no branch pipeline, its merge request's runs the CI")
		}) {
			t.Errorf("W1's events %q do not say the push skipped the branch pipeline", texts)
		}

		// The request opens; work added to the azima ends it again.
		forge.mu.Lock()
		forge.lists[branch] = `[{"iid":7,"web_url":"https://gitlab.example.com/acme/project/-/merge_requests/7"}]`
		forge.mu.Unlock()
		w2 := in.finished(t, "W2", map[string]string{"app/src/b.txt": "b\n"}, partOf)
		in.pass(t, 0)
		if got := pushOptions(t, pushed); !slices.Equal(got, []string{ciSkip, ""}) {
			t.Fatalf("pushes received with options %q, want the second with none", got)
		}
		if _, texts := in.integration(t, w2); slices.ContainsFunc(texts, func(s string) bool { return strings.Contains(s, ciSkip) }) {
			t.Errorf("W2's events %q say ci.skip with a request open", texts)
		}
	})

	// ci_skip_before_request: false, or glab that cannot say whether a request is open: the push goes with no option.
	for _, c := range []struct {
		name, settings string
		glabFails      bool
	}{
		{"setting off", "ci_skip_before_request: false\n", false},
		{"glab cannot say", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			in := integrating(t)
			in.setAzimaStrategy(t)
			pushed := in.gitlabRemote(t)
			writeFile(t, in.home, filepath.Join("projects", in.projectID, "settings.txtpb"),
				"generated: \"gen/**\"\ngenerate: \"gen\"\ntest: \"test\"\nmerge_main: MERGE_MAIN_OFF\n"+c.settings)
			in.mu.Lock()
			if c.glabFails {
				in.exits = map[string]int{"glab": 1}
			} else {
				in.forge = &fakeForge{}
			}
			in.mu.Unlock()
			azima := in.azima(t, in.wishID, "Letters")
			in.finished(t, "W1", map[string]string{"app/src/a.txt": "a\n"}, func(task *planv1.Task) { task.PartOf = azima.GetId() })
			in.pass(t, 0)
			if got := pushOptions(t, pushed); !slices.Equal(got, []string{""}) {
				t.Errorf("pushes received with options %q, want one with none", got)
			}
		})
	}
}
