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
}

type Table struct {
	models map[string]ModelPricing
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

func (t *Table) Cost(model string, tokensIn, tokensOut int64) (inputCost, outputCost float64, found bool) {
	p, ok := t.lookup(model)
	if !ok {
		return 0, 0, false
	}
	return float64(tokensIn) * p.InputCostPerToken,
		float64(tokensOut) * p.OutputCostPerToken,
		true
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
		if p.InputCostPerToken > 0 || p.OutputCostPerToken > 0 {
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
