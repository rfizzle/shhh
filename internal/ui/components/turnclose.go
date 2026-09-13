package components

// The turn close (docs/interface/surfaces.md#the-turns-close). The
// question after an agent stops is never "what did it say", it is "what did
// it change" — so a turn ends with up to three rows answering one question
// each: what it did, what it changed, and whether the checks still pass.
//
// The rows sit on the column grid but start at the rail column rather than
// the pointer column: they belong to the turn, not to a step, so nothing
// folds them and no ordinal precedes them. The changed-files row carries the
// mutation rail, which is why the close of a turn looks like the rows that
// produced it.
//
// This is a passive renderer. The keys it offers are handled by the host's
// focus mode on the row, so the input keeps every other key.

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
	// Mode states a turn whose whole change is one file's permissions,
	// already worded by the host. There are no lines to count for one, so it
	// stands where the +N −M would: a row saying `1 file changed +0 −0` is
	// a row that hides the only thing that happened.
	Mode string
	// Keys are the offers the row makes, in order.
	Keys []TurnKey
	// Note is the right-aligned reversibility note.
	Note string
}

// CommitUndoNote is what the commit row says about undo, and it is the
// component's own words rather than the host's because it is a fact about
// shhh and not about this turn: an undo puts files back out of the session's
// own records and never touches history. It is stated on the row rather than
// left to be discovered, because the honest way back from a commit is `git
// revert`, which is a sentence somebody types and not a key we offer.
//
// It is short because it has to survive the note column at eighty columns
// inside a transcript, where the row has around seventy-five to itself and
// the receipt has already spent forty of them. The longer sentence — undo
// restores files and leaves history alone — is the same fact and does not
// fit; this half of it is the half a reader needs at the moment they are
// looking for a way back.
const CommitUndoNote = "/undo does not reach history"

// TurnCommit is the row a turn that committed adds — the receipt, worded by
// the host, in the one place a reader looks for what the turn did. A turn
// that made no commit has none, and the changed-files row above it then keeps
// its undo offer.
type TurnCommit struct {
	// Receipt is the commit said in one line: what landed, its sha, and the
	// branch it landed on.
	Receipt string
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
	// Superseded is how many earlier failures a later verification answered.
	// The row states the count rather than swallowing it: the turn really did
	// watch something fail, and a close that reported only the green would be
	// hiding the work it took to get there. The attempts themselves are still
	// in the transcript with the outcome and the time they had — what they no
	// longer do is decide what this row says
	// (docs/interface/surfaces.md#the-turns-close).
	Superseded int
	// Keys are the offers the row makes, in order. There is one — run the
	// suite again — and it is present only where there is a suite to run: a
	// verdict a command left is a verdict about a line nobody is looking at
	// any more, and re-running that is not a key's to offer.
	Keys []TurnKey
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
	Commit  *TurnCommit
	Checks  *TurnChecks
	// Notes is what the turn's delegates left in the session's shared
	// notebook, e.g. "2 notes from reviewer". Empty where no delegate wrote
	// one, which is every turn that did not fan out.
	Notes string
	// KeysWaiting says the changeset row does not hold the keyboard, so its
	// keys render grey rather than in the colour that means "you can press
	// this": while the draft has it, `v` is a letter and belongs in the
	// sentence (invariant 5). A host that claims nothing keeps the live
	// treatment the row always had.
	KeysWaiting bool
	// Handover is the key that hands the keyboard over, unbracketed, offered
	// live beside the waiting keys. Empty where there is no such key to
	// press from this screen.
	Handover string
	// Option says this block is the first in the session to offer a chord,
	// so its first key run names the profile setting an alt chord needs on a
	// stock macOS terminal (inertkeys.go).
	Option bool
}

