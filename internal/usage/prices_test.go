package usage

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/internal/testx"
)

// TestPriceTableIntegrity: the embedded table parses cleanly, has a valid date and currency,
// and every model entry defines positive prices, a non-empty public source URL, and a date.
func TestPriceTableIntegrity(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatalf("DefaultTable() error = %v", err)
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

// TestEstimateTokens: an estimate is correctly calculated from tokens.
func TestEstimateTokens(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}

	// 1,000 input, 500 output, 100 thinking, 2,000 cache read, 0 cache write for claude-sonnet-5-5
	// claude-sonnet-5-5: input: 3.0, output: 15.0, cache_read: 0.3, cache_write: 3.75, thinking: 15.0
	// regularOutput: output (500) - thinking (100) = 400
	// expected: (1000*3 + 400*15 + 100*15 + 2000*0.3 + 0*3.75) / 1,000,000 = (3000 + 6000 + 1500 + 600) / 1,000,000 = 11100 / 1,000,000 = 0.0111
	u := &planv1.Usage{
		InputTokens:     1000,
		OutputTokens:    500,
		ThinkingTokens:  100,
		CacheReadTokens: 2000,
	}
	cost, hasPrice, err := tbl.Estimate(u, "claude-sonnet-5-5", planv1.Provider_PROVIDER_CLAUDE)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if !hasPrice {
		t.Error("expected hasPrice = true")
	}
	wantCost := 0.0111
	if math.Abs(cost-wantCost) > 1e-6 {
		t.Errorf("cost = %f, want %f", cost, wantCost)
	}
}

// TestModelMissingFromTable: a model without an entry in the table returns hasPrice = false.
func TestModelMissingFromTable(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}

	u := &planv1.Usage{
		InputTokens:  1000,
		OutputTokens: 500,
	}
	cost, hasPrice, err := tbl.Estimate(u, "custom-unknown-model-xyz", planv1.Provider_PROVIDER_FAKE)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if hasPrice {
		t.Errorf("expected hasPrice = false for unknown model, got cost %f", cost)
	}
}

// TestClaudeReportedCostPreserved: a task's reported cost stays preserved while an estimate is also computed.
func TestClaudeReportedCostPreserved(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	tbl, err := DefaultTable()
	if err != nil {
		t.Fatal(err)
	}

	u := &planv1.Usage{
		InputTokens:  10000,
		OutputTokens: 2000,
		CostUsd:      0.060, // Reported by Claude stream
	}
	est, hasPrice, err := tbl.Estimate(u, "claude-sonnet-5-5", planv1.Provider_PROVIDER_CLAUDE)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPrice {
		t.Fatal("expected hasPrice = true")
	}
	// Reported cost stays in u.CostUsd
	if got, want := u.GetCostUsd(), 0.060; got != want {
		t.Errorf("reported cost = %f, want %f", got, want)
	}
	// Estimate is calculated from current table
	// 10000 * 3.0 + 2000 * 15.0 = 30000 + 30000 = 60000 / 1e6 = 0.060
	if math.Abs(est-0.060) > 1e-6 {
		t.Errorf("estimated cost = %f, want 0.060", est)
	}
}

// TestOverrideSeam: LoadTable loads a custom override file when provided.
func TestOverrideSeam(t *testing.T) {
	testx.Portable(t)
	t.Parallel()

	dir := t.TempDir()
	overridePath := filepath.Join(dir, "custom_prices.yaml")
	customYAML := `date: "2030-01-01"
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
	if err := os.WriteFile(overridePath, []byte(customYAML), 0644); err != nil {
		t.Fatal(err)
	}

	tbl, err := LoadTable(overridePath)
	if err != nil {
		t.Fatalf("LoadTable(override) error = %v", err)
	}
	if tbl.Date != "2030-01-01" {
		t.Errorf("tbl.Date = %q, want 2030-01-01", tbl.Date)
	}
	if _, ok := tbl.Models["custom-future-model"]; !ok {
		t.Error("custom-future-model missing from loaded override table")
	}
}
