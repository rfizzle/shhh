package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/secret"
)

const gateSecret = "hunter2-not-a-real-credential"

// gateChain is the wrappers a gate call passes through in a session, in the
// order buildToolset puts them: the gate, the reducer scrubbing before it
// stores, the repeat detector, and the vault outermost.
func gateChain(t *testing.T) (agent.ToolExecutor, *quality.Runner) {
	t.Helper()
	ws := t.TempDir()
	writeQualityConfig(t, ws, `{"suites": {"fast": {"checks": [
		{"name": "leaky", "exe": "sh", "args": ["-c", "echo `+gateSecret+`; exit 3"]}]}}}`)
	gate := &quality.Runner{Workspace: ws}
	vault := secret.New()
	if err := vault.Add("API_KEY", gateSecret); err != nil {
		t.Fatal(err)
	}
	store, err := evidence.OpenAt(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	reducer := evidence.NewReducer(store)
	reducer.SetScrub(vault.Scrub)
	next := func(name string, _ json.RawMessage) (string, error) { return "unexpected " + name, nil }
	exec := reducer.WrapExecutor(gate.WrapExecutor(next))
	exec = agent.NewRepeatDetector().WrapExecutor(exec)
	return vault.WrapExecutor(exec), gate
}

// A failing check whose output holds a secret comes back scrubbed, and the
// row still draws as the gate's: the value was never in the text the
// scrub rewrites.
func TestToolResultValue_SurvivesTheScrub(t *testing.T) {
	exec, _ := gateChain(t)
	a := agent.New(nil, nil)
	a.SetExecutor(exec)
	calls := []provider.ToolCall{{ID: "g1", Name: quality.ToolName, Arguments: `{"action":"run","suite":"fast"}`}}
	got := a.ExecuteCalls(calls)[0]
	if strings.Contains(got.Result, gateSecret) {
		t.Fatalf("the secret reached the model:\n%s", got.Result)
	}
	if !strings.Contains(got.Result, "FAIL") {
		t.Fatalf("the check was meant to fail:\n%s", got.Result)
	}
	s, ok := quality.SummaryOf(got.Value)
	if !ok || s.Suite != "fast" || s.Verdict != quality.VerdictFail {
		t.Fatalf("value = %+v, %v; want the fast suite's fail", s, ok)
	}
}

// Two calls that return the same text in one round each keep their own value.
func TestToolResultValue_IdenticalTextsKeepTheirOwn(t *testing.T) {
	exec, gate := gateChain(t)
	if _, err := gate.ExecuteTool(json.RawMessage(`{"action":"run","suite":"fast"}`)); err != nil {
		t.Fatal(err)
	}
	provider.TakeValue(quality.ToolName, `{"action":"run","suite":"fast"}`)
	a := agent.New(nil, nil)
	a.SetExecutor(exec)
	args := `{"action":"result"}`
	got := a.ExecuteCalls([]provider.ToolCall{
		{ID: "r1", Name: quality.ToolName, Arguments: args},
		{ID: "r2", Name: quality.ToolName, Arguments: args},
	})
	for i, r := range got {
		if s, ok := quality.SummaryOf(r.Value); !ok || s.Verdict != quality.VerdictFail {
			t.Errorf("call %d: value = %+v, %v; want the fail", i+1, s, ok)
		}
	}
}
