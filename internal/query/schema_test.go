package query

import (
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestSchema(t *testing.T) {
	t.Parallel()
	byRef := map[string]Column{}
	for _, c := range Schema((&planv1.Task{}).ProtoReflect().Descriptor()) {
		if _, twice := byRef[c.Ref]; twice {
			t.Errorf("ref %s twice", c.Ref)
		}
		byRef[c.Ref] = c
	}
	for _, tc := range []struct {
		ref  string
		typ  Type
		list bool
		jump string
	}{
		{"code", TypeString, false, ""},
		{"status", TypeEnum, false, ""},
		{"urgent", TypeBool, false, ""},
		{"usage.input_tokens", TypeInt, false, ""},
		{"usage.cost_usd", TypeFloat, false, ""},
		{"create_time", TypeTime, false, ""},
		{"running", TypeDuration, false, ""},
		{"depends_on", TypeString, true, ""},
		{"integration.state", TypeEnum, false, ""},
		{"wish_id", TypeString, false, "plan.v1.Wish"},
		{"part_of", TypeString, false, "plan.v1.Task"},
		{"wait_question_id", TypeString, false, "plan.v1.Question"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()
			c, ok := byRef[tc.ref]
			if !ok {
				t.Fatalf("no ref %s", tc.ref)
			}
			if c.Type != tc.typ || c.List != tc.list || string(c.Jump) != tc.jump {
				t.Errorf("%s = %s list %v jump %q; want %s list %v jump %q",
					tc.ref, c.Type, c.List, c.Jump, tc.typ, tc.list, tc.jump)
			}
		})
	}
	t.Run("hidden, maps and repeated messages left out", func(t *testing.T) {
		t.Parallel()
		for _, ref := range []string{"prompt", "last_line", "runs", "comments", "usage"} {
			if _, ok := byRef[ref]; ok {
				t.Errorf("ref %s in the schema", ref)
			}
		}
	})
	t.Run("a message that holds itself stops", func(t *testing.T) {
		t.Parallel()
		if len(Schema((&planv1.Question{}).ProtoReflect().Descriptor())) == 0 {
			t.Error("Question has no refs")
		}
	})
}
