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
	// Ignored is how many of the files git's ignore rules cover. They are
	// still changes the turn made and are counted in Files; only the quit's
	// offer of a handoff leaves them out.
	Ignored int
	// Mode states a turn whose whole change is one file's permissions,
	// already worded by the host. There are no lines to count for one, so it
	// stands where the +N −M would: a row saying `1 file changed +0 −0` is
	// a row that hides the only thing that happened.
	Mode string
	// Keys are the offers the row makes, in order.
	Keys []TurnKey
	// ByYou is how many of the files the person wrote from the editor pane
	// rather than the turn's own edits; the row says so after its counts,
	// and says nothing where it is none
	// (docs/capabilities/coding-agent.md#a-turn-ends-with-what-changed).
	ByYou int
	// ByCommand, Generated and Claimed count the files the turn holds with
	// no before side: what its commands changed, the part of that the
	// project calls generated, and what a claim on the card made the
	// session's. They are counted in Files and add nothing to the line
	// counts, and the row says them in their own words and that undo cannot
	// put them back (docs/capabilities/coding-agent.md#a-rewind-can-put-the-files-back).
	ByCommand, Generated, Claimed int
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
	// Flakes is what the checkout's ledger says about a check that flaked
	// in the verdict — `flaked 3 times before · /gate flakes` — said in the
	// note column, beside what the verdict answered. Empty on a first flake
	// and on a run with none.
	Flakes string
	// Named says the failed row above already names this command, with its
	// exit and its time, so the verdict keeps the word and drops the label
	// and the counts rather than reporting one event twice with two
	// durations (docs/interface/surfaces.md#the-turns-close). It has no
	// effect on a passing verdict.
	Named bool
}

// TurnList is the session's working list as a turn's close reads it: its
// count, and how many tool rounds it has stood unchanged.
type TurnList struct {
	Done, Total, Age int
}

