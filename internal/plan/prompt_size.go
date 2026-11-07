package plan

import (
	"context"
	"errors"
	"unicode/utf8"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
)

// Who keeps the prompt sizes of the tasks from before PromptSize, as the journal records it.
const promptMethod = "harness/prompt-size" // a task's prompt size was read from its events; the request is the size

// CountPrompt keeps, in tx, the size of ev's text when ev is the first prompt of its task: Djinn writes every event of
// a task through it (harness putEvents), beside CountGate. A later prompt (a continue, a wake) keeps the first's.
func CountPrompt(ctx context.Context, tx *store.Tx, ev *planv1.TaskEvent) error {
	if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
		return nil
	}
	if _, err := store.Get[*planv1.PromptSize](ctx, tx, ev.GetTaskId()); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return tx.Put(promptSize(ev.GetTaskId(), ev))
}

// promptSize is the size of the task's prompt from ev, its first prompt event; 0 for none.
func promptSize(taskID string, ev *planv1.TaskEvent) *planv1.PromptSize {
	return &planv1.PromptSize{Id: taskID, Length: int64(utf8.RuneCountInString(ev.GetText()))}
}

// KeepPromptSizes keeps in s the prompt size of each of tasks that has none, from its first prompt event, all in one
// transaction: the tasks from before PromptSize, at the first start after the update, and never again. It reads one
// statement per 500 tasks for those it finds kept, then the events of each other, oldest first, up to its first prompt.
func KeepPromptSizes(ctx context.Context, s *store.Store, tasks []*planv1.Task) error {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.GetId()
	}
	kept, err := store.GetMany[*planv1.PromptSize](ctx, s, ids)
	if err != nil {
		return err
	}
	var sizes []*planv1.PromptSize
	for _, id := range ids {
		if _, ok := kept[id]; ok || id == "" {
			continue
		}
		var first *planv1.TaskEvent
		err := store.Oldest(ctx, s, store.Where{"task_id": id}, func(ev *planv1.TaskEvent) bool {
			if ev.GetKind() != planv1.TaskEventKind_TASK_EVENT_KIND_PROMPT {
				return true
			}
			first = ev
			return false
		})
		if err != nil {
			return err
		}
		sizes = append(sizes, promptSize(id, first))
	}
	if len(sizes) == 0 {
		return nil
	}
	return s.Tx(ctx, func(tx *store.Tx) error {
		for _, size := range sizes {
			// A prompt written since was counted by CountPrompt: its size stands.
			if _, err := store.Get[*planv1.PromptSize](ctx, tx, size.GetId()); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := tx.Journal(gateActor, promptMethod, size); err != nil {
				return err
			}
			if err := tx.Put(size); err != nil {
				return err
			}
		}
		return nil
	})
}

// PromptLengths sets on tasks the length of their first prompt kept (Task.prompt_length), read in one pass.
func PromptLengths(ctx context.Context, r store.Reader, tasks []*planv1.Task) error {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.GetId()
	}
	sizes, err := store.GetMany[*planv1.PromptSize](ctx, r, ids)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		t.PromptLength = sizes[t.GetId()].GetLength()
	}
	return nil
}
