package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
)

// A child's shell read is answered the way the session's is: its output, and
// under it the built-in tool that would have answered it, once in the turn.
func TestAChildsShellReadNamesTheToolThatAnswersItOnce(t *testing.T) {
	env := &scriptedEnv{
		steps: []streamStep{
			{calls: []provider.ToolCall{{ID: "c1", Name: tools.ExecCommandName, Arguments: `{"command":"cat importer.go"}`}}},
			{calls: []provider.ToolCall{{ID: "c2", Name: tools.ExecCommandName, Arguments: `{"command":"head -5 exporter.go"}`}}},
			{calls: []provider.ToolCall{{ID: "c3", Name: tools.ExecCommandName, Arguments: `{"command":"go vet ./..."}`}}},
			{text: "read both"},
		},
		gated:   map[string]bool{tools.ExecCommandName: true},
		execOut: "package importer",
	}
	sup := New(context.Background(), Options{
		Root:             t.TempDir(),
		NewEnv:           env.factory(),
		CommandAllowlist: []string{"cat", "head", "go vet"},
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"read the importer"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	env.mu.Lock()
	last := env.requests[len(env.requests)-1]
	env.mu.Unlock()
	results := map[string]string{}
	for _, m := range last {
		if m.Role == provider.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	if got := results["c1"]; !strings.Contains(got, "package importer") || !strings.Contains(got, "\n[built-in: read_file answers this without an approval") {
		t.Errorf("the first read should name read_file under its output: %q", got)
	}
	if got := results["c2"]; got == "" || strings.Contains(got, "[built-in:") {
		t.Errorf("read_file was named once this turn already: %q", got)
	}
	if got := results["c3"]; got == "" || strings.Contains(got, "[built-in:") {
		t.Errorf("a vet is no read: %q", got)
	}
}
