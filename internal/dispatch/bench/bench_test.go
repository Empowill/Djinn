package bench

import (
	"strings"
	"testing"
	"time"
)

// TestGo: the plain Go scheduler decides every case as written by hand, and says why a task waits or fails.
func TestGo(t *testing.T) {
	cases, err := Cases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("%d cases", len(cases))
	}
	var results []Result
	for _, c := range cases {
		r := Go(c, time.Millisecond)
		results = append(results, r)
		if !r.Right() {
			t.Errorf("%s: wrong for %v: %+v", c.Name, r.Wrong, r.Answers)
		}
		if len(r.Answers) != len(c.Expect) {
			t.Errorf("%s: %d answers for %d planned tasks", c.Name, len(r.Answers), len(c.Expect))
		}
		for _, a := range r.Answers {
			if (a.Decision == Start) != (a.Reason == "") {
				t.Errorf("%s: %s %s with reason %q", c.Name, a.Code, a.Decision, a.Reason)
			}
		}
	}
	var b strings.Builder
	Table(&b, "Go", results)
	if !strings.Contains(b.String(), "Go: 35 of 35 cases right") {
		t.Errorf("table:\n%s", b.String())
	}
}

// TestCheck: a case that names what it lacks, or expects nothing of a planned task, is refused.
func TestCheck(t *testing.T) {
	for _, c := range []Case{
		{Name: "no wish", Tasks: []Task{{Code: "W1", Wish: "A", Status: "planned"}}, Expect: map[string]string{"W1": Start}},
		{Name: "no expectation", Wishes: []Wish{{ID: "A"}}, Tasks: []Task{{Code: "W1", Wish: "A", Status: "planned"}}},
		{Name: "an expectation for a running task", Wishes: []Wish{{ID: "A"}},
			Tasks: []Task{{Code: "W1", Wish: "A", Status: "running"}}, Expect: map[string]string{"W1": Start}},
		{Name: "a bad status", Wishes: []Wish{{ID: "A"}}, Tasks: []Task{{Code: "W1", Wish: "A", Status: "sleeping"}}},
		{Name: "a bad decision", Wishes: []Wish{{ID: "A"}}, Tasks: []Task{{Code: "W1", Wish: "A", Status: "planned"}},
			Expect: map[string]string{"W1": "later"}},
	} {
		if err := c.check(); err == nil {
			t.Errorf("%s: accepted", c.Name)
		}
	}
}
