package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// events joins lines of go test -json.
func events(lines ...string) string { return strings.Join(lines, "\n") }

// TestRun fails on a failing test, on a test over -max on Linux outside CI (10 s by default), and elsewhere only over
// the limit of that system; it names the slow tests, then the failed ones last, and prints the suite total.
func TestRun(t *testing.T) {
	t.Parallel()
	pass := func(test string, elapsed string) string {
		return `{"Action":"pass","Package":"p","Test":"` + test + `","Elapsed":` + elapsed + `}`
	}
	cases := []struct {
		name   string
		args   []string
		in     string
		goos   string
		ci     bool
		code   int
		stdout string
		stderr []string
	}{
		{"fast tests pass", nil, pass("TestFast", "0.1"), "linux", false, 0, "Suite total: 0.1s sum", nil},
		{"under the default 10 s passes", nil, pass("TestNine", "9.9"), "linux", false, 0, "Suite total", nil},
		{"over the default 10 s fails on Linux", nil, pass("TestEleven", "11"), "linux", false, 1, "",
			[]string{"These tests take longer than 10s", "  11s  p TestEleven"}},
		{"over -max is only reported on CI", nil, pass("TestFifteen", "15"), "linux", true, 0, "",
			[]string{"On CI (linux) they are reported; only one over 20s fails", "p TestFifteen"}},
		{"over twice -max fails on CI", nil, pass("TestHangs", "21"), "linux", true, 1, "", []string{"p TestHangs"}},
		{"over -max is only reported on Windows", nil, pass("TestGit", "12"), "windows", false, 0, "",
			[]string{"On windows they are reported; only one over 15s fails"}},
		{"over -max-elsewhere fails on macOS", []string{"-max-elsewhere", "11s"}, pass("TestGit", "12"), "darwin", false, 1, "",
			[]string{"only one over 11s fails"}},
		{"a failing test fails and is named last", nil, events(
			`{"Action":"output","Package":"p","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}`,
			`{"Action":"fail","Package":"p","Test":"TestBad","Elapsed":0}`,
			`{"Action":"fail","Package":"p","Elapsed":0.1}`,
		), "linux", false, 1, "--- FAIL: TestBad", []string{"\nFailed: p TestBad\n"}},
		{"a line too long is an error", nil, strings.Repeat("x", 17*1024*1024), "linux", false, 1, "",
			[]string{"slowtests: bufio.Scanner: token too long"}},
		{"an unknown flag is a usage error", []string{"-nope"}, "", "linux", false, 2, "", []string{"flag provided but not defined"}},
		{"help is no error", []string{"-h"}, "", "linux", false, 0, "", []string{"-max-elsewhere"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"-baseline", filepath.Join(t.TempDir(), "none.json")}, c.args...)
			var stdout, stderr strings.Builder
			if code := run(args, strings.NewReader(c.in), &stdout, &stderr, c.goos, c.ci); code != c.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), c.stdout) {
				t.Errorf("stdout lacks %q:\n%s", c.stdout, stdout.String())
			}
			for _, w := range c.stderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr.String())
				}
			}
			if strings.Contains(c.in, "TestBad") && !strings.HasSuffix(stderr.String(), "Failed: p TestBad\n") {
				t.Errorf("the failed tests are not last:\n%s", stderr.String())
			}
		})
	}
}

