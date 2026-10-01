package components

// The turn close (docs/interface/surfaces.md#the-turns-close). The
// question after an agent stops is never "what did it say", it is "what did
// it change" — so a turn ends with up to three rows answering one question
// each: what it did, what it changed, and whether the checks still pass.
//
// The first row is the turn's total — the one line a turn ends on, flat and
// dim at the glyph column a step's card puts its glyph in, its state said by
// the glyph and the word that leads it. The rows under it sit on the same
// columns: they belong to the turn, not to a step, so nothing folds them and
// no ordinal precedes them. The changed-files row carries the mutation rail,
// which is why the close of a turn looks like the rows that produced it.
//
// This is a passive renderer. The keys it offers are handled by the host's
// focus mode on the row, so the input keeps every other key.

import (
	"fmt"
	"strings"
	"time"

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

// wroteNothing is the changed-files row's statement for a turn that ran
// something that could have written and changed no file.
const wroteNothing = "changed no files"

// closeMinNoteGap is the space a right-aligned note needs before it is worth
// keeping; below it the note drops rather than crowding the statement.
const closeMinNoteGap = 2

// TurnChanges is the second row — what the turn wrote. It is absent from a
// turn that changed nothing; TurnClose.WroteNothing is what stands in its
// place where the turn ran something that could have written.
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
	// Back is the command that takes the turn back, said after Note where
	// there is room for both and given up before Note where there is not:
	// the note is a reading of the files, and this is a command /help also
	// names (docs/interface/surfaces.md#the-turns-close).
	Back string
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
// that made no commit has none.
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
	// Again is the command that runs the suite again, said in the note
	// column where there is room for it. It is present only where there is
	// a suite to run: a verdict a command left is a verdict about a line
	// nobody is looking at any more, and re-running that is not the row's
	// to suggest.
	Again string
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
	// At is when the turn ended, read off the session's held clock; zero
	// leaves the time out.
	At time.Time
	// Note is the first row's right-aligned aside: a dim reading beside the
	// stats that the row gives up first when the terminal is narrow, so
	// nothing the turn's outcome depends on may live here.
	Note string

	Changes *TurnChanges
	// WroteNothing says the turn ran a command or a call shhh cannot see the
	// far side of and its changeset is empty, so the changed-files row is
	// drawn answering the question that act raised: `changed no files`, with no
	// offers, since there is nothing to review, keep or take back. A command
	// is assumed to write (docs/interface/principles.md#weight-tracks-risk),
	// and this row is that assumption answered. A turn that only read has no
	// such question and no such row. Ignored where Changes is set.
	WroteNothing bool
	Commit       *TurnCommit
	Checks       *TurnChecks
	// Notes is what the turn's delegates left in the session's shared
	// notebook, and how much of it is still waiting on the screen that holds
	// it — e.g. "2 notes from reviewer · 3 unread". Empty where no delegate
	// wrote one, which is every turn that did not fan out. The host words
	// the whole clause, because what counts as unread is a reading of the
	// session (docs/interface/surfaces.md#the-supporting-screens).
	Notes string
	// KeysWaiting says the changeset row does not hold the keyboard, so its
	// keys render grey rather than in the colour that means "you can press
	// this": while the draft has it, `u` is a letter and belongs in the
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

// TurnTotal is the turn's last line: one slot in three states, the glyph the
// only thing that changes — `∗` once it is done, `✗` where it failed, `⊘`
// where it was stopped — and the rest dim, because it is the turn's own
// account of itself and never something to act on. A turn still running has
// no total line: the frame's status and the cockpit already count it, and a
// line under the transcript would be a second account of the same facts
// (docs/interface/surfaces.md#the-turns-close).
type TurnTotal struct {
	State TurnState
	// Elapsed is the turn's wall time, pre-formatted by FormatElapsed.
	Elapsed string
	Tools   int
	// Spend is what the turn has cost so far, already worded.
	Spend string
	// At is when a finished turn ended; zero leaves the time out.
	At time.Time
	// NoFiles says a turn that broke or was stopped changed no file, which
	// the line states rather than leaving the reader to wonder whether the
	// break took anything with it.
	NoFiles bool
	// Note is the right-aligned aside, the first thing a narrow pane drops.
	Note string
}

// glyph is the state's mark. The word after it says the state too, so
// colour never carries it alone (invariant 1).
func (t TurnTotal) glyph() string {
	switch t.State {
	case TurnCancelled:
		return sty.Dim.Render("⊘")
	case TurnFailed:
		return sty.Del.Render("✗")
	}
	return sty.Dim.Render("∗")
}

// text is the line's words. A turn that worked leads with how long it
// worked and ends on when it was done; one that broke or was stopped leads
// with that, counts what it got through, and says what it left changed.
// See docs/interface/departures.md#what-the-turns-total-says-where-the-catalogue-left-it-open.
func (t TurnTotal) text() string {
	var parts []string
	tools := ""
	if t.Tools > 0 {
		tools = plural(t.Tools, "tool")
	}
	add := func(s ...string) {
		for _, p := range s {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	switch t.State {
	case TurnFailed, TurnCancelled:
		add(strings.ToLower(t.State.Word()), tools, t.Elapsed, t.Spend)
		if t.NoFiles {
			add(wroteNothingTotal)
		}
	default:
		add(strings.TrimSpace("worked "+t.Elapsed), tools, t.Spend)
		if !t.At.IsZero() {
			add("done " + t.At.Format("3:04 PM"))
		}
	}
	return strings.Join(parts, " · ")
}

// View is the line at the given width.
//
// A pane too narrow for the line carries it on at the words' column rather
// than cutting it, because what a failed turn left changed is said last.
func (t TurnTotal) View(width int) string {
	lead, text := closeLead("", t.glyph()), t.text()
	if lipgloss.Width(lead+text) <= width {
		return closeLine(lead, sty.Dim.Render(text), sty.Dim.Render(t.Note), width)
	}
	head, rest, _ := strings.Cut(text, " · ")
	return strings.Join(closeRunOn(lead, sty.Dim.Render(head), rest, width), "\n")
}

// wroteNothingTotal is the total's statement for a turn that broke or was
// stopped having changed no file.
const wroteNothingTotal = "no files changed"

// total is the close's first row.
func (c TurnClose) total() TurnTotal {
	stopped := c.State == TurnFailed || c.State == TurnCancelled
	return TurnTotal{
		State: c.State, Elapsed: c.Elapsed, Tools: c.Tools, Spend: c.Spend, At: c.At,
		NoFiles: stopped && c.Changes == nil, Note: c.Note,
	}
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
	} else if c.WroteNothing {
		parts = append(parts, wroteNothing)
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

// summaryStats is the notification's stats: the steps, tools, wall time and
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

// closeLead is the gutter the close rows share: the card's own columns — a
// pointer column held blank, the rail column, the glyph and one blank — so
// the words start at the column a card's verb and body start at.
//
// Nothing folds a close row, and the pointer column is held anyway. The
// changed-files row carries the mutation rail so that the close of a turn
// looks like the cards that produced it (docs/interface/surfaces.md#the-turns-close),
// and a rail in any other column than theirs does not look like them. The
// two columns before the glyph are also where reading mode puts its cursor,
// so it stands on the block without pushing the whole thing sideways
// (docs/interface/surfaces.md#the-leading-columns).
func closeLead(rail, glyph string) string {
	if rail == "" {
		rail = strings.Repeat(" ", railWidth)
	}
	return " " + rail + glyph + " "
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

// closeOfferRows lays out a close row that carries offers. Where the row and
// its run fit on one line they share it. Where they do not, the keys that are
// not live yet give up the width first and the key that makes them live is
// the last to go (keyRunNarrow): one is an offer, the others are not offers
// yet. Where even that does not fit, the statement keeps its row and the
// offers take rows of their own under it, packed the way KeyFooter packs a
// screen's keys beside its lead — an offer the row acts on goes to the next
// row whole, never clipped off the edge of this one
// (docs/interface/principles.md#fold-never-hide).
//
// notes are the note column's candidates, fullest first: the row takes the
// first that fits beside what it states.
func closeOfferRows(lead, stated string, keys []TurnKey, waiting bool, handover string, notes []string, width int) []string {
	line := func(text string) string {
		note := ""
		for _, n := range notes {
			if closeNoteFits(lead+text, n, width) {
				note = n
				break
			}
		}
		return closeLine(lead, text, note, width)
	}
	run := keyRun(keys, waiting, handover)
	if run == "" {
		return []string{line(stated)}
	}
	sep := sty.Dim.Render(" · ")
	if text := stated + sep + run; lipgloss.Width(lead+text) <= width {
		return []string{line(text)}
	}
	if text := stated + sep + keyRunNarrow(keys, waiting, handover); lipgloss.Width(lead+text) <= width {
		return []string{line(text)}
	}
	under := closeLead("", " ")
	rows := []string{line(stated)}
	for _, r := range keyRunRows(keys, waiting, handover, width-lipgloss.Width(under)) {
		rows = append(rows, closeLine(under, r, "", width))
	}
	return rows
}

// View renders the close block at the given width, one line per row.
func (c TurnClose) View(width int) string {
	lines := []string{c.total().View(width)}

	if ch := c.Changes; ch != nil {
		stats := DiffStat(ch.Added, ch.Removed)
		if ch.Mode != "" {
			stats = sty.Dim.Render(ch.Mode)
		}
		stated := sty.Body.Render(plural(ch.Files, "file")+" changed ") + stats
		lead := closeLead(sty.Accent.Render("▎"), sty.Accent.Render("✎"))
		notes := []string{sty.Dim.Render(ch.Note)}
		if ch.Back != "" {
			fuller := ch.Back
			if ch.Note != "" {
				fuller = ch.Note + " · " + ch.Back
			}
			notes = append([]string{sty.Dim.Render(fuller)}, notes...)
		}
		if len(ch.Keys) == 0 {
			// Offering nothing, the row says the reading of the files and the
			// way back after what it states, as one run of words, and a pane
			// too narrow for the run carries it on at the words' column
			// rather than dropping the way back.
			tail := ch.Note
			if ch.Back != "" {
				tail = strings.TrimPrefix(ch.Note+" · "+ch.Back, " · ")
			}
			lines = append(lines, closeRunOn(lead, stated, tail, width)...)
		} else {
			lines = append(lines, closeOfferRows(lead, stated, ch.Keys, c.KeysWaiting, c.Handover, notes, width)...)
		}
	} else if c.WroteNothing && c.State != TurnFailed && c.State != TurnCancelled {
		// A turn that broke or was stopped says it changed no files on its
		// total, so the row would say it twice.
		// The changed-files row's own lead, because it is that row answering
		// with nothing: the rail the command's row carried is the question,
		// and this is where it is answered. No offers — there is nothing to
		// review, keep or take back.
		lines = append(lines, closeLine(
			closeLead(sty.Accent.Render("▎"), sty.Accent.Render("✎")),
			sty.Body.Render(wroteNothing), "", width))
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
		// What the verdict answered, and what to type to run it again, ride
		// in the note column, where they are the first thing a narrow
		// terminal drops: they annotate the verdict and are never the
		// verdict. Where there is room for one of the two, it is what the
		// verdict answered: that is a reading of the row, and the other is a
		// command /help also names.
		lead := closeLead("", glyph)
		var notes []string
		if ck.Superseded > 0 {
			notes = append(notes, plural(ck.Superseded, "earlier failure")+" since passed")
		}
		if ck.Again != "" {
			again := append(append([]string{}, notes...), ck.Again+" runs it again")
			if closeNoteFits(lead+text, strings.Join(again, " · "), width) {
				notes = again
			}
		}
		lines = append(lines, closeLine(lead, text, sty.Dim.Render(strings.Join(notes, " · ")), width))
	}
	return strings.Join(lines, "\n")
}

// closeRunOn lays out a row whose statement is followed by a run of dim
// words: on the line where they fit, and wrapped at the words' column under
// it where they do not.
func closeRunOn(lead, stated, tail string, width int) []string {
	left := lead + stated
	if tail == "" {
		return []string{strings.TrimRight(Clip(left, width), " ")}
	}
	sep := " · "
	room := width - lipgloss.Width(left) - lipgloss.Width(sep)
	words := strings.Fields(tail)
	first := ""
	for len(words) > 0 {
		next := strings.TrimSpace(first + " " + words[0])
		if lipgloss.Width(next) > room {
			break
		}
		first, words = next, words[1:]
	}
	if first == "" {
		left = strings.TrimRight(Clip(left, width), " ")
	} else {
		left += sty.Dim.Render(sep + first)
	}
	lines := []string{left}
	if len(words) == 0 {
		return lines
	}
	under := closeLead("", " ")
	inner := max(width-lipgloss.Width(under), 1)
	for _, l := range strings.Split(lipgloss.Wrap(strings.Join(words, " "), inner, ""), "\n") {
		lines = append(lines, under+sty.Dim.Render(Clip(strings.TrimRight(l, " "), inner)))
	}
	return lines
}

// closeNoteFits reports that a note has room beside a row's statement, which
// is the test closeLine makes before it keeps one.
func closeNoteFits(left, note string, width int) bool {
	return lipgloss.Width(left)+closeMinNoteGap+lipgloss.Width(note) <= width
}
