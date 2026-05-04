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
	Register("stub", func() (Provider, error) {
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

func TestAvailable(t *testing.T) {
	Register("a", func() (Provider, error) { return &stubProvider{}, nil })
	Register("b", func() (Provider, error) { return &stubProvider{}, nil })
	t.Cleanup(func() { delete(registry, "a"); delete(registry, "b") })

	names := Available()
	if len(names) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(names))
	}
}
