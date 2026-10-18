// Package usage provides model price tables and cost estimation for worker token usage.
package usage

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

//go:embed prices.yaml
var defaultPricesYAML []byte

// DefaultLocalPath is the documented path where a user can place an override prices.yaml file.
const DefaultLocalPath = ".config/djinn/prices.yaml"

// ModelPrice contains token pricing per million tokens in USD, its display label, and its public citation.
type ModelPrice struct {
	Label      string  `yaml:"label"`
	Input      float64 `yaml:"input"`
	Output     float64 `yaml:"output"`
	CacheRead  float64 `yaml:"cache_read"`
	CacheWrite float64 `yaml:"cache_write"`
	Thinking   float64 `yaml:"thinking"`
	Source     string  `yaml:"source"`
	Date       string  `yaml:"date"`
}

// PriceTable is the set of known model prices and labels at a given date.
type PriceTable struct {
	Date     string                `yaml:"date"`
	Currency string                `yaml:"currency"`
	Models   map[string]ModelPrice `yaml:"models"`
	Order    []string              `yaml:"-"`
}

var (
	defaultOnce  sync.Once
	defaultTable *PriceTable
	errDefault   error
)

// DefaultTable returns the embedded default price table.
func DefaultTable() (*PriceTable, error) {
	defaultOnce.Do(func() {
		defaultTable, errDefault = LoadTable()
	})
	return defaultTable, errDefault
}

// LoadTable loads the price table.
// If an override path is provided and non-empty, and the file exists, it is loaded;
// otherwise the embedded default table is returned.
// This provides a clear seam for future local overrides.
func LoadTable(overridePath ...string) (*PriceTable, error) {
	var data []byte
	if len(overridePath) > 0 && overridePath[0] != "" {
		path := overridePath[0]
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		b, err := os.ReadFile(path)
		if err == nil {
			data = b
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read price override: %w", err)
		}
	}
	if data == nil {
		data = defaultPricesYAML
	}
	var table PriceTable
	if err := yaml.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("parse price table: %w", err)
	}
	table.Order = extractModelOrder(data)
	return &table, nil
}

// extractModelOrder reads the YAML document's models mapping key order so Catalog() is deterministic.
func extractModelOrder(data []byte) []string {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "models" && root.Content[i+1].Kind == yaml.MappingNode {
			modelsNode := root.Content[i+1]
			order := make([]string, 0, len(modelsNode.Content)/2)
			for j := 0; j+1 < len(modelsNode.Content); j += 2 {
				order = append(order, modelsNode.Content[j].Value)
			}
			return order
		}
	}
	return nil
}

// ProviderForModel returns the provider for a canonical model ID prefix.
func ProviderForModel(model string) planv1.Provider {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude-"):
		return planv1.Provider_PROVIDER_CLAUDE
	case strings.HasPrefix(m, "gemini-"):
		return planv1.Provider_PROVIDER_ANTIGRAVITY
	case strings.HasPrefix(m, "gpt-"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return planv1.Provider_PROVIDER_CODEX
	default:
		return planv1.Provider_PROVIDER_UNSPECIFIED
	}
}

// Catalog returns all models in the price table in YAML declaration order with their canonical ID, label, and provider.
func (t *PriceTable) Catalog() []*planv1.CircleModel {
	if t == nil {
		return nil
	}
	keys := t.Order
	if len(keys) == 0 {
		keys = make([]string, 0, len(t.Models))
		for k := range t.Models {
			keys = append(keys, k)
		}
	}
	out := make([]*planv1.CircleModel, 0, len(keys))
	for _, id := range keys {
		entry, ok := t.Models[id]
		if !ok {
			continue
		}
		label := entry.Label
		if label == "" {
			label = id
		}
		out = append(out, &planv1.CircleModel{
			Id:       id,
			Label:    label,
			Provider: ProviderForModel(id),
		})
	}
	return out
}

// ResolveModel maps a requested or stream-reported model and provider to a canonical entry in the table.
func (t *PriceTable) ResolveModel(model string, provider planv1.Provider) string {
	m := strings.TrimSpace(model)
	if m == "" {
		switch provider {
		case planv1.Provider_PROVIDER_CLAUDE, planv1.Provider_PROVIDER_UNSPECIFIED:
			m = "claude-sonnet-5-5"
		case planv1.Provider_PROVIDER_ANTIGRAVITY:
			m = "gemini-3.8-flash-high"
		case planv1.Provider_PROVIDER_CODEX:
			m = "gpt-5.5"
		default:
			return ""
		}
	}

	m = strings.ToLower(m)
	// Strip vendor prefixes: "anthropic/", "google/", "openai/"
	if idx := strings.Index(m, "/"); idx != -1 {
		m = m[idx+1:]
	}

	// Exact match first
	if _, ok := t.Models[m]; ok {
		return m
	}

	// Check common aliases
	switch m {
	case "fable", "claude-fable", "claude fable 5.1":
		return "claude-fable-5-1"
	case "sonnet", "claude-sonnet":
		return "claude-sonnet-5-5"
	case "opus", "claude-opus":
		return "claude-opus-5-5"
	case "haiku", "claude-haiku":
		return "claude-haiku-5-5"
	case "argon", "gemini-argon", "gemini-4-argon", "gemini 4 argon", "gemini 4 argon (high)":
		return "gemini-4-argon-high"
	case "gemini 4 argon (medium)":
		return "gemini-4-argon-medium"
	case "flash", "gemini-flash", "gemini-3.8-flash", "gemini 3.8 flash", "gemini 3.8 flash (high)":
		return "gemini-3.8-flash-high"
	case "gemini 3.8 flash (medium)":
		return "gemini-3.8-flash-medium"
	case "codex":
		return "gpt-5.3-codex"
	}

	// Prefix match against known models (e.g. "claude-sonnet-4-5-20250929" -> "claude-sonnet-4-5")
	for key := range t.Models {
		if strings.HasPrefix(m, key) {
			return key
		}
	}

	return m
}

// Estimate computes the estimated cost in USD for the given usage and model.
// Returns cost, whether a price exists for the model, and any error.
func (t *PriceTable) Estimate(u *planv1.Usage, model string, provider planv1.Provider) (cost float64, hasPrice bool, err error) {
	if u == nil {
		return 0, false, nil
	}
	canonical := t.ResolveModel(model, provider)
	p, ok := t.Models[canonical]
	if !ok {
		return 0, false, nil
	}

	regularOutput := u.GetOutputTokens()
	thinking := u.GetThinkingTokens()
	if thinking > 0 && regularOutput >= thinking {
		regularOutput -= thinking
	}

	// Prices are per million tokens:
	c := (float64(u.GetInputTokens())*p.Input +
		float64(regularOutput)*p.Output +
		float64(thinking)*p.Thinking +
		float64(u.GetCacheReadTokens())*p.CacheRead +
		float64(u.GetCacheWriteTokens())*p.CacheWrite) / 1_000_000.0

	return c, true, nil
}
