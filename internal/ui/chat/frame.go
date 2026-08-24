package chat

// Command-center prompt surface (S-082, DESIGN-TUI.md §12). The input sits in
// a rounded-corner frame whose borders carry information: the top rail shows
// session identity and the live activity state, the vitals rail re-homes the
// §8 cockpit segments, and the bottom rail carries contextual key hints. A
// notice rail above the frame appears only while there is something to say.
// Takeover surfaces (approval cards, pickers, the agent list, routed child
// asks, focus/diff hints) replace the framed input wholesale and keep the
// divider + status-bar stack, so their geometry is unchanged.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Layout thresholds in content columns (COCKPIT_SPEC.md §3 applied to shhh's
// bottom panel, DESIGN-TUI.md §12b).
const (
	frameWideWidth    = 110
	frameCompactWidth = 70
	// minFrameWidth matches the component cards' minCardWidth: below it the
	// prompt surface degrades to plain rows (divider + status bar + input).
	minFrameWidth = 12
	// frameSideWidth is what the side borders and inner padding consume
	// ("│ " + " │").
	frameSideWidth = 4
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

var (
	frameAccentPermissive = lipgloss.NewStyle().Foreground(components.Palette.Add)
	frameAccentGated      = lipgloss.NewStyle().Foreground(components.Palette.Accent)
	frameAccentChecking   = lipgloss.NewStyle().Foreground(components.Palette.Spin)
	frameIdleStyle        = lipgloss.NewStyle().Foreground(components.Palette.Dim)
	frameWorkingStyle     = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Spin)
	frameHintStyle        = lipgloss.NewStyle().Foreground(components.Palette.Dim).Italic(true)
	gutterIdleStyle       = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Info)
	gutterWorkStyle       = lipgloss.NewStyle().Bold(true).Foreground(components.Palette.Spin)
	noticeInfoStyle       = lipgloss.NewStyle().Foreground(components.Palette.Info)
	noticeAlertStyle      = lipgloss.NewStyle().Foreground(components.Palette.Del)
)

func (m Model) frameLayout() frameLayout { return frameLayoutFor(m.contentWidth()) }

// frameShowing reports whether the framed input is the current bottom panel.
func (m Model) frameShowing() bool {
	if m.agentList != nil || m.activeChildAsk() != nil {
		return false
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		return m.frameLayout() != framePlain
	}
	return false
}

