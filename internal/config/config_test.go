package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_MissingFileReturnsZeroValue(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider.Default != "" {
		t.Errorf("expected empty default provider, got %q", cfg.Provider.Default)
	}
}

func TestLoadFrom_FullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[provider]
default = "openai"
model = "gpt-4o"
api_key = "sk-test-key"
base_url = "http://localhost:11434/v1"
name = "Ollama"

[behavior]
silent_mode = true
shell = "/bin/zsh"

[appearance]
accent_color = "magenta"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Provider.Default != "openai" {
		t.Errorf("provider.default = %q, want %q", cfg.Provider.Default, "openai")
	}
	if cfg.Provider.Model != "gpt-4o" {
		t.Errorf("provider.model = %q, want %q", cfg.Provider.Model, "gpt-4o")
	}
	if cfg.Provider.APIKey != "sk-test-key" {
		t.Errorf("provider.api_key = %q, want %q", cfg.Provider.APIKey, "sk-test-key")
	}
	if cfg.Provider.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("provider.base_url = %q, want %q", cfg.Provider.BaseURL, "http://localhost:11434/v1")
	}
	if cfg.Provider.Name != "Ollama" {
		t.Errorf("provider.name = %q, want %q", cfg.Provider.Name, "Ollama")
	}
	if !cfg.Behavior.SilentMode {
		t.Error("behavior.silent_mode = false, want true")
	}
	if cfg.Behavior.Shell != "/bin/zsh" {
		t.Errorf("behavior.shell = %q, want %q", cfg.Behavior.Shell, "/bin/zsh")
	}
	if cfg.Appearance.AccentColor != "magenta" {
		t.Errorf("appearance.accent_color = %q, want %q", cfg.Appearance.AccentColor, "magenta")
	}
}

func TestLoadFrom_FirstFileWins(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")

	os.WriteFile(first, []byte(`[provider]
default = "gemini"
`), 0644)
	os.WriteFile(second, []byte(`[provider]
default = "openai"
`), 0644)

	cfg, err := LoadFrom(first, second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider.Default != "gemini" {
		t.Errorf("expected first file to win, got provider.default = %q", cfg.Provider.Default)
	}
}

func TestLoadFrom_SkipsMissingThenReadsNext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`[provider]
default = "openrouter"
`), 0644)

	cfg, err := LoadFrom("/nonexistent/config.toml", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider.Default != "openrouter" {
		t.Errorf("expected openrouter, got %q", cfg.Provider.Default)
	}
}

func TestLoadFrom_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	os.WriteFile(path, []byte(`[broken`), 0644)

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestProviderAPIKey(t *testing.T) {
	cfg := Config{
		Provider: ProviderConfig{
			APIKey: "sk-test-key",
		},
	}

	if got := cfg.ProviderAPIKey(); got != "sk-test-key" {
		t.Errorf("ProviderAPIKey() = %q, want %q", got, "sk-test-key")
	}

	empty := Config{}
	if got := empty.ProviderAPIKey(); got != "" {
		t.Errorf("ProviderAPIKey() on empty config = %q, want empty", got)
	}
}


func TestProviderBaseURL(t *testing.T) {
	cfg := Config{
		Provider: ProviderConfig{
			BaseURL: "http://localhost:11434/v1",
		},
	}

	if got := cfg.ProviderBaseURL(); got != "http://localhost:11434/v1" {
		t.Errorf("ProviderBaseURL() = %q, want %q", got, "http://localhost:11434/v1")
	}

	empty := Config{}
	if got := empty.ProviderBaseURL(); got != "" {
		t.Errorf("ProviderBaseURL() on empty config = %q, want empty", got)
	}
}

func TestPaths_IncludesDotConfig(t *testing.T) {
	ps := Paths()
	found := false
	for _, p := range ps {
		if filepath.Base(filepath.Dir(p)) == "shhh" && filepath.Base(p) == "config.toml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Paths() should include a .../shhh/config.toml path, got %v", ps)
	}
}
