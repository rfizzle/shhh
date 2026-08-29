package chat

// Command-center prompt surface (S-082, DESIGN-TUI.md §12). The input sits in
// a rounded-corner frame whose borders carry information: the top rail shows
// session identity and the live activity state, the vitals rail re-homes the
// §8 cockpit segments, and the bottom rail carries contextual key hints. A
// notice rail above the frame appears only while there is something to say,
// and a staged rail under it while an attachment is waiting to ride.
// Takeover surfaces (approval cards, pickers, the agent list, routed child
// asks, focus/diff hints) replace the framed input wholesale and keep the
// divider + status-bar stack, so their geometry is unchanged.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// Layout thresholds in content columns (COCKPIT_SPEC.md §3 applied to shhh's
// bottom panel, DESIGN-TUI.md §12b).
const (
	frameWideWidth    = 110
	frameCompactWidth = 70
	// minFrameWidth matches the component cards' minCardWidth: below it the
	// prompt surface degrades to plain rows (divider + status bar + input).
	minFrameWidth = 12
	// frameRailEnd is a rail's fixed end: the corner and the dash beside it.
	// It is the one part of a border row that never gives ground, which is
	// why it is a Len and the labels between the two ends are not.
	frameRailEnd = 2
)

type frameLayout int

const (
	framePlain frameLayout = iota
	frameNarrow
	frameCompact
	frameWide
)

func frameLayoutFor(width int) frameLayout {
	switch {
	case width < minFrameWidth:
		return framePlain
	case width < frameCompactWidth:
		return frameNarrow
	case width < frameWideWidth:
		return frameCompact
	default:
		return frameWide
	}
}

// frameStyles is the input frame's own group (§12), built by newFrameStyles.
type frameStyles struct {
	AccentPermissive lipgloss.Style
	AccentGated      lipgloss.Style
	AccentChecking   lipgloss.Style
	Idle             lipgloss.Style
	Working          lipgloss.Style
	Hint             lipgloss.Style
	GutterIdle       lipgloss.Style
	GutterWork       lipgloss.Style
	NoticeInfo       lipgloss.Style
	NoticeAlert      lipgloss.Style
	// The undressed draft and the waiting chip a decision puts on the frame
	// (S-117, §7b): the chrome goes dim, the characters stay legible.
	DraftHeld   lipgloss.Style
	WaitingChip lipgloss.Style
}

func newFrameStyles(p components.ColorTokens) frameStyles {
	return frameStyles{
		AccentPermissive: lipgloss.NewStyle().Foreground(p.Add.Color()),
		AccentGated:      lipgloss.NewStyle().Foreground(p.Accent.Color()),
		AccentChecking:   lipgloss.NewStyle().Foreground(p.Spin.Color()),
		Idle:             lipgloss.NewStyle().Foreground(p.Dim.Color()),
		Working:          lipgloss.NewStyle().Bold(true).Foreground(p.Spin.Color()),
		Hint:             lipgloss.NewStyle().Foreground(p.Dim.Color()).Italic(true),
		GutterIdle:       lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		GutterWork:       lipgloss.NewStyle().Bold(true).Foreground(p.Spin.Color()),
		NoticeInfo:       lipgloss.NewStyle().Foreground(p.Info.Color()),
		NoticeAlert:      lipgloss.NewStyle().Foreground(p.Del.Color()),
		DraftHeld:        lipgloss.NewStyle().Foreground(p.Body.Color()),
		WaitingChip:      lipgloss.NewStyle().Bold(true).Foreground(p.Accent.Color()),
	}
}

func (m Model) frameLayout() frameLayout { return frameLayoutFor(m.contentWidth()) }

// frameShowing reports whether the framed input is the current bottom panel.
func (m Model) frameShowing() bool {
	if m.agentList != nil {
		return false
	}
	if m.decisionUngated() {
		// The card rides above the frame rather than replacing it (§7b,
		// S-117): the draft still holds the keyboard, so it is still on
		// screen, still accented, and still being typed into.
		return m.frameLayout() != framePlain
	}
	if m.activeChildAsk() != nil {
		return false
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		return m.frameLayout() != framePlain
	}
	return false
}

