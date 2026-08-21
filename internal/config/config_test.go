package config

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestSet_MaxToolRounds(t *testing.T) {
	var cfg Config
	if err := Set(&cfg, "behavior.max_tool_rounds", "40"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Behavior.MaxToolRounds != 40 {
		t.Errorf("behavior.max_tool_rounds = %d, want 40", cfg.Behavior.MaxToolRounds)
	}
}

func TestLoadFrom_MaxToolRounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[behavior]
max_tool_rounds = 10
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Behavior.MaxToolRounds != 10 {
		t.Errorf("behavior.max_tool_rounds = %d, want 10", cfg.Behavior.MaxToolRounds)
	}
}

func TestSet_CommandAllowlist(t *testing.T) {
	var cfg Config
	if err := Set(&cfg, "behavior.command_allowlist", "git status, go test ,"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Behavior.CommandAllowlist
	if len(got) != 2 || got[0] != "git status" || got[1] != "go test" {
		t.Errorf("behavior.command_allowlist = %v, want [git status, go test]", got)
	}

	if err := Set(&cfg, "behavior.command_allowlist", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Behavior.CommandAllowlist) != 0 {
		t.Errorf("empty value should clear the allowlist, got %v", cfg.Behavior.CommandAllowlist)
	}
}

func TestSet_ModeConfig(t *testing.T) {
	var cfg Config
	if err := Set(&cfg, "behavior.default_mode", "accept-edits"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Behavior.DefaultMode != "accept-edits" {
		t.Errorf("behavior.default_mode = %q, want accept-edits", cfg.Behavior.DefaultMode)
	}
	if err := Set(&cfg, "behavior.mode_cycle", "manual, auto ,"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Behavior.ModeCycle
	if len(got) != 2 || got[0] != "manual" || got[1] != "auto" {
		t.Errorf("behavior.mode_cycle = %v, want [manual, auto]", got)
	}
}

func TestLoadFrom_ModeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[behavior]
default_mode = "auto"
mode_cycle = ["manual", "accept-edits"]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Behavior.DefaultMode != "auto" {
		t.Errorf("behavior.default_mode = %q, want auto", cfg.Behavior.DefaultMode)
	}
	got := cfg.Behavior.ModeCycle
	if len(got) != 2 || got[0] != "manual" || got[1] != "accept-edits" {
		t.Errorf("behavior.mode_cycle = %v, want [manual, accept-edits]", got)
	}
}

func TestLoadFrom_CommandAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[behavior]
command_allowlist = ["git status", "go test"]
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Behavior.CommandAllowlist
	if len(got) != 2 || got[0] != "git status" || got[1] != "go test" {
		t.Errorf("behavior.command_allowlist = %v, want [git status, go test]", got)
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

func TestSet_ClassifierConfig(t *testing.T) {
	var cfg Config
	if err := Set(&cfg, "behavior.classifier_model", "gpt-5-mini"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Behavior.ClassifierModel != "gpt-5-mini" {
		t.Errorf("behavior.classifier_model = %q, want gpt-5-mini", cfg.Behavior.ClassifierModel)
	}
	for key, want := range map[string]int{
		"behavior.classifier_timeout_seconds": 15,
		"behavior.classifier_max_tokens":      512,
		"behavior.classifier_retries":         2,
	} {
		if err := Set(&cfg, key, "0"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := Set(&cfg, key, strconv.Itoa(want)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if cfg.Behavior.ClassifierTimeoutSeconds != 15 || cfg.Behavior.ClassifierMaxTokens != 512 || cfg.Behavior.ClassifierRetries != 2 {
		t.Errorf("classifier settings = %d/%d/%d, want 15/512/2",
			cfg.Behavior.ClassifierTimeoutSeconds, cfg.Behavior.ClassifierMaxTokens, cfg.Behavior.ClassifierRetries)
	}
}
