package eval

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/llm"
)

// inheritModel is the child's model, scripted: each request is answered with
// the next of its replies. It keeps the first request so a test can say what
// the child was handed.
type inheritModel struct {
	mu      sync.Mutex
	replies []llm.StreamEvent
	first   []llm.Message
}

func (m *inheritModel) Name() string { return "scripted" }

func (m *inheritModel) StreamCompletion(_ context.Context, msgs []llm.Message, _ llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.first == nil {
		m.first = append([]llm.Message(nil), msgs...)
	}
	ch := make(chan llm.StreamEvent, 1)
	if len(m.replies) == 0 {
		ch <- llm.StreamEvent{Token: "out of script", Done: true}
	} else {
		ch <- m.replies[0]
		m.replies = m.replies[1:]
	}
	close(ch)
	return ch, nil
}

func inheritRow() Row {
	return Row{
		Name: "retry bound", Expect: []string{LabelActed}, Task: "say how many retries and where",
		Inherit: 1,
		Conversation: []llm.Message{
			{Role: llm.RoleUser, Content: "why do requests give up?"},
			{Role: llm.RoleAssistant, Content: "limits.go sets MaxRetries = 7."},
		},
		Paths: []string{"limits.go"},
		Files: map[string]string{"limits.go": "package limits\n\nconst MaxRetries = 7\n"},
		Needs: []string{"7", "limits.go"},
	}
}

// The three answers an inherit row can come back with, each decided by what
// the child did rather than by a reading of its prose: the fact carried and
// nothing read again, a file the turns had shown read again, and a report
// without the fact.
func TestAskInherit_LabelsWhatTheChildDid(t *testing.T) {
	answer := func(text string) llm.StreamEvent { return llm.StreamEvent{Token: text, Done: true} }
	cases := []struct {
		name    string
		replies []llm.StreamEvent
		want    string
	}{
		{"acted", []llm.StreamEvent{answer("7 retries, set in limits.go.")}, LabelActed},
		{"reread", []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"limits.go"}`}}},
			answer("Seven retries (7), set in limits.go."),
		}, LabelReread},
		{"missed", []llm.StreamEvent{answer("It retries a few times.")}, LabelMissed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &inheritModel{replies: c.replies}
			a := askInherit(context.Background(), m, "scripted", inheritRow())
			if a.Err != "" {
				t.Fatalf("the row did not run: %s", a.Err)
			}
			if a.Label != c.want {
				t.Fatalf("label %q (%s), want %q", a.Label, a.Reason, c.want)
			}
		})
	}
}

// The child is handed the row's conversation as its parent's turns, and its
// prompt says so.
func TestAskInherit_TheChildIsHandedTheTurns(t *testing.T) {
	m := &inheritModel{replies: []llm.StreamEvent{{Token: "7, limits.go", Done: true}}}
	askInherit(context.Background(), m, "scripted", inheritRow())
	var system, opening string
	for _, msg := range m.first {
		switch {
		case msg.Role == llm.RoleSystem:
			system = msg.Content
		case msg.Role == llm.RoleUser && opening == "":
			opening = msg.Content
		}
	}
	if !strings.Contains(system, "You were handed the orchestrator's last turn") {
		t.Fatalf("the child's prompt does not say it was handed a turn:\n%s", system)
	}
	if !strings.Contains(opening, "assistant: limits.go sets MaxRetries = 7.") || !strings.HasSuffix(opening, "say how many retries and where") {
		t.Fatalf("the child did not open on the turn and then its task:\n%s", opening)
	}
}

// A row that cannot measure anything is refused at load.
func TestInheritRowsNeedTheirTurnsAndWhatNotToRead(t *testing.T) {
	row := inheritRow()
	if err := checkScriptedRow("table.toml", KindInherit, row); err != nil {
		t.Fatalf("a complete row was refused: %v", err)
	}
	for name, broken := range map[string]func(*Row){
		"no turns":   func(r *Row) { r.Conversation = nil },
		"no inherit": func(r *Row) { r.Inherit = 0 },
		"no needs":   func(r *Row) { r.Needs = nil },
		"no paths":   func(r *Row) { r.Paths = nil },
	} {
		r := inheritRow()
		broken(&r)
		if err := checkScriptedRow("table.toml", KindInherit, r); err == nil {
			t.Errorf("%s: the row was accepted", name)
		}
	}
}

// A tool result in the turns, answering a call, is elided the way a session
// elides it: the placeholder names an id the child's evidence tool reads back.
func TestAskInherit_AnElidedResultCanBeReadBack(t *testing.T) {
	body := "package limits\n\n" + strings.Repeat("// a long comment about the bound\n", 30) + "const MaxRetries = 7\n"
	row := inheritRow()
	row.Conversation = conversation([]string{
		"user: why do requests give up?",
		"assistant: Reading limits.go.",
		`call: read_file {"path":"limits.go"}`,
		"tool: " + body,
		"assistant: limits.go sets MaxRetries = 7.",
	})
	m := &inheritModel{replies: []llm.StreamEvent{{Token: "7, limits.go", Done: true}}}
	a := askInherit(context.Background(), m, "scripted", row)
	if a.Err != "" {
		t.Fatalf("the row did not run: %s", a.Err)
	}
	var opening string
	for _, msg := range m.first {
		if msg.Role == llm.RoleUser {
			opening = msg.Content
			break
		}
	}
	if !strings.Contains(opening, "tool result (read_file): [result elided: ") || !strings.Contains(opening, "stored as evidence ev-") {
		t.Fatalf("the elided result does not offer itself back:\n%s", opening)
	}
}
