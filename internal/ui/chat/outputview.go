package chat

// The full-screen output view (docs/interface/surfaces.md#the-activity-row):
// the depth past a row's in-place body. A command's output, a read's file
// content or a search's matches opens whole here from reading mode's [enter]
// when the bounded body was not all of it, and the command approval card's
// [d] opens its own facts the same way — the host the full-screen diff uses,
// with lines for hunks. Esc returns to wherever it was opened from; esc
// never destroys.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// noOutputEntry marks a view that came from no transcript row — the command
// card's full view — so [enter] has no row to fold and simply leaves.
const noOutputEntry = -1

// openOutputFull takes the viewer full screen; esc returns to ret. idx is
// the transcript entry the view came from, or noOutputEntry.
func (m Model) openOutputFull(v *components.OutputView, idx int, ret state) (tea.Model, tea.Cmd) {
	m.fullOutput = v
	m.outputIdx = idx
	m.outputReturn = ret
	m.enterSurface(stateOutputFull)
	return m, nil
}

// rowOutputView builds the full-screen view of one row's body: the row's own
// words as the title, and the whole stored result — already bounded upstream
// by the evidence store — as the lines.
func (m Model) rowOutputView(e entry) *components.OutputView {
	title := activityVerb(e.toolName) + " " + digest.Arg(e.toolName, e.toolArgs)
	if e.kind == entryCommand {
		title = "$ " + firstLine(e.text)
	}
	return &components.OutputView{
		Title: strings.TrimSpace(title),
		Lines: outputLines(e),
	}
}

// outputLines is a row's detail body as the transcript shows it: the stored
// result split into lines, and nothing at all for a call that never ran —
// denied, cancelled, or still pending (activity.go clears those the same
// way).
func outputLines(e entry) []string {
	result := e.toolResult
	if e.kind != entryCommand {
		switch {
		case e.deniedBy != "", result == pendingToolResult, result == cancelledToolResult:
			return nil
		}
	}
	if strings.TrimSpace(result) == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(result, "\n"), "\n")
}

// updateOutputFull routes keys to the full-screen viewer; esc and q return
// to where it was opened from, and [enter] — the depth past full screen —
// folds the row the view came from on its way out.
func (m Model) updateOutputFull(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.fullOutput == nil {
		return m.closeOutputFull()
	}
	m.fullOutput.Height = m.viewportHeight()
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	switch m.fullOutput.Update(msg) {
	case components.OutputBack:
		return m.closeOutputFull()
	case components.OutputCollapse:
		if es := *m.entries(); m.outputIdx >= 0 && m.outputIdx < len(es) {
			es[m.outputIdx].expanded = false
		}
		return m.closeOutputFull()
	}
	return m, nil
}

// closeOutputFull returns from the full screen to wherever it was opened
// from — reading mode, or the approval card.
func (m Model) closeOutputFull() (tea.Model, tea.Cmd) {
	m.fullOutput = nil
	m.outputIdx = noOutputEntry
	if m.outputReturn.isSurface() {
		m.state = m.outputReturn
	} else {
		m.leaveSurface()
	}
	m.outputReturn = stateInput
	m.invalidateRenderCache()
	m.syncViewport()
	switch m.state {
	case stateFocus:
		m.refreshFocusView()
	case stateConfirmRun:
		m.viewport.SetLines(m.renderHistoryLines())
	default:
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// renderOutputFullHint fills the input area while the full screen shows.
func (m Model) renderOutputFullHint() string {
	label := "esc back"
	if m.outputReturn == stateConfirmRun {
		label = "esc: back to the approval prompt"
	}
	return sty.SystemMsg.Render(label) + strings.Repeat("\n", inputHeight-1)
}
