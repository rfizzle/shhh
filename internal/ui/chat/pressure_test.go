package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// pressureModel is a session whose conversation is already past the alert
// threshold and cannot be trimmed under it: the weight is in the turns
// themselves, not in tool results, which is what makes the card the only
// remedy left.
func pressureModel(t *testing.T, width int) Model {
	t.Helper()
	big := strings.Repeat("y ", 30000) // ~30k tokens over two messages
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("system prompt. ", 40)},
		{Role: provider.RoleUser, Content: big},
		{Role: provider.RoleAssistant, Content: big},
	}, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = updated.(Model)
	if m.contextSeverity() != 2 {
		t.Fatalf("fixture should already be at the alert threshold, severity %d", m.contextSeverity())
	}
	return m
}

// endTurn runs one turn to completion, which is the transition the card is
// armed from.
func endTurn(t *testing.T, m Model, text string) Model {
	t.Helper()
	m = sendText(t, m, text)
	updated, _ := m.Update(doneMsg{})
	return updated.(Model)
}

func TestPressure_ArmsWhenATurnEndsAtTheAlertThreshold(t *testing.T) {
	m := endTurn(t, pressureModel(t, 100), "carry on")

	if m.state != statePressure || m.pressure == nil {
		t.Fatalf("a turn ending at the alert threshold should raise the card, state=%d card=%v", m.state, m.pressure)
	}
	if m.turnState() != stateInput {
		t.Fatalf("the turn itself is over; only the screen is borrowed, turnState=%d", m.turnState())
	}
	view := m.pressure.View(80)
	if !strings.Contains(view, "Context is nearly full") {
		t.Fatalf("the card should name itself, got:\n%s", view)
	}
}

// Once per crossing, not once per turn: a session that answered "keep going"
// is not asked again until the window has actually come back down.
func TestPressure_AsksOncePerCrossing(t *testing.T) {
	m := endTurn(t, pressureModel(t, 100), "carry on")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state == statePressure || m.pressure != nil {
		t.Fatal("esc should take the card down")
	}
	if m.state != stateInput {
		t.Fatalf("esc should hand the screen back to the input, got %d", m.state)
	}

	m = endTurn(t, m, "and again")
	if m.state == statePressure {
		t.Fatal("the same crossing should not raise the card a second time")
	}

	// Back under the threshold: the next crossing is a new crossing.
	m.startNewSession()
	m.armPressureCard()
	if m.pressureShown {
		t.Fatal("falling back under the threshold should re-arm the card")
	}
}

// A surface already has the screen, so the card waits rather than stealing
// it: it will be raised at the end of the next turn.
func TestPressure_DoesNotOpenOverAnotherSurface(t *testing.T) {
	m := pressureModel(t, 100)
	m.enterSurface(stateFocus)
	m.armPressureCard()
	if m.state != stateFocus || m.pressure != nil {
		t.Fatalf("the card should not take a surface's screen, state=%d card=%v", m.state, m.pressure)
	}
	if m.pressureShown {
		t.Fatal("declining to open must not spend the crossing")
	}
}

func TestPressure_BreakdownComesFromTheAccounting(t *testing.T) {
	m := pressureModel(t, 100)
	card := m.pressureCardData()
	if card == nil {
		t.Fatal("a session with a window and a conversation should have a card")
	}

	b := m.contextAccounting()
	if card.Tokens != b.total() || card.Window != m.contextWindow() {
		t.Fatalf("the card should quote the accounting: %d/%d vs %d/%d",
			card.Tokens, card.Window, b.total(), m.contextWindow())
	}
	if card.Warn != warnThresholdPercent || card.Alert != trimThresholdPercent {
		t.Fatalf("the card's thresholds should be the rails': %d/%d", card.Warn, card.Alert)
	}
	var sum int64
	labels := map[string]bool{}
	for _, r := range card.Rows {
		sum += r.Tokens
		labels[r.Label] = true
	}
	if sum != b.total() {
		t.Fatalf("the categories should account for the whole total, %d of %d", sum, b.total())
	}
	for _, want := range []string{"the conversation", "system prompt"} {
		if !labels[want] {
			t.Fatalf("category %q missing from %+v", want, card.Rows)
		}
	}
	if len(card.Rows) > 1 && card.Rows[0].Tokens < card.Rows[1].Tokens {
		t.Fatalf("categories should be largest first, got %+v", card.Rows)
	}
	// This fixture is all prose, so there is nothing for the trim to elide
	// and the consequence is the honest one: the next overrun fails.
	if !strings.Contains(card.Continuing, "will fail rather than shrink") {
		t.Fatalf("keeping going should state its consequence, got %q", card.Continuing)
	}
	withTools := contextBreakdown{Messages: 100, ToolResults: 900}
	if !strings.Contains(continuingClause(withTools), "does not come back") {
		t.Fatalf("a session with tool output loses it silently, got %q", continuingClause(withTools))
	}
}

// Every offer on the card is one the session can honour, and every clause in
// what it promises to keep names something that is actually there.
func TestPressure_KeepsClauseNamesOnlyWhatExists(t *testing.T) {
	m := pressureModel(t, 100)
	if got := m.compactKeepsClause(); strings.Contains(got, "the plan") {
		t.Fatalf("a session with no plan should not promise to keep one, got %q", got)
	}
	if got := m.compactKeepsClause(); strings.Contains(got, "changed file") {
		t.Fatalf("a session that changed nothing should not promise to keep it, got %q", got)
	}

	// One more turn gives the tail something to keep.
	m = endTurn(t, m, "another turn")
	if got := m.compactKeepsClause(); !strings.Contains(got, "the last") {
		t.Fatalf("the kept turns should be named, got %q", got)
	}
}

