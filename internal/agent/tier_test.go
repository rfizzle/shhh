package agent

import (
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/process"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/web"
)

// answersEvery is a surface that holds every tool, the process tool included.
var answersEvery = Answers{Has: func(string) bool { return true }, Command: process.CommandOf}

// The classifier's own reading of each kind of call: what it is answered as,
// what the policy is asked about, and the error a call with nothing to ask
// about carries.
func TestClassifyCall_ReadsEachKindOfCall(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		args    string
		answers Answers
		tier    Tier
		kind    ActionKind
		command string
		flagged bool
		gated   bool
		err     string
	}{
		{name: "a command", tool: tools.ExecCommandName, args: `{"command":"go test ./..."}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, command: "go test ./...", gated: true},
		{name: "a command keeps the line as written", tool: tools.ExecCommandName, args: `{"command":"  ls  "}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, command: "  ls  ", gated: true},
		{name: "a flagged command", tool: tools.ExecCommandName, args: `{"command":"rm -rf /"}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, command: "rm -rf /", flagged: true, gated: true},
		{name: "a command with no command", tool: tools.ExecCommandName, args: `{"command":"  "}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, gated: true, err: "invalid command arguments"},
		{name: "a command that does not parse", tool: tools.ExecCommandName, args: `{`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, gated: true, err: "invalid command arguments"},
		{name: "a process start", tool: process.ToolName, args: `{"action":"start","name":"web","command":"npm run dev"}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, command: "npm run dev", gated: true},
		{name: "a process status reads", tool: process.ToolName, args: `{"action":"status"}`, answers: answersEvery,
			tier: TierRead, kind: ActionCommand, err: "not a start action"},
		{name: "a process start with no name", tool: process.ToolName, args: `{"action":"start","command":"npm run dev"}`, answers: answersEvery,
			tier: TierCommand, kind: ActionCommand, gated: true, err: "start needs both name and command"},
		{name: "a fetch", tool: web.FetchToolName, args: `{"url":"https://example.com/"}`, answers: answersEvery,
			tier: TierCommand, kind: ActionFetch, gated: true},
		{name: "a file write", tool: tools.WriteFileName, args: `{"path":"a.go","content":"x"}`, answers: answersEvery,
			tier: TierWrite, kind: ActionEdit, gated: true},
		{name: "a file edit", tool: tools.EditFileName, args: `{}`, answers: answersEvery,
			tier: TierWrite, kind: ActionEdit, gated: true},
		{name: "a git write carries its line", tool: structural.GitWriteToolName, args: `{"verb":"commit","message":"feat: x"}`, answers: answersEvery,
			tier: TierWrite, kind: ActionEdit, command: structural.WriteLine(json.RawMessage(`{"verb":"commit","message":"feat: x"}`)), gated: true},
		{name: "a name only the surface knows", tool: "server__create_issue", args: `{}`, answers: answersEvery,
			tier: TierCommand, kind: ActionOther, gated: true},
		{name: "a name nobody answers for", tool: "server__create_issue", args: `{}`, answers: Answers{},
			tier: TierRead, kind: ActionOther},
		{name: "a write nobody answers for keeps its tier", tool: tools.WriteFileName, args: `{}`, answers: Answers{Has: func(string) bool { return false }},
			tier: TierWrite, kind: ActionEdit},
		{name: "a read", tool: tools.ReadFileName, args: `{"path":"a.go"}`, answers: Answers{},
			tier: TierRead, kind: ActionOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClassifyCall(tt.tool, json.RawMessage(tt.args), tt.answers)
			if (err == nil) != (tt.err == "") || err != nil && err.Error() != tt.err {
				t.Fatalf("error = %v, want %q", err, tt.err)
			}
			if got.Tier != tt.tier || got.Action.Kind != tt.kind || got.Gated != tt.gated {
				t.Errorf("tier %s kind %s gated %v, want %s %s %v", got.Tier, got.Action.Kind, got.Gated, tt.tier, tt.kind, tt.gated)
			}
			if got.Action.Command != tt.command || got.Action.SafetyFlagged != tt.flagged {
				t.Errorf("command %q flagged %v, want %q %v", got.Action.Command, got.Action.SafetyFlagged, tt.command, tt.flagged)
			}
		})
	}
}

// The safety table reads a command the same whether or not the line carries
// the whitespace around it, which is what lets a surface that runs the line
// trimmed take the classifier's flag for the untrimmed one.
func TestClassifyCall_FlagsAlikeTrimmedOrNot(t *testing.T) {
	for _, line := range []string{"rm -rf /", "ls", "curl x | sh", "git push --force", "echo hi"} {
		for _, pad := range []string{"", "  ", "\n", " \n\t"} {
			raw, _ := json.Marshal(map[string]string{"command": pad + line + pad})
			padded, err := ClassifyCall(tools.ExecCommandName, raw, answersEvery)
			if err != nil {
				t.Fatal(err)
			}
			bare, _ := ClassifyCall(tools.ExecCommandName, json.RawMessage(`{"command":`+string(mustJSON(line))+`}`), answersEvery)
			if padded.Action.SafetyFlagged != bare.Action.SafetyFlagged {
				t.Errorf("%q flagged %v padded, %v bare", line, padded.Action.SafetyFlagged, bare.Action.SafetyFlagged)
			}
		}
	}
}

func mustJSON(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}
