// Command slowtests reads `go test -json` on its input, prints what go test prints without -json (the output of the
// tests that fail, the build errors, a line per package), and fails when a test fails or when a top-level test took
// longer than -max: tests stay fast, and a slow one is caught the day it slows down.
//
// Linux is the reference: there -max fails. Elsewhere a process costs ten times more (a test spawning git takes
// 0.7 s on Linux and 7 s on the Windows runner, the same test), so a test over -max is reported and fails only
// over -max-elsewhere: a test that hangs or grows tenfold is still caught there.
//
//	go test -json ./... | go run ./tools/slowtests
//
// A test that must stay slow goes in allowed, with why: the list stays empty unless a test has a reason.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	// defaultMax is the longest a top-level test may take on Linux before failing.
	defaultMax = 10 * time.Second

	// warnMargin is the threshold (25%) beyond baseline totals before warning.
	warnMargin = 0.25
)

// allowed are the tests that may take longer than -max, by package and name ("github.com/…/cmd/djinn TestX"), each
// with why it cannot be fast. Keep it empty.
var allowed = map[string]string{
	// Real djinn up processes: each test starts one or two full djinn up servers and CLI commands.
	"github.com/empowill/djinn/cmd/djinn TestUpdateFromRelease":                                     "two real djinn up processes, one restarting into the other",
	"github.com/empowill/djinn/cmd/djinn TestCrashReopensTheLeads":                                  "two real djinn up processes across a kill",
	"github.com/empowill/djinn/cmd/djinn TestUpdate":                                                "two real djinn up processes across a restart",
	"github.com/empowill/djinn/cmd/djinn TestWishResume":                                            "a real djinn up process and a djinn CLI invocation",
	"github.com/empowill/djinn/cmd/djinn TestCrashResumesTheWorkers":                                "two real djinn up processes across a kill",
	"github.com/empowill/djinn/cmd/djinn TestWindowTerminalOpensInAProject":                         "a real djinn up process and a djinn CLI invocation",
	"github.com/empowill/djinn/cmd/djinn TestCrossVet":                                              "runs go vet for windows and darwin targets across all dependencies",
	"github.com/empowill/djinn/internal/testx/bigwish TestSizes":                                    "generates a 10x wish with thousands of tasks and events",
	"github.com/empowill/djinn/internal/harness TestRestartResumesInOrder":                          "starts 7 sequential workers across a simulated restart",
	"github.com/empowill/djinn/internal/harness TestEnvFailureReplayOnVersionChange":                "starts 5 sequential harness instances across simulated version upgrades",
	"github.com/empowill/djinn/internal/harness TestSummonAtLaunch":                                 "creates two git repositories and spawns tasks for multiple providers",
	"github.com/empowill/djinn/internal/harness TestAzimaPRGlabRetargetAndReady":                    "runs multiple git passes and glab integrations against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaPRRetargetAfterMerge":                      "runs multiple git passes and PR retargeting against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaPRStackedProposal":                         "runs multiple git passes and stacked PR proposals against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaFollowMainMergePushed":                     "runs multiple git passes and merge operations against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaFollowMainRebaseUnpushed":                  "runs multiple git passes and rebases against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaFollowMainRebaseUnpushedWithRemoteAskMode": "runs multiple git passes and rebases against real git repos",
	"github.com/empowill/djinn/internal/harness TestAzimaDependentBranchStacked":                    "runs multiple git passes and branch stacking against real git repos",
}

// event is a line of go test -json (go doc test2json).
type event struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	ImportPath  string // build-output
	FailedBuild string
}

// slow is a test over the limit.
type slow struct {
	name    string
	elapsed time.Duration
}

// result is what check found.
type result struct {
	failed    bool          // a test, a package or a build failed
	slow      []slow        // the tests over the limit and not allowed, the slowest first
	sum       time.Duration // sum of all top-level test durations
	wallClock time.Duration // wall clock duration of the test run
}

type baselineData struct {
	Sum       time.Duration
	WallClock time.Duration
}

