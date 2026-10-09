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
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/tools"
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
	m := execModel(t, ran)
	m.wiring.Ledger = ledger
	m.classifier.judge = agent.NewClassifier(ledger.For(p, meter.SourceClassifier),
		agent.ClassifierConfig{Model: "judge"})
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
	if !strings.Contains(m.renderStatusBar(80), "✦ deciding") {
		t.Fatalf("status bar should show the deciding indicator, got %q", m.renderStatusBar(80))
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

// judgedCard drives one command through a classifier that says no, and
// returns the session holding the card it became, with every decision the
// record was told about so far.
func judgedCard(t *testing.T, ran *[]string, why string) (Model, *[][2]string) {
	t.Helper()
	decisions := &[][2]string{}
	m := recordingDecisions(classifierModel(t, ran, &verdictProvider{decision: "deny", reason: why}), decisions)
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: tools.ExecCommandName, Arguments: `{"command":"npm run deploy"}`},
	}})
	m = updated.(Model)
	if m.state != stateClassifying {
		t.Fatalf("an unlisted command in auto mode should be classified, got state %d", m.state)
	}
	updated, _ = m.Update(driveClassifierDone(t, cmd))
	return updated.(Model), decisions
}

// With a person in front of the session, the classifier's no is a card, not
// a refusal: nothing runs, nothing reaches the model, and the card carries
// the sentence the reader is answering with the safe answer offered last
// (docs/capabilities/approvals-and-safety.md#the-classifier-fails-closed).
func TestClassifierFlow_ANoIsACardCarryingItsReason(t *testing.T) {
	const why = "the task asked for a release check, and this publishes one"
	var ran []string
	m, decisions := judgedCard(t, &ran, why)

	if m.state != stateConfirmRun {
		t.Fatalf("a judged no should be put to the person, got state %d", m.state)
	}
	if len(ran) != 0 {
		t.Fatal("nothing may run before the person answers")
	}
	if last := m.Messages()[len(m.Messages())-1]; last.Role == provider.RoleTool {
		t.Fatalf("the model should hear nothing until the card is answered, got %+v", last)
	}
	card := m.buildApprovalCard()
	if card.Judged != why {
		t.Fatalf("the card should carry the classifier's sentence, got %q", card.Judged)
	}
	if card.Return != "not now — it keeps waiting" {
		t.Fatalf("the safe answer should be named as a flagged card names it, got %q", card.Return)
	}
	// No grant, as a flagged card offers none: a grant answers before the
	// classifier, so it would wave the refused shape through unasked.
	handed := handover(t, m).buildApprovalCard()
	if handed.AllowAlways || !strings.Contains(handed.Footnote, "the classifier would refuse this") {
		t.Fatalf("a judged card should withhold [a] and say why, got always=%v footnote=%q", handed.AllowAlways, handed.Footnote)
	}
	view := stripANSI(card.View(80))
	if !strings.Contains(view, "classifier: the task asked for a release check") {
		t.Fatalf("the classifier line should be drawn on the card:\n%s", view)
	}
	// The classifier's verdict is the record's first row, as the verdict it was.
	want := [][2]string{{observe.DecisionDeny, observe.ReasonClassifier}}
	if !slices.Equal(*decisions, want) {
		t.Fatalf("recorded %v, want %v", *decisions, want)
	}
	assertNoFrameDenial(t, m, "the card")
}

// The person's answer is a row of its own beside the classifier's, so the
// record can say how often the two disagreed — and either answer does
// exactly what it does on any other card.
func TestClassifierFlow_ThePersonsAnswerIsRecordedBesideTheVerdict(t *testing.T) {
	const why = "the task asked for a release check, not a release"
	t.Run("overturned", func(t *testing.T) {
		var ran []string
		m, decisions := judgedCard(t, &ran, why)
		updated, cmd := handover(t, m).Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
		if updated.(Model).state != stateRunningCmd {
			t.Fatalf("the person's yes should start the command, got state %d", updated.(Model).state)
		}
		driveCmdDone(t, cmd)
		if len(ran) != 1 || ran[0] != "npm run deploy" {
			t.Fatalf("the person's yes should run the command, got %v", ran)
		}
		want := [][2]string{{observe.DecisionDeny, observe.ReasonClassifier}, {observe.DecisionAllow, observe.ReasonUser}}
		if !slices.Equal(*decisions, want) {
			t.Fatalf("recorded %v, want %v", *decisions, want)
		}
	})
	t.Run("upheld", func(t *testing.T) {
		var ran []string
		m, decisions := judgedCard(t, &ran, why)
		updated, _ := handover(t, m).Update(keyN())
		m = updated.(Model)
		if len(ran) != 0 {
			t.Fatalf("the person's no should refuse the command, got %v", ran)
		}
		last := m.Messages()[len(m.Messages())-1]
		if last.Role != provider.RoleTool || !strings.Contains(last.Content, "the user declined to run this command") {
			t.Fatalf("the model reads the person's no as it reads any card's, got %+v", last)
		}
		want := [][2]string{{observe.DecisionDeny, observe.ReasonClassifier}, {observe.DecisionDeny, observe.ReasonUser}}
		if !slices.Equal(*decisions, want) {
			t.Fatalf("recorded %v, want %v", *decisions, want)
		}
	})
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
		if e.kind == entrySystem && strings.Contains(e.text, "classifier unavailable") {
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
	m := classifierModel(t, &ran, p)
	m.policy.denylist = []string{"rm"}

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
	m := batchModel(t)
	m.wiring.Runner = legacyRunner(func(ctx context.Context, cmd string) (string, int) {
		t.Fatalf("nothing may run on a verdict that never came, but %q did", cmd)
		return "", 0
	})
	m.wiring.Ledger = ledger
	m.classifier.judge = agent.NewClassifier(ledger.For(&verdictProvider{err: errors.New("api down")}, meter.SourceClassifier),
		agent.ClassifierConfig{Model: "judge"})
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
			case e.kind == entrySystem && strings.Contains(e.text, "classifier unavailable"):
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
