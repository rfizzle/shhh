package chat

// The pointer (docs/interface/surfaces.md#reading-mode): reading mode's
// cursor, seen from the prompt.
//
// Everything in the pane that can be opened has one mechanism behind it —
// the cursor reading mode puts on a row — and reaching it used to cost the
// handover: ctrl+o, the mode's own keys, esc back. That is the right price
// for a row's letters, which are text everywhere else, and the wrong price
// for the two acts that have no letter in them: standing on a row and
// opening it. So those two are reachable from the prompt on four chords
// (keys.Draft.PointUp and its three siblings), and what they move is the
// same index the mode moves. The draft keeps the keyboard and every
// character; the pane scrolls only as far as keeps the pointed row in view;
// the gutter drawn is the mode's gutter, minus the row's letters, which are
// not live here and are drawn as they are everywhere the draft has the
// keyboard.
//
// The pointer is a flag beside the index rather than a state of its own,
// because a state is what holds the keyboard and this holds nothing. It is
// lit by the chords, dropped by esc, handed to reading mode by ctrl+o, and
// it clears itself when the row it names is gone.

// pointerLit reports whether the pointer is on screen: lit, on a row that is
// still there to point at, while the draft holds the keyboard.
func (m Model) pointerLit() bool {
	if !m.pointer || m.state == stateFocus || m.attachedTo != "" || !m.inputLive() {
		return false
	}
	for _, idx := range m.expandableIndices() {
		if idx == m.focusIdx {
			return true
		}
	}
	return false
}

// gutterShowing reports whether the transcript is drawn with the selection
// gutter: under reading mode, or under a lit pointer.
func (m Model) gutterShowing() bool {
	return m.state == stateFocus || m.pointerLit()
}

// movePointer lights the pointer or moves it. Unlit, either chord lights it
// on the row reading mode would open on — the most recent one, or the
// failure that ended the turn — because the first press is "show me where I
// am" and the second is the step. Lit, it steps. A pane with nothing to
// point at does nothing: the chords are the pointer's, not a scroll's.
func (m *Model) movePointer(dir int) {
	idxs := m.expandableIndices()
	if len(idxs) == 0 {
		return
	}
	if !m.pointerLit() {
		// The gutter shifts every line two columns, and a selection's
		// coordinates were taken before it did — the same reason reading
		// mode drops one on the way in (enterSurface).
		m.cancelSelection()
		m.pointer = true
		m.focusIdx = m.openingCursor(idxs)
		m.refreshCursorView()
		return
	}
	for pos, idx := range idxs {
		if idx == m.focusIdx {
			if next := pos + dir; next >= 0 && next < len(idxs) {
				m.focusIdx = idxs[next]
			}
			break
		}
	}
	m.refreshCursorView()
}

// openingCursor is the row a cursor lands on when nothing has chosen one
// yet: the most recent selectable row, unless a failure or a dropped stream
// sits under the close rows that follow it — the row that broke the turn is
// the one holding the way out, so that is where the cursor belongs.
func (m Model) openingCursor(idxs []int) int {
	es := *m.entries()
	at := idxs[len(idxs)-1]
	for i := len(idxs) - 1; i >= 0; i-- {
		if es[idxs[i]].kind == entryTurnClose {
			continue
		}
		if k := es[idxs[i]].kind; k == entryFailure || k == entryStreamDrop {
			at = idxs[i]
		}
		break
	}
	return at
}

// closePointed is reading mode's collapse on the pointed row. With nothing
// open under it the chord does nothing, which is what the mode's [-] does
// too — minus the letter going to the draft, since a chord is not a letter.
func (m *Model) closePointed() {
	if m.collapseFocused() {
		m.refreshCursorView()
	}
}

// dropPointer puts the pane back the way it was before the pointer was lit,
// and reports whether there was one to drop. A flag set on a row that has
// since gone is not a pointer on screen, so the key is not claimed for it
// and the flag is left where it is: a row folded away can be given back.
func (m *Model) dropPointer() bool {
	if !m.pointerLit() {
		return false
	}
	m.pointer = false
	m.invalidateRenderCache()
	m.viewport.SetLines(m.renderHistoryLines())
	return true
}

// refreshCursorView redraws the pane around the cursor and keeps it in view,
// from either side of the handover. Under reading mode it is the mode's own
// refresh; under the pointer it is the same refresh with one more thing to
// keep true: a reader who moved the pointer off the live end has scrolled,
// and the next stream flush must not snap them back to the bottom.
func (m *Model) refreshCursorView() {
	m.invalidateRenderCache()
	m.refreshFocusView()
	if m.state != stateFocus {
		// The mode's refresh draws the bare render; the feed draws the
		// same render with a selection lit over it (renderHistoryLines),
		// and the next flush would use the feed's. Drawing it that way
		// here too is what keeps a frame from alternating between the two.
		m.viewport.SetLines(m.renderHistoryLines())
		m.atBottom = m.viewport.AtBottom()
	}
}
