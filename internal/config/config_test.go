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

	if err := os.WriteFile(first, []byte(`[provider]
default = "gemini"
`), 0644); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(second, []byte(`[provider]
default = "openai"
`), 0644))

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
	must(t, os.WriteFile(path, []byte(`[provider]
default = "openrouter"
`), 0644))

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
	must(t, os.WriteFile(path, []byte(`[broken`), 0644))

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

func TestSet_MemoryConfig(t *testing.T) {
	var cfg Config
	if cfg.EffectiveMemoryMaxEntries() != DefaultMemoryMaxEntries || cfg.EffectiveMemoryMaxTokens() != DefaultMemoryMaxTokens {
		t.Errorf("zero config should fall back to defaults, got %d/%d",
			cfg.EffectiveMemoryMaxEntries(), cfg.EffectiveMemoryMaxTokens())
	}
	for key, value := range map[string]string{
		"behavior.memory_disabled":    "true",
		"behavior.memory_max_entries": "5",
		"behavior.memory_max_tokens":  "300",
	} {
		if err := Set(&cfg, key, value); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
	}
	if !cfg.Behavior.MemoryDisabled {
		t.Error("behavior.memory_disabled should be set")
	}
	if cfg.EffectiveMemoryMaxEntries() != 5 || cfg.EffectiveMemoryMaxTokens() != 300 {
		t.Errorf("memory bounds = %d/%d, want 5/300",
			cfg.EffectiveMemoryMaxEntries(), cfg.EffectiveMemoryMaxTokens())
	}
}

func TestSet_WebConfig(t *testing.T) {
	var cfg Config
	for key, value := range map[string]string{
		"web.allow_private":         "true",
		"web.fetch_max_bytes":       "1048576",
		"web.fetch_timeout_seconds": "10",
		"web.cache_ttl_minutes":     "30",
		"web.search_provider":       "brave",
		"web.search_api_key":        "bsk-test",
	} {
		if err := Set(&cfg, key, value); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
	}
	if !cfg.Web.AllowPrivate {
		t.Error("web.allow_private not set")
	}
	if cfg.Web.FetchMaxBytes != 1048576 {
		t.Errorf("web.fetch_max_bytes = %d", cfg.Web.FetchMaxBytes)
	}
	if cfg.Web.FetchTimeoutSeconds != 10 || cfg.Web.CacheTTLMinutes != 30 {
		t.Errorf("web timings = %d/%d, want 10/30", cfg.Web.FetchTimeoutSeconds, cfg.Web.CacheTTLMinutes)
	}
	if cfg.Web.SearchProvider != "brave" || cfg.Web.SearchAPIKey != "bsk-test" {
		t.Errorf("web search = %q/%q", cfg.Web.SearchProvider, cfg.Web.SearchAPIKey)
	}
}

func TestLoadFrom_WebConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[web]
allow_private = true
fetch_max_bytes = 4194304
fetch_timeout_seconds = 20
cache_ttl_minutes = 15
search_provider = "brave"
search_api_key = "bsk-abc"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Web.AllowPrivate || cfg.Web.FetchMaxBytes != 4194304 || cfg.Web.FetchTimeoutSeconds != 20 ||
		cfg.Web.CacheTTLMinutes != 15 || cfg.Web.SearchProvider != "brave" || cfg.Web.SearchAPIKey != "bsk-abc" {
		t.Errorf("web config = %+v", cfg.Web)
	}
}

func TestSet_LSPConfig(t *testing.T) {
	var cfg Config
	if err := Set(&cfg, "lsp.disabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := Set(&cfg, "lsp.request_timeout_seconds", "20"); err != nil {
		t.Fatal(err)
	}
	if err := Set(&cfg, "lsp.diagnostics_timeout_seconds", "5"); err != nil {
		t.Fatal(err)
	}
	if !cfg.LSP.Disabled || cfg.LSP.RequestTimeoutSeconds != 20 || cfg.LSP.DiagnosticsTimeoutSeconds != 5 {
		t.Fatalf("lsp config not applied: %+v", cfg.LSP)
	}
}

func TestAgentModelResolution(t *testing.T) {
	cfg := Config{}
	if got := cfg.AgentModel("writer", "session-model"); got != "session-model" {
		t.Errorf("unset config should inherit the session model, got %q", got)
	}
	cfg.Agents.Model = "agents-default"
	if got := cfg.AgentModel("writer", "session-model"); got != "agents-default" {
		t.Errorf("agents.model should apply, got %q", got)
	}
	cfg.Agents.Profiles = map[string]AgentProfile{"researcher": {Model: "cheap-model"}}
	if got := cfg.AgentModel("researcher", "session-model"); got != "cheap-model" {
		t.Errorf("the role profile should win, got %q", got)
	}
	if got := cfg.AgentModel("writer", "session-model"); got != "agents-default" {
		t.Errorf("a role without a profile falls back to agents.model, got %q", got)
	}
	// "inherit" at any level falls through to the session model.
	cfg.Agents.Profiles["writer"] = AgentProfile{Model: "inherit"}
	if got := cfg.AgentModel("writer", "session-model"); got != "session-model" {
		t.Errorf("inherit should fall through to the session model, got %q", got)
	}
}

