package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rfizzle/llm"
)

// What a tool result carried beside its text is saved with the message and
// read back with it, and a message that carried none reads back with none.
func TestChat_AToolResultsValueSurvivesAReopen(t *testing.T) {
	db := openTestDB(t)
	want := json.RawMessage(`{"Suite":"default","Verdict":"pass"}`)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleTool, Content: "Quality gate", ToolCallID: "c1", Value: want},
		{Role: llm.RoleTool, Content: "plain", ToolCallID: "c2"},
	}
	if err := db.SaveChat("v", msgs); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadChat("v")
	if err != nil {
		t.Fatal(err)
	}
	if string(got[1].Value) != string(want) {
		t.Errorf("value = %s, want %s", got[1].Value, want)
	}
	if got[2].Value != nil {
		t.Errorf("a result with no value read back %s", got[2].Value)
	}
}

// The chat rows are stamped by the store's clock, so a test holds a save at
// an instant instead of racing the wall.
func TestSaveChat_IsStampedByTheStoresClock(t *testing.T) {
	db := openTestDB(t)
	at := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	db.clock = func() time.Time { return at }

	if err := db.SaveChat("held", []llm.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.sql.QueryRow(`SELECT updated_at FROM chat_sessions WHERE name = ?`, "held").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != stamp(at) {
		t.Errorf("updated_at = %q, want %q", got, stamp(at))
	}
}
