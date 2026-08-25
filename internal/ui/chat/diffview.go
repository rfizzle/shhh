package chat

// Rich diff rendering (S-074, DESIGN-TUI.md §3): the full-screen diff state
// shared by transcript edit rows, the approval card's [d], and the /diff
// session diff.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// maxDiffExpandedLines bounds an applied edit's in-transcript expanded view;
// larger diffs end with the "… (+N more diff lines)" notice and open full
// screen from focus mode.
const maxDiffExpandedLines = 20

// maxSessionDiffBytes bounds how large a /diff patch is parsed and rendered.
const maxSessionDiffBytes = 4 << 20

// WithSessionDiff wires /diff: fn returns the cumulative git diff of the
// workspace against the session's starting state. Leave unset (nil) when the
// workspace is not a git repository.
func (m Model) WithSessionDiff(fn func() (string, error)) Model {
	m.sessionDiff = fn
	return m
}

// systemNotice appends a system line and scrolls to it.
func (m Model) systemNotice(text string) (tea.Model, tea.Cmd) {
	m.appendEntry(entry{kind: entrySystem, text: text})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// openSessionDiff shows the cumulative session diff full screen (/diff).
func (m Model) openSessionDiff() (tea.Model, tea.Cmd) {
	if m.sessionDiff == nil {
		return m.systemNotice("The session diff is only available inside a git repository.")
	}
	patch, err := m.sessionDiff()
	if err != nil {
		return m.systemNotice("Error reading the session diff: " + err.Error())
	}
	if len(patch) > maxSessionDiffBytes {
		return m.systemNotice("The session diff is too large to render here; use git diff directly.")
	}
	files := diff.ParsePatch(patch)
	if len(files) == 0 {
		return m.systemNotice("No changes since the session started.")
	}
	return m.openDiffFull(&components.DiffView{
		Path:      "session diff",
		Files:     files,
		SyntaxFor: diffSyntax,
	}, stateInput)
}

// openDiffFull takes the given viewer full screen; esc returns to ret.
func (m Model) openDiffFull(d *components.DiffView, ret state) (tea.Model, tea.Cmd) {
	d.Mode = components.DiffFull
	d.Offset = 0
	m.fullDiff = d
	m.diffReturn = ret
	m.enterSurface(stateDiffFull)
	return m, nil
}

// updateDiffFull routes keys to the full-screen viewer; any key that leaves
// full-screen mode (esc, or enter's collapse) returns to where it was opened
// from. Esc never destroys.
func (m Model) updateDiffFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.fullDiff == nil {
		return m.closeDiffFull()
	}
	m.fullDiff.Height = m.viewportHeight()
	switch msg.String() {
	case "ctrl+d":
		m.quitting = true
		return m, m.quitCmd()
	case "q", "ctrl+c":
		m.fullDiff.Mode = components.DiffExpanded
	default:
		m.fullDiff.Update(msg)
	}
	if m.fullDiff.Mode != components.DiffFull {
		return m.closeDiffFull()
	}
	return m, nil
}

// closeDiffFull returns from the full-screen diff to wherever it was opened
// from — the confirm prompt, focus mode, or the input.
func (m Model) closeDiffFull() (tea.Model, tea.Cmd) {
	m.fullDiff = nil
	// A diff opened from focus mode goes back to it; anything else hands the
	// screen back to the turn, which may have moved on while it was up
	// (S-087).
	if m.diffReturn.isSurface() {
		m.state = m.diffReturn
	} else {
		m.leaveSurface()
	}
	m.diffReturn = stateInput
	// A transcript diff row's mode may have changed while full screen.
	m.invalidateRenderCache()
	m.syncViewport()
	switch m.state {
	case stateFocus:
		m.refreshFocusView()
	case stateConfirmRun:
		m.viewport.SetContent(m.renderHistory())
	default:
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// renderDiffFullHint fills the input area while the full-screen diff shows.
func (m Model) renderDiffFullHint() string {
	label := "esc back"
	if m.diffReturn == stateConfirmRun {
		label = "esc: back to the approval prompt"
	}
	return systemMsgStyle.Render(label) + strings.Repeat("\n", inputHeight-1)
}
