package resolve

import (
	"os"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SHHH_PROVIDER", "SHHH_MODEL"} {
		orig := os.Getenv(key)
		t.Cleanup(func() { os.Setenv(key, orig) })
		os.Unsetenv(key)
	}
}

func TestResolve_Defaults(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{})
	if r.Provider != DefaultProvider {
		t.Errorf("expected provider %q, got %q", DefaultProvider, r.Provider)
	}
	if r.Model != DefaultModel {
		t.Errorf("expected model %q, got %q", DefaultModel, r.Model)
	}
	if r.APIKey != "" {
		t.Errorf("expected empty API key, got %q", r.APIKey)
	}
}

func TestResolve_ConfigOverridesDefaults(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		ConfigProvider: "gemini",
		ConfigModel:    "gemini-2.5-flash",
		ConfigAPIKey:   "ai-config-key",
	})
	if r.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", r.Provider)
	}
	if r.Model != "gemini-2.5-flash" {
		t.Errorf("expected model 'gemini-2.5-flash', got %q", r.Model)
	}
	if r.APIKey != "ai-config-key" {
		t.Errorf("expected API key 'ai-config-key', got %q", r.APIKey)
	}
}

func TestResolve_EnvOverridesConfig(t *testing.T) {
	clearEnv(t)
	os.Setenv("SHHH_PROVIDER", "openai-compatible")
	os.Setenv("SHHH_MODEL", "llama3")

	r := Resolve(Opts{
		ConfigProvider: "gemini",
		ConfigModel:    "gemini-2.5-flash",
	})
	if r.Provider != "openai-compatible" {
		t.Errorf("expected provider 'openai-compatible', got %q", r.Provider)
	}
	if r.Model != "llama3" {
		t.Errorf("expected model 'llama3', got %q", r.Model)
	}
}

func TestResolve_EnvOverridesDefaults(t *testing.T) {
	clearEnv(t)
	os.Setenv("SHHH_PROVIDER", "openai-compatible")
	os.Setenv("SHHH_MODEL", "llama3")

	r := Resolve(Opts{})
	if r.Provider != "openai-compatible" {
		t.Errorf("expected provider 'openai-compatible', got %q", r.Provider)
	}
	if r.Model != "llama3" {
		t.Errorf("expected model 'llama3', got %q", r.Model)
	}
}

func TestResolve_FlagsOverrideEnv(t *testing.T) {
	clearEnv(t)
	os.Setenv("SHHH_PROVIDER", "openai-compatible")
	os.Setenv("SHHH_MODEL", "llama3")

	r := Resolve(Opts{
		FlagProvider: "openai",
		FlagModel:    "gpt-4o-mini",
	})
	if r.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", r.Provider)
	}
	if r.Model != "gpt-4o-mini" {
		t.Errorf("expected model 'gpt-4o-mini', got %q", r.Model)
	}
}

func TestResolve_FlagsOverrideConfig(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		FlagProvider:   "openai",
		FlagModel:      "gpt-4o-mini",
		FlagAPIKey:     "sk-flag",
		ConfigProvider: "gemini",
		ConfigModel:    "gemini-2.5-flash",
		ConfigAPIKey:   "ai-config",
	})
	if r.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", r.Provider)
	}
	if r.Model != "gpt-4o-mini" {
		t.Errorf("expected model 'gpt-4o-mini', got %q", r.Model)
	}
	if r.APIKey != "sk-flag" {
		t.Errorf("expected API key 'sk-flag', got %q", r.APIKey)
	}
}

func TestResolve_FlagAPIKey(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{FlagAPIKey: "sk-test-123"})
	if r.APIKey != "sk-test-123" {
		t.Errorf("expected API key 'sk-test-123', got %q", r.APIKey)
	}
}

func TestResolve_PartialFlags(t *testing.T) {
	clearEnv(t)
	os.Setenv("SHHH_MODEL", "gemini-pro")

	r := Resolve(Opts{FlagProvider: "gemini"})
	if r.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", r.Provider)
	}
	if r.Model != "gemini-pro" {
		t.Errorf("expected model 'gemini-pro' from env, got %q", r.Model)
	}
}

func TestResolve_EmptyFlagsFallThrough(t *testing.T) {
	clearEnv(t)
	os.Setenv("SHHH_PROVIDER", "openrouter")

	r := Resolve(Opts{FlagProvider: "", FlagModel: ""})
	if r.Provider != "openrouter" {
		t.Errorf("expected provider 'openrouter' from env, got %q", r.Provider)
	}
	if r.Model != DefaultModel {
		t.Errorf("expected default model, got %q", r.Model)
	}
}

func TestResolve_ProviderModelOverridesGlobalModel(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		ConfigProvider:      "gemini",
		ConfigModel:         "gpt-4o",
		ConfigProviderModel: "gemini-2.5-pro",
	})
	if r.Model != "gemini-2.5-pro" {
		t.Errorf("expected per-provider model 'gemini-2.5-pro', got %q", r.Model)
	}
}

func TestResolve_FlagModelOverridesProviderModel(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		FlagModel:           "gpt-4o-mini",
		ConfigProviderModel: "gemini-2.5-pro",
		ConfigModel:         "gpt-4o",
	})
	if r.Model != "gpt-4o-mini" {
		t.Errorf("expected flag model 'gpt-4o-mini', got %q", r.Model)
	}
}

func TestResolve_GlobalModelUsedWhenNoProviderModel(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		ConfigModel: "gpt-4o",
	})
	if r.Model != "gpt-4o" {
		t.Errorf("expected global config model 'gpt-4o', got %q", r.Model)
	}
}

func TestResolve_ConfigAPIKeyUsedWhenNoFlag(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{ConfigAPIKey: "cfg-key"})
	if r.APIKey != "cfg-key" {
		t.Errorf("expected API key 'cfg-key', got %q", r.APIKey)
	}
}
