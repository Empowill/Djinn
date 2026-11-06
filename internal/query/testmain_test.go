package query

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/testx"
)

func TestMain(m *testing.M) {
	testx.Main(m)
}

// id is the n-th identifier of the frozen set, in the order of the rows.
func id(n int) string { return fmt.Sprintf("01900000-0000-7000-8000-%012d", n) }

// day0 is the first day of the frozen set.
var day0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// frozen is the frozen set: one project, two wishes, four tasks, two questions.
//
//	T1 W1 of "Stats", DONE, WORK, CLAUDE, cost 1.5, ran 90 min, merged; prompt and last line hidden
//	T2 W2 of "Stats", RUNNING, cost 0 (a usage with no cost yet), part of T1, waits on Q1, depends on T1
//	T3 W1 of "Docs", no status, no usage, no project, urgent
//	T4 W3 of "Stats", FAILED, cost 0.25, its wish_id names no wish
//	Q1 of "Stats", asked by T2, answered A; Q2 of "Docs", asked by the lead, its context hidden
func frozen() []proto.Message {
	stats, docs := id(2), id(3)
	t1, t2 := id(4), id(5)
	return []proto.Message{
		&planv1.Project{Id: id(1), Name: "djinn", Directory: "/src/djinn", Git: true},
		&planv1.Wish{Id: stats, Title: "Stats", ProjectIds: []string{id(1)}, State: planv1.WishState_WISH_STATE_ACTIVE,
			CreateTime: timestamppb.New(day0)},
		&planv1.Wish{Id: docs, Title: "Docs", State: planv1.WishState_WISH_STATE_GRANTED, Rank: 2,
			CreateTime: timestamppb.New(day0.Add(time.Hour))},
		&planv1.Task{
			Id: t1, WishId: stats, ProjectId: id(1), Code: "W1", Title: "Core query",
			Status: planv1.TaskStatus_TASK_STATUS_DONE, Kind: planv1.TaskKind_TASK_KIND_WORK,
			Provider: planv1.Provider_PROVIDER_CLAUDE, Usage: &planv1.Usage{CostUsd: 1.5, InputTokens: 100},
			CreateTime: timestamppb.New(day0), EndTime: timestamppb.New(day0.Add(2 * time.Hour)),
			Running: durationpb.New(90 * time.Minute), Prompt: "secret prompt", LastLine: "secret line",
			Integration: &planv1.TaskIntegration{State: planv1.IntegrationState_INTEGRATION_STATE_COMMITTED},
		},
		&planv1.Task{
			Id: t2, WishId: stats, ProjectId: id(1), Code: "W2", Title: "Aggregates",
			Status: planv1.TaskStatus_TASK_STATUS_RUNNING, Kind: planv1.TaskKind_TASK_KIND_WORK,
			Provider: planv1.Provider_PROVIDER_CODEX, Usage: &planv1.Usage{}, PartOf: t1, WaitQuestionId: id(8),
			DependsOn: []string{t1}, CreateTime: timestamppb.New(day0.Add(24 * time.Hour)),
		},
		&planv1.Task{
			Id: id(6), WishId: docs, Code: "W1", Title: "docs page", Urgent: true,
			CreateTime: timestamppb.New(day0.Add(48 * time.Hour)),
		},
		&planv1.Task{
			Id: id(7), WishId: id(99), ProjectId: id(1), Code: "W3", Title: "Arrow writer",
			Status: planv1.TaskStatus_TASK_STATUS_FAILED, Usage: &planv1.Usage{CostUsd: 0.25},
			CreateTime: timestamppb.New(day0.Add(time.Hour)),
		},
		&planv1.Question{Id: id(8), Code: "Q1", WishId: stats, TaskId: t2, Text: "Which chart?", Context: "hidden",
			CreateTime: timestamppb.New(day0.Add(25 * time.Hour)),
			Answer:     &planv1.Answer{Choice: planv1.Choice_CHOICE_A, CreateTime: timestamppb.New(day0.Add(26 * time.Hour))}},
		&planv1.Question{Id: id(9), Code: "Q2", WishId: docs, Text: "Which page?",
			CreateTime: timestamppb.New(day0.Add(50 * time.Hour))},
	}
}

// openFrozen opens a store in memory holding the frozen set.
func openFrozen(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, "", &planv1.Project{}, &planv1.Wish{}, &planv1.Task{}, &planv1.Question{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	err = s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.Journal("test", "/test.v1.TestService/Put", &planv1.ProjectServiceListRequest{}); err != nil {
			return err
		}
		for _, m := range frozen() {
			if err := tx.Put(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// spy is a source that keeps the where of each load.
type spy struct {
	Source
	mu     sync.Mutex
	wheres []map[string]string
}

func (s *spy) Load(ctx context.Context, where map[string]string) ([]proto.Message, error) {
	s.mu.Lock()
	s.wheres = append(s.wheres, where)
	s.mu.Unlock()
	return s.Source.Load(ctx, where)
}

// catalog is the catalog of the frozen set: TASKS, behind a spy, QUESTIONS, WISHES and PROJECTS.
func catalog(t *testing.T) (*Catalog, *spy) {
	t.Helper()
	s := openFrozen(t)
	src := func(m proto.Message) Source {
		t.Helper()
		out, err := FromStore(s, m)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	tasks := &spy{Source: src(&planv1.Task{})}
	c, err := NewCatalog(
		Collection{Name: "TASKS", Source: tasks, Defaults: []string{"code", "title", "status"}},
		Collection{Name: "QUESTIONS", Source: src(&planv1.Question{})},
		Collection{Name: "WISHES", Source: src(&planv1.Wish{})},
		Collection{Name: "PROJECTS", Source: src(&planv1.Project{})},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c, tasks
}

func targets() map[protoreflect.FullName]map[string]protoreflect.Message {
	return map[protoreflect.FullName]map[string]protoreflect.Message{}
}

// contains tells whether err's text holds s.
func contains(err error, s string) bool { return err != nil && strings.Contains(err.Error(), s) }
