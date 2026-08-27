package chat

// When a decision lands mid-sentence (S-117, DESIGN-TUI.md §7b).
//
// An approval arrives when the agent needs it, not when the reader is ready,
// so roughly once a session it lands on top of a half-typed sentence. Until
// this file existed the card took the keyboard the moment it appeared, which
// made the most dangerous state in the product reachable by accident: a
// sentence containing the word "yes" could approve a shell command, and the
// draft it interrupted was neither visible nor reachable while it did.
//
// Invariant 5 is what makes the two halves compatible. The card arrives
// ungated: it is on screen, its keys render as not-yet-live, and the draft
// keeps the keyboard, so `y` is a letter and goes into the sentence. One key
// — ctrl+g, offered on the card and on the frame's own rail — hands the
// keyboard over. Gated, the card's keys are ordinary keys again and the draft
// is shown undressed beneath it, holding its characters and saying which one
// the cursor is on. Answering hands the keyboard straight back, at the same
// character.
//
// A labelled rail names whichever surface holds it, the way reading mode's
// does (§7a): DRAFT while the sentence has it, DECISION while the card does.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// handoverKey hands the keyboard from the draft to the decision on screen.
// It is a control chord for the reason every transfer in §7a is one: no
// sentence can produce it, so it can be live while the draft is.
const handoverKey = "ctrl+g"

// interruptShowing reports whether a decision that arrived unbidden is on
// screen: the approval card, the /run confirm, the plan card, or a child
// agent's routed approval. These are the surfaces that appear without being
// asked for, which is what makes them the ones invariant 5 is about.
func (m Model) interruptShowing() bool {
	switch m.state {
	case stateConfirmRun, statePlanApprove:
		return true
	}
	return m.activeChildAsk() != nil
}

// decisionUngated is the arrival state: the card is up and the draft still
// holds the keyboard.
func (m Model) decisionUngated() bool { return m.interruptShowing() && !m.decisionHeld }

// decisionGated is after ctrl+g: the card holds the keyboard and its keys are
// live.
func (m Model) decisionGated() bool { return m.interruptShowing() && m.decisionHeld }

// releaseDecision hands the keyboard back to the draft. It is called wherever
// a decision arrives or is answered, so a card can never inherit the gate a
// previous one was given.
func (m *Model) releaseDecision() { m.decisionHeld = false }

// gateDecision gives the card the keyboard. The draft is not touched: it
// keeps every character and its cursor, and gets them back when the decision
// is answered.
func (m Model) gateDecision() (tea.Model, tea.Cmd) {
	if !m.interruptShowing() {
		return m, nil
	}
	m.decisionHeld = true
	// The completion menu belongs to the draft, and the draft no longer has
	// the keyboard; leaving it open would offer keys nothing would answer.
	m.dismissCompletions()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	return m, nil
}

// ungateDecision hands the keyboard back with the decision still waiting.
// This is what esc does on a gated approval card (§7b): it leaves the
// decision unanswered rather than denying it, which is the distinction
// invariant 3 depends on — esc can only be the safe answer if the reader
// knows which surface it reached.
func (m Model) ungateDecision() (tea.Model, tea.Cmd) {
	m.releaseDecision()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	return m, nil
}

// routeDecision hands a key to whichever decision surface is on screen. It is
// only reached once the surface holds the keyboard.
func (m Model) routeDecision(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateConfirmRun:
		return m.updateConfirmRun(msg)
	case statePlanApprove:
		return m.updatePlanApprove(msg)
	}
	if ask := m.activeChildAsk(); ask != nil {
		return m.updateChildAsk(msg, ask)
	}
	return m, nil
}

// escLeavesWaiting reports whether esc on the gated surface hands the
// keyboard back rather than answering. It does on the approval cards, where
// leaving and denying are different acts (§7b). The plan card keeps its own
// esc: "keep planning" is already the answer that decides nothing and returns
// to the draft, so rebinding it would replace one safe answer with another
// and lose the mode it names. A memory proposal keeps its esc for the same
// reason.
func (m Model) escLeavesWaiting() bool {
	switch m.state {
	case stateConfirmRun:
		return m.memoryAsk == nil
	case statePlanApprove:
		return false
	}
	return m.activeChildAsk() != nil
}

// waitingCount is how many decisions are waiting for an answer, the card on
// screen included. It is what the frame's top rail counts (`⏸ 2 waiting`) and
// what the DECISION rail numbers, so the two never disagree.
func (m Model) waitingCount() int {
	n := len(m.childAsks)
	switch m.state {
	case stateConfirmRun, statePlanApprove:
		n++
	}
	return n
}

// decisionRailLabel names the surface holding the keyboard and its place in
// what is waiting. A lone decision has no position worth stating.
func (m Model) decisionRailLabel() string {
	if n := m.waitingCount(); n > 1 {
		return fmt.Sprintf("DECISION 1/%d", n)
	}
	return "DECISION"
}

// keyboardRail is the labelled rule that names the surface holding the
// keyboard (§7b): four cells of rule, the label in its own spaces, then the
// rule to the edge. Too narrow for the label, it falls back to a plain
// divider rather than clipping the word that carries the meaning — the same
// judgement the reading rail makes (invariant 1).
func keyboardRail(label string, width int) string {
	if width <= 0 {
		return ""
	}
	rendered := readingLabelStyle.Render(label)
	lw := lipgloss.Width(rendered)
	if width < lw+8 {
		return dividerStyle(width)
	}
	return readingRuleStyle.Render(strings.Repeat("─", 4)) + " " + rendered + " " +
		readingRuleStyle.Render(strings.Repeat("─", width-lw-6))
}

