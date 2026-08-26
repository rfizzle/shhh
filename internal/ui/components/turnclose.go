package components

// The turn close (S-098, DESIGN-TUI.md §16). The question after an agent
// stops is never "what did it say", it is "what did it change" — so a turn
// ends with up to three rows answering one question each: what it did, what
// it changed, and whether the checks still pass.
//
// The rows sit on the §6a grid but start at the rail column rather than the
// pointer column: they belong to the turn, not to a step, so nothing folds
// them and no ordinal precedes them. The changed-files row carries the
// mutation rail (§14), which is why the close of a turn looks like the rows
// that produced it.
//
// This is a passive renderer. The keys it offers are handled by the host's
// focus mode on the row (§7), so the input keeps every other key.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// TurnState is how the turn ended. A cancelled or failed turn says so and
// still reports what it changed before it stopped.
type TurnState int

const (
	TurnDone      TurnState = iota // ✓ the turn ran to completion
	TurnCancelled                  // ⊘ you stopped it
	TurnFailed                     // ✗ it broke
)

// closeMinNoteGap is the space a right-aligned note needs before it is worth
// keeping; below it the note drops rather than crowding the statement.
const closeMinNoteGap = 2

// TurnChanges is the second row — what the turn wrote. It is absent from a
// turn that changed nothing.
type TurnChanges struct {
	Files          int
	Added, Removed int
	// Keys are the offers the row makes, in order.
	Keys []TurnKey
	// Note is the right-aligned reversibility note.
	Note string
}

// TurnChecks is the third row — the verdict of a quality gate or a test run
// the turn made. Absent when the turn ran neither.
type TurnChecks struct {
	// Failed says the verdict, so the glyph never carries it alone.
	Failed bool
	// Label names what ran, e.g. `go test ./internal/agent/...`.
	Label string
	// Counts is the pass/fail tally, e.g. "4/4 checks · 12.8s".
	Counts string
}

// TurnClose is the block a finished turn appends. Steps, Tools, Elapsed and
// Spend are the first row's stats; an empty or zero field is left out rather
// than reported as nothing.
type TurnClose struct {
	State TurnState
	Steps int
	Tools int
	// Elapsed is the turn's wall time, pre-formatted by FormatElapsed.
	Elapsed string
	// Spend is the turn's cost, or its token count where the pricing table
	// did not know the model — never a made-up zero.
	Spend string
	// Note is the first row's right-aligned note, e.g. "round 7/25".
	Note string

	Changes *TurnChanges
	Checks  *TurnChecks
}

// stateGlyph is the first row's glyph and the word beside it. Both carry the
// state: colour never carries it alone (invariant 1).
func (c TurnClose) stateGlyph() (string, string) {
	switch c.State {
	case TurnCancelled:
		return dimStyle.Render("⊘"), "Cancelled"
	case TurnFailed:
		return delStyle.Render("✗"), "Failed"
	}
	return addStyle.Render("✓"), "Done"
}

// summaryStats is the first row's detail: the steps, tools, wall time and
// spend the turn cost, in that order, with nothing said about a field the
// session cannot report.
func (c TurnClose) summaryStats() string {
	var parts []string
	if c.Steps > 0 {
		parts = append(parts, plural(c.Steps, "step"))
	}
	if c.Tools > 0 {
		parts = append(parts, plural(c.Tools, "tool"))
	}
	if c.Elapsed != "" {
		parts = append(parts, c.Elapsed)
	}
	if c.Spend != "" {
		parts = append(parts, c.Spend)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

// keyOffers renders the changeset row's offers: every key the interface
// offers is info, so a key in any other colour is not an offer (§10a).
func keyOffers(keys []TurnKey) string {
	var parts []string
	for _, k := range keys {
		parts = append(parts, infoStyle.Render(k.Key)+dimStyle.Render(" "+k.Label))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// closeLead is the gutter the close rows share: the rail column, then the
// glyph column. There is no pointer column — nothing folds a close row.
func closeLead(rail, glyph string) string {
	if rail == "" {
		rail = strings.Repeat(" ", railWidth)
	}
	return rail + glyph + strings.Repeat(" ", glyphWidth-1)
}

// closeLine lays out one close row: the lead and its statement on the left,
// a note right-aligned in what is left. The note is the first thing to go
// when the terminal is narrow — it annotates the row, it is never the row.
func closeLine(lead, text, note string, width int) string {
	left := lead + text
	leftW := lipgloss.Width(left)
	if noteW := lipgloss.Width(note); noteW > 0 && leftW+closeMinNoteGap+noteW <= width {
		return left + strings.Repeat(" ", width-leftW-noteW) + note
	}
	return strings.TrimRight(clip(left, width), " ")
}

// View renders the close block at the given width, one line per row.
func (c TurnClose) View(width int) string {
	glyph, word := c.stateGlyph()
	lines := []string{closeLine(
		closeLead("", glyph),
		bodyStyle.Render(word)+dimStyle.Render(c.summaryStats()),
		dimStyle.Render(c.Note), width)}

	if ch := c.Changes; ch != nil {
		stats := addStyle.Render(fmt.Sprintf("+%d", ch.Added)) + " " + delStyle.Render(fmt.Sprintf("−%d", ch.Removed))
		text := bodyStyle.Render(plural(ch.Files, "file")+" changed ") + stats
		if offers := keyOffers(ch.Keys); offers != "" {
			text += dimStyle.Render(" · ") + offers
		}
		lines = append(lines, closeLine(
			closeLead(accentStyle.Render("▎"), accentStyle.Render("✎")),
			text, dimStyle.Render(ch.Note), width))
	}

	if ck := c.Checks; ck != nil {
		glyph, verdict := addStyle.Render("✓"), " passing"
		if ck.Failed {
			glyph, verdict = delStyle.Render("✗"), " failing"
		}
		text := bodyStyle.Render(ck.Label + verdict)
		if ck.Counts != "" {
			text += dimStyle.Render(" · " + ck.Counts)
		}
		lines = append(lines, closeLine(closeLead("", glyph), text, "", width))
	}
	return strings.Join(lines, "\n")
}
