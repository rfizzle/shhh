package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/subagent"
)

// delegationModel is the model, scripted: each request is answered with the
// next reply. It keeps the first request's messages and tools so a test can
// say what the policy put in front of it.
type delegationModel struct {
	inheritModel
	tools []llm.Tool
}

func (m *delegationModel) StreamCompletion(ctx context.Context, msgs []llm.Message, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	if m.tools == nil {
		m.tools = opts.Tools
	}
	return m.inheritModel.StreamCompletion(ctx, msgs, opts)
}

func delegationRow(policy string) Row {
	return Row{
		Name: "three packages", Expect: []string{LabelDivided}, Delegation: policy,
		Parts:       []string{"a", "b"},
		Instruction: "go through each package under pkg/",
		Files:       map[string]string{"pkg/a/a.go": "package a\n", "pkg/b/b.go": "package b\n"},
	}
}

func spawnCall(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: subagent.SpawnToolName, Arguments: `{"role":"researcher","task":"read one package"}`}
}

// The three answers are decided by the calls the model made, never by its
// prose: two spawns in one round, one, or an answer with none after the
// read-only tools have run.
func TestAskDelegation_LabelsWhatTheModelAskedFor(t *testing.T) {
	cases := []struct {
		name    string
		replies []llm.StreamEvent
		want    string
	}{
		{"divided", []llm.StreamEvent{{ToolCalls: []llm.ToolCall{spawnCall("s1"), spawnCall("s2")}, Done: true}}, LabelDivided},
		{"spawned after a read", []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{{ID: "l1", Name: "list_directory", Arguments: `{"path":"pkg"}`}}, Done: true},
			{ToolCalls: []llm.ToolCall{spawnCall("s1")}, Done: true},
		}, LabelSpawned},
		{"alone", []llm.StreamEvent{{Token: "a has none; b has none.", Done: true}}, LabelAlone},
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

// A round of reads that reaches two parts is the work divided in place; a
// glob is not a read, one part is not two, and the parts one to a round are
// still one agent working through them.
func TestAskDelegation_ReadsAcrossPartsInOneRoundAreDividedInPlace(t *testing.T) {
	read := func(id, path string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "read_file", Arguments: `{"path":"` + path + `"}`}
	}
	glob := llm.ToolCall{ID: "g", Name: "glob", Arguments: `{"pattern":"pkg/**/*.go"}`}
	done := llm.StreamEvent{Token: "none.", Done: true}
	cases := []struct {
		name    string
		replies []llm.StreamEvent
		want    string
	}{
		{"two parts at once", []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{glob, read("1", "pkg/a/a.go"), read("2", "pkg/b/b.go")}, Done: true}}, LabelDividedInPlace},
		{"one part", []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{read("1", "pkg/a/a.go"), read("2", "pkg/a/a.go")}, Done: true}, done}, LabelAlone},
		{"a glob alone", []llm.StreamEvent{{ToolCalls: []llm.ToolCall{glob}, Done: true}, done}, LabelAlone},
		{"one part per round", []llm.StreamEvent{
			{ToolCalls: []llm.ToolCall{read("1", "pkg/a/a.go")}, Done: true},
			{ToolCalls: []llm.ToolCall{read("2", "pkg/b/b.go")}, Done: true}, done}, LabelAlone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &delegationModel{inheritModel: inheritModel{replies: c.replies}}
			a := askDelegation(context.Background(), m, "scripted", delegationRow("proactive"))
			if a.Label != c.want {
				t.Fatalf("label %q (%s), want %q", a.Label, a.Reason, c.want)
			}
			if c.want == LabelDividedInPlace && a.Went != "glob, read_file×2" {
				t.Errorf("went = %q", a.Went)
			}
		})
	}
}

// Off puts no orchestration tool in front of the model, and proactive puts
// the dividing sentence on the spawn line; the row is asked what the session
// would have asked.
func TestAskDelegation_ThePolicyIsWhatTheSessionWouldSend(t *testing.T) {
	has := func(tools []llm.Tool, name string) bool {
		for _, tl := range tools {
			if tl.Name == name {
				return true
			}
		}
		return false
	}
	off := &delegationModel{inheritModel: inheritModel{replies: []llm.StreamEvent{{Token: "done", Done: true}}}}
	askDelegation(context.Background(), off, "scripted", delegationRow("off"))
	if has(off.tools, subagent.SpawnToolName) {
		t.Fatal("off put spawn_agent in front of the model")
	}

	pro := &delegationModel{inheritModel: inheritModel{replies: []llm.StreamEvent{{Token: "done", Done: true}}}}
	askDelegation(context.Background(), pro, "scripted", delegationRow("proactive"))
	if !has(pro.tools, subagent.SpawnToolName) {
		t.Fatal("proactive left spawn_agent out")
	}
	if len(pro.first) == 0 || !strings.Contains(pro.first[0].Content, "is to be divided") {
		t.Fatal("the proactive prompt does not carry the dividing sentence")
	}
}

// A row that read to the bound says which tools each round called, compactly
// and clipped.
func TestAskDelegation_AloneNamesTheToolsEachRoundCalled(t *testing.T) {
	read := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Name: "read_file", Arguments: `{"path":"pkg/a/a.go"}`}
	}
	glob := llm.ToolCall{ID: "g", Name: "glob", Arguments: `{"pattern":"*"}`}
	var replies []llm.StreamEvent
	for range delegationRounds {
		replies = append(replies, llm.StreamEvent{ToolCalls: []llm.ToolCall{read("a"), read("b"), glob}, Done: true})
	}
	m := &delegationModel{inheritModel: inheritModel{replies: replies}}
	a := askDelegation(context.Background(), m, "scripted", delegationRow("proactive"))
	if a.Label != LabelAlone || !strings.HasPrefix(a.Reason, "read for 6 rounds") {
		t.Fatalf("label %q (%s)", a.Label, a.Reason)
	}
	if !strings.HasPrefix(a.Went, "read_file×2, glob · read_file×2, glob · ") {
		t.Errorf("went = %q", a.Went)
	}
	if n := len([]rune(a.Went)); n > maxWent {
		t.Errorf("went is %d runes, over %d", n, maxWent)
	}
	if got := roundTools([]llm.ToolCall{read("a"), glob, read("b")}); got != "read_file×2, glob" {
		t.Errorf("roundTools = %q", got)
	}
	if got := clipRunes(strings.Repeat("x", 200), 10); got != strings.Repeat("x", 9)+"…" {
		t.Errorf("clipRunes = %q", got)
	}

	first := &delegationModel{inheritModel: inheritModel{replies: []llm.StreamEvent{{Token: "a has none.", Done: true}}}}
	if a := askDelegation(context.Background(), first, "scripted", delegationRow("proactive")); a.Went != "answered in its first turn" {
		t.Errorf("an answer in the first turn says so: %q", a.Went)
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
		"in place without parts": func(r *Row) {
			r.Expect, r.Parts = []string{LabelDividedInPlace}, nil
		},
	} {
		r := delegationRow("explicit")
		broken(&r)
		if err := checkScriptedRow("table.toml", KindDelegation, r); err == nil {
			t.Errorf("%s: the row was accepted", name)
		}
	}
}
