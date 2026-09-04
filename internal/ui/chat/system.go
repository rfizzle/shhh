package chat

// The system route: what the terminal and the window did.
//
// None of it is the session's own turn and none of it is a key, which is why
// it is answered first and answered whole. It is also the route with the
// least to say about the session: a size, a wheel notch, a paste, a window
// losing focus, and the ticks that close a window shhh opened on the clock.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/attachment"
)

// updateSystem answers a message from the terminal or the window. handled is
// false for a message this route does not own, which is every key and every
// report the turn makes about itself.
func (m Model) updateSystem(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.EnvMsg:
		// The program's own environment, which over ssh is the client's
		// terminal rather than this machine's. It arrives once, at
		// startup, and asking is the only thing to do with it.
		return m, m.caps.Query(msg), true
	case tea.FocusMsg:
		// The window came back to the front. Nothing on screen changes; what
		// changes is whether shhh may assume nobody is looking.
		m.away = false
		return m, nil, true

	case tea.BlurMsg:
		m.away = true
		return m, nil, true

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// The horizontal pass first, because nothing above it depends on a
		// height; the vertical rows are read after it, and read again on the
		// tail when fitting the draft to the new width re-wrapped it into a
		// different number of rows (layout.go, syncInputHeight).
		m.fitDraft()
		// The transcript wraps to its pane, which is narrower than the content
		// width while the inspector rail shows.
		paneWidth := m.transcriptWidth()
		vpHeight := m.viewportHeight()
		// Every rendered line reflows at a new width, so a selection's
		// coordinates stop naming the text they were taken over.
		m.resizeSelection(paneWidth)

		if !m.ready {
			m.viewport = newViewport(paneWidth, vpHeight)
			m.viewport.SetLines(m.renderHistoryLines())
			m.ready = true
			return m, m.placePicture(), true
		}
		// The rectangles move on every message: the screen is never the wrong
		// shape, and the pane clips or pads the lines it already has. What
		// waits is the re-wrap of the whole history — a drag across the
		// terminal edge delivers a size per column crossed, and re-rendering
		// a long session at every intermediate width is the stutter, so the
		// render runs once, when the size has stopped moving (resizeSettled).
		m.viewport.SetWidth(paneWidth)
		m.viewport.SetHeight(vpHeight)
		m.resizeSeq++
		seq := m.resizeSeq
		settle := tea.Tick(resizeSettle, func(time.Time) tea.Msg { return resizeSettledMsg{seq: seq} })
		// A placement is cells at a size: a pane that changed
		// shape under a picture the terminal is holding is a picture that no
		// longer fits the hole left for it, so it is sent again at the new
		// one. Every other surface reflows from its own View.
		return m, tea.Batch(m.placePicture(), settle), true

	case resizeSettledMsg:
		// The size held still for the settle window. A stale settle — the
		// terminal moved again after this one was scheduled — matches nothing
		// and changes nothing; its successor carries the render.
		if msg.seq != m.resizeSeq {
			return m, nil, true
		}
		m.viewport.SetLines(m.renderHistoryLines())
		return m, nil, true

	case tea.MouseMsg:
		// The wheel scrolls whatever is showing content — the transcript, or
		// the full-screen diff and review surfaces that take the screen from
		// it. It never reaches the textarea, which is what made a scroll
		// gesture over the conversation move the three-line prompt box
		//. Press, drag and release own the transcript's text
		// selection (select.go).
		if !m.mouseOn {
			return m, nil, true
		}
		return answered(m.updateMouse(msg))

	case coalescedWheelMsg:
		// A wheel flood merged at the program boundary (wheel.go): one scroll
		// for the whole run, through the same per-surface switch a single
		// notch takes, then the message the flush was triggered by.
		if m.mouseOn {
			m.scrollLines(msg.lines)
		}
		if msg.then != nil {
			return answered(m.update(msg.then))
		}
		return m, nil, true

	case tea.PasteMsg:
		// A file dragged into the terminal arrives as a bracketed paste of
		// its path. When it points at an image or a document, attaching it
		// is the only thing the gesture can have meant; everything
		// else pastes as the text it is.
		//
		// In v2 a paste is a message of its own rather than a keystroke
		// wearing a Paste flag. What that flag bought was routing:
		// pasted text reached whichever surface had the keyboard, so a paste
		// into a card's filter row filtered. So the text is handed on
		// as the keystroke it used to be — one press carrying the whole run,
		// which is what v1 delivered — and every surface below sees exactly
		// what it saw before.
		if m.inputLive() && m.attachedTo == "" {
			if path, ok := pastedFileAttachment(msg.Content); ok {
				return m, attachFileCmd(m.inWorkspace(path)), true
			}
			// A stack trace or a log is a file that happens to have arrived
			// through the clipboard, and typing it into a three-row box hides
			// the sentence it was meant to go with (attachments.go). The
			// line endings are settled first, because the count that decides
			// this is a count of newlines and a terminal is free to send
			// carriage returns.
			if pasted := attachment.NormalizeNewlines(msg.Content); m.pasteOverflows(pasted) {
				return answered(m.stagePaste(pasted))
			}
		}
		if msg.Content == "" {
			return m, nil, true
		}
		return answered(m.update(tea.KeyPressMsg{
			Code: []rune(msg.Content)[0],
			Text: msg.Content,
		}))

	case selectionScrollMsg:
		// A drag held at the edge of the transcript pane. It is
		// answered whatever the surface, because the fence inside it is what
		// decides whether the tick is still wanted — a selection cancelled
		// between the tick being scheduled and arriving has already bumped
		// the sequence.
		return answered(m.updateSelectionScroll(msg))

	case armExpiredMsg:
		// The window between a first press and its second shut on its own;
		// repainting is what reverts the rail's hint. A stale expiry — the
		// window was consumed or re-armed since — matches nothing and
		// changes nothing.
		if msg.seq == m.armed.seq {
			m.disarm()
		}
		return m, nil, true

	case progressClearedMsg:
		// The tab's red has been up long enough (terminal.go). A stale tick —
		// another turn has started or broken since — matches nothing and
		// leaves the state it would have cleared alone.
		if msg.seq == m.progressSeq {
			m.progressFailed = false
		}
		return m, nil, true

	case tea.ResumeMsg:
		// Back from a suspend, with the terminal in whatever state the shell
		// left it (terminal.go).
		return answered(m.resumed())

	case graceTickMsg:
		// The grace window expired between keys; arriving here repaints the
		// card with its keys live. A stale tick repaints a card that already
		// looks right, which costs nothing.
		return m, nil, true

	}
	return m, nil, false
}
