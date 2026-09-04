package chat

// Terminal interactivity (docs/interface/surfaces.md#reading-mode).
// Two things kept a reader stuck in the input box, and they are opposite
// failures of the same rule.
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
// The rule both halves follow: while the input owns the keyboard the viewport
// hears no keys at all, and the only things that move it are the ones a draft
// cannot produce — the wheel, pgup/pgdn, shift+arrows, ctrl+o.
//
// That list is split in two, because it had been conflating scrolling with
// giving up the keyboard. Reading is not a decision: the wheel always said so
// and the pager keys did not, so pgup took the draft off the screen to answer
// a question about the pane above it. Now every scroll gesture leaves the
// keyboard where it is, and ctrl+o is the one transfer — the reader who wants
// the row cursor, the [enter] expansions and the keys a close row or a
// failure offers asks for them, and gets focus mode, which is still the
// one reading surface. This file is how the keyboard gets to it and back.

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// wheelLines is how far one wheel notch moves the transcript. It matches the
// bubbles viewport default, so the gesture feels the same here as in every
// other pager the reader uses.
const wheelLines = 3

// keyScrollLines is how far shift+↑ / shift+↓ move it. One line, because the
// key is the fine adjustment in a pair: pgup/pgdn are there for the distance,
// and a reader nudging the transcript to bring one row back into view wants
// the row, not three of them.
const keyScrollLines = 1

// keys.Draft.Mouse flips terminal mouse reporting from anywhere. Ctrl+X
// because of what is left rather than what it stands for: the textarea
// underneath claims a, b, d, e, f, k, n, p, t, u, v and w; this surface
// spends c, d, e, g and j of its own; ctrl+s and ctrl+q belong to the
// terminal, and ctrl+z is the suspend the session now answers itself
// (terminal.go); and ctrl+o opens a step's detail (detail.go). It is not a
// mnemonic and does not pretend to be one — the start screen and /ui both
// name it, which is where a chord is actually learned.

// toggleMouse flips reporting, persists the answer, and tells the terminal.
// It is the chord's whole job, and the same path /ui mouse takes, so the two
// cannot drift into different states or different notices.
func (m Model) toggleMouse() (tea.Model, tea.Cmd) {
	note := m.setMouse(!m.mouseOn)
	return m.systemNotice(note)
}

// WithMouse sets whether the session starts with terminal mouse reporting on
// (appearance.mouse). On is the default.
func (m Model) WithMouse(on bool) Model {
	m.mouseOn = on
	return m
}

// updateMouse routes a mouse event: the wheel reads, and the primary button
// selects text (select.go) or clicks a target (click.go).
//
// The wheel reads, and reading is not a focus transfer: the draft keeps the
// keyboard, so a scroll while composing never swallows the next keystroke.
//
// The primary button carries both of the other gestures, and the event they
// are told apart by is the release rather than the press. A press that also
// expanded a row would make every drag a gamble on where it started; a
// release in the cell the press landed in cannot be a drag, because a drag
// that went anywhere released somewhere else. So the press anchors and
// nothing fires until the button comes back up. A middle or right button does
// nothing at all.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// In v2 the action a mouse event is comes from the message's own type
	// rather than from a field on one struct, which is why the wheel
	// no longer has to be told apart from a press by its button.
	mouse := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.scrollLines(-wheelLines)
		case tea.MouseWheelDown:
			m.scrollLines(wheelLines)
		}
		return m, nil
	}
	switch msg.(type) {
	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft {
			return m, nil
		}
		// Every press is remembered, wherever it lands: a click target can be
		// on the card in the bottom panel, which is not a surface a selection
		// can be anchored in (click.go).
		m.beginClick(mouse.X, mouse.Y)
		if !m.selectableSurface() {
			return m, nil
		}
		return m.beginSelection(mouse.X, mouse.Y)
	case tea.MouseMotionMsg:
		// Cell-motion reporting sends motion only while a button is down, so
		// this is a drag. The button is checked anyway, because terminals
		// disagree about what they put in the field on a drag and only the
		// primary one selects.
		if mouse.Button != tea.MouseLeft && mouse.Button != tea.MouseNone {
			return m, nil
		}
		if !m.selectableSurface() {
			return m, nil
		}
		return m.dragSelection(mouse.X, mouse.Y)
	case tea.MouseReleaseMsg:
		if m.endClick(mouse.X, mouse.Y) {
			// A press and a release in the same cell is a click, and a click
			// is not a selection: nothing was covered, so nothing is
			// copied and what was under the pointer is what the gesture
			// meant.
			if m.cancelSelection() {
				m.refreshTranscript()
			}
			return m.clickAt(mouse.X, mouse.Y)
		}
		if !m.selectableSurface() {
			return m, nil
		}
		// A release reports no button at all under X10 encoding, so the drag
		// in flight is what says whether this release is ours.
		return m.releaseSelection(mouse.X, mouse.Y)
	}
	return m, nil
}

