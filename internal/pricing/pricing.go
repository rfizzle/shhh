// Package pricing is shhh's model data: what a model costs, how much context
// it has, and how it spells its thinking level. One public table carries all
// three, so one download serves the spend meter, the context gauge, and the
// reasoning ladder — and one snapshot of it ships inside the binary as the
// floor under the download
// (docs/capabilities/providers.md#model-data-is-fetched-and-a-snapshot-ships).
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	pricingURL   = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	cacheFile    = "model_prices.json"
	recheckAfter = 24 * time.Hour
)

// snapshot is the trimmed table built into the binary by `make model-data`.
// It answers before the first download and after a download that failed,
// so a model's ladder is never unknown just because the network was.
//
//go:embed models.json
var snapshot []byte

// ModelPricing is one model's entry: its prices and context window, and the
// capability flags that decide how a thinking level is sent to it. The flags
// are absent-is-false, which is how the table writes them.
type ModelPricing struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
	MaxInputTokens     int64   `json:"max_input_tokens"`
	MaxOutputTokens    int64   `json:"max_output_tokens"`

	// SupportsReasoning is whether the model has a thinking knob at all. A
	// model without one is handed no reasoning field, whatever the session
	// asked for.
	SupportsReasoning bool `json:"supports_reasoning"`
	// AdaptiveThinking is the Anthropic models whose knob is a named effort
	// under adaptive thinking; LegacyThinking is the ones that still take a
	// token budget. A model can be both (the 4.6 generation).
	AdaptiveThinking bool `json:"supports_adaptive_thinking"`
	LegacyThinking   bool `json:"supports_legacy_thinking"`
	// ThinkingAlwaysOn is a model that cannot be told not to think.
	ThinkingAlwaysOn bool `json:"thinking_always_on"`
	// XHighEffort, MaxEffort, MinimalEffort and NoneEffort are the named
	// levels beyond low/medium/high that the model accepts.
	XHighEffort   bool `json:"supports_xhigh_reasoning_effort"`
	MaxEffort     bool `json:"supports_max_reasoning_effort"`
	MinimalEffort bool `json:"supports_minimal_reasoning_effort"`
	NoneEffort    bool `json:"supports_none_reasoning_effort"`
}

// known reports whether the entry carries anything worth keeping.
func (p ModelPricing) known() bool {
	return p.InputCostPerToken > 0 || p.OutputCostPerToken > 0 || p.MaxInputTokens > 0 ||
		p.MaxOutputTokens > 0 || p.SupportsReasoning
}

type Table struct {
	models map[string]ModelPricing
}

// NewTable builds a table from explicit per-model entries, for tests and
// offline use.
func NewTable(models map[string]ModelPricing) *Table {
	return &Table{models: models}
}

// Snapshot is the built-in table alone, with nothing downloaded over it.
func Snapshot() *Table {
	t, err := parse(snapshot)
	if err != nil {
		// The snapshot is generated and checked in; a malformed one is a
		// build defect, and an empty table is the only sane runtime answer.
		return NewTable(nil)
	}
	return t
}

// Load is the table for this process: the snapshot, with the downloaded file
// over it. The download is refreshed when it is older than a day; a refresh
// that fails leaves whatever was there. It never errors on the network —
// the snapshot always answers — only on a cache directory it cannot use.
func Load() (*Table, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	if shouldRefresh(path) {
		_ = download(path)
	}
	return loadWithSnapshot(path)
}

// Refresh downloads the table now, whatever the cache's age, and returns the
// resulting table. This is the manual trigger; Load is the routine one.
func Refresh() (*Table, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	if err := download(path); err != nil {
		return nil, fmt.Errorf("fetch model data: %w", err)
	}
	return loadWithSnapshot(path)
}

// FetchedAt is when the downloaded table was last written, or zero when
// nothing has been downloaded yet.
func FetchedAt() time.Time {
	path, err := cachePath()
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func loadWithSnapshot(path string) (*Table, error) {
	t := Snapshot()
	data, err := os.ReadFile(path)
	if err != nil {
		return t, nil
	}
	downloaded, err := parse(data)
	if err != nil {
		return t, nil
	}
	t.Overlay(downloaded.models)
	return t, nil
}

// Overlay adds entries that take precedence over the ones already here. It
// is how the download lands on the snapshot, and how a gateway profile's
// declared costs and context windows reach the spend meter: what is already
// here stays the fallback for every model the overlay does not describe.
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
		// An overlay may describe only part of a model — prices but no
		// context window, or the reverse; keep what it leaves out.
		if p.InputCostPerToken == 0 && p.OutputCostPerToken == 0 {
			p.InputCostPerToken = existing.InputCostPerToken
			p.OutputCostPerToken = existing.OutputCostPerToken
		}
		if p.MaxInputTokens == 0 {
			p.MaxInputTokens = existing.MaxInputTokens
		}
		if p.MaxOutputTokens == 0 {
			p.MaxOutputTokens = existing.MaxOutputTokens
		}
		if !p.SupportsReasoning {
			// A profile entry says nothing about thinking; the flags are
			// only ever written together, so an entry without the first
			// keeps the set it is landing on.
			p.SupportsReasoning = existing.SupportsReasoning
			p.AdaptiveThinking = existing.AdaptiveThinking
			p.LegacyThinking = existing.LegacyThinking
			p.ThinkingAlwaysOn = existing.ThinkingAlwaysOn
			p.XHighEffort = existing.XHighEffort
			p.MaxEffort = existing.MaxEffort
			p.MinimalEffort = existing.MinimalEffort
			p.NoneEffort = existing.NoneEffort
		}
		t.models[name] = p
	}
}

// Len is how many models the table describes.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.models)
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

// Entry is the model's whole entry, for the reasoning ladder that needs the
// capability flags together.
func (t *Table) Entry(model string) (ModelPricing, bool) {
	return t.lookup(model)
}

func (t *Table) lookup(model string) (ModelPricing, bool) {
	if t == nil {
		return ModelPricing{}, false
	}
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
	// A body that does not parse is not written: a broken download must
	// not replace a good cache, and the snapshot is the floor either way.
	if _, err := parse(data); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func parse(data []byte) (*Table, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse model data: %w", err)
	}

	models := make(map[string]ModelPricing, len(raw))
	for key, val := range raw {
		var p ModelPricing
		if err := json.Unmarshal(val, &p); err != nil {
			continue
		}
		if p.known() {
			models[key] = p
		}
	}
	return &Table{models: models}, nil
}

func cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFile), nil
}

// CacheDir is where the price table is cached. It is exported so the
// migration that moves a Mac off ~/Library can name the same directory this
// package writes to, rather than deriving the rule a second time.
func CacheDir() (string, error) { return cacheDir() }

// cacheDir is one layout on every platform: XDG_CACHE_HOME if it is set, then
// ~/.cache/shhh — the same rule the config and data directories follow
// (docs/capabilities/configuration.md#one-layout-everywhere). This one holds
// only the price table, which is re-downloaded when it is missing, so a Mac
// that still has the old ~/Library/Caches/shhh loses nothing by ignoring it.
func cacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "shhh"), nil
}
