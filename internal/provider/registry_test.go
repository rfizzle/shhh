package provider

import (
	"context"
	"testing"
)

type stubProvider struct{}

func (s *stubProvider) StreamCompletion(_ context.Context, _ []Message, _ CompletionOpts) (<-chan StreamEvent, error) {
	return nil, nil
}

func (s *stubProvider) Name() string { return "stub" }

func TestResolve_Registered(t *testing.T) {
	Register("stub", func(ResolveOpts) (Provider, error) {
		return &stubProvider{}, nil
	})
	t.Cleanup(func() { delete(registry, "stub") })

	p, err := Resolve("stub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "stub" {
		t.Errorf("expected name 'stub', got %q", p.Name())
	}
}

func TestResolve_Unknown(t *testing.T) {
	_, err := Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolve_UnderscoreNormalization(t *testing.T) {
	Register("my-provider", func(ResolveOpts) (Provider, error) {
		return &stubProvider{}, nil
	})
	t.Cleanup(func() { delete(registry, "my-provider") })

	p, err := Resolve("my_provider")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "stub" {
		t.Errorf("expected name 'stub', got %q", p.Name())
	}
}

func TestAvailable(t *testing.T) {
	Register("a", func(ResolveOpts) (Provider, error) { return &stubProvider{}, nil })
	Register("b", func(ResolveOpts) (Provider, error) { return &stubProvider{}, nil })
	t.Cleanup(func() { delete(registry, "a"); delete(registry, "b") })

	names := Available()
	if len(names) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(names))
	}
}

// Every provider whose catalog can be known ahead of time names a small
// model for the bounded calls, and the one whose catalog cannot does not:
// openai-compatible points at whatever endpoint the user runs, and a name
// guessed for it is a request that 404s.
func TestDefaults_CheapModel(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "openai-responses", "gemini", "openrouter"} {
		d := Defaults(name)
		if d.CheapModel == "" {
			t.Errorf("%s names no cheap model", name)
			continue
		}
		if d.CheapModel == d.Model {
			t.Errorf("%s: the cheap model is the default model", name)
		}
		if !CapabilitiesFor(d.CheapModel).Known {
			t.Errorf("%s: nothing describes %q", name, d.CheapModel)
		}
	}
	if got := Defaults("openai-compatible").CheapModel; got != "" {
		t.Errorf("openai-compatible should name none, got %q", got)
	}
	// The underscore spelling reaches the same entry the model does.
	if Defaults("openai_responses").CheapModel != Defaults("openai-responses").CheapModel {
		t.Error("the name should normalise the same way for both fields")
	}
}
