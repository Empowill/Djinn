// Command benchworkers compares five ways Djinn starts a worker on the same small task, with a real model: cold
// (claude -p as Djinn runs it), --bare, a fork of a session, a warm worker, and a cold worker behind a stable brief.
// It measures the time to the first output, the tokens with the cache, and the cost. It is a PAID run: it prints
// its quote, and refuses to run without BENCH_PAID=yes and a cap, BENCH_MAX_USD.
//
//	go tool task bench-workers                                   # the quote, and how to run it
//	BENCH_PAID=yes BENCH_MAX_USD=0.50 go tool task bench-workers  # the run
//
// BENCH_MODEL (haiku by default), BENCH_RUNS (3 rounds by default), BENCH_WARM_WAIT (5s: how long a warm worker
// loads before its message), BENCH_OUT (a file for the Markdown table, besides the output).
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/plan"
)

// The variants, in the order of each round.
var variants = []string{"cold", "bare", "fork", "warm", "brief"}

// The task every variant runs: one file to read, one word to say.
const (
	readme       = "# Bench\n\nA small project for Djinn's workers' bench. The answer is 42.\n"
	agents       = "# AGENTS.md\n\nAnswer in one word. Never edit a file.\n"
	prompt       = "Read README.md and reply with the answer it gives, as a number, nothing else."
	parentPrompt = "Read AGENTS.md and README.md, then reply: ready."
)

// observed are costs seen on real cold runs (docs/providers.md, the claude fixtures): the cost of one small run, by
// model. A model not listed is quoted as the dearest.
var observed = []struct {
	model string
	usd   float64
	from  string
}{
	{"haiku", 0.006, "a 2-turn Haiku 5.5 run, 27k tokens written to the cache: $0.0059"},
	{"opus", 0.18, "a first Opus 5.5 call, 22k tokens written to the cache: $0.18"},
}

type config struct {
	paid     bool
	maxUSD   float64
	model    string
	runs     int
	warmWait time.Duration
	out      string
}