func TestSetAgentAndReadOnlyKeys(t *testing.T) {
	var cfg Config
	for _, kv := range [][2]string{
		{"agents.model", "haiku"},
		{"agents.researcher_model", "tiny"},
		{"agents.max_concurrent", "5"},
		{"behavior.read_only_commands", "make lint, bazel query"},
		{"behavior.read_only_auto", "false"},
	} {
		if err := Set(&cfg, kv[0], kv[1]); err != nil {
			t.Fatalf("Set(%q): %v", kv[0], err)
		}
	}
	if cfg.Agents.Model != "haiku" || cfg.Agents.Profiles["researcher"].Model != "tiny" {
		t.Errorf("agent models not set: %+v", cfg.Agents)
	}
	if cfg.Agents.MaxConcurrent != 5 {
		t.Errorf("max_concurrent = %d, want 5", cfg.Agents.MaxConcurrent)
	}
	if len(cfg.Behavior.ReadOnlyCommands) != 2 {
		t.Errorf("read_only_commands = %v", cfg.Behavior.ReadOnlyCommands)
	}
	if cfg.ReadOnlyAutoEnabled() {
		t.Error("read_only_auto=false should disable the built-in list")
	}
}

func TestSet_SummaryConfig(t *testing.T) {
	var cfg Config
	for key, value := range map[string]string{
		"summary.model":           "anthropic/claude-haiku-4-5",
		"summary.interval_rounds": "25",
		"summary.min_gap_seconds": "45",
		"summary.timeout_seconds": "10",
		"summary.max_tokens":      "256",
		"summary.disabled":        "true",
	} {
		if err := Set(&cfg, key, value); err != nil {
			t.Fatalf("Set(%q) unexpected error: %v", key, err)
		}
	}
	want := SummaryConfig{
		Model: "anthropic/claude-haiku-4-5", IntervalRounds: 25,
		MinGapSeconds: 45, TimeoutSeconds: 10, MaxTokens: 256, Disabled: true,
	}
	if cfg.Summary != want {
		t.Errorf("summary = %+v, want %+v", cfg.Summary, want)
	}
}

func TestLoadFrom_SummaryConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[summary]
model = "anthropic/claude-haiku-4-5"
interval_rounds = 12
disabled = true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Summary.Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("summary.model = %q", cfg.Summary.Model)
	}
	if cfg.Summary.IntervalRounds != 12 || !cfg.Summary.Disabled {
		t.Errorf("summary = %+v", cfg.Summary)
	}
	// Unset numbers stay zero so the agent defaults stand.
	if cfg.Summary.MinGapSeconds != 0 || cfg.Summary.TimeoutSeconds != 0 || cfg.Summary.MaxTokens != 0 {
		t.Errorf("unset summary numbers should be zero, got %+v", cfg.Summary)
	}
}

// The paste thresholds read back from the file, and a negative survives:
// it is the answer "never on this count" rather than a number to floor.
func TestSet_PasteThresholds(t *testing.T) {
	var cfg Config
	for key, want := range map[string]int{
		"appearance.paste_lines":   40,
		"appearance.paste_columns": -1,
	} {
		if err := Set(&cfg, key, strconv.Itoa(want)); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	if cfg.Appearance.PasteLines != 40 {
		t.Errorf("appearance.paste_lines = %d, want 40", cfg.Appearance.PasteLines)
	}
	if cfg.Appearance.PasteColumns != -1 {
		t.Errorf("appearance.paste_columns = %d, want -1", cfg.Appearance.PasteColumns)
	}
	// Resetting a row hands Set an empty value; it has to read back as unset
	// rather than as a parse failure that leaves the old number standing.
	if err := Set(&cfg, "appearance.paste_lines", ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.PasteLines != 0 {
		t.Errorf("a reset left appearance.paste_lines = %d", cfg.Appearance.PasteLines)
	}
}

func TestLoadFrom_PasteThresholds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[appearance]\npaste_lines = 25\npaste_columns = 400\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.PasteLines != 25 || cfg.Appearance.PasteColumns != 400 {
		t.Fatalf("paste thresholds = %d/%d, want 25/400",
			cfg.Appearance.PasteLines, cfg.Appearance.PasteColumns)
	}
}
