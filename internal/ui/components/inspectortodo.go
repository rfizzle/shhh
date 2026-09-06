package components

// The rail's TODO block: what the project still owes, four rows of it. It is
// a file of its own for the same reason PLAN is — a row's state, its glyph
// and the note beside it are decided together and read nowhere else.

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// TodoRowState is what a backlog row's glyph says about it.
type TodoRowState int

const (
	// TodoReady can be started now.
	TodoReady TodoRowState = iota
	// TodoWaiting is open but has a dependency still outstanding.
	TodoWaiting
	// TodoBlocked needs a person before it can move.
	TodoBlocked
	// TodoRunning is being worked on in this session.
	TodoRunning
)

// InspectorTodoRow is one backlog item in the TODO block.
type InspectorTodoRow struct {
	Slug string
	// Priority and Grade are one letter each — the priority's, and the
	// item's grade on whatever scale the project grades work by. They are
	// the two facts that decide the order and the ceremony, and nothing
	// else fits.
	Priority, Grade string
	State           TodoRowState
	// Note is the right-hand column: what the row waits on, or the stage a
	// running one is at. Blank for a ready row, which has nothing to add.
	Note string
	// LanesDone and LanesTotal are a running item's fan-out: how many of the
	// writers' patches have landed, out of how many lanes it was divided
	// into. A total of zero draws no meter, because an item built in one turn
	// has no lanes to measure and a bar against a denominator nobody supplied
	// is a number the interface invented.
	LanesDone, LanesTotal int
	// Stale marks a row whose note is about the item having fallen behind
	// the tree rather than about where it stands. It is drawn in warning
	// tone: every other note on this block is a statement of where the work
	// is, and this one is a statement that the work may be described wrong.
	Stale bool
}

// InspectorTodo is the TODO block: what the project still owes, so "what
// comes after this" is on screen next to "where are we". PLAN says through
// what this turn is going; TODO says what is queued behind it. It is the
// one block scoped wider than the session — the backlog is the project's —
// and its heading says so in words, like every other block says its scope.
// See docs/interface/surfaces.md#the-inspector-rail.
type InspectorTodo struct {
	// Open and Blocked are the counts the heading states over the whole
	// backlog, whatever Rows shows of it.
	Open, Blocked int
	// Sprint is the name of the set being worked, and SprintDone of
	// SprintTotal how much of it is finished. Empty draws no sprint row:
	// a backlog worked without one has no set to state.
	Sprint                  string
	SprintDone, SprintTotal int
	// SprintItem is the slug a sprint is working now and SprintStage the
	// stage that item's run is in. Both empty draws no row: a backlog read
	// while nothing is being worked has no current item to name, and a
	// sprint's whole claim is that it is moving through the set on its own.
	SprintItem, SprintStage string
	Rows                    []InspectorTodoRow
	// More is how many active items Rows left out.
	More int
	// Hint is the row under the list naming how to see the whole backlog.
	Hint string
}

// todoBlock is the TODO block. It sits under PLAN because it is the same
// question one step further out — PLAN is this turn's list, TODO is the
// project's — and above CHANGES because it is about work, not about files.
// A backlog with nothing active is no block at all.
func (r InspectorRail) todoBlock(width int) (railBlock, bool) {
	t := r.Todo
	if t == nil || (t.Open == 0 && t.Blocked == 0 && len(t.Rows) == 0) {
		return railBlock{}, false
	}
	meta := fmt.Sprintf("project · %d open", t.Open)
	if t.Blocked > 0 {
		meta += fmt.Sprintf(" · %d blocked", t.Blocked)
	}
	b := railBlock{heading: railHeading("TODO", meta, sty.Dim, width)}
	// The sprint sits above the items because it is what scopes them: the
	// rows under it are the set, and n of m is how far through it the
	// project is. The word "sprint" is on the row because the name alone
	// would read as one more item.
	if t.Sprint != "" {
		b.add(railRow(sty.Dim.Render("sprint")+" "+sty.Body.Render(t.Sprint),
			sty.Dim.Render(fmt.Sprintf("%d of %d", t.SprintDone, t.SprintTotal)), width, inspectorIndent))
	}
	// And which of them is being worked, on its own row under the set. It is
	// said here as well as on the item's own row because the list below shows
	// four of a backlog that may hold forty, and where a sprint is up to is
	// the one fact that must not depend on the current item having fitted.
	if t.SprintItem != "" {
		b.add(railRow(sty.Dim.Render("on")+" "+sty.Body.Render(t.SprintItem),
			sty.Dim.Render(t.SprintStage), width, inspectorIndent))
	}
	for _, row := range t.Rows {
		glyph, style := todoRowTone(row.State)
		left := glyph + " " + sty.Dim.Render(row.Priority+" "+row.Grade) + " " + style.Render(row.Slug)
		b.add(railRow(left, row.note(), width, inspectorIndent))
	}
	if t.More > 0 {
		b.add(indentRow(sty.Dim.Render(fmt.Sprintf("… %d more", t.More)), width))
	}
	if t.Hint != "" {
		b.add(indentRow(sty.Hint.Render(t.Hint), width))
	}
	return b, true
}

// note is the row's right-hand column: the stage or the wait in words, and —
// for an item being built in lanes — the meter that says how many of them
// have landed. The words come first because they are what the column is for;
// the meter is the count the words cannot carry.
func (r InspectorTodoRow) note() string {
	note := ""
	if r.Note != "" {
		note = sty.Dim.Render(r.Note)
		if r.Stale {
			note = sty.Warn.Render(r.Note)
		}
	}
	m, ok := AgentMeter(r.LanesDone, r.LanesTotal)
	if !ok {
		return note
	}
	m.Text = fmt.Sprintf("%d/%d", r.LanesDone, r.LanesTotal)
	if note == "" {
		return m.View()
	}
	return note + " " + m.View()
}

// todoRowTone is a backlog row's glyph and the weight its slug carries. The
// running one is bright for the same reason the running plan step is; a
// blocked one carries the error mark because it is waiting on a person.
func todoRowTone(s TodoRowState) (string, lipgloss.Style) {
	switch s {
	case TodoRunning:
		return sty.SpinText.Render("▸"), brightStyle()
	case TodoBlocked:
		return sty.Err.Render("!"), sty.Body
	case TodoWaiting:
		return sty.Dim.Render("·"), sty.Dim
	}
	return sty.Dim.Render("·"), sty.Body
}
