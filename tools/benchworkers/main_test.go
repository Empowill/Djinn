package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

// TestRefusesWithoutConsent: the bench prints its quote and runs nothing without BENCH_PAID=yes and a cap that
// covers the quote. A refusal comes before anything is started.
func TestRefusesWithoutConsent(t *testing.T) {
	for name, tc := range map[string]struct {
		vars map[string]string
		says string
	}{
		"no consent":           {map[string]string{}, "BENCH_PAID=yes"},
		"consent without cap":  {map[string]string{"BENCH_PAID": "yes"}, "BENCH_MAX_USD is required"},
		"a cap below quote":    {map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "0.01"}, "below the quote"},
		"a yes that is not":    {map[string]string{"BENCH_PAID": "true", "BENCH_MAX_USD": "100"}, "BENCH_PAID=yes"},
		"opus costs more":      {map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "1", "BENCH_MODEL": "opus"}, "below the quote"},
		"an unknown model too": {map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "1", "BENCH_MODEL": "sonnet"}, "below the quote"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := readConfig(env(tc.vars))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = run(t.Context(), cfg, &out)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("run = %v, want a refusal saying %q", err, tc.says)
			}
			if !strings.Contains(out.String(), "Quote: about $") {
				t.Errorf("no quote before the refusal: %q", out.String())
			}
		})
	}
	for _, bad := range []map[string]string{{"BENCH_MAX_USD": "-1"}, {"BENCH_MAX_USD": "lots"}, {"BENCH_RUNS": "0"}, {"BENCH_WARM_WAIT": "soon"}} {
		if _, err := readConfig(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestQuote(t *testing.T) {
	// Haiku, 3 rounds: 3 x 5.5 runs + the parent = 17.5 runs of $0.006.
	mid, low, high, basis := quote(config{model: "haiku", runs: 3})
	if mid < 0.104 || mid > 0.106 || low != mid/2 || high != mid*2 || !strings.Contains(basis, "Haiku") {
		t.Errorf("quote = %.4f (%.4f to %.4f), %s", mid, low, high, basis)
	}
	if mid, _, _, _ := quote(config{model: "claude-opus-5-5", runs: 3}); mid < 3.14 || mid > 3.16 {
		t.Errorf("opus quote = %.4f", mid)
	}
}

// TestMain plays claude when BENCH_FAKE_CLAUDE is set: for each message on its input, an init line, the answer and
// a result with its usage, as claude -p --output-format stream-json writes them. No model is called.
func TestMain(m *testing.M) {
	if os.Getenv("BENCH_FAKE_CLAUDE") != "" {
		fakeClaude()
		return
	}
	os.Exit(m.Run())
}

func fakeClaude() {
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		fmt.Println(`{"type":"system","subtype":"init","session_id":"s1","model":"claude-haiku-5-5"}`)
		fmt.Println(`{"type":"assistant","message":{"content":[{"type":"text","text":"42"}]}}`)
		fmt.Println(`{"type":"result","subtype":"success","session_id":"s1","num_turns":1,"total_cost_usd":0.001,"ttft_ms":900,` +
			`"modelUsage":{"claude-haiku-5-5":{"inputTokens":3,"outputTokens":2,"cacheReadInputTokens":20000,"cacheCreationInputTokens":500}}}`)
	}
}

// TestBenchRuns runs the whole bench against the fake claude: every variant runs, is measured, and lands in the
// table.
func TestBenchRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a symbolic link to the test binary")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	bin := t.TempDir()
	if err := os.Symlink(os.Args[0], filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BENCH_FAKE_CLAUDE", "1")
	out := filepath.Join(t.TempDir(), "bench.md")
	var buf bytes.Buffer
	err := run(t.Context(), config{paid: true, maxUSD: 1, model: "haiku", runs: 2, warmWait: 10 * time.Millisecond, out: out}, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	table, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range variants {
		if !strings.Contains(string(table), "| "+v+" | 2 of 2 | ") {
			t.Errorf("%s did not run twice:\n%s\n%s", v, buf.String(), table)
		}
	}
	if !strings.Contains(string(table), "$0.0110 spent") || !strings.Contains(string(table), "| 900 ms | 20000 | 500 |") {
		t.Errorf("table:\n%s", table)
	}
}
