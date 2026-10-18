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

	"github.com/empowill/djinn/internal/testx"
)

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

// TestReadConfig reads the bench from the environment: haiku, 3 rounds and no consent by default; a cap that is not
// a positive amount or a count of rounds out of 1 to 20 is refused.
func TestReadConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		vars map[string]string
		want config
		err  string
	}{
		{"the defaults", map[string]string{}, config{model: "haiku", runs: 3}, ""},
		{"all set", map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "2.5", "BENCH_MODEL": "opus", "BENCH_RUNS": "20", "BENCH_OUT": "b.md"},
			config{paid: true, maxUSD: 2.5, model: "opus", runs: 20, out: "b.md"}, ""},
		{"a negative cap", map[string]string{"BENCH_MAX_USD": "-1"}, config{}, "BENCH_MAX_USD"},
		{"a cap not a number", map[string]string{"BENCH_MAX_USD": "lots"}, config{}, "BENCH_MAX_USD"},
		{"no round", map[string]string{"BENCH_RUNS": "0"}, config{}, "BENCH_RUNS"},
		{"too many rounds", map[string]string{"BENCH_RUNS": "21"}, config{}, "BENCH_RUNS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readConfig(env(c.vars))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("readConfig = %+v, %v; want an error on %s", cfg, err, c.err)
				}
				return
			}
			if err != nil || cfg != c.want {
				t.Errorf("readConfig = %+v, %v; want %+v", cfg, err, c.want)
			}
		})
	}
}

// TestQuote: one run per variant and round, a fork reading its parent again, and the parent; low and high frame it.
func TestQuote(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		cfg      config
		min, max float64
		basis    string
	}{
		// Haiku, 3 rounds: 3 x 4.5 runs + the parent = 14.5 runs of $0.006.
		{"haiku", config{model: "haiku", runs: 3}, 0.086, 0.088, "Haiku"},
		{"opus", config{model: "claude-opus-5-5", runs: 3}, 2.60, 2.62, ""},
		{"a model not observed is quoted as the dearest", config{model: "sonnet", runs: 3}, 2.60, 2.62, "no run of sonnet observed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			mid, low, high, basis := quote(c.cfg)
			if mid < c.min || mid > c.max || low != mid/2 || high != mid*2 || !strings.Contains(basis, c.basis) {
				t.Errorf("quote = %.4f (%.4f to %.4f), %s", mid, low, high, basis)
			}
		})
	}
}

// TestMain plays claude when BENCH_FAKE_CLAUDE is set: for each message on its input, an init line, the answer and
// a result with its usage, as claude -p --output-format stream-json writes them. No model is called.
func TestMain(m *testing.M) {
	if os.Getenv("BENCH_FAKE_CLAUDE") != "" {
		fakeClaude()
		return
	}
	cleanup := testx.Guard()
	code := m.Run()
	cleanup()
	os.Exit(code)
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

// TestRun prints its quote, then refuses without BENCH_PAID=yes and a cap that covers the quote, before anything is
// started; with them, it runs the whole bench against the fake claude: every variant runs, is measured, and lands in
// the table.
func TestRun(t *testing.T) {
	refusals := []struct {
		name string
		vars map[string]string
		says string
	}{
		{"no consent", map[string]string{}, "BENCH_PAID=yes"},
		{"consent without cap", map[string]string{"BENCH_PAID": "yes"}, "BENCH_MAX_USD is required"},
		{"a cap below quote", map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "0.01"}, "below the quote"},
		{"a yes that is not", map[string]string{"BENCH_PAID": "true", "BENCH_MAX_USD": "100"}, "BENCH_PAID=yes"},
		{"opus costs more", map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "1", "BENCH_MODEL": "opus"}, "below the quote"},
		{"an unknown model too", map[string]string{"BENCH_PAID": "yes", "BENCH_MAX_USD": "1", "BENCH_MODEL": "sonnet"}, "below the quote"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := readConfig(env(c.vars))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = run(t.Context(), cfg, &out)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("run = %v, want a refusal saying %q", err, c.says)
			}
			if !strings.Contains(out.String(), "Quote: about $") {
				t.Errorf("no quote before the refusal: %q", out.String())
			}
		})
	}
	// Not parallel: the fake claude comes first on the PATH. About 0.5 s on a calm machine (git worktrees and nine runs
	// of the fake claude, each a start of the test binary); 15 s under -race, where each start is slow.
	t.Run("the bench runs", func(t *testing.T) {
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
		err := run(t.Context(), config{paid: true, maxUSD: 1, model: "haiku", runs: 2, out: out}, &buf)
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
		if !strings.Contains(string(table), "$0.0090 spent") || !strings.Contains(string(table), "| 900 ms | 20000 | 500 |") {
			t.Errorf("table:\n%s", table)
		}
	})
	t.Run("no claude on the path", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		var out bytes.Buffer
		if err := run(t.Context(), config{paid: true, maxUSD: 1, model: "haiku", runs: 1}, &out); err == nil ||
			!strings.Contains(err.Error(), "claude is not on the PATH") {
			t.Fatalf("run = %v", err)
		}
	})
}