// TestCheck: the output of a passing test is dropped, that of a failing one printed; a top-level test over the limit
// is slow, a subtest is not counted on its own, and an allowed test is not slow. Sum includes top-level tests only.
func TestCheck(t *testing.T) {
	t.Parallel()
	const allowedTest = "github.com/empowill/djinn/cmd/djinn TestCrossVet"
	if allowed[allowedTest] == "" {
		t.Fatalf("%s is no longer allowed: pick another", allowedTest)
	}
	t.Run("slow tests and the sum", func(t *testing.T) {
		t.Parallel()
		in := events(
			`{"Action":"run","Package":"p","Test":"TestFast"}`,
			`{"Action":"output","Package":"p","Test":"TestFast","Output":"quiet\n"}`,
			`{"Action":"pass","Package":"p","Test":"TestFast","Elapsed":0.1}`,
			`{"Action":"pass","Package":"p","Test":"TestSlow/case","Elapsed":3}`,
			`{"Action":"pass","Package":"p","Test":"TestSlow","Elapsed":3}`,
			`{"Action":"pass","Package":"p","Test":"TestSlower","Elapsed":5}`,
			`{"Action":"output","Package":"p","Output":"PASS\n"}`,
			`{"Action":"output","Package":"p","Output":"ok  \tp\t9s\n"}`,
			`{"Action":"pass","Package":"p","Elapsed":9}`,
			``,
			`{"Action":"pass","Package":"github.com/empowill/djinn/cmd/djinn","Test":"TestCrossVet","Elapsed":9}`,
		)
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
		// Top-level tests: TestFast (0.1s), TestSlow (3s), TestSlower (5s), TestCrossVet (9s) = 17.1s.
		// TestSlow/case is a subtest and must not be in sum.
		if got, want := res.sum, 17100*time.Millisecond; got != want {
			t.Errorf("sum = %v, want %v", got, want)
		}
		if got, want := res.wallClock, 12*time.Second; got != want {
			t.Errorf("wallClock = %v, want %v", got, want)
		}
	})
	t.Run("a failing test is printed", func(t *testing.T) {
		t.Parallel()
		in := events(
			`{"Action":"output","Package":"p","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}`,
			`{"Action":"fail","Package":"p","Test":"TestBad","Elapsed":0}`,
			`{"Action":"fail","Package":"p","Elapsed":0.1}`,
		)
		var out strings.Builder
		res, err := check(strings.NewReader(in), &out, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !res.failed || out.String() != "--- FAIL: TestBad (0.00s)\n" {
			t.Errorf("failed = %v, out = %q", res.failed, out.String())
		}
		// A push check keeps only the end of the output: run names the failed tests last (W135).
		if want := []string{"p TestBad"}; !slices.Equal(res.failures, want) {
			t.Errorf("failures = %q, want %q", res.failures, want)
		}
	})
	t.Run("a line that is not an event fails", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		if res, _ := check(strings.NewReader("go: no Go files\n"), &out, time.Second); !res.failed || out.String() != "go: no Go files\n" {
			t.Errorf("failed = %v, out = %q", res.failed, out.String())
		}
	})
	// W530: a goroutine of a test that had ended failed it, a panic that ended the package's test binary in the middle
	// of the next test, to which the panic's output went; go test then printed one FAIL line for the package, and
	// nothing on why. The tests left running are named, and the package's raw output printed.
	t.Run("a package cut in the middle", func(t *testing.T) {
		t.Parallel()
		in := events(
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
		)
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
		if want := []string{"p (its test binary ended in the middle)"}; !slices.Equal(res.failures, want) {
			t.Errorf("failures = %q, want %q", res.failures, want)
		}
	})
	// A package that failed to build is not cut: its build errors are printed already.
	t.Run("a package that failed to build", func(t *testing.T) {
		t.Parallel()
		in := events(
			`{"Action":"build-output","ImportPath":"p [p.test]","Output":"p_test.go:1: syntax error\n"}`,
			`{"Action":"build-fail","ImportPath":"p [p.test]"}`,
			`{"Action":"output","Package":"p","Output":"FAIL\tp [build failed]\n"}`,
			`{"Action":"fail","Package":"p","Elapsed":0,"FailedBuild":"p [p.test]"}`,
		)
		var out strings.Builder
		res, err := check(strings.NewReader(in), &out, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if want := "p_test.go:1: syntax error\nFAIL\tp [build failed]\n"; !res.failed || out.String() != want {
			t.Errorf("failed = %v, out = %q, want %q", res.failed, out.String(), want)
		}
		if want := []string{"p (build failed)"}; !slices.Equal(res.failures, want) {
			t.Errorf("failures = %q, want %q", res.failures, want)
		}
	})
}

// TestAllowed: a test may stay slow only with a reason.
func TestAllowed(t *testing.T) {
	t.Parallel()
	for name, why := range allowed {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is allowed to be slow without a reason", name)
		}
	}
}

