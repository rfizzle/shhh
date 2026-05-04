package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCost_ExactMatch(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{
		"gpt-4o": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001},
	}}

	in, out, found := table.Cost("gpt-4o", 1000, 500)
	if !found {
		t.Fatal("expected to find gpt-4o")
	}
	if in != 0.0025 {
		t.Fatalf("input cost: want 0.0025, got %f", in)
	}
	if out != 0.005 {
		t.Fatalf("output cost: want 0.005, got %f", out)
	}
}

func TestCost_SuffixMatch(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{
		"openai/gpt-4o": {InputCostPerToken: 0.0000025, OutputCostPerToken: 0.00001},
	}}

	_, _, found := table.Cost("gpt-4o", 100, 50)
	if !found {
		t.Fatal("expected suffix match for gpt-4o")
	}
}

func TestCost_NotFound(t *testing.T) {
	table := &Table{models: map[string]ModelPricing{}}

	_, _, found := table.Cost("nonexistent", 100, 50)
	if found {
		t.Fatal("expected not found")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	data := `{
		"gpt-4o": {"input_cost_per_token": 0.0000025, "output_cost_per_token": 0.00001},
		"gemini-2.5-flash": {"input_cost_per_token": 0.0000001, "output_cost_per_token": 0.0000004}
	}`
	os.WriteFile(path, []byte(data), 0o600)

	table, err := loadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, _, found := table.Cost("gpt-4o", 1, 1); !found {
		t.Error("expected to find gpt-4o")
	}
	if _, _, found := table.Cost("gemini-2.5-flash", 1, 1); !found {
		t.Error("expected to find gemini-2.5-flash")
	}
}

func TestShouldRefresh(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if !shouldRefresh(missing) {
		t.Error("expected refresh for missing file")
	}

	existing := filepath.Join(dir, "exists.json")
	os.WriteFile(existing, []byte("{}"), 0o600)
	if shouldRefresh(existing) {
		t.Error("expected no refresh for fresh file")
	}
}
