package components

// The rail's STEPS block: the session's own working list, as a count and the
// step it is on. It is a file of its own because it is not PLAN and must not
// read as it: PLAN is a checklist somebody approved, drawn step by step with
// a glyph each; STEPS is the agent's own account of where it is, drawn as the
// reading a child's lane gives — `n of m` and the step it is on — and nothing
// in it says a task is finished.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// InspectorSteps is the STEPS block: how far the session is through the list
// it declared itself.
type InspectorSteps struct {
	// Done and Total are the list's count. Total zero is no list, which the
	// host hands over as a nil block rather than as zero of zero.
	Done, Total int
	// Current is the first step not yet marked; empty once every step is.
	Current string
	// Age is how many tool rounds have passed since the agent last set the
	// list. It is drawn only from StaleListRounds on.
	Age int
}

// StaleListRounds is how many tool rounds a working list may stand unchanged
// before the surfaces that draw it say how old it is. A list being worked
// through moves every few rounds — a step is a handful of calls and the
// agent sends the list again as each one finishes — so a list that has not
// moved in twenty has stopped being kept, whatever its count says. Twenty is
// half the session's check-in interval of forty: the person sees a list go
// quiet well before the agent is next asked about it, and a fresh list,
// being worked, never shows an age at all.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
const StaleListRounds = 20

// listAge is how a list's age is said, and "" for a list younger than
// StaleListRounds, which says nothing about its age.
func listAge(age int) string {
	if age < StaleListRounds {
		return ""
	}
	return "unchanged " + plural(age, "round")
}

// stepsBlock is the STEPS reading. It takes PLAN's place in the rail's order
// because the two are never up together — while an approved plan is being
// executed the plan is the checklist, and the host sends no STEPS — and it
// answers PLAN's question, through what, one step at a time rather than as
// the whole list: the agent's list is its own and can change, so the row is
// where it is now, not a promise of what is left.
// See docs/interface/surfaces.md#the-inspector-rail.
func (r InspectorRail) stepsBlock(width int) (railBlock, bool) {
	s := r.Steps
	if s == nil || s.Total <= 0 {
		return railBlock{}, false
	}
	count := fmt.Sprintf("%d of %d", min(max(s.Done, 0), s.Total), s.Total)
	// The agent's own list is standing: it changes between turns, and is read
	// before the next send. Collapsed, its count is the heading's.
	b := railBlock{heading: railHeading("STEPS", "", sty.dim, width), class: railStanding,
		collapsedHeading: railHeading("STEPS", count, sty.dim, width)}
	parts := []string{sty.info.Render(count)}
	if s.Current != "" {
		parts = append(parts, sty.body.Render(s.Current))
	}
	row := strings.Join(parts, sty.dim.Render(" · "))
	// A list that has gone quiet says so, dim, after what it states: the
	// count of a list nobody has kept for twenty rounds looks exactly as
	// authoritative as a fresh one, and is not. The age rides the row where
	// the row has room for it whole, and takes a row of its own under it
	// where it does not, so it is never the thing a long step title clips;
	// collapsed, it joins the count in the heading while that fits and is
	// shed before the count is. Under the threshold nothing is drawn.
	// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.
	age := listAge(s.Age)
	if age == "" {
		b.rows = append(b.rows, railLine{text: indentRow(row, width), stated: true})
		return b, true
	}
	if heading := count + " · " + age; lipgloss.Width("STEPS "+heading) <= railRoom(width, "", inspectorIndent) {
		b.collapsedHeading = railHeading("STEPS", heading, sty.dim, width)
	}
	if aged := row + sty.dim.Render(" · "+age); lipgloss.Width(aged) <= railRoom(width, "", inspectorIndent) {
		b.rows = append(b.rows, railLine{text: indentRow(aged, width), stated: true})
		return b, true
	}
	b.rows = append(b.rows,
		railLine{text: indentRow(row, width), stated: true},
		railLine{text: indentRow(sty.dim.Render(age), width), stated: true})
	return b, true
}
