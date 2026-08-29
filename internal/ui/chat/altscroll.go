package chat

// Alternate scroll (DECSET 1007), and why shhh turns it off while it owns the
// screen.
//
// Reading mode says the wheel "was never enabled, so a trackpad gesture over
// the transcript did nothing at all". That is true of terminals which leave
// alternate scroll off. It is false of most of the ones people use: Ghostty,
// iTerm2, WezTerm, Alacritty and Terminal.app all ship with 1007 set, and in
// the alternate screen that mode makes the terminal translate every wheel
// notch into a cursor key. Not a mouse event the surface can recognise and
// route — a bare CSI A or CSI B, the same bytes the arrow keys send, arriving
// hundreds at a time.
//
// So on those terminals the wheel was never inert. It was typing arrow keys
// into the draft. What it did depended on what the draft held: it scrubbed
// the input history on an empty one, walked the cursor between lines of a
// half-written message, and moved the selection on the start screen's
// suggestion list. The one thing it never did is scroll the transcript, which
// is the only thing the reader turning the wheel wanted.
//
// Enabling mouse reporting appeared to fix it, which is why this went unseen:
// tracking supersedes alternate scroll, so ctrl+x swapped the synthetic
// arrows for real SGR wheel events and the gesture started working. The
// reader who took that trade paid for the terminal's click-drag selection to
// stop a bug.
//
// The fix is to make the documented behaviour the real one. shhh asks the
// terminal to stop synthesising, and the wheel goes back to doing nothing
// until reporting is on — which is what reading mode always claimed, and what
// makes the keyboard transfers (pgup/pgdn, shift+arrows, ctrl+e) the whole
// story for a session that has not bought the wheel with its selection.
//
// Suppressing rather than using those arrows is a decision, not an oversight,
// and the obvious objection to it is a good one: a synthetic wheel costs no
// mouse tracking, so routing it would have bought wheel scrolling without
// giving up the terminal's click-drag selection. It turns on whether a real
// arrow press can be told apart from a synthesised one, which was measured
// rather than argued about — the readings, the one mode combination that
// separates them, and why that combination costs more than the gesture is
// worth are in docs/interface/surfaces.md#reading-mode. Read that before
// reaching for this again.
//
// It is asked for with XTSAVE/XTRESTORE rather than a bare set and clear, so
// a terminal that had 1007 off keeps it off afterwards and one that had it on
// gets it back.
//
// A terminal that knows neither sequence ignores both and keeps synthesising,
// so this is a request rather than a guarantee. What bounds the damage there
// is that Up no longer hands the keyboard to the transcript (model.go): the
// worst a stray synthetic arrow can now do is walk the draft's cursor or step
// the input history, never drop the reader into a mode they did not ask for.

import "io"

const (
	// saveAlternateScroll pushes the terminal's current 1007 setting onto its
	// own stack, so what we restore is what the user had rather than a guess.
	saveAlternateScroll = "\x1b[?1007s"
	// disableAlternateScroll stops the terminal turning wheel notches into
	// cursor keys while we hold the alternate screen.
	disableAlternateScroll = "\x1b[?1007l"
	// restoreAlternateScroll pops the saved setting back.
	restoreAlternateScroll = "\x1b[?1007r"
)

// SuppressAlternateScroll asks the terminal to stop translating the wheel
// into cursor keys, and returns the function that puts the setting back. It
// is safe to call unconditionally: a terminal that does not know the
// sequences ignores them.
func SuppressAlternateScroll(w io.Writer) func() {
	if w == nil {
		return func() {}
	}
	io.WriteString(w, saveAlternateScroll+disableAlternateScroll)
	return func() { io.WriteString(w, restoreAlternateScroll) }
}
