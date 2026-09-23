package eval

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// inheritModel is the child's model, scripted: each request is answered with
// the next of its replies. It keeps the first request so a test can say what
// the child was handed.
type inheritModel struct {
	mu      sync.Mutex
	replies []provider.StreamEvent
	first   []provider.Message
}

func (m *inheritModel) Name() string { return "scripted" }

func (m *inheritModel) StreamCompletion(_ context.Context, msgs []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.first == nil {
		m.first = append([]provider.Message(nil), msgs...)
	}
	ch := make(chan provider.StreamEvent, 1)
	if len(m.replies) == 0 {
		ch <- provider.StreamEvent{Token: "out of script", Done: true}
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
		Conversation: []provider.Message{
			{Role: provider.RoleUser, Content: "why do requests give up?"},
			{Role: provider.RoleAssistant, Content: "limits.go sets MaxRetries = 7."},
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
	answer := func(text string) provider.StreamEvent { return provider.StreamEvent{Token: text, Done: true} }
	cases := []struct {
		name    string
		replies []provider.StreamEvent
		want    string
	}{
		{"acted", []provider.StreamEvent{answer("7 retries, set in limits.go.")}, LabelActed},
		{"reread", []provider.StreamEvent{
			{ToolCalls: []provider.ToolCall{{ID: "r1", Name: "read_file", Arguments: `{"path":"limits.go"}`}}},
			answer("Seven retries (7), set in limits.go."),
		}, LabelReread},
		{"missed", []provider.StreamEvent{answer("It retries a few times.")}, LabelMissed},
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
	m := &inheritModel{replies: []provider.StreamEvent{{Token: "7, limits.go", Done: true}}}
	askInherit(context.Background(), m, "scripted", inheritRow())
	var system, opening string
	for _, msg := range m.first {
		switch {
		case msg.Role == provider.RoleSystem:
			system = msg.Content
		case msg.Role == provider.RoleUser && opening == "":
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
