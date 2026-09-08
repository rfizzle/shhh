package components

// The tab strip: one row that says how many parts a decision has, which of
// them is in front of you, and which of them are done
// (docs/interface/surfaces.md#the-question-card).
//
// It is a component and not a card's private drawing because what an answered
// tab looks like is one fact, and a second renderer would be a second source
// of truth about it. The backlog screen is the other surface with tabs and it
// draws no strip at all — its tab is a field on the screen header's rail and
// its three tabs are filters over one list rather than parts of one answer —
// so there is nothing there to unify with and nothing there that can drift
// from this. It stays where it is (docs/interface/departures.md).
//
// Every distinction here is a glyph and a word, never a colour: the marks say
// which tabs are done and where you are standing, and the tail says the same
// thing in words for a reader whose terminal has one colour
// (docs/interface/principles.md#colour-never-carries-meaning-alone).

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// Tab is one tab on the strip.
type Tab struct {
	// Label is the tab's name, which is what a reader says out loud to
	// themselves — a number on a strip of questions, a word on the one that
	// ends it.
	Label string
	// Answered marks a tab whose work is done.
	Answered bool
	// Word is what this tab's mark means in words, drawn beside the label
	// wherever the row has room for it. Empty leaves the word to the tail.
	Word string
}

// The three marks. A tab is where you are, or done, or neither, and the mark
// says which without asking the terminal for a colour.
const (
	tabHere = "▸"
	tabDone = "✓"
	tabTodo = "·"
)

// tabGap separates tabs. Two spaces rather than the rails' ` · `, because the
// mark for a tab that is not yet answered is that middle dot and a row that
// used it for both would be a row where the separator and the state look the
// same.
const tabGap = "  "

// TabStrip is the strip as a whole: the tabs in the order they are stepped
// through, and which one has the keyboard.
type TabStrip struct {
	Tabs []Tab
	// At is the tab the keyboard is on.
	At int
	// Tail is the words the strip ends with — where the reader is standing
	// and what is still outstanding. It is dropped before any tab is, since
	// a strip with no tabs on it has lost the thing it is for.
	Tail string
	// ShortTail is the same fact in fewer words, for a terminal too narrow
	// for the whole of it.
	ShortTail string
}

// View lays the strip on one row.
//
// The word beside the tab the keyboard is on is the first thing given up,
// then the long tail for the short one, then the short tail altogether. The
// marks are last because they are the strip: a row that had given them up
// would say only how many tabs there are, which the card already says.
func (t TabStrip) View(width int) string {
	if len(t.Tabs) == 0 || width <= 0 {
		return ""
	}
	for _, rung := range []struct {
		word bool
		tail string
	}{
		{true, t.Tail}, {false, t.Tail}, {false, t.ShortTail}, {false, ""},
	} {
		row := t.row(rung.word, rung.tail)
		if lipgloss.Width(row) <= width {
			return row
		}
	}
	// The last rung is the tab the keyboard is on and nothing else, the way
	// the attachment strip's last rung is one chip: a row clipped from the end
	// would have given up exactly what a reader looks at the strip for, which
	// is where they are standing.
	if t.At >= 0 && t.At < len(t.Tabs) {
		return Clip(t.render(t.At, t.Tabs[t.At], false), width)
	}
	return Clip(t.row(false, ""), width)
}

// row is the strip at one rung: the tabs, then whatever tail was left.
func (t TabStrip) row(word bool, tail string) string {
	parts := make([]string, 0, len(t.Tabs))
	for i, tab := range t.Tabs {
		parts = append(parts, t.render(i, tab, word))
	}
	row := strings.Join(parts, tabGap)
	if tail != "" {
		row += sty.Dim.Render(tabGap + tail)
	}
	return row
}

// render lays one tab: its mark, its label, and — where the row has room for
// it — what its mark means in words.
func (t TabStrip) render(i int, tab Tab, word bool) string {
	here := i == t.At
	text := tabTodo + " " + tab.Label
	switch {
	case here:
		text = tabHere + " " + tab.Label
	case tab.Answered:
		text = tabDone + " " + tab.Label
	}
	if word && tab.Word != "" {
		text += " " + tab.Word
	}
	switch {
	case here:
		// The one place a background is used, and it says the same thing the
		// mark already said: a reader on a monochrome terminal reads ▸ and
		// loses nothing (invariant 1).
		return sty.FocusRow.Render(text)
	case tab.Answered:
		return sty.Add.Render(text)
	}
	return sty.Dim.Render(text)
}

// TabTally is the strip's tail in words: where the reader is standing among
// the tabs that hold a part of the answer, and what leaving now would cost.
//
// It is here rather than at the caller because it is the words half of the
// glyph-and-word rule, and the strip is what has to keep the two in step.
func TabTally(at, of, answered int, unansweredCost string) (long, short string) {
	where := "submit"
	if at < of {
		where = strconv.Itoa(at+1) + " of " + strconv.Itoa(of)
	}
	left := of - answered
	if left <= 0 {
		return where + " · all answered", where
	}
	count := strconv.Itoa(left) + " unanswered"
	return where + " · " + count + " " + unansweredCost, where + " · " + count
}
