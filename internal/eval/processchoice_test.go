package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// The table shipped in this repository is the one the item asks for: at
// least twelve prompts, eight whose right first call is a process start and
// four whose right call is an ordinary command, each with its reason and one
// label.
func TestEval_ProcessChoiceTableShape(t *testing.T) {
	c, err := LoadCase(filepath.Join("..", "..", "evals", "process-choice"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != KindProcessChoice {
		t.Fatalf("kind = %q", c.Kind)
	}
	if len(c.Rows) < 12 {
		t.Fatalf("%d rows, want at least twelve", len(c.Rows))
	}
	seen := map[string]bool{}
	byLabel := map[string]int{}
	for _, r := range c.Rows {
		if r.Why == "" || strings.TrimSpace(r.Instruction) == "" {
			t.Errorf("%q: a row needs its reason and its request", r.Name)
		}
		if len(r.Expect) != 1 {
			t.Errorf("%q: expects %v, want exactly one label", r.Name, r.Expect)
			continue
		}
		byLabel[r.Expect[0]]++
		if seen[r.Instruction] {
			t.Errorf("%q: the request is put twice", r.Name)
		}
		seen[r.Instruction] = true
	}
	if byLabel[LabelProcess] < 8 || byLabel[LabelCommand] < 4 || byLabel[LabelOtherCall] != 0 {
		t.Errorf("labels = %v, want at least 8 process and 4 command, and none for other", byLabel)
	}
}

func TestAskProcessChoice_ReadsTheFirstToolCall(t *testing.T) {
	row := Row{Name: "dev server", Expect: []string{LabelProcess}, Instruction: "start the dev server"}
	start := provider.ToolCall{ID: "p", Name: process.ToolName, Arguments: `{"action":"start","name":"dev","command":"npm run dev"}`}
	cases := []struct {
		name  string
		reply provider.StreamEvent
		want  string
	}{
		{"a process start", provider.StreamEvent{ToolCalls: []provider.ToolCall{start}, Done: true}, LabelProcess},
		{"the command tool", provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "c", Name: tools.ExecCommandName, Arguments: `{"command":"npm run dev"}`}}, Done: true}, LabelCommand},
		{"a process read is neither", provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "r", Name: process.ToolName, Arguments: `{"action":"read","name":"dev"}`}}, Done: true}, LabelOtherCall},
		{"a read of the tree first", provider.StreamEvent{ToolCalls: []provider.ToolCall{{ID: "l", Name: "list_directory", Arguments: `{"path":"."}`}, start}, Done: true}, LabelOtherCall},
		{"no call", provider.StreamEvent{Token: "which port?", Done: true}, LabelOtherCall},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &delegationModel{inheritModel: inheritModel{replies: []provider.StreamEvent{c.reply}}}
			a := askProcessChoice(context.Background(), m, "scripted", row)
			if a.Err != "" || a.Label != c.want {
				t.Fatalf("label %q err %q (%s), want %q", a.Label, a.Err, a.Reason, c.want)
			}
		})
	}
}

// The row is asked with both tools in front of the model and the toolbox's
// line for the process tool, which is what a session holding it writes.
func TestAskProcessChoice_HoldsBothToolsAndTheToolbox(t *testing.T) {
	m := &delegationModel{inheritModel: inheritModel{replies: []provider.StreamEvent{{Token: "ok", Done: true}}}}
	askProcessChoice(context.Background(), m, "scripted", Row{Name: "x", Instruction: "start it"})
	have := map[string]bool{}
	for _, tl := range m.tools {
		have[tl.Name] = true
	}
	if !have[tools.ExecCommandName] || !have[process.ToolName] {
		t.Fatalf("tools = %v, want the command tool and the process tool", have)
	}
	if len(m.first) == 0 || !strings.Contains(m.first[0].Content, "- process —") {
		t.Fatal("the system prompt lacks the toolbox line for the process tool")
	}
}
