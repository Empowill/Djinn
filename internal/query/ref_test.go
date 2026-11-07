package query

import (
	"errors"
	"testing"
	"time"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestCoerce(t *testing.T) {
	t.Parallel()
	cols := map[string]Column{}
	for _, c := range Schema((&planv1.Task{}).ProtoReflect().Descriptor()) {
		cols[c.Ref] = c
	}
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local).UTC()
	for _, tc := range []struct {
		ref  string
		in   any
		want any
	}{
		{"code", "W1", "W1"},
		{"status", "done", "TASK_STATUS_DONE"},
		{"status", "TASK_STATUS_DONE", "TASK_STATUS_DONE"},
		{"status", 3, "TASK_STATUS_DONE"},
		{"status", "LOST", nil},
		{"status", 99, nil},
		{"usage.input_tokens", "12", int64(12)},
		{"usage.input_tokens", 12.0, int64(12)},
		{"usage.input_tokens", 1.5, nil},
		{"usage.input_tokens", "x", nil},
		{"usage.cost_usd", "0.5", 0.5},
		{"usage.cost_usd", 2, 2.0},
		{"usage.cost_usd", true, nil},
		{"urgent", "true", true},
		{"urgent", false, false},
		{"urgent", 1, nil},
		{"create_time", "2026-11-02T09:00:00+01:00", time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)},
		{"create_time", "2026-11-02", day},
		{"create_time", day0, day0},
		{"create_time", "yesterday", nil},
		{"running", "90m", 90 * time.Minute},
		{"running", time.Second, time.Second},
		{"running", 5, nil},
		{"code", 5, nil},
		{"code", nil, nil},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()
			got, err := coerce(cols[tc.ref], tc.in)
			if tc.want == nil {
				if err == nil {
					t.Errorf("coerce(%s, %v) = %v; want an error", tc.ref, tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("coerce(%s, %v) = %v, %v; want %v", tc.ref, tc.in, got, err, tc.want)
			}
		})
	}
}

func TestRef(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	md := (&planv1.Task{}).ProtoReflect().Descriptor()
	for _, tc := range []struct {
		ref, err string
	}{
		{"", "an empty field name"},
		{"nope", "has no field"},
		{"prompt", "is hidden"},
		{"usage", "is a message"},
		{"code.x", "holds no fields"},
		{"create_time.seconds", "holds no fields"},
		{"runs.reason", "holds no fields"},
		{"code->title", "names no other collection"},
		{"wish_id->prompt", "has no field prompt"},
		{"part_of->prompt", "is hidden"},
		{"wish_id->", "an empty field name"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()
			comp := &compiler{cat: c, targets: targets()}
			_, err := comp.ref(md, tc.ref)
			if !errors.Is(err, ErrInvalid) || !contains(err, tc.err) {
				t.Errorf("ref %q: %v; want ErrInvalid with %q", tc.ref, err, tc.err)
			}
		})
	}
	t.Run("a jump to a type no collection holds", func(t *testing.T) {
		t.Parallel()
		only, err := NewCatalog(Collection{Name: "TASKS", Source: c.byName["TASKS"].Source})
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&compiler{cat: only, targets: targets()}).ref(md, "wish_id->title")
		if !contains(err, "no collection holds plan.v1.Wish") {
			t.Errorf("wish_id->title without WISHES: %v", err)
		}
	})
}
