package storage

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// What a tool result carried beside its text is saved with the message and
// read back with it, and a message that carried none reads back with none.
func TestChat_AToolResultsValueSurvivesAReopen(t *testing.T) {
	db := openTestDB(t)
	want := json.RawMessage(`{"Suite":"default","Verdict":"pass"}`)
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "go"},
		{Role: provider.RoleTool, Content: "Quality gate", ToolCallID: "c1", Value: want},
		{Role: provider.RoleTool, Content: "plain", ToolCallID: "c2"},
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
