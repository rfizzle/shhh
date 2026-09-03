package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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
		{"agents.profiles.researcher.model", "tiny"},
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

// Which surfaces take readings is the reader's to decide, because the cost is
// per agent. The defaults differ because the arithmetic does: a headless run
// is one agent, a fan-out is as many as it is wide.
func TestSummarySurfaces_Defaults(t *testing.T) {
	var c Config
	if !c.HeadlessSummaryEnabled() {
		t.Error("a headless run has nobody watching it; readings default on")
	}
	if c.SubagentSummaryEnabled() {
		t.Error("a fan-out multiplies the cost by its width; readings default off")
	}
}

func TestSummarySurfaces_Overrides(t *testing.T) {
	off, on := false, true
	c := Config{Summary: SummaryConfig{Headless: &off, Subagents: &on}}
	if c.HeadlessSummaryEnabled() {
		t.Error("summary.headless=false should turn headless readings off")
	}
	if !c.SubagentSummaryEnabled() {
		t.Error("summary.subagents=true should turn child readings on")
	}
}

// summary.disabled is the master switch: it turns the mechanism off
// everywhere, whatever a per-surface key says.
func TestSummarySurfaces_DisabledBeatsEverySurface(t *testing.T) {
	on := true
	c := Config{Summary: SummaryConfig{Disabled: true, Headless: &on, Subagents: &on}}
	if c.HeadlessSummaryEnabled() || c.SubagentSummaryEnabled() {
		t.Error("summary.disabled must turn readings off everywhere")
	}
}

func TestSummarySurfaces_SetByKey(t *testing.T) {
	var c Config
	for key, check := range map[string]func() bool{
		"summary.headless":  func() bool { return c.HeadlessSummaryEnabled() },
		"summary.subagents": func() bool { return c.SubagentSummaryEnabled() },
	} {
		if err := Set(&c, key, "true"); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		if !check() {
			t.Errorf("%s=true did not take", key)
		}
		if err := Set(&c, key, "false"); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		if check() {
			t.Errorf("%s=false did not take", key)
		}
	}
}

func TestCommandTimeoutDefaultsAndOverrides(t *testing.T) {
	var cfg Config
	if got := cfg.CommandTimeout(); got != DefaultCommandTimeout {
		t.Errorf("an unset ceiling keeps the default: want %v got %v", DefaultCommandTimeout, got)
	}

	cfg.Behavior.CommandTimeoutSeconds = 90
	if got := cfg.CommandTimeout(); got != 90*time.Second {
		t.Errorf("a stated ceiling wins: want 90s got %v", got)
	}

	// The escape for a machine whose builds really do run for hours.
	cfg.Behavior.CommandTimeoutSeconds = -1
	if got := cfg.CommandTimeout(); got != 0 {
		t.Errorf("a negative removes the ceiling: want 0 got %v", got)
	}
}

// The interruption machinery's thresholds and wordings are reachable from
// the file, and a negative is kept: it is how the file says the interval
// never widens and the steer quotes the instruction whole.
func TestSet_SteeringConfig(t *testing.T) {
	var cfg Config
	for key, want := range map[string]int{
		"behavior.check_in_interval_rounds":    15,
		"behavior.check_in_max_doublings":      -1,
		"summary.intervene_cooldown_intervals": 3,
		"summary.steer_target_chars":           -1,
	} {
		if err := Set(&cfg, key, strconv.Itoa(want)); err != nil {
			t.Fatalf("%s: unexpected error: %v", key, err)
		}
	}
	if cfg.Behavior.CheckInIntervalRounds != 15 || cfg.Behavior.CheckInMaxDoublings != -1 {
		t.Errorf("check-in keys = %d/%d, want 15/-1",
			cfg.Behavior.CheckInIntervalRounds, cfg.Behavior.CheckInMaxDoublings)
	}
	if cfg.Summary.InterveneCooldownIntervals != 3 || cfg.Summary.SteerTargetChars != -1 {
		t.Errorf("steer keys = %d/%d, want 3/-1",
			cfg.Summary.InterveneCooldownIntervals, cfg.Summary.SteerTargetChars)
	}

	for key, into := range map[string]*string{
		"prompts.steer":      &cfg.Prompts.Steer,
		"prompts.check_in":   &cfg.Prompts.CheckIn,
		"prompts.summary":    &cfg.Prompts.Summary,
		"prompts.classifier": &cfg.Prompts.Classifier,
	} {
		if err := Set(&cfg, key, "/wordings/"+key); err != nil {
			t.Fatalf("%s: unexpected error: %v", key, err)
		}
		if *into != "/wordings/"+key {
			t.Errorf("%s = %q", key, *into)
		}
	}
}

