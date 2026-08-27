package chat

// Terminal interactivity (S-115, DESIGN-TUI.md §7a). Two things kept a reader
// stuck in the input box, and they are opposite failures of the same rule.
//
// The wheel was never enabled, so a trackpad gesture over the transcript did
// nothing at all — the viewport had MouseWheelEnabled set and no mouse events
// were ever reported to reach it.
//
// And the viewport was handed every keystroke on its way past the input, so
// its pager bindings fired from inside a sentence: bubbles binds j, k, u, d,
// f, b and the spacebar by default, which means typing "just fix the build"
// scrolled the history four times before the space bar paged it.
//
// The rule both halves now follow: while the input owns the keyboard the
// viewport hears no keys at all, and the only ways into the transcript are
// the ones a draft cannot produce — the wheel, pgup/pgdn, ctrl+e, and ↑ once
// the input history has nothing left to recall. Everything the transcript
// then offers lives in focus mode (§7), which is the one reading surface;
// this file is how the keyboard gets to it and back.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// wheelLines is how far one wheel notch moves the transcript. It matches the
// bubbles viewport default, so the gesture feels the same here as in every
// other pager the reader uses.
const wheelLines = 3

// mouseCmd turns terminal mouse reporting on or off. Reporting is on for the
// session by default — the wheel is the gesture people reach for first — and
// `/ui mouse off` gives the terminal's own click-drag selection back, which
// is the one thing tracking costs.
func mouseCmd(on bool) tea.Cmd {
	if on {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

// updateMouse routes a mouse event. Only the wheel does anything: shhh draws
// no clickable targets, so a press is better ignored than guessed at, and a
// gesture that moved the cursor is not a decision anyone made.
//
// The wheel reads, and reading is not a focus transfer: the draft keeps the
// keyboard, so a scroll while composing never swallows the next keystroke.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollLines(-wheelLines)
	case tea.MouseButtonWheelDown:
		m.scrollLines(wheelLines)
	}
	return m, nil
}

// scrollLines moves whichever surface is showing content by delta rows. The
// full-screen diff and review mode own the screen when they are up (§3c,
// §16a), so the wheel has to reach them rather than the transcript behind
// them — that is the "code viewport" half of the story.
func (m *Model) scrollLines(delta int) {
	if delta == 0 {
		return
	}
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		m.fullDiff.Scroll(delta)
	case m.state == stateReview && m.review != nil:
		m.review.Scroll(delta)
	default:
		if delta < 0 {
			m.viewport.ScrollUp(-delta)
		} else {
			m.viewport.ScrollDown(delta)
		}
		m.atBottom = m.viewport.AtBottom()
	}
}

// scrollPage moves the same surface by a page, dir being -1 up and +1 down.
func (m *Model) scrollPage(dir int) {
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		m.fullDiff.Scroll(dir * max(m.viewportHeight()-1, 1))
	case m.state == stateReview && m.review != nil:
		m.review.Scroll(dir * max(m.viewportHeight()-1, 1))
	default:
		if dir < 0 {
			m.viewport.PageUp()
		} else {
			m.viewport.PageDown()
		}
		m.atBottom = m.viewport.AtBottom()
	}
}

// enterReading hands the keyboard to the transcript and pages it in dir. It
// opens the same surface ctrl+e does, because there is only one: the row
// cursor, the [enter] expansions and the keys a close row or a failure offers
// all come with it. A pager key that opened a second, lesser reading mode
// would be a fourth list implementation by another name.
//
// Paging down with nothing below is not a transfer: the bottom of the
// transcript is where the input already stands.
func (m Model) enterReading(dir int) (tea.Model, tea.Cmd) {
	if dir > 0 && m.state != stateFocus && m.viewport.AtBottom() {
		return m, nil
	}
	next, cmd := m.enterFocusMode()
	rm, ok := next.(Model)
	if !ok || rm.state != stateFocus {
		// The transcript had nothing to read and said so; the notice is the
		// answer, not a half-entered mode.
		return next, cmd
	}
	rm.scrollPage(dir)
	return rm, cmd
}

