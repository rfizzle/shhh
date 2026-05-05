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
	if r.Model != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %q", r.Model)
	}
}

func TestResolve_ConfigOverridesDefaults(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		ConfigProvider: "gemini",
		ConfigModel:    "gemini-2.5-flash",
	})
	if r.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", r.Provider)
	}
	if r.Model != "gemini-2.5-flash" {
		t.Errorf("expected model 'gemini-2.5-flash', got %q", r.Model)
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
		ConfigProvider: "gemini",
		ConfigModel:    "gemini-2.5-flash",
	})
	if r.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", r.Provider)
	}
	if r.Model != "gpt-4o-mini" {
		t.Errorf("expected model 'gpt-4o-mini', got %q", r.Model)
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
	if r.Model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("expected default openrouter model, got %q", r.Model)
	}
}

func TestResolve_DefaultModelMatchesProvider(t *testing.T) {
	clearEnv(t)

	tests := []struct {
		provider string
		model    string
	}{
		{"openai", "gpt-4o"},
		{"gemini", "gemini-2.5-flash"},
		{"openrouter", "anthropic/claude-sonnet-4-6"},
		{"openai-compatible", "llama3"},
	}
	for _, tt := range tests {
		r := Resolve(Opts{ConfigProvider: tt.provider})
		if r.Model != tt.model {
			t.Errorf("provider %q: expected model %q, got %q", tt.provider, tt.model, r.Model)
		}
	}
}

func TestResolve_ConfigModelUsed(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		ConfigModel: "gpt-4o",
	})
	if r.Model != "gpt-4o" {
		t.Errorf("expected config model 'gpt-4o', got %q", r.Model)
	}
}

func TestResolve_FlagModelOverridesConfigModel(t *testing.T) {
	clearEnv(t)

	r := Resolve(Opts{
		FlagModel:   "gpt-4o-mini",
		ConfigModel: "gpt-4o",
	})
	if r.Model != "gpt-4o-mini" {
		t.Errorf("expected flag model 'gpt-4o-mini', got %q", r.Model)
	}
}
