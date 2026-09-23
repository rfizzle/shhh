package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
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
	m.policy.mode = agent.ModeAuto
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
	if got := allowedOnRow(t, m, "go test ./..."); !strings.HasPrefix(got, "auto-allowed · classifier") {
		t.Fatalf("the command's own row should say the classifier allowed it, got %q", got)
	}
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "go test ./...") {
			t.Fatalf("the approval should not have a row of its own: %q", e.text)
		}
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

// A judged denial is a row in the feed and nothing on the frame
// (docs/capabilities/approvals-and-safety.md#a-judged-denial-carries-its-reason).
// The three moments the frame used to carry a stale label are all here: the
// turn streaming on under the refusal, a later call that ran, and the next
// user turn.
func TestClassifierFlow_DenialKeepsItsReasonOnTheRowAndNothingOnTheFrame(t *testing.T) {
	const (
		denied = "npm run deploy"
		why    = "the task asked for read-only work"
	)
	var ran []string
	p := &verdictProvider{decision: "deny", reason: why}
	m := classifierModel(t, &ran, p)

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: tools.ExecCommandName, Arguments: `{"command":"` + denied + `"}`},
	}})
	m = updated.(Model)
	updated, restream := m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 0 {
		t.Fatal("a denied command must not run")
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("the turn should carry on streaming under the refusal")
	}

	// The row is the call's own, completed and blocked, with the rule in the
	// account field and the judgement's sentence folded under it.
	idx := len(m.transcript) - 1
	row := m.transcript[idx]
	if row.kind != entryTool || row.deniedBy != decidedByAuto || row.denyRule != classifierRule {
		t.Fatalf("a classifier denial is a rule's denial on the call's own row, got %+v", row)
	}
	if !strings.Contains(row.denyWhy, why) || !strings.Contains(row.denyWhy, denied) {
		t.Fatalf("the row should keep the reason and the call it judged, got %q", row.denyWhy)
	}
	compact := m.activityRowDetail(row, false, m.contentWidth())
	if compact.State != components.ActivityDenied || compact.Outcome != components.OutcomeBlocked {
		t.Fatalf("the settled row should read as blocked, got %q/%d", compact.Outcome, compact.State)
	}
	if strings.Contains(compact.Allowed, why) || strings.Contains(compact.Outcome, why) {
		t.Fatalf("the sentence belongs under the row, not in its outcome: %q %q", compact.Outcome, compact.Allowed)
	}
	if compact.Keys != "" {
		t.Fatalf("a row holding the reason should not send the reader elsewhere for it, got %q", compact.Keys)
	}

	// Nothing of it is on the frame, now or after the turn has moved on.
	assertNoFrameDenial(t, m, "the refusal")
	p.decision, p.reason = "allow", "the checks are read-only"
	updated, cmd = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_y", Name: tools.ExecCommandName, Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	updated, cmd = m.Update(driveClassifierDone(t, cmd))
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "go test ./..." {
		t.Fatalf("the later command should have run, got %v", ran)
	}
	assertNoFrameDenial(t, m, "a later command that ran")

	// The refusal selects and opens like any other row that never ran: the
	// reading cursor walks back to it and [enter] gives the body.
	updated, _ = m.Update(readingChord())
	m = updated.(Model)
	for m.focusIdx > idx {
		updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
		m = updated.(Model)
	}
	if m.focusIdx != idx {
		t.Fatalf("the reading cursor should reach the denied row, got %d want %d", m.focusIdx, idx)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.transcript[idx].expanded {
		t.Fatal("[enter] should open the denied row")
	}
	opened := stripANSI(m.activityRowDetail(m.transcript[idx], false, m.contentWidth()).View(m.contentWidth()))
	for _, want := range []string{why, denied} {
		if !strings.Contains(opened, want) {
			t.Fatalf("the opened row should show %q:\n%s", want, opened)
		}
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = updated.(Model)

	// And the next turn starts on a frame that never had one to inherit,
	// with the refusal still in the transcript behind it.
	m.state = stateInput
	m.input.SetValue("read it instead")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	assertNoFrameDenial(t, m, "the next turn")
	if got := m.transcript[idx]; got.denyWhy != row.denyWhy {
		t.Fatalf("the refusal should still carry its reason a turn later, got %q", got.denyWhy)
	}

	// The session summary still answers the question it always answered.
	if _, out := m.handleSlashCommand("/permissions why"); !strings.Contains(out, why) {
		t.Fatalf("/permissions why should still report the latest denial, got %q", out)
	}
}

// assertNoFrameDenial reads the whole frame for the label a denial used to
// leave on it.
func assertNoFrameDenial(t *testing.T, m Model, when string) {
	t.Helper()
	if m.denialNotice != "" {
		t.Fatalf("%s left a denial on the notice rail: %q", when, m.denialNotice)
	}
	if view := stripANSI(m.View().Content); strings.Contains(view, "auto denied") {
		t.Fatalf("%s left a denial label on the frame:\n%s", when, view)
	}
}

// A rule that only matched keeps the semantics it had: its own rule name on
// the row, no judgement's sentence under it, and the notice rail saying a
// standing rule of the reader's refused something.
func TestClassifierFlow_ADenyListRefusalIsNotAJudgement(t *testing.T) {
	var ran []string
	p := &verdictProvider{decision: "allow", reason: "looks fine"}
	m := classifierModel(t, &ran, p).WithCommandDenylist([]string{"rm"})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: tools.ExecCommandName, Arguments: `{"command":"rm -rf build"}`},
	}})
	m = updated.(Model)

	if p.calls != 0 {
		t.Fatal("the deny list answers before the classifier is paid to think")
	}
	row := m.transcript[len(m.transcript)-1]
	if row.denyRule != agent.DenyReasonDenylist {
		t.Fatalf("a deny-list refusal names its own rule, got %q", row.denyRule)
	}
	if row.denyWhy != "" {
		t.Fatalf("a rule that only matched has no judgement to fold under the row, got %q", row.denyWhy)
	}
	if m.denialNotice == "" {
		t.Fatal("a standing rule's refusal still says so on the notice rail")
	}
	if got := m.activityRowDetail(row, false, m.contentWidth()); got.Keys != "/permissions why" {
		t.Fatalf("a matched rule's row still offers the longer answer, got %q", got.Keys)
	}
}