func main() {
	cfg, err := readConfig(os.Getenv)
	if err == nil {
		err = run(context.Background(), cfg, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench-workers:", err)
		os.Exit(2)
	}
}

func readConfig(env func(string) string) (config, error) {
	cfg := config{
		paid: env("BENCH_PAID") == "yes", model: cmp.Or(env("BENCH_MODEL"), "haiku"), runs: 3, warmWait: 5 * time.Second,
		out: env("BENCH_OUT"),
	}
	var err error
	if v := env("BENCH_MAX_USD"); v != "" {
		if cfg.maxUSD, err = strconv.ParseFloat(v, 64); err != nil || cfg.maxUSD <= 0 {
			return cfg, fmt.Errorf("BENCH_MAX_USD=%q: a positive amount in US dollars", v)
		}
	}
	if v := env("BENCH_RUNS"); v != "" {
		if cfg.runs, err = strconv.Atoi(v); err != nil || cfg.runs < 1 || cfg.runs > 20 {
			return cfg, fmt.Errorf("BENCH_RUNS=%q: 1 to 20 rounds", v)
		}
	}
	if v := env("BENCH_WARM_WAIT"); v != "" {
		if cfg.warmWait, err = time.ParseDuration(v); err != nil {
			return cfg, fmt.Errorf("BENCH_WARM_WAIT=%q: %w", v, err)
		}
	}
	return cfg, nil
}

// quote is what the bench should cost: one run per variant and round, a fork reading its parent's context again
// (half a run more), and the parent's own run. low and high frame it: the cache makes the later runs cheaper, a
// longer answer dearer.
func quote(cfg config) (mid, low, high float64, basis string) {
	per, basis := observed[len(observed)-1].usd, "no run of "+cfg.model+" observed: quoted as "+observed[len(observed)-1].model
	for _, o := range observed {
		if strings.Contains(strings.ToLower(cfg.model), o.model) {
			per, basis = o.usd, o.from
			break
		}
	}
	runs := float64(cfg.runs)*(float64(len(variants))+0.5) + 1
	mid = per * runs
	return mid, mid / 2, mid * 2, basis
}

// run prints the quote, refuses without the developer's yes and a cap that covers it, then runs the bench.
func run(ctx context.Context, cfg config, out io.Writer) error {
	mid, low, high, basis := quote(cfg)
	fmt.Fprintf(out, "Bench of the workers: %d variants (%s) x %d rounds, model %s, plus one parent session to fork.\n",
		len(variants), strings.Join(variants, ", "), cfg.runs, cfg.model)
	fmt.Fprintf(out, "Quote: about $%.2f (between $%.2f and $%.2f), from %s.\n", mid, low, high, basis)
	switch {
	case !cfg.paid:
		return errors.New("a paid run: set BENCH_PAID=yes and a cap, BENCH_MAX_USD, to run it")
	case cfg.maxUSD == 0:
		return errors.New("BENCH_MAX_USD is required: the bench stops once it has spent it")
	case cfg.maxUSD < mid:
		return fmt.Errorf("BENCH_MAX_USD=%.2f is below the quote: raise it to %.2f at least, or lower BENCH_RUNS", cfg.maxUSD, mid)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("claude is not on the PATH")
	}
	b, err := newBench(ctx, cfg, out)
	if err != nil {
		return err
	}
	defer b.close()
	return b.run(ctx)
}

// result is one run of one variant.
type result struct {
	variant  string
	round    int
	first    time.Duration // from asking to the first output of the agent
	total    time.Duration // from asking to the result
	ttftMS   float64       // claude's own ttft_ms, from its result line
	usage    *planv1.Usage
	rssMB    float64 // a warm worker's memory while it waited
	err      string
	asked    time.Time
	gotFirst bool
}

type bench struct {
	cfg     config
	out     io.Writer
	repo    string // a Git repository with README.md and AGENTS.md
	rules   string // the stable brief, for the brief variant
	parent  string // the session the forks start from
	spent   float64
	results []result
}

func newBench(ctx context.Context, cfg config, out io.Writer) (*bench, error) {
	dir, err := os.MkdirTemp("", "djinn-bench-")
	if err != nil {
		return nil, err
	}
	b := &bench{cfg: cfg, out: out, repo: filepath.Join(dir, "bench")}
	if err := os.MkdirAll(b.repo, 0o700); err != nil {
		return nil, err
	}
	for name, text := range map[string]string{"README.md": readme, "AGENTS.md": agents} {
		if err := os.WriteFile(filepath.Join(b.repo, name), []byte(text), 0o600); err != nil {
			return nil, err
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Bench"}} {
		if err := git(ctx, b.repo, args...); err != nil {
			return nil, err
		}
	}
	b.rules = filepath.Join(dir, "lead-rules.md")
	stable := plan.StableBrief("", []*planv1.Project{{Name: "bench", Directory: b.repo, Git: true}})
	if err := os.WriteFile(b.rules, []byte(stable), 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *bench) close() { _ = os.RemoveAll(filepath.Dir(b.repo)) }

func git(ctx context.Context, dir string, args ...string) error {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, out)
	}
	return nil
}

// worktree is a new worktree of the repository, as Djinn gives each task.
func (b *bench) worktree(ctx context.Context, id string) (string, error) {
	path := filepath.Join(filepath.Dir(b.repo), "worktrees", id)
	return path, git(ctx, b.repo, "worktree", "add", "-q", "-b", "bench-"+id[len(id)-8:], path, "HEAD")
}

func (b *bench) run(ctx context.Context) error {
	// The parent of the forks: a session that has read the project, in the repository's own folder. The forks run
	// in other folders, as a forked worker runs in its own worktree.
	p := b.once(ctx, "parent", 0, harness.Claude{}, harness.Spec{Dir: b.repo, Prompt: parentPrompt}, false)
	b.parent = p.id
	if p.res.err != "" {
		fmt.Fprintf(b.out, "The parent session failed (%s): the forks will fail too.\n", p.res.err)
	}
	for round := 1; round <= b.cfg.runs; round++ {
		for _, v := range variants {
			if b.spent >= b.cfg.maxUSD {
				fmt.Fprintf(b.out, "Stopped: $%.4f spent, the cap is $%.2f.\n", b.spent, b.cfg.maxUSD)
				return b.report()
			}
			var c harness.Claude
			spec := harness.Spec{Prompt: prompt}
			warm := false
			switch v {
			case "bare":
				c.Extra = []string{"--bare"}
			case "fork":
				spec.Resume, spec.Fork = b.parent, true
			case "warm":
				warm = true
			case "brief":
				c.Extra = []string{"--append-system-prompt-file", b.rules, "--exclude-dynamic-system-prompt-sections"}
			}
			b.once(ctx, v, round, c, spec, warm)
		}
	}
	return b.report()
}

type ran struct {
	id  string
	res result
}

// once runs the task once, in a new worktree unless spec has a folder, and records what it measured.
func (b *bench) once(ctx context.Context, variant string, round int, c harness.Claude, spec harness.Spec, warm bool) ran {
	id := uuid.Must(uuid.NewV7()).String()
	r := result{variant: variant, round: round}
	defer func() {
		if round > 0 {
			b.results = append(b.results, r)
			fmt.Fprintln(b.out, r.line())
		}
	}()
	spec.TaskID, spec.Model = id, b.cfg.model
	spec.MaxBudgetUSD = b.cfg.maxUSD - b.spent
	if spec.Dir == "" {
		dir, err := b.worktree(ctx, id)
		if err != nil {
			r.err = err.Error()
			return ran{id, r}
		}
		spec.Dir = dir
	}
	var w harness.Worker
	var err error
	if warm {
		if w, err = c.Warm(ctx, spec); err == nil {
			time.Sleep(b.cfg.warmWait)
			r.rssMB = rss(w)
			r.asked = time.Now()
			err = w.Send(spec.Prompt)
		}
	} else {
		r.asked = time.Now()
		w, err = c.Start(ctx, spec)
	}
	if err != nil {
		r.err = err.Error()
		return ran{id, r}
	}
	for ev := range w.Events() {
		switch ev.Kind {
		case planv1.TaskEventKind_TASK_EVENT_KIND_TEXT, planv1.TaskEventKind_TASK_EVENT_KIND_TOOL_CALL:
			if !r.gotFirst {
				r.first, r.gotFirst = time.Since(r.asked), true
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_USAGE:
			r.total, r.usage = time.Since(r.asked), ev.Usage
			var line struct {
				TTFT float64 `json:"ttft_ms"`
			}
			if json.Unmarshal([]byte(ev.Raw), &line) == nil {
				r.ttftMS = line.TTFT
			}
		case planv1.TaskEventKind_TASK_EVENT_KIND_ERROR:
			r.err = ev.Text
		}
	}
	if res := w.Wait(); res.Err != nil && r.err == "" {
		r.err = res.Err.Error()
	}
	b.spent += r.usage.GetCostUsd()
	return ran{id, r}
}

// rss is the memory of a worker's process in MB, read with ps on macOS and Linux; 0 elsewhere or when unknown.
func rss(w harness.Worker) float64 {
	p, ok := w.(interface{ PID() int })
	if !ok || runtime.GOOS == "windows" {
		return 0
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(p.PID())).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return kb / 1024
}

func (r result) line() string {
	if r.err != "" {
		return fmt.Sprintf("%-5s #%d  failed: %s", r.variant, r.round, r.err)
	}
	u := r.usage
	return fmt.Sprintf("%-5s #%d  first %5dms  result %5dms  ttft %5.0fms  in %d  cache read %d  cache write %d  out %d  $%.4f",
		r.variant, r.round, r.first.Milliseconds(), r.total.Milliseconds(), r.ttftMS, u.GetInputTokens(),
		u.GetCacheReadTokens(), u.GetCacheWriteTokens(), u.GetOutputTokens(), u.GetCostUsd())
}

// report prints the medians by variant as a Markdown table, and writes it to BENCH_OUT when set.
func (b *bench) report() error {
	var t strings.Builder
	fmt.Fprintf(&t, "\n%s, model %s, %d rounds, $%.4f spent. Medians:\n\n", time.Now().UTC().Format("2006-01-02"), b.cfg.model, b.cfg.runs, b.spent)
	t.WriteString("| Variant | Runs ok | First output | Result | Claude's ttft | Cache read | Cache write | Input | Output | Cost | Warm RSS |\n")
	t.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, v := range variants {
		var ok []result
		for _, r := range b.results {
			if r.variant == v && r.err == "" {
				ok = append(ok, r)
			}
		}
		med := func(f func(result) float64) float64 {
			if len(ok) == 0 {
				return 0
			}
			xs := make([]float64, len(ok))
			for i, r := range ok {
				xs[i] = f(r)
			}
			slices.Sort(xs)
			return xs[len(xs)/2]
		}
		rssText := "-"
		if v == "warm" {
			rssText = fmt.Sprintf("%.0f MB", med(func(r result) float64 { return r.rssMB }))
		}
		fmt.Fprintf(&t, "| %s | %d of %d | %.0f ms | %.0f ms | %.0f ms | %.0f | %.0f | %.0f | %.0f | $%.4f | %s |\n", v, len(ok), b.cfg.runs,
			med(func(r result) float64 { return float64(r.first.Milliseconds()) }),
			med(func(r result) float64 { return float64(r.total.Milliseconds()) }),
			med(func(r result) float64 { return r.ttftMS }),
			med(func(r result) float64 { return float64(r.usage.GetCacheReadTokens()) }),
			med(func(r result) float64 { return float64(r.usage.GetCacheWriteTokens()) }),
			med(func(r result) float64 { return float64(r.usage.GetInputTokens()) }),
			med(func(r result) float64 { return float64(r.usage.GetOutputTokens()) }),
			med(func(r result) float64 { return r.usage.GetCostUsd() }), rssText)
	}
	fmt.Fprint(b.out, t.String())
	if b.cfg.out != "" {
		return os.WriteFile(b.cfg.out, []byte(t.String()), 0o600)
	}
	return nil
}
