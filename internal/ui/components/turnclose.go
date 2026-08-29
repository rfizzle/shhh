package components

// The turn close (S-098, docs/interface/surfaces.md#the-turns-close). The
// question after an agent stops is never "what did it say", it is "what did
// it change" — so a turn ends with up to three rows answering one question
// each: what it did, what it changed, and whether the checks still pass.
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

	"charm.land/lipgloss/v2"
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
	// KeysWaiting says the changeset row does not hold the keyboard, so its
	// keys render grey rather than in the colour that means "you can press
	// this" (§7c): while the draft has it, `v` is a letter and belongs in the
	// sentence (invariant 5). A host that claims nothing keeps the live
	// treatment the row always had.
	KeysWaiting bool
	// Handover is the key that hands the keyboard over, unbracketed, offered
	// live beside the waiting keys. Empty where there is no such key to
	// press from this screen.
	Handover string
}

// Word is how the turn ended, in the one word that rides beside the glyph so
// colour never carries the state alone (invariant 1). It is exported because
// the screen is not the only surface that has to say how a turn ended: the
// desktop notification a finished turn raises has no glyph and no colour, and
// says this (S-157, §10l).
func (s TurnState) Word() string {
	switch s {
	case TurnCancelled:
		return "Cancelled"
	case TurnFailed:
		return "Failed"
	}
	return "Done"
}

// stateGlyph is the first row's glyph and the word beside it. Both carry the
// state: colour never carries it alone (invariant 1).
func (c TurnClose) stateGlyph() (string, string) {
	switch c.State {
	case TurnCancelled:
		return sty.Dim.Render("⊘"), c.State.Word()
	case TurnFailed:
		return sty.Del.Render("✗"), c.State.Word()
	}
	return sty.Add.Render("✓"), c.State.Word()
}

// Summary is the whole block said in one plain line, without the state word
// the notification's title already carries and without a glyph in it: what
// the turn cost, what it changed, and whether the checks still pass — the
// three rows of §16, in the order the screen draws them.
//
// It exists because a notification is the one surface that cannot draw
// (S-157, §10l). Everything it says has to be words, so the glyph that
// carries "changed" and the colours that carry "+3 −5" are spent here as
// the words they stand for, and nothing is said twice.
func (c TurnClose) Summary() string {
	var parts []string
	if stats := strings.TrimPrefix(c.summaryStats(), " · "); stats != "" {
		parts = append(parts, stats)
	}
	if ch := c.Changes; ch != nil {
		parts = append(parts, fmt.Sprintf("%s changed · +%d −%d", plural(ch.Files, "file"), ch.Added, ch.Removed))
	}
	if ck := c.Checks; ck != nil {
		verdict := " passing"
		if ck.Failed {
			verdict = " failing"
		}
		parts = append(parts, ck.Label+verdict)
	}
	return strings.Join(parts, " · ")
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
		sty.Body.Render(word)+sty.Dim.Render(c.summaryStats()),
		sty.Dim.Render(c.Note), width)}

	if ch := c.Changes; ch != nil {
		stats := sty.Add.Render(fmt.Sprintf("+%d", ch.Added)) + " " + sty.Del.Render(fmt.Sprintf("−%d", ch.Removed))
		stated := sty.Body.Render(plural(ch.Files, "file")+" changed ") + stats
		lead := closeLead(sty.Accent.Render("▎"), sty.Accent.Render("✎"))
		text := stated
		if run := keyRun(ch.Keys, c.KeysWaiting, c.Handover); run != "" {
			text = stated + sty.Dim.Render(" · ") + run
		}
		// The keys that are not live yet are the first thing to give up the
		// width, and the key that makes them live is the last: one is an
		// offer, the others are not offers yet (§7c).
		if lipgloss.Width(lead+text) > width {
			if run := keyRunNarrow(ch.Keys, c.KeysWaiting, c.Handover); run != "" {
				text = stated + sty.Dim.Render(" · ") + run
			}
		}
		lines = append(lines, closeLine(lead, text, sty.Dim.Render(ch.Note), width))
	}

	if ck := c.Checks; ck != nil {
		glyph, verdict := sty.Add.Render("✓"), " passing"
		if ck.Failed {
			glyph, verdict = sty.Del.Render("✗"), " failing"
		}
		text := sty.Body.Render(ck.Label + verdict)
		if ck.Counts != "" {
			text += sty.Dim.Render(" · " + ck.Counts)
		}
		lines = append(lines, closeLine(closeLead("", glyph), text, "", width))
	}
	return strings.Join(lines, "\n")
}