// scrollLines moves whichever surface is showing content by delta rows. The
// full-screen diff and review mode own the screen when they are up, so the
// wheel has to reach them rather than the transcript behind
// them — that is the "code viewport" half of the story.
func (m *Model) scrollLines(delta int) {
	if delta == 0 {
		return
	}
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		m.fullDiff.Scroll(delta)
	case m.state == stateOutputFull && m.fullOutput != nil:
		m.fullOutput.Scroll(delta)
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
	case m.state == stateOutputFull && m.fullOutput != nil:
		m.fullOutput.Scroll(dir * max(m.viewportHeight()-1, 1))
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

// searchTranscript looks for what the reader typed in the pane they are
// reading, and reports how many times it is there. It is the reading
// surface's own search: the transcript is one scroll of prose, and the
// question a reader has in the middle of it is where a path or an error was
// said, not which row said it.
//
// It goes to the pane rather than to a viewer with a screen of its own, for
// the same reason the wheel does — whatever is holding the pane is what a
// gesture over the pane means. The full-screen diff and review mode scroll
// themselves and have no search; a query typed while one of those is up finds
// nothing rather than moving the transcript behind it.
func (m *Model) searchTranscript(query string) int {
	if m.paneTakenOver() {
		return 0
	}
	found := m.viewport.Search(query)
	m.atBottom = m.viewport.AtBottom()
	return found
}

// searchStep moves to the next occurrence, dir being -1 back and +1 on. The
// pane follows it, which pauses the follow-the-live-end the way any other
// scroll does.
func (m *Model) searchStep(dir int) {
	if m.paneTakenOver() {
		return
	}
	if dir < 0 {
		m.viewport.PrevMatch()
	} else {
		m.viewport.NextMatch()
	}
	m.atBottom = m.viewport.AtBottom()
}

// searchNotice is what the rail says while a search is open: which occurrence
// the reader is on and how many there are, or that there are none. A count on
// its own would leave "is there another one below me" unanswered, which is
// the question the keys next to it answer.
func (m Model) searchNotice() string {
	if !m.viewport.Searching() {
		return ""
	}
	at, total := m.viewport.MatchPosition()
	if total == 0 {
		return "no match"
	}
	return fmt.Sprintf("%d/%d", at, total)
}

// setSearchQuery is one edit of the query row. The search is incremental —
// every keystroke is a new question, answered before the next one is asked —
// because a query that only ran on enter would make the reader spell a path
// out blind.
//
// The pane is brought to what the new query found, and the follow is read
// back after that rather than before: the jump is a scroll like any other and
// pauses the follow-the-live-end the same way.
func (m *Model) setSearchQuery(query string) {
	m.searchTranscript(query)
	m.viewport.RevealMatch()
	m.atBottom = m.viewport.AtBottom()
}

// typeSearch puts a character into the query.
func (m *Model) typeSearch(text string) {
	m.setSearchQuery(m.viewport.SearchQuery() + text)
}

// backspaceSearch takes the last character back, by runes rather than bytes:
// a query is prose and a path, and deleting half of a multi-byte character
// would leave the pane matching against something nobody typed.
func (m *Model) backspaceSearch() {
	q := []rune(m.viewport.SearchQuery())
	if len(q) == 0 {
		return
	}
	m.setSearchQuery(string(q[:len(q)-1]))
}

// transcriptSearchHead is the query row up to the end of what has been typed:
// the label and the query. The cursor stands at the end of it, so its width
// is measured here rather than written down a second time.
func (m Model) transcriptSearchHead() string {
	return sty.Search.Label.Render("search") +
		sty.Search.State.Render(": ") + sty.Search.Query.Render(m.viewport.SearchQuery())
}

// transcriptSearchLines are the query row and its two keys, which is what
// reading mode's key bar becomes while the row is open. The mode's own keys
// are not on it: every letter is going into the query, so offering them would
// be offering what the surface cannot honour.
//
// It is the input history search's row in a second place, deliberately — both
// are a query stating itself on one line with its count, and drawing them
// differently would say they were different kinds of thing.
func (m Model) transcriptSearchLines(width int) []string {
	state := "type to search"
	if m.viewport.Searching() {
		at, total := m.viewport.MatchPosition()
		state = fmt.Sprintf("%d of %d", at, total)
		if total == 0 {
			state = "no match"
		}
	}
	row := m.transcriptSearchHead() + sty.Search.State.Render(" · "+state)
	hint := strings.Join([]string{
		keys.Shown(keys.Find.Keep) + " " + keys.Words(keys.Find.Keep),
		keys.Shown(keys.Find.Clear) + " " + keys.Words(keys.Find.Clear),
	}, " · ")
	return []string{clipRow(row, width), sty.Search.Hint.Render(clipRow(hint, width))}
}

// readingSearchCursor is where the terminal's cursor stands in reading mode's
// panel: after the query, which is what the next keystroke extends. With the
// row closed the mode is read rather than written and places none, and the
// terminal hides its cursor over it (the register's cursor column,
// overlay.go).
func (m Model) readingSearchCursor(width int) *tea.Cursor {
	if !m.viewport.SearchOpen() {
		return nil
	}
	// The row itself is clipped to the panel's width, so the cursor stops
	// where the row does rather than standing past its end.
	return tea.NewCursor(min(lipgloss.Width(m.transcriptSearchHead()), max(width-1, 0)), 0)
}

// paneTakenOver reports that something other than the transcript is drawing
// in the pane and doing its own scrolling.
func (m Model) paneTakenOver() bool {
	switch {
	case m.state == stateDiffFull && m.fullDiff != nil:
		return true
	case m.state == stateOutputFull && m.fullOutput != nil:
		return true
	case m.state == stateReview && m.review != nil:
		return true
	}
	return false
}

// returnToInput leaves focus mode carrying the keystroke that ended it, so
// the character a reader typed lands in the draft instead of being spent on
// the exit. Esc and typing are the two ways out; this is the second.
func (m Model) returnToInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	next, _ := m.exitFocusMode()
	rm, ok := next.(Model)
	if !ok {
		return next, nil
	}
	var cmd tea.Cmd
	rm.input, cmd = rm.input.Update(msg)
	rm.syncCompletions()
	rm.syncViewport()
	rm.viewport.SetLines(rm.renderHistoryLines())
	return rm, cmd
}

