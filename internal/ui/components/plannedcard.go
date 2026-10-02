package components

// The approved plan as a card (docs/interface/surfaces.md#the-progress-checkpoint):
// drawn once where the plan was approved — the plan's receipt as its header,
// its own sentence as the body, and a footer row per step with what the step
// will write — and each step the run then finishes is a flat line of its own
// further down, so progress costs a line rather than a redraw of the card.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// plannedVerb is the plan's verb in the receipt's past tense.
const plannedVerb = "planned"

// plannedCeiling is how many step rows the card draws before it counts the
// rest: the catalogue's plan of seven draws four. A plan one longer than the
// ceiling draws every step, since the line counting the last would cost the
// row it stands for.
const plannedCeiling = 4

// plannedNumberSlot is the columns a step's number takes, so the titles start
// in one column down the list.
const plannedNumberSlot = 3

// PlannedStep is one step of an approved plan, as its row draws it.
type PlannedStep struct {
	Number int
	Title  string
	State  PlanStepState
	// Writes marks a step that changes files, and File is the first file it
	// named, by its base name, with More the count of the others. Delete
	// marks a step that removes them.
	Writes bool
	File   string
	More   int
	Delete bool
	// Does is what a step that writes nothing will do, in the approval
	// card's words: `read only`, `$ runs`, `network`.
	Does string
}

// PlannedCard is an approved plan in the transcript.
type PlannedCard struct {
	Steps []PlannedStep
	// Files is how many different files the plan's steps name.
	Files int
	// Reversible is the approval card's answer to whether the plan's files
	// can be put back, in its words and tone.
	Reversible     string
	ReversibleTone FieldTone
	// Body is the plan's own sentence.
	Body string
	// Open draws every step, where the ceiling would count the ones past
	// it. Low is the low rung's closed card: the header alone on one inset
	// band row, as a step's card is there. Selected is the reading cursor
	// on the header.
	Open, Low, Selected bool
}

// Windowed reports whether the closed card counts steps rather than drawing
// them: the plan is longer than the ceiling lets the card draw, so opening
// it has rows to add.
func (c PlannedCard) Windowed() bool { return len(c.Steps) > plannedCeiling+1 }

// View draws the card at the pane's width.
func (c PlannedCard) View(width int) string {
	lines, _ := c.cardLines(width)
	return strings.Join(lines, "\n")
}

// Lines is what each line View draws is, in order.
func (c PlannedCard) Lines(width int) []CardLine {
	_, roles := c.cardLines(width)
	return roles
}

// cardLines is the card and what each of its lines is. Whether the plan's
// files can be put back stands on the right with the count of its writes,
// where a step's card puts its answer.
// See docs/interface/departures.md#a-plans-card-says-what-can-be-put-back-on-the-right.
func (c PlannedCard) cardLines(width int) ([]string, []CardLine) {
	steps := plural(len(c.Steps), "step")
	rollup := steps
	if c.Files > 0 {
		rollup += detailSep + plural(c.Files, "file")
	}
	writes := 0
	for _, s := range c.Steps {
		if s.Writes {
			writes++
		}
	}
	card := StepCard{
		Mark:           sty.Dim.Render("▸"),
		Verb:           plannedVerb,
		Rollup:         rollup,
		Bare:           steps,
		Outcome:        c.ReversibleTone.style().Render(c.Reversible),
		OutcomePainted: c.Reversible != "",
		Duration:       plural(writes, "write"),
		Body:           c.Body,
		Selected:       c.Selected,
	}
	if c.Reversible == "" {
		card.Outcome = ""
	}
	if c.Low {
		card.Density = CardLow
		return []string{card.View(width)}, []CardLine{CardLineHeader}
	}
	roles := []CardLine{CardLineText, CardLineHeader}
	for range card.bodyLines(width) {
		roles = append(roles, CardLineText)
	}
	from, to := c.window()
	if from > 0 {
		card.Rows = append(card.Rows, c.countRow(fmt.Sprintf("… %d earlier", from), width))
		roles = append(roles, CardLineText)
	}
	for _, s := range c.Steps[from:to] {
		card.Rows = append(card.Rows, c.stepRow(s, width))
		roles = append(roles, CardLineChild)
	}
	if rest := c.Steps[to:]; len(rest) > 0 {
		card.Rows = append(card.Rows, c.countRow(restLine(rest), width))
		roles = append(roles, CardLineText)
	}
	roles = append(roles, CardLineText)
	return strings.Split(card.View(width), "\n"), roles
}

// window is the run of steps the card draws rows for: all of them under the
// ceiling or on the card the reader opened, and otherwise the ceiling's
// worth around the step the run is on — one finished step above it where
// there is one, so the row the reader is watching is never the first or the
// last thing the card says.
func (c PlannedCard) window() (from, to int) {
	n := len(c.Steps)
	if c.Open || !c.Windowed() {
		return 0, n
	}
	at := n - 1
	for i, s := range c.Steps {
		if s.State == PlanStepRunning {
			at = i
			break
		}
	}
	if at == n-1 {
		for i, s := range c.Steps {
			if s.State == PlanStepQueued {
				at = i
				break
			}
		}
	}
	from = min(max(at-2, 0), n-plannedCeiling)
	return from, from + plannedCeiling
}

