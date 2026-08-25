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
// scoped to whichever agent's transcript the surface renders (S-077). Step
// headers are targets too (S-090, §7): j/k steps between headers and rows
// alike, and a folded step offers only its header, since its rows are not on
// screen to select.
func (m Model) expandableIndices() []int {
	es := *m.entries()
	var idxs []int
	for _, blk := range stepBlocks(es) {
		if blk.step != nil {
			idxs = append(idxs, blk.step.titleIdx)
			if m.headerFor(blk, es).Folded {
				continue
			}
			// A folded group offers its group row, not the rows inside it
			// (S-091, §13c).
			for _, sl := range m.stepSlots(es, blk.step.start, blk.step.end) {
				if expandable(es[sl.idx]) {
					idxs = append(idxs, sl.idx)
				}
			}
			continue
		}
		start, end := blk.members()
		for i := start; i < end; i++ {
			if expandable(es[i]) {
				idxs = append(idxs, i)
			}
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
	m.enterSurface(stateFocus)
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
			if _, ok := stepBlockAt(es, m.focusIdx); ok {
				// Enter on a step header folds or unfolds the whole group in
				// place (S-090, §13b).
				m.toggleStepFold(m.focusIdx)
				m.refreshFocusView()
				return m, nil
			}
			if m.groupAnchor(es, m.focusIdx) {
				// Enter on a folded group restores its rows in place, and
				// folds them back again (S-091, §13c).
				m.toggleGroupFold(m.focusIdx)
				m.refreshFocusView()
				return m, nil
			}
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
	m.leaveSurface()
	m.invalidateRenderCache()
	m.syncViewportHeight()
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
	es := *m.entries()
	var b strings.Builder
	line := 0
	var prev entry
	havePrev := false
	for _, u := range m.transcriptUnits(es, w, true, m.focusIdx) {
		// The separator counts toward the line total before the unit starts,
		// so the gutter pointer and scrolling stay aligned.
		if havePrev {
			sep := separatorBefore(prev, u.sepBefore)
			b.WriteString(sep)
			line += strings.Count(sep, "\n")
		}
		n := strings.Count(u.text, "\n")
		if u.idx == m.focusIdx {
			selStart, selCount = line, n
		}
		b.WriteString(u.text)
		line += n
		prev, havePrev = u.sepAfter, true
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