// classifierDownRound is a round of three in auto mode whose middle call is a
// command the classifier could not judge. The reads on either side need no
// decision and land while the classifier is being asked, so the failed
// verdict arrives with the call after it already on screen — the case where
// a notice filed at the end of the feed reads as being about the wrong call.
func classifierDownRound(t *testing.T, width int) Model {
	t.Helper()
	ledger := meter.New(nil)
	m := batchModel(t).
		WithRunner(legacyRunner(func(ctx context.Context, cmd string) (string, int) {
			t.Fatalf("nothing may run on a verdict that never came, but %q did", cmd)
			return "", 0
		})).
		WithLedger(ledger).
		WithClassifier(agent.NewClassifier(ledger.For(&verdictProvider{err: errors.New("api down")}, meter.SourceClassifier),
			agent.ClassifierConfig{Model: "judge"}))
	m.policy.mode = agent.ModeAuto
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 48})
	m = updated.(Model)
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		readCall("call_1", "internal/agent/loop.go"),
		execCall("call_2", "go test ./internal/agent"),
		readCall("call_3", "internal/ui/chat/turn.go"),
	}})
	m = updated.(Model)
	var verdict *classifierDoneMsg
	pending := unwrapBatch(cmd)
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch msg := c().(type) {
		case toolResultsMsg:
			updated, cmd = m.Update(msg)
			m = updated.(Model)
			pending = append(pending, unwrapBatch(cmd)...)
		case classifierDoneMsg:
			verdict = &msg
		}
	}
	if verdict == nil {
		t.Fatalf("the command should have been put to the classifier, state %d, %d rows", m.state, len(m.transcript))
	}
	updated, _ = m.Update(*verdict)
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("a failed classifier must fall back to asking, got state %d", m.state)
	}
	return m
}

// TestClassifierFlow_TheFailureNoticeTakesTheCallsPlace holds the notice to
// the call it is about. It is filed at the call's place in the round — after
// the read asked for before it, in front of the read asked for after it — and
// the row the card's answer files goes directly under it, so the two read as
// one account of one call (docs/interface/principles.md#one-grid).
func TestClassifierFlow_TheFailureNoticeTakesTheCallsPlace(t *testing.T) {
	m := classifierDownRound(t, 80)
	rows := func(m Model) []string {
		var out []string
		for _, e := range m.transcript {
			switch {
			case e.kind == entrySystem && strings.Contains(e.text, "Classifier unavailable"):
				out = append(out, "notice")
			case e.kind == entryTool || e.kind == entryCommand:
				out = append(out, m.activityRowFor(e).Target)
			}
		}
		return out
	}
	want := []string{"internal/agent/loop.go", "notice", "internal/ui/chat/turn.go"}
	if got := rows(m); !slices.Equal(got, want) {
		t.Fatalf("the notice should sit in the command's place, want %v, got %v", want, got)
	}

	updated, _ := handover(t, m).Update(keyN())
	m = updated.(Model)
	got := rows(m)
	if len(got) != 4 || got[0] != want[0] || got[1] != "notice" || got[3] != want[2] {
		t.Fatalf("the answer's row should land under the notice, between the reads, got %v", got)
	}
	if !strings.Contains(got[2], "go test") {
		t.Fatalf("the row under the notice should be the refused command, got %q", got[2])
	}
}
