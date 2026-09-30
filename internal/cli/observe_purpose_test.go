package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// commandPurposes is the window's command split as the dashboard reads it,
// keyed by word.
func commandPurposes(t *testing.T, db *storage.DB) map[string]int {
	t.Helper()
	got, err := db.AgentCommandPurposes(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("command purposes: %v", err)
	}
	out := map[string]int{}
	for _, p := range got {
		out[p.Purpose] = p.Count
	}
	return out
}

// An unattended run files each command under what it was for, and the
// command's text reaches no column of the record.
func TestHeadlessObserver_ACommandIsRecordedByWhatItWasFor(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "print", "anthropic", "test-model", nil)
	obs := headlessObserver{rec: rec, rounds: func() int { return 1 }}

	obs.toolResult(agent.ToolResult{Call: provider.ToolCall{ID: "1", Name: "execute_command",
		Arguments: `{"command":"tail -40 /home/someone/notes.md"}`}, Result: "text", Duration: time.Millisecond})
	obs.toolResult(agent.ToolResult{Call: provider.ToolCall{ID: "2", Name: "execute_command",
		Arguments: `{"command":"go test ./..."}`}, Result: "ok", Duration: time.Millisecond})
	obs.toolResult(toolResultOf("read_file", time.Millisecond, "the file"))
	rec.end()

	if got := commandPurposes(t, db); got[observe.PurposeRead] != 1 || got[observe.PurposeBuild] != 1 || len(got) != 2 {
		t.Fatalf("commands by purpose = %v, want one read and one build", got)
	}
	events, err := db.AgentSessionEvents(rec.sessionID())
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if e.Tool == "read_file" && e.Purpose != "" {
			t.Fatalf("a tool that is not a command carries a purpose: %+v", e)
		}
		for _, s := range []string{e.Tool, e.Outcome, e.Reason, e.Purpose} {
			if strings.Contains(s, "someone") || strings.Contains(s, "tail") {
				t.Fatalf("the command's text reached the record: %+v", e)
			}
		}
	}
}

// Commands recorded before the record carried a word are read back out of
// the conversation their session saved, the way the session page reads its
// targets: by turn, round and order. A command at turn 0 is left unread, and
// a second pass writes nothing.
func TestClassifyRecordedCommands_ReadsTheWordFromTheSavedConversation(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	slot := "2026-09-30 10:00:00"
	if err := db.SaveChat(slot, []provider.Message{
		{Role: provider.RoleUser, Content: "how big is it?", Turn: 1},
		{Role: provider.RoleAssistant, Turn: 1, Round: 2, ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "execute_command", Arguments: `{"command":"wc -c docs/big.md"}`},
			{ID: "b", Name: "execute_command", Arguments: `{"command":"grep -n foo docs/big.md"}`},
		}},
		{Role: provider.RoleTool, ToolCallID: "a", Turn: 1, Round: 2, Content: "51000 docs/big.md"},
		{Role: provider.RoleTool, ToolCallID: "b", Turn: 1, Round: 2, Content: "3:foo"},
	}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	rec.link(slot)
	// As a build from before the word would have recorded them: no purpose.
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 2}, "execute_command", time.Millisecond, "ok", "", "")
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 2}, "execute_command", time.Millisecond, "ok", "", "")
	rec.toolCallAt(observe.Pos{}, "execute_command", time.Millisecond, "ok", "", "")
	rec.end()

	written, unread, err := classifyRecordedCommands(db, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if written != 2 || unread != 1 {
		t.Fatalf("classified %d, unread %d; want 2 and 1", written, unread)
	}
	got := commandPurposes(t, db)
	if got[observe.PurposeRead] != 1 || got[observe.PurposeSearch] != 1 || got[""] != 1 {
		t.Fatalf("commands by purpose = %v, want one read, one search, one unrecorded", got)
	}

	written, _, err = classifyRecordedCommands(db, time.Now().Add(-time.Hour))
	if err != nil || written != 0 {
		t.Fatalf("a second pass wrote %d (err %v), want nothing", written, err)
	}
}

// A word the session recorded as the command ran is never overwritten by a
// reading of the conversation, which holds the line the model asked for
// rather than the one that ran.
func TestClassifyRecordedCommands_LeavesAWordAlreadyRecorded(t *testing.T) {
	db := fixtureStore(t)
	rec := startObserveRecorder(db, "code", "anthropic", "test-model", nil)
	slot := "2026-09-30 11:00:00"
	if err := db.SaveChat(slot, []provider.Message{
		{Role: provider.RoleUser, Content: "go", Turn: 1},
		{Role: provider.RoleAssistant, Turn: 1, Round: 1, ToolCalls: []provider.ToolCall{
			{ID: "a", Name: "execute_command", Arguments: `{"command":"rm -rf build"}`},
			{ID: "b", Name: "execute_command", Arguments: `{"command":"ls"}`},
		}},
	}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	rec.link(slot)
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "execute_command", time.Millisecond, "ok", "", observe.PurposeList)
	rec.toolCallAt(observe.Pos{Turn: 1, Round: 1}, "execute_command", time.Millisecond, "ok", "", "")
	rec.end()

	if _, _, err := classifyRecordedCommands(db, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("classify: %v", err)
	}
	// The first keeps the word it ran with; the second is paired with the
	// second call, not the first, because the pairing counts both.
	if got := commandPurposes(t, db); got[observe.PurposeList] != 2 || len(got) != 1 {
		t.Fatalf("commands by purpose = %v, want both list", got)
	}
}