// The same block read back from a file, so a key that decodes under a
// different name than it is set by cannot pass the test above.
func TestLoadFrom_SteeringConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[behavior]
check_in_interval_rounds = 15
check_in_max_doublings = -1

[summary]
intervene_cooldown_intervals = 3
steer_target_chars = 120

[prompts]
steer = "steer.md"
check_in = "checkin.md"
summary = "summary.md"
classifier = "classifier.md"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Behavior.CheckInIntervalRounds != 15 || cfg.Behavior.CheckInMaxDoublings != -1 {
		t.Errorf("check-in keys = %d/%d", cfg.Behavior.CheckInIntervalRounds, cfg.Behavior.CheckInMaxDoublings)
	}
	if cfg.Summary.InterveneCooldownIntervals != 3 || cfg.Summary.SteerTargetChars != 120 {
		t.Errorf("steer keys = %d/%d", cfg.Summary.InterveneCooldownIntervals, cfg.Summary.SteerTargetChars)
	}
	want := PromptsConfig{Steer: "steer.md", CheckIn: "checkin.md", Summary: "summary.md", Classifier: "classifier.md"}
	if cfg.Prompts != want {
		t.Errorf("prompts = %+v, want %+v", cfg.Prompts, want)
	}
}

// The retry bound is a count whose unset is not its zero: a file that says
// nothing keeps the built-in bound, and a file that says nought is asking
// for a failure to stand rather than be waited out.
func TestSet_ProviderRetriesKeepsUnsetApartFromNone(t *testing.T) {
	var cfg Config
	if cfg.Behavior.ProviderRetries != nil {
		t.Fatalf("a config nobody has written to names no bound, got %v", *cfg.Behavior.ProviderRetries)
	}
	for _, tc := range []struct {
		value string
		want  *int
	}{
		{"5", intp(5)},
		{"0", intp(0)},
		{"", nil},
	} {
		if err := Set(&cfg, "behavior.provider_retries", tc.value); err != nil {
			t.Fatalf("Set(behavior.provider_retries, %q): %v", tc.value, err)
		}
		got := cfg.Behavior.ProviderRetries
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%q left %d set, want unset", tc.value, *got)
		case tc.want != nil && got == nil:
			t.Errorf("%q left the key unset, want %d", tc.value, *tc.want)
		case tc.want != nil && *got != *tc.want:
			t.Errorf("%q = %d, want %d", tc.value, *got, *tc.want)
		}
	}
	// Fewer than none is not a bound, and neither is a word.
	for _, value := range []string{"-1", "twice"} {
		if err := Set(&cfg, "behavior.provider_retries", value); err == nil {
			t.Errorf("Set(behavior.provider_retries, %q) should be refused", value)
		}
	}
}

// The same key read back from a file, so a bound of none survives the round
// trip that a count whose zero meant unset would lose.
func TestLoadFrom_ProviderRetriesOfNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[behavior]\nprovider_retries = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Behavior.ProviderRetries == nil || *cfg.Behavior.ProviderRetries != 0 {
		t.Errorf("provider_retries = %v, want a bound of none", cfg.Behavior.ProviderRetries)
	}
}

func intp(n int) *int { return &n }