// typedRune reports a plain printable keystroke — the kind that belongs in
// the draft. Focus mode's own letters are matched before this is asked, so
// what reaches it is a character the transcript has no use for.
//
// Text is exactly that question in v2: it carries the characters a key
// contributes and is empty for everything else, including a key held under a
// modifier that changes what it means.
func typedRune(msg tea.KeyPressMsg) bool { return msg.Text != "" }

// followNotice says that the transcript is no longer showing its live end,
// and how far off it the reader is.
//
// Scrolling away pauses the follow — tokenMsg only calls GotoBottom while
// atBottom — and until now nothing said so. A reader who scrolled up to check
// a path during a turn watched a transcript that had silently stopped growing
// under them, with no way to tell that from a model that had stopped talking.
// That was survivable while scrolling cost a keyboard handover, because the
// labelled rail said READING and a mode is its own explanation. Now that the
// draft keeps the keyboard, the only thing on screen that changed is the part
// nobody is looking at, so the state has to say its own name.
//
// It is a notice rather than a key: pgdn already walks back to the end and
// re-pins on arrival, so there is nothing here to offer that the reader does
// not have. Reading mode has its own rail and position, so this stays
// out of its way.
func (m Model) followNotice() string {
	if m.state == stateFocus || m.viewport.AtBottom() {
		return ""
	}
	below := m.viewport.TotalLineCount() - m.viewport.YOffset() - m.viewport.VisibleLineCount()
	if below <= 0 {
		return ""
	}
	line := "lines"
	if below == 1 {
		line = "line"
	}
	// The word carries it, the glyph repeats it, and neither needs the colour
	// (invariant 1).
	return fmt.Sprintf("↓ %d %s below · [pgdn] the live end", below, line)
}

// transcriptBody is the viewport with its scroll gutter glued to the right
// . The viewport pads every row it returns to its own width and
// returns exactly its own height of them, so the gutter is a glyph appended
// per row rather than a column that has to be laid out.
//
// It is the transcript's, so every surface the viewport shows gets it — the
// feed, reading mode, an attached child's session — and the two surfaces that
// take the pane over instead of filling the viewport, the full-screen diff
// and review mode, do not: they scroll themselves and say so on their own
// status bars.
func (m Model) transcriptBody() string {
	view := m.viewport.View()
	rows := components.Scrollbar(m.viewport.Height(),
		m.viewport.TotalLineCount(), m.viewport.Height(), m.viewport.YOffset())
	if rows == nil {
		return view
	}
	lines := strings.Split(view, "\n")
	for i := range lines {
		if i >= len(rows) {
			break
		}
		lines[i] += rows[i]
	}
	return strings.Join(lines, "\n")
}

