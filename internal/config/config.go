package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider   ProviderConfig   `toml:"provider"`
	Behavior   BehaviorConfig   `toml:"behavior"`
	Appearance AppearanceConfig `toml:"appearance"`
}

type ProviderConfig struct {
	Default        string         `toml:"default"`
	Model          string         `toml:"model"`
	OpenAI         ProviderDetail `toml:"openai"`
	Gemini         ProviderDetail `toml:"gemini"`
	OpenRouter     ProviderDetail `toml:"openrouter"`
	OpenAICompat   ProviderDetail `toml:"openai_compatible"`
}

type ProviderDetail struct {
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
	BaseURL string `toml:"base_url"`
}

type BehaviorConfig struct {
	SilentMode bool   `toml:"silent_mode"`
	Shell      string `toml:"shell"`
}

type AppearanceConfig struct {
	AccentColor string `toml:"accent_color"`
}

// ProviderAPIKey returns the API key for the named provider, or empty string.
func (c Config) ProviderAPIKey(name string) string {
	switch name {
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
	switch name {
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

// ProviderBaseURL returns the base URL for the named provider, or empty string.
func (c Config) ProviderBaseURL(name string) string {
	switch name {
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
