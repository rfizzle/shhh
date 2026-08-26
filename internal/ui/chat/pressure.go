package chat

// The context-pressure card (S-108, DESIGN-TUI.md §17b).
//
// S-055 already trimmed the oldest tool results when the window filled, and
// said so afterwards in one grey line. That is a notice about something that
// already happened to your conversation, which is the wrong shape: by the
// time it is printed the decision has been made for you, and the only two
// remedies that actually recover the window — compacting, and starting again
// — were things you had to know to type.
//
// The alert threshold gets a decision surface instead. It states the
// occupancy, itemises where the window went (S-093's accounting, so it cannot
// quote a number the rails disagree with), predicts what compaction would
// recover, and offers the three answers. The warning threshold keeps what it
// had: a colour change in the rails and nothing that stops you.
//
// It appears once per crossing. Re-arming happens only after the occupancy
// has fallen back under the threshold, so a session that answers "keep going"
// is not asked again every turn — which is the difference between a decision
// surface and a nag.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// armPressureCard opens the card when the turn that just closed left the
// window at the alert threshold. It is called from the one transition every
// turn ends through (setTurnState), so no path back to the input can skip it
// — and it declines to open over anything that is already using the screen,
// because a card that steals a surface is worse than a card that waits a turn.
func (m *Model) armPressureCard() {
	if m.contextSeverity() < 2 {
		// Back under the threshold: the next crossing is a new crossing.
		m.pressureShown = false
		return
	}
	if m.pressureShown || m.pressure != nil {
		return
	}
	// Something else owns the screen, the session is looking at a child, or
	// the turn is about to continue with what was typed into it — all three
	// are reasons to say nothing now and ask at the next turn's end.
	if m.state.isSurface() || m.attachedTo != "" || m.agentList != nil ||
		m.activeChildAsk() != nil || len(m.steering) > 0 {
		return
	}
	// So is a turn that stopped at its round limit: that checkpoint is a
	// decision of its own, and two at once is one too many (S-109).
	if m.pausedAtRoundLimit() {
		return
	}
	card := m.pressureCardData()
	if card == nil {
		return
	}
	m.pressure = card
	m.pressureShown = true
	m.enterSurface(statePressure)
	m.syncViewport()
}

// pressureCardData builds the card from the session's own accounting. It
// returns nil when there is no window to measure against, which is the one
// case where every number on the card would be invented.
func (m Model) pressureCardData() *components.PressureCard {
	window := m.contextWindow()
	b := m.contextAccounting()
	total := b.total()
	if window <= 0 || total <= 0 {
		return nil
	}
	card := components.PressureCard{
		Pct:       int(min(total*100/window, 100)),
		Tokens:    total,
		Window:    window,
		Warn:      warnThresholdPercent,
		Alert:     trimThresholdPercent,
		Estimated: !b.Reported,
		Rows:      m.pressureRows(b),
		Keys: []components.KeyOffer{
			{Key: "[enter]", Label: "compact now"},
			{Key: "[n]", Label: "new session"},
			{Key: "[esc]", Label: "keep going"},
		},
	}
	card.Keeps = m.compactKeepsClause()
	card.Drops = compactDropsClause(b)
	if recovers := m.compactRecovers(b); recovers > 0 {
		card.Recovers = recovers
		card.RecoversPct = int(recovers * 100 / window)
	}
	card.Continuing = continuingClause(b)
	return &card
}