// frameExtraHeight is what the frame adds beyond the standard chrome rows:
// the notice rail, the staged rail (§12g) and, in the wide layout, the
// dedicated vitals rail. The frame's top and bottom borders take the rows the
// bottom divider and status bar otherwise use, so the compact and narrow
// layouts add nothing.
func (m Model) frameExtraHeight() int {
	if !m.frameShowing() {
		return 0
	}
	extra := m.interruptHeight()
	if m.noticeLine() != "" {
		extra++
	}
	if m.stagedRail() != "" {
		extra++
	}
	if m.frameLayout() == frameWide {
		extra++
	}
	return extra
}

// frameWorking reports whether the focused agent is actively working — the
// top rail's WORKING state and the steering gutter glyph key off it.
func (m Model) frameWorking() bool {
	if m.attachedTo != "" {
		if m.subagents == nil {
			return false
		}
		st, ok := m.subagents.Get(m.attachedTo)
		return ok && st.State == subagent.StateRunning
	}
	switch m.turnState() {
	case stateStreaming, stateRunningCmd, stateClassifying:
		return true
	}
	return false
}

// frameAccentStyle is the mode-aware border accent (§12c): add for the
// permissive modes, accent for the gated ones, spin while the auto-mode
// classifier is checking. Attached, it reflects the child's mode. The mode
// glyphs in the vitals keep meaning independent of color.
func (m Model) frameAccentStyle() lipgloss.Style {
	if m.turnState() == stateClassifying {
		return sty.Frame.AccentChecking
	}
	mode := m.mode
	if m.attachedTo != "" && m.subagents != nil {
		if cm, ok := m.subagents.AgentMode(m.attachedTo); ok {
			mode = cm
		}
	}
	switch mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return sty.Frame.AccentPermissive
	default:
		return sty.Frame.AccentGated
	}
}

// frameIdentity is the top rail's left side: the title plus the attached
// breadcrumb (S-077).
func (m Model) frameIdentity() string {
	title := m.title
	if title == "" {
		title = "shhh chat"
	}
	if m.attachedTo != "" {
		return title + " · " + m.breadcrumb()
	}
	return title
}

// frameActivity is the top rail's right side (§12a): the running turn's
// status line while the turn works, the summary it resolved into once it is
// done, `⏸ N waiting` while decisions are queued and ungated, and dim `idle`
// when there is nothing to report. width is the room the slot has; a slot too
// small for even the phase says nothing rather than clipping the identity
// beside it.
func (m Model) frameActivity(width int) string {
	if width <= 0 {
		return ""
	}
	// A turn paused on a decision is not working, and what the rail should
	// say is how many answers it is waiting for (S-117, §7b).
	if n := m.waitingCount(); n > 0 {
		return sty.Frame.WaitingChip.Render(clipRow(fmt.Sprintf("⏸ %d waiting", n), width))
	}
	// Attached, the frame is scoped to the child (§12d) and the child's phase
	// is not something the supervisor reports — a subagent is running,
	// blocked or done. Naming one of §8d's four for it would be inventing the
	// fact, so the attached rail keeps the working indicator it had.
	if m.attachedTo != "" {
		if m.frameWorking() {
			return clipRow(m.spinner.View()+sty.Frame.Working.Render("WORKING"), width)
		}
		return sty.Frame.Idle.Render(clipRow("idle", width))
	}
	if s, ok := m.turnStatus(); ok {
		if line := s.View(width); line != "" {
			return line
		}
	}
	return sty.Frame.Idle.Render(clipRow("idle", width))
}