func TestTreeCheckIsOnUnlessTurnedOff(t *testing.T) {
	var cfg Config
	if !cfg.TreeCheckEnabled() {
		t.Fatal("unset is on")
	}
	if err := Set(&cfg, "behavior.tree_check", "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.TreeCheckEnabled() {
		t.Error("behavior.tree_check=false should turn the reading off")
	}
	if err := Set(&cfg, "behavior.tree_check", "true"); err != nil {
		t.Fatal(err)
	}
	if !cfg.TreeCheckEnabled() {
		t.Error("behavior.tree_check=true should turn it back on")
	}
}

// A value that is not the shape its key takes is refused, naming the key and
// what it wanted, and the setting is left as it was. Every one of these
// wrote a plausible wrong answer once: `abc` on a retention key wrote zero,
// which at the next startup is "keep nothing".
func TestSet_RefusesAValueThatIsNotTheKeysShape(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"history.retention_days", "abc", "a whole number"},
		{"web.fetch_max_bytes", "2mb", "a whole number"},
		{"agents.max_concurrent", "-1", "a whole number, zero or above"},
		{"behavior.silent_mode", "yes", "true or false"},
		{"summary.disabled", "on", "true or false"},
		{"appearance.mouse", "yes", "true or false"},
		{"behavior.tree_check", "off", "true or false"},
	} {
		cfg := Config{}
		err := Set(&cfg, tc.key, tc.value)
		if err == nil {
			t.Fatalf("Set(%q, %q) should be refused", tc.key, tc.value)
		}
		if msg := err.Error(); !strings.Contains(msg, tc.key) || !strings.Contains(msg, tc.want) {
			t.Errorf("Set(%q, %q) = %q, want the key and %q", tc.key, tc.value, msg, tc.want)
		}
		if !reflect.DeepEqual(cfg, Config{}) {
			t.Errorf("Set(%q, %q) wrote something: %+v", tc.key, tc.value, cfg)
		}
	}
}

// A boolean takes the spellings Go reads and no others, so a key set to a
// word the parser does not know is a refusal rather than a silent false.
func TestSet_BooleansTakeGosSpellings(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"true", true}, {"True", true}, {"TRUE", true}, {"t", true}, {"1", true},
		{"false", false}, {"False", false}, {"f", false}, {"0", false}} {
		var cfg Config
		if err := Set(&cfg, "summary.disabled", tc.value); err != nil {
			t.Fatalf("Set(summary.disabled, %q): %v", tc.value, err)
		}
		if cfg.Summary.Disabled != tc.want {
			t.Errorf("summary.disabled = %v for %q, want %v", cfg.Summary.Disabled, tc.value, tc.want)
		}
	}
}

// A negative is a value on the keys whose own comment says what it means and
// a refusal on the rest, where it would write a ceiling nothing satisfies.
func TestSet_ANegativeIsAValueOnlyWhereItMeansSomething(t *testing.T) {
	for _, key := range []string{
		"behavior.max_tool_rounds", "behavior.command_timeout_seconds",
		"behavior.check_in_max_doublings", "summary.steer_target_chars",
		"appearance.paste_lines", "appearance.paste_columns",
	} {
		var cfg Config
		if err := Set(&cfg, key, "-1"); err != nil {
			t.Errorf("Set(%q, -1): %v", key, err)
		}
	}
	for _, key := range []string{
		"history.retention_days", "reports.retention_days",
		"behavior.context_max_tokens", "summary.max_tokens",
		"lsp.request_timeout_seconds", "sandbox.container_pids",
	} {
		var cfg Config
		if err := Set(&cfg, key, "-1"); err == nil {
			t.Errorf("Set(%q, -1) should be refused", key)
		}
	}
}

// An empty value is a reset rather than an answer: the setting goes back to
// its zero, and for a tri-state key that is unset rather than false.
func TestSet_AnEmptyValueResets(t *testing.T) {
	cfg := Config{}
	cfg.History.RetentionDays = 30
	on := true
	cfg.Appearance.Mouse = &on
	cfg.Summary.Disabled = true
	for _, key := range []string{"history.retention_days", "appearance.mouse", "summary.disabled"} {
		if err := Set(&cfg, key, ""); err != nil {
			t.Fatalf("Set(%q, \"\"): %v", key, err)
		}
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("a reset leaves the zero value: %+v", cfg)
	}
}

