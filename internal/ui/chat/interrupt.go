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
	"time"

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
// a decision is answered or left, so a card can never inherit the gate a
// previous one was given.
func (m *Model) releaseDecision() { m.decisionHeld, m.heldOnArrival = false, false }

// draftQuiet is how long the keyboard must have gone untouched before an
// arriving decision is allowed to hold it. The window is not about the reader
// who is watching a turn work — they have been quiet for the whole turn, and
// any window at all catches them. It is about the one who stopped typing a
// moment ago and is still mid-thought: their draft is empty because the
// backspace emptied it, and the next thing they press is a letter meant for
// the sentence they are about to start.
const draftQuiet = time.Second

// arrivesHeld reports whether a decision arriving now takes the keyboard
// rather than waiting for ctrl+g.
//
// §7b's rule is about a card landing on top of a sentence: `y` stays a letter
// because it belongs in the sentence, and the reader is charged one ctrl+g
// for the protection. But most cards do not land on a sentence. They land
// while the reader is watching a turn work with an empty box, and there the
// toll buys nothing — there is no sentence for the letter to belong to, and
// the reader who came to press `y` presses it twice.
//
// So the arrival state is decided by whether there is anything to protect.
// With a sentence in the box, or a keyboard still warm from one, nothing
// changes: the card arrives ungated, exactly as before. With neither, the
// card arrives holding the keyboard — which is not a departure from §7b but
// its other branch, the one every takeover surface takes: a surface whose
// keys are live is a surface that holds the keyboard exclusively.
func (m Model) arrivesHeld() bool {
	if strings.TrimSpace(m.input.Value()) != "" {
		return false
	}
	return m.summoned() || time.Since(m.lastKeypress) >= draftQuiet
}

// summoned reports whether the decision on screen is one the reader asked
// for: the /run confirm, which is the user's own command and has no agent
// request behind it. A card you opened yourself holds the keyboard whatever
// the clock says — the keystroke the quiet window would count against it is
// the very keystroke that summoned it.
func (m Model) summoned() bool {
	return m.pendingApproval == nil && m.pendingRun != ""
}

// armDecision decides where the keyboard is as the turn arrives at s. It is
// the one place a turn-state arrival is answered, so no surface reached
// through setTurnState can drift into an answer of its own.
//
// Departures pass through here too, and land on false: leaving a decision
// gives the draft the keyboard back whatever it was doing. So does a decision
// the turn reaches while a surface has the screen — it has not arrived in
// front of anyone yet, and leaveSurface is where it does.
func (m *Model) armDecision(s state) {
	if m.state.isSurface() || !m.arrivalGates(s) {
		m.decisionHeld, m.heldOnArrival = false, false
		return
	}
	m.decisionHeld = m.arrivesHeld()
	m.heldOnArrival = m.decisionHeld
}

// armArrival arms a decision that arrives outside the turn state machine: a
// child agent's routed approval, which is a queue rather than a state (§9c).
// It answers the same questions setTurnState's arming does — a decision
// already holding the keyboard keeps it, one landing behind a surface has not
// landed in front of anyone, and otherwise it depends on whether there is a
// sentence to protect.
func (m *Model) armArrival() {
	if m.decisionHeld || m.state.isSurface() {
		return
	}
	m.decisionHeld = m.arrivesHeld()
	m.heldOnArrival = m.decisionHeld
}

// arrivalGates reports whether a decision arriving at s is one that may take
// the keyboard by arriving at all.
//
// Only the approval card and the /run confirm are. Their question is the one
// a reader walks up to a screen to answer, and the answer is one letter. The
// plan card (§4d) and the memory proposal (S-070) both take typed input — a
// choice moved with j/k, a note written into a field — so a card that took
// the keyboard would be a card eating a sentence, which is the hazard this
// whole rule exists for. They keep the handover, and it costs them nothing:
// they arrive once, not once per tool call.
func (m Model) arrivalGates(s state) bool {
	return s == stateConfirmRun && m.memoryAsk == nil
}

// releaseToDraft hands the keyboard back and delivers the keystroke to the
// draft (§7b). It is what a card holding the keyboard by arrival does with a
// key it has no answer for: the reader never asked for the keyboard, so the
// letter they typed is the start of a sentence and belongs in the box, not
// dropped on the floor while they look at a card.
func (m Model) releaseToDraft(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.releaseDecision()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncCompletions()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	return m, cmd
}

// gateDecision gives the card the keyboard. The draft is not touched: it
// keeps every character and its cursor, and gets them back when the decision
// is answered.
func (m Model) gateDecision() (tea.Model, tea.Cmd) {
	if !m.interruptShowing() {
		return m, nil
	}
	m.decisionHeld, m.heldOnArrival = true, false
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
	card.Handover = handoverKey
	if m.decisionUngated() {
		card.NotYetLive = true
		return
	}
	// A card that took the keyboard by arriving on an idle draft claims less
	// than one the reader handed it (§7b): it says so on the card, and says
	// what the handover would still buy.
	card.HeldOnArrival = m.heldOnArrival
	if m.escLeavesWaiting() {
		card.Return = "[esc] back to your draft — the decision stays waiting, nothing is denied"
	}
}
