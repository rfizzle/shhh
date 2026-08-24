package chat

// Focus mode (S-076, DESIGN-TUI.md §7): ctrl+e gives the transcript a
// selection cursor over expandable rows (tool and command output). j/k moves
// between them, enter expands/collapses the selected row in place, and esc
// returns to the input. This is the one mechanism behind "[enter] expand"
// everywhere in the transcript, so the input textarea keeps all other keys.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// expandable reports whether a transcript entry has bounded output that focus
// mode can expand.
func expandable(e entry) bool {
	return e.kind == entryTool || e.kind == entryCommand || e.kind == entryDiff
}

// expandableIndices lists the transcript indices focus mode can select,
// scoped to whichever agent's transcript the surface renders (S-077).
func (m Model) expandableIndices() []int {
	var idxs []int
	for i, e := range *m.entries() {
		if expandable(e) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}

// enterFocusMode starts focus mode on the most recent expandable row.
func (m Model) enterFocusMode() (tea.Model, tea.Cmd) {
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		const notice = "Nothing to focus yet — tool and command rows become expandable."
		if m.attachedTo != "" {
			m.noteChild(m.attachedTo, notice)
		} else {
			m.appendEntry(entry{kind: entrySystem, text: notice})
		}
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.state = stateFocus
	m.focusIdx = idxs[len(idxs)-1]
	m.refreshFocusView()
	return m, nil
}

// updateFocus handles keys while focus mode is active. Esc never destroys:
// it only returns to the input, keeping any expansion state.
func (m Model) updateFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+d":
		m.quitting = true
		return m, m.quitCmd()
	case "esc", "ctrl+e", "ctrl+c", "q":
		return m.exitFocusMode()
	case "j", "down":
		m.moveFocus(1)
		return m, nil
	case "k", "up":
		m.moveFocus(-1)
		return m, nil
	case "enter":
		es := *m.entries()
		if m.focusIdx >= 0 && m.focusIdx < len(es) {
			if d := es[m.focusIdx].diff; d != nil {
				// A diff row cycles collapsed → expanded → full screen
				// (S-074, DESIGN-TUI.md §3b).
				d.Update(msg)
				if d.Mode == components.DiffFull {
					return m.openDiffFull(d, stateFocus)
				}
			} else {
				es[m.focusIdx].expanded = !es[m.focusIdx].expanded
			}
		}
		m.refreshFocusView()
		return m, nil
	}
	// Everything else (PgUp/PgDn, mouse wheel) still scrolls the viewport.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// exitFocusMode returns to the input, keeping expansion state; the render
// cache is rebuilt without the selection gutter.
func (m Model) exitFocusMode() (tea.Model, tea.Cmd) {
	m.state = stateInput
	m.invalidateRenderCache()
	m.viewport.SetContent(m.renderHistory())
	return m, nil
}

// moveFocus selects the next (+1) or previous (-1) expandable row.
func (m *Model) moveFocus(dir int) {
	idxs := m.expandableIndices()
	for pos, idx := range idxs {
		if idx == m.focusIdx {
			if next := pos + dir; next >= 0 && next < len(idxs) {
				m.focusIdx = idxs[next]
			}
			break
		}
	}
	m.refreshFocusView()
}

// refreshFocusView re-renders the transcript with the selection gutter and
// scrolls the selected row into view.
func (m *Model) refreshFocusView() {
	content, start, count := m.renderFocusHistory()
	m.viewport.SetContent(content)
	switch {
	case start < m.viewport.YOffset:
		m.viewport.SetYOffset(start)
	case start+count > m.viewport.YOffset+m.viewport.Height:
		m.viewport.SetYOffset(start + count - m.viewport.Height)
	}
}

// renderFocusHistory renders every entry with a two-column gutter on
// expandable rows (❯ on the selected one) and reports the selected block's
// first line and line count for scrolling. It bypasses the incremental cache.
func (m *Model) renderFocusHistory() (content string, selStart, selCount int) {
	w := m.contentWidth()
	var b strings.Builder
	line := 0
	for i, e := range *m.entries() {
		var block string
		if expandable(e) {
			block = gutterPrefix(m.renderEntry(e, w-2), i == m.focusIdx)
		} else {
			block = m.renderEntry(e, w)
		}
		n := strings.Count(block, "\n")
		if i == m.focusIdx {
			selStart, selCount = line, n
		}
		b.WriteString(block)
		line += n
	}
	return b.String(), selStart, selCount
}

// gutterPrefix indents a rendered block by two columns, placing the focus
// pointer on the first line of the selected block.
func gutterPrefix(block string, selected bool) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		if i == 0 && selected {
			lines[i] = focusMarkerStyle.Render("❯") + " " + l
		} else {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// renderFocusHint replaces the input area while focus mode is active.
func (m Model) renderFocusHint() string {
	hint := systemMsgStyle.Render("focus · j/k select row · enter expand/collapse · esc back")
	return hint + strings.Repeat("\n", inputHeight-1)
}