func parseDurationValue(v any) (time.Duration, error) {
	switch val := v.(type) {
	case string:
		return time.ParseDuration(val)
	case float64:
		return time.Duration(val * float64(time.Second)), nil
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}

func findBaseline(custom string) string {
	if custom != "" {
		return custom
	}
	candidates := []string{
		"tools/slowtests/baseline.json",
		"baseline.json",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "tools/slowtests/baseline.json"
}

func loadBaseline(path string) (baselineData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baselineData{}, err
	}
	var raw struct {
		Sum       any `json:"sum"`
		WallClock any `json:"wall_clock"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return baselineData{}, err
	}
	var base baselineData
	if raw.Sum != nil {
		d, err := parseDurationValue(raw.Sum)
		if err != nil {
			return baselineData{}, fmt.Errorf("invalid sum in %s: %w", path, err)
		}
		base.Sum = d
	}
	if raw.WallClock != nil {
		d, err := parseDurationValue(raw.WallClock)
		if err != nil {
			return baselineData{}, fmt.Errorf("invalid wall_clock in %s: %w", path, err)
		}
		base.WallClock = d
	}
	return base, nil
}

func saveBaseline(path string, sum, wallClock time.Duration) error {
	raw := struct {
		Sum       string `json:"sum"`
		WallClock string `json:"wall_clock"`
	}{
		Sum:       formatDuration(sum),
		WallClock: formatDuration(wallClock),
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func formatDuration(d time.Duration) string {
	d = d.Round(100 * time.Millisecond)
	sec := d.Seconds()
	if sec == float64(int(sec)) {
		return fmt.Sprintf("%ds", int(sec))
	}
	return fmt.Sprintf("%.1fs", sec)
}

func reportTotal(out, errOut io.Writer, res result, baselinePath string, updateBaseline bool) {
	if res.sum == 0 && res.wallClock == 0 {
		return
	}
	if updateBaseline {
		if res.failed {
			fmt.Fprintln(errOut, "slowtests: tests failed; baseline not updated")
			return
		}
		if err := saveBaseline(baselinePath, res.sum, res.wallClock); err != nil {
			fmt.Fprintf(errOut, "slowtests: failed to update baseline: %v\n", err)
		} else {
			fmt.Fprintf(out, "Updated baseline at %s: sum %s, wall clock %s\n",
				baselinePath, formatDuration(res.sum), formatDuration(res.wallClock))
		}
		return
	}

	base, err := loadBaseline(baselinePath)
	if err != nil || (base.Sum == 0 && base.WallClock == 0) {
		fmt.Fprintf(out, "Suite total: %s sum, %s wall clock\n",
			formatDuration(res.sum), formatDuration(res.wallClock))
		return
	}

	fmt.Fprintf(out, "Suite total: %s sum, %s wall clock (baseline: %s sum, %s wall clock)\n",
		formatDuration(res.sum), formatDuration(res.wallClock),
		formatDuration(base.Sum), formatDuration(base.WallClock))

	var warnings []string
	if base.Sum > 0 && res.sum > time.Duration(float64(base.Sum)*(1.0+warnMargin)) {
		pct := (float64(res.sum-base.Sum) / float64(base.Sum)) * 100
		warnings = append(warnings, fmt.Sprintf("sum %s exceeds baseline %s by %.1f%% (>%.0f%% margin)",
			formatDuration(res.sum), formatDuration(base.Sum), pct, warnMargin*100))
	}
	if base.WallClock > 0 && res.wallClock > time.Duration(float64(base.WallClock)*(1.0+warnMargin)) {
		pct := (float64(res.wallClock-base.WallClock) / float64(base.WallClock)) * 100
		warnings = append(warnings, fmt.Sprintf("wall clock %s exceeds baseline %s by %.1f%% (>%.0f%% margin)",
			formatDuration(res.wallClock), formatDuration(base.WallClock), pct, warnMargin*100))
	}
	if len(warnings) > 0 {
		fmt.Fprintf(errOut, "\nWARNING: test suite total time exceeds baseline by more than %.0f%%:\n", warnMargin*100)
		for _, w := range warnings {
			fmt.Fprintf(errOut, "  - %s\n", w)
		}
		fmt.Fprintf(errOut, "Update baseline with go run ./tools/slowtests -update-baseline or edit %s.\n\n", baselinePath)
	}
}

func main() {
	limit := flag.Duration("max", defaultMax, "the longest a top-level test may take, on Linux")
	elsewhere := flag.Duration("max-elsewhere", 15*time.Second,
		"the longest a top-level test may take on another system, where one over -max is only reported")
	baselineFlag := flag.String("baseline", "", "path to baseline.json (defaults to tools/slowtests/baseline.json)")
	updateBaseline := flag.Bool("update-baseline", false, "update baseline.json with measured totals")
	flag.Parse()

	res, err := check(os.Stdin, os.Stdout, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "slowtests:", err)
		os.Exit(1)
	}
	baselinePath := findBaseline(*baselineFlag)
	reportTotal(os.Stdout, os.Stderr, res, baselinePath, *updateBaseline)

	fail := failLimit(runtime.GOOS, *limit, *elsewhere)
	if len(res.slow) > 0 {
		fmt.Fprintf(os.Stderr, "\nThese tests take longer than %s: make them fast (a fake clock, a short tick, a wait on an event "+
			"instead of a sleep), or say why in tools/slowtests/main.go (allowed).\n", *limit)
		if fail != *limit {
			fmt.Fprintf(os.Stderr, "On %s they are reported; only one over %s fails (Linux is the reference).\n",
				runtime.GOOS, fail)
		}
		for _, s := range res.slow {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", s.elapsed.Round(10*time.Millisecond), s.name)
		}
	}
	if res.failed || len(over(res.slow, fail)) > 0 {
		os.Exit(1)
	}
}

// failLimit is how long a test may take before it fails on goos: limit on Linux, elsewhere on the others.
func failLimit(goos string, limit, elsewhere time.Duration) time.Duration {
	if goos == "linux" {
		return limit
	}
	return max(limit, elsewhere)
}

// over are the tests of tests over limit.
func over(tests []slow, limit time.Duration) []slow {
	var out []slow
	for _, s := range tests {
		if s.elapsed > limit {
			out = append(out, s)
		}
	}
	return out
}

// check reads the events of in, writes to out what go test prints without -json, and says what failed and which
// tests were too slow. A line that is not an event (go test's own error) is printed and fails.
func check(in io.Reader, out io.Writer, limit time.Duration) (result, error) {
	return checkClock(in, out, limit, time.Now)
}

func checkClock(in io.Reader, out io.Writer, limit time.Duration, now func() time.Time) (result, error) {
	var res result
	start := now()
	held := map[string][]string{} // the output of each running test, printed only if it fails
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var e event
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' || json.Unmarshal(line, &e) != nil {
			fmt.Fprintln(out, string(line))
			res.failed = true
			continue
		}
		key := e.Package + " " + e.Test
		switch {
		case e.Action == "build-output":
			fmt.Fprint(out, e.Output)
		case e.Action == "build-fail":
			res.failed = true
		case e.Test == "":
			if e.Action == "output" && e.Output != "PASS\n" { // go test says it only with -v
				fmt.Fprint(out, e.Output)
			}
			if e.Action == "fail" {
				res.failed = true
			}
		case e.Action == "output":
			held[key] = append(held[key], e.Output)
		case e.Action == "pass" || e.Action == "fail" || e.Action == "skip":
			if e.Action == "fail" {
				res.failed = true
				fmt.Fprint(out, strings.Join(held[key], ""))
			}
			delete(held, key)
			elapsed := time.Duration(e.Elapsed * float64(time.Second))
			if !strings.Contains(e.Test, "/") {
				res.sum += elapsed
			}
			if strings.Contains(e.Test, "/") || elapsed <= limit {
				continue
			}
			if _, ok := allowed[key]; !ok {
				res.slow = append(res.slow, slow{key, elapsed})
			}
		}
	}
	res.wallClock = now().Sub(start)
	slices.SortFunc(res.slow, func(a, b slow) int { return int(b.elapsed - a.elapsed) })
	return res, sc.Err()
}