// dressDecision puts the rail that names the keyboard's owner around a
// decision surface's rows. Ungated the card is the whole panel — the DRAFT
// rail and the live frame under it are assembled by View, because the draft
// is still on screen and still typing. Gated, the rail names the decision and
// the draft it is holding is shown undressed beneath it.
func (m Model) dressDecision(card []string, width int) []string {
	if !m.decisionGated() {
		return card
	}
	lines := append([]string{keyboardRail(m.decisionRailLabel(), width)}, card...)
	if draft := m.undressedDraft(width); len(draft) > 0 {
		lines = append(lines, "")
		lines = append(lines, draft...)
	}
	return lines
}

// gatedExtraRows is what dressDecision adds to a panel, so the panel's bound
// can pay for it instead of clipping the draft off the bottom.
func (m Model) gatedExtraRows() int {
	if !m.decisionGated() {
		return 0
	}
	if draft := m.undressedDraft(m.contentWidth()); len(draft) > 0 {
		return 1 + 1 + len(draft)
	}
	return 1
}

// undressedDraft renders the draft while the decision holds the keyboard: the
// frame drops its mode colour and its block cursor and keeps every character,
// and its rail states the position it is holding, so the reader can see that
// nothing moved while they were not typing into it (§7b).
//
// An empty draft has nothing to hold, and a row saying so would be a row
// spent on the absence of one — the block is rendered only when there is
// something to preserve.
func (m Model) undressedDraft(width int) []string {
	value := m.input.Value()
	if value == "" || width < minFrameWidth {
		return nil
	}
	inner := width - frameSideWidth
	text := strings.ReplaceAll(value, "\n", " ")
	row := frameIdleStyle.Render("▸ ") + draftHeldStyle.Render(text)
	row = clipRow(row, inner)
	pad := strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
	return []string{
		frameRail(frameIdleStyle, "╭", "╮", " "+frameIdleStyle.Render(m.frameIdentity())+" ", "", width),
		frameIdleStyle.Render("│") + " " + row + pad + " " + frameIdleStyle.Render("│"),
		frameRail(frameIdleStyle, "╰", "╯", " "+frameIdleStyle.Render(m.draftPosition())+" ", "", width),
	}
}

// draftPosition is the rail under the undressed draft: how much is held and
// where the cursor is standing in it. It is the evidence that answering a
// decision costs the sentence nothing.
func (m Model) draftPosition() string {
	chars := len([]rune(m.input.Value()))
	noun := "characters"
	if chars == 1 {
		noun = "character"
	}
	return fmt.Sprintf("%d %s, cursor at %d", chars, noun, m.draftCursor())
}

// draftCursor is the cursor's offset in the whole draft, counted in runes.
// The textarea reports its position per logical line, so the lines above are
// counted with the newline that ends each of them.
func (m Model) draftCursor() int {
	lines := strings.Split(m.input.Value(), "\n")
	row := min(m.input.Line(), len(lines)-1)
	n := 0
	for i := 0; i < row; i++ {
		n += len([]rune(lines[i])) + 1
	}
	info := m.input.LineInfo()
	return n + min(info.StartColumn+info.ColumnOffset, len([]rune(lines[row])))
}

// interruptLines is the decision card riding above a live frame, ungated.
func (m Model) interruptLines() []string {
	switch m.state {
	case stateConfirmRun:
		return m.confirmLines()
	case statePlanApprove:
		return m.planApproveLines()
	}
	if ask := m.activeChildAsk(); ask != nil {
		return m.childAskLines(ask)
	}
	return nil
}

// interruptHeight is what the ungated card and its rail add to the bottom
// panel, so the layout accounting pays for them the way it pays for the
// notice rail.
func (m Model) interruptHeight() int {
	if !m.decisionUngated() || !m.frameShowing() {
		return 0
	}
	if n := len(m.interruptLines()); n > 0 {
		return n + 1
	}
	return 0
}

// renderInterrupt is the ungated card with the DRAFT rail under it. The card
// is bordered but undressed; the frame below keeps the accent, because the
// frame is where the keystrokes are going.
func (m Model) renderInterrupt(width int) string {
	if !m.decisionUngated() {
		return ""
	}
	lines := m.interruptLines()
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n" + keyboardRail("DRAFT", width)
}

// applyNotYetLive puts a card into the state the keyboard says it is in. It
// is called from every card builder, so no surface can render its keys as
// live while the draft is the one being typed into.
// The card keeps the bound it would have had on its own whether or not it
// holds the keyboard: a decision the reader cannot read is not one they can
// decide, so the transcript above gives up the rows instead.
func (m Model) applyNotYetLive(card *components.ApprovalCard) {
	card.MaxLines = m.maxConfirmPanelHeight()
	if m.decisionUngated() {
		card.NotYetLive, card.Handover = true, handoverKey
		return
	}
	if m.escLeavesWaiting() {
		card.Return = "[esc] back to your draft — the decision stays waiting, nothing is denied"
	}
}