// pressureRows are S-093's categories in the card's own words, largest first,
// with the empty ones dropped. The wording differs from /stats' because the
// card is a sentence about the session and /stats is a table; the numbers are
// the same numbers.
func (m Model) pressureRows(b contextBreakdown) []components.PressureRow {
	messages, results := m.messageCounts()
	rows := []components.PressureRow{
		{Tokens: b.ToolResults, Label: "tool output", Detail: countDetail(results, "result")},
		{Tokens: b.Messages, Label: "the conversation", Detail: countDetail(messages, "message")},
		{Tokens: b.System, Label: "system prompt"},
		{Tokens: b.Project, Label: "project context"},
		{Tokens: b.Tools, Label: "tool definitions"},
	}
	out := make([]components.PressureRow, 0, len(rows))
	for _, r := range rows {
		if r.Tokens > 0 {
			out = append(out, r)
		}
	}
	// A stable sort by size: the biggest category is the one you can act on.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Tokens > out[j-1].Tokens; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// countDetail is the clause after the em dash, or nothing at all when there
// is no count to state.
func countDetail(n int, noun string) string {
	if n <= 0 {
		return ""
	}
	return plural(n, noun)
}

// messageCounts is how many messages the conversation is and how many tool
// results are in it, which is what makes the two largest categories
// characterisable rather than just large.
func (m Model) messageCounts() (messages, results int) {
	for i, msg := range m.agent.Messages() {
		switch {
		case i == 0 && msg.Role == provider.RoleSystem:
		case msg.Role == provider.RoleTool:
			results++
		default:
			messages++
		}
	}
	return messages, results
}

// compactKeepsClause is what compaction preserves, in the order it matters:
// the plan being carried out, the files the session has changed, and the most
// recent turns. Each clause is only there when the thing it names is, because
// a card that promises to keep a plan there is no plan of is a card that
// cannot be trusted about the changed files either.
func (m Model) compactKeepsClause() string {
	var parts []string
	if m.planRun != nil {
		parts = append(parts, "the plan")
	}
	if files, _, _ := m.changes.Totals(); files > 0 {
		parts = append(parts, plural(files, "changed file"))
	}
	switch kept := m.keptTurnCount(m.compactKeep()); {
	case kept == 1:
		parts = append(parts, "the last turn")
	case kept > 1:
		parts = append(parts, "the last "+plural(kept, "turn"))
	}
	return joinClauses(parts)
}

// joinClauses reads a list as a sentence: `a`, `a and b`, `a, b and c`.
func joinClauses(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// continuingClause is the honest half of invariant 3: keeping going is
// allowed, and it is not free. What it costs depends on what the trim has
// left to work with — S-055 elides tool results and nothing else, so a
// conversation that is all prose has nothing to give and the next request
// that overruns the window fails instead of shrinking.
func continuingClause(b contextBreakdown) string {
	if b.ToolResults > 0 {
		return "keeping going asks nothing further — the oldest tool output is elided before each request from here, and what falls out does not come back"
	}
	return "keeping going asks nothing further — there is no tool output left to trim, so the first request that overruns the window will fail rather than shrink"
}

// compactDropsClause names what the summary replaces, from what is actually
// in the window: a session that has run no tools has no tool output to drop,
// and a card that says otherwise is describing a different session.
func compactDropsClause(b contextBreakdown) string {
	switch {
	case b.ToolResults > 0 && b.Messages > 0:
		return "the older turns and their tool output"
	case b.ToolResults > 0:
		return "the older tool output"
	case b.Messages > 0:
		return "the older turns"
	}
	return ""
}

// compactRecovers predicts what compaction frees: everything but the parts
// that survive it — the system prompt, the project context inside it, the
// tool definitions, the turns kept verbatim — less an allowance for the
// summary that replaces the rest. The card says "about" because the summary
// has not been written yet, and that is the one term nobody can know in
// advance.
func (m Model) compactRecovers(b contextBreakdown) int64 {
	kept := estimateMessageTokens(m.compactKeep())
	after := b.System + b.Project + b.Tools + kept + compactSummaryEstimate
	if recovers := b.total() - after; recovers > 0 {
		return recovers
	}
	return 0
}

// updatePressure routes keys while the card is up.
func (m Model) updatePressure(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pressure == nil {
		return m.closePressure()
	}
	if msg.String() == "ctrl+d" {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.pressure.Update(msg)
	if !done {
		return m, nil
	}
	pressed, _ := result.(string)
	updated, cmd := m.closePressure()
	next := updated.(Model)
	switch pressed {
	case "enter":
		return next.startCompact()
	case "n":
		return next.pressureNewSession()
	}
	// Esc keeps going, and says nothing: the answer that changes nothing
	// should not leave a line in the transcript claiming it did.
	return next, cmd
}

// closePressure takes the card down and hands the screen back to the turn.
func (m Model) closePressure() (tea.Model, tea.Cmd) {
	m.pressure = nil
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// pressureNewSession is `[n]`: save what the session has said, then start
// over with an empty conversation. The save is the autosave slot, which is
// what `shhh chat --continue` reopens — a new session that lost the old one
// is not an offer, it is a mistake with a key bound to it.
func (m Model) pressureNewSession() (tea.Model, tea.Cmd) {
	save := m.autosaveCmd()
	note := "Started a new conversation."
	if save != nil {
		note = fmt.Sprintf("Saved the conversation as %q and started a new one; `shhh chat --continue` reopens it.", AutosaveName)
	}
	m.clearConversation()
	// The window is empty again, so the next crossing is a new crossing.
	m.pressureShown = false
	m.appendEntry(entry{kind: entrySystem, text: note})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, save
}

// pressureLines renders the card, one row per line.
func (m Model) pressureLines() []string {
	if m.pressure == nil {
		return nil
	}
	return strings.Split(m.pressure.View(m.contentWidth()), "\n")
}

// renderPressure pads the card to the bottom panel's height.
func (m Model) renderPressure() string {
	lines := m.pressureLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}
