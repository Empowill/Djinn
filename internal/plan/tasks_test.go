package plan

import (
	"testing"

	"google.golang.org/protobuf/proto"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

func TestFillPrompts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		remote    string
		azimaURL  string
		babysit   *planv1.TaskBabysit
		title     string
		prompt    string
		wantURL   string
		wantNum   int32
		wantNilBS bool
	}{
		{
			name:     "own_babysit_first",
			remote:   "git@gitlab.com:empowill/dolmen.git",
			azimaURL: "https://gitlab.com/empowill/dolmen/-/merge_requests/2864",
			babysit:  &planv1.TaskBabysit{Number: 2800, Url: "https://gitlab.com/empowill/dolmen/-/merge_requests/2800"},
			title:    "Babysit MR !2863",
			prompt:   "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2863 -watch",
			wantURL:  "https://gitlab.com/empowill/dolmen/-/merge_requests/2800",
			wantNum:  2800,
		},
		{
			name:     "azima_pr_second",
			remote:   "git@gitlab.com:empowill/dolmen.git",
			azimaURL: "https://gitlab.com/empowill/dolmen/-/merge_requests/2864",
			title:    "Babysit T1 MR",
			prompt:   "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2863 -watch",
			wantURL:  "https://gitlab.com/empowill/dolmen/-/merge_requests/2864",
		},
		{
			name:    "command_mr_on_gitlab_third",
			remote:  "git@gitlab.com:empowill/dolmen.git",
			title:   "Babysit W17's MR",
			prompt:  "go run ./.agents/skills/babysit-mr-empowill/mrwatch -mr 2863 -watch -state $DJINN_HOME/watchers/$DJINN_TASK_ID/mr-2863",
			wantURL: "https://gitlab.com/empowill/dolmen/-/merge_requests/2863",
		},
		{
			name:    "title_mr_on_gitlab_third",
			remote:  "https://gitlab.com/empowill/dolmen.git",
			title:   "Babysit MR !2899 (ET-4399)",
			prompt:  "sh ./scripts/watch-pipeline.sh",
			wantURL: "https://gitlab.com/empowill/dolmen/-/merge_requests/2899",
		},
		{
			name:    "command_pr_on_github_third",
			remote:  "git@github.com:empowill/djinn.git",
			title:   "Watch PR",
			prompt:  "sh .agents/skills/babysit-pr/watch.sh 40",
			wantURL: "https://github.com/empowill/djinn/pull/40",
		},
		{
			name:      "unrelated_watch_stays_empty",
			remote:    "git@github.com:empowill/djinn.git",
			title:     "Watch it",
			prompt:    "sh .agents/skills/babysit-it/watch.sh 4",
			wantNilBS: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			s, err := store.Open(ctx, "", Entities()...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })

			proj := &planv1.Project{Id: "proj-1", Name: "repo", Remote: tc.remote}
			wish := &planv1.Wish{Id: "wish-1", Title: "Dew", ProjectIds: []string{proj.GetId()}}
			azima := &planv1.Task{
				Id:     "azima-1",
				WishId: wish.GetId(),
				Code:   "T1",
				Title:  "Track",
				Kind:   planv1.TaskKind_TASK_KIND_AZIMA,
				Azima:  &planv1.Azima{},
			}
			if tc.azimaURL != "" {
				azima.Azima.Pr = &planv1.AzimaPr{Url: tc.azimaURL}
			}
			watch := &planv1.Task{
				Id:       "watch-1",
				WishId:   wish.GetId(),
				PartOf:   azima.GetId(),
				Code:     "W1",
				Title:    tc.title,
				Provider: planv1.Provider_PROVIDER_WATCH,
				Babysit:  tc.babysit,
			}
			ev := &planv1.TaskEvent{
				Id:     "ev-1",
				TaskId: watch.GetId(),
				Seq:    1,
				Kind:   planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT,
				Text:   tc.prompt,
			}
			if err := s.Tx(ctx, func(tx *store.Tx) error {
				if err := tx.Journal(actor, "test/setup", &planv1.WishServiceMakeRequest{}); err != nil {
					return err
				}
				for _, m := range []proto.Message{proj, wish, azima, watch, ev} {
					if err := tx.Put(m); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Pass only the watcher to verify FillPrompts resolves its prompt and its azima from the store.
			tasks := []*planv1.Task{watch}
			if err := FillPrompts(ctx, s, tasks); err != nil {
				t.Fatal(err)
			}
			if got := watch.GetPrompt(); got != tc.prompt {
				t.Fatalf("Prompt = %q; want %q", got, tc.prompt)
			}
			if tc.wantNilBS {
				if watch.GetBabysit() != nil {
					t.Fatalf("Babysit = %v; want nil", watch.GetBabysit())
				}
				return
			}
			if got := watch.GetBabysit().GetUrl(); got != tc.wantURL {
				t.Fatalf("Babysit.Url = %q; want %q", got, tc.wantURL)
			}
			if got := watch.GetBabysit().GetNumber(); got != tc.wantNum {
				t.Fatalf("Babysit.Number = %d; want %d", got, tc.wantNum)
			}
		})
	}
}
