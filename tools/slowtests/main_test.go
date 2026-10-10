package main

import (
	"strings"
	"testing"
	"time"
)

// TestCheck: the output of a passing test is dropped, that of a failing one printed; a top-level test over the limit
// is slow, a subtest is not counted on its own, and an allowed test is not slow.
func TestCheck(t *testing.T) {
	allowed["p TestAllowed"] = "a reason"
	t.Cleanup(func() { delete(allowed, "p TestAllowed") })
	in := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestFast"}`,
		`{"Action":"output","Package":"p","Test":"TestFast","Output":"quiet\n"}`,
		`{"Action":"pass","Package":"p","Test":"TestFast","Elapsed":0.1}`,
		`{"Action":"pass","Package":"p","Test":"TestSlow/case","Elapsed":3}`,
		`{"Action":"pass","Package":"p","Test":"TestSlow","Elapsed":3}`,
		`{"Action":"pass","Package":"p","Test":"TestAllowed","Elapsed":9}`,
		`{"Action":"pass","Package":"p","Test":"TestSlower","Elapsed":5}`,
		`{"Action":"output","Package":"p","Output":"ok  \tp\t9s\n"}`,
		`{"Action":"pass","Package":"p","Elapsed":9}`,
	}, "\n")
	var out strings.Builder
	res, err := check(strings.NewReader(in), &out, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.failed || out.String() != "ok  \tp\t9s\n" {
		t.Errorf("failed = %v, out = %q", res.failed, out.String())
	}
	if len(res.slow) != 2 || res.slow[0].name != "p TestSlower" || res.slow[1].name != "p TestSlow" {
		t.Errorf("slow = %+v", res.slow)
	}

	in = strings.Join([]string{
		`{"Action":"output","Package":"p","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}`,
		`{"Action":"fail","Package":"p","Test":"TestBad","Elapsed":0}`,
		`{"Action":"fail","Package":"p","Elapsed":0.1}`,
	}, "\n")
	out.Reset()
	if res, err = check(strings.NewReader(in), &out, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if !res.failed || out.String() != "--- FAIL: TestBad (0.00s)\n" {
		t.Errorf("failed = %v, out = %q", res.failed, out.String())
	}

	out.Reset()
	if res, _ = check(strings.NewReader("go: no Go files\n"), &out, time.Second); !res.failed {
		t.Error("a line that is not an event did not fail")
	}
}

// TestAllowedSayWhy: a test may stay slow only with a reason.
func TestAllowedSayWhy(t *testing.T) {
	for name, why := range allowed {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is allowed to be slow without a reason", name)
		}
	}
}

// TestFailLimit: on Linux a test over -max fails; on Windows or macOS, where a process costs ten times more, only one
// over -max-elsewhere does, the others reported.
func TestFailLimit(t *testing.T) {
	for goos, want := range map[string]time.Duration{"linux": 2 * time.Second, "windows": 15 * time.Second, "darwin": 15 * time.Second} {
		if got := failLimit(goos, 2*time.Second, 15*time.Second); got != want {
			t.Errorf("failLimit(%s) = %s, want %s", goos, got, want)
		}
	}
	if got := failLimit("windows", 3*time.Second, time.Second); got != 3*time.Second {
		t.Errorf("a -max-elsewhere under -max: %s, want -max", got)
	}
	tests := []slow{{"p TestHangs", 20 * time.Second}, {"p TestGit", 7 * time.Second}}
	if got := over(tests, 15*time.Second); len(got) != 1 || got[0].name != "p TestHangs" {
		t.Errorf("over 15 s = %+v, want TestHangs alone", got)
	}
	if got := over(tests, 2*time.Second); len(got) != 2 {
		t.Errorf("over 2 s = %+v, want both", got)
	}
}
