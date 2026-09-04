package chat

// The transcript, rendered.
//
// One entry at a time, cached by width, into the lines the pane scrolls. The
// cache is why the render is a method on a pointer receiver: re-wrapping a
// long session at every frame is the stutter, so a line is wrapped once per
// width and kept until something invalidates it.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func (m *Model) appendEntry(e entry) {
	// Every entry knows the turn it belongs to, so a row that outlives its
	// turn can still name it — the rail's alerts do. An entry
	// that already carries one (a close block, a round-limit pause) keeps it.
	if e.turn == 0 {
		e.turn = m.turnCount
	}
	m.transcript = append(m.transcript, e)
}

func (m *Model) resetTranscript() {
	m.transcript = nil
	// The index a fan-out would have converted points into a transcript that
	// no longer exists, and so does the round's think row and the run's row.
	m.spawnRow = 0
	m.thinkIdx = 0
	m.todoRunner.rowIdx, m.todoRunner.followUpRow = 0, 0
	// The checklist is read off the transcript, so a transcript that is gone
	// takes the approved plan with it rather than pointing at entries that no
	// longer exist.
	m.planRun = nil
	// A selection is a pair of coordinates into a render of this transcript;
	// with the transcript gone they name nothing.
	m.clearSelection()
	m.invalidateRenderCache()
}

// flushStream repaints the transcript with as much of the arriving message as
// has landed, and forgets that a repaint was owed.
func (m *Model) flushStream() {
	m.streamDirty = false
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
}

// invalidateRenderCache forces the next renderHistory to re-render every
// entry (used when an entry's rendering changes in place, e.g. focus-mode
// expansion).
func (m *Model) invalidateRenderCache() {
	m.cached.reset()
}

// renderEntry renders one entry's own lines, always ending in exactly one
// newline and never in a trailing blank line. Spacing between entries is not
// an entry's business — separatorBefore owns it, so every caller that
// concatenates entries gets the same rhythm.
func (m Model) renderEntry(e entry, width int) string {
	return m.renderEntryKeys(e, width, false)
}

// renderEntryKeys is the same, told whether the row's own keys are live —
// which they are only while reading mode's cursor is standing on this row
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
// Everywhere else the row is beside a live draft, `v` is a letter, and the
// row says so: its keys go grey and the key that hands the keyboard over is
// offered in the live treatment beside them.
func (m Model) renderEntryKeys(e entry, width int, keysLive bool) string {
	return m.renderEntryDetail(e, width, keysLive, false)
}

// renderEntryDetail is the same again, told whether the step this row belongs
// to has its detail open. Only the activity rows can answer to
// it; every other kind of entry renders the same inside an opened step as
// outside one, because a step opens the bodies of its calls and nothing else.
func (m Model) renderEntryDetail(e entry, width int, keysLive, stepDetail bool) string {
	switch e.kind {
	case entryUser:
		// The same renderer the model's prose gets. A sent message is not a
		// draft any more: it is a message in a transcript, and a reader
		// scrolling back has no way to tell which half of a conversation was
		// allowed to use a code fence. Someone who writes `--flag` or pastes
		// a fenced block into the box means it, and rendering it as plain
		// text is the transcript declining to read what it was given.
		//
		// The draft above deliberately does not do this — it stays a plain
		// editor, because a sentence being typed is bytes and a renderer that
		// reflowed them under the cursor would be fighting the writer.
		row := sty.User.Render("You") + "\n" + renderMarkdown(e.text, width) + "\n"
		if len(e.attached) > 0 {
			row += sty.SystemMsg.Render(clipRow("attached: "+strings.Join(e.attached, ", "), width)) + "\n"
		}
		return row
	case entryAssistant:
		return sty.Assistant.Render("Assistant") + "\n" + renderMarkdown(e.text, width) + "\n"
	case entryTool, entryCommand:
		// Compact one-row activity rendering; focus mode expands it,
		// and so does the step around it.
		return m.activityRowDetail(e, stepDetail).View(width) + "\n"
	case entryThink:
		// The round's reasoning, folded (think.go). Low verbosity draws no
		// row at all, and an entry that renders to nothing is not a unit, so
		// nothing downstream — spacing, line mapping, the reading cursor —
		// has to know it was skipped.
		if !m.showThink() {
			return ""
		}
		return m.thinkRowFor(e, width).View(width) + "\n"
	case entrySummary:
		if e.reading == nil {
			return ""
		}
		return m.summaryRowFor(e, width).View(width) + "\n"
	case entryTurnClose:
		if e.close == nil {
			return ""
		}
		c := *e.close
		c.KeysWaiting, c.Handover = !keysLive, m.rowHandover(keysLive)
		return c.View(width) + "\n"
	case entryFailure:
		return m.gateRow(m.failureRow(e), keysLive).View(width) + "\n"
	case entryStreamDrop:
		return m.gateRow(m.dropRow(e), keysLive).View(width) + "\n"
	case entryRoundPause:
		return m.gateRow(m.roundPauseRow(e), keysLive).View(width) + "\n"
	case entryFanout:
		block := m.fanoutBlockFor(e)
		if len(block.Lanes) == 0 {
			return ""
		}
		return block.View(width) + "\n"
	case entryTodoRun:
		if e.todorun == nil {
			return ""
		}
		return m.todoRunRowView(e, width, keysLive) + "\n"
	case entryDiff:
		if e.diff == nil {
			return ""
		}
		// While its full-screen view is showing, the transcript behind keeps
		// the bounded expanded form.
		if e.diff.Mode == components.DiffFull {
			return strings.Join(e.diff.ExpandedLines(width), "\n") + "\n"
		}
		return e.diff.View(width) + "\n"
	case entrySystem:
		return m.systemRow(e, width) + "\n"
	case entryError:
		return sty.Error.Render("Error: "+e.text) + "\n"
	}
	return ""
}