// returnToInput leaves focus mode carrying the keystroke that ended it, so
// the character a reader typed lands in the draft instead of being spent on
// the exit. Esc and typing are the two ways out (§7a); this is the second.
func (m Model) returnToInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	next, _ := m.exitFocusMode()
	rm, ok := next.(Model)
	if !ok {
		return next, nil
	}
	var cmd tea.Cmd
	rm.input, cmd = rm.input.Update(msg)
	rm.syncCompletions()
	rm.syncViewport()
	rm.viewport.SetContent(rm.renderHistory())
	return rm, cmd
}

// typedRune reports a plain printable keystroke — the kind that belongs in
// the draft. Focus mode's own letters are matched before this is asked, so
// what reaches it is a character the transcript has no use for.
func typedRune(msg tea.KeyMsg) bool {
	if msg.Alt {
		return false
	}
	switch msg.Type {
	case tea.KeyRunes:
		return len(msg.Runes) > 0
	case tea.KeySpace:
		return true
	}
	return false
}

// readingRail is the line under the header. It is a plain divider while the
// input owns the keyboard and names the transcript when the transcript does,
// so the two panes are never both dressed as the active one (§7a). The word
// carries it; the accent is decoration, as everywhere else (invariant 1).
func (m Model) readingRail(width int) string {
	if m.state != stateFocus || width <= 0 {
		return dividerStyle(width)
	}
	label := readingLabelStyle.Render(m.readingLabel())
	lw := lipgloss.Width(label)
	if width < lw+4 {
		// Too narrow for the label: the hint bar under the transcript still
		// says where the keyboard is, so the rail goes back to a divider
		// rather than clipping the word that carries the meaning.
		return dividerStyle(width)
	}
	// The label sits in its own spaces on the rule, as every other rail's
	// does (§12a).
	rule := readingRuleStyle.Render(strings.Repeat("─", width-lw-3))
	return rule + " " + label + " " + readingRuleStyle.Render("─")
}

// readingLabel is READING plus the cursor's place in the selectable rows,
// which is the transcript's answer to "how much of this is there". A
// transcript with nothing expandable is being read rather than navigated, so
// it has no place to report.
func (m Model) readingLabel() string {
	pos, total := m.readingPosition()
	if total == 0 {
		return "READING"
	}
	return fmt.Sprintf("READING %d/%d", pos, total)
}

// readingPosition is the 1-based index of the selected row among the
// selectable ones, and how many there are.
func (m Model) readingPosition() (pos, total int) {
	idxs := m.expandableIndices()
	for i, idx := range idxs {
		if idx == m.focusIdx {
			return i + 1, len(idxs)
		}
	}
	return 0, len(idxs)
}

// mouseStatus describes the current mouse-reporting state for /ui.
func (m Model) mouseStatus() string {
	if m.mouseOff {
		return "off"
	}
	return "on"
}

// mouseCommand handles /ui mouse: whether the terminal reports the wheel to
// shhh at all. On, the wheel scrolls the transcript and the full-screen
// viewers; off, the terminal keeps its own click-drag selection and the
// keyboard is the only way through the history. It is a real trade, which is
// why it is a setting and not a default nobody can reach.
func (m *Model) mouseCommand(parts []string) string {
	if len(parts) == 2 {
		return "Mouse reporting: " + m.mouseStatus() +
			".\nUsage: /ui mouse <on|off> — on, the wheel scrolls the transcript; off, the terminal keeps click-drag selection."
	}
	if len(parts) != 3 {
		return "Usage: /ui mouse <on|off>"
	}
	var on bool
	switch parts[2] {
	case "on", "true", "yes":
		on = true
	case "off", "false", "no":
		on = false
	default:
		return fmt.Sprintf("Error: unknown mouse setting %q (on, off)", parts[2])
	}
	if on == !m.mouseOff {
		return "Mouse reporting is already " + m.mouseStatus() + "."
	}
	m.mouseOff = !on
	if on {
		return "Mouse reporting on — the wheel scrolls the transcript; the terminal's own selection needs shift (or option) held."
	}
	return "Mouse reporting off — the terminal keeps click-drag selection; pgup, ctrl+e and j/k read the transcript."
}

// applyNavigateStyles rebuilds this file's styles from the palette; called
// from applyPalette with the rest.
func applyNavigateStyles(p components.ColorTokens) {
	readingLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Accent)
	readingRuleStyle = lipgloss.NewStyle().Foreground(p.Accent)
}
