package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	pricingURL   = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	cacheFile    = "model_prices.json"
	recheckAfter = 24 * time.Hour
)

type ModelPricing struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
	MaxInputTokens     int64   `json:"max_input_tokens"`
}

type Table struct {
	models map[string]ModelPricing
}

// NewTable builds a table from explicit per-model entries, for tests and
// offline use.
func NewTable(models map[string]ModelPricing) *Table {
	return &Table{models: models}
}

func Load() (*Table, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, cacheFile)

	if shouldRefresh(path) {
		if err := download(path); err != nil {
			if _, statErr := os.Stat(path); statErr != nil {
				return nil, fmt.Errorf("fetch pricing data: %w", err)
			}
		}
	}

	return loadFromFile(path)
}

// Overlay adds entries that take precedence over the downloaded table. It is
// how a gateway profile's declared costs and context windows reach the spend
// meter: the public table stays the fallback for every model the profile
// does not describe.
func (t *Table) Overlay(models map[string]ModelPricing) {
	if t == nil || len(models) == 0 {
		return
	}
	if t.models == nil {
		t.models = map[string]ModelPricing{}
	}
	for name, p := range models {
		existing, ok := t.models[name]
		if !ok {
			t.models[name] = p
			continue
		}
		// A profile may describe only part of a model — prices but no
		// context window, or the reverse; keep what it leaves out.
		if p.InputCostPerToken == 0 && p.OutputCostPerToken == 0 {
			p.InputCostPerToken = existing.InputCostPerToken
			p.OutputCostPerToken = existing.OutputCostPerToken
		}
		if p.MaxInputTokens == 0 {
			p.MaxInputTokens = existing.MaxInputTokens
		}
		t.models[name] = p
	}
}

func (t *Table) Cost(model string, tokensIn, tokensOut int64) (inputCost, outputCost float64, found bool) {
	p, ok := t.lookup(model)
	if !ok || (p.InputCostPerToken == 0 && p.OutputCostPerToken == 0) {
		return 0, 0, false
	}
	return float64(tokensIn) * p.InputCostPerToken,
		float64(tokensOut) * p.OutputCostPerToken,
		true
}

// ContextWindow returns the model's context window (max input tokens) when
// the table knows it.
func (t *Table) ContextWindow(model string) (int64, bool) {
	p, ok := t.lookup(model)
	if !ok || p.MaxInputTokens <= 0 {
		return 0, false
	}
	return p.MaxInputTokens, true
}

func (t *Table) lookup(model string) (ModelPricing, bool) {
	if p, ok := t.models[model]; ok {
		return p, true
	}
	lower := strings.ToLower(model)
	if p, ok := t.models[lower]; ok {
		return p, true
	}
	for k, p := range t.models {
		if strings.HasSuffix(k, "/"+lower) || strings.HasSuffix(k, "/"+model) {
			return p, true
		}
	}
	return ModelPricing{}, false
}

func shouldRefresh(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > recheckAfter
}

func download(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(pricingURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func loadFromFile(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse pricing data: %w", err)
	}

	models := make(map[string]ModelPricing, len(raw))
	for key, val := range raw {
		var p ModelPricing
		if err := json.Unmarshal(val, &p); err != nil {
			continue
		}
		if p.InputCostPerToken > 0 || p.OutputCostPerToken > 0 || p.MaxInputTokens > 0 {
			models[key] = p
		}
	}

	return &Table{models: models}, nil
}

func cacheDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Caches", "shhh"), nil
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "shhh"), nil
}