// readingRail is the line under the header. It is a plain divider while the
// input owns the keyboard and names the transcript when the transcript does,
// so the two panes are never both dressed as the active one. The word
// carries it; the accent is decoration, as everywhere else (invariant 1).
//
// It is the same labelled rail a waiting decision draws: four cells of
// rule, the label in its own spaces, then the rule to the edge. Reading mode
// shipped before that rail existed and borrowed components/terminal/Rule's
// trailing variant, which hung the label off the right end — the one place
// the three rails that name the keyboard's owner did not look alike.
func (m Model) readingRail(width int) string {
	if m.state != stateFocus || width <= 0 {
		return dividerStyle(width)
	}
	if width < frameCompactWidth {
		// Below the minimal breakpoint the word goes rather than being cut
		// down (guidelines/layout-breakpoints): the hint bar under the
		// transcript still says where the keyboard is, and the lit row still
		// says which row it is, so dropping the label costs nothing there.
		return dividerStyle(width)
	}
	return keyboardRail(m.readingLabel(), width)
}

// readingLabel is READING plus the cursor's place in the selectable rows,
// which is the transcript's answer to "how much of this is there". A
// transcript with nothing expandable is being read rather than navigated, so
// it has no place to report.
//
// An open search takes that place over. Both answer "where am I in this", and
// while a query is up the occurrence is the one the reader is moving through
// — the row cursor is not what the next key moves.
func (m Model) readingLabel() string {
	if note := m.searchNotice(); note != "" {
		return "READING · " + note
	}
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
	if m.mouseOn {
		return "on"
	}
	return "off"
}

// mouseCommand handles /ui mouse: whether the terminal reports the mouse to
// shhh at all. On, the wheel scrolls the transcript and the full-screen
// viewers, a drag selects transcript text shhh copies itself, and a
// click opens a row or answers a decision key; off, the terminal
// keeps its own click-drag selection and the keyboard is the only way through
// the history. It is a real trade, which is why it is a setting and not a
// default nobody can reach.
func (m *Model) mouseCommand(parts []string) string {
	if len(parts) == 2 {
		return "Mouse reporting: " + m.mouseStatus() +
			".\nUsage: /ui mouse <on|off> — on, the wheel scrolls the transcript, click-drag selects it and a click opens the row or card key under it; off, the terminal keeps its own click-drag selection."
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
	if on == m.mouseOn {
		return "Mouse reporting is already " + m.mouseStatus() + "."
	}
	return m.setMouse(on)
}

// setMouse flips reporting and persists the answer, so the choice outlives
// the process that made it. A session with no writer still flips — the
// setting is real either way — and says only what it could not do.
func (m *Model) setMouse(on bool) string {
	m.mouseOn = on
	// Reporting off hands the selection back to the terminal, so shhh's own
	// has to let go of it — including any edge scroll still running under a
	// drag the reader never released.
	if !on && m.cancelSelection() {
		m.refreshTranscript()
	}
	note := mouseNote(on)
	if m.writeConfig == nil {
		return note + "\nThis session cannot write the config file, so it is for this session only."
	}
	if err := m.writeConfig("appearance.mouse", strconv.FormatBool(on)); err != nil {
		return note + "\nIt could not be saved: " + err.Error()
	}
	return note + " Saved — new sessions start this way."
}

// mouseNote says what the new state costs and what it buys, because both
// readings are a trade rather than an improvement.
//
// The on-side used to say the terminal's selection "needs shift (or option)
// held", which was true and useless: shift-drag selects what is on the
// screen, and the thing a reader reaches for the mouse to copy is usually
// longer than the screen. So the note names what shhh does instead — a drag
// that scrolls with the selection and copies on release — and the
// off-side names what the terminal gives back.
func mouseNote(on bool) string {
	if on {
		return "Mouse reporting on — the wheel scrolls the transcript, click-drag selects it (the drag scrolls past the edge of the pane, esc cancels, and releasing copies), and a click opens the activity row or answers the approval key under it."
	}
	return "Mouse reporting off — the terminal keeps click-drag selection for what is on screen; pgup, ctrl+o and j/k read the transcript."
}

// readingStyles is the reading rail's own group.
type readingStyles struct {
	Label lipgloss.Style
	Rule  lipgloss.Style
}

func newReadingStyles(p components.ColorTokens) readingStyles {
	return readingStyles{
		// The label is info and bold, as DRAFT, DECISION and READING all are
		// in guidelines/invariant-inert-keys; the rule it sits on is chrome,
		// so it is dim like every other divider. The accent belongs to the
		// rows.
		Label: lipgloss.NewStyle().Bold(true).Foreground(p.Info.Color()),
		Rule:  lipgloss.NewStyle().Foreground(p.Dim.Color()),
	}
}
