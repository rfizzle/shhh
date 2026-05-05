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
	Appearance AppearanceConfig `toml:"appearance"`
}

type ProviderConfig struct {
	Default      string         `toml:"default"`
	Model        string         `toml:"model"`
	OpenAI       ProviderDetail `toml:"openai"`
	Gemini       ProviderDetail `toml:"gemini"`
	OpenRouter   ProviderDetail `toml:"openrouter"`
	OpenAICompat ProviderDetail `toml:"openai_compatible"`
}

type ProviderDetail struct {
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
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

func normalizeProvider(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

// ProviderAPIKey returns the API key for the named provider, or empty string.
func (c Config) ProviderAPIKey(name string) string {
	switch normalizeProvider(name) {
	case "openai":
		return c.Provider.OpenAI.APIKey
	case "gemini":
		return c.Provider.Gemini.APIKey
	case "openrouter":
		return c.Provider.OpenRouter.APIKey
	case "openai-compatible":
		return c.Provider.OpenAICompat.APIKey
	}
	return ""
}

// ProviderModel returns the per-provider model override, or empty string.
func (c Config) ProviderModel(name string) string {
	switch normalizeProvider(name) {
	case "openai":
		return c.Provider.OpenAI.Model
	case "gemini":
		return c.Provider.Gemini.Model
	case "openrouter":
		return c.Provider.OpenRouter.Model
	case "openai-compatible":
		return c.Provider.OpenAICompat.Model
	}
	return ""
}

// ProviderName returns the custom display name for the named provider, or empty string.
func (c Config) ProviderName(name string) string {
	switch normalizeProvider(name) {
	case "openai-compatible":
		return c.Provider.OpenAICompat.Name
	}
	return ""
}

// ProviderBaseURL returns the base URL for the named provider, or empty string.
func (c Config) ProviderBaseURL(name string) string {
	switch normalizeProvider(name) {
	case "openrouter":
		return c.Provider.OpenRouter.BaseURL
	case "openai-compatible":
		return c.Provider.OpenAICompat.BaseURL
	}
	return ""
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
	case "provider.openai.api_key":
		cfg.Provider.OpenAI.APIKey = value
	case "provider.openai.model":
		cfg.Provider.OpenAI.Model = value
	case "provider.gemini.api_key":
		cfg.Provider.Gemini.APIKey = value
	case "provider.gemini.model":
		cfg.Provider.Gemini.Model = value
	case "provider.openrouter.api_key":
		cfg.Provider.OpenRouter.APIKey = value
	case "provider.openrouter.model":
		cfg.Provider.OpenRouter.Model = value
	case "provider.openai_compatible.api_key":
		cfg.Provider.OpenAICompat.APIKey = value
	case "provider.openai_compatible.model":
		cfg.Provider.OpenAICompat.Model = value
	case "provider.openai_compatible.base_url":
		cfg.Provider.OpenAICompat.BaseURL = value
	case "provider.openai_compatible.name":
		cfg.Provider.OpenAICompat.Name = value
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
