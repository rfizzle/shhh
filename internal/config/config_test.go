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

[provider.openai]
api_key = "sk-test-key"

[provider.openai_compatible]
api_key = "local-key"
base_url = "http://localhost:11434/v1"
model = "llama3"

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
	if cfg.Provider.OpenAI.APIKey != "sk-test-key" {
		t.Errorf("provider.openai.api_key = %q, want %q", cfg.Provider.OpenAI.APIKey, "sk-test-key")
	}
	if cfg.Provider.OpenAICompat.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("provider.openai_compatible.base_url = %q, want %q", cfg.Provider.OpenAICompat.BaseURL, "http://localhost:11434/v1")
	}
	if cfg.Provider.OpenAICompat.Model != "llama3" {
		t.Errorf("provider.openai_compatible.model = %q, want %q", cfg.Provider.OpenAICompat.Model, "llama3")
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
			OpenAI:     ProviderDetail{APIKey: "sk-openai"},
			Gemini:     ProviderDetail{APIKey: "ai-gemini"},
			OpenRouter: ProviderDetail{APIKey: "sk-or-test"},
			OpenAICompat: ProviderDetail{APIKey: "local"},
		},
	}

	tests := []struct {
		name string
		want string
	}{
		{"openai", "sk-openai"},
		{"gemini", "ai-gemini"},
		{"openrouter", "sk-or-test"},
		{"openai-compatible", "local"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := cfg.ProviderAPIKey(tt.name); got != tt.want {
			t.Errorf("ProviderAPIKey(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestProviderModel(t *testing.T) {
	cfg := Config{
		Provider: ProviderConfig{
			OpenAI:       ProviderDetail{Model: "gpt-4o-mini"},
			OpenAICompat: ProviderDetail{Model: "llama3"},
		},
	}

	if got := cfg.ProviderModel("openai"); got != "gpt-4o-mini" {
		t.Errorf("ProviderModel(openai) = %q, want %q", got, "gpt-4o-mini")
	}
	if got := cfg.ProviderModel("openai-compatible"); got != "llama3" {
		t.Errorf("ProviderModel(openai-compatible) = %q, want %q", got, "llama3")
	}
	if got := cfg.ProviderModel("unknown"); got != "" {
		t.Errorf("ProviderModel(unknown) = %q, want empty", got)
	}
}

func TestProviderBaseURL(t *testing.T) {
	cfg := Config{
		Provider: ProviderConfig{
			OpenAICompat: ProviderDetail{BaseURL: "http://localhost:11434/v1"},
		},
	}

	if got := cfg.ProviderBaseURL("openai-compatible"); got != "http://localhost:11434/v1" {
		t.Errorf("got %q, want %q", got, "http://localhost:11434/v1")
	}
	if got := cfg.ProviderBaseURL("openai"); got != "" {
		t.Errorf("ProviderBaseURL(openai) = %q, want empty", got)
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