// systemRow renders a notice: its one line, and — for the notices that carry
// one — the body a reader opened it for, indented under the line the way
// every other detail body is indented rather than re-gridded.
//
// Most notices have no body and render exactly as they always did. The ones
// that do are the refusals whose short form is the useful one to scan and
// whose long form is the one to act on
// (docs/interface/principles.md#fold-never-hide).
func (m Model) systemRow(e entry, width int) string {
	row := sty.SystemMsg.Render(e.text)
	if !e.expanded {
		return row
	}
	lines := outputLines(e)
	if len(lines) == 0 {
		return row
	}
	indent := strings.Repeat(" ", components.GridDetailIndent)
	inner := max(width-components.GridDetailIndent, 1)
	// Wrapped rather than clipped: this body is a sentence, and half a
	// sentence is worse than a row that costs two lines.
	for _, l := range strings.Split(m.wordWrap(strings.Join(lines, "\n"), inner), "\n") {
		row += "\n" + indent + sty.SystemMsg.Render(l)
	}
	return row
}

// entryIsBlock reports whether an entry reads as a standalone block — a
// conversational turn, a diff, or a notice long enough to wrap onto its own
// lines — rather than as a row in the compact activity feed.
func entryIsBlock(e entry) bool {
	switch e.kind {
	case entryUser, entryAssistant, entryDiff, entryTurnClose, entryFanout, entryTodoRun:
		return true
	case entrySystem, entryError:
		return strings.Contains(strings.TrimSpace(e.text), "\n")
	}
	return false
}

// separatorBefore returns the spacing between two adjacent entries: one blank
// line whenever either side is a block, and nothing between feed rows, so
// activity rows and one-line notices pack tight while turns keep their air.
func separatorBefore(prev, cur entry) string {
	if entryIsBlock(prev) || entryIsBlock(cur) {
		return "\n"
	}
	return ""
}

// renderStatusBar renders the cockpit rail (
// docs/interface/surfaces.md#the-input-frame): the active mode, tool-round
// counter, context occupancy meter (coloured at the trim thresholds), usage
// and spend, queued steering, policy grants, and the sub-agent badge, with
// the model name right-aligned and dropped first when narrow.
func (m Model) renderStatusBar(width int) string {
	// Attached, the status bar scopes to the focused child.
	if m.attachedTo != "" && m.subagents != nil {
		return m.renderChildStatusBar(width)
	}
	return m.cockpitData(true).View(width)
}

