package components

// The exit banner (S-148, DESIGN-TUI.md §22b). The chat surfaces run on the
// alternate screen, which means quitting does not leave the session in the
// scrollback the way a scrolling program would — it restores the terminal to
// the moment before shhh started, and everything the session drew is gone in
// one frame. The vitals go with it: which slot the conversation is in, how
// long it got, what the sitting cost, and whether any of it was written down.
//
// So the banner is not a sign-off. It is the handful of facts the screen was
// carrying that a reader still needs after the screen is gone, printed where
// the shell prompt is about to be. It is the bookend of the first-contact
// screen (§17c): that screen offers `pick up (last session) — 7 turns ·
// $0.42`, and this is where that offer comes from.
//
// There is no wordmark and no parting line. A banner whose first two lines
// say nothing is a banner a reader learns to skip, and the one line here that
// has to be read is a command.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ExitBanner is what the terminal keeps once the alt screen has gone.
type ExitBanner struct {
	// Session is what the conversation is called in storage — the same word
	// the saved-chat picker and the start screen's resume offer use for it,
	// so a reader who sees it here will recognise it there. It names the slot
	// the conversation was actually written to, which is not always the one
	// the session was working under.
	Session string
	// Turns is how many exchanges that conversation holds, counted the way
	// /chats counts them: the whole conversation, including whatever a
	// --continue brought back into it, because that is what reopening it
	// returns.
	Turns int
	// Spend is what this sitting cost, formatted by the host — a price where
	// the model is priced, a token count where it is not, empty where nothing
	// was spent. Never a made-up $0.00, for the reason §17c gives about the
	// resume offer.
	Spend string
	// Resume is the command that reopens the conversation. It is the only
	// thing on this surface a reader has to be able to retype, so it is the
	// one field that is never clipped: a command with its tail eaten is not a
	// shorter command, it is a wrong one.
	Resume string
	// Unsaved marks a conversation that could not be written down at all.
	// The banner then says so instead of naming a slot, because the failure a
	// reader must not discover by typing a resume command is the one that
	// silently reopens something older (§17a).
	Unsaved bool
}

// exitLabelWidth is the banner's label column. `session` is the longest of
// the three labels and is always present, so the column never changes width
// and the values line up whether or not the sitting cost anything.
const exitLabelWidth = 7

// View renders the banner at the given width, or nothing at all when there is
// no conversation behind it — a session opened and quit without a word has
// nothing to resume and nothing to report, and the shell prompt says more
// about that than a line acknowledging it would.
func (b ExitBanner) View(width int) string {
	if width <= 0 || b.Turns <= 0 {
		return ""
	}
	// The value column starts after the label and its two-space gutter, the
	// same one the start screen's labelled notes use (§17c).
	body := width - exitLabelWidth - 2
	if body <= 0 {
		return ""
	}

	rows := []string{b.row("session", b.sessionLine(body), bodyStyle)}
	if b.Spend != "" {
		rows = append(rows, b.row("spent", clip(b.Spend, body), bodyStyle))
	}
	switch {
	case b.Unsaved:
		// One thing gone wrong and no way out of it, which is the honest
		// shape here: there is no command that brings this back.
		rows = append(rows, b.row("resume", clip("not saved · chat persistence was unavailable", body), dimStyle))
	case b.Resume != "":
		rows = append(rows, b.row("resume", b.Resume, brightStyle()))
	}
	return strings.Join(rows, "\n")
}

// row lays one labelled line out: the label dim in its column, the value in
// the tone the row is read for.
func (b ExitBanner) row(label, value string, style lipgloss.Style) string {
	return dimStyle.Render(padRight(label, exitLabelWidth)) + "  " + style.Render(value)
}

// sessionLine is the conversation's identity: what it is called, and how big
// it got. The turn count drops first when the line will not fit, and the name
// is what the clip eats into last — a session a reader cannot name is one
// they cannot find again, and the count is only ever colour on top of that.
//
// An unsaved conversation has no name to give: the working slot did not
// receive it, so printing that slot would point at somebody else's messages.
// The count survives, because how much was lost is the part still true.
func (b ExitBanner) sessionLine(width int) string {
	turns := plural(b.Turns, "turn")
	if b.Unsaved || b.Session == "" {
		return clip(turns, width)
	}
	if line := b.Session + " · " + turns; lipgloss.Width(line) <= width {
		return line
	}
	return clip(b.Session, width)
}
