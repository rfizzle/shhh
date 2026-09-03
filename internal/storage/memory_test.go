package storage

import (
	"strings"
	"testing"
)

func TestAddAndListMemories(t *testing.T) {
	db := openTestDB(t)

	first, err := db.AddMemory("global", "preference", "prefers table-driven tests", "user")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("expected an assigned id")
	}
	if _, err := db.AddMemory("/proj/a", "convention", "errors wrap with %w", "agent"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := db.AddMemory("/proj/b", "lesson", "other project", "user"); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := db.ListMemories("global", "/proj/a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in-scope memories, got %d", len(got))
	}
	// Newest first.
	if got[0].Scope != "/proj/a" || got[1].Scope != "global" {
		t.Fatalf("expected newest-first ordering, got scopes %q, %q", got[0].Scope, got[1].Scope)
	}
	if got[1].Kind != "preference" || got[1].Provenance != "user" || !strings.Contains(got[1].Text, "table-driven") {
		t.Fatalf("round-trip mismatch: %+v", got[1])
	}
	if got[0].CreatedAt.IsZero() || got[0].UpdatedAt.IsZero() {
		t.Fatal("timestamps should round-trip")
	}
}

func TestListMemories_NoScopes(t *testing.T) {
	db := openTestDB(t)
	got, err := db.ListMemories()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("no scopes should return no entries, got %d", len(got))
	}
}

func TestGetAndDeleteMemory(t *testing.T) {
	db := openTestDB(t)

	m, err := db.AddMemory("global", "correction", "never force-push", "user")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := db.GetMemory(m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != "never force-push" {
		t.Fatalf("get mismatch: %+v", got)
	}

	if err := db.DeleteMemory(m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.GetMemory(m.ID); err == nil {
		t.Fatal("expected get after delete to fail")
	}
	if err := db.DeleteMemory(m.ID); err == nil {
		t.Fatal("expected deleting a missing memory to fail")
	}
}

// TestUpdateMemory covers the column the listing and recall are both ordered
// by: an edited entry has to sort as freshly stated, or the memory the user
// just fixed sinks below the ones they left alone.
func TestUpdateMemory(t *testing.T) {
	db := openTestDB(t)

	m, err := db.AddMemory("global", "convention", "wrap errors", "user")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	updated, err := db.UpdateMemory(m.ID, "wrap errors with %w")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Text != "wrap errors with %w" {
		t.Fatalf("text mismatch: %+v", updated)
	}
	if !updated.UpdatedAt.After(m.UpdatedAt) {
		t.Fatalf("updated_at should be bumped: %s not after %s", updated.UpdatedAt, m.UpdatedAt)
	}
	if !updated.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("created_at should be left alone: %s want %s", updated.CreatedAt, m.CreatedAt)
	}

	if _, err := db.UpdateMemory(m.ID+999, "nothing here"); err == nil {
		t.Fatal("expected updating a missing memory to fail")
	}
}