// Word is how the turn ended, in the one word that rides beside the glyph so
// colour never carries the state alone (invariant 1). It is exported because
// the screen is not the only surface that has to say how a turn ended: the
// desktop notification a finished turn raises has no glyph and no colour, and
// says this.
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
// the turn cost, what it changed, what its delegates wrote down, and whether
// the checks still pass — the rows a turn closes with, in the order the
// screen draws them.
//
// It exists because a notification is the one surface that cannot draw
// . Everything it says has to be words, so the glyph that
// carries "changed" and the colours that carry "+3 −5" are spent here as
// the words they stand for, and nothing is said twice.
func (c TurnClose) Summary() string {
	var parts []string
	if stats := strings.TrimPrefix(c.summaryStats(), " · "); stats != "" {
		parts = append(parts, stats)
	}
	if ch := c.Changes; ch != nil {
		changed := fmt.Sprintf("%s changed · +%d −%d", plural(ch.Files, "file"), ch.Added, ch.Removed)
		if ch.Mode != "" {
			changed = plural(ch.Files, "file") + " changed · " + ch.Mode
		}
		parts = append(parts, changed)
	}
	if cm := c.Commit; cm != nil {
		parts = append(parts, cm.Receipt)
	}
	if c.Notes != "" {
		parts = append(parts, c.Notes)
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

// closeLead is the gutter the close rows share: the pointer column held
// blank, then the rail column, then the glyph column.
//
// Nothing folds a close row, and the pointer column is held anyway. The
// changed-files row carries the mutation rail so that the close of a turn
// looks like the rows that produced it (docs/interface/surfaces.md#the-turns-close),
// and a rail one column left of every other rail in the transcript does not
// look like them — it reads as a fourth mark in the column the fold carets
// and the reading cursor own. Holding it is also what lets reading mode put
// its cursor on the block without pushing the whole thing sideways
// (docs/interface/surfaces.md#the-leading-columns).
func closeLead(rail, glyph string) string {
	if rail == "" {
		rail = strings.Repeat(" ", railWidth)
	}
	return strings.Repeat(" ", ptrWidth) + rail + glyph + strings.Repeat(" ", glyphWidth-1)
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
	return strings.TrimRight(Clip(left, width), " ")
}

// View renders the close block at the given width, one line per row.
func (c TurnClose) View(width int) string {
	glyph, word := c.stateGlyph()
	lines := []string{closeLine(
		closeLead("", glyph),
		sty.Body.Render(word)+sty.Dim.Render(c.summaryStats()),
		sty.Dim.Render(c.Note), width)}

	if ch := c.Changes; ch != nil {
		stats := DiffStat(ch.Added, ch.Removed)
		if ch.Mode != "" {
			stats = sty.Dim.Render(ch.Mode)
		}
		stated := sty.Body.Render(plural(ch.Files, "file")+" changed ") + stats
		lead := closeLead(sty.Accent.Render("▎"), sty.Accent.Render("✎"))
		text := stated
		if run := keyRun(ch.Keys, c.KeysWaiting, c.Handover); run != "" {
			text = stated + sty.Dim.Render(" · ") + run
		}
		// The keys that are not live yet are the first thing to give up the
		// width, and the key that makes them live is the last: one is an
		// offer, the others are not offers yet.
		if lipgloss.Width(lead+text) > width {
			if run := keyRunNarrow(ch.Keys, c.KeysWaiting, c.Handover); run != "" {
				text = stated + sty.Dim.Render(" · ") + run
			}
		}
		lines = append(lines, closeLine(lead, text, sty.Dim.Render(ch.Note), width))
		// And, the first time a session offers a chord, what alt costs on a
		// stock macOS terminal. It takes a line under the row rather than a
		// clause on it: the row is already the widest line in the block, and
		// this is the sentence a reader whose chord did nothing needs most
		// (inertkeys.go).
		if option := KeyRunOption(ch.Keys, c.KeysWaiting, c.Option); option != "" {
			lines = append(lines, closeLine(closeLead("", " "), option, "", width))
		}
	}

	if cm := c.Commit; cm != nil {
		// The accent rail, because a commit changed the machine, and the
		// ✓ of a thing that landed rather than the ✎ of a thing that was
		// written: the row above already said what was written.
		lines = append(lines, closeLine(
			closeLead(sty.Accent.Render("▎"), sty.Add.Render("✓")),
			sty.Body.Render(cm.Receipt), sty.Dim.Render(CommitUndoNote), width))
	}

	if c.Notes != "" {
		// No rail and no glyph: nothing here changed the machine, and a
		// reading of what the session already holds is not one of the acts
		// the glyph column names. The empty gutter is what says so.
		lines = append(lines, closeLine(closeLead("", " "), sty.Dim.Render(c.Notes), "", width))
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
		// What the verdict answered rides in the note column, where it is the
		// first thing a narrow terminal drops: it annotates the verdict and
		// is never the verdict, and the offer beside it is a key somebody can
		// press.
		note := ""
		if ck.Superseded > 0 {
			note = sty.Dim.Render(plural(ck.Superseded, "earlier failure") + " superseded")
		}
		// The offer is answered by reading mode on the row, exactly as the
		// changed-files row's are, so it renders under the same rule about
		// which keys are live (invariant 5). The handover is not repeated
		// here: it is one key for the whole block and the row above already
		// names it, and a chord printed twice in four lines reads as two.
		if run := keyRun(ck.Keys, c.KeysWaiting, ""); run != "" {
			text += sty.Dim.Render(" · ") + run
		}
		lines = append(lines, closeLine(closeLead("", glyph), text, note, width))
		// The Option sentence belongs to whichever row in the block offers a
		// chord first, and the changed-files row above has already said it
		// where there is one: a turn that changed nothing and ran its checks
		// leaves this row holding the block's only chord.
		if c.Changes == nil {
			if option := KeyRunOption(ck.Keys, c.KeysWaiting, c.Option); option != "" {
				lines = append(lines, closeLine(closeLead("", " "), option, "", width))
			}
		}
	}
	return strings.Join(lines, "\n")
}