// TestFailLimit: on Linux outside CI a test over -max fails; on CI it fails only over twice -max, and on Windows or
// macOS, where a process costs ten times more, over -max-elsewhere (at least twice -max on CI), the others reported.
func TestFailLimit(t *testing.T) {
	t.Parallel()
	const s = time.Second
	for _, c := range []struct {
		goos             string
		ci               bool
		limit, elsewhere time.Duration
		want             time.Duration
	}{
		{"linux", false, 10 * s, 15 * s, 10 * s},
		{"windows", false, 10 * s, 15 * s, 15 * s},
		{"darwin", false, 10 * s, 15 * s, 15 * s},
		{"linux", true, 10 * s, 15 * s, 20 * s},
		{"windows", true, 10 * s, 15 * s, 20 * s},
		{"darwin", true, 10 * s, 15 * s, 20 * s},
		{"windows", true, 10 * s, 30 * s, 30 * s},
		{"windows", false, 3 * s, s, 3 * s}, // a -max-elsewhere under -max: -max
	} {
		ci := map[bool]string{true: "CI", false: "not CI"}[c.ci]
		t.Run(c.goos+" "+ci+" "+c.limit.String()+" "+c.elsewhere.String(), func(t *testing.T) {
			t.Parallel()
			if got := failLimit(c.goos, c.ci, c.limit, c.elsewhere); got != c.want {
				t.Errorf("failLimit = %s, want %s", got, c.want)
			}
		})
	}
}

// TestOver keeps the tests over the limit only.
func TestOver(t *testing.T) {
	t.Parallel()
	tests := []slow{{"p TestHangs", 20 * time.Second}, {"p TestGit", 7 * time.Second}}
	for _, c := range []struct {
		name  string
		limit time.Duration
		want  []string
	}{
		{"one over 15 s", 15 * time.Second, []string{"p TestHangs"}},
		{"both over 2 s", 2 * time.Second, []string{"p TestHangs", "p TestGit"}},
		{"none over 30 s", 30 * time.Second, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, s := range over(tests, c.limit) {
				got = append(got, s.name)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("over = %q, want %q", got, c.want)
			}
		})
	}
}

// TestLoadBaseline reads what saveBaseline writes, durations as text or as seconds, and refuses what is no baseline.
func TestLoadBaseline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		body           string // none: no file
		sum, wallClock time.Duration
		err            string
	}{
		{"as text", `{"sum": "95s", "wall_clock": "45s"}`, 95 * time.Second, 45 * time.Second, ""},
		{"as seconds", `{"sum": 1.5, "wall_clock": 2}`, 1500 * time.Millisecond, 2 * time.Second, ""},
		{"the sum alone", `{"sum": "3s"}`, 3 * time.Second, 0, ""},
		{"no file", "", 0, 0, "no such file"},
		{"not JSON", `{`, 0, 0, "unexpected end"},
		{"a sum of another type", `{"sum": true}`, 0, 0, "invalid sum in"},
		{"a wall clock not a duration", `{"wall_clock": "soon"}`, 0, 0, "invalid wall_clock in"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "baseline.json")
			if c.body != "" {
				if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			base, err := loadBaseline(path)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || base.Sum != c.sum || base.WallClock != c.wallClock {
				t.Errorf("got sum=%v, wc=%v, %v; want %v, %v", base.Sum, base.WallClock, err, c.sum, c.wallClock)
			}
		})
	}
}

// TestSaveBaseline writes the totals rounded to a tenth of a second, as loadBaseline reads them.
func TestSaveBaseline(t *testing.T) {
	t.Parallel()
	t.Run("saved", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "baseline.json")
		if err := saveBaseline(path, 95*time.Second, 45*time.Second); err != nil {
			t.Fatalf("saveBaseline failed: %v", err)
		}
		if b, _ := os.ReadFile(path); string(b) != "{\n  \"sum\": \"95s\",\n  \"wall_clock\": \"45s\"\n}\n" {
			t.Errorf("saved %q", b)
		}
		base, err := loadBaseline(path)
		if err != nil || base.Sum != 95*time.Second || base.WallClock != 45*time.Second {
			t.Errorf("got sum=%v, wc=%v, %v; want 95s, 45s", base.Sum, base.WallClock, err)
		}
	})
	t.Run("a folder missing", func(t *testing.T) {
		t.Parallel()
		if err := saveBaseline(filepath.Join(t.TempDir(), "none", "baseline.json"), time.Second, time.Second); err == nil {
			t.Error("saved into a missing folder")
		}
	})
}

