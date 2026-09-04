package components

// The exit banner (docs/interface/surfaces.md#outside-the-tui). The
// chat surfaces run on the alternate screen, which means quitting does not
// leave the session in the scrollback the way a scrolling program would — it
// restores the terminal to the moment before shhh started, and everything the
// session drew is gone in one frame. The vitals go with it: which slot the
// conversation is in, how long it got, what the sitting cost, and whether any
// of it was written down.
//
// So the banner is not a sign-off. It is the handful of facts the screen was
// carrying that a reader still needs after the screen is gone, printed where
// the shell prompt is about to be. It is the bookend of the first-contact
// screen: that screen offers `pick up (last session) — 7 turns ·
// $0.42`, and this is where that offer comes from.
//
// There is no wordmark, and nothing that says nothing goes above the facts. A
// banner whose first two lines say nothing is a banner a reader learns to
// skip, and the one line here that has to be read is a command. The parting
// line is the last row for that reason: a reader who skips it loses nothing,
// because there is nothing behind it.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// ExitBanner is what the terminal keeps once the alt screen has gone.
type ExitBanner struct {
	// Session is what the conversation is called in storage — the same word
	// the saved-chat picker and the start screen's resume offer use for it,
	// so a reader who sees it here will recognise it there. It names the slot
	// the conversation was actually written to, which is not always the one
	// the session was working under.
	Session string
	// Title is the conversation's generated title, if a reading named it:
	// shown beside the slot, never instead of it, because the slot is what
	// the resume command reads.
	Title string
	// Turns is how many exchanges that conversation holds, counted the way
	// /chats counts them: the whole conversation, including whatever a
	// --continue brought back into it, because that is what reopening it
	// returns.
	Turns int
	// Spend is what this sitting cost, formatted by the host — a price where the
	// model is priced, a token count where it is not, empty where nothing was
	// spent. Never a made-up $0.00, for the reason the start screen gives about
	// the resume offer.
	Spend string
	// Resume is the command that reopens the conversation. It is the only
	// thing on this surface a reader has to be able to retype, so it is the
	// one field that is never clipped: a command with its tail eaten is not a
	// shorter command, it is a wrong one.
	Resume string
	// Unsaved marks a conversation that could not be written down at all.
	// The banner then says so instead of naming a slot, because the failure a
	// reader must not discover by typing a resume command is the one that
	// silently reopens something older.
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
	// same one the start screen's labelled notes use.
	body := width - exitLabelWidth - 2
	if body <= 0 {
		return ""
	}

	rows := []string{b.row("session", b.sessionLine(body), sty.Body)}
	if b.Spend != "" {
		rows = append(rows, b.row("spent", Clip(b.Spend, body), sty.Body))
	}
	switch {
	case b.Unsaved:
		// One thing gone wrong and no way out of it, which is the honest
		// shape here: there is no command that brings this back.
		rows = append(rows, b.row("resume", Clip("not saved · chat persistence was unavailable", body), sty.Dim))
	case b.Resume != "":
		rows = append(rows, b.row("resume", b.Resume, brightStyle()))
	}
	if line := partingLine(width); line != "" {
		rows = append(rows, "", line)
	}
	return strings.Join(rows, "\n")
}

// partingWords are the banner's last row: what the surface just took away,
// said once, so the three rows above it read as the answer to a question
// rather than as a receipt printed at nobody
// (docs/interface/surfaces.md#outside-the-tui).
const partingWords = "that is everything the screen was holding"

// partingLine draws the parting row, and only where somebody is watching.
//
// Nobody is watching a redirected stream: what reads the banner then is a
// script or a scrollback capture, and a line of voice in a capture is a line
// the next reader has to parse past to reach the facts. The question is put
// to the colour profile because that is what already answers it — the profile
// is settled against stdout, and a stream with no terminal behind it is the
// one thing it reports before it reports any colour at all.
//
// A terminal with a person and no colour still gets the line: it is words,
// and words are what a monochrome terminal keeps
// (docs/interface/principles.md#colour-never-carries-meaning-alone).
func partingLine(width int) string {
	if Profile() <= colorprofile.NoTTY {
		return ""
	}
	return sty.Dim.Render(Clip(partingWords, width))
}

// row lays one labelled line out: the label dim in its column, the value in
// the tone the row is read for.
func (b ExitBanner) row(label, value string, style lipgloss.Style) string {
	return sty.Dim.Render(padRight(label, exitLabelWidth)) + "  " + style.Render(value)
}

// sessionLine is the conversation's identity: what it is called, what it
// was about, and how big it got. The turn count drops first when the line
// will not fit, then the title, and the name is what the clip eats into last
// — a session a reader cannot name is one they cannot find again, and the
// rest is only ever colour on top of that.
//
// An unsaved conversation has no name to give: the working slot did not
// receive it, so printing that slot would point at somebody else's messages.
// The count survives, because how much was lost is the part still true.
func (b ExitBanner) sessionLine(width int) string {
	turns := plural(b.Turns, "turn")
	if b.Unsaved || b.Session == "" {
		return Clip(turns, width)
	}
	name := b.Session
	if b.Title != "" {
		name += " — " + b.Title
	}
	if line := name + " · " + turns; lipgloss.Width(line) <= width {
		return line
	}
	if lipgloss.Width(name) <= width {
		return name
	}
	return Clip(b.Session, width)
}
