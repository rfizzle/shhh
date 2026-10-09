package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

// seedPatternRound opens a session in checkout p whose one round asked for
// calls and recorded events, saving the conversation under slot and linking
// it, so the reading has both sides of the join to read.
func seedPatternRound(t *testing.T, db *storage.DB, slot string, calls []provider.ToolCall, events ...storage.AgentEvent) {
	t.Helper()
	id, err := db.StartAgentSession("code", "openai", "gpt-test")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := db.StampAgentSession(id, storage.AgentProvenance{Project: "p"}); err != nil {
		t.Fatalf("stamp session: %v", err)
	}
	for _, e := range events {
		if e.Kind != storage.AgentEventSignal {
			e.Turn, e.Round = 1, 1
		}
		if err := db.RecordAgentEvent(id, e); err != nil {
			t.Fatalf("record event: %v", err)
		}
	}
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "go", Turn: 1},
		{Role: provider.RoleAssistant, Turn: 1, Round: 1, ToolCalls: calls},
	}
	if err := db.SaveChat(slot, msgs); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	if _, err := db.LinkAgentSession(id, slot); err != nil {
		t.Fatalf("link session: %v", err)
	}
}

// The three tables over one seeded checkout: a file read in three sessions,
// a command asked about in three, a suite that failed first in three — and
// a fourth session whose conversation was pruned, said in the footer rather
// than dropped. Nothing in the reading asks a model or writes a row.
func TestObservePatterns_FilesCommandsSuites(t *testing.T) {
	db := fixtureStore(t)
	for i := range 3 {
		answer := "allow"
		if i == 2 {
			answer = "deny"
		}
		seedPatternRound(t, db, fmt.Sprintf("read-%d", i),
			[]provider.ToolCall{{ID: "r", Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`}},
			storage.AgentEvent{Kind: storage.AgentEventTool, Tool: "read_file", Outcome: "ok"})
		seedPatternRound(t, db, fmt.Sprintf("ask-%d", i),
			[]provider.ToolCall{{ID: "c", Name: "execute_command", Arguments: `{"command":"go test ./internal/agent"}`}},
			storage.AgentEvent{Kind: storage.AgentEventDecision, Outcome: "ask", Reason: "safety"},
			storage.AgentEvent{Kind: storage.AgentEventDecision, Outcome: answer, Reason: "user"},
			storage.AgentEvent{Kind: storage.AgentEventSignal, Outcome: "gate", Tool: "default", Reason: "fail"},
			storage.AgentEvent{Kind: storage.AgentEventSignal, Outcome: "gate", Tool: "default", Reason: "pass"})
	}
	seedPatternRound(t, db, "pruned",
		[]provider.ToolCall{{ID: "r", Name: "read_file", Arguments: `{"path":"internal/agent/loop.go"}`}},
		storage.AgentEvent{Kind: storage.AgentEventTool, Tool: "read_file", Outcome: "ok"})
	if err := db.DeleteChat("pruned"); err != nil {
		t.Fatalf("delete chat: %v", err)
	}

	p, err := readObservePatterns(db, "30d", time.Now().AddDate(0, 0, -30), "p", observePatternsMinSessions)
	if err != nil {
		t.Fatalf("read patterns: %v", err)
	}
	out := renderObservePatterns(p).Render(110)
	for _, want := range []string{
		"FILES READ", "3 sessions  internal/agent/loop.go · 1 call a session",
		"COMMANDS ASKED ABOUT", "go test · asked 3 times", "[allowed 2 of 3]",
		"SUITES THAT FAILED FIRST", "default · failed before it passed", "[of 3 sessions that ran it]",
		"1 read in 1 session had no conversation to name the file",
		"nothing was written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("patterns report missing %q:\n%s", want, out)
		}
	}

	// The dashboard names the top of each table and the way in.
	line := observePatternsLine(p)
	if len(line) != 1 || !strings.Contains(line[0].Text,
		"internal/agent/loop.go read in 3 sessions · `go test` asked about in 3 · default failed first in 3 · `shhh observe patterns`") {
		t.Errorf("dashboard line = %+v", line)
	}

	// Another checkout, or no checkout at all, has none of these.
	for _, project := range []string{"q", ""} {
		other, err := readObservePatterns(db, "30d", time.Now().AddDate(0, 0, -30), project, observePatternsMinSessions)
		if err != nil || len(other.Files)+len(other.Commands)+len(other.Suites) != 0 {
			t.Errorf("checkout %q read %+v (%v)", project, other, err)
		}
		if observePatternsLine(other) != nil {
			t.Errorf("checkout %q drew a dashboard line", project)
		}
	}
}
