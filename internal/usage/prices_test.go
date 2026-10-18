package usage

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// customYAML is an override table with one model the embedded table lacks.
const customYAML = `date: "2030-01-01"
currency: "USD"
models:
  custom-future-model:
    input: 1.0
    output: 2.0
    cache_read: 0.1
    cache_write: 0.5
    thinking: 2.0
    source: "https://example.com/pricing"
    date: "2030-01-01"
`

// TestDefaultTable: the embedded table parses cleanly, has a valid date and currency, and every model entry defines
// positive prices, a non-empty public source URL, and a date; a second call returns the same table.
func TestDefaultTable(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatalf("DefaultTable() error = %v", err)
	}
	if again, _ := DefaultTable(); again != tbl {
		t.Error("DefaultTable() returned a new table on its second call")
	}
	if tbl.Date == "" {
		t.Error("table has empty date")
	}
	if tbl.Currency != "USD" {
		t.Errorf("table currency = %q, want USD", tbl.Currency)
	}
	if len(tbl.Models) == 0 {
		t.Fatal("table has no models")
	}

	for name, model := range tbl.Models {
		if model.Source == "" {
			t.Errorf("model %q has no source URL", name)
		}
		if model.Date == "" {
			t.Errorf("model %q has no date", name)
		}
		if model.Input <= 0 || model.Output <= 0 {
			t.Errorf("model %q has non-positive input (%f) or output (%f) price", name, model.Input, model.Output)
		}
	}
}

// TestLoadTable: an override file is loaded when it exists; a missing or empty path falls back to the embedded
// table; "~/" names the home; an unreadable path or a malformed file is an error. Not parallel: the home case sets
// HOME.
func TestLoadTable(t *testing.T) {
	testx.Portable(t)

	dir := t.TempDir()
	custom := filepath.Join(dir, "custom_prices.yaml")
	if err := os.WriteFile(custom, []byte(customYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("models: [not, a, map"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "prices.yaml"), []byte(customYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		path    []string
		home    bool
		custom  bool // the override table, else the embedded one
		wantErr bool
	}{
		{name: "override", path: []string{custom}, custom: true},
		{name: "no path"},
		{name: "empty path", path: []string{""}},
		{name: "missing file", path: []string{filepath.Join(dir, "none.yaml")}},
		{name: "home", path: []string{"~/prices.yaml"}, home: true, custom: true},
		{name: "unreadable", path: []string{dir}, wantErr: true},
		{name: "malformed", path: []string{broken}, wantErr: true},
	}
	embedded, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.home {
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
			}
			tbl, err := LoadTable(c.path...)
			if c.wantErr {
				if err == nil {
					t.Fatalf("LoadTable(%q) = %+v, want an error", c.path, tbl)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadTable(%q) error = %v", c.path, err)
			}
			want := embedded.Date
			if c.custom {
				want = "2030-01-01"
			}
			if tbl.Date != want {
				t.Errorf("LoadTable(%q).Date = %q, want %q", c.path, tbl.Date, want)
			}
			if _, ok := tbl.Models["custom-future-model"]; ok != c.custom {
				t.Errorf("LoadTable(%q) has custom-future-model = %v, want %v", c.path, ok, c.custom)
			}
		})
	}
}

// TestResolveModel: an empty model takes its provider's default; vendor prefixes and case drop; exact names,
// aliases and dated names map to a table entry; an unknown name passes through.
func TestResolveModel(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		model    string
		provider planv1.Provider
		want     string
	}{
		{"empty claude", "", planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-5-5"},
		{"empty unspecified", "  ", planv1.Provider_PROVIDER_UNSPECIFIED, "claude-sonnet-5-5"},
		{"empty antigravity", "", planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3.8-flash-high"},
		{"empty codex", "", planv1.Provider_PROVIDER_CODEX, "gpt-5.5"},
		{"empty fake", "", planv1.Provider_PROVIDER_FAKE, ""},
		{"exact", "claude-opus-5-5", planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5"},
		{"vendor prefix and case", "Anthropic/Claude-Opus-5-5", planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5"},
		{"sonnet", "sonnet", planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-5-5"},
		{"claude-sonnet", "claude-sonnet", planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-5-5"},
		{"opus", "opus", planv1.Provider_PROVIDER_CLAUDE, "claude-opus-5-5"},
		{"haiku", "claude-haiku", planv1.Provider_PROVIDER_CLAUDE, "claude-haiku-5-5"},
		{"flash", "google/flash", planv1.Provider_PROVIDER_ANTIGRAVITY, "gemini-3.8-flash-high"},
		{"codex", "codex", planv1.Provider_PROVIDER_CODEX, "gpt-5.3-codex"},
		{"dated", "claude-sonnet-4-5-20250929", planv1.Provider_PROVIDER_CLAUDE, "claude-sonnet-4-5"},
		{"unknown", "Custom-Model", planv1.Provider_PROVIDER_FAKE, "custom-model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := tbl.ResolveModel(c.model, c.provider); got != c.want {
				t.Errorf("ResolveModel(%q, %v) = %q, want %q", c.model, c.provider, got, c.want)
			}
		})
	}
}

// TestEstimate: the cost sums every token kind at its price per million, thinking counted out of the output; a
// reported cost stays untouched beside the estimate; no usage or an unknown model has no price.
func TestEstimate(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}
	// claude-sonnet-5-5: input 2.0, output 10.0, cache_read 0.10, cache_write 2.50, thinking 10.0.
	cases := []struct {
		name      string
		usage     *planv1.Usage
		model     string
		wantCost  float64
		wantPrice bool
	}{
		// (1000*2 + (500-100)*10 + 100*10 + 2000*0.1 + 0*2.50) / 1e6 = 7200 / 1e6.
		{"tokens", &planv1.Usage{InputTokens: 1000, OutputTokens: 500, ThinkingTokens: 100, CacheReadTokens: 2000},
			"claude-sonnet-5-5", 0.0072, true},
		// (10000*2 + 2000*10) / 1e6 = 40000 / 1e6; the reported 0.060 is checked below.
		{"reported cost kept", &planv1.Usage{InputTokens: 10000, OutputTokens: 2000, CostUsd: 0.060},
			"claude-sonnet-5-5", 0.040, true},
		// Thinking above the output is not taken out of it: (100*10 + 200*10 + 1000*2.5) / 1e6 = 5500 / 1e6.
		{"thinking above output", &planv1.Usage{OutputTokens: 100, ThinkingTokens: 200, CacheWriteTokens: 1000},
			"claude-sonnet-5-5", 0.0055, true},
		{"no usage", nil, "claude-sonnet-5-5", 0, false},
		{"unknown model", &planv1.Usage{InputTokens: 1000, OutputTokens: 500}, "custom-unknown-model-xyz", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			reported := c.usage.GetCostUsd()
			cost, hasPrice, err := tbl.Estimate(c.usage, c.model, planv1.Provider_PROVIDER_CLAUDE)
			if err != nil {
				t.Fatalf("Estimate() error = %v", err)
			}
			if hasPrice != c.wantPrice {
				t.Errorf("hasPrice = %v, want %v", hasPrice, c.wantPrice)
			}
			if math.Abs(cost-c.wantCost) > 1e-9 {
				t.Errorf("cost = %f, want %f", cost, c.wantCost)
			}
			if got := c.usage.GetCostUsd(); got != reported {
				t.Errorf("reported cost = %f, want %f", got, reported)
			}
		})
	}
}
