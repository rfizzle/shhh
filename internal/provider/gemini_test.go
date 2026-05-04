package provider

import (
	"errors"
	"os"
	"testing"
)

func TestGemini_Name(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	p, err := NewGemini(ResolveOpts{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected 'gemini', got %q", p.Name())
	}
}

func TestGemini_DefaultModel(t *testing.T) {
	p, err := NewGemini(ResolveOpts{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-flash" {
		t.Errorf("expected default model 'gemini-2.5-flash', got %q", p.model)
	}
}

func TestGemini_CustomModel(t *testing.T) {
	p, err := NewGemini(ResolveOpts{APIKey: "test-key", Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.model != "gemini-2.5-pro" {
		t.Errorf("expected model 'gemini-2.5-pro', got %q", p.model)
	}
}

func TestNewGemini_MissingKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	os.Unsetenv("GEMINI_API_KEY")
	_, err := NewGemini(ResolveOpts{})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewGemini_EnvKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-key")
	p, err := NewGemini(ResolveOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestToGeminiContents(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "you are helpful"},
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "hi there"},
		{Role: RoleUser, Content: "what's up"},
	}

	contents, sysInstruction := toGeminiContents(msgs)

	if sysInstruction == nil {
		t.Fatal("expected system instruction")
	}
	if sysInstruction.Parts[0].Text != "you are helpful" {
		t.Errorf("system instruction mismatch: %q", sysInstruction.Parts[0].Text)
	}

	if len(contents) != 3 {
		t.Fatalf("expected 3 content entries, got %d", len(contents))
	}
	if contents[0].Role != "user" || contents[0].Parts[0].Text != "hello" {
		t.Errorf("content 0 mismatch: %+v", contents[0])
	}
	if contents[1].Role != "model" || contents[1].Parts[0].Text != "hi there" {
		t.Errorf("content 1 mismatch: %+v", contents[1])
	}
	if contents[2].Role != "user" || contents[2].Parts[0].Text != "what's up" {
		t.Errorf("content 2 mismatch: %+v", contents[2])
	}
}

func TestToGeminiContents_NoSystem(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
	}

	contents, sysInstruction := toGeminiContents(msgs)

	if sysInstruction != nil {
		t.Error("expected nil system instruction")
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(contents))
	}
}

func TestClassifyGeminiError_Unauthorized(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 401: invalid key"))
	if !errors.Is(err, ErrGeminiUnauthorized) {
		t.Errorf("expected ErrGeminiUnauthorized, got: %v", err)
	}
}

func TestClassifyGeminiError_Forbidden(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 403: forbidden"))
	if !errors.Is(err, ErrGeminiUnauthorized) {
		t.Errorf("expected ErrGeminiUnauthorized, got: %v", err)
	}
}

func TestClassifyGeminiError_RateLimited(t *testing.T) {
	err := classifyGeminiError(errors.New("googleapi: Error 429: rate limit"))
	if !errors.Is(err, ErrGeminiRateLimited) {
		t.Errorf("expected ErrGeminiRateLimited, got: %v", err)
	}
}

func TestClassifyGeminiError_Generic(t *testing.T) {
	original := errors.New("some network error")
	got := classifyGeminiError(original)
	if got != original {
		t.Errorf("expected original error returned, got %v", got)
	}
}

func TestGemini_Registration(t *testing.T) {
	names := Available()
	found := false
	for _, n := range names {
		if n == "gemini" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'gemini' to be registered")
	}
}
