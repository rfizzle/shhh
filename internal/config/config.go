package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider   ProviderConfig   `toml:"provider"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Sandbox    SandboxConfig    `toml:"sandbox"`
	Appearance AppearanceConfig `toml:"appearance"`
	History    HistoryConfig    `toml:"history"`
}

// SandboxConfig tunes process containment for agent-executed commands
// (S-062). The built-in deny mask (~/.ssh, ~/.aws, ~/.config/gh, shhh's own
// config and state dirs) is deliberately not configurable — it cannot be
// disabled, only extended.
type SandboxConfig struct {
	// Profile is "workspace" (network preserved, the default) or
	// "workspace-netless".
	Profile string `toml:"profile"`
	// DenyExtra paths join the built-in deny mask; contained commands see
	// them as empty.
	DenyExtra []string `toml:"deny_extra"`
	// WriteExtra paths are writable inside containment, in addition to the
	// workspace, scratch, and toolchain caches.
	WriteExtra []string `toml:"write_extra"`
}

type ProviderConfig struct {
	Default string `toml:"default"`
	Model   string `toml:"model"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Name    string `toml:"name"`
}

type BehaviorConfig struct {
	SilentMode        bool   `toml:"silent_mode"`
	Shell             string `toml:"shell"`
	ContextMaxTokens  int    `toml:"context_max_tokens"`
	MaxToolRounds     int    `toml:"max_tool_rounds"`
	SafetyWarnings    *bool  `toml:"safety_warnings"`
	SystemPromptExtra string `toml:"system_prompt_extra"`
	// CommandAllowlist entries auto-approve matching agent commands in chat
	// sessions ("go test" approves "go test ./..."); safety-flagged commands
	// always prompt regardless. Empty (the default) means every command asks.
	CommandAllowlist []string `toml:"command_allowlist"`
	// DefaultMode is the permission mode agent sessions start in: "manual",
	// "accept-edits", "auto", or "plan". Empty means manual (everything
	// prompts).
	DefaultMode string `toml:"default_mode"`
	// ModeCycle overrides the Shift+Tab mode order (same names as
	// DefaultMode). Empty means manual → accept-edits → auto → plan.
	ModeCycle []string `toml:"mode_cycle"`
	// ClassifierModel is the model auto mode's permission classifier uses
	// (S-060). Empty means the session model.
	ClassifierModel string `toml:"classifier_model"`
	// ClassifierTimeoutSeconds bounds each classifier request (default 30).
	ClassifierTimeoutSeconds int `toml:"classifier_timeout_seconds"`
	// ClassifierMaxTokens caps the classifier's response (default 1024).
	ClassifierMaxTokens int `toml:"classifier_max_tokens"`
	// ClassifierRetries is how many extra attempts an invalid or failed
	// classifier response gets before failing closed (default 1).
	ClassifierRetries int `toml:"classifier_retries"`
}

type AppearanceConfig struct {
	AccentColor string `toml:"accent_color"`
}

type HistoryConfig struct {
	RetentionDays int `toml:"retention_days"`
}

const DefaultRetentionDays = 90

const DefaultContextMaxTokens = 8000

func (c Config) SafetyWarningsEnabled() bool {
	if c.Behavior.SafetyWarnings == nil {
		return true
	}
	return *c.Behavior.SafetyWarnings
}

func (c Config) EffectiveContextMaxTokens() int {
	if c.Behavior.ContextMaxTokens > 0 {
		return c.Behavior.ContextMaxTokens
	}
	return DefaultContextMaxTokens
}

func (c Config) EffectiveRetentionDays() int {
	if c.History.RetentionDays > 0 {
		return c.History.RetentionDays
	}
	return DefaultRetentionDays
}

// ProviderAPIKey returns the configured API key.
func (c Config) ProviderAPIKey() string {
	return c.Provider.APIKey
}

// ProviderBaseURL returns the configured base URL.
func (c Config) ProviderBaseURL() string {
	return c.Provider.BaseURL
}

// ProviderDisplayName returns the configured custom display name.
func (c Config) ProviderDisplayName() string {
	return c.Provider.Name
}

func Load() (Config, error) {
	return LoadFrom(Paths()...)
}

func LoadFrom(paths ...string) (Config, error) {
	var cfg Config
	for _, p := range paths {
		if _, err := toml.DecodeFile(p, &cfg); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Config{}, err
		}
		return cfg, nil
	}
	return Config{}, nil
}

func Set(cfg *Config, key, value string) error {
	switch key {
	case "provider.default":
		cfg.Provider.Default = value
	case "provider.model":
		cfg.Provider.Model = value
	case "provider.api_key":
		cfg.Provider.APIKey = value
	case "provider.base_url":
		cfg.Provider.BaseURL = value
	case "provider.name":
		cfg.Provider.Name = value
	case "behavior.silent_mode":
		cfg.Behavior.SilentMode = value == "true"
	case "behavior.shell":
		cfg.Behavior.Shell = value
	case "behavior.context_max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ContextMaxTokens = n
	case "behavior.max_tool_rounds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.MaxToolRounds = n
	case "behavior.safety_warnings":
		v := value == "true"
		cfg.Behavior.SafetyWarnings = &v
	case "behavior.system_prompt_extra":
		cfg.Behavior.SystemPromptExtra = value
	case "behavior.command_allowlist":
		cfg.Behavior.CommandAllowlist = splitList(value)
	case "behavior.default_mode":
		cfg.Behavior.DefaultMode = value
	case "behavior.mode_cycle":
		cfg.Behavior.ModeCycle = splitList(value)
	case "behavior.classifier_model":
		cfg.Behavior.ClassifierModel = value
	case "behavior.classifier_timeout_seconds":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierTimeoutSeconds = n
	case "behavior.classifier_max_tokens":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierMaxTokens = n
	case "behavior.classifier_retries":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.Behavior.ClassifierRetries = n
	case "sandbox.profile":
		cfg.Sandbox.Profile = value
	case "sandbox.deny_extra":
		cfg.Sandbox.DenyExtra = splitList(value)
	case "sandbox.write_extra":
		cfg.Sandbox.WriteExtra = splitList(value)
	case "appearance.accent_color":
		cfg.Appearance.AccentColor = value
	case "history.retention_days":
		n := 0
		fmt.Sscanf(value, "%d", &n)
		cfg.History.RetentionDays = n
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// splitList parses a comma-separated config value into its non-empty,
// trimmed entries; an empty value clears the list.
func splitList(value string) []string {
	var list []string
	for _, part := range strings.Split(value, ",") {
		if p := strings.TrimSpace(part); p != "" {
			list = append(list, p)
		}
	}
	return list
}

func Save(cfg Config) error {
	p := WritePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func WritePath() string {
	paths := Paths()
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shhh", "config.toml")
}

// Paths returns config file paths in search order (highest priority first).
func Paths() []string {
	var out []string
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, "Library", "Application Support", "shhh", "config.toml"))
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		out = append(out, filepath.Join(xdg, "shhh", "config.toml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "shhh", "config.toml"))
	}
	return out
}