// A role's model is the key's own segment, so a role nobody wrote a case
// for is as settable as the three that are built in.
func TestSet_AnyRoleModelRoundTrips(t *testing.T) {
	var cfg Config
	for _, role := range []string{"reviewer", "archaeologist"} {
		if err := Set(&cfg, "agents.profiles."+role+".model", "claude-haiku-4-5"); err != nil {
			t.Fatal(err)
		}
		if got := cfg.AgentModel(role, "session-model"); got != "claude-haiku-4-5" {
			t.Fatalf("%s model = %q", role, got)
		}
	}
}

// The spelling the role models used to take says what to type instead. It is
// the one answer a person who has the setting can act on: "unknown key" would
// send them looking for a feature that is still there.
func TestSet_TheOldRoleModelSpellingNamesTheNewKey(t *testing.T) {
	var cfg Config
	err := Set(&cfg, "agents.reviewer_model", "claude-haiku-4-5")
	if err == nil || !strings.Contains(err.Error(), "agents.profiles.reviewer.model") {
		t.Fatalf("the old spelling should name the new key, got %v", err)
	}
}

// A key no setting reads is refused with the nearest one, so a slipped
// letter is a sentence to act on rather than a listing to search.
func TestSet_AnUnknownKeyNamesTheNearest(t *testing.T) {
	var cfg Config
	err := Set(&cfg, "behaviour.shell", "/bin/zsh")
	if err == nil || !strings.Contains(err.Error(), "behavior.shell") {
		t.Fatalf("an unknown key should offer the nearest, got %v", err)
	}
}

// The lifetime of the repeated opening reads back off the file and through
// the write door, which is the pair every key needs: a file that no setting
// reads is refused, and a write nothing reads back is a key that saves and
// does nothing.
func TestProviderCacheTTL_LoadsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[provider]\ncache_ttl = \"5m\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ProviderCacheTTL() != "5m" {
		t.Errorf("provider.cache_ttl = %q, want %q", cfg.ProviderCacheTTL(), "5m")
	}

	if err := Write(path, Edit{Key: "provider.cache_ttl", Value: "1h"}); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadFrom(path); err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderCacheTTL() != "1h" {
		t.Errorf("after the write, provider.cache_ttl = %q", cfg.ProviderCacheTTL())
	}
}

// The inspector rail's width reads back off the file and through the write
// door, which is the pair every key needs. It is a string because its value
// is a word or a number and the surface that owns the rail decides which;
// what config holds is the line, unread.
func TestAppearanceRailWidth_LoadsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[appearance]\nrail_width = \"60\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Appearance.RailWidth != "60" {
		t.Errorf("appearance.rail_width = %q, want %q", cfg.Appearance.RailWidth, "60")
	}

	if err := Write(path, Edit{Key: "appearance.rail_width", Value: "auto"}); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadFrom(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.RailWidth != "auto" {
		t.Errorf("after the write, appearance.rail_width = %q", cfg.Appearance.RailWidth)
	}
}

