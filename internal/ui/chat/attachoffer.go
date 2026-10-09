package chat

// The attach offer: the frame's top rail names the attach chord only while the
// clipboard holds something the chord would take.
//
// Whether it does is a fact about the system clipboard, which shells out and
// is read off the render loop, so it is asked twice and no more: when the
// frame goes idle, and when the window comes back to the front — the two
// moments a person has been somewhere else and copied something. It is never
// asked on a tick or a keystroke, and the session is only given a reader when
// the host has one (Wiring.ClipboardRead), so a screen built without one asks
// nothing. The key itself is always live and always in the key list
// (docs/interface/surfaces.md#the-input-frame).

import (
	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/attachment"
)

// clipboardOfferMsg is what one read of the clipboard found to offer: the
// name of the first thing the attach chord would take, or nothing.
type clipboardOfferMsg struct{ name string }

// clipboardOfferCmd reads the clipboard through the reader the attach chord
// uses and reports the name of what it holds, off the render loop.
func clipboardOfferCmd(read func() (attachment.Clipboard, error)) tea.Cmd {
	return func() tea.Msg {
		clip, err := read()
		if err != nil || len(clip.Attachments) == 0 {
			return clipboardOfferMsg{}
		}
		return clipboardOfferMsg{name: clip.Attachments[0].Name}
	}
}

// offerWindow is whether the frame is idle for the offer's purposes: the
// screen is up, and no turn is working, held or waiting on a decision.
func (m Model) offerWindow() bool { return m.ready && !m.turnInFlight() }

// attachOfferCmd is the tail's rule for the offer: a read when the model has
// just gone idle, or the window has just come back while it is.
func (m Model) attachOfferCmd(before Model) tea.Cmd {
	read := m.wiring.ClipboardRead
	if read == nil || !m.offerWindow() {
		return nil
	}
	wentIdle := !before.offerWindow()
	regained := before.away && !m.away
	if !wentIdle && !regained {
		return nil
	}
	return clipboardOfferCmd(read)
}