// TestFormatDuration rounds to a tenth of a second, and writes whole seconds without a decimal.
func TestFormatDuration(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{
		0: "0s", 95 * time.Second: "95s", 1240 * time.Millisecond: "1.2s", 1960 * time.Millisecond: "2s",
	} {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			if got := formatDuration(d); got != want {
				t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
			}
		})
	}
}

// TestReportTotal prints the suite total beside the baseline, warns when it grows by more than 25 %, and updates the
// baseline when asked unless a test failed.
func TestReportTotal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		baseline       bool // a baseline of 100 s sum, 40 s wall clock
		res            result
		update         bool
		out, errOut    string
		sum, wallClock time.Duration // the baseline after
	}{
		{"within the baseline", true, result{sum: 110 * time.Second, wallClock: 42 * time.Second}, false,
			"Suite total: 110s sum, 42s wall clock (baseline: 100s sum, 40s wall clock)", "", 100 * time.Second, 40 * time.Second},
		{"over the baseline", true, result{sum: 130 * time.Second, wallClock: 55 * time.Second}, false,
			"Suite total: 130s sum", "WARNING: test suite total time exceeds baseline by more than 25%:\n" +
				"  - sum 130s exceeds baseline 100s by 30.0% (>25% margin)\n" +
				"  - wall clock 55s exceeds baseline 40s by 37.5% (>25% margin)", 100 * time.Second, 40 * time.Second},
		{"updated", true, result{sum: 130 * time.Second, wallClock: 55 * time.Second}, true,
			"Updated baseline at ", "", 130 * time.Second, 55 * time.Second},
		{"not updated when tests failed", true, result{failed: true, sum: 200 * time.Second, wallClock: 90 * time.Second}, true,
			"", "slowtests: tests failed; baseline not updated", 100 * time.Second, 40 * time.Second},
		{"no baseline", false, result{sum: 3 * time.Second, wallClock: time.Second}, false,
			"Suite total: 3s sum, 1s wall clock\n", "", 0, 0},
		{"nothing measured", true, result{}, false, "", "", 100 * time.Second, 40 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "baseline.json")
			if c.baseline {
				if err := saveBaseline(path, 100*time.Second, 40*time.Second); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut strings.Builder
			reportTotal(&out, &errOut, c.res, path, c.update)
			if !strings.Contains(out.String(), c.out) || c.out == "" && out.Len() != 0 {
				t.Errorf("out = %q, want %q", out.String(), c.out)
			}
			if !strings.Contains(errOut.String(), c.errOut) || c.errOut == "" && errOut.Len() != 0 {
				t.Errorf("errOut = %q, want %q", errOut.String(), c.errOut)
			}
			base, _ := loadBaseline(path)
			if base.Sum != c.sum || base.WallClock != c.wallClock {
				t.Errorf("baseline after: sum=%v, wc=%v; want %v, %v", base.Sum, base.WallClock, c.sum, c.wallClock)
			}
		})
	}
	t.Run("a baseline it cannot write", func(t *testing.T) {
		t.Parallel()
		var out, errOut strings.Builder
		reportTotal(&out, &errOut, result{sum: time.Second}, filepath.Join(t.TempDir(), "none", "b.json"), true)
		if !strings.Contains(errOut.String(), "slowtests: failed to update baseline") {
			t.Errorf("errOut = %q", errOut.String())
		}
	})
}

// TestFindBaseline takes the path given, else the first baseline found from the repository or from this folder.
func TestFindBaseline(t *testing.T) {
	t.Run("the path given", func(t *testing.T) {
		if got := findBaseline("custom.json"); got != "custom.json" {
			t.Errorf("findBaseline(custom) = %s, want custom.json", got)
		}
	})
	t.Run("from this folder", func(t *testing.T) {
		if got := findBaseline(""); got != "baseline.json" {
			t.Errorf("findBaseline() = %s, want baseline.json", got)
		}
	})
	t.Run("from the repository", func(t *testing.T) {
		t.Chdir(filepath.Join("..", ".."))
		if got := findBaseline(""); got != "tools/slowtests/baseline.json" {
			t.Errorf("findBaseline() = %s", got)
		}
	})
	t.Run("none found", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if got := findBaseline(""); got != "tools/slowtests/baseline.json" {
			t.Errorf("findBaseline() = %s", got)
		}
	})
}
