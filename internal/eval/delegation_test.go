package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
)

// delegationModel is the model, scripted: each request is answered with the
// next reply. It keeps the first request's messages and tools so a test can
// say what the policy put in front of it.
type delegationModel struct {
	inheritModel
	tools []provider.Tool
}

func (m *delegationModel) StreamCompletion(ctx context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	if m.tools == nil {
		m.tools = opts.Tools
	}
	return m.inheritModel.StreamCompletion(ctx, msgs, opts)
}

func delegationRow(policy string) Row {
	return Row{
		Name: "three packages", Expect: []string{LabelDivided}, Delegation: policy,
		Instruction: "go through each package under pkg/",
		Files:       map[string]string{"pkg/a/a.go": "package a\n", "pkg/b/b.go": "package b\n"},
	}
}

func spawnCall(id string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: subagent.SpawnToolName, Arguments: `{"role":"researcher","task":"read one package"}`}
}

// The three answers are decided by the calls the model made, never by its
// prose: two spawns in one round, one, or an answer with none after the
// read-only tools have run.
func TestAskDelegation_LabelsWhatTheModelAskedFor(t *testing.T) {
	cases := []struct {
		name    string
		replies []provider.StreamEvent
		want    string
	}{
		{"divided", []provider.StreamEvent{{ToolCalls: []provider.ToolCall{spawnCall("s1"), spawnCall("s2")}, Done: true}}, LabelDivided},
		{"spawned after a read", []provider.StreamEvent{
			{ToolCalls: []provider.ToolCall{{ID: "l1", Name: "list_directory", Arguments: `{"path":"pkg"}`}}, Done: true},
			{ToolCalls: []provider.ToolCall{spawnCall("s1")}, Done: true},
		}, LabelSpawned},
		{"alone", []provider.StreamEvent{{Token: "a has none; b has none.", Done: true}}, LabelAlone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &delegationModel{inheritModel: inheritModel{replies: c.replies}}
			a := askDelegation(context.Background(), m, "scripted", delegationRow("proactive"))
			if a.Err != "" {
				t.Fatalf("the row did not run: %s", a.Err)
			}
			if a.Label != c.want {
				t.Fatalf("label %q (%s), want %q", a.Label, a.Reason, c.want)
			}
		})
	}
}

// Off puts no orchestration tool in front of the model, and proactive puts
// the dividing sentence on the spawn line; the row is asked what the session
// would have asked.
func TestAskDelegation_ThePolicyIsWhatTheSessionWouldSend(t *testing.T) {
	has := func(tools []provider.Tool, name string) bool {
		for _, tl := range tools {
			if tl.Name == name {
				return true
			}
		}
		return false
	}
	off := &delegationModel{inheritModel: inheritModel{replies: []provider.StreamEvent{{Token: "done", Done: true}}}}
	askDelegation(context.Background(), off, "scripted", delegationRow("off"))
	if has(off.tools, subagent.SpawnToolName) {
		t.Fatal("off put spawn_agent in front of the model")
	}

	pro := &delegationModel{inheritModel: inheritModel{replies: []provider.StreamEvent{{Token: "done", Done: true}}}}
	askDelegation(context.Background(), pro, "scripted", delegationRow("proactive"))
	if !has(pro.tools, subagent.SpawnToolName) {
		t.Fatal("proactive left spawn_agent out")
	}
	if len(pro.first) == 0 || !strings.Contains(pro.first[0].Content, "is to be divided") {
		t.Fatal("the proactive prompt does not carry the dividing sentence")
	}
}

// A row with no request, or a policy the settings do not hold, is refused at
// load rather than scored.
func TestDelegationRowsNeedARequestAndAPolicy(t *testing.T) {
	if err := checkScriptedRow("table.toml", KindDelegation, delegationRow("explicit")); err != nil {
		t.Fatalf("a complete row was refused: %v", err)
	}
	for name, broken := range map[string]func(*Row){
		"no instruction": func(r *Row) { r.Instruction = "" },
		"no policy":      func(r *Row) { r.Delegation = "" },
		"not a policy":   func(r *Row) { r.Delegation = "sometimes" },
	} {
		r := delegationRow("explicit")
		broken(&r)
		if err := checkScriptedRow("table.toml", KindDelegation, r); err == nil {
			t.Errorf("%s: the row was accepted", name)
		}
	}
}
