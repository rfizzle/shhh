package chat

// The paint: the whole screen, once.
//
// View is what Bubble Tea asks for and paint is what answers it — the header,
// the pane, the rails and the bottom panel drawn into the rectangles the
// layout resolved (layout.go). Everything here reads the model and writes
// cells; nothing here decides anything about the session.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// View is the frame the terminal shows. In v2 that is a value rather than a
// string: the screen's content, and the terminal states the session
// is asking for while it is up. Two of those used to be commands — the alt
// screen a program option each host passed, mouse reporting a command the
// toggle had to remember to send — and both were the same bug waiting, a
// state the model believed in and the terminal had never been told about. A
// field cannot drift from what View draws, because it is what View draws.
func (m Model) View() tea.View {
	var cur cursorSink
	v := tea.NewView(m.paint(&cur))
	// The one cursor the terminal draws, reported by whichever block was
	// painted with the keyboard (layout.go). Nil is a frame nobody is typing
	// into, and the terminal hides its own rather than parking it wherever
	// the last write left it.
	v.Cursor = cur.at
	v.AltScreen = true
	// Focus reporting is asked for unconditionally, because it costs the
	// terminal nothing and it is the only thing that can say whether anyone
	// is looking. A terminal that does not know the mode says
	// nothing back, and saying nothing is an answer shhh can act on: no blur
	// ever arrives, so it never concludes the reader has gone.
	v.ReportFocus = true
	// Reporting is on for the session by default — the wheel scrolls the
	// transcript, shhh selects text itself and clicks open rows. `/ui mouse off`
	// hands selection back to the terminal.
	if m.mouseOn {
		v.MouseMode = tea.MouseModeCellMotion
	}
	// The screen's own ground, which is nil unless the reader asked the theme
	// to paint the background it was chosen against (/ui ground). Nil is the
	// default and means the terminal's own background stands, which is what
	// every other program on that screen sits on.
	v.BackgroundColor = components.GroundColor()
	// And the two states outside the rectangle, for the third and fourth
	// times the same argument: the tab's name and the tab's progress light
	// are what this frame says they are (terminal.go). Empty and nil mean
	// "nothing", which is what a terminal that was never asked, a reader who
	// turned them off, and the frame after the last one all want.
	v.WindowTitle = m.windowTitle()
	v.ProgressBar = m.progressBar()
	return v
}

// screen paints the whole terminal, drawing each block into the rectangle
// layout.go resolved for it. Nothing here measures anything: a
// block is handed a rectangle, it fills what it can, and ultraviolet clips
// the rest at the edge.
func (m Model) screen() string { return m.paint(nil) }

// paint is screen() with somewhere to report the cursor to. A caller that
// holds no terminal — a capture, a measurement — passes nil and gets the
// same characters.
func (m Model) paint(cur *cursorSink) string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "Initializing…"
	}

	// One frame's geometry and one render of each block whose size decides
	// it (layout.go). Everything below reads what this resolved.
	m.framed = &frame{}
	s := m.surface()
	scr := uv.NewScreenBuffer(max(m.width, 0), max(m.height, 0))
	draw := func(view string, area uv.Rectangle) { drawIn(scr, view, area) }

	draw(m.headerRow(), s.header)
	// The line under the header says which pane has the keyboard (reading
	// mode): a plain divider while the input does, the transcript's own rail
	// while focus mode does.
	draw(m.readingRail(s.rail.Dx()), s.rail)

	// The body renders into the transcript pane; the header, divider and the
	// prompt frame span both panes. Surfaces that take the pane
	// over get all of it — the scroll gutter's column is the transcript's own
	//, and they do their own scrolling.
	view := s.in(s.view, s.pane)
	draw(m.paneView(view), view)
	draw(m.liveTail(s.pane.Dx()), s.in(s.tail, s.pane))
	// Working sub-agents render as compact progress rows above the divider
	//; hidden while the agent list or an attached view covers them.
	draw(m.renderAgentRows(s.pane.Dx()), s.in(s.agents, s.pane))

	// Past 130 content columns the body shares its rows with the inspector
	// rail; the split is horizontal only, so the row budget the
	// vertical split handed out is unchanged.
	if rail := m.inspectorData().Lines(s.inspector.Dx(), s.body.Dy()); len(rail) > 0 {
		column := strings.TrimSuffix(strings.Repeat(sty.Pane.Divider.Render("│")+"\n", s.body.Dy()), "\n")
		draw(column, s.in(s.body, s.divider))
		draw(strings.Join(rail, "\n"), s.in(s.body, s.inspector))
	}

	m.drawBottomPanel(scr, s.bottom, cur)

	return renderScreen(scr)
}

