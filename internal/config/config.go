package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider   ProviderConfig   `toml:"provider"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Appearance AppearanceConfig `toml:"appearance"`
	History    HistoryConfig    `toml:"history"`
}

type ProviderConfig struct {
	Default string `toml:"default"`
	Model   string `toml:"model"`
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Name    string `toml:"name"`
}

type BehaviorConfig struct {
	SilentMode       bool   `toml:"silent_mode"`
	Shell            string `toml:"shell"`
	ContextMaxTokens int    `toml:"context_max_tokens"`
	SafetyWarnings   *bool  `toml:"safety_warnings"`
	SystemPromptExtra string `toml:"system_prompt_extra"`
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
	case "behavior.safety_warnings":
		v := value == "true"
		cfg.Behavior.SafetyWarnings = &v
	case "behavior.system_prompt_extra":
		cfg.Behavior.SystemPromptExtra = value
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