// frameHints is the contextual bottom-rail hint set, swapped by state; it
// absorbs the old static header hint and the textarea placeholder.
func (m Model) frameHints() string {
	var hints []string
	switch {
	case m.decisionUngated():
		// The three keys that matter while a decision waits (§7b). Stopping
		// the run is ctrl+c here rather than the artboard's esc, because esc
		// clears the draft on this surface and always has — see the departure
		// recorded in DESIGN-TUI.md §7b.
		hints = []string{
			keys.Shown(keys.Draft.Answer) + " " + keys.Words(keys.Draft.Answer),
			keys.Shown(keys.Draft.Send) + " queues steering",
			keys.Shown(keys.Draft.Cancel) + " stop the run",
		}
	case m.attachedTo != "":
		hints = []string{
			keys.Shown(keys.Agent.Detach) + " detach",
			keys.Shown(keys.Draft.Agents) + " agents",
		}
	case m.working():
		// Commands run mid-turn now (S-087), so the working rail says so;
		// with children in flight the agent manager is the first thing to
		// reach for.
		steer := keys.Shown(keys.Draft.Send) + " queues steering"
		cancel := keys.Shown(keys.Draft.Cancel) + " cancel"
		hints = []string{steer, "/ commands", cancel}
		if active, _ := m.activeAgents(); active > 0 {
			hints = []string{steer, keys.Shown(keys.Draft.Agents) + " agents", "/ commands", cancel}
		}
	default:
		hints = []string{
			keys.Shown(keys.Draft.Send) + " send",
			keys.Shown(keys.Draft.Newline) + " newline",
			"/ commands",
			keys.Shown(keys.Draft.Attach) + " attach",
			keys.Shown(keys.Draft.Palette) + " palette",
			keys.Shown(keys.Draft.Mode) + " mode",
		}
	}
	return sty.Frame.Hint.Render(strings.Join(hints, " · "))
}

// promptGutter is the input's leading glyph (§12a): ❯ idle, ▸ while the
// agent works (typed text becomes steering, S-058), and the child's name
// while attached.
func (m Model) promptGutter() string {
	if m.attachedTo != "" {
		return sty.Frame.GutterIdle.Render(m.attachedTo+" ❯") + " "
	}
	if m.frameWorking() {
		return sty.Frame.GutterWork.Render("▸") + " "
	}
	return sty.Frame.GutterIdle.Render("❯") + " "
}

// frameBox is the prompt frame's own rectangles (S-161, §12): the box, the
// two border columns, what they leave between them, and the split a draft
// row makes of that — the prompt gutter's columns and the text's.
type frameBox struct {
	area   uv.Rectangle
	left   uv.Rectangle
	right  uv.Rectangle
	inner  uv.Rectangle
	gutter uv.Rectangle
	draft  uv.Rectangle
}

// frameBoxFor resolves the box inside a rectangle. The columns are the same
// at every row, which is why a caller that only wants a width can hand it a
// single row.
func (m Model) frameBoxFor(area uv.Rectangle) frameBox {
	var b frameBox
	b.area = area
	// border, its padding column, the content, and the same again mirrored.
	layout.Horizontal(
		layout.Len(1), layout.Len(1),
		layout.Fill(1),
		layout.Len(1), layout.Len(1),
	).Split(b.area).Assign(&b.left, new(uv.Rectangle), &b.inner, new(uv.Rectangle), &b.right)
	layout.Horizontal(
		layout.Len(lipgloss.Width(m.promptGutter())),
		layout.Fill(1),
	).Split(b.inner).Assign(&b.gutter, &b.draft)
	return b
}

// inputInnerWidth is the textarea's usable width inside the frame: what the
// box leaves after its borders and the prompt gutter. The plain
// (sub-minFrameWidth) layout keeps the full content width.
func (m Model) inputInnerWidth() int {
	if m.frameLayout() == framePlain {
		return max(m.contentWidth(), 1)
	}
	return max(m.frameBoxFor(uv.Rect(0, 0, max(m.contentWidth(), 0), 1)).draft.Dx(), 1)
}

// syncInputWidth re-fits the textarea to the frame; call it when the width
// or the gutter (attach/detach) changes.
func (m *Model) syncInputWidth() {
	m.input.SetWidth(m.inputInnerWidth())
}