// staleRow is the list's row, and "" where the close has nothing to say
// about it: a turn that ends with steps open on a list nobody has kept for
// StaleListRounds is ending on a count that may not be true, and the close
// is the last thing the person reads before they believe it.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
func (l *TurnList) staleRow() (stated, age string) {
	if l == nil || l.Total <= 0 || l.Done >= l.Total {
		return "", ""
	}
	if age = listAge(l.Age); age == "" {
		return "", ""
	}
	return fmt.Sprintf("steps %d of %d done", max(l.Done, 0), l.Total), "list " + age
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
	// List is the session's working list as the turn left it, nil where it
	// keeps none. It is not the first row's Steps, which counts the titled
	// runs the turn made: this is the agent's own list. It draws a row only
	// where the turn ends with steps still open on a list that has stood
	// StaleListRounds unchanged — a fresh list, or one every step of which
	// is marked, has nothing the close needs to say.
	List *TurnList
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

// turnTotal is the turn's last line: one slot in three states, the glyph the
// only thing that changes — `∗` once it is done, `✗` where it failed, `⊘`
// where it was stopped — and the rest dim, because it is the turn's own
// account of itself and never something to act on. A turn still running has
// no total line: the frame's status and the cockpit already count it, and a
// line under the transcript would be a second account of the same facts
// (docs/interface/surfaces.md#the-turns-close).
type turnTotal struct {
	state TurnState
	// elapsed is the turn's wall time, pre-formatted by FormatElapsed.
	elapsed string
	tools   int
	// spend is what the turn has cost so far, already worded.
	spend string
	// at is when a finished turn ended; zero leaves the time out.
	at time.Time
	// noFiles says a turn that broke or was stopped changed no file, which
	// the line states rather than leaving the reader to wonder whether the
	// break took anything with it.
	noFiles bool
	// note is the right-aligned aside, the first thing a narrow pane drops.
	note string
}

// glyph is the state's mark. The word after it says the state too, so
// colour never carries it alone (invariant 1).
func (t turnTotal) glyph() string {
	switch t.state {
	case TurnCancelled:
		return StateCancelled.Mark()
	case TurnFailed:
		return StateFailed.Mark()
	}
	return StateClosed.Mark()
}

// text is the line's words. A turn that worked leads with how long it
// worked and ends on when it was done; one that broke or was stopped leads
// with that, counts what it got through, and says what it left changed.
// See docs/interface/departures.md#what-the-turns-total-says-where-the-catalogue-left-it-open.
func (t turnTotal) text() string {
	var parts []string
	tools := ""
	if t.tools > 0 {
		tools = plural(t.tools, "tool")
	}
	add := func(s ...string) {
		for _, p := range s {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	switch t.state {
	case TurnFailed, TurnCancelled:
		add(strings.ToLower(t.state.Word()), tools, t.elapsed, t.spend)
		if t.noFiles {
			add(wroteNothingTotal)
		}
	default:
		add(strings.TrimSpace("worked "+t.elapsed), tools, t.spend)
		if !t.at.IsZero() {
			add("done " + t.at.Format("3:04 PM"))
		}
	}
	return strings.Join(parts, " · ")
}

// view is the line at the given width.
//
// A pane too narrow for the line carries it on at the words' column rather
// than cutting it, because what a failed turn left changed is said last.
func (t turnTotal) view(width int) string {
	lead, text := closeLead("", t.glyph()), t.text()
	if lipgloss.Width(lead+text) <= width {
		return closeLine(lead, sty.dim.Render(text), sty.dim.Render(t.note), width)
	}
	head, rest, _ := strings.Cut(text, " · ")
	return strings.Join(closeRunOn(lead, sty.dim.Render(head), rest, width), "\n")
}

// wroteNothingTotal is the total's statement for a turn that broke or was
// stopped having changed no file.
const wroteNothingTotal = "no files changed"

// total is the close's first row.
func (c TurnClose) total() turnTotal {
	stopped := c.State == TurnFailed || c.State == TurnCancelled
	return turnTotal{
		state: c.State, elapsed: c.Elapsed, tools: c.Tools, spend: c.Spend, at: c.At,
		noFiles: stopped && c.Changes == nil, note: c.Note,
	}
}

// Summary is the whole block said in one plain line, without the state word
// the notification's title already carries and without a glyph in it: what
// the turn cost, what it changed, what its delegates wrote down, a working
// list left standing, and whether the checks still pass — the rows a turn
// closes with, in the order the screen draws them.
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
		switch {
		case ch.Mode != "":
			changed = plural(ch.Files, "file") + " changed · " + ch.Mode
		case ch.uncounted():
			changed = plural(ch.Files, "file") + " changed"
		}
		if ch.ByYou > 0 {
			changed += fmt.Sprintf(" · %d changed by you", ch.ByYou)
		}
		changed += ch.unrecordedClause()
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
	if stated, age := c.List.staleRow(); stated != "" {
		parts = append(parts, stated+" · "+age)
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
// offers take rows of their own under it, packed the way keyFooter packs a
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
	sep := sty.dim.Render(" · ")
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

// byYouClause is the changed-files row's count of the person's own files,
// and nothing where there are none.
func byYouClause(n int) string {
	if n == 0 {
		return ""
	}
	return sty.dim.Render(fmt.Sprintf(" · %d changed by you", n))
}

// uncounted reports a row whose every file is one with no before side, so
// there are no lines to count and `+0 −0` would be a measurement of nothing
// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
func (ch TurnChanges) uncounted() bool {
	return ch.Added == 0 && ch.Removed == 0 && ch.ByCommand+ch.Generated+ch.Claimed == ch.Files
}

// unrecordedClause is the changed-files row's account of the files it holds
// with no before side — ` · 2 by commands · 1 generated · not restorable` —
// and nothing where there are none.
func (ch TurnChanges) unrecordedClause() string {
	var parts []string
	switch {
	case ch.ByCommand == 1:
		parts = append(parts, "1 by a command")
	case ch.ByCommand > 1:
		parts = append(parts, fmt.Sprintf("%d by commands", ch.ByCommand))
	}
	if ch.Generated > 0 {
		parts = append(parts, fmt.Sprintf("%d generated", ch.Generated))
	}
	if ch.Claimed > 0 {
		parts = append(parts, fmt.Sprintf("%d claimed", ch.Claimed))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ") + " · not restorable"
}

// stated is the changed-files row's statement: the count, the lines where
// there are lines to count, and the clauses saying whose files they are.
func (ch TurnChanges) stated() string {
	stats := DiffStat(ch.Added, ch.Removed)
	switch {
	case ch.Mode != "":
		stats = sty.dim.Render(ch.Mode)
	case ch.uncounted():
		stats = ""
	}
	changed := plural(ch.Files, "file") + " changed"
	if stats != "" {
		changed += " "
	}
	stated := sty.body.Render(changed) + stats + byYouClause(ch.ByYou)
	if clause := ch.unrecordedClause(); clause != "" {
		stated += sty.dim.Render(clause)
	}
	return stated
}

// View renders the close block at the given width, one line per row.
func (c TurnClose) View(width int) string {
	lines := []string{c.total().view(width)}

	if ch := c.Changes; ch != nil {
		stated := ch.stated()
		lead := closeLead(sty.accent.Render("▎"), sty.accent.Render("✎"))
		notes := []string{sty.dim.Render(ch.Note)}
		if ch.Back != "" {
			fuller := ch.Back
			if ch.Note != "" {
				fuller = ch.Note + " · " + ch.Back
			}
			notes = append([]string{sty.dim.Render(fuller)}, notes...)
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
			closeLead(sty.accent.Render("▎"), sty.accent.Render("✎")),
			sty.body.Render(wroteNothing), "", width))
	}

	if cm := c.Commit; cm != nil {
		// The accent rail, because a commit changed the machine, and the
		// ✓ of a thing that landed rather than the ✎ of a thing that was
		// written: the row above already said what was written.
		lines = append(lines, closeLine(
			closeLead(sty.accent.Render("▎"), sty.add.Render("✓")),
			sty.body.Render(cm.Receipt), sty.dim.Render(CommitUndoNote), width))
	}

	if c.Notes != "" {
		// No rail and no glyph: nothing here changed the machine, and a
		// reading of what the session already holds is not one of the acts
		// the glyph column names. The empty gutter is what says so.
		lines = append(lines, closeLine(closeLead("", " "), sty.dim.Render(c.Notes), "", width))
	}

	if stated, age := c.List.staleRow(); stated != "" {
		// The notes row's empty gutter, for its reason: a reading of what
		// the session holds, not an act. A narrow pane carries the age on
		// at the words' column rather than dropping it.
		lines = append(lines, closeRunOn(closeLead("", " "), sty.dim.Render(stated), age, width)...)
	}

	if ck := c.Checks; ck != nil {
		glyph, verdict := sty.add.Render("✓"), " passing"
		if ck.Failed {
			glyph, verdict = sty.del.Render("✗"), " failing"
		}
		text := sty.body.Render(ck.Label + verdict)
		if ck.Counts != "" {
			text += sty.dim.Render(" · " + ck.Counts)
		}
		if ck.Failed && ck.Named {
			glyph, text = sty.del.Render(StateFailed.Glyph()), sty.body.Render(StateFailed.Word())
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
		if ck.Flakes != "" {
			notes = append(notes, ck.Flakes)
		}
		if ck.Again != "" {
			again := append(append([]string{}, notes...), ck.Again+" runs it again")
			if closeNoteFits(lead+text, strings.Join(again, " · "), width) {
				notes = again
			}
		}
		lines = append(lines, closeLine(lead, text, sty.dim.Render(strings.Join(notes, " · ")), width))
	}
	return strings.Join(lines, "\n")
}

// closeRunOn lays out a row whose statement is followed by a run of dim
// words: on the line where they fit, and wrapped at the words' column under
// it where they do not. A clause that is a command (`/undo 1 takes it back`)
// moves whole to the next line and is never broken inside the command: a
// reader copying the line would copy half of it.
func closeRunOn(lead, stated, tail string, width int) []string {
	left := lead + stated
	if tail == "" {
		return []string{strings.TrimRight(Clip(left, width), " ")}
	}
	const sep = " · "
	// A unit is a piece a line may break before; clause marks the ones that
	// begin a clause, so that a break between clauses is ` · ` and a break
	// between words is a space.
	type unit struct {
		text   string
		clause bool
	}
	var units []unit
	for _, c := range strings.Split(tail, sep) {
		if c = strings.TrimSpace(c); strings.HasPrefix(c, "/") {
			units = append(units, unit{c, true})
			continue
		}
		for i, w := range strings.Fields(c) {
			units = append(units, unit{w, i == 0})
		}
	}
	// take is the longest run of units that fits room, and the units left.
	take := func(units []unit, room int) (string, []unit) {
		run := ""
		for len(units) > 0 {
			u, next := units[0], run+" "+units[0].text
			switch {
			case run == "":
				next = u.text
			case u.clause:
				next = run + sep + u.text
			}
			if lipgloss.Width(next) > room {
				break
			}
			run, units = next, units[1:]
		}
		return run, units
	}
	first, units := take(units, width-lipgloss.Width(left)-lipgloss.Width(sep))
	if first == "" {
		left = strings.TrimRight(Clip(left, width), " ")
	} else {
		left += sty.dim.Render(sep + first)
	}
	lines := []string{left}
	under := closeLead("", " ")
	inner := max(width-lipgloss.Width(under), 1)
	for len(units) > 0 {
		var run string
		run, units = take(units, inner)
		if run == "" {
			// One unit wider than the row: clipped, as every row is.
			run, units = Clip(units[0].text, inner), units[1:]
		}
		lines = append(lines, under+sty.dim.Render(run))
	}
	return lines
}

// closeNoteFits reports that a note has room beside a row's statement, which
// is the test closeLine makes before it keeps one.
func closeNoteFits(left, note string, width int) bool {
	return lipgloss.Width(left)+closeMinNoteGap+lipgloss.Width(note) <= width
}
