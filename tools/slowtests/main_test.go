package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCheck: the output of a passing test is dropped, that of a failing one printed; a top-level test over the limit
// is slow, a subtest is not counted on its own, and an allowed test is not slow. Sum includes top-level tests only.
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
	start := time.Unix(1000, 0)
	clockCalls := 0
	fakeClock := func() time.Time {
		if clockCalls == 0 {
			clockCalls++
			return start
		}
		return start.Add(12 * time.Second)
	}

	res, err := checkClock(strings.NewReader(in), &out, 2*time.Second, fakeClock)
	if err != nil {
		t.Fatal(err)
	}
	if res.failed || out.String() != "ok  \tp\t9s\n" {
		t.Errorf("failed = %v, out = %q", res.failed, out.String())
	}
	if len(res.slow) != 2 || res.slow[0].name != "p TestSlower" || res.slow[1].name != "p TestSlow" {
		t.Errorf("slow = %+v", res.slow)
	}
	// Top-level tests: TestFast (0.1s), TestSlow (3s), TestAllowed (9s), TestSlower (5s) = 17.1s.
	// TestSlow/case is a subtest and must not be in sum.
	if got, want := res.sum, 17100*time.Millisecond; got != want {
		t.Errorf("sum = %v, want %v", got, want)
	}
	if got, want := res.wallClock, 12*time.Second; got != want {
		t.Errorf("wallClock = %v, want %v", got, want)
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

// TestCheckCutPackage (W530): a goroutine of a test that had ended failed it, a panic that ended the package's test
// binary in the middle of the next test, to which the panic's output went; go test then printed one FAIL line for the
// package, and nothing on why. The tests left running are named, and the package's raw output printed. A package that
// failed to build is not: its build errors are printed already.
func TestCheckCutPackage(t *testing.T) {
	in := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestTicks"}`,
		`{"Action":"output","Package":"p","Test":"TestTicks","Output":"=== RUN   TestTicks\n"}`,
		`{"Action":"output","Package":"p","Test":"TestTicks","Output":"--- PASS: TestTicks (0.40s)\n"}`,
		`{"Action":"pass","Package":"p","Test":"TestTicks","Elapsed":0.4}`,
		`{"Action":"run","Package":"p","Test":"TestNext"}`,
		`{"Action":"output","Package":"p","Test":"TestNext","Output":"=== RUN   TestNext\n"}`,
		`{"Action":"output","Package":"p","Test":"TestNext","Output":"panic: Log in goroutine after TestTicks has completed\n"}`,
		`{"Action":"output","Package":"p","Output":"FAIL\tp\t9.245s\n"}`,
		`{"Action":"fail","Package":"p","Elapsed":9.245}`,
		`{"Action":"output","Package":"q","Output":"ok  \tq\t1s\n"}`,
		`{"Action":"pass","Package":"q","Elapsed":1}`,
	}, "\n")
	var out strings.Builder
	res, err := check(strings.NewReader(in), &out, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"FAIL\tp\t9.245s\nslowtests: p failed, yet none of its tests did",
		"Tests that never ended: TestNext.\n",
		"Its raw output:\n=== RUN   TestTicks\n--- PASS: TestTicks (0.40s)\n=== RUN   TestNext\npanic: Log in goroutine after TestTicks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if !res.failed || !strings.HasSuffix(got, "ok  \tq\t1s\n") {
		t.Errorf("failed = %v, out:\n%s", res.failed, got)
	}

	in = strings.Join([]string{
		`{"Action":"build-output","ImportPath":"p [p.test]","Output":"p_test.go:1: syntax error\n"}`,
		`{"Action":"build-fail","ImportPath":"p [p.test]"}`,
		`{"Action":"output","Package":"p","Output":"FAIL\tp [build failed]\n"}`,
		`{"Action":"fail","Package":"p","Elapsed":0,"FailedBuild":"p [p.test]"}`,
	}, "\n")
	out.Reset()
	if res, err = check(strings.NewReader(in), &out, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if want := "p_test.go:1: syntax error\nFAIL\tp [build failed]\n"; !res.failed || out.String() != want {
		t.Errorf("failed = %v, out = %q, want %q", res.failed, out.String(), want)
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
	for goos, want := range map[string]time.Duration{"linux": 10 * time.Second, "windows": 15 * time.Second, "darwin": 15 * time.Second} {
		if got := failLimit(goos, 10*time.Second, 15*time.Second); got != want {
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

func TestDefaultMaxAndMargin(t *testing.T) {
	if defaultMax != 10*time.Second {
		t.Errorf("defaultMax = %v, want 10s", defaultMax)
	}
	if warnMargin != 0.25 {
		t.Errorf("warnMargin = %v, want 0.25", warnMargin)
	}
}

func TestBaselineLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "baseline.json")

	if err := saveBaseline(path, 95*time.Second, 45*time.Second); err != nil {
		t.Fatalf("saveBaseline failed: %v", err)
	}

	base, err := loadBaseline(path)
	if err != nil {
		t.Fatalf("loadBaseline failed: %v", err)
	}
	if base.Sum != 95*time.Second || base.WallClock != 45*time.Second {
		t.Errorf("got sum=%v, wc=%v; want 95s, 45s", base.Sum, base.WallClock)
	}
}

func TestReportTotal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "baseline.json")
	if err := saveBaseline(path, 100*time.Second, 40*time.Second); err != nil {
		t.Fatal(err)
	}

	// 1. Within baseline (+10% margin, threshold is 25%): no warning
	var out, errOut strings.Builder
	res := result{sum: 110 * time.Second, wallClock: 42 * time.Second}
	reportTotal(&out, &errOut, res, path, false)
	if !strings.Contains(out.String(), "Suite total: 110s sum, 42s wall clock (baseline: 100s sum, 40s wall clock)") {
		t.Errorf("unexpected out: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected warning: %q", errOut.String())
	}

	// 2. Exceeds baseline by > 25%: warns on stderr, but check does not fail
	out.Reset()
	errOut.Reset()
	res = result{sum: 130 * time.Second, wallClock: 55 * time.Second}
	reportTotal(&out, &errOut, res, path, false)
	if !strings.Contains(errOut.String(), "WARNING: test suite total time exceeds baseline by more than 25%") {
		t.Errorf("expected warning in errOut: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "sum 130s exceeds baseline 100s by 30.0%") {
		t.Errorf("expected sum warning detail: %q", errOut.String())
	}

	// 3. Update baseline flag saves measured values
	out.Reset()
	errOut.Reset()
	reportTotal(&out, &errOut, res, path, true)
	if !strings.Contains(out.String(), "Updated baseline") {
		t.Errorf("expected updated message: %q", out.String())
	}
	updated, err := loadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Sum != 130*time.Second || updated.WallClock != 55*time.Second {
		t.Errorf("updated baseline: sum=%v, wc=%v", updated.Sum, updated.WallClock)
	}

	// 4. Update baseline skipped if tests failed
	out.Reset()
	errOut.Reset()
	failRes := result{failed: true, sum: 200 * time.Second, wallClock: 90 * time.Second}
	reportTotal(&out, &errOut, failRes, path, true)
	if !strings.Contains(errOut.String(), "tests failed; baseline not updated") {
		t.Errorf("expected failure message: %q", errOut.String())
	}
	// Baseline should remain 130s, 55s
	current, _ := loadBaseline(path)
	if current.Sum != 130*time.Second {
		t.Errorf("baseline was overwritten on failure: %v", current.Sum)
	}
}

func TestFindBaseline(t *testing.T) {
	if got := findBaseline("custom.json"); got != "custom.json" {
		t.Errorf("findBaseline(custom) = %s, want custom.json", got)
	}
	// When file does not exist, default fallback
	_ = os.Remove("nonexistent.json")
	if got := findBaseline(""); got != "tools/slowtests/baseline.json" && got != "baseline.json" {
		t.Errorf("findBaseline() = %s", got)
	}
}