// cockpitData assembles the cockpit segments. The frame's vitals rail
// omits the queued-steering extra — the notice rail carries it — so
// includeQueued is false there.
func (m Model) cockpitData(includeQueued bool) components.Cockpit {
	c := components.Cockpit{
		CtxPct:    -1,
		WarnPct:   warnThresholdPercent,
		AlertPct:  trimThresholdPercent,
		Reasoning: m.reasoningSegment(),
		Model:     m.modelName,
	}
	if m.turnState() == stateClassifying {
		c.Mode, c.ModeKind = "checking", components.CockpitChecking
	} else {
		c.Mode = strings.ReplaceAll(m.policy.mode.String(), "-", " ")
		switch m.policy.mode {
		case agent.ModeAcceptEdits, agent.ModeAuto:
			c.ModeKind = components.CockpitPermissive
		default:
			c.ModeKind = components.CockpitGated
		}
	}
	// Round counter shows only mid-turn, so long tool loops are visible — and
	// through a round-limit pause, where the ceiling is the thing being
	// decided. The grant on offer is stated beside it, so the counter says
	// both what the bound is and what taking the offer would make it.
	if m.agent.Rounds() > 0 && (m.turnState() != stateInput || m.pausedAtRoundLimit() || m.heldAtBoundary()) {
		c.Round = m.roundCounter()
	}
	// The session's account with the running turn's live estimate in it, so
	// the rail's counters and its spend move with the round instead of
	// standing still until it reports. While they are moving they print every
	// digit; at rest they go back to the shape a total is read in.
	sessionIn, sessionOut := m.liveSessionTokens()
	if sessionIn != 0 || sessionOut != 0 {
		c.Tokens = fmt.Sprintf("↑%s ↓%s", m.countLabel(sessionIn), m.countLabel(sessionOut))
		if label := m.spendLabel(sessionIn, sessionOut); strings.HasPrefix(label, "$") {
			c.Spend = label
		}
		if tokens := m.estimatedContextTokens(); tokens > 0 {
			c.CtxPct = int(tokens * 100 / m.contextWindow())
		}
	}
	// Steering messages waiting to be injected.
	if n := len(m.steering); n > 0 && includeQueued {
		c.Extra = append(c.Extra, fmt.Sprintf("queued %d", n))
	}
	// Active approval policy; absent in the default ask-everything
	// state.
	if p := m.policyLabel(); p != "" {
		c.Extra = append(c.Extra, p)
	}
	// Working sub-agents, with blocked-on-approval count.
	if m.subagents != nil {
		c.Agents, c.AgentsBlocked = m.subagents.ActiveCounts()
	}
	return c
}

// formatTokenCount is a settled count's shape, `412` or `41.2k`, which every
// listing and every rail at rest prints a total in.
func formatTokenCount(n int64) string { return components.FormatCount(n) }

// countLabel is the same count at the resolution the moment calls for: every
// digit while a turn is producing it, the rested shape once nothing is moving
// it (turnstatus.go).
func (m Model) countLabel(n int64) string {
	if m.countsLive() {
		return components.FormatLiveCount(n)
	}
	return formatTokenCount(n)
}

// renderHistoryLines is the transcript the pane shows: the history, with any
// application-owned selection lit over it (select.go). The highlight
// is the last thing applied and the first thing dropped — the raw render is
// what the clipboard extraction reads, so no selection styling can reach it.
//
// Lines rather than one string is the currency the pane takes,
// so nothing between the block cache and the screen splits the session into
// lines again.
func (m *Model) renderHistoryLines() []string {
	lines := m.renderHistoryRawLines()
	if !m.selectableSurface() {
		return lines
	}
	return m.applySelectionHighlight(lines)
}

// renderHistory and renderHistoryRaw are the same two renders as one string.
// Nothing on the drawing path uses them: they are what the goldens capture
// and what the tests read, joined back up from the lines above.
func (m *Model) renderHistory() string {
	return strings.Join(m.renderHistoryLines(), "\n")
}

func (m *Model) renderHistoryRaw() string {
	return strings.Join(m.renderHistoryRawLines(), "\n")
}

