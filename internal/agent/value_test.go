package agent

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A result's value is filed on the message beside its text, on both ways a
// result is recorded, and the loop collects it from the call it was left under.
func TestToolResultValue_RidesTheRecordedMessage(t *testing.T) {
	a := newTestAgent()
	calls := []provider.ToolCall{{ID: "c1", Name: "read_file"}, {ID: "c2", Name: "execute_command"}}
	auto, _ := a.BeginToolRound("", calls, func(tc provider.ToolCall) bool { return tc.Name == "read_file" })

	a.RecordAutoResults([]ToolResult{{Call: auto[0], Result: "gate text one", Value: json.RawMessage(`{"b":2}`)}})
	head := a.PendingApprovals()[0]
	provider.NoteValue(head.Name, json.RawMessage(head.Arguments), json.RawMessage(`{"a":1}`))
	if v := a.ResolveApproval("gate text two"); string(v) != `{"a":1}` {
		t.Fatalf("ResolveApproval returned %q", v)
	}
	msgs := a.Messages()
	n := len(msgs)
	if string(msgs[n-2].Value) != `{"b":2}` || string(msgs[n-1].Value) != `{"a":1}` {
		t.Errorf("values on the messages = %q, %q", msgs[n-2].Value, msgs[n-1].Value)
	}
}