// headerRow is the title row: the header carries only the title —
// the static key hint moved into the frame's contextual bottom rail, the
// update notice onto the notice rail, and the attached breadcrumb onto the
// frame's top rail.
func (m Model) headerRow() string {
	title := m.title
	if title == "" {
		title = defaultTitle
	}
	header := sty.Header.Render(" " + title)
	if m.attachedTo != "" && !m.frameShowing() {
		// A takeover surface while attached keeps the breadcrumb visible.
		header += sty.HeaderHint.Render("  " + m.breadcrumb())
	}
	return header
}

// paneView is what the transcript pane's rows hold: whichever overlay takes
// the pane over (overlay.go), or the transcript itself. A pane overlay draws
// no live tail under it, so the rectangle it is handed is the whole body and
// it does its own scrolling in it. One whose subject has gone — a diff closed
// out from under the state — draws nothing and the transcript stands.
func (m Model) paneView(area uv.Rectangle) string {
	if o := overlayFor(m.state); o != nil && o.place == placePane {
		if lines := o.Lines(m, area.Dx(), area.Dy()); lines != nil {
			return strings.Join(lines, "\n")
		}
	}
	return m.transcriptBody()
}

// liveTail is the block the turn draws under the transcript while it works:
// the running command's own activity row, the retry countdown, the spinner
// for the housekeeping the status rail does not report. It is the one part
// of the pane whose height is not fixed, so the layout asks it rather than
// assuming — the row it takes used to be spent without being budgeted for,
// which put the bottom of the frame one row past the bottom of the terminal.
//
// Attached, the child's session fills the pane and its liveness shows in the
// child-scoped status bar, not a parent spinner.
func (m Model) liveTail(width int) string {
	if m.framed == nil {
		return m.resolveLiveTail(width)
	}
	if m.framed.tail == nil || m.framed.tail.width != width {
		m.framed.tail = &tailBlock{width: width, view: m.resolveLiveTail(width)}
	}
	return m.framed.tail.view
}

// resolveLiveTail is the block itself, rendered once per frame.
func (m Model) resolveLiveTail(width int) string {
	if m.attachedTo != "" {
		return ""
	}
	switch m.state {
	case stateStreaming:
		if m.streaming != "" {
			return ""
		}
		// What the turn is doing is the status rail's to say, once, above
		// the prompt (turnstatus.go): a spinner here would name the same
		// phase a second time, in a second vocabulary, a few rows apart.
		// Compaction is the exception — it is housekeeping rather than a
		// phase of a turn, the rail does not report it, and nothing else on
		// screen would say the history is being rewritten.
		if m.compacting {
			return m.spinner.View() + " Compacting…"
		}
		return ""
	case stateRunningCmd:
		if m.pendingApproval != nil && m.pendingApproval.kind != approvalExec {
			return m.spinner.View() + " Applying changes…"
		}
		// The running command renders as a live activity row whose tail is
		// its last output line; spinner ticks keep it fresh.
		return m.runningCommandRow(width)
	case stateClassifying:
		return m.spinner.View() + " Checking permission…"
	case stateCloseGate:
		return m.closeGateBlock()
	case stateRetryWait:
		if m.retry == nil {
			return ""
		}
		// The failure row is already in the transcript; this is the part of
		// it that drains. A wait is a meter, never a spinner.
		return m.retryWaitBlock(width)
	case stateModelList:
		return m.spinner.View() + " Listing models…"
	}
	return ""
}