// noticeLine assembles the notice rail (§12a): update notice, queued
// steering, blocked sub-agents, and the latest auto-mode denial. Empty —
// rail hidden — when there is nothing to say; orchestrator-scoped, so it
// hides while attached.
func (m Model) noticeLine() string {
	if m.attachedTo != "" {
		return ""
	}
	var parts []string
	if m.updateNotice != "" {
		parts = append(parts, sty.UpdateNotice.Render(m.updateNotice))
	}
	if n := len(m.steering); n > 0 {
		parts = append(parts, sty.Frame.NoticeInfo.Render(fmt.Sprintf("%d steering queued", n)))
	}
	// Scrolled off the live end, so the transcript has stopped following the
	// turn (S-140, navigate.go). The draft still holds the keyboard, so this
	// rail is the only thing that can say so.
	if note := m.followNotice(); note != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(note))
	}
	// What the last mouse selection put on the clipboard (S-145, select.go).
	// It rides here rather than in the transcript because a copy is not part
	// of the conversation, and because appending a row would scroll the pane
	// away from the selection the reader is still looking at.
	if m.selNotice != "" {
		parts = append(parts, sty.Frame.NoticeInfo.Render(m.selNotice))
	}
	if m.subagents != nil {
		if _, blocked := m.subagents.ActiveCounts(); blocked > 0 {
			label := fmt.Sprintf("⚠ %d agents waiting approval", blocked)
			if blocked == 1 {
				label = "⚠ 1 agent waiting approval"
			}
			parts = append(parts, sty.Frame.NoticeAlert.Render(label))
		}
	}
	if m.denialNotice != "" {
		parts = append(parts, sty.Frame.NoticeAlert.Render("✗ auto denied: "+firstLine(m.denialNotice)+" (/permissions why)"))
	}
	if len(parts) == 0 {
		return ""
	}
	return clipRow(strings.Join(parts, sty.SystemMsg.Render(" · ")), m.contentWidth())
}

// railSlots splits a border row into the five rectangles it is drawn from:
// the two fixed ends, the left label, the dash fill between the labels, and
// the right label. It is separate from drawRail because the top rail has to
// know how wide the right-hand slot is *before* it can ask the status line
// what to put in it (§12a).
func railSlots(leftLabel, rightLabel string, width int) (head, left, fill, right, tail uv.Rectangle) {
	var labels uv.Rectangle
	layout.Horizontal(
		layout.Len(frameRailEnd),
		layout.Fill(1),
		layout.Len(frameRailEnd),
	).Split(uv.Rect(0, 0, max(width, 0), 1)).Assign(&head, &labels, &tail)

	// A right label too wide for what is left says nothing rather than
	// crowding the identity beside it (§12a). That is the design's rule, not
	// the fill's, so it is spelled out here rather than left to the solver's
	// idea of which segment should give ground.
	rw := lipgloss.Width(rightLabel)
	if rw > labels.Dx() {
		rw = 0
	}
	var rest uv.Rectangle
	layout.Horizontal(layout.Fill(1), layout.Len(rw)).Split(labels).Assign(&rest, &right)
	layout.Horizontal(
		layout.Len(min(lipgloss.Width(leftLabel), max(rest.Dx(), 0))),
		layout.Fill(1),
	).Split(rest).Assign(&left, &fill)
	return head, left, fill, right, tail
}

// drawRail draws one border row into those rectangles: corner and dash at
// each end, the labels in their slots, dashes across what is left. Nothing
// is measured against a remainder — a label wider than the columns it was
// given is cut by the edge it was drawn against.
func drawRail(scr uv.Screen, area uv.Rectangle, accent lipgloss.Style, leftCorner, rightCorner, leftLabel, rightLabel string) {
	head, left, fill, right, tail := railSlots(leftLabel, rightLabel, area.Dx())
	at := func(r uv.Rectangle) uv.Rectangle { return r.Add(area.Min) }
	drawIn(scr, accent.Render(leftCorner+"─"), at(head))
	drawIn(scr, leftLabel, at(left))
	drawIn(scr, accent.Render(strings.Repeat("─", max(fill.Dx(), 0))), at(fill))
	if right.Dx() > 0 {
		drawIn(scr, rightLabel, at(right))
	}
	drawIn(scr, accent.Render("─"+rightCorner), at(tail))
}


