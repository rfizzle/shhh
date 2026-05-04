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
