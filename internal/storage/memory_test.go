package storage

import (
	"strings"
	"testing"
	"time"
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
	second, err := db.AddMemory("/proj/a", "convention", "errors wrap with %w", "agent")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	third, err := db.AddMemory("global", "lesson", "read the error before the stack", "agent")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := db.AddMemory("/proj/b", "lesson", "other project", "user"); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := db.ListMemories("global", "/proj/a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 in-scope memories, got %d", len(got))
	}
	// Three saved in one tick share a timestamp to whatever precision the
	// clock has, so only a total order puts them here every run.
	wantIDs := []int64{third.ID, second.ID, first.ID}
	for i, want := range wantIDs {
		if got[i].ID != want {
			gotIDs := make([]int64, len(got))
			for j, m := range got {
				gotIDs[j] = m.ID
			}
			t.Fatalf("expected newest-first ids %v, got %v", wantIDs, gotIDs)
		}
	}
	if got[2].Scope != "global" || got[1].Scope != "/proj/a" {
		t.Fatalf("expected the out-of-scope entry left out, got scopes %q, %q, %q", got[0].Scope, got[1].Scope, got[2].Scope)
	}
	if got[2].Kind != "preference" || got[2].Provenance != "user" || !strings.Contains(got[2].Text, "table-driven") {
		t.Fatalf("round-trip mismatch: %+v", got[2])
	}
	if got[0].CreatedAt.IsZero() || got[0].UpdatedAt.IsZero() {
		t.Fatal("timestamps should round-trip")
	}
}

// TestMemoryStampOrdersAsText pins the write format against the reason it was
// chosen. SQLite orders the listing by comparing these columns as text, so a
// format whose width varies with the value sorts a later entry first, and the
// id tie-break above never fires because the two strings are not equal.
func TestMemoryStampOrdersAsText(t *testing.T) {
	base := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	// Trailing zeros are what a variable-width format drops: .500000000
	// renders as ".5" and outsorts the later ".512".
	earlier := base.Add(500 * time.Millisecond)
	later := base.Add(512 * time.Millisecond)

	if memoryStamp(earlier) >= memoryStamp(later) {
		t.Fatalf("a later instant must sort after an earlier one as text: %q >= %q",
			memoryStamp(earlier), memoryStamp(later))
	}
	if len(memoryStamp(earlier)) != len(memoryStamp(later)) {
		t.Fatalf("the stamp width must not vary with the value: %q vs %q",
			memoryStamp(earlier), memoryStamp(later))
	}

	// The reader parses with RFC3339Nano, so the two have to agree.
	back, err := time.Parse(time.RFC3339Nano, memoryStamp(later))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !back.Equal(later) {
		t.Fatalf("round-trip mismatch: %s want %s", back, later)
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