// liveTailHeight is what that block costs the transcript. It is measured
// rather than declared: a retry countdown is a meter and its two offers, and
// a constant saying otherwise was how the surface came to overrun the
// terminal by a row.
func (m Model) liveTailHeight() int {
	tail := m.liveTail(m.paneWidth())
	if tail == "" {
		return 0
	}
	return lipgloss.Height(tail)
}

// drawBottomPanel paints the surface's bottom rows: the command-center frame
// (docs/interface/surfaces.md#the-input-frame), or the divider +
// status-bar stack with whichever takeover surface replaced the input under
// it.
func (m Model) drawBottomPanel(scr uv.Screen, area uv.Rectangle, cur *cursorSink) {
	if m.frameShowing() {
		// A decision that has not been given the keyboard rides above the
		// frame, with the rail that names the keyboard's owner between them.
		var head, frame uv.Rectangle
		layout.Vertical(layout.Len(m.interruptHeight()), layout.Fill(1)).
			Split(area).Assign(&head, &frame)
		drawIn(scr, m.renderInterrupt(area.Dx()), head)
		m.drawPromptFrame(scr, frame, cur)
		return
	}
	drawIn(scr, m.takeoverPanel(area.Dx()), area)
	// The panel opens with the divider and the status bar, so whatever is
	// being typed into under them starts that far down.
	body := area
	body.Min.Y += dividerHeight + statusBarHeight
	cur.place(m.takeoverCursor(area.Dx()), body)
}

// takeoverCursor is where the terminal's cursor stands in the takeover panel,
// in the panel's own cells below the divider and the status bar: whichever
// mode is up and is typed into (the register's cursor column, overlay.go), or
// — on a terminal too narrow for the frame at all — the bare input that
// stands in for it. Every mode that offers none places none, and the terminal
// hides its cursor over it.
func (m Model) takeoverCursor(width int) *tea.Cursor {
	if m.agentList != nil || m.activeChildAsk() != nil {
		return nil
	}
	if o := overlayFor(m.state); o != nil {
		if o.cursor == nil {
			return nil
		}
		return o.cursor(m, width)
	}
	switch m.state {
	case stateInput, stateStreaming, stateRunningCmd, stateClassifying:
		// Below minFrameWidth the frame degrades to the bare input
		// (frame.go), which is still the draft and still owns the keyboard.
		return m.input.Cursor()
	}
	return nil
}

// takeoverPanel is the bottom of the surface when the prompt frame is not
// showing: the divider, the status bar, and under them whichever surface has
// the panel's rows — already rendered, because the vertical split had to
// render it to know how many rows to give it (layout.go).
func (m Model) takeoverPanel(width int) string {
	// One resolve, not one per read: a paint has the frame's, and a caller
	// with no frame — a test rendering this panel on its own — would
	// otherwise render the surface twice to ask it two questions.
	p := m.panel()
	body := p.view()
	if p.lines == nil {
		// Nothing took the panel over, so the draft box is what is under the
		// status bar and it renders itself.
		body = m.draftPanel()
	}
	return dividerStyle(width) + "\n" + m.renderStatusBar(width) + "\n" + body
}

// draftPanel is the panel nothing has taken over: the draft box with the
// slash-command menu under it, or the one-line hint a full-screen surface
// leaves in the box's place while it holds the keyboard.
func (m Model) draftPanel() string {
	if o := overlayFor(m.state); o != nil && o.hint != nil {
		return o.hint(m)
	}
	inputView := m.input.View()
	// The slash-command completion menu renders under the input.
	if m.completionActive() && m.attachedTo == "" && m.agentList == nil && m.activeChildAsk() == nil {
		inputView += "\n" + strings.Join(m.completionMenuLines(), "\n")
	}
	return inputView
}
