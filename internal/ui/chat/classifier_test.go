package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
)

// verdictProvider answers every classifier request with a scripted decision
// tool call (or an error).
type verdictProvider struct {
	decision string // "allow" or "deny"
	reason   string
	err      error
	calls    int
}

func (p *verdictProvider) StreamCompletion(ctx context.Context, msgs []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{
		ToolCalls: []provider.ToolCall{{
			ID:        "d1",
			Name:      agent.DecisionToolName,
			Arguments: `{"decision":"` + p.decision + `","reason":"` + p.reason + `"}`,
		}},
		Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 10},
		Done:  true,
	}
	close(ch)
	return ch, nil
}

func (p *verdictProvider) Name() string { return "verdict" }

// classifierModel is execModel in auto mode with a classifier over the given
// fake provider.
func classifierModel(t *testing.T, ran *[]string, p provider.Provider) Model {
	t.Helper()
	// Wired the way the session wires it: the classifier gets its provider
	// through the gate, so what it spends is billed without the model
	// counting anything itself.
	ledger := meter.New(nil)
	m := execModel(t, ran).
		WithLedger(ledger).
		WithClassifier(agent.NewClassifier(ledger.For(p, meter.SourceClassifier),
			agent.ClassifierConfig{Model: "judge"}))
	m.mode = agent.ModeAuto
	return m
}

// driveClassifierDone extracts the classifierDoneMsg from a classifier cmd.
func driveClassifierDone(t *testing.T, cmd tea.Cmd) classifierDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(classifierDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected classifierDoneMsg from the classifier cmd")
	return classifierDoneMsg{}
}

func TestClassifierFlow_AllowRunsCommand(t *testing.T) {
	var ran []string
	m := classifierModel(t, &ran, &verdictProvider{decision: "allow", reason: "runs the requested check"})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	if m.state != stateClassifying {
		t.Fatalf("an unlisted command in auto mode should be classified, got state %d", m.state)
	}
	if !strings.Contains(m.renderStatusBar(80), "✦ checking") {
		t.Fatalf("status bar should show the checking indicator, got %q", m.renderStatusBar(80))
	}

	updated, cmd = m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("classifier allow should run the command, got state %d", m.state)
	}
	// The verdict is background spend, so it counts toward the session but
	// not toward the agent's own turns.
	if spend := m.sessionSpend(); spend.In != 100 || spend.Out != 10 {
		t.Fatalf("classifier usage should count in session spend, got ↑%d ↓%d", spend.In, spend.Out)
	}
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		t.Fatalf("a classifier verdict is not the agent's own spend, got ↑%d ↓%d", m.TotalTokensIn, m.TotalTokensOut)
	}
	updated, restream := m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "go test ./..." {
		t.Fatalf("expected the command to run, got %v", ran)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after the classifier-approved command completes")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (classifier") && strings.Contains(e.text, "go test ./...") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the classifier auto-approval")
	}
}

func TestClassifierFlow_DenyRefusesAndModeWhyExplains(t *testing.T) {
	var ran []string
	m := classifierModel(t, &ran, &verdictProvider{decision: "deny", reason: "user asked for read-only work"})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	updated, restream := m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)

	if len(ran) != 0 {
		t.Fatal("a denied command must not run")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_x" ||
		!strings.Contains(last.Content, "auto mode denied") ||
		!strings.Contains(last.Content, "user asked for read-only work") {
		t.Fatalf("the model should get a denial tool result with the reason, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("the loop should resume so the model sees the denial")
	}
	_, why := m.handleSlashCommand("/mode why")
	if !strings.Contains(why, "user asked for read-only work") {
		t.Fatalf("/mode why should show the latest denial's reason, got %q", why)
	}
}

func TestClassifierFlow_FailureFallsBackToPrompt(t *testing.T) {
	var ran []string
	m := classifierModel(t, &ran, &verdictProvider{err: errors.New("api down")})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	updated, _ = m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("a failed classifier must fall back to asking the user, got state %d", m.state)
	}
	if len(ran) != 0 {
		t.Fatal("nothing may run when the classifier fails")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Classifier unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the fail-closed fallback")
	}
}

func TestClassifierFlow_FlaggedCommandSkipsClassifier(t *testing.T) {
	var ran []string
	p := &verdictProvider{decision: "allow", reason: "sure"}
	m := classifierModel(t, &ran, p)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"git reset --hard"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("safety-flagged commands must prompt the human, got state %d", m.state)
	}
	if p.calls != 0 {
		t.Fatal("safety-flagged commands must never be sent to the classifier")
	}
}

func TestClassifierFlow_CtrlCFallsBackToPrompt(t *testing.T) {
	var ran []string
	m := classifierModel(t, &ran, &verdictProvider{decision: "allow", reason: "ok"})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	if m.state != stateClassifying {
		t.Fatalf("expected a classifier check, got state %d", m.state)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("ctrl+c should skip the check and ask the user, got state %d", m.state)
	}
	// The late verdict from the cancelled check must be dropped.
	updated, _ = m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)
	if m.state != stateConfirmRun || len(ran) != 0 {
		t.Fatal("a stale classifier verdict must not act after cancellation")
	}
}
