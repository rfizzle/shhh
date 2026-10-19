package permission

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/digest"
)

// Both digests a reading can be given — the recorder a headless run and a
// child fill, and the recent context built from a conversation — are rows of
// one description of a call, so the sweep has to be visible in both. A run
// that asks one directory three questions is three rows, and the reading is
// asked to judge repetition from something that can still show it.
func TestDigestRows_SearchesOfOneDirectoryAreTheirPatterns(t *testing.T) {
	patterns := []string{"steeringItem", "queuedSteer", "authorOf"}

	r := agent.NewRecorder(agent.DefaultDigestRows, testDigest)
	var msgs []llm.Message
	for i, pattern := range patterns {
		args := fmt.Sprintf(`{"pattern":%q,"path":"internal/ui/chat"}`, pattern)
		r.Tool("search", args, "no matches")
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: id, Name: "search", Arguments: args},
			}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "no matches"})
	}

	for _, digest := range []struct{ name, text string }{
		{"the recorder", strings.Join(r.Rows(), "\n")},
		{"recent context", RecentContext(msgs, 12, 24_000)},
	} {
		for _, pattern := range patterns {
			want := "search · " + pattern + " ./internal/ui/chat · "
			if !strings.Contains(digest.text, want) {
				t.Fatalf("%s: no row reads %q:\n%s", digest.name, want, digest.text)
			}
		}
		rows := map[string]bool{}
		for _, line := range strings.Split(digest.text, "\n") {
			if strings.Contains(line, "search · ") {
				rows[strings.TrimSpace(line)] = true
			}
		}
		if len(rows) != len(patterns) {
			t.Fatalf("%s: %d questions read as %d distinct rows:\n%s",
				digest.name, len(patterns), len(rows), digest.text)
		}
	}
}

// testDigest is the app words, as the production sites pass them.
var testDigest = agent.Digest{Arg: digest.Arg, Outcome: digest.Outcome, FirstLine: digest.FirstLine}