// frameVitals renders the vitals rail content: the §8 cockpit segments with
// the §12b field-drop order. The narrow layout keeps only the never-dropped
// fields (minimal rail); attached, the vitals scope to the child.
func (m Model) frameVitals(layout frameLayout, width int) string {
	var segs []components.RailSegment
	if m.attachedTo != "" && m.subagents != nil {
		segs = m.childRailSegments()
	} else {
		segs = m.cockpitData(false).RailSegments()
	}
	if layout == frameNarrow {
		minimal := segs[:0:0]
		for _, s := range segs {
			if s.Drop <= components.RailVital {
				minimal = append(minimal, s)
			}
		}
		segs = minimal
	}
	return components.FitRail(segs, sty.StatusBar.Render(" · "), width)
}

// childRailSegments is the attached child's vitals (S-077): mode, live
// detail (alert-styled when blocked), spend, queued steering, and the
// child's name as the droppable-first detail field.
func (m Model) childRailSegments() []components.RailSegment {
	name := m.attachedTo
	st, ok := m.subagents.Get(name)
	if !ok {
		return []components.RailSegment{{Text: sty.StatusBar.Render(name), Drop: components.RailKeep}}
	}
	mode, _ := m.subagents.AgentMode(name)
	segs := []components.RailSegment{{Text: childModeSegment(mode), Drop: components.RailKeep}}
	detail, drop := sty.StatusBar.Render(st.Detail), components.RailNormal
	if st.State == subagent.StateBlocked {
		detail, drop = sty.CtxAlert.Render(st.Detail), components.RailVital
	}
	segs = append(segs, components.RailSegment{Text: detail, Drop: drop})
	if spend := m.spendLabel(st.TokensIn, st.TokensOut); spend != "" {
		segs = append(segs, components.RailSegment{Text: sty.StatusBar.Render(spend), Drop: components.RailVital})
	}
	if q := m.subagents.QueuedSteering(name); q > 0 {
		segs = append(segs, components.RailSegment{Text: sty.StatusBar.Render(fmt.Sprintf("queued %d", q)), Drop: components.RailNormal})
	}
	return append(segs, components.RailSegment{Text: sty.StatusBar.Render(st.Name), Drop: components.RailDetail})
}

// railLabelWidth is the room a rail label has once the ends and an
// already-placed label have taken theirs: the slot the split leaves, less the
// space on each side of the label itself.
func railLabelWidth(leftLabel string, width int) int {
	_, _, fill, _, _ := railSlots(leftLabel, "", width)
	var slot uv.Rectangle
	layout.Horizontal(layout.Len(1), layout.Fill(1), layout.Len(1)).
		Split(fill).Assign(new(uv.Rectangle), &slot, new(uv.Rectangle))
	return slot.Dx()
}

// topRailLabels is the top rail's two labels (§12a): the identity on the
// left, and on the right the running turn's status line — or, attached below
// the wide layout, the hints rail that has nowhere else to go (§12b).
func (m Model) topRailLabels(mode frameLayout, width int) (identity, right string) {
	identity = " " + m.frameIdentity() + " "
	if mode == frameNarrow {
		identity = ""
	}
	if m.attachedTo != "" && mode != frameWide {
		// Compact/narrow drop the hints rail; the detach affordance moves to
		// the top rail (§12b).
		return identity, " " + m.frameHints() + " "
	}
	// The identity is the rail's left label and keeps its room; the status
	// line takes the slot that is left and sheds fields in the §8d order to
	// fit it.
	if activity := m.frameActivity(railLabelWidth(identity, width)); activity != "" {
		right = " " + activity + " "
	}
	return identity, right
}

// frameDraftLines is what goes inside the box: the textarea's rows and, under
// them, the completion menu (S-078). bottomPanelHeight already caps the pair
// at the confirm-panel bound, and the cut is taken here so the box's height
// and its contents can never be counted differently.
func (m Model) frameDraftLines() (lines, menu []string) {
	lines = strings.Split(m.input.View(), "\n")
	if m.completionActive() && m.attachedTo == "" {
		menu = m.completionMenuLines()
	}
	if maxRows := m.bottomPanelHeight(); len(lines)+len(menu) > maxRows {
		if len(lines) > maxRows {
			return lines[:maxRows], nil
		}
		menu = menu[:maxRows-len(lines)]
	}
	return lines, menu
}