// restLine counts the steps past the ceiling and says whether any of them
// write, which is the one thing about them a reader approving the run would
// want before scrolling for them.
func restLine(rest []PlannedStep) string {
	w := 0
	for _, s := range rest {
		if s.Writes {
			w++
		}
	}
	said := fmt.Sprintf("… %d more", len(rest))
	switch {
	case len(rest) == 1 && w == 1:
		return said + ", it writes"
	case len(rest) == 1:
		return said + ", it writes nothing"
	case w == 0:
		return said + ", none of them write"
	case w == 1:
		return said + ", 1 of them writes"
	}
	return said + fmt.Sprintf(", %d of them write", w)
}

// countRow is a line of the list that stands for steps rather than being
// one, at the titles' column.
func (c PlannedCard) countRow(text string, width int) string {
	col := CardBodyIndent + 2 + plannedNumberSlot
	return onBand(strings.Repeat(" ", col)+sty.Dim.Render(Clip(text, max(width-cardMargin-col, 1))), width)
}

// stepRow is one step: its state's glyph, its number, its title — bright
// while it runs, body once done, dim while it waits — and what it will write
// on the right. The step in flight is a still `▸` in the spin colour, the
// mark the rail's PLAN block gives the same step: the frame's status is the
// one thing on screen that animates, and a second spinner here would count
// the turn beside it (docs/interface/surfaces.md#the-progress-checkpoint).
func (c PlannedCard) stepRow(s PlannedStep, width int) string {
	inner := max(width-cardMargin, 1)
	glyph, title := sty.Dim.Render("·"), sty.Dim
	switch s.State {
	case PlanStepDone:
		glyph, title = sty.Add.Render("✓"), sty.Body
	case PlanStepFailed:
		glyph, title = sty.Err.Render("✗"), sty.Body
	case PlanStepRunning:
		glyph, title = sty.SpinText.Render("▸"), sty.Bright
	}
	num := fmt.Sprintf("%-*d", plannedNumberSlot, s.Number)
	head := strings.Repeat(" ", CardBodyIndent) + glyph + " " + sty.Dim.Render(num)
	right := s.mark()
	room := inner - lipgloss.Width(head)
	if right != "" && lipgloss.Width(right)+1+laneFactsMin > room {
		right = ""
	}
	if right != "" {
		room -= lipgloss.Width(right) + 1
	}
	said := title.Render(Clip(s.Title, max(room, 1)))
	gap := max(inner-lipgloss.Width(head)-lipgloss.Width(said)-lipgloss.Width(right), 1)
	return onBand(Clip(head+said+strings.Repeat(" ", gap)+right, inner), width)
}

// mark is what the step will do to the tree, on its row's right: the write
// mark and the file a write names, or the approval card's word for a step
// that writes nothing.
func (s PlannedStep) mark() string {
	if !s.Writes {
		return sty.Dim.Render(s.Does)
	}
	tone := sty.Accent
	if s.Delete {
		tone = sty.Del
	}
	file := s.File
	if s.More > 0 {
		file += fmt.Sprintf(" +%d", s.More)
	}
	if file == "" {
		return tone.Render("✎")
	}
	return tone.Render("✎") + sty.Dim.Render(" "+file)
}

// PlanTick is a step of an approved plan the run finished, as the flat line
// the transcript draws under the step's card: the plan's card is drawn once,
// and progress through it is a line each.
type PlanTick struct {
	Number, Of int
	Title      string
	// WritesLeft is how many of the plan's steps that write the run had not
	// reached when it took this one.
	WritesLeft int
	Failed     bool
}

// View draws the line at the glyph column, dim, with no band: it is the
// session's note about the plan, not a step of the work.
func (t PlanTick) View(width int) string {
	glyph := sty.Add.Render("✓")
	if t.Failed {
		glyph = sty.Err.Render("✗")
	}
	left := "  " + glyph + " "
	room := max(width-lipgloss.Width(left)-cardMargin, 1)
	lead := fmt.Sprintf("plan · %d of %d · ", t.Number, t.Of)
	tail := detailSep + writesLeft(t.WritesLeft)
	// The step's title gives way before the count of writes left, which is
	// the line's news; under a title's worth of room the count goes too.
	title := t.Title
	if over := lipgloss.Width(lead+title+tail) - room; over > 0 {
		if keep := lipgloss.Width(title) - over; keep >= laneFactsMin {
			title = Clip(title, keep)
		} else {
			tail = ""
		}
	}
	return left + sty.Dim.Render(Clip(lead+title+tail, room))
}

// writesLeft says how many of the plan's writes are still to come.
func writesLeft(n int) string {
	switch n {
	case 0:
		return "no writes left"
	case 1:
		return "1 write left"
	}
	return fmt.Sprintf("%d writes left", n)
}