func TestPressure_EnterCompactsAndNKeepsTheSessionSaved(t *testing.T) {
	m := endTurn(t, pressureModel(t, 100), "carry on")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	after := updated.(Model)
	if !after.compacting {
		t.Fatal("[enter] should start a compaction")
	}

	m = endTurn(t, pressureModel(t, 100), "carry on")
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	after = updated.(Model)
	if after.state != stateInput || after.pressure != nil {
		t.Fatalf("[n] should close the card, state=%d", after.state)
	}
	if len(after.Messages()) != 1 {
		t.Fatalf("[n] should start a fresh conversation, got %d messages", len(after.Messages()))
	}
	// Without a store there is nowhere to save to, and the notice says only
	// what happened: the save clause is an offer, not a claim.
	last := after.transcript[len(after.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "new session") {
		t.Fatalf("[n] should say what it did with the old conversation, got %+v", last)
	}
	if after.pressureShown {
		t.Fatal("an empty conversation is a new crossing")
	}
}

// The card promises compaction keeps the most recent turns; this is the
// promise being kept.
func TestCompact_KeepsTheMostRecentTurnsVerbatim(t *testing.T) {
	stream := summaryStream("the summary")
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "first answer"},
		{Role: provider.RoleUser, Content: "second"},
		{Role: provider.RoleAssistant, Content: "second answer"},
		{Role: provider.RoleUser, Content: "third"},
		{Role: provider.RoleAssistant, Content: "third answer"},
	}, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	m = driveCompact(t, m)

	msgs := m.Messages()
	if len(msgs) != 6 {
		t.Fatalf("system + summary + the last two turns, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != provider.RoleSystem {
		t.Fatal("the system prompt must survive")
	}
	if !strings.Contains(msgs[1].Content, "the summary") {
		t.Fatalf("the summary should lead the rebuilt conversation, got %q", msgs[1].Content)
	}
	if msgs[2].Content != "second" || msgs[5].Content != "third answer" {
		t.Fatalf("the last two turns should be verbatim, got %+v", msgs[2:])
	}
	// The kept turns are on screen too: a transcript that lost them would say
	// the conversation starts at the summary, and the next request would not.
	var texts []string
	for _, e := range m.transcript {
		texts = append(texts, e.text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"the last 2 turns", "second", "third answer"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the transcript should carry %q, got:\n%s", want, joined)
		}
	}
}

// A conversation with nothing behind the tail has no tail: keeping it all
// would leave the summary describing nothing.
func TestCompact_KeepsNothingWhenThereIsNoOlderConversation(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "only"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}, summaryStream("the summary"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	if kept := m.compactKeep(); len(kept) != 0 {
		t.Fatalf("the whole conversation is not a tail, got %+v", kept)
	}
}

// A tail bigger than its share of the window is not a tail either: keeping it
// would compact the conversation into the corner it started in.
func TestCompact_KeepsNothingWhenTheTailIsTooBig(t *testing.T) {
	big := strings.Repeat("y ", 40000)
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Content: big},
	}, summaryStream("the summary"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	if kept := m.compactKeep(); len(kept) != 0 {
		t.Fatalf("a tail over the budget should not be kept, got %d messages", len(kept))
	}
}

// The plan is the other thing the card promises compaction keeps. Its steps'
// states are read off a transcript compaction discards, so the run carries
// them across.
func TestCompact_CarriesThePlanChecklistAcross(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "do it"},
		{Role: provider.RoleAssistant, Content: "done"},
	}, summaryStream("the summary"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)

	run := newPlanRun(plan.Parse(planFixture), len(m.transcript))
	if run == nil {
		t.Fatal("the plan fixture should be structured")
	}
	// The first step has been carried out; that is what has to survive.
	run.carried = map[int]components.InspectorPlanStep{
		run.doc.Steps[0].Number: {
			Number: run.doc.Steps[0].Number, Title: run.doc.Steps[0].Title,
			State: components.PlanStepDone, Elapsed: "1.2s",
		},
	}
	m.planRun = run

	m = driveCompact(t, m)

	if m.planRun == nil {
		t.Fatal("compaction should keep the plan it promised to keep")
	}
	if m.planRun.start != len(m.transcript) {
		t.Fatalf("the run should be rebased on the new transcript, start=%d len=%d",
			m.planRun.start, len(m.transcript))
	}
	steps := m.planChecklist()
	if len(steps) == 0 {
		t.Fatal("the checklist should survive")
	}
	if steps[0].State != components.PlanStepDone {
		t.Fatalf("a step carried out before the compaction is still carried out, got %+v", steps[0])
	}
	if len(steps) > 1 && steps[1].State != components.PlanStepQueued {
		t.Fatalf("a step nobody reached is still queued, got %+v", steps[1])
	}
}

// summaryStream answers one compaction request with a summary.
func summaryStream(summary string) StreamFunc {
	return func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: summary}
		ch <- provider.StreamEvent{Done: true, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 10}}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}
