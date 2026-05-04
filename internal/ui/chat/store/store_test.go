package store

import (
	"path/filepath"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func setupTestDir(t *testing.T) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "chats")
	overrideDir = dir
	t.Cleanup(func() { overrideDir = "" })
}

func TestSaveAndLoad(t *testing.T) {
	setupTestDir(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi there"},
	}

	if err := Save("test-chat", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load("test-chat")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loaded))
	}
	if loaded[1].Content != "hello" {
		t.Fatalf("expected 'hello', got %q", loaded[1].Content)
	}
	if loaded[2].Content != "hi there" {
		t.Fatalf("expected 'hi there', got %q", loaded[2].Content)
	}
}

func TestLoad_NotFound(t *testing.T) {
	setupTestDir(t)

	_, err := Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent chat")
	}
}

func TestList_Empty(t *testing.T) {
	setupTestDir(t)

	entries, err := List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestList_WithSessions(t *testing.T) {
	setupTestDir(t)

	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1"},
		{Role: provider.RoleUser, Content: "q2"},
		{Role: provider.RoleAssistant, Content: "a2"},
	}

	if err := Save("chat-one", msgs); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if err := Save("chat-two", msgs[:3]); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	entries, err := List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
		if e.Name == "chat-one" && e.Turns != 2 {
			t.Fatalf("chat-one should have 2 turns, got %d", e.Turns)
		}
		if e.Name == "chat-two" && e.Turns != 1 {
			t.Fatalf("chat-two should have 1 turn, got %d", e.Turns)
		}
	}
	if !found["chat-one"] || !found["chat-two"] {
		t.Fatal("missing expected entries")
	}
}

func TestSave_OverwritePreservesCreatedAt(t *testing.T) {
	setupTestDir(t)

	msgs1 := []provider.Message{{Role: provider.RoleUser, Content: "first"}}
	if err := Save("update-test", msgs1); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	msgs2 := []provider.Message{{Role: provider.RoleUser, Content: "second"}}
	if err := Save("update-test", msgs2); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load("update-test")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded[0].Content != "second" {
		t.Fatalf("expected updated content 'second', got %q", loaded[0].Content)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with spaces", "with-spaces"},
		{"special/chars!", "special-chars-"},
		{"", "unnamed"},
		{"  ", "unnamed"},
	}
	for _, tt := range tests {
		got := sanitizeName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