// A backlog run commits unless the file says otherwise, so an unset key and
// one that says true read the same and only false turns it off. The key
// reads back off the file and through the write door, which is the pair
// every key needs.
func TestTodoCommitIsOnUnlessTurnedOff(t *testing.T) {
	var cfg Config
	if !cfg.TodoCommitEnabled() {
		t.Fatal("an unset todo.commit should be a commit")
	}
	if err := Set(&cfg, "todo.commit", "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.TodoCommitEnabled() {
		t.Error("todo.commit=false should end a run without one")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := Write(path, Edit{Key: "todo.commit", Value: "false"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TodoCommitEnabled() {
		t.Errorf("after the write, todo.commit reads back on: %+v", loaded.Todo)
	}
	if err := Write(path, Edit{Key: "todo.commit", Value: ""}); err != nil {
		t.Fatal(err)
	}
	if loaded, err = LoadFrom(path); err != nil {
		t.Fatal(err)
	}
	if !loaded.TodoCommitEnabled() {
		t.Error("a reset should put the key back to its default, which is a commit")
	}
}

// A file that names a variable is read through that variable, ahead of a
// literal key the same file still holds. The two spellings overlap only while
// somebody is moving between them, and the move has to change what is in
// force the moment the name is written — otherwise the way to find out which
// one answered is to change the key and see whether anything breaks.
func TestProviderAPIKey_TheNamedVariableOutranksTheLiteral(t *testing.T) {
	t.Setenv("SHHH_TEST_PROVIDER_KEY", "sk-from-the-environment")
	cfg := Config{Provider: ProviderConfig{
		APIKey:    "sk-from-the-file",
		APIKeyEnv: "SHHH_TEST_PROVIDER_KEY",
	}}
	if got := cfg.ProviderAPIKey(); got != "sk-from-the-environment" {
		t.Fatalf("the named variable did not outrank the literal, got %q", got)
	}

	cfg.Provider.APIKeyEnv = "SHHH_TEST_PROVIDER_KEY_NOBODY_EXPORTED"
	if got := cfg.ProviderAPIKey(); got != "" {
		t.Fatalf("a variable nobody exported fell through to the literal, got %q", got)
	}

	cfg.Provider.APIKeyEnv = ""
	if got := cfg.ProviderAPIKey(); got != "sk-from-the-file" {
		t.Fatalf("a file naming no variable did not answer with its literal, got %q", got)
	}
}

// The search key resolves on the same terms, because a second rule for the
// second credential is a second thing to be wrong about.
func TestWebSearchAPIKey_TheNamedVariableOutranksTheLiteral(t *testing.T) {
	t.Setenv("SHHH_TEST_SEARCH_KEY", "brave-from-the-environment")
	cfg := Config{Web: WebConfig{
		SearchAPIKey:    "brave-from-the-file",
		SearchAPIKeyEnv: "SHHH_TEST_SEARCH_KEY",
	}}
	if got := cfg.WebSearchAPIKey(); got != "brave-from-the-environment" {
		t.Fatalf("the named variable did not outrank the literal, got %q", got)
	}
	cfg.Web.SearchAPIKeyEnv = ""
	if got := cfg.WebSearchAPIKey(); got != "brave-from-the-file" {
		t.Fatalf("a file naming no variable did not answer with its literal, got %q", got)
	}
}

// A credential's companion is found by the convention rather than by a list,
// which is what stops a credential added later from being one the surfaces
// silently have nothing to say about. A key that is not a credential has
// none, and neither does a credential the file offers no name for.
func TestSetting_EnvKeyIsTheCompanionKey(t *testing.T) {
	for key, want := range map[string]string{
		"provider.api_key":     "provider.api_key_env",
		"web.search_api_key":   "web.search_api_key_env",
		"provider.model":       "",
		"provider.api_key_env": "",
	} {
		s, ok := Lookup(key)
		if !ok {
			t.Fatalf("%s is not a setting", key)
		}
		if got := s.EnvKey(); got != want {
			t.Errorf("%s companion is %q, want %q", key, got, want)
		}
	}
}

// "Set" means the same thing everywhere it is said, and a variable exported
// empty is not a key: a surface that called it set would be promising a
// session that will fail to start.
func TestEnvVarSet_AnEmptyExportIsNotAKey(t *testing.T) {
	t.Setenv("SHHH_TEST_EMPTY_VAR", "   ")
	t.Setenv("SHHH_TEST_FULL_VAR", "sk-something")
	if EnvVarSet("SHHH_TEST_EMPTY_VAR") {
		t.Error("a variable exported empty reads as set")
	}
	if !EnvVarSet("SHHH_TEST_FULL_VAR") {
		t.Error("a variable holding a key reads as unset")
	}
	if EnvVarSet("") {
		t.Error("naming no variable reads as set")
	}
}
