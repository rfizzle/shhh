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
	"github.com/rfizzle/shhh/internal/ui/markdown"
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

// appendEntries appends a run of entries a single act left behind — the
// session boundary's account and the offer beside it — so a caller that owns
// none of them individually does not have to know how many there were.
func (m *Model) appendEntries(es []entry) {
	for _, e := range es {
		m.appendEntry(e)
	}
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
//
// A reader who has opened reading mode or lit the pointer and scrolled off
// the live end is shown none of it: the arriving message is not on their
// screen (renderFocusLines draws the transcript and nothing after it), so the
// repaint would cost the gutter's render to change nothing they can see. The
// owed repaint is kept rather than dropped, so the flush that follows them
// back to the bottom — or out of the gutter — is the one that pays for it.
func (m *Model) flushStream() {
	// One repaint tiles the transcript once, the way one paint does
	// (plan.go). The gutter's own question — is the pointed row still on
	// screen — is a scan over the tiling, the render that follows it is
	// another, and nothing between them can change it.
	if m.framed == nil {
		m.framed = &frame{}
		defer func() { m.framed = nil }()
	}
	// The cheap half of the condition first: a reader at the live end is
	// being repainted whatever surface they are on, and asking after the
	// pointer would be a scan spent to learn nothing.
	if !m.atBottom && m.gutterShowing() {
		m.streamDirty = true
		return
	}
	m.streamDirty = false
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
}

// invalidateRenderCache forces the next renderHistory to re-render every
// entry (used when an entry's rendering changes in place, e.g. focus-mode
// expansion). Both caches go: the feed's lines and the gutter's units are
// two renders of the same entries, and an entry that changed changed in both.
func (m *Model) invalidateRenderCache() {
	m.cached.reset()
	m.gutter.reset()
}

// entryLineStarts is each transcript entry's first rendered line in the pane
// as it is drawn right now, by the same walk the pane's own render makes
// (lines.go: the open line every unit continues, and separatorBefore's
// rhythm between them). Under the gutter it is the reading cursor's own map
// instead, because that render wraps selectable rows two columns narrower
// and a line taken from the other one would name a different row.
//
// It exists so a change that reflows the transcript can be told where a row
// went. A pane repainted around a row the reader is looking at has to put
// that row back where it was, and a line number taken from a render that is
// no longer on screen would put it somewhere else.
func (m *Model) entryLineStarts() map[int]int {
	if m.gutterShowing() {
		return m.unitLineStarts()
	}
	starts := map[int]int{}
	units := m.transcriptUnits(*m.entries(), m.transcriptWidth(), false, noFocusRow)
	// One open line to begin with, the way both renders begin.
	n := 1
	var prev entry
	havePrev := false
	for i := range units {
		u := &units[i]
		if havePrev {
			n += strings.Count(separatorBefore(prev, u.sepBefore), "\n")
		}
		starts[u.idx] = n - 1
		n += strings.Count(u.text, "\n")
		prev, havePrev = u.sepAfter, true
	}
	return starts
}

// topEntry is the entry the top of the pane is showing: the last one that
// starts at or above the scroll offset. It is what a reflow is re-anchored
// to, and -1 where the pane is showing nothing that has an entry behind it.
func topEntry(starts map[int]int, offset int) int {
	top, best := -1, -1
	for idx, line := range starts {
		if line <= offset && (line > best || (line == best && idx > top)) {
			top, best = idx, line
		}
	}
	return top
}

// anchorTo puts the pane back on the entry it was showing at its top, after
// something reflowed the transcript underneath it. A reader following the
// live end is left following it, because that is the row they were looking
// at; a reader scrolled up to a row keeps that row. An entry that the reflow
// folded away is answered by the nearest one before it, which is the row that
// swallowed it.
func (m *Model) anchorTo(idx int, follow bool) {
	if follow || idx < 0 {
		m.viewport.GotoBottom()
		m.atBottom = m.viewport.AtBottom()
		return
	}
	starts := m.entryLineStarts()
	for i := idx; i >= 0; i-- {
		if line, ok := starts[i]; ok {
			m.viewport.SetYOffset(line)
			break
		}
	}
	m.atBottom = m.viewport.AtBottom()
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
		//
		// No speaker label on either half of the conversation. The prompt
		// mark in the margin says whose words these are and the rule under
		// them says where they stop, which is two of the transcript's own
		// devices doing a job a word was doing badly: `You` and `Assistant`
		// cost a row each, said nothing a reader scrolling a log needed, and
		// spent Add — the token for a thing that landed — on a heading
		// (docs/interface/surfaces.md#the-activity-row).
		row := promptMarked(renderReaderMarkdown(e.text, width)) + "\n"
		if len(e.attached) > 0 {
			row += sty.SystemMsg.Render(clipRow("attached: "+strings.Join(e.attached, ", "), width)) + "\n"
		}
		return row + promptRule(width) + "\n"
	case entryAssistant:
		return renderMarkdown(e.text, width) + "\n"
	case entryCompactSummary:
		block := m.compactBlock(e, width)
		if block == "" {
			return ""
		}
		return block + "\n"
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
		// The offer an interruption's notice carries, where it carries one:
		// the machinery wrote a message into this conversation, and `[u]` is
		// how the reader takes it back (intervene.go).
		return m.systemRow(e, width) + m.steerOfferLine(e, keysLive) + "\n"
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
// A notice that carries a row of its own is drawn as that row: what the
// session did to itself sits in the transcript's columns rather than beside
// them (docs/interface/principles.md#one-grid).
func (m Model) systemRow(e entry, width int) string {
	if e.notice != nil {
		row := *e.notice
		row.Expanded = e.expanded
		if e.expanded {
			row.Detail = m.noticeBody(e, width)
		}
		return row.View(width)
	}
	row := m.wrapped(e.text, width)
	if !e.expanded {
		return row
	}
	if body := m.noticeBody(e, width); len(body) > 0 {
		inner := max(width-components.GridDetailIndent, 1)
		indent := strings.Repeat(" ", components.GridDetailIndent)
		for _, l := range body {
			row += "\n" + indent + sty.SystemMsg.Render(components.Clip(l, inner))
		}
	}
	return row
}

// noticeBody is what a notice folds: the sentence a reader opened it for,
// wrapped rather than clipped, because half a sentence is worse than a row
// that costs two lines.
func (m Model) noticeBody(e entry, width int) []string {
	lines := outputLines(e)
	if len(lines) == 0 {
		return nil
	}
	inner := max(width-components.GridDetailIndent, 1)
	return strings.Split(m.wordWrap(strings.Join(lines, "\n"), inner), "\n")
}

// wrapped is a notice's own line, wrapped to the pane. A notice is prose,
// and prose that ran past the right edge was not a long row: it was a row
// the terminal broke wherever it happened to run out, over the top of
// whatever the pane drew in the column beside it. Wrapping is what keeps it
// inside the grid, however many lines it costs
// (docs/interface/principles.md#one-grid); a single word wider than the pane
// — a path, a URL — has nowhere to break and clips instead.
//
// A notice that already arrived as several lines is left alone, and is left
// alone whatever its widest line measures. It laid itself out — the key help,
// a run's report — with its own columns and indents, so re-flowing it by
// words would take a table apart and clipping it would cut a row of that
// table off with no way to reach the rest, which is the one thing a fold may
// not do (docs/interface/principles.md#fold-never-hide). A block wider than
// the pane is its author's to fit; what the terminal does to it meanwhile
// keeps the words on screen, which neither of the alternatives here does.
func (m Model) wrapped(text string, width int) string {
	if strings.Contains(text, "\n") {
		return sty.SystemMsg.Render(text)
	}
	var out []string
	for _, l := range strings.Split(m.wordWrap(text, width), "\n") {
		out = append(out, sty.SystemMsg.Render(components.Clip(l, width)))
	}
	return strings.Join(out, "\n")
}

// entryIsBlock reports whether an entry reads as a standalone block — a
// conversational turn, or a notice long enough to wrap onto its own lines —
// rather than as a row in the compact activity feed.
//
// An applied edit is not one of them. It is an activity row like the call
// that made it (components/diff.go), and a row with a blank line either side
// of it is the archetypal mutation set apart from the acts it belongs among
// — which broke the one thing the feed is for, scanning a step's rows as one
// run (docs/interface/principles.md#one-grid). Opened, it costs the lines
// its body costs, the way an opened tool row does.
func entryIsBlock(e entry) bool {
	switch e.kind {
	case entryUser, entryAssistant, entryCompactSummary,
		entryTurnClose, entryFanout, entryTodoRun:
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

// modeWord is what a mode segment says, on this session's frame and on an
// attached child's: the permission class the mark already means, and the
// mode's own name after it where the class is not the whole of it. `⏵⏵ auto`
// is every gate a mode can open; `⏵⏵ auto · accept edits` wears the same mark
// narrowed to edits, and the second word is the difference between the two.
// `⏸ gated` and `⏸ read-only` are each the only mode of their class, so the
// name they happen to be set under would be one state said twice — which is
// the drift the one segment read before every keystroke cannot afford
// (docs/interface/principles.md#closed-vocabularies).
func modeWord(mode agent.Mode) string {
	if mode == agent.ModeAcceptEdits {
		return mode.Class() + " · " + strings.ReplaceAll(mode.String(), "-", " ")
	}
	return mode.Class()
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
		c.Mode = modeWord(m.policy.mode)
		switch m.policy.mode {
		case agent.ModeAcceptEdits, agent.ModeAuto:
			c.ModeKind = components.CockpitPermissive
		default:
			c.ModeKind = components.CockpitGated
		}
	}
	// The round counter stands as soon as there is one to state, idle
	// included. It is the third field the rail sheds when it runs out of
	// columns and the model is the first (guidelines/layout-drop-order), so a
	// rail that hid the counter at rest while keeping the model had the order
	// backwards — and what the counter answers at rest, how much of the
	// ceiling the last turn spent, is exactly what the reader about to send
	// the next one is asking. The grant on offer is stated beside it through
	// a round-limit pause, so the counter says both what the bound is and
	// what taking the offer would make it.
	if m.agent.Rounds() > 0 {
		c.Round = m.roundCounter()
	}
	// The session's account carries the running turn's live token estimate,
	// so its counters move with the round instead of standing still until it
	// reports. While they are moving they print every digit; at rest they go
	// back to the shape a total is read in.
	sessionIn, sessionOut := m.liveSessionTokens()
	if sessionIn != 0 || sessionOut != 0 {
		c.Tokens = fmt.Sprintf("↑%s ↓%s", m.countLabel(sessionIn), m.countLabel(sessionOut))
		if tokens := m.estimatedContextTokens(); tokens > 0 {
			c.CtxPct = int(tokens * 100 / m.contextWindow())
		}
	}
	// Spend is the ledger's billed total, cache split included. Re-pricing the
	// live token pair at the fresh input rate turns cached prompt reads into an
	// inflated estimate that contradicts the inspector.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	if label := m.totalsLabel(m.sessionSpend()); strings.HasPrefix(label, "$") {
		c.Spend = label
	}
	if _, warned := m.ledger.Warning(); warned {
		c.Extra = append(c.Extra, "spend warning")
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
	if m.gutterShowing() {
		// Reading mode and the pointer draw the selection gutter, from a
		// cache of their own (focus.go); it scopes to whichever agent is
		// focused. A pointer lit from the prompt is the same gutter without
		// the mode.
		lines, _, _ := m.renderFocusLines()
		return lines
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
		return strings.Split(sty.Welcome.Render("nothing yet — ask for anything"), "\n")
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

// promptMarked writes the ❯ into the first row of a sent message, in the two
// columns internal/ui/markdown holds every document back from the edge by.
// The mark lands in the transcript's own pointer column that way, so a sent
// message and the activity rows under it share one left edge and the reply
// keeps the plain indent (the `Main` artboard's first two rows).
func promptMarked(doc string) string {
	first, rest, multi := strings.Cut(doc, "\n")
	marked := sty.PromptMark.Render("❯") + " " + strings.TrimPrefix(first, strings.Repeat(" ", markdown.Margin))
	if !multi {
		return marked
	}
	return marked + "\n" + rest
}

// promptRule closes a sent message. It runs the pane rather than the
// document's width: what it separates is the reader's sentence from
// everything the session did about it, and that boundary is the pane's.
func promptRule(width int) string {
	return sty.PromptRule.Render(strings.Repeat("─", max(width, 0)))
}

// dividerStyle is the faint rule that opens the bottom panel and closes the
// header.
func dividerStyle(width int) string {
	return sty.Divider.Render(strings.Repeat("─", width))
}
