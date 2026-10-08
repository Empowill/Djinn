package dispatch

import "testing"

func TestOverlap(t *testing.T) {
	for _, tt := range []struct {
		a, b []string
		want bool
	}{
		{nil, []string{"x"}, true},
		{[]string{"src"}, []string{"src/app"}, true},
		{[]string{"SRC/App"}, []string{"src"}, true},
		{[]string{"src"}, []string{"srcs"}, false},
		{[]string{"docs", "src/a"}, []string{"src/b"}, false},
	} {
		if got := Overlap(tt.a, tt.b); got != tt.want {
			t.Errorf("Overlap(%v, %v) = %v", tt.a, tt.b, got)
		}
	}
}
