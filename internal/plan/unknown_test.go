package plan

import (
	"slices"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

func TestFindUnknownFields(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "valid settings",
			content: "provider: PROVIDER_CLAUDE\nmodel: \"sonnet\"\nmax_budget_usd: 5.0\n",
			want:    nil,
		},
		{
			name:    "unknown top-level field",
			content: "provider: PROVIDER_CLAUDE\nfuture_flag: true\n",
			want:    []string{"future_flag"},
		},
		{
			name:    "multiple unknown fields",
			content: "unknown_one: 123\nprovider: PROVIDER_CLAUDE\nunknown_two: \"abc\"\n",
			want:    []string{"unknown_one", "unknown_two"},
		},
		{
			name:    "unknown message block",
			content: "provider: PROVIDER_CLAUDE\nunknown_section { nested_field: \"value\" }\n",
			want:    []string{"unknown_section"},
		},
		{
			name:    "unknown field inside known message",
			content: "checks { name: \"lint\" command: \"task lint\" when: CHECK_WHEN_COMMIT unknown_check_attr: 123 }\n",
			want:    []string{"unknown_check_attr"},
		},
		{
			name:    "comments and colons in strings",
			content: "# comments: ignored\nsetup: \"echo foo: bar; { baz }\"\nfuture_field: 42\n",
			want:    []string{"future_field"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindUnknownFields([]byte(tc.content), &planv1.ProjectSettings{})
			if !slices.Equal(got, tc.want) {
				t.Errorf("FindUnknownFields() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWarnUnknownField(t *testing.T) {
	ResetWarnedUnknownFields()
	w1 := WarnUnknownField(".agents/settings.txtpb", "future_setting")
	if w1 != "unknown field future_setting: a newer Djinn knows it" {
		t.Errorf("unexpected warning text: %s", w1)
	}
	// Calling again should not re-log (deduplication)
	w2 := WarnUnknownField(".agents/settings.txtpb", "future_setting")
	if w2 != w1 {
		t.Errorf("expected same warning: %s", w2)
	}
}