func (m *Model) renderHistoryRawLines() []string {
	if testHookRenderHistory != nil {
		testHookRenderHistory()
	}
	if m.state == stateFocus {
		// Focus mode renders fresh with the selection gutter, bypassing the
		// incremental cache; it scopes to whichever agent is focused.
		content, _, _ := m.renderFocusHistory()
		return strings.Split(content, "\n")
	}
	// Attached view: the focused child's session, rendered fresh from
	// the supervisor's live transcript (the parent's cache is untouched).
	if m.attachedTo != "" && m.subagents != nil {
		return strings.Split(m.renderAttachedHistory(), "\n")
	}
	if len(m.transcript) == 0 && m.turnState() != stateStreaming {
		// First contact: the empty session states what it already
		// knows about the project and offers work. Hosts without a survey —
		// the attached child view, a bare test model — keep the plain line.
		if m.startScreenShowing() {
			return strings.Split(m.renderStartScreen(m.transcriptWidth()), "\n")
		}
		return strings.Split(sty.Welcome.Render("Type a message to start chatting."), "\n")
	}
	w := m.transcriptWidth()
	if w != m.cached.width {
		m.cached.width = w
		m.invalidateRenderCache()
	}
	// History renders as step blocks. Every block but the last
	// is frozen — the grouping scan is left to right, so a block that already
	// has a successor can never change — and only the last one re-renders
	// each frame, because a running step's header restates its count and
	// duration as rows land.
	blocks := m.blocksOf(m.transcript)
	// Freeze everything before the last block rows can still land in. With an
	// approved plan that is not the last block: its declared-but-not-started
	// steps trail it, and they change as the run reaches them.
	// A live fan-out is the one entry that keeps changing without a row
	// landing in it, so its block cannot be frozen either.
	// A run's row is the other one: it redraws from the machine's state on
	// every transition, and a transition lands no row of its own.
	freeze := min(lastLiveBlock(blocks), m.liveFanoutBlock(blocks), m.liveTodoRunBlock(blocks))
	// Back to the settled lines and no further: what the frozen blocks wrote
	// stays written, and only the tail after them is built again.
	m.cached.rewind()
	for bi := 0; bi < freeze; bi++ {
		blk := blocks[bi]
		if blk.end <= m.cached.count {
			continue
		}
		block, prev, have := joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), m.cached.sep, m.cached.hasSep)
		m.cached.write(block)
		m.cached.freeze()
		m.cached.sep, m.cached.hasSep = prev, have
		m.cached.count = blk.end
	}
	prev, havePrev := m.cached.sep, m.cached.hasSep
	for _, blk := range blocks {
		if blk.end <= m.cached.count {
			continue
		}
		var block string
		block, prev, havePrev = joinUnits(m.blockUnits(blk, m.transcript, w, false, -1), prev, havePrev)
		m.cached.write(block)
	}
	if m.answerIsArriving() {
		if havePrev {
			m.cached.write(separatorBefore(prev, entry{kind: entryAssistant}))
		}
		m.cached.write(sty.Assistant.Render("Assistant") + "\n")
		// The one thing in the transcript that is not frozen, and the only
		// place the stable-prefix cache is used: everything else here is
		// either cached whole or rendered once (streammd.go).
		m.cached.write(m.streamMD.Render(m.streaming, w))
	}
	// The call the round is writing, counted, under whatever the round has
	// said so far — which is where the reader is looking (activity.go).
	m.cached.write(m.composeRowLine(w, prev, havePrev))
	return m.cached.lines
}

// contentWidth is the surface inside the horizontal padding.
func (m Model) contentWidth() int {
	return m.columns().content.Dx()
}

// viewportHeight is the transcript's own rows, read off the vertical split
// rather than counted down from the terminal. The floor is a
// floor and not a layout: a terminal with no room left still has to hand the
// viewport a height it can render at.
func (m Model) viewportHeight() int {
	return max(m.surface().view.Dy(), 1)
}

func (m Model) wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= width {
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteByte('\n')
			continue
		}
		lineLen := 0
		for i, word := range words {
			wLen := lipgloss.Width(word)
			if i > 0 && lineLen+1+wLen > width {
				result.WriteByte('\n')
				lineLen = 0
			} else if i > 0 {
				result.WriteByte(' ')
				lineLen++
			}
			result.WriteString(word)
			lineLen += wLen
		}
		result.WriteByte('\n')
	}
	return strings.TrimRight(result.String(), "\n")
}

// dividerStyle is the faint rule that opens the bottom panel and closes the
// header.
func dividerStyle(width int) string {
	return sty.Divider.Render(strings.Repeat("─", width))
}