// drawPromptFrame paints the whole surface into its rectangle: notice rail,
// staged rail, then the box — top rail, gutter + input rows (+ completion
// menu), vitals rail, bottom rail — each in the rectangle frameBoxFor
// resolved for it (S-161, §10n). The two rails above the box are rows of the
// surface rather than rows of the box, which is why they are split off first.
func (m Model) drawPromptFrame(scr uv.Screen, area uv.Rectangle) {
	mode := m.frameLayout()
	accent := m.frameAccentStyle()
	width := area.Dx()

	var above, boxArea uv.Rectangle
	notice, staged := m.noticeLine(), m.stagedRail()
	rails := 0
	for _, rail := range []string{notice, staged} {
		if rail != "" {
			rails++
		}
	}
	layout.Vertical(layout.Len(rails), layout.Fill(1)).Split(area).Assign(&above, &boxArea)
	row := 0
	// The staged rail sits between the notices and the frame (§12g): what is
	// staged rides with the sentence being typed, so it belongs against the
	// box it will leave with, under anything transient the session is saying.
	for _, rail := range []string{notice, staged} {
		if rail == "" {
			continue
		}
		drawIn(scr, rail, rowAt(above, row))
		row++
	}

	lines, menu := m.frameDraftLines()
	// The wide layout gets a rail of its own for the vitals; the others hang
	// them on the closing rail (§12b).
	vitalsRows := 0
	if mode == frameWide {
		vitalsRows = 1
	}
	box := m.frameBoxFor(boxArea)
	var top, drafts, vitalsRail, bottom uv.Rectangle
	// The rails are fixed and the draft rows absorb whatever is left, so a
	// box given more rows than it has content for stays closed rather than
	// trailing blank rows under its own bottom border.
	layout.Vertical(
		layout.Len(1),
		layout.Fill(1),
		layout.Len(vitalsRows),
		layout.Len(1),
	).Split(box.area).Assign(&top, &drafts, &vitalsRail, &bottom)

	identity, topRight := m.topRailLabels(mode, width)
	drawRail(scr, top, accent, "╭", "╮", identity, topRight)

	for i := range drafts.Dy() {
		y := drafts.Min.Y - box.area.Min.Y + i
		drawIn(scr, accent.Render("│"), rowAt(box.left, y))
		drawIn(scr, accent.Render("│"), rowAt(box.right, y))
		switch {
		case i == 0:
			// The prompt glyph owns its columns and the draft wraps to the
			// rest; a continuation line simply leaves the gutter blank.
			drawIn(scr, m.promptGutter(), rowAt(box.gutter, y))
			drawIn(scr, lines[i], rowAt(box.draft, y))
		case i < len(lines):
			drawIn(scr, lines[i], rowAt(box.draft, y))
		case i-len(lines) < len(menu):
			// The menu is the frame's, not the draft's: it starts at the box
			// edge rather than under the text.
			drawIn(scr, menu[i-len(lines)], rowAt(box.inner, y))
		}
	}

	vitals := " " + m.frameVitals(mode, railLabelWidth("", width)) + " "
	if mode == frameWide {
		drawRail(scr, vitalsRail, accent, "├", "┤", vitals, "")
		drawRail(scr, bottom, accent, "╰", "╯", " "+m.frameHints()+" ", "")
		return
	}
	drawRail(scr, bottom, accent, "╰", "╯", vitals, "")
}

// renderPromptFrame is the same surface as a string, for the captures and for
// callers that hold no screen. Its height is the bottom panel's own rows less
// the interrupt card riding above it (§7b), which is the same accounting the
// vertical split hands out — the frame cannot be sized one way and budgeted
// for another.
func (m Model) renderPromptFrame() string {
	scr := uv.NewScreenBuffer(max(m.contentWidth(), 0), max(m.bottomRows()-m.interruptHeight(), 0))
	m.drawPromptFrame(scr, scr.Bounds())
	return renderScreen(scr)
}
