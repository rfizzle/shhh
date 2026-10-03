package chat

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// pointerState is what the mouse and the pane's pointer are doing on the
// session: whether the terminal reports the mouse at all, the cell the
// button went down in, the drag selection and its edge scroll, the rail cell
// a surface was opened from, the pointer lit from the prompt, and the one-line
// notices a copy or a fold leaves on the rail. The selection (select.go), the
// clicks (click.go, railclick.go), the pointer (pointer.go), reading mode
// (focus.go) and the notice rail (frame.go) all read it.
//
// It is a value, held by value on the Model and copied with it every frame
// like the rest of the Model, so it takes no pointer of its own: every field
// below was a field on the Model before it was gathered here, and none of
// them is a pointer, a map or a slice.
//
// It has no clear. Nothing drops these fields together: the selection is
// dropped with its notice and its edge scroll (clearSelection, select.go), a
// press by its release, the lit pointer and the held row by the mode that
// takes the keyboard (focus.go), and the fold's notice by the next key
// (update).
type pointerState struct {
	// mouseOn turns terminal mouse reporting on (ctrl+x, /ui mouse). It is
	// on by default so the wheel scrolls the transcript, click-drag selects
	// text, and clicks open rows or answer cards. Turning it off hands
	// selection back to the terminal's native selector.
	mouseOn bool
	// Application-owned transcript selection (select.go). sel is the
	// selection itself — anchor, endpoint, and whether the button is still
	// down — in rendered-transcript coordinates. scrollDir and scrollSeq
	// drive the edge auto-scroll: the direction a drag held at the edge of
	// the pane is asking for, and the fence that stops a tick which outlived
	// its drag. selNotice is the notice rail's line after a successful copy.
	sel       selection
	scrollDir int
	scrollSeq int
	selNotice string
	// foldNotice is the notice rail's line after esc folded the rows the
	// reader had opened, or after it found only the verbosity's open
	// (readinghint.go). It lasts exactly one press: the next key clears it on
	// the way in, the way an armed two-press window is consumed, because it
	// is an account of the press just made and not a state of the session.
	foldNotice string
	// press is the cell the primary button last went down in (
	// click.go). A click is a press and a release in the same cell, which is
	// what lets one button carry both the selection drag and the targets.
	press pointerPress
	// lit is whether the pane's pointer is lit from the prompt: reading
	// mode's cursor (focusIdx) drawn while the draft holds the keyboard
	// (pointer.go). A flag beside the index rather than a state of its own,
	// because the keyboard does not move.
	lit bool
	// rowHeld is whether reading mode was opened by the handover on a
	// recovery or round-limit row (keyroute.go). The row was handed the
	// keyboard the way a card is, so answering it hands the keyboard back
	// the way answering a card does (focus.go's rowLetter), rather than
	// leaving the reader in a mode they never asked to read in.
	rowHeld bool
	// railOpened is the rail cell a surface was opened from, and the surface
	// (railclick.go).
	railOpened railOpening
}

// pointerKey is what the pointer's keyboard made of a key, for updateKey to
// carry out on the session.
type pointerKey int

const (
	// pointerPass is a key that is not the pointer's; routing goes on.
	pointerPass pointerKey = iota
	// pointerToggleMouse is the mouse chord: turn mouse reporting over
	// (toggleMouse).
	pointerToggleMouse
)

// update reads a key for the pointer before any surface gets it, and says
// what it is. Every key clears the fold's account of the press before it
// (readinghint.go): it says what one press did, so it lasts exactly as long
// as that press is the last thing the reader did.
//
// Mouse reporting is the one setting with a chord of its own (reading mode),
// and the only key answered before the surfaces are: what it costs — the
// terminal's own click-drag selection — is discovered at the moment of
// wanting to copy something, with a mouse already in hand and no appetite for
// a slash command. That moment arrives just as often over the full-screen
// diff or a transcript being read as it does over the draft, so the chord is
// answered above all of them. Nothing else claims it, so nothing is taken
// away by that.
func (p pointerState) update(msg tea.KeyPressMsg) (pointerState, pointerKey) {
	p.foldNotice = ""
	if keys.Match(msg, keys.Draft.Mouse) {
		return p, pointerToggleMouse
	}
	return p, pointerPass
}