// frameExtraHeight is what the frame adds beyond the standard chrome rows:
// the notice rail and, in the wide layout, the dedicated vitals rail. The
// frame's top and bottom borders take the rows the bottom divider and status
// bar otherwise use, so the compact and narrow layouts add nothing.
func (m Model) frameExtraHeight() int {
	if !m.frameShowing() {
		return 0
	}
	extra := 0
	if m.noticeLine() != "" {
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
	switch m.state {
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
	if m.state == stateClassifying {
		return frameAccentChecking
	}
	mode := m.mode
	if m.attachedTo != "" && m.subagents != nil {
		if cm, ok := m.subagents.AgentMode(m.attachedTo); ok {
			mode = cm
		}
	}
	switch mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return frameAccentPermissive
	default:
		return frameAccentGated
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

// frameActivity is the top rail's right side: spinner + WORKING while the
// focused agent works, dim idle otherwise.
func (m Model) frameActivity() string {
	if m.frameWorking() {
		return m.spinner.View() + frameWorkingStyle.Render("WORKING")
	}
	return frameIdleStyle.Render("idle")
}

// frameHints is the contextual bottom-rail hint set, swapped by state; it
// absorbs the old static header hint and the textarea placeholder.
func (m Model) frameHints() string {
	var hints []string
	switch {
	case m.attachedTo != "":
		hints = []string{"esc detach", "ctrl+a agents"}
	case m.state == stateStreaming, m.state == stateRunningCmd, m.state == stateClassifying:
		hints = []string{"enter queues steering", "ctrl+c cancel"}
	default:
		hints = []string{"enter send", "/ commands", "shift+tab mode"}
	}
	return frameHintStyle.Render(strings.Join(hints, " · "))
}

// promptGutter is the input's leading glyph (§12a): ❯ idle, ▸ while the
// agent works (typed text becomes steering, S-058), and the child's name
// while attached.
func (m Model) promptGutter() string {
	if m.attachedTo != "" {
		return gutterIdleStyle.Render(m.attachedTo+" ❯") + " "
	}
	if m.frameWorking() {
		return gutterWorkStyle.Render("▸") + " "
	}
	return gutterIdleStyle.Render("❯") + " "
}

// inputInnerWidth is the textarea's usable width inside the frame: the
// content width minus the side borders and the prompt gutter. The plain
// (sub-minFrameWidth) layout keeps the full content width.
func (m Model) inputInnerWidth() int {
	w := m.contentWidth()
	if m.frameLayout() != framePlain {
		w -= frameSideWidth + lipgloss.Width(m.promptGutter())
	}
	if w < 1 {
		return 1
	}
	return w
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
		parts = append(parts, updateNoticeStyle.Render(m.updateNotice))
	}
	if n := len(m.steering); n > 0 {
		parts = append(parts, noticeInfoStyle.Render(fmt.Sprintf("%d steering queued", n)))
	}
	if m.subagents != nil {
		if _, blocked := m.subagents.ActiveCounts(); blocked > 0 {
			label := fmt.Sprintf("⚠ %d agents waiting approval", blocked)
			if blocked == 1 {
				label = "⚠ 1 agent waiting approval"
			}
			parts = append(parts, noticeAlertStyle.Render(label))
		}
	}
	if m.denialNotice != "" {
		parts = append(parts, noticeAlertStyle.Render("✗ auto denied: "+firstLine(m.denialNotice)+" (/mode why)"))
	}
	if len(parts) == 0 {
		return ""
	}
	return clipRow(strings.Join(parts, systemMsgStyle.Render(" · ")), m.contentWidth())
}

// frameRail draws one border row: corner, dash, a left label, dash fill, an
// optional right label, dash, corner. Labels are clipped before the fill so
// the rail never overflows the width.
func frameRail(accent lipgloss.Style, leftCorner, rightCorner, leftLabel, rightLabel string, width int) string {
	inner := width - 4 // corners plus one dash on each side
	if inner < 0 {
		return accent.Render(clipRow(leftCorner+strings.Repeat("─", max(0, width-2))+rightCorner, width))
	}
	rw := lipgloss.Width(rightLabel)
	if rw > inner {
		rightLabel, rw = "", 0
	}
	leftLabel = clipRow(leftLabel, inner-rw)
	fill := inner - lipgloss.Width(leftLabel) - rw
	return accent.Render(leftCorner+"─") + leftLabel +
		accent.Render(strings.Repeat("─", max(0, fill))) + rightLabel + accent.Render("─"+rightCorner)
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
	return components.FitRail(segs, statusBarStyle.Render(" · "), width)
}

// childRailSegments is the attached child's vitals (S-077): mode, live
// detail (alert-styled when blocked), spend, queued steering, and the
// child's name as the droppable-first detail field.
func (m Model) childRailSegments() []components.RailSegment {
	name := m.attachedTo
	st, ok := m.subagents.Get(name)
	if !ok {
		return []components.RailSegment{{Text: statusBarStyle.Render(name), Drop: components.RailKeep}}
	}
	mode, _ := m.subagents.AgentMode(name)
	segs := []components.RailSegment{{Text: childModeSegment(mode), Drop: components.RailKeep}}
	detail, drop := statusBarStyle.Render(st.Detail), components.RailNormal
	if st.State == subagent.StateBlocked {
		detail, drop = ctxAlertStyle.Render(st.Detail), components.RailVital
	}
	segs = append(segs, components.RailSegment{Text: detail, Drop: drop})
	if spend := m.spendLabel(st.TokensIn, st.TokensOut); spend != "" {
		segs = append(segs, components.RailSegment{Text: statusBarStyle.Render(spend), Drop: components.RailVital})
	}
	if q := m.subagents.QueuedSteering(name); q > 0 {
		segs = append(segs, components.RailSegment{Text: statusBarStyle.Render(fmt.Sprintf("queued %d", q)), Drop: components.RailNormal})
	}
	return append(segs, components.RailSegment{Text: statusBarStyle.Render(st.Name), Drop: components.RailDetail})
}

// renderPromptFrame assembles the whole surface: notice rail, top rail,
// gutter + input rows (+ completion menu), vitals rail, bottom rail.
func (m Model) renderPromptFrame() string {
	width := m.contentWidth()
	layout := m.frameLayout()
	accent := m.frameAccentStyle()
	inner := width - frameSideWidth

	gutter := m.promptGutter()
	indent := strings.Repeat(" ", lipgloss.Width(gutter))
	var rows []string
	for i, line := range strings.Split(m.input.View(), "\n") {
		if i == 0 {
			rows = append(rows, gutter+line)
		} else {
			rows = append(rows, indent+line)
		}
	}
	// The completion menu (S-078) renders inside the frame, under the input;
	// bottomPanelHeight already caps input + menu at the confirm-panel bound.
	if m.completionActive() && m.attachedTo == "" {
		rows = append(rows, m.completionMenuLines()...)
	}
	if maxRows := m.bottomPanelHeight(); len(rows) > maxRows {
		rows = rows[:maxRows]
	}

	var b strings.Builder
	if notice := m.noticeLine(); notice != "" {
		b.WriteString(notice + "\n")
	}

	topRight := " " + m.frameActivity() + " "
	if m.attachedTo != "" && layout != frameWide {
		// Compact/narrow drop the hints rail; the detach affordance moves to
		// the top rail (§12b).
		topRight = " " + m.frameHints() + " "
	}
	identity := " " + m.frameIdentity() + " "
	if layout == frameNarrow {
		identity = ""
	}
	b.WriteString(frameRail(accent, "╭", "╮", identity, topRight, width))

	for _, row := range rows {
		row = clipRow(row, inner)
		pad := strings.Repeat(" ", max(0, inner-lipgloss.Width(row)))
		b.WriteString("\n" + accent.Render("│") + " " + row + pad + " " + accent.Render("│"))
	}

	vitals := " " + m.frameVitals(layout, width-6) + " "
	if layout == frameWide {
		b.WriteString("\n" + frameRail(accent, "├", "┤", vitals, "", width))
		b.WriteString("\n" + frameRail(accent, "╰", "╯", " "+m.frameHints()+" ", "", width))
	} else {
		b.WriteString("\n" + frameRail(accent, "╰", "╯", vitals, "", width))
	}
	return b.String()
}
